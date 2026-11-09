package api

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

type mcpServerAccessUpdate struct {
	ServerID   int64   `json:"server_id"`
	Visibility string  `json:"visibility"`
	UserIDs    []int64 `json:"user_ids"`
}
type mcpTrafficTarget struct {
	ServerID        int64 `json:"server_id"`
	TargetUsedBytes int64 `json:"target_used_bytes"`
}

func mcpTrafficSummary(v serverstore.Server) map[string]any {
	out := map[string]any{"server_id": v.ID, "traffic_used_bytes": nil, "monthly_traffic_limit_bytes": v.MonthlyTrafficLimitBytes, "traffic_count_mode": v.TrafficCountMode, "traffic_reset_day": v.TrafficResetDay, "traffic_reset_time": v.TrafficResetTime, "metrics_status": "unavailable"}
	if v.Metrics != nil {
		m := v.Metrics
		status := "available"
		if m.CycleStartedAt == nil {
			status = "unavailable"
		} else if v.Status != "online" || time.Since(m.UpdatedAt) > 2*time.Minute {
			status = "stale"
		}
		out["metrics_status"] = status
		out["traffic_used_bytes"] = v.TrafficUsedBytes()
		out["cycle_rx_bytes"] = m.CycleRXBytes
		out["cycle_tx_bytes"] = m.CycleTXBytes
		out["traffic_adjustment_bytes"] = m.TrafficAdjustmentBytes
		out["cycle_started_at"] = m.CycleStartedAt
		out["metrics_updated_at"] = m.UpdatedAt
	}
	return out
}
func mcpRedactedAgentError(v string) string {
	if v != "" {
		return "error recorded; inspect the Panel for details"
	}
	return ""
}

func (s *server) registerMCPP0ServerTools(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("get_agent_status", "Get current Agent metadata, upgrade and configuration synchronization status."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpServerID) (*mcp.CallToolResult, any, error) {
		v, err := s.mcpVisibleServer(ctx, identity.user, in.ServerID)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"server_id": v.ID, "status": v.Status, "implementation": v.AgentImplementation, "version": v.AgentVersion, "api_version": v.AgentAPIVersion, "capabilities": v.AgentCapabilities, "last_seen_at": v.LastSeenAt, "self_upgrade_supported": v.AgentVersion != "" && agentcontrol.CanSelfUpgrade(v.AgentImplementation, v.AgentAPIVersion, v.AgentCapabilities), "upgrade_target": v.AgentUpgradeTarget, "upgrade_status": v.AgentUpgradeStatus, "upgrade_error": mcpRedactedAgentError(v.AgentUpgradeError), "desired_version": v.DesiredStateVersion, "applied_version": v.AgentAppliedConfigVersion, "config_sync_status": v.AgentConfigSyncStatus, "config_sync_error": mcpRedactedAgentError(v.AgentConfigSyncError), "config_synced_at": v.AgentConfigSyncedAt}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("create_agent_enrollment", "Create sensitive one-time Agent enrollment command for one server using the configured public Panel domain.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpServerID) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		if !validPanelDomain(s.backup.Domain) {
			return nil, nil, errors.New("a trusted configured public Panel domain is required")
		}
		out, err := s.mcpInvoke(ctx, identity.user, "POST", in.ServerID, nil, s.createEnrollment)
		if err != nil {
			return nil, nil, err
		}
		s.mcpAuditAction(ctx, identity, "agent.enrollment.create", "server", in.ServerID)
		return nil, map[string]any{"server_id": in.ServerID, "enrollment_token_expires_at": out["enrollment_token_expires_at"], "agent_installation_command": out["agent_installation_command"]}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("upgrade_server_agent", "Request an official Agent upgrade; upgrading is not completion. Recheck get_agent_status.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpServerID) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInvoke(ctx, identity.user, "POST", in.ServerID, nil, s.upgradeAgent)
		if err != nil {
			return nil, nil, err
		}
		s.mcpAuditAction(ctx, identity, "agent.upgrade", "server", in.ServerID)
		out["server_id"] = in.ServerID
		return nil, out, nil
	})
	mcp.AddTool(ms, mcpDef("get_server_access", "Get a visible Server's visibility and authorized user IDs."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpServerID) (*mcp.CallToolResult, any, error) {
		v, err := s.mcpVisibleServer(ctx, identity.user, in.ServerID)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"server_id": v.ID, "visibility": v.Visibility, "user_ids": v.AccessUserIDs}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("update_server_access", "Update one Server's visibility and complete authorized user list.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpServerAccessUpdate) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		body := map[string]any{"visibility": in.Visibility, "user_ids": in.UserIDs}
		_, err := s.mcpInvoke(ctx, identity.user, "PATCH", in.ServerID, body, s.updateServerAccess)
		if err != nil {
			return nil, nil, err
		}
		v, err := s.mcpVisibleServer(ctx, identity.user, in.ServerID)
		if err != nil {
			return nil, nil, errors.New("saved but access could not be re-read")
		}
		return nil, map[string]any{"saved": true, "server_id": v.ID, "visibility": v.Visibility, "user_ids": v.AccessUserIDs}, nil
	})
	mcp.AddTool(ms, mcpDef("get_server_traffic", "Get a Server's traffic accounting and metric freshness."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpServerID) (*mcp.CallToolResult, any, error) {
		v, err := s.mcpVisibleServer(ctx, identity.user, in.ServerID)
		if err != nil {
			return nil, nil, err
		}
		return nil, mcpTrafficSummary(v), nil
	})
	writeTraffic := func(ctx context.Context, id int64, method string, body any, handler mcpBusinessHandler) (any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, err
		}
		before, err := s.mcpVisibleServer(ctx, identity.user, id)
		if err != nil {
			return nil, err
		}
		_, err = s.mcpInvoke(ctx, identity.user, method, id, body, handler)
		if err != nil {
			return nil, err
		}
		after, err := s.mcpVisibleServer(ctx, identity.user, id)
		if err != nil {
			return nil, errors.New("saved but traffic could not be re-read")
		}
		return map[string]any{"saved": true, "server_id": id, "before": mcpTrafficSummary(before), "after": mcpTrafficSummary(after)}, nil
	}
	mcp.AddTool(ms, mcpWriteDef("set_server_traffic_adjustment", "Calibrate displayed server usage to target_used_bytes; this is not the monthly traffic limit.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpTrafficTarget) (*mcp.CallToolResult, any, error) {
		out, err := writeTraffic(ctx, in.ServerID, "PATCH", map[string]any{"target_used_bytes": in.TargetUsedBytes}, s.updateTrafficAdjustment)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("clear_server_traffic_adjustment", "Clear a Server's traffic adjustment without resetting Agent counters.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpServerID) (*mcp.CallToolResult, any, error) {
		out, err := writeTraffic(ctx, in.ServerID, "DELETE", nil, s.clearTrafficAdjustment)
		return nil, out, err
	})
}
