package api

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpPlanCreate struct {
	Name                   string `json:"name"`
	SubscriptionTitle      string `json:"subscription_title,omitempty"`
	Enabled                *bool  `json:"enabled,omitempty"`
	TrafficLimitBytes      *int64 `json:"traffic_limit_bytes,omitempty"`
	RoutingPresetID        *int64 `json:"routing_preset_id,omitempty"`
	MihomoTemplateID       *int64 `json:"mihomo_template_id,omitempty"`
	ShadowrocketTemplateID *int64 `json:"shadowrocket_template_id,omitempty"`
}
type mcpPlanNodes struct {
	ID      int64   `json:"id"`
	NodeIDs []int64 `json:"node_ids"`
}

// A plan may refer to servers outside this administrator's server ACL.
func (s *server) mcpVisiblePlan(ctx context.Context, identity mcpIdentity, id int64) error {
	v, err := s.subscriptions.GetPlan(ctx, id)
	if err != nil {
		return errors.New("plan not found")
	}
	for _, node := range v.Nodes {
		if _, err := s.mcpVisiblePublished(ctx, identity, node.ID); err != nil {
			return errors.New("plan not found")
		}
	}
	return nil
}

func (s *server) mcpSubscriptionWrite(ctx context.Context, identity mcpIdentity, method string, id int64, body any, handler mcpBusinessHandler, kind string) (map[string]any, error) {
	if err := s.requireMCPWrite(ctx, identity); err != nil {
		return nil, err
	}
	if kind == "subscription_plan" && id > 0 {
		if err := s.mcpVisiblePlan(ctx, identity, id); err != nil {
			return nil, err
		}
	}
	out, err := s.mcpInvoke(ctx, identity.user, method, id, body, handler)
	if err != nil {
		return nil, err
	}
	out["saved"] = true
	out["agent_sync"] = "not_checked"
	if id > 0 {
		out["id"] = id
	}
	s.mcpAuditAction(ctx, identity, "subscription.mcp_change", kind, id)
	return out, nil
}
func (s *server) registerMCPSubscriptionPlans(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("list_subscription_plans", "List shared subscription plans with stable pagination."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpInvoke(ctx, identity.user, "GET", 0, nil, s.listSubscriptionPlans)
		if err != nil {
			return nil, nil, err
		}
		items, ok := out["plans"].([]any)
		if !ok {
			return nil, nil, errors.New("plan response invalid")
		}
		visible := make([]any, 0, len(items))
		for _, item := range items {
			v, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, ok := v["id"].(float64)
			if !ok {
				continue
			}
			if s.mcpVisiblePlan(ctx, identity, int64(id)) == nil {
				visible = append(visible, item)
			}
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(visible))
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"items": visible[a:b], "total": len(visible)}, nil
	})
	mcp.AddTool(ms, mcpDef("get_subscription_plan", "Get one shared subscription plan and its published nodes."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		if err := s.mcpVisiblePlan(ctx, identity, in.ID); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInvoke(ctx, identity.user, "GET", in.ID, nil, s.getSubscriptionPlan)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("create_subscription_plan", "Create a shared plan using existing validation.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPlanCreate) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSubscriptionWrite(ctx, identity, "POST", 0, in, s.createSubscriptionPlan, "subscription_plan")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("update_subscription_plan", "Update a shared plan; changes accepts name, subscription_title, enabled, traffic_limit_bytes, routing_preset_id and template IDs. This may reconcile subscriber clients; Agent sync is asynchronous.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSubscriptionPatch) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSubscriptionWrite(ctx, identity, "PATCH", in.ID, in.Changes, s.updateSubscriptionPlan, "subscription_plan")
		if err == nil {
			out["subscriber_reconciliation"] = "performed"
		}
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("delete_subscription_plan", "Delete a shared plan subject to dependency rules; expected_name must match.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.subscriptions.GetPlan(ctx, in.ID)
		if err != nil {
			return nil, nil, errors.New("plan not found")
		}
		if in.ExpectedName == "" || v.Name != in.ExpectedName {
			return nil, nil, errors.New("plan name does not match")
		}
		out, err := s.mcpSubscriptionWrite(ctx, identity, "DELETE", in.ID, nil, s.deleteSubscriptionPlan, "subscription_plan")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_subscription_plan_nodes", "Replace a shared plan's ordered published node IDs; this may reconcile subscriber clients.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPlanNodes) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		for _, id := range in.NodeIDs {
			if _, err := s.mcpVisiblePublished(ctx, identity, id); err != nil {
				return nil, nil, err
			}
		}
		out, err := s.mcpSubscriptionWrite(ctx, identity, "PUT", in.ID, map[string]any{"node_ids": in.NodeIDs}, s.setSubscriptionPlanNodes, "subscription_plan")
		if err == nil {
			out["subscriber_reconciliation"] = "performed"
		}
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_subscription_plan_routing_bindings", "Replace routing bindings for one shared plan.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpRoutingBindings) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSubscriptionWrite(ctx, identity, "PUT", in.ID, map[string]any{"routing_bindings": in.RoutingBindings}, s.setSubscriptionPlanRoutingBindings, "subscription_plan")
		return nil, out, err
	})
}
