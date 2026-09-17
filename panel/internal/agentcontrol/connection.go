package agentcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/version"
)

type Connection struct {
	Socket  *websocket.Conn
	Version string
	writeMu sync.Mutex
}
type agentConfigChangedMessage struct {
	Type    string `json:"type"`
	Version int64  `json:"version"`
}

type agentUpgradeMessage struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}

func (s *Service) TrackConnection(serverID int64, connection *Connection) *Connection {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	previous := s.connections[serverID]
	s.connections[serverID] = connection
	return previous
}

func (s *Service) UntrackConnection(serverID int64, connection *Connection) bool {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if s.connections[serverID] != connection {
		return false
	}
	delete(s.connections, serverID)
	return true
}

func (s *Service) IsCurrentConnection(serverID int64, connection *Connection) bool {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	return s.connections[serverID] == connection
}

func (s *Service) DisconnectCurrent(serverID int64, connection *Connection) (bool, error) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if s.connections[serverID] != connection {
		return false, nil
	}
	delete(s.connections, serverID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return true, s.SetAgentOffline(ctx, serverID)
}

func (s *Service) NotifyConfigChanged(serverID, version int64) error {
	if version <= 0 {
		return errors.New("invalid desired state version")
	}
	s.connectionsMu.Lock()
	connection := s.connections[serverID]
	s.connectionsMu.Unlock()
	if connection == nil {
		return nil
	}

	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	s.connectionsMu.Lock()
	current := s.connections[serverID] == connection
	s.connectionsMu.Unlock()
	if !current {
		return nil
	}
	payload, err := json.Marshal(agentConfigChangedMessage{Type: "config_changed", Version: version})
	if err != nil {
		return fmt.Errorf("encode Agent config notification: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connection.Socket.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("notify Agent config changed: %w", err)
	}
	return nil
}

func (s *Service) NotifyAgentUpgrade(serverID int64, targetVersion string) error {
	s.connectionsMu.Lock()
	connection := s.connections[serverID]
	s.connectionsMu.Unlock()
	if connection == nil {
		return ErrAgentOffline
	}
	payload, err := json.Marshal(agentUpgradeMessage{Type: "agent_upgrade", Version: targetVersion})
	if err != nil {
		return fmt.Errorf("encode Agent upgrade notification: %w", err)
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if !s.IsCurrentConnection(serverID, connection) {
		return ErrAgentOffline
	}
	comparison, ok := version.Compare(connection.Version, targetVersion)
	if !ok {
		return ErrUnknownAgentVersion
	}
	if comparison == 0 {
		return ErrAgentAlreadyCurrent
	}
	if comparison > 0 {
		return fmt.Errorf("%w: Agent %s is newer than Panel %s", ErrAgentNewer, connection.Version, targetVersion)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connection.Socket.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("notify Agent upgrade: %w", err)
	}
	return nil
}

func (s *Service) CloseConnections(serverID int64) {
	s.connectionsMu.Lock()
	connection := s.connections[serverID]
	delete(s.connections, serverID)
	s.connectionsMu.Unlock()
	if connection != nil {
		connection.Socket.CloseNow()
	}
}

// WithCurrentConnection keeps replacement/disconnection serialized with a report.
func (s *Service) WithCurrentConnection(serverID int64, connection *Connection, report func(context.Context) error) (bool, error) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if s.connections[serverID] != connection {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return true, report(ctx)
}
