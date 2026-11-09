package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func (s *server) mcpVisiblePlanIDs(ctx context.Context, identity mcpIdentity, raw any) error {
	if raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return errors.New("invalid plan IDs")
	}
	var ids []int64
	if err := json.Unmarshal(data, &ids); err != nil {
		return errors.New("invalid plan IDs")
	}
	for _, id := range ids {
		if err := s.mcpVisiblePlan(ctx, identity, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) mcpVisiblePublishedPlans(ctx context.Context, identity mcpIdentity, nodeID int64) error {
	plans, err := s.subscriptions.ListPlans(ctx)
	if err != nil {
		return errors.New("plan query failed")
	}
	for _, plan := range plans {
		for _, node := range plan.Nodes {
			if node.ID == nodeID {
				if err := s.mcpVisiblePlan(ctx, identity, plan.ID); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

type mcpPublishedCreate struct {
	Name              string   `json:"name"`
	Mode              string   `json:"mode"`
	TargetProxyID     int64    `json:"target_proxy_id"`
	SourceServerID    *int64   `json:"source_server_id,omitempty"`
	EntryHostMode     string   `json:"entry_host_mode,omitempty"`
	EntryHost         string   `json:"entry_host,omitempty"`
	EntryPortMode     string   `json:"entry_port_mode,omitempty"`
	EntryPort         *int     `json:"entry_port,omitempty"`
	PlanIDs           []int64  `json:"plan_ids,omitempty"`
	TrafficMultiplier *float64 `json:"traffic_multiplier,omitempty"`
	Enabled           *bool    `json:"enabled,omitempty"`
}

func (s *server) mcpVisiblePublished(ctx context.Context, identity mcpIdentity, id int64) (subscriptionstore.PublishedNode, error) {
	v, err := s.subscriptions.GetPublishedNode(ctx, id)
	if err != nil {
		return v, errors.New("published node not found")
	}
	if _, err := s.proxyForUser(ctx, identity.user, v.TargetProxyID); err != nil {
		return v, errors.New("published node not found")
	}
	if v.SourceServerID != nil {
		if _, err := s.mcpVisibleServer(ctx, identity.user, *v.SourceServerID); err != nil {
			return v, errors.New("published node not found")
		}
	}
	return v, nil
}
func (s *server) registerMCPSubscriptionNodes(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("get_published_node", "Get an accessible subscription published node."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		v, err := s.mcpVisiblePublished(ctx, identity, in.ID)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"node": toSubscriptionPublishedNodeResponse(v)}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("create_published_node", "Create a published node with direct or relay mode; Agent synchronization is asynchronous.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPublishedCreate) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		if _, err := s.proxyForUser(ctx, identity.user, in.TargetProxyID); err != nil {
			return nil, nil, errors.New("target proxy not found")
		}
		if in.SourceServerID != nil {
			if _, err := s.mcpVisibleServer(ctx, identity.user, *in.SourceServerID); err != nil {
				return nil, nil, err
			}
		}
		if err := s.mcpVisiblePlanIDs(ctx, identity, in.PlanIDs); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpSubscriptionWrite(ctx, identity, "POST", 0, in, s.createSubscriptionPublishedNode, "published_node")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("update_published_node", "Update a published node's name, enabled state, entry settings, multiplier or plan_ids; Agent sync is asynchronous.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSubscriptionPatch) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		if _, err := s.mcpVisiblePublished(ctx, identity, in.ID); err != nil {
			return nil, nil, err
		}
		if err := s.mcpVisiblePublishedPlans(ctx, identity, in.ID); err != nil {
			return nil, nil, err
		}
		if err := s.mcpVisiblePlanIDs(ctx, identity, in.Changes["plan_ids"]); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpSubscriptionWrite(ctx, identity, "PATCH", in.ID, in.Changes, s.updateSubscriptionPublishedNode, "published_node")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("delete_published_node", "Delete a published node subject to plan dependencies; expected_name must match.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.mcpVisiblePublished(ctx, identity, in.ID)
		if err != nil {
			return nil, nil, err
		}
		if err := s.mcpVisiblePublishedPlans(ctx, identity, in.ID); err != nil {
			return nil, nil, err
		}
		if in.ExpectedName == "" || v.Name != in.ExpectedName {
			return nil, nil, errors.New("published node name does not match")
		}
		out, err := s.mcpSubscriptionWrite(ctx, identity, "DELETE", in.ID, nil, s.deleteSubscriptionPublishedNode, "published_node")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("list_subscription_relay_servers", "List relay-capable servers accessible to the current user."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		raw, err := s.mcpInvoke(ctx, identity.user, "GET", 0, nil, s.listSubscriptionRelayServers)
		if err != nil {
			return nil, nil, err
		}
		values, _ := raw["servers"].([]any)
		out := []any{}
		for _, item := range values {
			v, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, ok := v["id"].(float64)
			if !ok {
				continue
			}
			if _, err := s.mcpVisibleServer(ctx, identity.user, int64(id)); err == nil {
				out = append(out, v)
			}
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(out))
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"items": out[a:b], "total": len(out)}, nil
	})
}
