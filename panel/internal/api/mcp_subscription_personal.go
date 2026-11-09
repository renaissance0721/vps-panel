package api

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpPersonalCreate struct {
	Name                   string `json:"name"`
	SubscriptionTitle      string `json:"subscription_title,omitempty"`
	Enabled                *bool  `json:"enabled,omitempty"`
	ClientName             string `json:"client_name,omitempty"`
	RoutingPresetID        *int64 `json:"routing_preset_id,omitempty"`
	MihomoTemplateID       *int64 `json:"mihomo_template_id,omitempty"`
	ShadowrocketTemplateID *int64 `json:"shadowrocket_template_id,omitempty"`
}
type mcpSubscriptionPatch struct {
	ID      int64          `json:"id"`
	Changes map[string]any `json:"changes"`
}
type mcpPersonalNode struct {
	ID          *int64  `json:"id,omitempty"`
	SourceType  string  `json:"source_type"`
	SourceID    int64   `json:"source_id"`
	DisplayName string  `json:"display_name,omitempty"`
	Enabled     bool    `json:"enabled"`
	EntryHost   *string `json:"entry_host,omitempty"`
	EntryPort   *int    `json:"entry_port,omitempty"`
}
type mcpPersonalNodes struct {
	ID    int64             `json:"id"`
	Nodes []mcpPersonalNode `json:"nodes"`
}
type mcpRoutingBindings struct {
	ID              int64              `json:"id"`
	RoutingBindings map[string][]int64 `json:"routing_bindings"`
}
type mcpPersonalSources struct {
	ClientName string `json:"client_name"`
	Limit      int    `json:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty"`
}

func mcpSanitizeSubscription(out map[string]any, key string) map[string]any {
	if value, ok := out[key].(map[string]any); ok {
		for _, k := range []string{"subscription_token", "subscription_url", "subscription_base64_url", "subscription_mihomo_url", "subscription_shadowrocket_url", "subscription_auto_url"} {
			delete(value, k)
		}
	}
	return out
}
func (s *server) mcpPersonalWrite(ctx context.Context, identity mcpIdentity, method string, id int64, body any, handler mcpBusinessHandler) (map[string]any, error) {
	if err := s.requireMCPWrite(ctx, identity); err != nil {
		return nil, err
	}
	out, err := s.mcpInvoke(ctx, identity.user, method, id, body, handler)
	if err != nil {
		return nil, err
	}
	mcpSanitizeSubscription(out, "personal_subscription")
	out["saved"] = true
	return out, nil
}
func (s *server) registerMCPPersonalSubscriptions(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("get_personal_subscription", "Get your personal subscription details without subscription credentials."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpInvoke(ctx, identity.user, "GET", in.ID, nil, s.getPersonalSubscription)
		if err != nil {
			return nil, nil, err
		}
		return nil, mcpSanitizeSubscription(out, "personal_subscription"), nil
	})
	mcp.AddTool(ms, mcpWriteDef("create_personal_subscription", "Create a personal subscription; no subscription URL is returned.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPersonalCreate) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpPersonalWrite(ctx, identity, "POST", 0, in, s.createPersonalSubscription)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("update_personal_subscription", "Update personal subscription fields; changes may contain name, subscription_title, enabled, client_name, routing_preset_id, mihomo_template_id, shadowrocket_template_id. Omit unchanged fields; null clears template IDs.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSubscriptionPatch) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpPersonalWrite(ctx, identity, "PATCH", in.ID, in.Changes, s.updatePersonalSubscription)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("delete_personal_subscription", "Delete your personal subscription; expected_name must match.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.subscriptions.GetPersonalSubscription(ctx, personalActor(identity.user), in.ID)
		if err != nil {
			return nil, nil, errors.New("personal subscription not found")
		}
		if in.ExpectedName == "" || v.Name != in.ExpectedName {
			return nil, nil, errors.New("subscription name does not match")
		}
		out, err := s.mcpPersonalWrite(ctx, identity, "DELETE", in.ID, nil, s.deletePersonalSubscription)
		if err == nil {
			out["id"] = in.ID
		}
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("list_personal_subscription_sources", "List accessible personal subscription sources for a client name."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPersonalSources) (*mcp.CallToolResult, any, error) {
		values, err := s.subscriptions.ListPersonalSubscriptionSources(ctx, personalActor(identity.user), in.ClientName)
		if err != nil {
			return nil, nil, errors.New("source query failed")
		}
		out := []any{}
		for _, v := range values {
			out = append(out, map[string]any{"source_type": v.SourceType, "source_id": v.SourceID, "name": v.Name, "detail": v.Detail, "default_name": v.DefaultName, "status": v.Status, "status_detail": v.StatusDetail, "requires_client": v.RequiresClient})
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(out))
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"items": out[a:b], "total": len(out)}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("set_personal_subscription_nodes", "Replace one personal subscription's complete ordered nodes list.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPersonalNodes) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpPersonalWrite(ctx, identity, "PUT", in.ID, map[string]any{"nodes": in.Nodes}, s.setPersonalSubscriptionNodes)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_personal_subscription_routing_bindings", "Replace routing bindings for one personal subscription.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpRoutingBindings) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpPersonalWrite(ctx, identity, "PUT", in.ID, map[string]any{"routing_bindings": in.RoutingBindings}, s.setPersonalSubscriptionRoutingBindings)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("preview_personal_subscription_mihomo", "Explicitly render the actual personal Mihomo configuration; output may contain credentials and requires read_write."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInvoke(ctx, identity.user, "GET", in.ID, nil, s.previewPersonalSubscriptionMihomo)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("preview_personal_subscription_shadowrocket", "Explicitly render the actual personal Shadowrocket configuration; output may contain credentials and requires read_write."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInvoke(ctx, identity.user, "GET", in.ID, nil, s.previewPersonalSubscriptionShadowrocket)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("regenerate_personal_subscription_token", "Invalidate and regenerate a personal subscription token; returns the sensitive new token and requires expected_name.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.subscriptions.GetPersonalSubscription(ctx, personalActor(identity.user), in.ID)
		if err != nil {
			return nil, nil, errors.New("personal subscription not found")
		}
		if in.ExpectedName == "" || v.Name != in.ExpectedName {
			return nil, nil, errors.New("subscription name does not match")
		}
		updated, err := s.subscriptions.RegeneratePersonalSubscriptionToken(ctx, personalActor(identity.user), in.ID)
		if err != nil {
			return nil, nil, errors.New("token regeneration failed")
		}
		s.mcpAuditAction(ctx, identity, "personal_subscription.token.regenerate", "personal_subscription", in.ID)
		return nil, map[string]any{"saved": true, "id": in.ID, "subscription_token": updated.Token}, nil
	})
}
