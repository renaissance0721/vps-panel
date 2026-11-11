package api

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpRoutingGroup struct {
	Key          string   `json:"key,omitempty"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Proxies      []string `json:"proxies"`
	IncludeAll   bool     `json:"include_all,omitempty"`
	NodePatterns []string `json:"node_patterns,omitempty"`
}
type mcpRoutingProvider struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Type     string `json:"type"`
	Behavior string `json:"behavior"`
	Format   string `json:"format"`
	Interval int    `json:"interval"`
}
type mcpRoutingCreate struct {
	Name          string               `json:"name"`
	Enabled       *bool                `json:"enabled,omitempty"`
	Groups        []mcpRoutingGroup    `json:"groups"`
	RuleProviders []mcpRoutingProvider `json:"rule_providers"`
	Rules         []string             `json:"rules"`
}

func (s *server) registerMCPSubscriptionRouting(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("list_routing_presets", "List routing presets in stable order."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		values, err := s.subscriptions.ListRoutingPresets(ctx)
		if err != nil {
			return nil, nil, err
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(values))
		if err != nil {
			return nil, nil, err
		}
		items := make([]map[string]any, 0, b-a)
		for _, v := range values[a:b] {
			items = append(items, map[string]any{"id": v.ID, "name": v.Name, "enabled": v.Enabled, "is_default": v.IsDefault})
		}
		return nil, map[string]any{"items": items, "total": len(values)}, nil
	})
	mcp.AddTool(ms, mcpDef("get_routing_preset", "Get the complete routing preset before editing groups or rules."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		v, err := s.subscriptions.GetRoutingPreset(ctx, in.ID)
		if err != nil {
			return nil, nil, errors.New("routing preset not found")
		}
		return nil, map[string]any{"routing_preset": toRoutingPresetResponse(v)}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("create_routing_preset", "Create a routing preset using the existing schema and validation.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpRoutingCreate) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSubscriptionWrite(ctx, identity, "POST", 0, in, s.createSubscriptionRoutingPreset, "routing_preset")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("update_routing_preset", "Update name, enabled, groups, rule_providers or rules in changes. Arrays replace the complete field: read and preserve other entries first.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSubscriptionPatch) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSubscriptionWrite(ctx, identity, "PATCH", in.ID, in.Changes, s.updateSubscriptionRoutingPreset, "routing_preset")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("delete_routing_preset", "Delete an unreferenced non-default preset; expected_name must match.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.subscriptions.GetRoutingPreset(ctx, in.ID)
		if err != nil {
			return nil, nil, errors.New("routing preset not found")
		}
		if in.ExpectedName == "" || v.Name != in.ExpectedName {
			return nil, nil, errors.New("routing preset name does not match")
		}
		out, err := s.mcpSubscriptionWrite(ctx, identity, "DELETE", in.ID, nil, s.deleteSubscriptionRoutingPreset, "routing_preset")
		return nil, out, err
	})
}
