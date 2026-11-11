package api

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type mcpRoutingINIText struct {
	Source string `json:"source"`
}
type mcpRoutingINIPreview struct {
	Source                 string `json:"source"`
	PresetID               *int64 `json:"preset_id,omitempty"`
	PersonalSubscriptionID *int64 `json:"personal_subscription_id,omitempty"`
	SubscriberUserID       *int64 `json:"subscriber_user_id,omitempty"`
}
type mcpRoutingINICreate struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled,omitempty"`
	Source  string `json:"source"`
}
type mcpRoutingINIUpdate struct {
	ID       int64  `json:"id"`
	Revision string `json:"revision"`
	Source   string `json:"source"`
}

func (s *server) registerMCPRoutingINI(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("get_routing_ini", "Export an existing routing preset as supported INI and return its revision."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpInvoke(ctx, identity.user, "GET", in.ID, nil, s.getRoutingINI)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("validate_routing_ini", "Validate routing INI without saving or fetching rule URLs."), func(_ context.Context, _ *mcp.CallToolRequest, in mcpRoutingINIText) (*mcp.CallToolResult, any, error) {
		return nil, subscriptionstore.ParseRoutingINI(in.Source), nil
	})
	mcp.AddTool(ms, mcpDef("preview_routing_ini", "Preview semantic changes and binding conflicts without saving; optionally render against an owned personal subscription."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpRoutingINIPreview) (*mcp.CallToolResult, any, error) {
		out, err := s.routingINIPreviewForUser(ctx, identity.user, in.Source, in.PresetID, in.PersonalSubscriptionID, in.SubscriberUserID)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("create_routing_from_ini", "Create a routing preset from validated INI.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpRoutingINICreate) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSubscriptionWrite(ctx, identity, "POST", 0, in, s.createRoutingINI, "routing_preset")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("update_routing_from_ini", "Update an existing preset using its current revision; existing bound groups cannot be removed.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpRoutingINIUpdate) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSubscriptionWrite(ctx, identity, "PUT", in.ID, map[string]any{"source": in.Source, "revision": in.Revision}, s.updateRoutingINI, "routing_preset")
		return nil, out, err
	})
}
