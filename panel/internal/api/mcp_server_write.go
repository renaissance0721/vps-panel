package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

type mcpCreateServer struct {
	Name                     string  `json:"name"`
	Visibility               string  `json:"visibility,omitempty"`
	UserIDs                  []int64 `json:"user_ids,omitempty"`
	BandwidthMbps            *int64  `json:"bandwidth_mbps,omitempty"`
	NetworkStack             *string `json:"network_stack,omitempty"`
	BoundDomainIPv4          string  `json:"bound_domain_ipv4,omitempty"`
	BoundDomainIPv6          string  `json:"bound_domain_ipv6,omitempty"`
	ExpiresAt                string  `json:"expires_at,omitempty"`
	RenewalPeriodMonths      *int    `json:"renewal_period_months,omitempty"`
	AutoRenew                bool    `json:"auto_renew,omitempty"`
	MonthlyTrafficLimitBytes *int64  `json:"monthly_traffic_limit_bytes,omitempty"`
}
type mcpUpdateServer struct {
	ServerID                 int64   `json:"server_id"`
	Name                     string  `json:"name,omitempty"`
	BandwidthMbps            *int64  `json:"bandwidth_mbps,omitempty"`
	NetworkStack             *string `json:"network_stack,omitempty"`
	CountryOverrideCode      *string `json:"country_override_code,omitempty"`
	BoundDomainIPv4          *string `json:"bound_domain_ipv4,omitempty"`
	BoundDomainIPv6          *string `json:"bound_domain_ipv6,omitempty"`
	OutboundPreference       *string `json:"outbound_preference,omitempty"`
	BlockChinaInbound        *bool   `json:"block_china_inbound,omitempty"`
	ExpiresAt                *string `json:"expires_at,omitempty"`
	RenewalPeriodMonths      *int    `json:"renewal_period_months,omitempty"`
	AutoRenew                *bool   `json:"auto_renew,omitempty"`
	MonthlyTrafficLimitBytes *int64  `json:"monthly_traffic_limit_bytes,omitempty"`
	TrafficCountMode         *string `json:"traffic_count_mode,omitempty"`
	TrafficResetDay          *int    `json:"traffic_reset_day,omitempty"`
	TrafficResetTime         *string `json:"traffic_reset_time,omitempty"`
	DryRun                   bool    `json:"dry_run,omitempty"`
}

func (s *server) registerMCPServerWrites(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpWriteDef("create_server", "Create a server record. Enrollment credentials are not returned; use the Panel to enroll its Agent.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpCreateServer) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		var expires *time.Time
		if in.ExpiresAt != "" {
			raw, _ := json.Marshal(in.ExpiresAt)
			var err error
			expires, err = parseServerExpiration(raw)
			if err != nil {
				return nil, nil, errors.New("invalid expiration date; use YYYY-MM-DD")
			}
		}
		s.probeMu.Lock()
		created, err := s.servers.CreateWithSettings(ctx, serverstore.CreateServerInput{Name: in.Name, Visibility: in.Visibility, UserIDs: in.UserIDs, CreatorID: identity.user.ID, BandwidthMbps: in.BandwidthMbps, NetworkStack: in.NetworkStack, BoundDomainIPv4: in.BoundDomainIPv4, BoundDomainIPv6: in.BoundDomainIPv6, ExpiresAt: expires, RenewalPeriodMonths: in.RenewalPeriodMonths, AutoRenew: in.AutoRenew, MonthlyTrafficLimitBytes: in.MonthlyTrafficLimitBytes})
		s.probeMu.Unlock()
		if err != nil {
			return nil, nil, errors.New("server creation rejected: check name and access settings")
		}
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://localhost/internal", nil)
		s.recordAudit(req, identity.user, "server.create", "server", created.ID, "通过 MCP 创建服务器")
		return nil, map[string]any{"saved": true, "server": mcpServerSummary(created.Server), "agent_enrollment": "not enrolled; generate an installation command in the Panel"}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("update_server", "Update one server setting group (name, network, domains, expiry, traffic, or outbound). dry_run previews renaming only.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUpdateServer) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.mcpVisibleServer(ctx, identity.user, in.ServerID)
		if err != nil {
			return nil, nil, err
		}
		if err = s.servers.EnsureMutable(ctx, in.ServerID); err != nil {
			return nil, nil, errors.New("server unavailable")
		}
		changes := map[string]any{}
		if in.Name != "" {
			changes["name"] = in.Name
		}
		if in.BandwidthMbps != nil {
			changes["bandwidth_mbps"] = *in.BandwidthMbps
		}
		if in.NetworkStack != nil {
			changes["network_stack"] = *in.NetworkStack
		}
		if in.CountryOverrideCode != nil {
			changes["country_override_code"] = *in.CountryOverrideCode
		}
		if in.BoundDomainIPv4 != nil {
			changes["bound_domain_ipv4"] = *in.BoundDomainIPv4
		}
		if in.BoundDomainIPv6 != nil {
			changes["bound_domain_ipv6"] = *in.BoundDomainIPv6
		}
		if in.OutboundPreference != nil {
			changes["outbound_preference"] = *in.OutboundPreference
		}
		if in.BlockChinaInbound != nil {
			changes["block_china_inbound"] = *in.BlockChinaInbound
		}
		if in.ExpiresAt != nil {
			changes["expires_at"] = *in.ExpiresAt
		}
		if in.RenewalPeriodMonths != nil {
			changes["renewal_period_months"] = *in.RenewalPeriodMonths
		}
		if in.AutoRenew != nil {
			changes["auto_renew"] = *in.AutoRenew
		}
		if in.MonthlyTrafficLimitBytes != nil {
			changes["monthly_traffic_limit_bytes"] = *in.MonthlyTrafficLimitBytes
		}
		if in.TrafficCountMode != nil {
			changes["traffic_count_mode"] = *in.TrafficCountMode
		}
		if in.TrafficResetDay != nil {
			changes["traffic_reset_day"] = *in.TrafficResetDay
		}
		if in.TrafficResetTime != nil {
			changes["traffic_reset_time"] = *in.TrafficResetTime
		}
		if len(changes) == 0 {
			return nil, nil, errors.New("no server setting provided")
		}
		if in.DryRun {
			if len(changes) != 1 || in.Name == "" {
				return nil, nil, errors.New("dry_run is supported for renaming only")
			}
			name, err := serverstore.NormalizeName(in.Name)
			if err != nil {
				return nil, nil, errors.New("invalid server name")
			}
			return nil, map[string]any{"dry_run": true, "server_id": v.ID, "before": v.Name, "after": name, "agent_sync_required": false}, nil
		}
		result, err := s.mcpInvoke(ctx, identity.user, "PATCH", v.ID, changes, s.updateServerExpiration)
		if err != nil {
			return nil, nil, err
		}
		out := map[string]any{"saved": true, "server": mcpPublicEntity(result["server"], "id", "name", "status", "bandwidth_mbps", "network_stack", "country_override_code"), "agent_sync_required": in.OutboundPreference != nil || in.BlockChinaInbound != nil}
		if in.OutboundPreference != nil || in.BlockChinaInbound != nil {
			out["agent_sync"] = s.mcpSync(ctx, identity.user, v.ID)
		}
		return nil, out, nil
	})
}
