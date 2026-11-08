package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
)

type mcpResourceID struct {
	ID int64 `json:"id"`
}
type mcpToggle struct {
	ID      int64 `json:"id"`
	Enabled bool  `json:"enabled"`
}
type mcpDelete struct {
	ID           int64  `json:"id"`
	ExpectedName string `json:"expected_name"`
}

func mcpWriteDef(name, description string, destructive bool) *mcp.Tool {
	return &mcp.Tool{Name: name, Description: description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive}}
}
func (s *server) requireMCPWrite(ctx context.Context, identity mcpIdentity) error {
	var allowed bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mcp_tokens t JOIN users u ON u.id=t.owner_user_id JOIN mcp_settings m ON m.id=1 WHERE t.id=? AND u.id=? AND u.role='admin' AND t.access_scope='read_write' AND t.revoked_at IS NULL AND t.expires_at>? AND m.enabled=1 AND m.write_enabled=1)`, identity.tokenID, identity.user.ID, time.Now().Unix()).Scan(&allowed)
	if err != nil || !allowed {
		return errors.New("read_write token and enabled MCP write access required")
	}
	return nil
}

// Each MCP tool invokes a fixed existing business handler. No route or method is
// supplied by the client, and the handler performs the same ACL and validation.
type mcpResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *mcpResponseWriter) Header() http.Header { return w.header }
func (w *mcpResponseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *mcpResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(p)
}

type mcpBusinessHandler func(http.ResponseWriter, *http.Request, auth.User)

func (s *server) mcpInvoke(ctx context.Context, user auth.User, method string, id int64, body any, handler mcpBusinessHandler) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("invalid request")
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost/internal", bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("invalid request")
	}
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	req.Header.Set("Content-Type", "application/json")
	response := &mcpResponseWriter{header: make(http.Header)}
	handler(response, req, user)
	if response.status >= 500 {
		return nil, errors.New("operation outcome uncertain: check resource state before retrying")
	}
	if response.status >= 400 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(response.body.Bytes(), &v)
		if v.Error == "" {
			v.Error = "operation rejected"
		}
		return nil, errors.New(v.Error)
	}
	if response.status == http.StatusNoContent {
		return map[string]any{}, nil
	}
	var result map[string]any
	if json.Unmarshal(response.body.Bytes(), &result) != nil {
		return nil, errors.New("operation outcome uncertain: invalid response")
	}
	return result, nil
}
func mcpPublicEntity(raw any, keys ...string) map[string]any {
	src, _ := raw.(map[string]any)
	out := map[string]any{}
	for _, key := range keys {
		if v, ok := src[key]; ok {
			out[key] = v
		}
	}
	return out
}
func (s *server) mcpSync(ctx context.Context, user auth.User, serverID int64) map[string]any {
	v, err := s.mcpVisibleServer(ctx, user, serverID)
	if err != nil {
		return map[string]any{"status": "unknown"}
	}
	status := v.AgentConfigSyncStatus
	if status == "" {
		status = "unknown"
	}
	if status == "success" && v.AgentAppliedConfigVersion < v.DesiredStateVersion {
		status = "pending"
	}
	return map[string]any{"status": status, "desired_version": v.DesiredStateVersion, "applied_version": v.AgentAppliedConfigVersion, "network_reachability": "unknown", "scope": "latest_server_configuration"}
}
func (s *server) mcpWriteHandler(ctx context.Context, identity mcpIdentity, method string, id, serverID int64, body any, handler mcpBusinessHandler, entity string) (any, error) {
	if err := s.requireMCPWrite(ctx, identity); err != nil {
		return nil, err
	}
	result, err := s.mcpInvoke(ctx, identity.user, method, id, body, handler)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"saved": true, "agent_sync": s.mcpSync(ctx, identity.user, serverID)}
	if entity == "proxy" {
		out["dependent_server_sync"] = "not_checked"
	}
	if entity != "" {
		out[entity] = mcpPublicEntity(result[entity], "id", "server_id", "name", "protocol", "listen_port", "enabled", "network", "target_type", "target_address_ready")
	}
	return out, nil
}
func (s *server) registerMCPWriteTools(ms *mcp.Server, identity mcpIdentity) {
	s.registerMCPServerWrites(ms, identity)
	s.registerMCPProxyTools(ms, identity)
	s.registerMCPRelayWrites(ms, identity)
	s.registerMCPOrders(ms, identity)
}
