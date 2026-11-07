package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpBearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t mcpBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(r)
}
func TestMCPTokenAndSDK(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	created := performRequest(t, handler, "POST", "/api/mcp/tokens", map[string]string{"name": "Codex", "scope": "read"}, cookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var body struct {
		Token string `json:"token"`
		Item  struct {
			ID int64 `json:"id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil || body.Token == "" {
		t.Fatal("missing token", err)
	}
	list := performRequest(t, handler, "GET", "/api/mcp/tokens", nil, cookie)
	if strings.Contains(list.Body.String(), body.Token) || strings.Contains(list.Body.String(), "token_hash") {
		t.Fatal("secret exposed by list")
	}
	var stored []byte
	if err := db.QueryRow(`SELECT token_hash FROM mcp_tokens WHERE id=?`, body.Item.ID).Scan(&stored); err != nil || string(stored) == body.Token {
		t.Fatal("invalid token storage", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	clientHTTP := &http.Client{Transport: mcpBearerTransport{base: http.DefaultTransport, token: body.Token}}
	protocol := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	connect := func() (*mcp.ClientSession, error) {
		return protocol.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: clientHTTP, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	}
	if session, err := connect(); err == nil {
		session.Close()
		t.Fatal("disabled MCP accepted token")
	}
	settings := performRequest(t, handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_enabled": true}, cookie)
	if settings.Code != 200 {
		t.Fatal(settings.Body.String())
	}
	session, err := connect()
	if err != nil {
		t.Fatalf("SDK connect: %v", err)
	}
	defer session.Close()
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) < 9 {
		t.Fatalf("tools list: %+v %v", tools, err)
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_servers", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("tool call: %+v %v", result, err)
	}
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "does_not_exist", Arguments: map[string]any{}}); err == nil {
		t.Fatal("unknown tool accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM mcp_audit_logs WHERE tool_name='list_servers' AND success=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit = %d %v", count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM mcp_audit_logs WHERE tool_name='unknown' AND success=0`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed call audit = %d %v", count, err)
	}
	revoked := performRequest(t, handler, "DELETE", "/api/mcp/tokens/"+strconv.FormatInt(body.Item.ID, 10), nil, cookie)
	if revoked.Code != 204 {
		t.Fatal(revoked.Body.String())
	}
	if _, err := session.ListTools(t.Context(), nil); err == nil {
		t.Fatal("revoked token accepted")
	}
	if err := db.QueryRow(`SELECT count(*) FROM mcp_audit_logs WHERE token_id=?`, body.Item.ID).Scan(&count); err != nil || count == 0 {
		t.Fatal("audit lost after revoke", err)
	}
}

func TestMCPEnforcesServerACLAndOwnerRole(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	visible := createAccessTestServer(t, handler, accounts.adminCookie, "MCP Visible", "private", nil)
	hidden := createAccessTestServer(t, handler, accounts.memberCookie, "MCP Secret", "private", nil)
	createProxy := func(cookie *http.Cookie, id int64, name string, port int) proxyResponse {
		response := performRequest(t, handler, "POST", "/api/proxies", createProxyRequest{ServerID: id, Name: name, ListenPort: port, Security: "reality", EntryHostMode: "manual", EntryHost: "node.example.com", ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Client"}, cookie)
		if response.Code != 201 {
			t.Fatalf("create proxy: %d", response.Code)
		}
		var payload struct {
			Proxy proxyResponse `json:"proxy"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Proxy
	}
	visibleProxy := createProxy(accounts.adminCookie, visible.Server.ID, "Visible Proxy", 8443)
	hiddenProxy := createProxy(accounts.memberCookie, hidden.Server.ID, "Hidden Proxy", 8444)
	for _, tc := range []struct {
		cookie *http.Cookie
		server int64
		proxy  proxyResponse
		name   string
	}{{accounts.adminCookie, visible.Server.ID, visibleProxy, "Visible Relay"}, {accounts.memberCookie, hidden.Server.ID, hiddenProxy, "Hidden Relay"}} {
		clientID := tc.proxy.Clients[0].ID
		response := performRequest(t, handler, "POST", "/api/relays", createRelayRequest{ServerID: tc.server, Name: tc.name, ListenPort: 9500, TargetType: "proxy", TargetProxyID: &tc.proxy.ID, TargetClientID: &clientID, Network: "tcp"}, tc.cookie)
		if response.Code != 201 {
			t.Fatalf("create relay: %d %s", response.Code, response.Body.String())
		}
	}
	for _, tc := range []struct {
		cookie *http.Cookie
		name   string
	}{{accounts.adminCookie, "Visible Subscription"}, {accounts.memberCookie, "Hidden Subscription"}} {
		response := performRequest(t, handler, "POST", "/api/personal-subscriptions", map[string]any{"name": tc.name, "client_name": "client", "enabled": true}, tc.cookie)
		if response.Code != 201 {
			t.Fatalf("create subscription: %d %s", response.Code, response.Body.String())
		}
	}
	var configJSON, credentialJSON string
	if err := db.QueryRow(`SELECT p.config_json,c.credential_json FROM proxies p JOIN clients c ON c.proxy_id=p.id WHERE p.name='Visible Proxy'`).Scan(&configJSON, &credentialJSON); err != nil {
		t.Fatal(err)
	}
	var config struct {
		Reality struct {
			PrivateKey string `json:"private_key"`
		} `json:"reality"`
	}
	var credential struct {
		UUID string `json:"uuid"`
	}
	if json.Unmarshal([]byte(configJSON), &config) != nil || json.Unmarshal([]byte(credentialJSON), &credential) != nil || config.Reality.PrivateKey == "" || credential.UUID == "" {
		t.Fatal("test credentials missing")
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{{"GET", "/api/mcp/tokens", nil}, {"POST", "/api/mcp/tokens", map[string]string{"name": "bad"}}, {"PATCH", "/api/mcp/settings", map[string]bool{"mcp_enabled": true}}, {"DELETE", "/api/mcp/tokens/1", nil}} {
		response := performRequest(t, handler, tc.method, tc.path, tc.body, accounts.memberCookie)
		if response.Code != http.StatusForbidden {
			t.Fatalf("nonadmin %s %s: %d", tc.method, tc.path, response.Code)
		}
	}
	response := performRequest(t, handler, "POST", "/api/mcp/tokens", map[string]string{"name": "ACL"}, accounts.adminCookie)
	var issued struct {
		Token string `json:"token"`
	}
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &issued) != nil {
		t.Fatal(response.Body.String())
	}
	if got := performRequest(t, handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_enabled": true}, accounts.adminCookie); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	transport := mcpBearerTransport{base: http.DefaultTransport, token: issued.Token}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: transport}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_servers", Arguments: map[string]any{}})
	raw, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), "MCP Visible") || strings.Contains(string(raw), "MCP Secret") {
		t.Fatalf("ACL list %s %v", raw, err)
	}
	result, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_proxies", Arguments: map[string]any{}})
	raw, _ = json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), "Visible Proxy") || strings.Contains(string(raw), "Hidden Proxy") || strings.Contains(string(raw), config.Reality.PrivateKey) || strings.Contains(string(raw), credential.UUID) || strings.Contains(string(raw), issued.Token) {
		t.Fatal("proxy ACL or secret isolation failed")
	}
	for _, tc := range []struct{ tool, visible, hidden string }{{"list_relays", "Visible Relay", "Hidden Relay"}, {"list_subscriptions", "Visible Subscription", "Hidden Subscription"}} {
		result, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.tool, Arguments: map[string]any{}})
		raw, _ = json.Marshal(result)
		if err != nil || result.IsError || !strings.Contains(string(raw), tc.visible) || strings.Contains(string(raw), tc.hidden) || strings.Contains(string(raw), credential.UUID) || strings.Contains(string(raw), config.Reality.PrivateKey) {
			t.Fatalf("%s ACL or secret isolation failed", tc.tool)
		}
	}
	result, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_server_details", Arguments: map[string]any{"server_id": hidden.Server.ID}})
	if err != nil || !result.IsError {
		t.Fatalf("private server accepted: %+v %v", result, err)
	}
	result, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_server_details", Arguments: map[string]any{"server_id": visible.Server.ID}})
	if err != nil || result.IsError {
		t.Fatalf("visible server denied: %+v %v", result, err)
	}
	req, _ := http.NewRequest("GET", server.URL+"/api/mcp/tokens", nil)
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("bearer accessed management: %d", resp.StatusCode)
	}
	if _, err := db.Exec(`UPDATE users SET role='vip' WHERE id=?`, accounts.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := session.ListTools(t.Context(), nil); err == nil {
		t.Fatal("demoted owner token accepted")
	}
}

func TestMCPOwnerExpiryAndTokenScope(t *testing.T) {
	db, handler, cookie := monitorTestAPI(t)
	bad := performRequest(t, handler, "POST", "/api/mcp/tokens", map[string]string{"name": "write", "scope": "write"}, cookie)
	if bad.Code != 400 {
		t.Fatalf("write scope accepted: %d", bad.Code)
	}
	issued := performRequest(t, handler, "POST", "/api/mcp/tokens", map[string]string{"name": "expires"}, cookie)
	var body struct {
		Token string `json:"token"`
		Item  struct {
			ID int64 `json:"id"`
		} `json:"item"`
	}
	if issued.Code != 201 || json.Unmarshal(issued.Body.Bytes(), &body) != nil {
		t.Fatal(issued.Body.String())
	}
	if response := performRequest(t, handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_enabled": true}, cookie); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	request := func(token string) int {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if status := request(""); status != 401 {
		t.Fatalf("no token: %d", status)
	}
	if status := request("wrong"); status != 401 {
		t.Fatalf("invalid token: %d", status)
	}
	if status := request(body.Token); status != 200 {
		t.Fatalf("valid token: %d", status)
	}
	if _, err := db.Exec(`UPDATE mcp_tokens SET expires_at=0 WHERE id=?`, body.Item.ID); err != nil {
		t.Fatal(err)
	}
	if status := request(body.Token); status != 401 {
		t.Fatalf("expired token: %d", status)
	}
	if _, err := db.Exec(`UPDATE mcp_tokens SET expires_at=4102444800 WHERE id=?`, body.Item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET role='vip' WHERE username='admin'`); err != nil {
		t.Fatal(err)
	}
	if status := request(body.Token); status != 401 {
		t.Fatalf("demoted owner: %d", status)
	}
}

func TestMCPProxyHostProtection(t *testing.T) {
	db, base, cookie := monitorTestAPI(t)
	issued := performRequest(t, base, "POST", "/api/mcp/tokens", map[string]string{"name": "proxy"}, cookie)
	var body struct {
		Token string `json:"token"`
	}
	if issued.Code != 201 || json.Unmarshal(issued.Body.Bytes(), &body) != nil {
		t.Fatal("issue failed")
	}
	if response := performRequest(t, base, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_enabled": true}, cookie); response.Code != 200 {
		t.Fatal("enable failed")
	}
	server := httptest.NewServer(NewHandlerWithBackup(db, t.TempDir(), "test", BackupConfig{Domain: "panel.example.com"}))
	defer server.Close()
	request := func(host string) int {
		r, _ := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		r.Host = host
		r.Header.Set("Authorization", "Bearer "+body.Token)
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if status := request("panel.example.com"); status != 200 {
		t.Fatalf("trusted proxy host: %d", status)
	}
	if status := request("attacker.example.com"); status != 403 {
		t.Fatalf("untrusted proxy host: %d", status)
	}
}
