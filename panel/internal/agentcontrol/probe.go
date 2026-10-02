package agentcontrol

import (
	"context"
	"encoding/json"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/monitor"
)

func (s *Service) ProbeConnectionIDs() []int64 {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	ids := []int64{}
	for id, connection := range s.connections {
		if monitor.Supports(connection.Capabilities, "tcp") || monitor.Supports(connection.Capabilities, "icmp") {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Service) NotifyProbeTasks(serverID int64, probes *monitor.Service) error {
	s.connectionsMu.Lock()
	connection := s.connections[serverID]
	s.connectionsMu.Unlock()
	if connection == nil || !monitor.Supports(connection.Capabilities, "tcp") && !monitor.Supports(connection.Capabilities, "icmp") {
		return nil
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if !s.IsCurrentConnection(serverID, connection) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Read after taking writeMu: concurrent CRUD notifications cannot send an older
	// snapshot after a newer one. Versions are scoped to this WebSocket connection.
	tasks, err := probes.Desired(ctx, serverID, connection.Capabilities)
	if err != nil {
		return err
	}
	connection.probeVersion++
	payload, err := json.Marshal(monitor.DesiredTasks{Type: "probe_tasks", Version: connection.probeVersion, Tasks: tasks})
	if err != nil {
		return err
	}
	if err := connection.Socket.Write(ctx, websocket.MessageText, payload); err != nil {
		// Reconnection delivers the complete current list if a write was lost.
		connection.Socket.CloseNow()
		return err
	}
	return nil
}
