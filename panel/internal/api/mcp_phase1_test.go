package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func phase1Session(t *testing.T, handler http.Handler, cookie *http.Cookie) *mcp.ClientSession {
	t.Helper()
	issued := performRequest(t, handler, "POST", "/api/mcp/tokens", map[string]string{"name": "phase1"}, cookie)
	var body struct {
		Token string `json:"token"`
	}
	if issued.Code != 201 || json.Unmarshal(issued.Body.Bytes(), &body) != nil {
		t.Fatal("token creation failed")
	}
	if response := performRequest(t, handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_enabled": true}, cookie); response.Code != 200 {
		t.Fatal("enable MCP failed")
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	transport := mcpBearerTransport{base: http.DefaultTransport, token: body.Token}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "phase1-test", Version: "1"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: transport}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type phase1Page struct {
	Items []map[string]any `json:"items"`
	Total int              `json:"total"`
}

func phase1Call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, phase1Page) {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil || result.IsError {
		t.Fatalf("%s failed: %v %+v", name, err, result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var page phase1Page
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	return result, page
}

func TestMCPPhase1PaginationSubscriptionsAndReadOnly(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	first := createAccessTestServer(t, handler, accounts.adminCookie, "Page First", "private", nil)
	second := createAccessTestServer(t, handler, accounts.adminCookie, "Page Second", "private", nil)
	createAccessTestServer(t, handler, accounts.memberCookie, "Page Hidden", "private", nil)
	if _, err := db.Exec(`UPDATE servers SET created_at = 10 WHERE id IN (?, ?)`, first.Server.ID, second.Server.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE servers SET status = 'online' WHERE id = ?`, first.Server.ID); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		cookie *http.Cookie
		name   string
	}{{accounts.adminCookie, "Owned Personal"}, {accounts.memberCookie, "Other Personal"}} {
		response := performRequest(t, handler, "POST", "/api/personal-subscriptions", map[string]any{"name": v.name, "client_name": "client", "enabled": true}, v.cookie)
		if response.Code != 201 {
			t.Fatalf("personal setup: %d", response.Code)
		}
	}
	for _, query := range []string{
		`INSERT INTO users (id,username,password_hash,role,created_at,updated_at) VALUES (100,'subscriber-test','hash','subscriber',1,1)`,
		`INSERT INTO subscriber_profiles (user_id,enabled,subscription_token,created_at,updated_at) VALUES (100,1,'secret-subscriber-token',1,1)`,
		`INSERT INTO subscriber_usage (user_id,cycle_started_at,updated_at) VALUES (100,1,1)`,
		`INSERT INTO users (id,username,password_hash,role,created_at,updated_at) VALUES (101,'carpool-a','hash','carpool',1,1)`,
		`INSERT INTO users (id,username,password_hash,role,created_at,updated_at) VALUES (102,'carpool-b','hash','carpool',1,1)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	var personalToken string
	if err := db.QueryRow(`SELECT token FROM personal_subscription_groups WHERE owner_user_id = ?`, accounts.adminID).Scan(&personalToken); err != nil {
		t.Fatal(err)
	}
	session := phase1Session(t, handler, accounts.adminCookie)
	_, page := phase1Call(t, session, "list_servers", map[string]any{"limit": 1})
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0]["name"] != "Page Second" {
		t.Fatalf("first server page: %+v", page)
	}
	_, page = phase1Call(t, session, "list_servers", map[string]any{"limit": 1, "offset": 1})
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0]["name"] != "Page First" {
		t.Fatalf("second server page: %+v", page)
	}
	_, page = phase1Call(t, session, "list_servers", map[string]any{"status": "online"})
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0]["name"] != "Page First" {
		t.Fatalf("filtered servers: %+v", page)
	}
	result, page := phase1Call(t, session, "list_subscriptions", map[string]any{})
	raw, _ := json.Marshal(result)
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0]["type"] != "personal" || page.Items[1]["type"] != "subscriber" || page.Items[1]["name"] != "subscriber-test" || strings.Contains(string(raw), personalToken) || strings.Contains(string(raw), "secret-subscriber-token") || strings.Contains(string(raw), "Other Personal") || strings.Contains(string(raw), "subscription_url") {
		t.Fatal("subscription types, authorization or secret isolation failed")
	}
	_, page = phase1Call(t, session, "list_subscriptions", map[string]any{"limit": 1, "offset": 1})
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0]["type"] != "subscriber" {
		t.Fatalf("subscription page: %+v", page)
	}
	_, page = phase1Call(t, session, "list_carpool_nodes", map[string]any{"limit": 1, "offset": 1})
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0]["username"] != "carpool-b" {
		t.Fatalf("carpool page: %+v", page)
	}
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) != 99 {
		t.Fatalf("tool count: %v", err)
	}
	for _, tool := range tools.Tools {
		if strings.HasPrefix(tool.Name, "create_") || strings.HasPrefix(tool.Name, "update_") || strings.HasPrefix(tool.Name, "delete_") || strings.HasPrefix(tool.Name, "set_") || strings.HasPrefix(tool.Name, "reset_") || strings.HasPrefix(tool.Name, "assign_") || strings.HasPrefix(tool.Name, "upgrade_") || strings.HasPrefix(tool.Name, "clear_") || strings.HasPrefix(tool.Name, "regenerate_") || tool.Name == "preview_subscription_user_mihomo" {
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %s is not marked read-only", tool.Name)
		}
	}
	for _, name := range []string{"list_proxies", "list_relays", "check_relay_health"} {
		_, page = phase1Call(t, session, name, map[string]any{"limit": 1, "offset": 100})
		if len(page.Items) != 0 {
			t.Fatalf("%s offset beyond end", name)
		}
	}
	// MCP audit and token last-used timestamps are intentionally excluded.
	const snapshotSQL = `SELECT
	 (SELECT json_group_array(json_array(id, name, status, updated_at)) FROM servers),
	 (SELECT json_group_array(json_array(id, name, enabled, updated_at)) FROM proxies),
	 (SELECT json_group_array(json_array(id, name, enabled, updated_at)) FROM relays),
	 (SELECT json_group_array(json_array(id, name, enabled, updated_at)) FROM personal_subscription_groups),
	 (SELECT json_group_array(json_array(user_id, enabled, subscription_token, updated_at)) FROM subscriber_profiles)`
	snapshot := func() [5]string {
		t.Helper()
		var v [5]string
		if err := db.QueryRow(snapshotSQL).Scan(&v[0], &v[1], &v[2], &v[3], &v[4]); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := snapshot()
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_servers", map[string]any{}}, {"get_server_details", map[string]any{"server_id": first.Server.ID}},
		{"get_server_metrics", map[string]any{"server_id": first.Server.ID}}, {"get_latency_history", map[string]any{"server_id": first.Server.ID, "hours": 1}},
		{"list_proxies", map[string]any{}}, {"list_relays", map[string]any{}}, {"check_relay_health", map[string]any{}},
		{"list_subscriptions", map[string]any{}}, {"list_carpool_nodes", map[string]any{}}, {"get_panel_info", map[string]any{}},
	} {
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil || result.IsError {
			t.Fatalf("tool %s failed: %v", tc.name, err)
		}
	}
	if before != snapshot() {
		t.Fatal("read-only tools changed business data")
	}
}

func TestMCPPhase1RelayHealthMissingAndHiddenTargets(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	source := createAccessTestServer(t, handler, accounts.adminCookie, "Health Source", "private", nil)
	hidden := createAccessTestServer(t, handler, accounts.memberCookie, "Private Target Server", "private", nil)
	response := performRequest(t, handler, "POST", "/api/proxies", createProxyRequest{ServerID: hidden.Server.ID, Name: "Private Target Proxy", ListenPort: 8444, Security: "reality", EntryHostMode: "manual", EntryHost: "private-target.example.com", ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "client"}, accounts.memberCookie)
	var proxy struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &proxy) != nil {
		t.Fatalf("proxy setup: %d", response.Code)
	}
	visibleResponse := performRequest(t, handler, "POST", "/api/proxies", createProxyRequest{ServerID: source.Server.ID, Name: "Visible Target", ListenPort: 8445, Security: "reality", EntryHostMode: "manual", EntryHost: "node.example.com", ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "client"}, accounts.adminCookie)
	var visibleProxy struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if visibleResponse.Code != 201 || json.Unmarshal(visibleResponse.Body.Bytes(), &visibleProxy) != nil {
		t.Fatal("visible proxy setup failed")
	}
	insert := `INSERT INTO relays (server_id,name,listen_port,target_type,target_proxy_id,network,enabled,created_at,updated_at) VALUES (?,?,?,'proxy',?,'tcp',1,1,1)`
	if _, err := db.Exec(insert, source.Server.ID, "Incompatible Relay", 9500, visibleProxy.Proxy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE relays SET network = 'udp' WHERE name = 'Incompatible Relay'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, source.Server.ID, "Hidden Target Relay", 9501, proxy.Proxy.ID); err != nil {
		t.Fatal(err)
	}
	// Simulate an inconsistent legacy database; normal foreign keys restrict deletion.
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, source.Server.ID, "Missing Target Relay", 9502, 999999); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, hidden.Server.ID, "Hidden Source Relay", 9503, 999999); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE relays SET enabled = 0 WHERE name = 'Missing Target Relay'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, hidden.Server.ID, "Another Invalid Relay", 9504, 999999); err == nil {
		t.Fatal("foreign keys should prevent missing target")
	}
	session := phase1Session(t, handler, accounts.adminCookie)
	result, page := phase1Call(t, session, "check_relay_health", map[string]any{})
	raw, _ := json.Marshal(result)
	text := string(raw)
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0]["name"] != "Missing Target Relay" || !strings.Contains(text, "target_unavailable") || !strings.Contains(text, "disabled") || !strings.Contains(text, "source_not_online") || !strings.Contains(text, "unknown") || !strings.Contains(text, "network_incompatible") {
		t.Fatalf("missing relay diagnosis: %s", text)
	}
	for _, forbidden := range []string{"Hidden Target Relay", "Hidden Source Relay", "Private Target Server", "Private Target Proxy", "private-target.example.com"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("relay target information leaked")
		}
	}
	_, page = phase1Call(t, session, "check_relay_health", map[string]any{"limit": 1, "offset": 1})
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0]["name"] != "Incompatible Relay" {
		t.Fatalf("relay health pagination: %+v", page)
	}
	_, page = phase1Call(t, session, "list_relays", map[string]any{"limit": 1})
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0]["name"] != "Incompatible Relay" {
		t.Fatalf("relay ACL or pagination: %+v", page)
	}
	_, page = phase1Call(t, session, "list_proxies", map[string]any{"limit": 1})
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0]["name"] != "Visible Target" {
		t.Fatalf("proxy ACL or pagination: %+v", page)
	}
	if _, err := db.Exec(`UPDATE servers SET archived_at = 1 WHERE id = ?`, hidden.Server.ID); err != nil {
		t.Fatal(err)
	}
	result, page = phase1Call(t, session, "check_relay_health", map[string]any{})
	raw, _ = json.Marshal(result)
	if page.Total != 2 || strings.Contains(string(raw), "Hidden Target Relay") {
		t.Fatal("archived inaccessible target exposed")
	}
	var missingID int64
	if err := db.QueryRow(`SELECT id FROM relays WHERE name = 'Missing Target Relay'`).Scan(&missingID); err != nil {
		t.Fatal(err)
	}
	_, page = phase1Call(t, session, "check_relay_health", map[string]any{"relay_id": missingID, "limit": 1})
	if page.Total != 1 {
		t.Fatal("specific missing relay was not diagnosable")
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "check_relay_health", Arguments: map[string]any{"relay_id": 999999}})
	if err != nil || !result.IsError {
		t.Fatal("nonexistent relay should not be visible")
	}
}
