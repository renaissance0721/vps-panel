package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type writeFixture struct {
	db           *sql.DB
	handler      http.Handler
	cookie       *http.Cookie
	memberCookie *http.Cookie
	session      *mcp.ClientSession
	token        string
	tokenID      int64
	serverID     int64
	hiddenID     int64
}

func TestMCPServerOrderPaginationPermissionsAndWebCompatibility(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	second := createAccessTestServer(t, f.handler, f.cookie, "Second", "private", nil).Server.ID
	archived := createAccessTestServer(t, f.handler, f.cookie, "Archived", "private", nil).Server.ID
	if _, err := f.db.Exec(`UPDATE servers SET status='online', created_at=10 WHERE id IN (?,?)`, f.serverID, second); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE servers SET archived_at=1 WHERE id=?`, archived); err != nil {
		t.Fatal(err)
	}
	var owner int64
	if err := f.db.QueryRow(`SELECT owner_user_id FROM mcp_tokens WHERE id=?`, f.tokenID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO user_server_order(user_id,server_id,position) VALUES (?, ?, 1), (?, ?, 2)`, owner, f.hiddenID, owner, archived); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := f.db.QueryRow(`SELECT json_group_array(json_array(id,desired_state_version,updated_at)) FROM servers`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	read := phase1Session(t, f.handler, f.cookie)
	readResult, err := read.CallTool(t.Context(), &mcp.CallToolParams{Name: "set_server_order", Arguments: map[string]any{"server_ids": []int64{f.serverID, second}}})
	if err != nil || !readResult.IsError {
		t.Fatal("read token reordered servers")
	}
	for _, ids := range [][]int64{{f.serverID}, {f.serverID, f.serverID}, {f.serverID, f.hiddenID}, {f.serverID, archived}, {f.serverID, 999999}} {
		f.call(t, "set_server_order", map[string]any{"server_ids": ids}, false)
	}
	args := map[string]any{"server_ids": []int64{f.serverID, second}, "dry_run": true}
	preview := f.call(t, "set_server_order", args, true)
	if preview["saved"] != false || preview["dry_run"] != true {
		t.Fatal("incorrect preview")
	}
	_, originalPage := phase1Call(t, f.session, "list_servers", map[string]any{"limit": 1})
	if originalPage.Total != 2 || int64(originalPage.Items[0]["id"].(float64)) != second {
		t.Fatal("preview modified order")
	}
	delete(args, "dry_run")
	out := f.call(t, "set_server_order", args, true)
	if out["saved"] != true || !reflect.DeepEqual(out["server_ids"], []any{float64(f.serverID), float64(second)}) {
		t.Fatalf("unexpected saved order: %+v", out)
	}
	for offset, id := range []int64{f.serverID, second} {
		_, page := phase1Call(t, f.session, "list_servers", map[string]any{"limit": 1, "offset": offset})
		if page.Total != 2 || len(page.Items) != 1 || int64(page.Items[0]["id"].(float64)) != id {
			t.Fatalf("page %d: %+v", offset, page)
		}
	}
	_, filtered := phase1Call(t, f.session, "list_servers", map[string]any{"status": "online", "limit": 1, "offset": 1})
	if filtered.Total != 2 || len(filtered.Items) != 1 || int64(filtered.Items[0]["id"].(float64)) != second {
		t.Fatalf("filtered page: %+v", filtered)
	}
	var preserved int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM user_server_order WHERE user_id=? AND server_id IN (?,?)`, owner, f.hiddenID, archived).Scan(&preserved); err != nil || preserved != 2 {
		t.Fatal("hidden or archived preference lost")
	}
	response := performRequest(t, f.handler, "POST", "/api/servers/"+strconv.FormatInt(second, 10)+"/reorder", map[string]any{"direction": "up"}, f.cookie)
	if response.Code != http.StatusNoContent {
		t.Fatalf("web reorder: %d %s", response.Code, response.Body.String())
	}
	_, page := phase1Call(t, f.session, "list_servers", map[string]any{"limit": 1})
	if int64(page.Items[0]["id"].(float64)) != second {
		t.Fatal("MCP order did not follow web drag")
	}
	var after string
	if err := f.db.QueryRow(`SELECT json_group_array(json_array(id,desired_state_version,updated_at)) FROM servers`).Scan(&after); err != nil || before != after {
		t.Fatal("ordering changed server business state")
	}
	third := createAccessTestServer(t, f.handler, f.cookie, "Unordered New", "private", nil).Server.ID
	for offset, id := range []int64{third, second, f.serverID} {
		_, page := phase1Call(t, f.session, "list_servers", map[string]any{"limit": 1, "offset": offset})
		if page.Total != 3 || len(page.Items) != 1 || int64(page.Items[0]["id"].(float64)) != id {
			t.Fatalf("unordered server page %d: %+v", offset, page)
		}
	}
}

func newWriteFixture(t *testing.T) (*writeFixture, func()) {
	t.Helper()
	db, h, accounts := setupAccessTest(t)
	server := createAccessTestServer(t, h, accounts.adminCookie, "Visible Server", "private", nil)
	hidden := createAccessTestServer(t, h, accounts.memberCookie, "Hidden Server", "private", nil)
	issue := performRequest(t, h, "POST", "/api/mcp/tokens", map[string]any{"name": "writer", "scope": "read_write"}, accounts.adminCookie)
	var b struct {
		Token string `json:"token"`
		Item  struct {
			ID int64 `json:"id"`
		} `json:"item"`
	}
	if issue.Code != 201 || json.Unmarshal(issue.Body.Bytes(), &b) != nil {
		t.Fatalf("issue writer %d", issue.Code)
	}
	if w := performRequest(t, h, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_enabled": true, "mcp_write_enabled": true}, accounts.adminCookie); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	srv := httptest.NewServer(h)
	transport := mcpBearerTransport{base: http.DefaultTransport, token: b.Token}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: transport}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &writeFixture{db: db, handler: h, cookie: accounts.adminCookie, memberCookie: accounts.memberCookie, session: session, token: b.Token, tokenID: b.Item.ID, serverID: server.Server.ID, hiddenID: hidden.Server.ID}
	return f, func() { session.Close(); srv.Close(); db.Close() }
}
func (f *writeFixture) call(t *testing.T, name string, args map[string]any, success bool) map[string]any {
	t.Helper()
	r, err := f.session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if r.IsError == success {
		t.Fatalf("%s error=%v content=%+v", name, r.IsError, r.Content)
	}
	if !success {
		return nil
	}
	raw, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func objectID(t *testing.T, v map[string]any, key string) int64 {
	t.Helper()
	x, ok := v[key].(map[string]any)
	if !ok {
		t.Fatalf("missing %s: %+v", key, v)
	}
	n, ok := x["id"].(float64)
	if !ok {
		t.Fatalf("missing id: %+v", x)
	}
	return int64(n)
}
func TestMCPDirectServerAndDryRun(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	args := map[string]any{"server_id": f.serverID, "name": "Renamed", "dry_run": true}
	out := f.call(t, "update_server", args, true)
	if out["dry_run"] != true || out["before"] != "Visible Server" {
		t.Fatal("invalid preview")
	}
	var name string
	if err := f.db.QueryRow(`SELECT name FROM servers WHERE id=?`, f.serverID).Scan(&name); err != nil || name != "Visible Server" {
		t.Fatal("dry run changed state")
	}
	args["dry_run"] = false
	f.call(t, "update_server", args, true)
	if err := f.db.QueryRow(`SELECT name FROM servers WHERE id=?`, f.serverID).Scan(&name); err != nil || name != "Renamed" {
		t.Fatal("rename not applied")
	}
	f.call(t, "update_server", map[string]any{"server_id": f.serverID, "bandwidth_mbps": 100}, true)
	var bandwidth int64
	if err := f.db.QueryRow(`SELECT bandwidth_mbps FROM servers WHERE id=?`, f.serverID).Scan(&bandwidth); err != nil || bandwidth != 100 {
		t.Fatal("metadata update failed")
	}
	f.call(t, "update_server", map[string]any{"server_id": f.serverID, "bandwidth_mbps": 200, "dry_run": true}, false)
	if err := f.db.QueryRow(`SELECT bandwidth_mbps FROM servers WHERE id=?`, f.serverID).Scan(&bandwidth); err != nil || bandwidth != 100 {
		t.Fatal("unsupported preview changed data")
	}
	f.call(t, "update_server", map[string]any{"server_id": f.hiddenID, "name": "leak"}, false)
	created := f.call(t, "create_server", map[string]any{"name": "Created", "visibility": "private"}, true)
	_ = objectID(t, created, "server")
	raw, _ := json.Marshal(created)
	if strings.Contains(string(raw), "token") || strings.Contains(string(raw), "vpsmcp_") {
		t.Fatal("credential leaked")
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM mcp_audit_logs WHERE tool_name='update_server' AND success=1`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("audit %d %v", count, err)
	}
}
func TestMCPDirectProxyRelayLifecycle(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	proxy := f.call(t, "create_proxy", map[string]any{"server_id": f.serverID, "name": "Test Proxy", "listen_port": 8443, "security": "reality", "entry_host_mode": "manual", "entry_host": "node.example.com", "server_name": "www.example.com", "reality_target": "www.example.com:443", "first_client_name": "client"}, true)
	proxyID := objectID(t, proxy, "proxy")
	f.call(t, "set_proxy_enabled", map[string]any{"id": proxyID}, false)
	f.call(t, "delete_proxy", map[string]any{"id": proxyID, "expected_name": "Test Proxy", "dry_run": true}, false)
	var stillExists int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM proxies WHERE id=?`, proxyID).Scan(&stillExists); err != nil || stillExists != 1 {
		t.Fatal("invalid or unsupported preview changed proxy")
	}
	if proxy["saved"] != true || proxy["agent_sync"] == nil {
		t.Fatal("missing sync result")
	}
	list := f.call(t, "list_proxy_clients", map[string]any{"id": proxyID}, true)
	items := list["items"].([]any)
	clientID := int64(items[0].(map[string]any)["id"].(float64))
	relay := f.call(t, "create_relay", map[string]any{"server_id": f.serverID, "name": "Test Relay", "listen_port": 9500, "target_type": "proxy", "target_proxy_id": proxyID, "target_client_id": clientID, "network": "tcp"}, true)
	relayID := objectID(t, relay, "relay")
	_, relayPage := phase1Call(t, f.session, "list_relays", map[string]any{})
	if len(relayPage.Items) != 1 || int64(relayPage.Items[0]["server_id"].(float64)) != f.serverID || int64(relayPage.Items[0]["target_proxy_id"].(float64)) != proxyID {
		t.Fatalf("relay relationship identifiers missing: %+v", relayPage)
	}
	f.call(t, "update_relay", map[string]any{"id": relayID, "changes": map[string]any{"name": "Relay Renamed"}}, true)
	f.call(t, "set_relay_enabled", map[string]any{"id": relayID, "enabled": false}, true)
	f.call(t, "delete_relay", map[string]any{"id": relayID, "expected_name": "wrong"}, false)
	f.call(t, "delete_relay", map[string]any{"id": relayID, "expected_name": "Relay Renamed"}, true)
	f.call(t, "update_proxy", map[string]any{"id": proxyID, "changes": map[string]any{"name": "Proxy Renamed"}}, true)
	f.call(t, "set_proxy_enabled", map[string]any{"id": proxyID, "enabled": false}, true)
	f.call(t, "delete_proxy", map[string]any{"id": proxyID, "expected_name": "Proxy Renamed"}, true)
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM proxies WHERE id=?`, proxyID).Scan(&count); err != nil || count != 0 {
		t.Fatal("proxy not deleted")
	}
}

func TestMCPExpiredWriteTokenCannotMutate(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	if _, err := f.db.Exec(`UPDATE mcp_tokens SET expires_at=1 WHERE id=?`, f.tokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.session.CallTool(t.Context(), &mcp.CallToolParams{Name: "update_server", Arguments: map[string]any{"server_id": f.serverID, "name": "Denied"}}); err == nil {
		t.Fatal("expired token was accepted")
	}
	var name string
	if err := f.db.QueryRow(`SELECT name FROM servers WHERE id=?`, f.serverID).Scan(&name); err != nil || name != "Visible Server" {
		t.Fatal("expired token changed state")
	}
}
func TestMCPWritePermissionsAndRetiredPlans(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	legacy := performRequest(t, f.handler, "POST", "/api/mcp/tokens", map[string]any{"name": "limited", "scope": "read_write", "capabilities": []string{"server.rename"}}, f.cookie)
	if legacy.Code != http.StatusBadRequest {
		t.Fatal("legacy limited token request gained broad write permissions")
	}
	read := phase1Session(t, f.handler, f.cookie)
	r, err := read.CallTool(t.Context(), &mcp.CallToolParams{Name: "update_server", Arguments: map[string]any{"server_id": f.serverID, "name": "Denied"}})
	if err != nil || !r.IsError {
		t.Fatal("read token wrote")
	}
	for _, name := range []string{"prepare_change", "get_change_status", "apply_change"} {
		_, err := f.session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"plan_id": "old"}})
		if err == nil {
			t.Fatalf("retired tool %s exists", name)
		}
	}
	response := performRequest(t, f.handler, "POST", "/api/mcp/change-plans/old/approve", map[string]any{}, f.cookie)
	if response.Code != 404 {
		t.Fatalf("old approval route: %d", response.Code)
	}
	if r := performRequest(t, f.handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_write_enabled": false}, f.cookie); r.Code != 200 {
		t.Fatal("disable failed")
	}
	f.call(t, "update_server", map[string]any{"server_id": f.serverID, "name": "Denied"}, false)
	if r := performRequest(t, f.handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_write_enabled": true}, f.cookie); r.Code != 200 {
		t.Fatal("enable failed")
	}
	if r := performRequest(t, f.handler, "DELETE", "/api/mcp/tokens/"+strconv.FormatInt(f.tokenID, 10), nil, f.cookie); r.Code != 204 {
		t.Fatal("revoke failed")
	}
	if _, err := f.session.CallTool(t.Context(), &mcp.CallToolParams{Name: "update_server", Arguments: map[string]any{"server_id": f.serverID, "name": "Denied"}}); err == nil {
		t.Fatal("revoked token used")
	}
}
func TestMCPUnsupportedDryRunCannotExecute(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	f.call(t, "create_proxy", map[string]any{"server_id": f.serverID, "name": "No Write", "listen_port": 8443, "security": "reality", "entry_host_mode": "manual", "entry_host": "node.example.com", "server_name": "www.example.com", "reality_target": "www.example.com:443", "dry_run": true}, false)
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM proxies WHERE name='No Write'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unsupported dry run changed data")
	}
}
