package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

type userPortalFixture struct {
	db          *sql.DB
	handler     http.Handler
	adminCookie *http.Cookie
	userCookie  *http.Cookie
	otherCookie *http.Cookie
	userID      int64
	otherID     int64
}

func setupUserPortalFixture(t *testing.T) userPortalFixture {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initialized.Code != http.StatusCreated {
		db.Close()
		t.Fatalf("initialize = %d, %s", initialized.Code, initialized.Body.String())
	}
	adminCookie := initialized.Result().Cookies()[0]
	registerUser := func(username string) *http.Cookie {
		created := performRequest(t, handler, http.MethodPost, "/api/admin/invitations", map[string]string{"role": "user"}, adminCookie)
		var invitation invitationResponse
		if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &invitation) != nil {
			t.Fatalf("create user invitation = %d, %s", created.Code, created.Body.String())
		}
		registered := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
			"token": invitation.Token, "username": username, "password": "current-password",
		}, nil)
		if registered.Code != http.StatusCreated || !strings.Contains(registered.Body.String(), `"role":"user"`) {
			t.Fatalf("register %s = %d, %s", username, registered.Code, registered.Body.String())
		}
		return registered.Result().Cookies()[0]
	}
	userCookie := registerUser("alice")
	otherCookie := registerUser("bob")
	fixture := userPortalFixture{db: db, handler: handler, adminCookie: adminCookie, userCookie: userCookie, otherCookie: otherCookie}
	if err := db.QueryRow(`SELECT id FROM users WHERE username = 'alice'`).Scan(&fixture.userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM users WHERE username = 'bob'`).Scan(&fixture.otherID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestUserRoleCannotAccessManagerAPIs(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	for _, path := range []string{
		"/api/users", "/api/overview", "/api/servers", "/api/proxies", "/api/clients/1",
		"/api/relays", "/api/landings",
	} {
		response := performRequest(t, fixture.handler, http.MethodGet, path, nil, fixture.userCookie)
		if response.Code != http.StatusForbidden {
			t.Fatalf("user GET %s = %d, %s; want 403", path, response.Code, response.Body.String())
		}
	}
	if response := performRequest(t, fixture.handler, http.MethodGet, "/api/servers", nil, fixture.adminCookie); response.Code != http.StatusOK {
		t.Fatalf("admin manager API = %d, %s", response.Code, response.Body.String())
	}
	createdVIP := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/invitations", map[string]string{"role": "vip"}, fixture.adminCookie)
	var invitation invitationResponse
	if createdVIP.Code != http.StatusCreated || json.Unmarshal(createdVIP.Body.Bytes(), &invitation) != nil || invitation.Role != "vip" {
		t.Fatalf("VIP invitation = %d, %s", createdVIP.Code, createdVIP.Body.String())
	}
	registeredVIP := performRequest(t, fixture.handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "vip-user", "password": "current-password",
	}, nil)
	if registeredVIP.Code != http.StatusCreated {
		t.Fatal(registeredVIP.Body.String())
	}
	if response := performRequest(t, fixture.handler, http.MethodGet, "/api/servers", nil, registeredVIP.Result().Cookies()[0]); response.Code != http.StatusOK {
		t.Fatalf("VIP manager API = %d, %s", response.Code, response.Body.String())
	}
	for _, role := range []string{"admin", "unknown"} {
		response := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/invitations", map[string]string{"role": role}, fixture.adminCookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invitation role %q = %d, %s", role, response.Code, response.Body.String())
		}
	}
}

func createPortalServerAndProxy(t *testing.T, fixture userPortalFixture, protocol string, port int) (createdServerResponse, proxyResponse) {
	t.Helper()
	serverResponse := performRequest(t, fixture.handler, http.MethodPost, "/api/servers", map[string]string{"name": "Portal Server"}, fixture.adminCookie)
	var server createdServerResponse
	if serverResponse.Code != http.StatusCreated || json.Unmarshal(serverResponse.Body.Bytes(), &server) != nil {
		t.Fatalf("create server = %d, %s", serverResponse.Code, serverResponse.Body.String())
	}
	request := createProxyRequest{
		ServerID: server.Server.ID, Name: "Portal Proxy", Protocol: protocol, ListenPort: port,
		EntryHostMode: "manual", EntryHost: "node.example.com", FirstClientName: "Subscription",
	}
	if protocol == proxystore.ProtocolVLESS {
		request.Security = proxystore.SecurityReality
		request.ServerName = "www.example.com"
		request.RealityTarget = "www.example.com:443"
	} else {
		request.Method = proxystore.ShadowsocksMethodAES128GCM
	}
	proxyResult := performRequest(t, fixture.handler, http.MethodPost, "/api/proxies", request, fixture.adminCookie)
	var response struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if proxyResult.Code != http.StatusCreated || json.Unmarshal(proxyResult.Body.Bytes(), &response) != nil {
		t.Fatalf("create %s proxy = %d, %s", protocol, proxyResult.Code, proxyResult.Body.String())
	}
	return server, response.Proxy
}

func TestAssignedNodesMetricsAndShareAreOwnerScoped(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	_, vless := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 8443)
	_, shadowsocks := createPortalServerAndProxy(t, fixture, proxystore.ProtocolShadowsocks, 8388)
	vlessClientID := vless.Clients[0].ID
	ssClientID := shadowsocks.Clients[0].ID
	assign := func(clientID, userID int64, billing int) {
		response := performRequest(t, fixture.handler, http.MethodPatch,
			"/api/admin/clients/"+strconv.FormatInt(clientID, 10)+"/assignment",
			map[string]any{"user_id": userID, "billing_period_months": billing}, fixture.adminCookie)
		if response.Code != http.StatusOK {
			t.Fatalf("assign client = %d, %s", response.Code, response.Body.String())
		}
	}
	assign(vlessClientID, fixture.userID, 1)
	assign(ssClientID, fixture.otherID, 12)
	unassigned := performRequest(t, fixture.handler, http.MethodPost,
		"/api/proxies/"+strconv.FormatInt(vless.ID, 10)+"/clients",
		map[string]any{"name": "Unassigned"}, fixture.adminCookie)
	if unassigned.Code != http.StatusCreated {
		t.Fatalf("create unassigned client = %d, %s", unassigned.Code, unassigned.Body.String())
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := fixture.db.Exec(`UPDATE clients SET traffic_limit_bytes = ?, expires_at = ? WHERE id = ?`, int64(1000), now.Add(time.Hour).Unix(), vlessClientID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Exec(`INSERT INTO client_metrics
		(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes, cycle_started_at, updated_at)
		VALUES (?, 100, 200, 300, 400, ?, ?)`, vlessClientID, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}

	nodes := performRequest(t, fixture.handler, http.MethodGet, "/api/me/nodes", nil, fixture.userCookie)
	var nodeBody struct {
		Nodes []myNodeResponse `json:"nodes"`
	}
	decodeErr := json.Unmarshal(nodes.Body.Bytes(), &nodeBody)
	if nodes.Code != http.StatusOK || !strings.Contains(nodes.Body.String(), `"traffic_used_bytes":700`) ||
		!strings.Contains(nodes.Body.String(), `"billing_period_months":1`) || !strings.Contains(nodes.Body.String(), `"expires_at"`) ||
		decodeErr != nil || len(nodeBody.Nodes) != 1 || nodeBody.Nodes[0].ID != vlessClientID || strings.Contains(nodes.Body.String(), "credential") {
		t.Fatalf("alice nodes = %d, %s", nodes.Code, nodes.Body.String())
	}
	ownedShare := performRequest(t, fixture.handler, http.MethodGet,
		"/api/me/nodes/"+strconv.FormatInt(vlessClientID, 10)+"/share", nil, fixture.userCookie)
	if ownedShare.Code != http.StatusOK || !strings.Contains(ownedShare.Body.String(), `"uri":"vless://`) {
		t.Fatalf("owned share = %d, %s", ownedShare.Code, ownedShare.Body.String())
	}
	otherShare := performRequest(t, fixture.handler, http.MethodGet,
		"/api/me/nodes/"+strconv.FormatInt(ssClientID, 10)+"/share", nil, fixture.userCookie)
	if otherShare.Code != http.StatusNotFound {
		t.Fatalf("other share = %d, %s", otherShare.Code, otherShare.Body.String())
	}
	ssShare := performRequest(t, fixture.handler, http.MethodGet,
		"/api/me/nodes/"+strconv.FormatInt(ssClientID, 10)+"/share", nil, fixture.otherCookie)
	if ssShare.Code != http.StatusOK || !strings.Contains(ssShare.Body.String(), `"uri":"ss://`) {
		t.Fatalf("Shadowsocks share = %d, %s", ssShare.Code, ssShare.Body.String())
	}

	for _, invalidUserID := range []int64{fixture.otherID + 9999} {
		response := performRequest(t, fixture.handler, http.MethodPatch,
			"/api/admin/clients/"+strconv.FormatInt(vlessClientID, 10)+"/assignment",
			map[string]any{"user_id": invalidUserID, "billing_period_months": 1}, fixture.adminCookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("assign nonexistent user = %d, %s", response.Code, response.Body.String())
		}
	}
	var adminID int64
	if err := fixture.db.QueryRow(`SELECT id FROM users WHERE role = 'admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.db.Exec(`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES ('vip-assignment', 'hash', 'vip', ?, ?)`, now.Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	vipID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for _, invalidUserID := range []int64{adminID, vipID} {
		response := performRequest(t, fixture.handler, http.MethodPatch,
			"/api/admin/clients/"+strconv.FormatInt(vlessClientID, 10)+"/assignment",
			map[string]any{"user_id": invalidUserID, "billing_period_months": 1}, fixture.adminCookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("assign manager user = %d, %s", response.Code, response.Body.String())
		}
	}
	vipInvitation := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/invitations", map[string]string{"role": "vip"}, fixture.adminCookie)
	var invitation invitationResponse
	if vipInvitation.Code != http.StatusCreated || json.Unmarshal(vipInvitation.Body.Bytes(), &invitation) != nil {
		t.Fatalf("create VIP invitation = %d, %s", vipInvitation.Code, vipInvitation.Body.String())
	}
	vipRegistration := performRequest(t, fixture.handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "assignment-vip", "password": "current-password",
	}, nil)
	if vipRegistration.Code != http.StatusCreated {
		t.Fatalf("register VIP = %d, %s", vipRegistration.Code, vipRegistration.Body.String())
	}
	if response := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/admin/clients/"+strconv.FormatInt(vlessClientID, 10)+"/assignment",
		map[string]any{"user_id": fixture.userID, "billing_period_months": 1}, vipRegistration.Result().Cookies()[0]); response.Code != http.StatusForbidden {
		t.Fatalf("VIP assignment = %d, %s", response.Code, response.Body.String())
	}
}

func TestPasswordChangeRequestApprovalAndRejection(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	wrong := performRequest(t, fixture.handler, http.MethodPost, "/api/me/password-change-request", map[string]string{
		"current_password": "wrong-password", "new_password": "replacement-password",
	}, fixture.userCookie)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password = %d, %s", wrong.Code, wrong.Body.String())
	}
	short := performRequest(t, fixture.handler, http.MethodPost, "/api/me/password-change-request", map[string]string{
		"current_password": "current-password", "new_password": "short",
	}, fixture.userCookie)
	if short.Code != http.StatusBadRequest {
		t.Fatalf("short password = %d, %s", short.Code, short.Body.String())
	}
	created := performRequest(t, fixture.handler, http.MethodPost, "/api/me/password-change-request", map[string]string{
		"current_password": "current-password", "new_password": "replacement-password",
	}, fixture.userCookie)
	var createdBody struct {
		Request passwordChangeRequestResponse `json:"request"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &createdBody) != nil {
		t.Fatalf("create password request = %d, %s", created.Code, created.Body.String())
	}
	var storedHash string
	if err := fixture.db.QueryRow(`SELECT proposed_password_hash FROM password_change_requests WHERE id = ?`, createdBody.Request.ID).Scan(&storedHash); err != nil ||
		storedHash == "replacement-password" || strings.Contains(storedHash, "replacement-password") {
		t.Fatalf("stored proposed password = %q, %v", storedHash, err)
	}
	duplicate := performRequest(t, fixture.handler, http.MethodPost, "/api/me/password-change-request", map[string]string{
		"current_password": "current-password", "new_password": "another-password",
	}, fixture.userCookie)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate request = %d, %s", duplicate.Code, duplicate.Body.String())
	}
	if denied := performRequest(t, fixture.handler, http.MethodPost,
		"/api/admin/password-change-requests/"+strconv.FormatInt(createdBody.Request.ID, 10)+"/approve", nil, fixture.userCookie); denied.Code != http.StatusForbidden {
		t.Fatalf("user review = %d, %s", denied.Code, denied.Body.String())
	}
	listed := performRequest(t, fixture.handler, http.MethodGet, "/api/admin/password-change-requests", nil, fixture.adminCookie)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "hash") || strings.Contains(listed.Body.String(), "replacement-password") {
		t.Fatalf("admin password requests = %d, %s", listed.Code, listed.Body.String())
	}
	approved := performRequest(t, fixture.handler, http.MethodPost,
		"/api/admin/password-change-requests/"+strconv.FormatInt(createdBody.Request.ID, 10)+"/approve", nil, fixture.adminCookie)
	if approved.Code != http.StatusNoContent {
		t.Fatalf("approve = %d, %s", approved.Code, approved.Body.String())
	}
	if after := performRequest(t, fixture.handler, http.MethodGet, "/api/me/nodes", nil, fixture.userCookie); after.Code != http.StatusUnauthorized {
		t.Fatalf("approved old session = %d, %s", after.Code, after.Body.String())
	}
	var proposed sql.NullString
	if err := fixture.db.QueryRow(`SELECT proposed_password_hash FROM password_change_requests WHERE id = ?`, createdBody.Request.ID).Scan(&proposed); err != nil || proposed.Valid {
		t.Fatalf("approved proposed hash = %+v, %v", proposed, err)
	}
	oldLogin := performRequest(t, fixture.handler, http.MethodPost, "/api/auth/login", map[string]string{"username": "alice", "password": "current-password"}, nil)
	newLogin := performRequest(t, fixture.handler, http.MethodPost, "/api/auth/login", map[string]string{"username": "alice", "password": "replacement-password"}, nil)
	if oldLogin.Code != http.StatusUnauthorized || newLogin.Code != http.StatusOK {
		t.Fatalf("logins after approve: old=%d new=%d", oldLogin.Code, newLogin.Code)
	}
	newCookie := newLogin.Result().Cookies()[0]
	rejectedRequest := performRequest(t, fixture.handler, http.MethodPost, "/api/me/password-change-request", map[string]string{
		"current_password": "replacement-password", "new_password": "rejected-password",
	}, newCookie)
	var rejectedBody struct {
		Request passwordChangeRequestResponse `json:"request"`
	}
	if rejectedRequest.Code != http.StatusCreated || json.Unmarshal(rejectedRequest.Body.Bytes(), &rejectedBody) != nil {
		t.Fatalf("create rejected request = %d, %s", rejectedRequest.Code, rejectedRequest.Body.String())
	}
	rejected := performRequest(t, fixture.handler, http.MethodPost,
		"/api/admin/password-change-requests/"+strconv.FormatInt(rejectedBody.Request.ID, 10)+"/reject", nil, fixture.adminCookie)
	if rejected.Code != http.StatusNoContent {
		t.Fatalf("reject = %d, %s", rejected.Code, rejected.Body.String())
	}
	if login := performRequest(t, fixture.handler, http.MethodPost, "/api/auth/login", map[string]string{"username": "alice", "password": "replacement-password"}, nil); login.Code != http.StatusOK {
		t.Fatalf("old password after reject = %d, %s", login.Code, login.Body.String())
	}
	if err := fixture.db.QueryRow(`SELECT proposed_password_hash FROM password_change_requests WHERE id = ?`, rejectedBody.Request.ID).Scan(&proposed); err != nil || proposed.Valid {
		t.Fatalf("rejected proposed hash = %+v, %v", proposed, err)
	}
}

func TestValidatePublicTargetIP(t *testing.T) {
	for _, value := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if _, err := validatePublicTargetIP(value); err != nil {
			t.Fatalf("validatePublicTargetIP(%q) = %v", value, err)
		}
	}
	for _, value := range []string{
		"127.0.0.1", "10.0.0.1", "192.168.1.1", "172.16.0.1", "169.254.169.254",
		"100.64.0.1", "::1", "fc00::1", "fe80::1", "example.com", "0.0.0.0", "::",
	} {
		if _, err := validatePublicTargetIP(value); !errors.Is(err, errInvalidPublicIP) {
			t.Fatalf("validatePublicTargetIP(%q) error = %v, want errInvalidPublicIP", value, err)
		}
	}
}

func TestUserRelayUsesPoolOwnershipPortChecksAndDesiredState(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	server, _ := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 20000)
	registration := performRequest(t, fixture.handler, http.MethodPost, "/api/agent/register", agentRegistrationRequest{
		EnrollmentToken: server.EnrollmentToken, AgentVersion: "1.0.0",
		AgentImplementation: agentcontrol.OfficialImplementation, AgentAPIVersion: agentcontrol.CurrentAPIVersion,
		AgentCapabilities: []string{agentcontrol.CapabilityRelayRealm, agentcontrol.CapabilityManagedRuntimePurge},
	}, nil)
	if registration.Code != http.StatusCreated {
		t.Fatalf("register Agent = %d, %s", registration.Code, registration.Body.String())
	}
	var registered agentRegistrationResponse
	if err := json.Unmarshal(registration.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Exec(`INSERT INTO server_system_info
		(server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, public_ipv4, agent_version, reported_at)
		VALUES (?, 'host', 'linux', '1', 'kernel', 'amd64', '[]', '[]', '8.8.8.8', '1.0.0', ?)`,
		server.Server.ID, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	managerRelay := performRequest(t, fixture.handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: server.Server.ID, Name: "Reserved Relay", ListenPort: 20001,
		TargetType: "manual", TargetHost: "1.1.1.1", TargetPort: 443, Network: "tcp",
	}, fixture.adminCookie)
	if managerRelay.Code != http.StatusCreated {
		t.Fatalf("create reserved relay = %d, %s", managerRelay.Code, managerRelay.Body.String())
	}
	unopenedSources := performRequest(t, fixture.handler, http.MethodGet, "/api/me/relay-sources", nil, fixture.userCookie)
	if unopenedSources.Code != http.StatusOK || unopenedSources.Body.String() != "{\"sources\":[]}\n" {
		t.Fatalf("unopened relay sources = %d, %s", unopenedSources.Code, unopenedSources.Body.String())
	}
	pool := performRequest(t, fixture.handler, http.MethodPut,
		"/api/admin/servers/"+strconv.FormatInt(server.Server.ID, 10)+"/user-relay-pool",
		map[string]any{"enabled": true, "listen_address": "0.0.0.0", "entry_host": "relay.example.com", "port_start": 20000, "port_end": 20002}, fixture.adminCookie)
	if pool.Code != http.StatusOK {
		t.Fatalf("configure pool = %d, %s", pool.Code, pool.Body.String())
	}
	sources := performRequest(t, fixture.handler, http.MethodGet, "/api/me/relay-sources", nil, fixture.userCookie)
	if sources.Code != http.StatusOK || !strings.Contains(sources.Body.String(), "relay.example.com") {
		t.Fatalf("relay sources = %d, %s", sources.Code, sources.Body.String())
	}
	var source struct {
		Sources []struct {
			ID int64 `json:"id"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(sources.Body.Bytes(), &source); err != nil || len(source.Sources) != 1 {
		t.Fatalf("decode sources: %+v, %v", source, err)
	}
	panel := httptest.NewServer(fixture.handler)
	defer panel.Close()
	agentHeaders := http.Header{"Authorization": []string{"Bearer " + registered.AgentToken}}
	agentHeaders.Set("X-VPS-Panel-Agent-Implementation", agentcontrol.OfficialImplementation)
	agentHeaders.Set("X-VPS-Panel-Agent-Version", "1.0.0")
	agentHeaders.Set("X-VPS-Panel-Agent-API", strconv.Itoa(agentcontrol.CurrentAPIVersion))
	agentHeaders.Set("X-VPS-Panel-Agent-Capabilities", strings.Join([]string{
		agentcontrol.CapabilityRelayRealm, agentcontrol.CapabilityManagedRuntimePurge,
	}, ","))
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{
		HTTPHeader: agentHeaders,
	})
	if err != nil {
		t.Fatalf("connect Agent: %v, response = %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, serverstore.NewService(fixture.db), server.Server.ID, serverstore.StatusOnline)
	var versionBefore int64
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	created := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"relay_source_id": source.Sources[0].ID, "name": "Alice Relay", "target_ip": "1.1.1.1", "target_port": 443,
	}, fixture.userCookie)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"listen_port":20002`) {
		t.Fatalf("create user relay = %d, %s", created.Code, created.Body.String())
	}
	var createdBody struct {
		Relay myRelayResponse `json:"relay"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatal(err)
	}
	var ownerID int64
	var targetType, network string
	var versionAfter int64
	if err := fixture.db.QueryRow(`SELECT owner_user_id, target_type, network FROM relays WHERE id = ?`, createdBody.Relay.ID).Scan(&ownerID, &targetType, &network); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionAfter); err != nil {
		t.Fatal(err)
	}
	if ownerID != fixture.userID || targetType != "manual" || network != "tcp" || versionAfter != versionBefore+1 {
		t.Fatalf("stored user relay = owner %d type %q network %q version %d->%d", ownerID, targetType, network, versionBefore, versionAfter)
	}
	readContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	messageType, message, err := connection.Read(readContext)
	if err != nil {
		t.Fatalf("read config notification: %v", err)
	}
	var notification struct {
		Type    string `json:"type"`
		Version int64  `json:"version"`
	}
	if messageType != websocket.MessageText || json.Unmarshal(message, &notification) != nil ||
		notification.Type != "config_changed" || notification.Version != versionAfter {
		t.Fatalf("config notification = %q", message)
	}
	if leaked := performRequest(t, fixture.handler, http.MethodGet, "/api/relays", nil, fixture.otherCookie); leaked.Code != http.StatusForbidden {
		t.Fatalf("user manager relay list = %d, %s", leaked.Code, leaked.Body.String())
	}
	if deleteOther := performRequest(t, fixture.handler, http.MethodDelete,
		"/api/me/relays/"+strconv.FormatInt(createdBody.Relay.ID, 10), nil, fixture.otherCookie); deleteOther.Code != http.StatusNotFound {
		t.Fatalf("other user delete = %d, %s", deleteOther.Code, deleteOther.Body.String())
	}
	adminList := performRequest(t, fixture.handler, http.MethodGet, "/api/admin/user-relays", nil, fixture.adminCookie)
	if adminList.Code != http.StatusOK || !strings.Contains(adminList.Body.String(), "alice") || strings.Contains(adminList.Body.String(), "Reserved Relay") {
		t.Fatalf("admin user relay list = %d, %s", adminList.Code, adminList.Body.String())
	}
	adminDelete := performRequest(t, fixture.handler, http.MethodDelete,
		"/api/admin/user-relays/"+strconv.FormatInt(createdBody.Relay.ID, 10), nil, fixture.adminCookie)
	if adminDelete.Code != http.StatusNoContent {
		t.Fatalf("admin delete user relay = %d, %s", adminDelete.Code, adminDelete.Body.String())
	}

	invalidFields := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"relay_source_id": source.Sources[0].ID, "name": "Bypass", "target_ip": "1.1.1.1", "target_port": 443,
		"server_id": server.Server.ID, "listen_port": 25000, "listen_address": "0.0.0.0",
	}, fixture.userCookie)
	if invalidFields.Code != http.StatusBadRequest {
		t.Fatalf("user internal relay fields = %d, %s", invalidFields.Code, invalidFields.Body.String())
	}
	fullPool := performRequest(t, fixture.handler, http.MethodPut,
		"/api/admin/servers/"+strconv.FormatInt(server.Server.ID, 10)+"/user-relay-pool",
		map[string]any{"enabled": true, "listen_address": "0.0.0.0", "entry_host": "relay.example.com", "port_start": 20000, "port_end": 20001}, fixture.adminCookie)
	if fullPool.Code != http.StatusOK {
		t.Fatal(fullPool.Body.String())
	}
	full := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"relay_source_id": source.Sources[0].ID, "name": "Full", "target_ip": "1.1.1.1", "target_port": 443,
	}, fixture.userCookie)
	if full.Code != http.StatusConflict {
		t.Fatalf("full pool = %d, %s", full.Code, full.Body.String())
	}
	for index := 0; index < maxUserRelays; index++ {
		if _, err := fixture.db.Exec(`INSERT INTO relays
			(server_id, owner_user_id, name, listen_address, listen_port, entry_host_mode, entry_host,
			 target_type, target_host, target_port, network, enabled, created_at, updated_at)
			VALUES (?, ?, ?, '0.0.0.0', ?, 'manual', 'relay.example.com', 'manual', '1.1.1.1', 443, 'tcp', 1, ?, ?)`,
			server.Server.ID, fixture.userID, "limit-"+strconv.Itoa(index), 21000+index, time.Now().Unix(), time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	limitPool := performRequest(t, fixture.handler, http.MethodPut,
		"/api/admin/servers/"+strconv.FormatInt(server.Server.ID, 10)+"/user-relay-pool",
		map[string]any{"enabled": true, "listen_address": "0.0.0.0", "entry_host": "relay.example.com", "port_start": 22000, "port_end": 22020}, fixture.adminCookie)
	if limitPool.Code != http.StatusOK {
		t.Fatal(limitPool.Body.String())
	}
	limited := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"relay_source_id": source.Sources[0].ID, "name": "Too Many", "target_ip": "1.1.1.1", "target_port": 443,
	}, fixture.userCookie)
	if limited.Code != http.StatusConflict || !strings.Contains(limited.Body.String(), "最多") {
		t.Fatalf("relay limit = %d, %s", limited.Code, limited.Body.String())
	}
}
