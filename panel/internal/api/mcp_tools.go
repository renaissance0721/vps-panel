package api

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type mcpListServersInput struct {
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Status string `json:"status,omitempty"`
}

type mcpHistoryInput struct {
	ServerID int64 `json:"server_id"`
	Hours    int   `json:"hours"`
}

type mcpRelayHealthInput struct {
	RelayID int64 `json:"relay_id,omitempty"`
	Limit   int   `json:"limit,omitempty"`
	Offset  int   `json:"offset,omitempty"`
}

func (s *server) registerMCPTools(ms *mcp.Server, user auth.User) {
	mcp.AddTool(ms, mcpDef("list_carpool_nodes", "Summarize carpool assignments for administrators."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		if user.Role != auth.RoleAdmin {
			return nil, nil, errors.New("access denied")
		}
		users, err := s.authService.ListUsers(ctx)
		if err != nil {
			return nil, nil, errors.New("user query failed")
		}
		out := []any{}
		for _, u := range users {
			if u.Role != auth.RoleCarpool {
				continue
			}
			assignments, err := s.carpoolAssignments(ctx, u.ID)
			if err != nil {
				return nil, nil, errors.New("assignment query failed")
			}
			direct, relay := 0, 0
			for _, a := range assignments {
				if a.SourceType == "proxy" {
					direct++
				} else if a.SourceType == "relay" {
					relay++
				}
			}
			out = append(out, map[string]any{"user_id": u.ID, "username": u.Username, "node_count": len(assignments), "direct_count": direct, "relay_count": relay})
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(out))
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"items": out[a:b], "total": len(out)}, nil
	})
	mcp.AddTool(ms, mcpDef("get_latency_history", "Summarize probe history for 1, 6 or 24 hours."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpHistoryInput) (*mcp.CallToolResult, any, error) {
		v, err := s.mcpVisibleServer(ctx, user, in.ServerID)
		if err != nil {
			return nil, nil, err
		}
		capabilities := map[string]bool{}
		for _, c := range v.AgentCapabilities {
			capabilities[c] = true
		}
		if in.Hours != 1 && in.Hours != 6 && in.Hours != 24 {
			return nil, nil, errors.New("hours must be 1, 6 or 24")
		}
		result, err := s.monitor.HistorySummary(ctx, v.ID, in.Hours, capabilities)
		if err != nil {
			return nil, nil, errors.New("history query failed")
		}
		return nil, result, nil
	})
	mcp.AddTool(ms, mcpDef("check_relay_health", "Analyze saved configuration; does not perform network probes."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpRelayHealthInput) (*mcp.CallToolResult, any, error) {
		if in.RelayID < 0 {
			return nil, nil, errors.New("invalid relay ID")
		}
		values, err := s.relays.List(ctx)
		if err != nil {
			return nil, nil, errors.New("relay query failed")
		}
		out := []any{}
		for _, v := range values {
			if in.RelayID != 0 && in.RelayID != v.ID {
				continue
			}
			if _, e := s.relayForUser(ctx, user, v.ID); e != nil {
				if errors.Is(e, relaystore.ErrNotFound) {
					continue
				}
				return nil, nil, errors.New("relay access check failed")
			}
			issues := []string{}
			if !v.Enabled {
				issues = append(issues, "disabled")
			}
			if !v.TargetAddressReady {
				issues = append(issues, "target_unavailable")
			}
			if v.EntryAddress == "" {
				issues = append(issues, "entry_unavailable")
			}
			source, e := s.mcpVisibleServer(ctx, user, v.ServerID)
			if e != nil {
				return nil, nil, e
			}
			if source.Status != "online" {
				issues = append(issues, "source_not_online")
			}
			if v.TargetProxyID != nil {
				p, e := s.proxyForUser(ctx, user, *v.TargetProxyID)
				if e == nil && !relaystore.NetworkCompatible(v.Network, p.Protocol) {
					issues = append(issues, "network_incompatible")
				}
			}
			out = append(out, map[string]any{"id": v.ID, "name": v.Name, "issues": issues, "network_reachability": "unknown"})
		}
		if in.RelayID != 0 && len(out) == 0 {
			return nil, nil, errors.New("relay not found")
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(out))
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"items": out[a:b], "total": len(out)}, nil
	})
	mcp.AddTool(ms, mcpDef("list_servers", "List visible servers, optionally filtering online or offline."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpListServersInput) (*mcp.CallToolResult, any, error) {
		if in.Status != "" && in.Status != "all" && in.Status != "online" && in.Status != "offline" {
			return nil, nil, errors.New("invalid status")
		}
		values, err := s.servers.ListForUser(ctx, user.ID)
		if err != nil {
			return nil, nil, errors.New("server query failed")
		}
		out := []any{}
		for _, v := range values {
			if in.Status != "" && in.Status != "all" && in.Status != v.Status {
				continue
			}
			out = append(out, mcpServerSummary(v))
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(out))
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"items": out[a:b], "total": len(out)}, nil
	})
	mcp.AddTool(ms, mcpDef("get_server_details", "Get visible server details by ID."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpServerID) (*mcp.CallToolResult, any, error) {
		v, err := s.mcpVisibleServer(ctx, user, in.ServerID)
		if err != nil {
			return nil, nil, err
		}
		out := mcpServerSummary(v)
		out["agent_version"] = v.AgentVersion
		out["last_seen_at"] = v.LastSeenAt
		if v.SystemInfo != nil {
			out["system_info"] = map[string]any{"os_name": v.SystemInfo.OSName, "os_version": v.SystemInfo.OSVersion, "arch": v.SystemInfo.Arch, "reported_at": v.SystemInfo.ReportedAt}
		}
		return nil, out, nil
	})
	mcp.AddTool(ms, mcpDef("get_server_metrics", "Get latest server metrics and freshness."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpServerID) (*mcp.CallToolResult, any, error) {
		v, err := s.mcpVisibleServer(ctx, user, in.ServerID)
		if err != nil {
			return nil, nil, err
		}
		out := map[string]any{"server_id": v.ID, "status": v.Status, "metrics_status": "unavailable", "network_speed": "unavailable"}
		if v.Metrics != nil {
			m := v.Metrics
			status := "available"
			if v.Status != "online" || time.Since(m.UpdatedAt) > 2*time.Minute {
				status = "stale"
			}
			out["metrics_status"] = status
			out["collected_at"] = m.UpdatedAt
			out["cpu_percent"] = m.CPUPercent
			out["memory_used_bytes"] = m.MemoryUsedBytes
			out["memory_total_bytes"] = m.MemoryTotalBytes
			if m.MemoryTotalBytes > 0 {
				out["memory_percent"] = 100 * float64(m.MemoryUsedBytes) / float64(m.MemoryTotalBytes)
			}
			out["disk_used_bytes"] = m.DiskUsedBytes
			out["disk_total_bytes"] = m.DiskTotalBytes
			if m.DiskTotalBytes > 0 {
				out["disk_percent"] = 100 * float64(m.DiskUsedBytes) / float64(m.DiskTotalBytes)
			}
			out["uptime_seconds"] = m.UptimeSeconds
			out["cycle_rx_bytes"] = m.CycleRXBytes
			out["cycle_tx_bytes"] = m.CycleTXBytes
			out["traffic_used_bytes"] = v.TrafficUsedBytes()
			out["cycle_started_at"] = m.CycleStartedAt
		}
		return nil, out, nil
	})
	mcp.AddTool(ms, mcpDef("list_proxies", "List visible proxy metadata without credentials."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		values, err := s.proxies.List(ctx)
		if err != nil {
			return nil, nil, errors.New("proxy query failed")
		}
		out := []any{}
		for _, v := range values {
			ok, e := s.canAccessServer(ctx, user, v.ServerID)
			if e != nil {
				return nil, nil, errors.New("access check failed")
			}
			if ok {
				out = append(out, map[string]any{"id": v.ID, "name": v.Name, "server_id": v.ServerID, "server_name": v.ServerName, "protocol": v.Protocol, "enabled": v.Enabled, "node_role": v.NodeRole, "updated_at": v.UpdatedAt})
			}
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(out))
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"items": out[a:b], "total": len(out)}, nil
	})
	mcp.AddTool(ms, mcpDef("list_relays", "List visible relay metadata without private targets."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		values, err := s.relays.List(ctx)
		if err != nil {
			return nil, nil, errors.New("relay query failed")
		}
		out := []any{}
		for _, v := range values {
			if _, e := s.relayForUser(ctx, user, v.ID); e != nil {
				if errors.Is(e, relaystore.ErrNotFound) {
					continue
				}
				return nil, nil, errors.New("relay access check failed")
			}
			out = append(out, map[string]any{"id": v.ID, "name": v.Name, "server_name": v.ServerName, "listen_port": v.ListenPort, "target_type": v.TargetType, "target_server_name": v.TargetServerName, "target_proxy_name": v.TargetProxyName, "enabled": v.Enabled, "target_address_ready": v.TargetAddressReady, "target_unavailable_reason": v.TargetUnavailableReason, "updated_at": v.UpdatedAt})
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(out))
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"items": out[a:b], "total": len(out)}, nil
	})
	mcp.AddTool(ms, mcpDef("list_subscriptions", "List the current user's personal subscription summaries."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		values, err := s.subscriptions.ListPersonalSubscriptions(ctx, subscriptionstore.PersonalSubscriptionActor{UserID: user.ID, Role: user.Role})
		if err != nil {
			return nil, nil, errors.New("subscription query failed")
		}
		out := []any{}
		for _, v := range values {
			out = append(out, map[string]any{"id": v.ID, "name": v.Name, "type": "personal", "enabled": v.Enabled, "node_count": len(v.Nodes), "updated_at": v.UpdatedAt})
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(out))
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"items": out[a:b], "total": len(out)}, nil
	})
	mcp.AddTool(ms, mcpDef("get_panel_info", "Identify the Panel and current authorization."), func(_ context.Context, _ *mcp.CallToolRequest, _ mcpEmpty) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"panel_version": s.panelVersion, "scope": "read", "user": map[string]any{"id": user.ID, "username": user.Username, "role": user.Role}}, nil
	})
}
