package api

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

type mcpTemplateCreate struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled,omitempty"`
	Content string `json:"content"`
}
type mcpEffectiveTemplate struct {
	TemplateID *int64 `json:"template_id,omitempty"`
}

func (s *server) registerMCPSubscriptionTemplates(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("list_subscription_templates", "List template metadata without template content."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpPage) (*mcp.CallToolResult, any, error) {
		values, err := s.subscriptions.ListTemplates(ctx)
		if err != nil {
			return nil, nil, err
		}
		a, b, err := mcpBounds(in.Limit, in.Offset, len(values))
		if err != nil {
			return nil, nil, err
		}
		items := make([]map[string]any, 0, b-a)
		for _, v := range values[a:b] {
			items = append(items, map[string]any{"id": v.ID, "name": v.Name, "type": v.Type, "enabled": v.Enabled})
		}
		return nil, map[string]any{"items": items, "total": len(values)}, nil
	})
	mcp.AddTool(ms, mcpDef("get_subscription_template", "Get one template including its content for editing; content may contain sensitive configuration."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		v, err := s.subscriptions.GetTemplate(ctx, in.ID)
		if err != nil {
			return nil, nil, errors.New("template not found")
		}
		return nil, map[string]any{"template": toSubscriptionTemplateResponse(v)}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("create_subscription_template", "Create a validated Mihomo or Shadowrocket template.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpTemplateCreate) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSubscriptionWrite(ctx, identity, "POST", 0, in, s.createSubscriptionTemplate, "subscription_template")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("update_subscription_template", "Update a template's name, enabled state or content using changes; type cannot change.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSubscriptionPatch) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpSubscriptionWrite(ctx, identity, "PATCH", in.ID, in.Changes, s.updateSubscriptionTemplate, "subscription_template")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("delete_subscription_template", "Delete an unreferenced template; expected_name must match.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.subscriptions.GetTemplate(ctx, in.ID)
		if err != nil {
			return nil, nil, errors.New("template not found")
		}
		if in.ExpectedName == "" || v.Name != in.ExpectedName {
			return nil, nil, errors.New("template name does not match")
		}
		out, err := s.mcpSubscriptionWrite(ctx, identity, "DELETE", in.ID, nil, s.deleteSubscriptionTemplate, "subscription_template")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("get_builtin_mihomo_configuration", "Get the built-in Mihomo base configuration."), func(ctx context.Context, _ *mcp.CallToolRequest, _ mcpEmpty) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpInvoke(ctx, identity.user, "GET", 0, nil, s.getBuiltinMihomoConfiguration)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("get_builtin_shadowrocket_configuration", "Get the built-in Shadowrocket base configuration."), func(ctx context.Context, _ *mcp.CallToolRequest, _ mcpEmpty) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpInvoke(ctx, identity.user, "GET", 0, nil, s.getBuiltinShadowrocketConfiguration)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("get_effective_mihomo_configuration", "Get the effective Mihomo base configuration for an optional template ID."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpEffectiveTemplate) (*mcp.CallToolResult, any, error) {
		var template *subscriptionstore.SubscriptionTemplate
		if in.TemplateID != nil {
			if *in.TemplateID <= 0 {
				return nil, nil, errors.New("invalid template ID")
			}
			v, err := s.subscriptions.GetTemplate(ctx, *in.TemplateID)
			if err != nil {
				return nil, nil, errors.New("template not found")
			}
			if v.Type != subscriptionstore.TemplateTypeMihomo {
				return nil, nil, errors.New("template type mismatch")
			}
			if v.Enabled {
				template = &v
			}
		}
		v, err := subscriptionstore.BuildMihomoConfiguration(template)
		if err != nil {
			return nil, nil, errors.New("configuration rendering failed")
		}
		name := subscriptionstore.BuiltinMihomoName
		if template != nil {
			name = template.Name
		}
		return nil, map[string]any{"name": name, "yaml": v.YAML}, nil
	})
}
