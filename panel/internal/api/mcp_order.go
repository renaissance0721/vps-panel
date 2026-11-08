package api

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/listorder"
)

type mcpProxyOrder struct {
	ProxyIDs []int64 `json:"proxy_ids"`
	DryRun   bool    `json:"dry_run,omitempty"`
}
type mcpRelayOrder struct {
	RelayIDs []int64 `json:"relay_ids"`
	DryRun   bool    `json:"dry_run,omitempty"`
}
type mcpLandingOrder struct {
	LandingIDs []int64 `json:"landing_ids"`
	DryRun     bool    `json:"dry_run,omitempty"`
}
type mcpUserOrder struct {
	UserIDs []int64 `json:"user_ids"`
	DryRun  bool    `json:"dry_run,omitempty"`
}
type mcpPersonalSubscriptionOrder struct {
	SubscriptionIDs []int64 `json:"subscription_ids"`
	DryRun          bool    `json:"dry_run,omitempty"`
}
type mcpPublishedNodeOrder struct {
	NodeIDs []int64 `json:"node_ids"`
	DryRun  bool    `json:"dry_run,omitempty"`
}

func (s *server) mcpSetOrder(ctx context.Context, identity mcpIdentity, kind listorder.Kind, field string, ids []int64, dryRun bool) (any, error) {
	if err := s.requireMCPWrite(ctx, identity); err != nil {
		return nil, err
	}
	result, err := s.orders.SetOrder(ctx, identity.user.ID, kind, ids, dryRun)
	if errors.Is(err, listorder.ErrInvalidOrder) {
		return nil, errors.New("IDs must contain each visible resource exactly once")
	}
	if err != nil {
		return nil, errors.New("order could not be saved")
	}
	if !dryRun {
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://localhost/internal", nil)
		s.recordAudit(req, identity.user, "mcp.reorder", string(kind), 0, "通过 MCP 更新列表排序")
	}
	return map[string]any{field: result, "dry_run": dryRun, "saved": !dryRun}, nil
}

func (s *server) registerMCPOrders(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpWriteDef("set_proxy_order", "Set the complete visible proxy order for the current user; dry_run validates only.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpProxyOrder) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSetOrder(ctx, identity, listorder.Proxies, "proxy_ids", in.ProxyIDs, in.DryRun)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_relay_order", "Set the complete visible relay order for the current user; dry_run validates only.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpRelayOrder) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSetOrder(ctx, identity, listorder.Relays, "relay_ids", in.RelayIDs, in.DryRun)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_landing_order", "Set the complete visible external landing order for the current user; dry_run validates only.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpLandingOrder) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSetOrder(ctx, identity, listorder.Landings, "landing_ids", in.LandingIDs, in.DryRun)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_user_order", "Set the complete administrator-visible user order; dry_run validates only.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUserOrder) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSetOrder(ctx, identity, listorder.Users, "user_ids", in.UserIDs, in.DryRun)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_personal_subscription_order", "Set the order of all your personal subscriptions; dry_run validates only.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPersonalSubscriptionOrder) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSetOrder(ctx, identity, listorder.PersonalSubscriptions, "subscription_ids", in.SubscriptionIDs, in.DryRun)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_published_node_order", "Set the complete administrator-visible subscription published node order; dry_run validates only.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPublishedNodeOrder) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSetOrder(ctx, identity, listorder.PublishedNodes, "node_ids", in.NodeIDs, in.DryRun)
		return nil, out, err
	})
}

// mcpOrderItems applies the web order to already-authorized results before pagination.
func (s *server) mcpOrderItems(ctx context.Context, user auth.User, kind listorder.Kind, items []map[string]any) error {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item["id"].(int64))
	}
	ranks, err := s.orderRanks(ctx, user.ID, kind, ids)
	if err != nil {
		return err
	}
	sort.SliceStable(items, func(i, j int) bool { return ranks[items[i]["id"].(int64)] < ranks[items[j]["id"].(int64)] })
	return nil
}

func mcpPageItems(items []map[string]any, in mcpPage) (map[string]any, error) {
	a, b, err := mcpBounds(in.Limit, in.Offset, len(items))
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": items[a:b], "total": len(items)}, nil
}
