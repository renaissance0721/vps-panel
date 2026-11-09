package api

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *server) mcpSubscriberGuard(ctx context.Context, in mcpDelete) error {
	v, err := s.subscriptions.GetSubscriber(ctx, in.ID)
	if err != nil {
		return errors.New("subscription user not found")
	}
	if in.ExpectedName == "" || v.Username != in.ExpectedName {
		return errors.New("subscription username does not match")
	}
	return nil
}

func (s *server) mcpVisibleSubscriber(ctx context.Context, identity mcpIdentity, id int64) error {
	v, err := s.subscriptions.GetSubscriber(ctx, id)
	if err != nil {
		return errors.New("subscription user not found")
	}
	if v.PlanID != nil && s.mcpVisiblePlan(ctx, identity, *v.PlanID) != nil {
		return errors.New("subscription user not found")
	}
	return nil
}
func (s *server) registerMCPSubscriptionUsers(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("list_subscription_users", "List subscriber accounts without subscription credentials."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpInvoke(ctx, identity.user, "GET", 0, nil, s.listSubscriptionUsers)
		if err != nil {
			return nil, nil, err
		}
		items, _ := out["users"].([]any)
		visible := make([]any, 0, len(items))
		for _, item := range items {
			v, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, ok := v["user_id"].(float64)
			if ok && s.mcpVisibleSubscriber(ctx, identity, int64(id)) == nil {
				visible = append(visible, item)
			}
		}
		items = visible
		a, b, err := mcpBounds(in.Limit, in.Offset, len(items))
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"items": items[a:b], "total": len(items)}, nil
	})
	mcp.AddTool(ms, mcpDef("get_subscription_user", "Get subscription user details without tokens or subscription URLs."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		if err := s.mcpVisibleSubscriber(ctx, identity, in.ID); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInvoke(ctx, identity.user, "GET", in.ID, nil, s.getSubscriptionUser)
		if err != nil {
			return nil, nil, err
		}
		return nil, mcpSanitizeSubscription(out, "user"), nil
	})
	mcp.AddTool(ms, mcpWriteDef("update_subscription_user", "Update plan_id, enabled, expires_at, traffic reset and billing fields; subscriber Client sync is asynchronous.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSubscriptionPatch) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		if err := s.mcpVisibleSubscriber(ctx, identity, in.ID); err != nil {
			return nil, nil, err
		}
		if v, ok := in.Changes["plan_id"]; ok && v != nil {
			if err := s.mcpVisiblePlanIDs(ctx, identity, []any{v}); err != nil {
				return nil, nil, err
			}
		}
		out, err := s.mcpSubscriptionWrite(ctx, identity, "PATCH", in.ID, in.Changes, s.updateSubscriptionUser, "subscriber")
		if err != nil {
			return nil, nil, err
		}
		return nil, mcpSanitizeSubscription(out, "user"), nil
	})
	mcp.AddTool(ms, mcpWriteDef("reset_subscription_user_traffic", "Reset subscriber traffic; expected_name must match the username.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		if err := s.mcpVisibleSubscriber(ctx, identity, in.ID); err != nil {
			return nil, nil, err
		}
		if err := s.mcpSubscriberGuard(ctx, in); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpSubscriptionWrite(ctx, identity, "POST", in.ID, nil, s.resetSubscriptionUserTraffic, "subscriber")
		if err != nil {
			return nil, nil, err
		}
		return nil, mcpSanitizeSubscription(out, "user"), nil
	})
	mcp.AddTool(ms, mcpWriteDef("regenerate_subscription_user_token", "Regenerate a subscriber token; returns a sensitive token once, and requires expected_name.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		if err := s.mcpVisibleSubscriber(ctx, identity, in.ID); err != nil {
			return nil, nil, err
		}
		if err := s.mcpSubscriberGuard(ctx, in); err != nil {
			return nil, nil, err
		}
		token, err := s.subscriptions.RegenerateSubscriptionToken(ctx, in.ID)
		if err != nil {
			return nil, nil, errors.New("token regeneration failed")
		}
		s.mcpAuditAction(ctx, identity, "subscriber.token.regenerate", "subscriber", in.ID)
		return nil, map[string]any{"saved": true, "user_id": in.ID, "subscription_token": token}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("preview_subscription_user_mihomo", "Render sensitive subscriber configuration; this can reconcile Clients and notify Agents.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		if err := s.mcpVisibleSubscriber(ctx, identity, in.ID); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInvoke(ctx, identity.user, "GET", in.ID, nil, s.previewSubscriptionUserMihomo)
		if err != nil {
			return nil, nil, errors.New("preview failed; subscriber client reconciliation may already have occurred, inspect current state before retrying")
		}
		out["agent_sync"] = "not_checked"
		return nil, out, nil
	})
}
