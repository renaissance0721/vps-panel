package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestMCPP0ClientAndLandingLifecycle(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	proxy := f.call(t, "create_proxy", map[string]any{"server_id": f.serverID, "name": "P0 proxy", "listen_port": 8443, "security": "reality", "entry_host_mode": "manual", "entry_host": "node.example.com", "server_name": "www.example.com", "reality_target": "www.example.com:443", "first_client_name": "initial"}, true)
	proxyID := objectID(t, proxy, "proxy")
	client := f.call(t, "create_proxy_client", map[string]any{"proxy_id": proxyID, "name": "P0 client", "expires_at": "2030-01-01T00:00:00Z", "traffic_limit": "1.5", "limit_unit": "G", "traffic_reset_mode": "monthly", "traffic_reset_day": 1, "traffic_reset_time": "00:00"}, true)
	clientID := objectID(t, client, "client")
	value := client["client"].(map[string]any)
	if value["expires_at"] == nil || value["traffic_limit_bytes"] == nil {
		t.Fatal("client settings missing")
	}
	omitted := f.call(t, "update_proxy_client", map[string]any{"id": clientID, "changes": map[string]any{"name": "Changed client"}}, true)
	if omitted["client"].(map[string]any)["expires_at"] == nil || omitted["client"].(map[string]any)["traffic_limit_bytes"] == nil {
		t.Fatal("omitted fields were cleared")
	}
	nullName := f.call(t, "update_proxy_client", map[string]any{"id": clientID, "changes": map[string]any{"name": nil}}, true)
	if nullName["client"].(map[string]any)["name"] != "Changed client" {
		t.Fatal("null name did not follow web semantics")
	}
	updated := f.call(t, "update_proxy_client", map[string]any{"id": clientID, "changes": map[string]any{"name": "Changed client", "expires_at": nil, "traffic_limit": nil}}, true)
	v := updated["client"].(map[string]any)
	if v["expires_at"] != nil || v["traffic_limit_bytes"] != nil {
		t.Fatal("explicit null did not clear settings")
	}
	f.call(t, "get_proxy_client", map[string]any{"id": clientID}, true)
	f.call(t, "reset_proxy_client_traffic", map[string]any{"id": clientID, "expected_name": "wrong"}, false)
	f.call(t, "reset_proxy_client_traffic", map[string]any{"id": clientID, "expected_name": "Changed client"}, true)
	f.call(t, "set_client_relay_port_count", map[string]any{"id": clientID, "user_relay_port_count": 9}, false)
	result, err := f.db.Exec(`INSERT INTO users(username,password_hash,role,created_at,updated_at) VALUES ('p0-carpool','hash','carpool',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	carpoolID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	f.call(t, "assign_proxy_client", map[string]any{"id": clientID, "expected_name": "Changed client", "user_id": carpoolID, "billing_period_months": 2}, false)
	assigned := f.call(t, "assign_proxy_client", map[string]any{"id": clientID, "expected_name": "Changed client", "user_id": carpoolID, "billing_period_months": 3}, true)
	if assigned["client"].(map[string]any)["assigned_user_id"] != float64(carpoolID) {
		t.Fatal("assignment failed")
	}
	f.call(t, "set_client_relay_port_count", map[string]any{"id": clientID, "user_relay_port_count": 1}, true)
	if _, err := f.db.Exec(`INSERT INTO relays(server_id,owner_user_id,source_client_id,name,listen_port,target_type,target_host,target_port,network,created_at,updated_at) VALUES (?,?,?,'dependent relay',25001,'manual','example.com',443,'tcp',1,1)`, f.serverID, carpoolID, clientID); err != nil {
		t.Fatal(err)
	}
	f.call(t, "assign_proxy_client", map[string]any{"id": clientID, "expected_name": "Changed client", "user_id": nil}, true)
	var dependent int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM relays WHERE source_client_id=?`, clientID).Scan(&dependent); err != nil || dependent != 0 {
		t.Fatal("unassignment failed to clean dependent relay")
	}
	clientShare := f.call(t, "get_proxy_client_share", map[string]any{"id": clientID}, true)
	uri := clientShare["share"].(map[string]any)["uri"].(string)
	parsed, err := url.Parse(uri)
	if err != nil || parsed.User == nil || parsed.User.Username() == "" {
		t.Fatal("Client share credential missing")
	}
	list := f.call(t, "list_proxy_clients", map[string]any{"id": proxyID}, true)
	detail := f.call(t, "get_proxy_client", map[string]any{"id": clientID}, true)
	for _, value := range []any{list, detail} {
		raw, _ := json.Marshal(value)
		if strings.Contains(string(raw), parsed.User.Username()) {
			t.Fatal("Client credential leaked in ordinary output")
		}
	}
	read := phase1Session(t, f.handler, f.cookie)
	for _, name := range []string{"delete_proxy_client", "get_proxy_client_share"} {
		args := map[string]any{"id": clientID}
		if name == "delete_proxy_client" {
			args["expected_name"] = "Changed client"
		}
		r, err := read.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || !r.IsError {
			t.Fatalf("read token access to %s", name)
		}
	}
	f.call(t, "delete_proxy_client", map[string]any{"id": clientID, "expected_name": "wrong"}, false)
	f.call(t, "delete_proxy_client", map[string]any{"id": clientID, "expected_name": "Changed client"}, true)
	managed := f.call(t, "create_proxy_client", map[string]any{"proxy_id": proxyID, "name": "Managed"}, true)
	managedID := objectID(t, managed, "client")
	result, err = f.db.Exec(`INSERT INTO users(username,password_hash,role,created_at,updated_at) VALUES ('p0-subscriber','hash','subscriber',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	subscriberID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO subscriber_clients(user_id,proxy_id,client_id,created_at) VALUES (?,?,?,1)`, subscriberID, proxyID, managedID); err != nil {
		t.Fatal(err)
	}
	f.call(t, "delete_proxy_client", map[string]any{"id": managedID, "expected_name": "Managed"}, false)
	f.call(t, "update_proxy_client", map[string]any{"id": managedID, "changes": map[string]any{"name": "Denied"}}, false)
	f.call(t, "assign_proxy_client", map[string]any{"id": managedID, "expected_name": "Managed", "user_id": nil}, false)
	f.call(t, "reset_proxy_client_traffic", map[string]any{"id": managedID, "expected_name": "Managed"}, false)

	const secret = "p0-test-secret"
	landing := f.call(t, "create_landing", map[string]any{"name": "P0 landing", "visibility": "private", "uri": "vless://" + secret + "@landing.example.com:443"}, true)
	landingID := objectID(t, landing, "landing")
	r, err := read.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_landing_share", Arguments: map[string]any{"id": landingID}})
	if err != nil || !r.IsError {
		t.Fatal("read token retrieved landing credential")
	}
	other := createLandingForAPI(t, f.handler, f.memberCookie, createLandingRequest{Name: "other private", URI: "vless://private@other.example.com:443"})
	f.call(t, "get_landing", map[string]any{"id": other.ID}, false)
	f.call(t, "get_landing_share", map[string]any{"id": other.ID}, false)
	regular := f.call(t, "get_landing", map[string]any{"id": landingID}, true)
	raw, _ := json.Marshal(regular)
	if strings.Contains(string(raw), secret) {
		t.Fatal("landing secret leaked in normal read")
	}
	share := f.call(t, "get_landing_share", map[string]any{"id": landingID}, true)
	raw, _ = json.Marshal(share)
	if !strings.Contains(string(raw), secret) {
		t.Fatal("share URI missing")
	}
	relay := f.call(t, "create_relay", map[string]any{"server_id": f.serverID, "name": "P0 relay", "listen_port": 9500, "entry_host_mode": "manual", "entry_host": "relay.example.com", "target_type": "landing", "target_landing_id": landingID, "network": "tcp"}, true)
	relayID := objectID(t, relay, "relay")
	f.call(t, "delete_landing", map[string]any{"id": landingID, "expected_name": "P0 landing"}, false)
	var oldVersion, newVersion int64
	if err := f.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id=?`, f.serverID).Scan(&oldVersion); err != nil {
		t.Fatal(err)
	}
	f.call(t, "update_landing", map[string]any{"id": landingID, "changes": map[string]any{"uri": "vless://" + secret + "@new.example.com:8443"}}, true)
	if err := f.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id=?`, f.serverID).Scan(&newVersion); err != nil || newVersion <= oldVersion {
		t.Fatal("landing endpoint change did not update relay desired state")
	}
	f.call(t, "delete_relay", map[string]any{"id": relayID, "expected_name": "P0 relay"}, true)
	f.call(t, "update_landing", map[string]any{"id": landingID, "changes": map[string]any{"name": "Renamed landing"}}, true)
	f.call(t, "delete_landing", map[string]any{"id": landingID, "expected_name": "Renamed landing"}, true)
	f.call(t, "create_landing", map[string]any{"name": "bad", "uri": "invalid"}, false)
}

func TestMCPP0ServerAccessTrafficAndEnrollment(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	var other int64
	if err := f.db.QueryRow(`SELECT id FROM users WHERE username='member'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	f.call(t, "get_server_access", map[string]any{"server_id": f.hiddenID}, false)
	f.call(t, "update_server_access", map[string]any{"server_id": f.serverID, "visibility": "private", "user_ids": []int64{999999}}, false)
	access := f.call(t, "update_server_access", map[string]any{"server_id": f.serverID, "visibility": "private", "user_ids": []int64{other}}, true)
	if access["saved"] != true {
		t.Fatal("access not saved")
	}
	ids := access["user_ids"].([]any)
	if len(ids) != 2 {
		t.Fatal("effective access did not preserve current administrator")
	}
	if read := f.call(t, "get_server_access", map[string]any{"server_id": f.serverID}, true); len(read["user_ids"].([]any)) != 2 {
		t.Fatal("access did not persist")
	}
	if _, err := f.db.Exec(`INSERT INTO server_metrics(server_id,cpu_percent,memory_used_bytes,memory_total_bytes,disk_used_bytes,disk_total_bytes,uptime_seconds,cycle_rx_bytes,cycle_tx_bytes,cycle_started_at,updated_at) VALUES (?,0,0,0,0,0,0,100,200,?,?)`, f.serverID, time.Now().Add(-time.Hour).Unix(), time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	before := f.call(t, "get_server_traffic", map[string]any{"server_id": f.serverID}, true)
	if before["metrics_status"] != "stale" {
		t.Fatal("stale metric reported fresh")
	}
	adjusted := f.call(t, "set_server_traffic_adjustment", map[string]any{"server_id": f.serverID, "target_used_bytes": 500}, true)
	if adjusted["after"].(map[string]any)["traffic_used_bytes"] != float64(500) {
		t.Fatal("calibration incorrect")
	}
	f.call(t, "set_server_traffic_adjustment", map[string]any{"server_id": f.serverID, "target_used_bytes": -1}, false)
	f.call(t, "clear_server_traffic_adjustment", map[string]any{"server_id": f.serverID}, true)
	f.call(t, "get_agent_status", map[string]any{"server_id": f.serverID}, true)
	f.call(t, "upgrade_server_agent", map[string]any{"server_id": f.serverID}, false)
	f.call(t, "create_agent_enrollment", map[string]any{"server_id": f.serverID}, false)

	h := NewHandlerWithBackup(f.db, t.TempDir(), "v1.2.3", BackupConfig{Domain: "panel.example.com"})
	srv := httptest.NewServer(h)
	defer srv.Close()
	transport := mcpBearerTransport{base: http.DefaultTransport, token: f.token}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: transport}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_agent_enrollment", Arguments: map[string]any{"server_id": f.serverID}})
	if err != nil || result.IsError {
		t.Fatalf("enrollment: %v", err)
	}
	content, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(content), "https://panel.example.com/install-agent.sh") || strings.Contains(string(content), "localhost") {
		t.Fatal("incorrect enrollment URL")
	}
	structured := result.StructuredContent.(map[string]any)
	command := structured["agent_installation_command"].(string)
	match := regexp.MustCompile(`--token\s+(\S+)`).FindStringSubmatch(command)
	if len(match) != 2 {
		t.Fatal("enrollment token missing")
	}
	var active int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM agent_enrollments WHERE server_id=? AND used_at IS NULL AND expires_at>?`, f.serverID, time.Now().Unix()).Scan(&active); err != nil || active != 1 {
		t.Fatal("enrollment expiration or invalidation incorrect")
	}
	var logs string
	if err := f.db.QueryRow(`SELECT (SELECT COALESCE(group_concat(tool_name||':'||error_code),'') FROM mcp_audit_logs) || (SELECT COALESCE(group_concat(summary),'') FROM audit_logs)`).Scan(&logs); err != nil || strings.Contains(logs, match[1]) {
		t.Fatal("enrollment leaked in audit")
	}
	registered, err := agentcontrol.NewService(f.db, time.Now).RegisterAgentWithMetadata(t.Context(), match[1], agentcontrol.Metadata{Implementation: agentcontrol.OfficialImplementation, Version: "v1.2.2", APIVersion: 1, Capabilities: []string{agentcontrol.CapabilitySelfUpgrade}}, false)
	if err != nil {
		t.Fatal(err)
	}
	callUpgrade := func() *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "upgrade_server_agent", Arguments: map[string]any{"server_id": f.serverID}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if !callUpgrade().IsError {
		t.Fatal("offline agent upgrade accepted")
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+registered.Token)
	header.Set("X-VPS-Panel-Agent-Implementation", agentcontrol.OfficialImplementation)
	header.Set("X-VPS-Panel-Agent-Version", "v1.2.2")
	header.Set("X-VPS-Panel-Agent-API", "1")
	header.Set("X-VPS-Panel-Agent-Capabilities", agentcontrol.CapabilitySelfUpgrade)
	connection, _, err := websocket.Dial(t.Context(), srv.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	service := serverstore.NewService(f.db)
	waitForServerStatus(t, service, f.serverID, serverstore.StatusOnline)
	upgrade := callUpgrade()
	if upgrade.IsError || upgrade.StructuredContent.(map[string]any)["status"] != "upgrading" {
		t.Fatal("upgrade was not initiated")
	}
	readCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	_, message, err := connection.Read(readCtx)
	cancel()
	if err != nil || string(message) != `{"type":"agent_upgrade","version":"v1.2.3"}` {
		t.Fatal("upgrade message not delivered")
	}
	status := f.call(t, "get_agent_status", map[string]any{"server_id": f.serverID}, true)
	if status["upgrade_status"] != "upgrading" {
		t.Fatal("upgrade state missing")
	}
	if _, err := f.db.Exec(`UPDATE agents SET upgrade_status='failed',upgrade_error='secret-error-value' WHERE server_id=?`, f.serverID); err != nil {
		t.Fatal(err)
	}
	status = f.call(t, "get_agent_status", map[string]any{"server_id": f.serverID}, true)
	if status["upgrade_status"] != "failed" || strings.Contains(status["upgrade_error"].(string), "secret-error-value") {
		t.Fatal("failure status missing or secret error leaked")
	}
	if err := connection.Close(websocket.StatusNormalClosure, "reconnect"); err != nil {
		t.Fatal(err)
	}
	waitForServerStatus(t, service, f.serverID, serverstore.StatusOffline)
	header.Set("X-VPS-Panel-Agent-Version", "v1.2.3")
	current, _, err := websocket.Dial(t.Context(), srv.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer current.CloseNow()
	waitForServerStatus(t, service, f.serverID, serverstore.StatusOnline)
	upgrade = callUpgrade()
	if upgrade.IsError || upgrade.StructuredContent.(map[string]any)["status"] != "already_current" {
		t.Fatal("current version not recognized")
	}
	thirdParty := createAccessTestServer(t, f.handler, f.cookie, "Third party", "private", nil)
	if _, err := agentcontrol.NewService(f.db, time.Now).RegisterAgentWithMetadata(t.Context(), thirdParty.EnrollmentToken, agentcontrol.Metadata{Implementation: "third-party", Version: "v1.0.0", APIVersion: 1, Capabilities: []string{agentcontrol.CapabilitySelfUpgrade}}, false); err != nil {
		t.Fatal(err)
	}
	thirdStatus := f.call(t, "get_agent_status", map[string]any{"server_id": thirdParty.Server.ID}, true)
	if thirdStatus["self_upgrade_supported"] != false {
		t.Fatal("third-party self-upgrade incorrectly supported")
	}
	thirdUpgrade, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "upgrade_server_agent", Arguments: map[string]any{"server_id": thirdParty.Server.ID}})
	if err != nil || !thirdUpgrade.IsError {
		t.Fatal("third-party upgrade accepted")
	}
	hiddenEnrollment, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_agent_enrollment", Arguments: map[string]any{"server_id": f.hiddenID}})
	if err != nil || !hiddenEnrollment.IsError {
		t.Fatal("hidden server enrollment was accepted")
	}
	renewed, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_agent_enrollment", Arguments: map[string]any{"server_id": f.serverID}})
	if err != nil || renewed.IsError {
		t.Fatal("re-enrollment failed")
	}
	waitForServerStatus(t, service, f.serverID, serverstore.StatusPending)
	closedCtx, cancelClosed := context.WithTimeout(t.Context(), time.Second)
	_, _, closedErr := current.Read(closedCtx)
	cancelClosed()
	if closedErr == nil {
		t.Fatal("obsolete Agent connection remained open")
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM agent_enrollments WHERE server_id=? AND used_at IS NULL AND expires_at>?`, f.serverID, time.Now().Unix()).Scan(&active); err != nil || active != 1 {
		t.Fatal("re-enrollment did not retain one active token")
	}
}

func TestMCPP0HiddenClientAndWriteSwitch(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	response := performRequest(t, f.handler, "POST", "/api/proxies", createProxyRequest{ServerID: f.hiddenID, Name: "hidden proxy", ListenPort: 8444, Security: "reality", EntryHostMode: "manual", EntryHost: "hidden.example.com", ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "hidden client"}, f.memberCookie)
	var created struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil || len(created.Proxy.Clients) == 0 {
		t.Fatal("hidden fixture creation failed")
	}
	clientID := created.Proxy.Clients[0].ID
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"get_proxy_client", map[string]any{"id": clientID}},
		{"get_proxy_client_share", map[string]any{"id": clientID}},
		{"assign_proxy_client", map[string]any{"id": clientID, "expected_name": "hidden client", "user_id": nil}},
		{"set_client_relay_port_count", map[string]any{"id": clientID, "user_relay_port_count": 0}},
		{"update_proxy_client", map[string]any{"id": clientID, "changes": map[string]any{"name": "denied"}}},
		{"delete_proxy_client", map[string]any{"id": clientID, "expected_name": "hidden client"}},
	} {
		f.call(t, tc.name, tc.args, false)
	}
	if r := performRequest(t, f.handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_write_enabled": false}, f.cookie); r.Code != 200 {
		t.Fatal("disable write failed")
	}
	f.call(t, "create_landing", map[string]any{"name": "denied", "uri": "vless://dummy@example.com:443"}, false)
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE id=?`, clientID).Scan(&count); err != nil || count != 1 {
		t.Fatal("hidden client changed")
	}
}

func TestMCPP0ServerLifecycleRestrictions(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	if _, err := f.db.Exec(`UPDATE servers SET decommission_status='pending' WHERE id=?`, f.serverID); err != nil {
		t.Fatal(err)
	}
	f.call(t, "update_server_access", map[string]any{"server_id": f.serverID, "visibility": "public", "user_ids": []int64{}}, false)
	f.call(t, "set_server_traffic_adjustment", map[string]any{"server_id": f.serverID, "target_used_bytes": 123}, false)
	f.call(t, "clear_server_traffic_adjustment", map[string]any{"server_id": f.serverID}, false)
	if _, err := f.db.Exec(`UPDATE servers SET decommission_status='',archived_at=? WHERE id=?`, time.Now().Unix(), f.serverID); err != nil {
		t.Fatal(err)
	}
	f.call(t, "update_server_access", map[string]any{"server_id": f.serverID, "visibility": "public", "user_ids": []int64{}}, false)
	f.call(t, "set_server_traffic_adjustment", map[string]any{"server_id": f.serverID, "target_used_bytes": 123}, false)
}

func TestMCPP0TrafficWithoutAgentCountersIsUnavailable(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	if _, err := f.db.Exec(`UPDATE servers SET status='online' WHERE id=?`, f.serverID); err != nil {
		t.Fatal(err)
	}
	before := f.call(t, "get_server_traffic", map[string]any{"server_id": f.serverID}, true)
	if before["metrics_status"] != "unavailable" || before["traffic_used_bytes"] != nil {
		t.Fatal("missing traffic counters were reported as fresh")
	}
	change := f.call(t, "set_server_traffic_adjustment", map[string]any{"server_id": f.serverID, "target_used_bytes": 100}, true)
	after := change["after"].(map[string]any)
	if after["metrics_status"] != "unavailable" || after["traffic_used_bytes"] != float64(100) {
		t.Fatal("synthetic adjustment was reported as a fresh Agent measurement")
	}
}

func TestMCPAuthorizationStateAndSanitizedDiagnostics(t *testing.T) {
	f, cleanup := newWriteFixture(t)
	defer cleanup()
	info := f.call(t, "get_panel_info", map[string]any{}, true)
	if info["scope"] != "read_write" || info["token_scope"] != "read_write" || info["write_available"] != true {
		t.Fatalf("writer state: %+v", info)
	}
	if r := performRequest(t, f.handler, "PATCH", "/api/mcp/settings", map[string]bool{"mcp_write_enabled": false}, f.cookie); r.Code != 200 {
		t.Fatal("disable failed")
	}
	info = f.call(t, "get_panel_info", map[string]any{}, true)
	if info["token_scope"] != "read_write" || info["write_available"] != false {
		t.Fatal("disabled write state incorrect")
	}
	reader := phase1Session(t, f.handler, f.cookie)
	result, err := reader.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_panel_info", Arguments: map[string]any{}})
	if err != nil || result.IsError || result.StructuredContent.(map[string]any)["token_scope"] != "read" {
		t.Fatal("reader scope incorrect")
	}
	v := serverstore.Server{Status: serverstore.StatusOffline, AgentVersion: "v1.0.0", AgentImplementation: "third-party", AgentAPIVersion: 1, AgentConfigSyncStatus: "failed", AgentUpgradeStatus: "failed", AgentConfigSyncError: "secret-sync", AgentUpgradeError: "secret-upgrade"}
	diag := mcpAgentDiagnostics(v, "v1.2.0")
	raw, _ := json.Marshal(diag)
	for _, want := range []string{"agent_offline", "self_upgrade_unsupported", "configuration_sync_failed", "upgrade_failed"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing diagnostic %s", want)
		}
	}
	if strings.Contains(string(raw), "secret-") {
		t.Fatal("diagnostics exposed raw error")
	}
}
