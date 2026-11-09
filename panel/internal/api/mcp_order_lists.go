package api

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/listorder"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func (s *server) registerMCPOrderLists(ms *mcp.Server, identity mcpIdentity) {
	user := identity.user
	mcp.AddTool(ms, mcpDef("list_landings", "List accessible external landing metadata without connection credentials."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		values, err := s.landings.List(ctx, user.ID)
		if err != nil {
			return nil, nil, errors.New("landing query failed")
		}
		items := []map[string]any{}
		for _, v := range values {
			items = append(items, map[string]any{"id": v.ID, "name": v.Name, "protocol": v.Protocol, "visibility": v.Visibility, "node_role": v.NodeRole, "owned_by_me": v.OwnedByMe})
		}
		if err := s.mcpOrderItems(ctx, user, listorder.Landings, items); err != nil {
			return nil, nil, errors.New("order query failed")
		}
		out, err := mcpPageItems(items, in)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("list_users", "List administrator-visible user IDs, names and roles without credentials."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		if user.Role != auth.RoleAdmin {
			return nil, nil, errors.New("access denied")
		}
		values, err := s.authService.ListUsers(ctx)
		if err != nil {
			return nil, nil, errors.New("user query failed")
		}
		items := []map[string]any{}
		for _, v := range values {
			items = append(items, map[string]any{"id": v.ID, "username": v.Username, "role": v.Role})
		}
		if err := s.mcpOrderItems(ctx, user, listorder.Users, items); err != nil {
			return nil, nil, errors.New("order query failed")
		}
		out, err := mcpPageItems(items, in)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("list_personal_subscriptions", "List your personal subscription IDs and summaries without secrets."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		values, err := s.subscriptions.ListPersonalSubscriptions(ctx, subscriptionstore.PersonalSubscriptionActor{UserID: user.ID, Role: user.Role})
		if err != nil {
			return nil, nil, errors.New("subscription query failed")
		}
		items := []map[string]any{}
		for _, v := range values {
			items = append(items, map[string]any{"id": v.ID, "name": v.Name, "type": "personal", "enabled": v.Enabled, "node_count": len(v.Nodes), "updated_at": v.UpdatedAt})
		}
		if err := s.mcpOrderItems(ctx, user, listorder.PersonalSubscriptions, items); err != nil {
			return nil, nil, errors.New("order query failed")
		}
		out, err := mcpPageItems(items, in)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("list_published_nodes", "List subscription published node metadata without credentials."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		if user.Role != auth.RoleAdmin {
			return nil, nil, errors.New("access denied")
		}
		values, err := s.subscriptions.ListPublishedNodes(ctx)
		if err != nil {
			return nil, nil, errors.New("published node query failed")
		}
		items := []map[string]any{}
		for _, v := range values {
			if _, err := s.mcpVisiblePublished(ctx, identity, v.ID); err != nil {
				continue
			}
			items = append(items, map[string]any{"id": v.ID, "name": v.Name, "mode": v.Mode, "enabled": v.Enabled, "updated_at": v.UpdatedAt})
		}
		if err := s.mcpOrderItems(ctx, user, listorder.PublishedNodes, items); err != nil {
			return nil, nil, errors.New("order query failed")
		}
		out, err := mcpPageItems(items, in)
		return nil, out, err
	})
}
