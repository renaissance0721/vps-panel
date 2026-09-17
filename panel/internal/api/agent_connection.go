package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func (s *server) agentWebSocket(w http.ResponseWriter, r *http.Request) {
	agent, _, ok := s.authenticateAgentRequest(w, r)
	if !ok {
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(8 << 10)
	currentConnection := &agentcontrol.Connection{
		Socket:  connection,
		Version: strings.TrimSpace(r.Header.Get("X-VPS-Panel-Agent-Version")),
	}
	previous := s.agents.TrackConnection(agent.ServerID, currentConnection)
	if previous != nil {
		previous.Socket.CloseNow()
	}

	statusContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = s.agents.SetAgentConnectedVersion(
		statusContext, agent.ID, agent.ServerID, r.Header.Get("X-VPS-Panel-Agent-Version"),
	)
	cancel()
	if err != nil {
		s.agents.UntrackConnection(agent.ServerID, currentConnection)
		if errors.Is(err, agentcontrol.ErrArchived) || errors.Is(err, agentcontrol.ErrServerNotFound) {
			return
		}
		log.Printf("set agent %d server %d online: %v", agent.ID, agent.ServerID, err)
		_ = connection.Close(websocket.StatusInternalError, "server status update failed")
		return
	}
	log.Printf("agent %d connected to server %d", agent.ID, agent.ServerID)

	for {
		messageType, message, readErr := connection.Read(r.Context())
		if readErr != nil {
			break
		}
		if !s.agents.IsCurrentConnection(agent.ServerID, currentConnection) {
			return
		}
		var payload struct {
			Type string `json:"type"`
		}
		if messageType != websocket.MessageText || json.Unmarshal(message, &payload) != nil {
			_ = connection.Close(websocket.StatusPolicyViolation, "invalid Agent message")
			break
		}
		validMessage := true
		switch payload.Type {
		case "heartbeat":
			heartbeatContext, cancelHeartbeat := context.WithTimeout(context.Background(), 5*time.Second)
			err = s.agents.TouchAgent(heartbeatContext, agent.ID, agent.ServerID)
			cancelHeartbeat()
			if err != nil {
				if !errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
					log.Printf("update agent %d heartbeat for server %d: %v", agent.ID, agent.ServerID, err)
				}
				validMessage = false
			}
		case "system_info":
			var systemInfo agentSystemInfoMessage
			if json.Unmarshal(message, &systemInfo) != nil {
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid system information")
				validMessage = false
				break
			}
			current, publicIPv4Changed, reportErr := s.reportCurrentSystemInfo(agent.ServerID, agent.ID, currentConnection, serverstore.SystemInfoReport{
				Hostname:   systemInfo.Hostname,
				OSName:     systemInfo.OSName,
				OSVersion:  systemInfo.OSVersion,
				Kernel:     systemInfo.Kernel,
				Arch:       systemInfo.Arch,
				IPv4:       systemInfo.IPv4,
				IPv6:       systemInfo.IPv6,
				PublicIPv4: systemInfo.PublicIPv4,
			})
			if !current {
				return
			}
			if reportErr == nil && publicIPv4Changed {
				mutations, dependencyErr := s.relays.BumpForAutoTargetServer(r.Context(), agent.ServerID)
				if dependencyErr != nil {
					log.Printf("update Relay dependencies for server %d public IPv4: %v", agent.ServerID, dependencyErr)
				} else {
					s.notifyRelayMutations(mutations)
				}
			}
			if reportErr != nil {
				if !errors.Is(reportErr, agentcontrol.ErrInvalidAgentToken) &&
					!errors.Is(reportErr, serverstore.ErrInvalidSystemInfo) {
					log.Printf("update system information for agent %d server %d: %v", agent.ID, agent.ServerID, reportErr)
				}
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid system information")
				validMessage = false
			}
		case "metrics":
			var metrics agentMetricsMessage
			if json.Unmarshal(message, &metrics) != nil {
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid metrics")
				validMessage = false
				break
			}
			if (metrics.NICRXBytes == nil) != (metrics.NICTXBytes == nil) {
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid metrics")
				validMessage = false
				break
			}
			var nicRXBytes, nicTXBytes int64
			if metrics.NICRXBytes != nil {
				nicRXBytes = *metrics.NICRXBytes
				nicTXBytes = *metrics.NICTXBytes
			}
			current, reportErr := s.reportCurrentMetrics(agent.ServerID, agent.ID, currentConnection, serverstore.MetricsReport{
				CPUPercent:       metrics.CPUPercent,
				MemoryUsedBytes:  metrics.MemoryUsedBytes,
				MemoryTotalBytes: metrics.MemoryTotalBytes,
				DiskUsedBytes:    metrics.DiskUsedBytes,
				DiskTotalBytes:   metrics.DiskTotalBytes,
				UptimeSeconds:    metrics.UptimeSeconds,
				HasNetworkUsage:  metrics.NICRXBytes != nil,
				NICRXBytes:       nicRXBytes,
				NICTXBytes:       nicTXBytes,
			})
			if !current {
				return
			}
			if reportErr != nil {
				if !errors.Is(reportErr, agentcontrol.ErrInvalidAgentToken) &&
					!errors.Is(reportErr, serverstore.ErrInvalidMetrics) {
					log.Printf("update metrics for agent %d server %d: %v", agent.ID, agent.ServerID, reportErr)
				}
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid metrics")
				validMessage = false
			}
		default:
			_ = connection.Close(websocket.StatusPolicyViolation, "unknown Agent message")
			validMessage = false
		}
		if !validMessage {
			break
		}
	}

	current, err := s.agents.DisconnectCurrent(agent.ServerID, currentConnection)
	if !current {
		return
	}
	if err != nil {
		if errors.Is(err, agentcontrol.ErrServerNotFound) {
			return
		}
		log.Printf("set agent %d server %d offline: %v", agent.ID, agent.ServerID, err)
		return
	}
	log.Printf("agent %d disconnected from server %d", agent.ID, agent.ServerID)
}

func (s *server) reportCurrentSystemInfo(serverID, agentID int64, connection *agentcontrol.Connection, report serverstore.SystemInfoReport) (bool, bool, error) {
	var changed bool
	current, err := s.agents.WithCurrentConnection(serverID, connection, func(ctx context.Context) error {
		var err error
		changed, err = s.servers.ReportSystemInfo(ctx, agentID, serverID, report)
		return err
	})
	return current, changed, err
}

func (s *server) reportCurrentMetrics(serverID, agentID int64, connection *agentcontrol.Connection, report serverstore.MetricsReport) (bool, error) {
	return s.agents.WithCurrentConnection(serverID, connection, func(ctx context.Context) error {
		return s.servers.ReportMetrics(ctx, agentID, serverID, report)
	})
}
