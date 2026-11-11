package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

type mcpPage struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}
type mcpServerID struct {
	ServerID int64 `json:"server_id"`
}
type mcpEmpty struct{}

func mcpLimit(limit, offset int) (int, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		return 0, errors.New("invalid pagination")
	}
	return limit, nil
}

func mcpBounds(limit, offset, n int) (int, int, error) {
	limit, err := mcpLimit(limit, offset)
	if err != nil {
		return 0, 0, err
	}
	start := min(offset, n)
	return start, min(start+limit, n), nil
}
func mcpDef(name, description string) *mcp.Tool {
	return &mcp.Tool{Name: name, Description: description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}
}
func (s *server) mcpServer(identity mcpIdentity, requestID string) *mcp.Server {
	ms := mcp.NewServer(&mcp.Implementation{Name: "vps-panel", Version: s.panelVersion}, nil)
	ms.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			name := "unknown"
			if call, ok := req.(*mcp.CallToolRequest); ok && call != nil && call.Params != nil {
				switch call.Params.Name {
				case "list_servers", "get_server_details", "get_server_metrics", "get_latency_history", "list_proxies", "list_relays", "check_relay_health", "list_subscriptions", "list_carpool_nodes", "get_panel_info", "create_server", "update_server", "set_server_order", "create_proxy", "update_proxy", "set_proxy_enabled", "delete_proxy", "create_relay", "update_relay", "set_relay_enabled", "delete_relay", "list_proxy_clients", "set_proxy_order", "set_relay_order", "set_landing_order", "set_user_order", "set_personal_subscription_order", "set_published_node_order", "list_landings", "list_users", "list_personal_subscriptions", "list_published_nodes", "get_proxy_client", "create_proxy_client", "update_proxy_client", "delete_proxy_client", "reset_proxy_client_traffic", "assign_proxy_client", "set_client_relay_port_count", "get_proxy_client_share", "get_landing", "create_landing", "update_landing", "delete_landing", "get_landing_share", "get_agent_status", "create_agent_enrollment", "upgrade_server_agent", "get_server_access", "update_server_access", "get_server_traffic", "set_server_traffic_adjustment", "clear_server_traffic_adjustment", "get_personal_subscription", "create_personal_subscription", "update_personal_subscription", "delete_personal_subscription", "list_personal_subscription_sources", "set_personal_subscription_nodes", "set_personal_subscription_routing_bindings", "preview_personal_subscription_mihomo", "preview_personal_subscription_shadowrocket", "regenerate_personal_subscription_token":
					name = call.Params.Name
				case "list_subscription_plans", "get_subscription_plan", "create_subscription_plan", "update_subscription_plan", "delete_subscription_plan", "set_subscription_plan_nodes", "set_subscription_plan_routing_bindings":
					name = call.Params.Name
				case "get_published_node", "create_published_node", "update_published_node", "delete_published_node", "list_subscription_relay_servers":
					name = call.Params.Name
				case "list_subscription_users", "get_subscription_user", "update_subscription_user", "reset_subscription_user_traffic", "regenerate_subscription_user_token", "preview_subscription_user_mihomo", "list_routing_presets", "get_routing_preset", "create_routing_preset", "update_routing_preset", "delete_routing_preset", "list_subscription_templates", "get_subscription_template", "create_subscription_template", "update_subscription_template", "delete_subscription_template", "get_builtin_mihomo_configuration", "get_builtin_shadowrocket_configuration", "get_effective_mihomo_configuration":
					name = call.Params.Name
				case "get_routing_ini", "validate_routing_ini", "preview_routing_ini", "create_routing_from_ini", "update_routing_from_ini":
					name = call.Params.Name
				}
			}
			start := time.Now()
			result, err := next(ctx, method, req)
			success := err == nil
			if result, ok := result.(*mcp.CallToolResult); ok && result != nil && result.IsError {
				success = false
			}
			code := ""
			if !success {
				code = "tool_error"
			}
			if _, auditErr := s.db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO mcp_audit_logs(token_id,owner_user_id,tool_name,called_at,duration_ms,success,error_code,request_id) VALUES(?,?,?,?,?,?,?,?)`, identity.tokenID, identity.user.ID, name, start.Unix(), time.Since(start).Milliseconds(), success, code, requestID); auditErr != nil {
				log.Printf("MCP audit write failed request_id=%s", requestID)
			}
			return result, err
		}
	})
	s.registerMCPTools(ms, identity)
	s.registerMCPOrderLists(ms, identity)
	s.registerMCPPersonalSubscriptions(ms, identity)
	s.registerMCPSubscriptionPlans(ms, identity)
	s.registerMCPSubscriptionNodes(ms, identity)
	s.registerMCPSubscriptionUsers(ms, identity)
	s.registerMCPSubscriptionRouting(ms, identity)
	s.registerMCPRoutingINI(ms, identity)
	s.registerMCPSubscriptionTemplates(ms, identity)
	s.registerMCPWriteTools(ms, identity)
	return ms
}
func (s *server) serveMCP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" && !s.validateSessionRequestOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	select {
	case s.mcpConcurrent <- struct{}{}:
		defer func() { <-s.mcpConcurrent }()
	default:
		http.Error(w, "Busy", http.StatusTooManyRequests)
		return
	}
	ip := clientIP(r)
	if ok, _ := s.mcpRequestLimiter.Take(ip, "mcp", time.Now()); !ok {
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}
	if ok, _ := s.mcpLimiter.Allow(ip, "mcp", time.Now()); !ok {
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}
	identity, err := s.authenticateMCP(r)
	if err != nil {
		s.mcpLimiter.RecordFailure(ip, "mcp", time.Now())
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	ms := s.mcpServer(identity, requestIDFromContext(r.Context()))
	// The standard deployment uses Caddy to proxy the configured public domain
	// to a loopback listener. Retain SDK localhost protection for every other Host.
	trustedProxyHost := validPanelDomain(s.backup.Domain) &&
		(strings.EqualFold(r.Host, s.backup.Domain) || strings.EqualFold(r.Host, s.backup.Domain+":443")) &&
		requestPeerIP(r.RemoteAddr).IsLoopback()
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 64 << 10, DisableLocalhostProtection: trustedProxyHost})
	w.Header().Set("Cache-Control", "no-store")
	h.ServeHTTP(w, r)
}
func (s *server) mcpVisibleServer(ctx context.Context, user auth.User, id int64) (serverstore.Server, error) {
	if id <= 0 {
		return serverstore.Server{}, errors.New("invalid server ID")
	}
	ok, err := s.canAccessServer(ctx, user, id)
	if err != nil || !ok {
		return serverstore.Server{}, errors.New("server not found")
	}
	v, err := s.servers.Get(ctx, id)
	if err != nil {
		return serverstore.Server{}, errors.New("server not found")
	}
	return v, nil
}
func mcpServerSummary(v serverstore.Server) map[string]any {
	country := ""
	if v.SystemInfo != nil {
		country = v.SystemInfo.CountryCode
	}
	if v.CountryOverrideCode != nil {
		country = *v.CountryOverrideCode
	}
	return map[string]any{"id": v.ID, "name": v.Name, "status": v.Status, "country_code": country, "bandwidth_mbps": v.BandwidthMbps, "network_stack": v.NetworkStack, "expires_at": v.ExpiresAt, "auto_renew": v.AutoRenew, "updated_at": v.UpdatedAt}
}
func (s *server) mcpAuditLogs(w http.ResponseWriter, r *http.Request, _ auth.User) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT a.id,t.name,a.tool_name,a.called_at,a.duration_ms,a.success,a.error_code FROM mcp_audit_logs a JOIN mcp_tokens t ON t.id=a.token_id ORDER BY a.id DESC LIMIT 100`)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, at, duration int64
		var name, tool, code string
		var success bool
		if err := rows.Scan(&id, &name, &tool, &at, &duration, &success, &code); err != nil {
			writeInternalError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "token_name": name, "tool_name": tool, "called_at": at, "duration_ms": duration, "success": success, "error_code": code})
	}
	if err := rows.Err(); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"logs": items})
}
