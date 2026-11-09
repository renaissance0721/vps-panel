package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpCreateProxy struct {
	ServerID          int64  `json:"server_id"`
	Name              string `json:"name"`
	ListenPort        int    `json:"listen_port"`
	Protocol          string `json:"protocol,omitempty"`
	Security          string `json:"security,omitempty"`
	TLSMode           string `json:"tls_mode,omitempty"`
	Certificate       string `json:"certificate,omitempty"`
	PrivateKey        string `json:"private_key,omitempty"`
	ServerName        string `json:"server_name,omitempty"`
	RealityTarget     string `json:"reality_target,omitempty"`
	Method            string `json:"method,omitempty"`
	ListenFamily      string `json:"listen_family,omitempty"`
	EntryHostMode     string `json:"entry_host_mode,omitempty"`
	EntryHost         string `json:"entry_host,omitempty"`
	NodeRole          string `json:"node_role,omitempty"`
	Enabled           *bool  `json:"enabled,omitempty"`
	FirstClientName   string `json:"first_client_name,omitempty"`
	FirstClientUDP443 bool   `json:"first_client_udp443,omitempty"`
}
type mcpProxyChanges struct {
	Name          *string `json:"name,omitempty"`
	ListenPort    *int    `json:"listen_port,omitempty"`
	ListenFamily  *string `json:"listen_family,omitempty"`
	EntryHostMode *string `json:"entry_host_mode,omitempty"`
	EntryHost     *string `json:"entry_host,omitempty"`
	NodeRole      *string `json:"node_role,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
	Security      *string `json:"security,omitempty"`
	TLSMode       *string `json:"tls_mode,omitempty"`
	ServerName    *string `json:"server_name,omitempty"`
	RealityTarget *string `json:"reality_target,omitempty"`
	Method        *string `json:"method,omitempty"`
	Certificate   *string `json:"certificate,omitempty"`
	PrivateKey    *string `json:"private_key,omitempty"`
}
type mcpUpdateProxy struct {
	ID      int64           `json:"id"`
	Changes mcpProxyChanges `json:"changes"`
}

func (s *server) registerMCPProxyTools(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("list_proxy_clients", "List client identifiers on a visible proxy without credentials."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		v, err := s.proxyForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("proxy not found")
		}
		out := []any{}
		for _, c := range v.Clients {
			out = append(out, toClientSummaryResponse(c))
		}
		return nil, map[string]any{"items": out, "total": len(out)}, nil
	})
	mcp.AddTool(ms, mcpWriteDef("create_proxy", "Create a proxy using existing validation and capability checks. Agent sync is asynchronous.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpCreateProxy) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpWriteHandler(ctx, identity, "POST", 0, in.ServerID, in, s.createProxy, "proxy")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("update_proxy", "Update an existing proxy; changes use the same validation as the Panel.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUpdateProxy) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.proxyForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("proxy not found")
		}
		changes, _ := json.Marshal(in.Changes)
		if string(changes) == "{}" {
			return nil, nil, errors.New("no proxy changes provided")
		}
		out, err := s.mcpWriteHandler(ctx, identity, "PATCH", in.ID, v.ServerID, in.Changes, s.updateProxy, "proxy")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_proxy_enabled", "Enable or disable one proxy; Agent sync is asynchronous.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpToggle) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.proxyForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("proxy not found")
		}
		out, err := s.mcpWriteHandler(ctx, identity, "PATCH", in.ID, v.ServerID, map[string]bool{"enabled": in.Enabled}, s.updateProxy, "proxy")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("delete_proxy", "Delete one proxy subject to existing reference checks. expected_name must match the current name.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.proxyForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("proxy not found")
		}
		if in.ExpectedName == "" || v.Name != in.ExpectedName {
			return nil, nil, errors.New("proxy name does not match")
		}
		out, err := s.mcpWriteHandler(ctx, identity, "DELETE", in.ID, v.ServerID, nil, s.deleteProxy, "")
		if err == nil {
			result := out.(map[string]any)
			result["deleted"] = map[string]any{"id": v.ID, "name": v.Name}
			result["dependent_server_sync"] = "not_checked"
		}
		return nil, out, err
	})
}
