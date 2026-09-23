package agentcontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/diagnostic"
)

type diagnosticResponse struct {
	result diagnostic.Result
	err    error
}

type DiagnosticStatus struct {
	DesiredVersion int64
	AppliedVersion int64
	SyncStatus     string
	SyncError      string
}

func (s *Service) GetDiagnosticStatus(ctx context.Context, serverID int64) (DiagnosticStatus, error) {
	var value DiagnosticStatus
	var applied sql.NullInt64
	var status, syncError sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT servers.desired_state_version, agents.applied_config_version,
		 agents.config_sync_status, agents.config_sync_error
		 FROM servers LEFT JOIN agents ON agents.server_id = servers.id
		 WHERE servers.id = ? AND servers.archived_at IS NULL`, serverID,
	).Scan(&value.DesiredVersion, &applied, &status, &syncError)
	if errors.Is(err, sql.ErrNoRows) {
		return DiagnosticStatus{}, ErrServerNotFound
	}
	if err != nil {
		return DiagnosticStatus{}, fmt.Errorf("read diagnostic config status: %w", err)
	}
	if !applied.Valid {
		return DiagnosticStatus{}, ErrAgentNotRegistered
	}
	value.AppliedVersion = applied.Int64
	value.SyncStatus = status.String
	value.SyncError = syncError.String
	return value, nil
}

func (s *Service) RequestDiagnostics(ctx context.Context, serverID int64) (diagnostic.Result, error) {
	s.connectionsMu.Lock()
	connection := s.connections[serverID]
	if connection == nil {
		s.connectionsMu.Unlock()
		return diagnostic.Result{}, ErrAgentOffline
	}
	if !connection.Capabilities[diagnostic.CapabilityV1] {
		s.connectionsMu.Unlock()
		return diagnostic.Result{}, ErrDiagnosticsUnsupported
	}
	requestID, err := diagnostic.NewRequestID()
	if err != nil {
		s.connectionsMu.Unlock()
		return diagnostic.Result{}, fmt.Errorf("generate diagnostic request ID: %w", err)
	}
	response := make(chan diagnosticResponse, 1)
	if err := connection.registerDiagnostic(requestID, response); err != nil {
		s.connectionsMu.Unlock()
		return diagnostic.Result{}, err
	}
	s.connectionsMu.Unlock()
	defer connection.removeDiagnostic(requestID)

	payload, err := json.Marshal(diagnostic.Request{Type: "diagnostic_request", RequestID: requestID})
	if err != nil {
		return diagnostic.Result{}, fmt.Errorf("encode diagnostic request: %w", err)
	}
	connection.writeMu.Lock()
	if !s.IsCurrentConnection(serverID, connection) {
		connection.writeMu.Unlock()
		return diagnostic.Result{}, ErrAgentOffline
	}
	err = connection.Socket.Write(ctx, websocket.MessageText, payload)
	connection.writeMu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			return diagnostic.Result{}, ctx.Err()
		}
		return diagnostic.Result{}, fmt.Errorf("%w: send diagnostic request: %v", ErrAgentOffline, err)
	}

	select {
	case <-ctx.Done():
		return diagnostic.Result{}, ctx.Err()
	case result := <-response:
		return result.result, result.err
	}
}

func (s *Service) ResolveDiagnostics(connection *Connection, result diagnostic.Result) bool {
	return connection.resolveDiagnostic(result)
}

func ParseCapabilities(value string) map[string]bool {
	normalized, err := ParseCapabilityHeader(value)
	if err != nil {
		return map[string]bool{}
	}
	capabilities := make(map[string]bool)
	for _, capability := range normalized {
		capabilities[capability] = true
	}
	return capabilities
}

func splitCapabilities(value string) []string {
	values := make([]string, 0)
	start := 0
	for index := 0; index <= len(value); index++ {
		if index != len(value) && value[index] != ',' {
			continue
		}
		capability := value[start:index]
		start = index + 1
		for len(capability) > 0 && (capability[0] == ' ' || capability[0] == '\t') {
			capability = capability[1:]
		}
		for len(capability) > 0 && (capability[len(capability)-1] == ' ' || capability[len(capability)-1] == '\t') {
			capability = capability[:len(capability)-1]
		}
		if capability != "" {
			values = append(values, capability)
		}
	}
	return values
}

func (connection *Connection) registerDiagnostic(requestID string, response chan diagnosticResponse) error {
	connection.diagnosticsMu.Lock()
	defer connection.diagnosticsMu.Unlock()
	if len(connection.pendingDiagnostics) != 0 {
		return ErrDiagnosticsInProgress
	}
	if connection.pendingDiagnostics == nil {
		connection.pendingDiagnostics = make(map[string]chan diagnosticResponse)
	}
	if _, exists := connection.pendingDiagnostics[requestID]; exists {
		return diagnostic.ErrInvalidRequestID
	}
	connection.pendingDiagnostics[requestID] = response
	return nil
}

func (connection *Connection) removeDiagnostic(requestID string) {
	connection.diagnosticsMu.Lock()
	delete(connection.pendingDiagnostics, requestID)
	connection.diagnosticsMu.Unlock()
}

func (connection *Connection) resolveDiagnostic(result diagnostic.Result) bool {
	connection.diagnosticsMu.Lock()
	response := connection.pendingDiagnostics[result.RequestID]
	if response != nil {
		delete(connection.pendingDiagnostics, result.RequestID)
	}
	connection.diagnosticsMu.Unlock()
	if response == nil {
		return false
	}
	response <- diagnosticResponse{result: result}
	return true
}

func (connection *Connection) failPendingDiagnostics(err error) {
	connection.diagnosticsMu.Lock()
	pending := connection.pendingDiagnostics
	connection.pendingDiagnostics = nil
	connection.diagnosticsMu.Unlock()
	for _, response := range pending {
		response <- diagnosticResponse{err: err}
	}
}
