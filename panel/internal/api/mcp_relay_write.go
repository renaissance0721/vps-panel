package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpCreateRelay struct {
	ServerID        int64  `json:"server_id"`
	Name            string `json:"name"`
	ListenPort      int    `json:"listen_port"`
	TargetType      string `json:"target_type"`
	ListenAddress   string `json:"listen_address,omitempty"`
	EntryHostMode   string `json:"entry_host_mode,omitempty"`
	EntryHost       string `json:"entry_host,omitempty"`
	TargetProxyID   *int64 `json:"target_proxy_id,omitempty"`
	TargetClientID  *int64 `json:"target_client_id,omitempty"`
	TargetLandingID *int64 `json:"target_landing_id,omitempty"`
	TargetHost      string `json:"target_host,omitempty"`
	TargetPort      int    `json:"target_port,omitempty"`
	Network         string `json:"network,omitempty"`
	Enabled         *bool  `json:"enabled,omitempty"`
}
type mcpRelayChanges struct {
	Name            *string `json:"name,omitempty"`
	ListenPort      *int    `json:"listen_port,omitempty"`
	ListenAddress   *string `json:"listen_address,omitempty"`
	EntryHostMode   *string `json:"entry_host_mode,omitempty"`
	EntryHost       *string `json:"entry_host,omitempty"`
	TargetType      *string `json:"target_type,omitempty"`
	TargetProxyID   *int64  `json:"target_proxy_id,omitempty"`
	TargetClientID  *int64  `json:"target_client_id,omitempty"`
	TargetLandingID *int64  `json:"target_landing_id,omitempty"`
	TargetHost      *string `json:"target_host,omitempty"`
	TargetPort      *int    `json:"target_port,omitempty"`
	Network         *string `json:"network,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}
type mcpUpdateRelay struct {
	ID      int64           `json:"id"`
	Changes mcpRelayChanges `json:"changes"`
}

func (s *server) registerMCPRelayWrites(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpWriteDef("create_relay", "Create a relay with the Panel's target access and validation rules.", false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpCreateRelay) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpWriteHandler(ctx, identity, "POST", 0, in.ServerID, in, s.createRelay, "relay")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("update_relay", "Update one ordinary relay; managed relays remain protected.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUpdateRelay) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.relayForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("relay not found")
		}
		changes, _ := json.Marshal(in.Changes)
		if string(changes) == "{}" {
			return nil, nil, errors.New("no relay changes provided")
		}
		out, err := s.mcpWriteHandler(ctx, identity, "PATCH", in.ID, v.ServerID, in.Changes, s.updateRelay, "relay")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_relay_enabled", "Enable or disable one ordinary relay; Agent sync is asynchronous.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpToggle) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.relayForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("relay not found")
		}
		out, err := s.mcpWriteHandler(ctx, identity, "PATCH", in.ID, v.ServerID, map[string]bool{"enabled": in.Enabled}, s.updateRelay, "relay")
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("delete_relay", "Delete one ordinary relay. expected_name must match the current name.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.relayForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("relay not found")
		}
		if in.ExpectedName == "" || v.Name != in.ExpectedName {
			return nil, nil, errors.New("relay name does not match")
		}
		out, err := s.mcpWriteHandler(ctx, identity, "DELETE", in.ID, v.ServerID, nil, s.deleteRelay, "")
		if err == nil {
			out.(map[string]any)["deleted"] = map[string]any{"id": v.ID, "name": v.Name}
		}
		return nil, out, err
	})
}
