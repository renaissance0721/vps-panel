package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpClientFields struct {
	Name                *string         `json:"name,omitempty"`
	Enabled             *bool           `json:"enabled,omitempty"`
	ClientUDP443        *bool           `json:"client_udp443,omitempty"`
	ExpiresAt           json.RawMessage `json:"expires_at,omitempty"`
	TrafficLimit        json.RawMessage `json:"traffic_limit,omitempty"`
	LimitUnit           *string         `json:"limit_unit,omitempty"`
	TrafficResetMode    *string         `json:"traffic_reset_mode,omitempty"`
	TrafficResetWeekday *int            `json:"traffic_reset_weekday,omitempty"`
	TrafficResetDay     *int            `json:"traffic_reset_day,omitempty"`
	TrafficResetTime    *string         `json:"traffic_reset_time,omitempty"`
}
type mcpCreateClient struct {
	ProxyID             int64           `json:"proxy_id"`
	Name                string          `json:"name"`
	ClientUDP443        bool            `json:"client_udp443,omitempty"`
	Enabled             *bool           `json:"enabled,omitempty"`
	ExpiresAt           json.RawMessage `json:"expires_at,omitempty"`
	TrafficLimit        json.RawMessage `json:"traffic_limit,omitempty"`
	LimitUnit           *string         `json:"limit_unit,omitempty"`
	TrafficResetMode    *string         `json:"traffic_reset_mode,omitempty"`
	TrafficResetWeekday *int            `json:"traffic_reset_weekday,omitempty"`
	TrafficResetDay     *int            `json:"traffic_reset_day,omitempty"`
	TrafficResetTime    *string         `json:"traffic_reset_time,omitempty"`
}
type mcpUpdateClient struct {
	ID      int64           `json:"id"`
	Changes mcpClientFields `json:"changes"`
}
type mcpAssignClient struct {
	ID                  int64  `json:"id"`
	ExpectedName        string `json:"expected_name"`
	UserID              *int64 `json:"user_id"`
	BillingPeriodMonths *int   `json:"billing_period_months,omitempty"`
}
type mcpClientPorts struct {
	ID    int64 `json:"id"`
	Count int   `json:"user_relay_port_count"`
}

func mcpClientBody(value any, omit string) map[string]json.RawMessage {
	data, _ := json.Marshal(value)
	var body map[string]json.RawMessage
	_ = json.Unmarshal(data, &body)
	delete(body, omit)
	return body
}

// RawMessage preserves explicit null for fields the existing API can clear;
// provide their actual JSON types instead of inferring byte-array schemas.
func mcpClientTool(name, description string, destructive bool, update bool) *mcp.Tool {
	t := mcpWriteDef(name, description, destructive)
	fields := `"enabled":{"type":["boolean","null"]},"client_udp443":{"type":["boolean","null"]},"expires_at":{"type":["string","null"]},"traffic_limit":{"type":["string","number","null"]},"limit_unit":{"type":["string","null"]},"traffic_reset_mode":{"type":["string","null"]},"traffic_reset_weekday":{"type":["integer","null"]},"traffic_reset_day":{"type":["integer","null"]},"traffic_reset_time":{"type":["string","null"]}`
	if update {
		t.InputSchema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"changes":{"type":"object","properties":{"name":{"type":["string","null"]},` + fields + `},"additionalProperties":false}},"required":["id","changes"],"additionalProperties":false}`)
	} else {
		t.InputSchema = json.RawMessage(`{"type":"object","properties":{"proxy_id":{"type":"integer"},"name":{"type":"string"},` + fields + `},"required":["proxy_id","name"],"additionalProperties":false}`)
	}
	return t
}

func (s *server) mcpClientMutation(ctx context.Context, identity mcpIdentity, id int64, method string, body any, handler mcpBusinessHandler) (any, error) {
	if err := s.requireMCPWrite(ctx, identity); err != nil {
		return nil, err
	}
	client, err := s.clientForUser(ctx, identity.user, id)
	if err != nil {
		return nil, errors.New("client not found")
	}
	proxy, err := s.proxyForUser(ctx, identity.user, client.ProxyID)
	if err != nil {
		return nil, errors.New("proxy not found")
	}
	result, err := s.mcpInvoke(ctx, identity.user, method, id, body, handler)
	if err != nil {
		return nil, err
	}
	return map[string]any{"saved": true, "client": result["client"], "agent_sync": s.mcpSync(ctx, identity.user, proxy.ServerID), "dependent_server_sync": "not_checked"}, nil
}

func (s *server) registerMCPP0ClientTools(ms *mcp.Server, identity mcpIdentity) {
	mcp.AddTool(ms, mcpDef("get_proxy_client", "Get accessible Client details without credentials."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		v, err := s.clientForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("client not found")
		}
		return nil, map[string]any{"client": toClientResponse(v)}, nil
	})
	mcp.AddTool(ms, mcpClientTool("create_proxy_client", "Create a Client using the Panel's validation; Agent synchronization is asynchronous.", false, false), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpCreateClient) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		p, err := s.proxyForUser(ctx, identity.user, in.ProxyID)
		if err != nil {
			return nil, nil, errors.New("proxy not found")
		}
		out, err := s.mcpInvoke(ctx, identity.user, "POST", in.ProxyID, mcpClientBody(in, "proxy_id"), s.createProxyClient)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"saved": true, "client": out["client"], "agent_sync": s.mcpSync(ctx, identity.user, p.ServerID)}, nil
	})
	mcp.AddTool(ms, mcpClientTool("update_proxy_client", "Update Client fields; omit unchanged values, and use null to clear expiration or traffic_limit.", true, true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpUpdateClient) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpClientMutation(ctx, identity, in.ID, "PATCH", in.Changes, s.updateProxyClient)
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("delete_proxy_client", "Delete a Client using existing dependent relay cleanup; expected_name must match.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.clientForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("client not found")
		}
		if v.SubscriptionManaged {
			return nil, nil, errors.New("subscription-managed client cannot be deleted here")
		}
		if in.ExpectedName == "" || in.ExpectedName != v.Name {
			return nil, nil, errors.New("client name does not match")
		}
		out, err := s.mcpClientMutation(ctx, identity, in.ID, "DELETE", nil, s.deleteProxyClient)
		if err != nil {
			return nil, nil, errors.New("deletion was rejected or partially applied; inspect the client and dependent relays before retrying: " + err.Error())
		}
		if err == nil {
			out.(map[string]any)["deleted"] = map[string]any{"id": v.ID, "name": v.Name}
		}
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("reset_proxy_client_traffic", "Reset one Client's traffic; expected_name must match.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpDelete) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.clientForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("client not found")
		}
		if in.ExpectedName == "" || in.ExpectedName != v.Name {
			return nil, nil, errors.New("client name does not match")
		}
		out, err := s.mcpClientMutation(ctx, identity, in.ID, "POST", nil, s.resetProxyClientTraffic)
		if err == nil {
			s.mcpAuditAction(ctx, identity, "client.traffic.reset", "client", in.ID)
		}
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("assign_proxy_client", "Assign or unassign a Client; expected_name must match and unassigning may remove dependent user relays.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpAssignClient) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		v, err := s.clientForUser(ctx, identity.user, in.ID)
		if err != nil {
			return nil, nil, errors.New("client not found")
		}
		if v.SubscriptionManaged {
			return nil, nil, errors.New("subscription-managed client cannot be assigned here")
		}
		if in.ExpectedName == "" || in.ExpectedName != v.Name {
			return nil, nil, errors.New("client name does not match")
		}
		body := mcpClientBody(in, "id")
		delete(body, "expected_name")
		out, err := s.mcpClientMutation(ctx, identity, in.ID, "PATCH", body, s.assignProxyClient)
		if err != nil {
			return nil, nil, errors.New("assignment was rejected or partially applied; inspect the client and dependent relays before retrying: " + err.Error())
		}
		if err == nil {
			s.mcpAuditAction(ctx, identity, "client.assign", "client", in.ID)
		}
		return nil, out, err
	})
	mcp.AddTool(ms, mcpWriteDef("set_client_relay_port_count", "Set the Client's reserved user relay port count, subject to current restrictions.", true), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpClientPorts) (*mcp.CallToolResult, any, error) {
		out, err := s.mcpClientMutation(ctx, identity, in.ID, "PATCH", mcpClientBody(in, "id"), s.updateClientRelayPortCount)
		if err == nil {
			s.mcpAuditAction(ctx, identity, "client.relay_ports", "client", in.ID)
		}
		return nil, out, err
	})
	mcp.AddTool(ms, mcpDef("get_proxy_client_share", "Explicitly retrieve a Client's credential-bearing share URI; requires read_write and write switch."), func(ctx context.Context, _ *mcp.CallToolRequest, in mcpResourceID) (*mcp.CallToolResult, any, error) {
		if err := s.requireMCPWrite(ctx, identity); err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInvoke(ctx, identity.user, "GET", in.ID, nil, s.getProxyClientShare)
		return nil, out, err
	})
}
