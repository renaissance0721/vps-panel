package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpLandingCreate struct {
	Name       string `json:"name"`
	NodeRole   string `json:"node_role,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	URI        string `json:"uri"`
}
type mcpLandingChanges struct {
	Name       *string `json:"name,omitempty"`
	NodeRole   *string `json:"node_role,omitempty"`
	Visibility *string `json:"visibility,omitempty"`
	URI        *string `json:"uri,omitempty"`
}
type mcpLandingUpdate struct {
	ID      int64             `json:"id"`
	Changes mcpLandingChanges `json:"changes"`
}

func (s *server) mcpLandingAudit(ctx context.Context, identity mcpIdentity, action string, id int64) {
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://localhost/internal", nil)
	s.recordAudit(req, identity.user, action, "landing", id, "通过 MCP 管理外部节点")
}

func (s *server) registerMCPP0LandingTools(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("get_landing", "Get an accessible external landing's metadata without its URI."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpInvoke(ctx, identity.user, "GET", in.ID, nil, s.getLanding)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("create_landing", "Create an external landing using the Panel's URI validation; never returns its URI.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpLandingCreate) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInvoke(ctx, identity.user, "POST", 0, in, s.createLanding)
		if err != nil {
			return nil, nil, err
		}
		v, _ := out["landing"].(map[string]any)
		id, _ := v["id"].(float64)
		s.mcpLandingAudit(ctx, identity, "landing.create", int64(id))
		return nil, map[string]any{"saved": true, "landing": v}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("update_landing", "Update an owned landing; dependent Relay synchronization is asynchronous.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpLandingUpdate) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInvoke(ctx, identity.user, "PATCH", in.ID, in.Changes, s.updateLanding)
		if err != nil {
			return nil, nil, err
		}
		s.mcpLandingAudit(ctx, identity, "landing.update", in.ID)
		return nil, map[string]any{"saved": true, "landing": out["landing"], "dependent_server_sync": "not_checked"}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("delete_landing", "Delete an owned landing subject to dependency rules; expected_name must match.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.landingForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("landing not found")
		}
		if in.ExpectedName == "" || v.Name != in.ExpectedName {
			return nil, nil, errors.New("landing name does not match")
		}
		_, err = s.mcpInvoke(ctx, identity.user, "DELETE", in.ID, nil, s.deleteLanding)
		if err != nil {
			return nil, nil, err
		}
		s.mcpLandingAudit(ctx, identity, "landing.delete", in.ID)
		return nil, map[string]any{"saved": true, "deleted": map[string]any{"id": v.ID, "name": v.Name}}, nil
	})
	mcp.AddTool(ms, mcpDef("get_landing_share", "Explicitly retrieve a landing's credential-bearing URI; requires read_write and write switch."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInvoke(ctx, identity.user, "GET", in.ID, nil, s.getLandingShare)
		return nil, out, err
	})
}
