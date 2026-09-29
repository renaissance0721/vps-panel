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
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
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

func setClientRelayPortCount(t *testing.T, fixture userPortalFixture, clientID int64, count int) {
	t.Helper()
	response := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/admin/clients/"+strconv.FormatInt(clientID, 10)+"/relay-ports",
		map[string]any{"user_relay_port_count": count}, fixture.adminCookie)
	if response.Code != http.StatusOK {
		t.Fatalf("set client %d relay port count to %d = %d, %s", clientID, count, response.Code, response.Body.String())
	}
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
		decodeErr != nil || len(nodeBody.Nodes) != 1 || nodeBody.Nodes[0].ClientID != vlessClientID ||
		nodeBody.Nodes[0].ServerName != "Portal Server" || nodeBody.Nodes[0].ClientName != "Subscription" ||
		strings.Contains(nodes.Body.String(), "credential") {
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

func TestPasswordResetRequestApprovalAndRejection(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	removed := performRequest(t, fixture.handler, http.MethodPost, "/api/me/password-change-request", map[string]string{
		"new_password": "replacement-password",
	}, fixture.userCookie)
	if removed.Code != http.StatusNotFound {
		t.Fatalf("removed password endpoint = %d, %s", removed.Code, removed.Body.String())
	}
	short := performRequest(t, fixture.handler, http.MethodPost, "/api/account/password-reset-request", map[string]string{
		"new_password": "short",
	}, fixture.userCookie)
	if short.Code != http.StatusBadRequest {
		t.Fatalf("short password = %d, %s", short.Code, short.Body.String())
	}
	created := performRequest(t, fixture.handler, http.MethodPost, "/api/account/password-reset-request", map[string]string{
		"new_password": "replacement-password",
	}, fixture.userCookie)
	var createdBody struct {
		Request passwordChangeRequestResponse `json:"request"`
	}
	if created.Code != http.StatusAccepted || json.Unmarshal(created.Body.Bytes(), &createdBody) != nil {
		t.Fatalf("create password request = %d, %s", created.Code, created.Body.String())
	}
	var storedHash string
	if err := fixture.db.QueryRow(`SELECT proposed_password_hash FROM password_change_requests WHERE id = ?`, createdBody.Request.ID).Scan(&storedHash); err != nil ||
		storedHash == "replacement-password" || strings.Contains(storedHash, "replacement-password") {
		t.Fatalf("stored proposed password = %q, %v", storedHash, err)
	}
	duplicate := performRequest(t, fixture.handler, http.MethodPost, "/api/account/password-reset-request", map[string]string{
		"new_password": "another-password",
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
	rejectedRequest := performRequest(t, fixture.handler, http.MethodPost, "/api/account/password-reset-request", map[string]string{
		"new_password": "rejected-password",
	}, newCookie)
	var rejectedBody struct {
		Request passwordChangeRequestResponse `json:"request"`
	}
	if rejectedRequest.Code != http.StatusAccepted || json.Unmarshal(rejectedRequest.Body.Bytes(), &rejectedBody) != nil {
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

func TestUserRelayUsesAssignedClientOwnershipPortChecksAndDesiredState(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	server, proxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 20000)
	sourceClientID := proxy.Clients[0].ID
	assignClient := func(clientID, userID int64) {
		response := performRequest(t, fixture.handler, http.MethodPatch,
			"/api/admin/clients/"+strconv.FormatInt(clientID, 10)+"/assignment",
			map[string]any{"user_id": userID, "billing_period_months": 1}, fixture.adminCookie)
		if response.Code != http.StatusOK {
			t.Fatalf("assign source client = %d, %s", response.Code, response.Body.String())
		}
	}
	assignClient(sourceClientID, fixture.userID)
	createClient := func(name string) int64 {
		response := performRequest(t, fixture.handler, http.MethodPost,
			"/api/proxies/"+strconv.FormatInt(proxy.ID, 10)+"/clients", map[string]any{"name": name}, fixture.adminCookie)
		var body struct {
			Client clientResponse `json:"client"`
		}
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("create source client %q = %d, %s", name, response.Code, response.Body.String())
		}
		return body.Client.ID
	}
	bobClientID := createClient("Bob Client")
	assignClient(bobClientID, fixture.otherID)
	unassignedClientID := createClient("Unassigned Client")

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
	setClientRelayPortCount(t, fixture, sourceClientID, 5)
	sources := performRequest(t, fixture.handler, http.MethodGet, "/api/me/relay-sources", nil, fixture.userCookie)
	if sources.Code != http.StatusOK || !strings.Contains(sources.Body.String(), `"server_name":"Portal Server"`) ||
		!strings.Contains(sources.Body.String(), `"proxy_name":"Portal Proxy"`) {
		t.Fatalf("relay sources = %d, %s", sources.Code, sources.Body.String())
	}
	var source struct {
		Sources []struct {
			ClientID int64 `json:"client_id"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(sources.Body.Bytes(), &source); err != nil || len(source.Sources) != 1 || source.Sources[0].ClientID != sourceClientID {
		t.Fatalf("decode sources: %+v, %v", source, err)
	}
	for _, invalidSource := range []int64{bobClientID, unassignedClientID} {
		response := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
			"mode": "custom", "source_client_id": invalidSource, "name": "Invalid Source", "target_ip": "1.1.1.1", "target_port": 443,
		}, fixture.userCookie)
		if response.Code != http.StatusNotFound {
			t.Fatalf("foreign source %d = %d, %s", invalidSource, response.Code, response.Body.String())
		}
	}
	invalidFields := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"mode": "custom", "source_client_id": sourceClientID, "name": "Bypass", "target_ip": "1.1.1.1", "target_port": 443,
		"server_id": server.Server.ID, "listen_port": 25000, "listen_address": "0.0.0.0",
	}, fixture.userCookie)
	if invalidFields.Code != http.StatusBadRequest {
		t.Fatalf("user internal relay fields = %d, %s", invalidFields.Code, invalidFields.Body.String())
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
		"mode": "custom", "source_client_id": sourceClientID, "name": "Alice Relay", "target_ip": "1.1.1.1", "target_port": 443,
	}, fixture.userCookie)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"entry_address":"node.example.com:`) {
		t.Fatalf("create user relay = %d, %s", created.Code, created.Body.String())
	}
	var createdBody struct {
		Relay myRelayResponse `json:"relay"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatal(err)
	}
	var ownerID, storedSourceClientID, storedServerID int64
	var targetType, network, entryMode, entryHost, targetHost string
	var listenPort, targetPort int
	var versionAfter int64
	if err := fixture.db.QueryRow(`SELECT owner_user_id, source_client_id, server_id, entry_host_mode, entry_host,
		listen_port, target_type, target_host, target_port, network FROM relays WHERE id = ?`, createdBody.Relay.ID).
		Scan(&ownerID, &storedSourceClientID, &storedServerID, &entryMode, &entryHost,
			&listenPort, &targetType, &targetHost, &targetPort, &network); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionAfter); err != nil {
		t.Fatal(err)
	}
	if ownerID != fixture.userID || storedSourceClientID != sourceClientID || storedServerID != server.Server.ID ||
		entryMode != "manual" || entryHost != "node.example.com" ||
		targetType != "manual" || targetHost != "1.1.1.1" || targetPort != 443 || network != "tcp" || versionAfter != versionBefore+1 {
		t.Fatalf("stored user relay = owner %d source %d server %d entry %s/%s port %d target %s:%d type %q network %q version %d->%d",
			ownerID, storedSourceClientID, storedServerID, entryMode, entryHost, listenPort,
			targetHost, targetPort, targetType, network, versionBefore, versionAfter)
	}
	var sourceReservation int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM client_relay_ports WHERE client_id = ? AND port = ?`, sourceClientID, listenPort).Scan(&sourceReservation); err != nil || sourceReservation != 1 {
		t.Fatalf("user relay port %d reservation count = %d, %v", listenPort, sourceReservation, err)
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
	if adminList.Code != http.StatusOK || !strings.Contains(adminList.Body.String(), "alice") ||
		!strings.Contains(adminList.Body.String(), "Portal Proxy") || strings.Contains(adminList.Body.String(), "Reserved Relay") {
		t.Fatalf("admin user relay list = %d, %s", adminList.Code, adminList.Body.String())
	}
	adminRelays := performRequest(t, fixture.handler, http.MethodGet, "/api/relays", nil, fixture.adminCookie)
	if adminRelays.Code != http.StatusOK || !strings.Contains(adminRelays.Body.String(), "Alice Relay") ||
		!strings.Contains(adminRelays.Body.String(), `"owner_username":"alice"`) {
		t.Fatalf("admin relay list = %d, %s", adminRelays.Code, adminRelays.Body.String())
	}
	vipInvitation := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/invitations", map[string]string{"role": "vip"}, fixture.adminCookie)
	var invitation invitationResponse
	if vipInvitation.Code != http.StatusCreated || json.Unmarshal(vipInvitation.Body.Bytes(), &invitation) != nil {
		t.Fatalf("create VIP invitation = %d, %s", vipInvitation.Code, vipInvitation.Body.String())
	}
	vipRegistration := performRequest(t, fixture.handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "relay-vip", "password": "current-password",
	}, nil)
	if vipRegistration.Code != http.StatusCreated {
		t.Fatalf("register VIP = %d, %s", vipRegistration.Code, vipRegistration.Body.String())
	}
	vipRelays := performRequest(t, fixture.handler, http.MethodGet, "/api/relays", nil, vipRegistration.Result().Cookies()[0])
	if vipRelays.Code != http.StatusOK || strings.Contains(vipRelays.Body.String(), "Alice Relay") {
		t.Fatalf("VIP relay list = %d, %s", vipRelays.Code, vipRelays.Body.String())
	}
	userRelays := performRequest(t, fixture.handler, http.MethodGet, "/api/me/relays", nil, fixture.userCookie)
	otherRelays := performRequest(t, fixture.handler, http.MethodGet, "/api/me/relays", nil, fixture.otherCookie)
	if userRelays.Code != http.StatusOK || !strings.Contains(userRelays.Body.String(), "Alice Relay") ||
		otherRelays.Code != http.StatusOK || strings.Contains(otherRelays.Body.String(), "Alice Relay") {
		t.Fatalf("owned relay visibility = alice(%d, %s) bob(%d, %s)", userRelays.Code, userRelays.Body.String(), otherRelays.Code, otherRelays.Body.String())
	}
	deleted := performRequest(t, fixture.handler, http.MethodDelete,
		"/api/me/relays/"+strconv.FormatInt(createdBody.Relay.ID, 10), nil, fixture.userCookie)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete user relay = %d, %s", deleted.Code, deleted.Body.String())
	}
	var versionDeleted int64
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionDeleted); err != nil || versionDeleted != versionAfter+1 {
		t.Fatalf("delete relay version = %d, %v; want %d", versionDeleted, err, versionAfter+1)
	}
	deleteContext, cancelDelete := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelDelete()
	_, deleteMessage, err := connection.Read(deleteContext)
	if err != nil || json.Unmarshal(deleteMessage, &notification) != nil || notification.Version != versionDeleted {
		t.Fatalf("delete config notification = %q, %v", deleteMessage, err)
	}
	for index := 0; index < maxUserRelays; index++ {
		if _, err := fixture.db.Exec(`INSERT INTO relays
			(server_id, owner_user_id, source_client_id, name, listen_address, listen_port, entry_host_mode, entry_host,
			 target_type, target_host, target_port, network, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, '0.0.0.0', ?, 'manual', 'node.example.com', 'manual', '1.1.1.1', 443, 'tcp', 1, ?, ?)`,
			server.Server.ID, fixture.userID, sourceClientID, "limit-"+strconv.Itoa(index), 21000+index, time.Now().Unix(), time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	limited := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"mode": "custom", "source_client_id": sourceClientID, "name": "Too Many", "target_ip": "1.1.1.1", "target_port": 443,
	}, fixture.userCookie)
	if limited.Code != http.StatusConflict || !strings.Contains(limited.Body.String(), "最多") {
		t.Fatalf("relay limit = %d, %s", limited.Code, limited.Body.String())
	}
}

func TestUserRelayModesSharesOwnerIsolationAndAdminRedaction(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	server, proxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, relaystore.UserRelayPortStart)
	assign := func(clientID, userID int64) {
		response := performRequest(t, fixture.handler, http.MethodPatch,
			"/api/admin/clients/"+strconv.FormatInt(clientID, 10)+"/assignment",
			map[string]any{"user_id": userID, "billing_period_months": 1}, fixture.adminCookie)
		if response.Code != http.StatusOK {
			t.Fatalf("assign client %d = %d, %s", clientID, response.Code, response.Body.String())
		}
	}
	createClient := func(name string) int64 {
		response := performRequest(t, fixture.handler, http.MethodPost,
			"/api/proxies/"+strconv.FormatInt(proxy.ID, 10)+"/clients", map[string]any{"name": name}, fixture.adminCookie)
		var body struct {
			Client clientResponse `json:"client"`
		}
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("create client %q = %d, %s", name, response.Code, response.Body.String())
		}
		return body.Client.ID
	}
	sourceClientID := proxy.Clients[0].ID
	targetClientID := createClient("Alice Target")
	foreignClientID := createClient("Bob Target")
	assign(sourceClientID, fixture.userID)
	assign(targetClientID, fixture.userID)
	assign(foreignClientID, fixture.otherID)
	setClientRelayPortCount(t, fixture, sourceClientID, 5)
	setClientRelayPortCount(t, fixture, targetClientID, 5)
	setClientRelayPortCount(t, fixture, foreignClientID, 5)
	registration := performRequest(t, fixture.handler, http.MethodPost, "/api/agent/register", agentRegistrationRequest{
		EnrollmentToken: server.EnrollmentToken, AgentVersion: "1.0.0",
		AgentImplementation: agentcontrol.OfficialImplementation, AgentAPIVersion: agentcontrol.CurrentAPIVersion,
		AgentCapabilities: []string{agentcontrol.CapabilityRelayRealm, agentcontrol.CapabilityManagedRuntimePurge},
	}, nil)
	if registration.Code != http.StatusCreated {
		t.Fatalf("register Agent = %d, %s", registration.Code, registration.Body.String())
	}

	sources := performRequest(t, fixture.handler, http.MethodGet, "/api/me/relay-sources", nil, fixture.userCookie)
	if sources.Code != http.StatusOK || strings.Contains(sources.Body.String(), "Subscription") ||
		strings.Contains(sources.Body.String(), "Alice Target") || strings.Contains(sources.Body.String(), "Bob Target") {
		t.Fatalf("relay source privacy = %d, %s", sources.Code, sources.Body.String())
	}
	var sourceBody struct {
		Sources []struct {
			ClientID         int64  `json:"client_id"`
			ServerName       string `json:"server_name"`
			ProxyName        string `json:"proxy_name"`
			EffectiveEnabled bool   `json:"effective_enabled"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(sources.Body.Bytes(), &sourceBody); err != nil || len(sourceBody.Sources) != 2 {
		t.Fatalf("decode relay sources = %+v, %v", sourceBody, err)
	}
	for _, source := range sourceBody.Sources {
		if source.ServerName != "Portal Server" || source.ProxyName != "Portal Proxy" || !source.EffectiveEnabled || source.ClientID == foreignClientID {
			t.Fatalf("relay source = %+v", source)
		}
	}

	for name, targetID := range map[string]int64{"foreign": foreignClientID, "same": sourceClientID} {
		response := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
			"name": name, "mode": "assigned_node", "source_client_id": sourceClientID, "target_client_id": targetID,
		}, fixture.userCookie)
		want := http.StatusNotFound
		if name == "same" {
			want = http.StatusBadRequest
		}
		if response.Code != want {
			t.Fatalf("%s target = %d, %s; want %d", name, response.Code, response.Body.String(), want)
		}
	}

	assignedCreated := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"name": "Assigned Relay", "mode": "assigned_node", "source_client_id": sourceClientID, "target_client_id": targetClientID,
	}, fixture.userCookie)
	var assignedBody struct {
		Relay myRelayResponse `json:"relay"`
	}
	if assignedCreated.Code != http.StatusCreated || json.Unmarshal(assignedCreated.Body.Bytes(), &assignedBody) != nil ||
		assignedBody.Relay.Mode != "assigned_node" || assignedBody.Relay.Target == nil ||
		assignedBody.Relay.Target.ServerName != "Portal Server" || assignedBody.Relay.Target.ProxyName != "Portal Proxy" {
		t.Fatalf("create assigned relay = %d, %s", assignedCreated.Code, assignedCreated.Body.String())
	}
	var storedServerID, storedSourceID, storedTargetID, storedTargetProxyID int64
	var storedType string
	var assignedPort int
	if err := fixture.db.QueryRow(`SELECT server_id, source_client_id, target_type, target_proxy_id, target_client_id, listen_port
		FROM relays WHERE id = ?`, assignedBody.Relay.ID).Scan(
		&storedServerID, &storedSourceID, &storedType, &storedTargetProxyID, &storedTargetID, &assignedPort,
	); err != nil {
		t.Fatal(err)
	}
	if storedServerID != server.Server.ID || storedSourceID != sourceClientID || storedType != relaystore.TargetProxy ||
		storedTargetProxyID != proxy.ID || storedTargetID != targetClientID {
		t.Fatalf("stored assigned relay = server %d source %d type %s proxy %d target %d port %d",
			storedServerID, storedSourceID, storedType, storedTargetProxyID, storedTargetID, assignedPort)
	}
	var reservedCount int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM client_relay_ports WHERE client_id = ? AND port = ?`, sourceClientID, assignedPort).Scan(&reservedCount); err != nil || reservedCount != 1 {
		t.Fatalf("assigned relay port %d reservation count = %d, %v", assignedPort, reservedCount, err)
	}

	relayService := relaystore.NewService(fixture.db)
	assignedRelay, err := relayService.Get(t.Context(), assignedBody.Relay.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTargetShare, err := proxystore.NewService(fixture.db).GetClientShareAtEndpoint(t.Context(), targetClientID, proxystore.ShareEndpoint{
		Address: assignedRelay.EntryAddress, Port: assignedRelay.ListenPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	assignedShare := performRequest(t, fixture.handler, http.MethodGet,
		"/api/me/relays/"+strconv.FormatInt(assignedBody.Relay.ID, 10)+"/share", nil, fixture.userCookie)
	var assignedShareBody struct {
		Share struct {
			URI string `json:"uri"`
		} `json:"share"`
	}
	if assignedShare.Code != http.StatusOK || json.Unmarshal(assignedShare.Body.Bytes(), &assignedShareBody) != nil ||
		assignedShareBody.Share.URI != wantTargetShare.URI {
		t.Fatalf("assigned share = %d, %s; want %s", assignedShare.Code, assignedShare.Body.String(), wantTargetShare.URI)
	}

	customCreated := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"name": "Private Custom", "mode": "custom", "source_client_id": sourceClientID,
		"target_ip": "1.1.1.1", "target_port": 5353,
	}, fixture.userCookie)
	var customBody struct {
		Relay myRelayResponse `json:"relay"`
	}
	if customCreated.Code != http.StatusCreated || json.Unmarshal(customCreated.Body.Bytes(), &customBody) != nil ||
		customBody.Relay.Mode != "custom" || customBody.Relay.TargetIP == nil || *customBody.Relay.TargetIP != "1.1.1.1" {
		t.Fatalf("create custom relay = %d, %s", customCreated.Code, customCreated.Body.String())
	}
	customRelay, err := relayService.Get(t.Context(), customBody.Relay.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantSourceShare, err := proxystore.NewService(fixture.db).GetClientShareAtEndpoint(t.Context(), sourceClientID, proxystore.ShareEndpoint{
		Address: customRelay.EntryAddress, Port: customRelay.ListenPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	customShare := performRequest(t, fixture.handler, http.MethodGet,
		"/api/me/relays/"+strconv.FormatInt(customBody.Relay.ID, 10)+"/share", nil, fixture.userCookie)
	var customShareBody struct {
		Share struct {
			URI string `json:"uri"`
		} `json:"share"`
	}
	if customShare.Code != http.StatusOK || json.Unmarshal(customShare.Body.Bytes(), &customShareBody) != nil ||
		customShareBody.Share.URI != wantSourceShare.URI {
		t.Fatalf("custom share = %d, %s; want %s", customShare.Code, customShare.Body.String(), wantSourceShare.URI)
	}

	assignedPatch := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/me/relays/"+strconv.FormatInt(assignedBody.Relay.ID, 10),
		map[string]any{"target_ip": "8.8.8.8", "target_port": 443}, fixture.userCookie)
	if assignedPatch.Code != http.StatusBadRequest {
		t.Fatalf("patch assigned relay = %d, %s", assignedPatch.Code, assignedPatch.Body.String())
	}
	customPatch := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/me/relays/"+strconv.FormatInt(customBody.Relay.ID, 10),
		map[string]any{"target_ip": "8.8.8.8", "target_port": 8443}, fixture.userCookie)
	if customPatch.Code != http.StatusOK || !strings.Contains(customPatch.Body.String(), `"target_ip":"8.8.8.8"`) ||
		!strings.Contains(customPatch.Body.String(), `"target_port":8443`) {
		t.Fatalf("patch custom relay = %d, %s", customPatch.Code, customPatch.Body.String())
	}
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		path := "/api/me/relays/" + strconv.FormatInt(customBody.Relay.ID, 10)
		body := any(nil)
		if method == http.MethodGet {
			path += "/share"
		} else {
			body = map[string]any{"target_ip": "9.9.9.9", "target_port": 443}
		}
		response := performRequest(t, fixture.handler, method, path, body, fixture.otherCookie)
		if response.Code != http.StatusNotFound {
			t.Fatalf("other user %s relay = %d, %s", method, response.Code, response.Body.String())
		}
	}

	owned := performRequest(t, fixture.handler, http.MethodGet, "/api/me/relays", nil, fixture.userCookie)
	if owned.Code != http.StatusOK || !strings.Contains(owned.Body.String(), "Assigned Relay") || !strings.Contains(owned.Body.String(), "Private Custom") ||
		!strings.Contains(owned.Body.String(), `"target_ip":"8.8.8.8"`) {
		t.Fatalf("owner relay list = %d, %s", owned.Code, owned.Body.String())
	}
	for _, path := range []string{
		"/api/admin/user-relays", "/api/relays", "/api/admin/users/" + strconv.FormatInt(fixture.userID, 10),
	} {
		response := performRequest(t, fixture.handler, http.MethodGet, path, nil, fixture.adminCookie)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Private Custom") ||
			strings.Contains(response.Body.String(), "8.8.8.8") || strings.Contains(response.Body.String(), `"target_port":8443`) {
			t.Fatalf("admin redaction %s = %d, %s", path, response.Code, response.Body.String())
		}
	}
}

func TestDeletingTargetAssignedClientPurgesUserRelay(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	server, proxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 24000)
	sourceClientID := proxy.Clients[0].ID
	targetResponse := performRequest(t, fixture.handler, http.MethodPost,
		"/api/proxies/"+strconv.FormatInt(proxy.ID, 10)+"/clients", map[string]any{"name": "Target"}, fixture.adminCookie)
	var targetBody struct {
		Client clientResponse `json:"client"`
	}
	if targetResponse.Code != http.StatusCreated || json.Unmarshal(targetResponse.Body.Bytes(), &targetBody) != nil {
		t.Fatalf("create target client = %d, %s", targetResponse.Code, targetResponse.Body.String())
	}
	for _, clientID := range []int64{sourceClientID, targetBody.Client.ID} {
		assigned := performRequest(t, fixture.handler, http.MethodPatch,
			"/api/admin/clients/"+strconv.FormatInt(clientID, 10)+"/assignment",
			map[string]any{"user_id": fixture.userID, "billing_period_months": 1}, fixture.adminCookie)
		if assigned.Code != http.StatusOK {
			t.Fatalf("assign client = %d, %s", assigned.Code, assigned.Body.String())
		}
	}
	setClientRelayPortCount(t, fixture, sourceClientID, 5)
	registration := performRequest(t, fixture.handler, http.MethodPost, "/api/agent/register", agentRegistrationRequest{
		EnrollmentToken: server.EnrollmentToken, AgentVersion: "1.0.0",
		AgentImplementation: agentcontrol.OfficialImplementation, AgentAPIVersion: agentcontrol.CurrentAPIVersion,
		AgentCapabilities: []string{agentcontrol.CapabilityRelayRealm, agentcontrol.CapabilityManagedRuntimePurge},
	}, nil)
	if registration.Code != http.StatusCreated {
		t.Fatalf("register Agent = %d, %s", registration.Code, registration.Body.String())
	}
	created := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"name": "Target Dependency", "mode": "assigned_node", "source_client_id": sourceClientID,
		"target_client_id": targetBody.Client.ID,
	}, fixture.userCookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("create target-dependent relay = %d, %s", created.Code, created.Body.String())
	}
	var versionBefore int64
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	deleted := performRequest(t, fixture.handler, http.MethodDelete,
		"/api/clients/"+strconv.FormatInt(targetBody.Client.ID, 10), nil, fixture.adminCookie)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete target client = %d, %s", deleted.Code, deleted.Body.String())
	}
	var relayCount, versionAfter int64
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM relays WHERE target_client_id = ?`, targetBody.Client.ID).Scan(&relayCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionAfter); err != nil {
		t.Fatal(err)
	}
	if relayCount != 0 || versionAfter != versionBefore+2 {
		t.Fatalf("target cleanup = relays %d version %d->%d", relayCount, versionBefore, versionAfter)
	}
}

func TestDeletingAssignedClientPurgesUserRelaysAndDesiredState(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	server, proxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 20000)
	clientID := proxy.Clients[0].ID
	assigned := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/admin/clients/"+strconv.FormatInt(clientID, 10)+"/assignment",
		map[string]any{"user_id": fixture.userID, "billing_period_months": 1}, fixture.adminCookie)
	if assigned.Code != http.StatusOK {
		t.Fatalf("assign client = %d, %s", assigned.Code, assigned.Body.String())
	}
	setClientRelayPortCount(t, fixture, clientID, 5)
	registration := performRequest(t, fixture.handler, http.MethodPost, "/api/agent/register", agentRegistrationRequest{
		EnrollmentToken: server.EnrollmentToken, AgentVersion: "1.0.0",
		AgentImplementation: agentcontrol.OfficialImplementation, AgentAPIVersion: agentcontrol.CurrentAPIVersion,
		AgentCapabilities: []string{agentcontrol.CapabilityRelayRealm, agentcontrol.CapabilityManagedRuntimePurge},
	}, nil)
	var registered agentRegistrationResponse
	if registration.Code != http.StatusCreated || json.Unmarshal(registration.Body.Bytes(), &registered) != nil {
		t.Fatalf("register Agent = %d, %s", registration.Code, registration.Body.String())
	}
	created := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"mode": "custom", "source_client_id": clientID, "name": "Dependent Relay", "target_ip": "1.1.1.1", "target_port": 443,
	}, fixture.userCookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("create dependent relay = %d, %s", created.Code, created.Body.String())
	}
	var versionBefore int64
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
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
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: agentHeaders})
	if err != nil {
		t.Fatalf("connect Agent: %v, response = %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, serverstore.NewService(fixture.db), server.Server.ID, serverstore.StatusOnline)
	deleted := performRequest(t, fixture.handler, http.MethodDelete,
		"/api/clients/"+strconv.FormatInt(clientID, 10), nil, fixture.adminCookie)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete assigned client = %d, %s", deleted.Code, deleted.Body.String())
	}
	var clientCount, relayCount int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE id = ?`, clientID).Scan(&clientCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM relays WHERE source_client_id = ?`, clientID).Scan(&relayCount); err != nil {
		t.Fatal(err)
	}
	var versionAfter int64
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionAfter); err != nil {
		t.Fatal(err)
	}
	if clientCount != 0 || relayCount != 0 || versionAfter != versionBefore+2 {
		t.Fatalf("cleanup = clients %d relays %d version %d->%d", clientCount, relayCount, versionBefore, versionAfter)
	}
	for _, expectedVersion := range []int64{versionBefore + 1, versionBefore + 2} {
		readContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		_, message, err := connection.Read(readContext)
		cancel()
		var notification struct {
			Type    string `json:"type"`
			Version int64  `json:"version"`
		}
		if err != nil || json.Unmarshal(message, &notification) != nil || notification.Type != "config_changed" || notification.Version != expectedVersion {
			t.Fatalf("cleanup notification = %q, %v; want version %d", message, err, expectedVersion)
		}
	}
	for _, path := range []string{"/api/me/nodes", "/api/me/relays"} {
		response := performRequest(t, fixture.handler, http.MethodGet, path, nil, fixture.userCookie)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "[]") {
			t.Fatalf("cleaned portal %s = %d, %s", path, response.Code, response.Body.String())
		}
	}
	proxyClients := performRequest(t, fixture.handler, http.MethodGet,
		"/api/proxies/"+strconv.FormatInt(proxy.ID, 10)+"/clients", nil, fixture.adminCookie)
	if proxyClients.Code != http.StatusOK || !strings.Contains(proxyClients.Body.String(), `"clients":[]`) {
		t.Fatalf("cleaned proxy clients = %d, %s", proxyClients.Code, proxyClients.Body.String())
	}
	config := performAgentRequest(t, fixture.handler, http.MethodGet, "/api/agent/config", nil, registered.AgentToken)
	var desired agentDesiredStateResponse
	if config.Code != http.StatusOK || json.Unmarshal(config.Body.Bytes(), &desired) != nil || len(desired.Realm.Relays) != 0 ||
		len(desired.Xray.Proxies) != 1 || len(desired.Xray.Proxies[0].Clients) != 0 {
		t.Fatalf("cleaned desired state = %d, %s", config.Code, config.Body.String())
	}
}

func TestAdminUserManagementCreatesRealAssignedClientsAndReusesExistingFlows(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	vlessServer, vlessProxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 24443)
	_, ssProxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolShadowsocks, 28388)

	vipInvitation := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/invitations", map[string]string{"role": "vip"}, fixture.adminCookie)
	var invitation invitationResponse
	if vipInvitation.Code != http.StatusCreated || json.Unmarshal(vipInvitation.Body.Bytes(), &invitation) != nil {
		t.Fatalf("create VIP invitation = %d, %s", vipInvitation.Code, vipInvitation.Body.String())
	}
	vipRegistration := performRequest(t, fixture.handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "management-vip", "password": "current-password",
	}, nil)
	if vipRegistration.Code != http.StatusCreated {
		t.Fatalf("register VIP = %d, %s", vipRegistration.Code, vipRegistration.Body.String())
	}
	vipCookie := vipRegistration.Result().Cookies()[0]
	users := performRequest(t, fixture.handler, http.MethodGet, "/api/admin/users", nil, fixture.adminCookie)
	if users.Code != http.StatusOK || !strings.Contains(users.Body.String(), `"username":"alice"`) ||
		!strings.Contains(users.Body.String(), `"username":"bob"`) || strings.Contains(users.Body.String(), `"username":"admin"`) ||
		strings.Contains(users.Body.String(), `"username":"management-vip"`) {
		t.Fatalf("admin users = %d, %s", users.Code, users.Body.String())
	}
	for name, cookie := range map[string]*http.Cookie{"vip": vipCookie, "user": fixture.userCookie} {
		response := performRequest(t, fixture.handler, http.MethodGet, "/api/admin/users", nil, cookie)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s admin users = %d, %s", name, response.Code, response.Body.String())
		}
	}

	registration := performRequest(t, fixture.handler, http.MethodPost, "/api/agent/register", agentRegistrationRequest{
		EnrollmentToken: vlessServer.EnrollmentToken, AgentVersion: "1.0.0",
		AgentImplementation: agentcontrol.OfficialImplementation, AgentAPIVersion: agentcontrol.CurrentAPIVersion,
		AgentCapabilities: []string{agentcontrol.CapabilityRelayRealm, agentcontrol.CapabilityManagedRuntimePurge},
	}, nil)
	var registered agentRegistrationResponse
	if registration.Code != http.StatusCreated || json.Unmarshal(registration.Body.Bytes(), &registered) != nil {
		t.Fatalf("register Agent = %d, %s", registration.Code, registration.Body.String())
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
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: agentHeaders})
	if err != nil {
		t.Fatalf("connect Agent: %v, response = %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, serverstore.NewService(fixture.db), vlessServer.Server.ID, serverstore.StatusOnline)

	createNode := func(proxyID int64, name string, billing int) *httptest.ResponseRecorder {
		return performRequest(t, fixture.handler, http.MethodPost,
			"/api/admin/users/"+strconv.FormatInt(fixture.userID, 10)+"/nodes",
			map[string]any{
				"proxy_id": proxyID, "name": name, "enabled": true, "client_udp443": false,
				"traffic_limit": 100, "limit_unit": "G", "traffic_reset_mode": "monthly",
				"traffic_reset_weekday": 1, "traffic_reset_day": 1, "traffic_reset_time": "00:00",
				"expires_at": nil, "billing_period_months": billing,
			}, fixture.adminCookie)
	}
	var versionBefore int64
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, vlessServer.Server.ID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	vlessCreated := createNode(vlessProxy.ID, "alice-SG", 1)
	var vlessBody struct {
		Client clientResponse `json:"client"`
	}
	if vlessCreated.Code != http.StatusCreated || json.Unmarshal(vlessCreated.Body.Bytes(), &vlessBody) != nil ||
		vlessBody.Client.AssignedUserID == nil || *vlessBody.Client.AssignedUserID != fixture.userID ||
		vlessBody.Client.BillingPeriodMonths == nil || *vlessBody.Client.BillingPeriodMonths != 1 {
		t.Fatalf("create VLESS assigned client = %d, %s", vlessCreated.Code, vlessCreated.Body.String())
	}
	var versionCreated int64
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, vlessServer.Server.ID).Scan(&versionCreated); err != nil || versionCreated != versionBefore+1 {
		t.Fatalf("create assigned version = %d, %v; want %d", versionCreated, err, versionBefore+1)
	}
	readContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	_, message, err := connection.Read(readContext)
	cancel()
	var notification struct {
		Type    string `json:"type"`
		Version int64  `json:"version"`
	}
	if err != nil || json.Unmarshal(message, &notification) != nil || notification.Type != "config_changed" || notification.Version != versionCreated {
		t.Fatalf("assigned client notification = %q, %v", message, err)
	}
	if duplicate := createNode(vlessProxy.ID, "alice-SG-duplicate", 1); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate assigned client = %d, %s", duplicate.Code, duplicate.Body.String())
	}
	if missingUser := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/users/999999/nodes",
		map[string]any{"proxy_id": vlessProxy.ID, "name": "missing"}, fixture.adminCookie); missingUser.Code != http.StatusNotFound {
		t.Fatalf("missing user = %d, %s", missingUser.Code, missingUser.Body.String())
	}
	var adminID, vipID int64
	if err := fixture.db.QueryRow(`SELECT id FROM users WHERE role = 'admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRow(`SELECT id FROM users WHERE username = 'management-vip'`).Scan(&vipID); err != nil {
		t.Fatal(err)
	}
	for _, invalidUserID := range []int64{adminID, vipID} {
		invalid := performRequest(t, fixture.handler, http.MethodPost,
			"/api/admin/users/"+strconv.FormatInt(invalidUserID, 10)+"/nodes",
			map[string]any{"proxy_id": vlessProxy.ID, "name": "invalid-role"}, fixture.adminCookie)
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf("invalid target role = %d, %s", invalid.Code, invalid.Body.String())
		}
	}
	if missingProxy := createNode(999999, "missing-proxy", 1); missingProxy.Code != http.StatusNotFound {
		t.Fatalf("missing proxy = %d, %s", missingProxy.Code, missingProxy.Body.String())
	}
	ssCreated := createNode(ssProxy.ID, "alice-SS", 12)
	var ssBody struct {
		Client clientResponse `json:"client"`
	}
	if ssCreated.Code != http.StatusCreated || json.Unmarshal(ssCreated.Body.Bytes(), &ssBody) != nil ||
		ssBody.Client.BillingPeriodMonths == nil || *ssBody.Client.BillingPeriodMonths != 12 {
		t.Fatalf("create SS assigned client = %d, %s", ssCreated.Code, ssCreated.Body.String())
	}

	proxyClients := performRequest(t, fixture.handler, http.MethodGet,
		"/api/proxies/"+strconv.FormatInt(vlessProxy.ID, 10)+"/clients", nil, fixture.adminCookie)
	if proxyClients.Code != http.StatusOK || !strings.Contains(proxyClients.Body.String(), `"id":`+strconv.FormatInt(vlessBody.Client.ID, 10)) ||
		!strings.Contains(proxyClients.Body.String(), "alice-SG") {
		t.Fatalf("proxy client list = %d, %s", proxyClients.Code, proxyClients.Body.String())
	}
	nodes := performRequest(t, fixture.handler, http.MethodGet, "/api/me/nodes", nil, fixture.userCookie)
	if nodes.Code != http.StatusOK || !strings.Contains(nodes.Body.String(), `"client_id":`+strconv.FormatInt(vlessBody.Client.ID, 10)) ||
		!strings.Contains(nodes.Body.String(), `"server_name":"Portal Server"`) ||
		!strings.Contains(nodes.Body.String(), `"proxy_name":"Portal Proxy"`) || !strings.Contains(nodes.Body.String(), `"client_name":"alice-SG"`) {
		t.Fatalf("user portal nodes = %d, %s", nodes.Code, nodes.Body.String())
	}
	for clientID, prefix := range map[int64]string{vlessBody.Client.ID: `"uri":"vless://`, ssBody.Client.ID: `"uri":"ss://`} {
		share := performRequest(t, fixture.handler, http.MethodGet,
			"/api/me/nodes/"+strconv.FormatInt(clientID, 10)+"/share", nil, fixture.userCookie)
		if share.Code != http.StatusOK || !strings.Contains(share.Body.String(), prefix) ||
			!strings.Contains(share.Body.String(), map[int64]string{vlessBody.Client.ID: "alice-SG", ssBody.Client.ID: "alice-SS"}[clientID]) {
			t.Fatalf("assigned client share = %d, %s", share.Code, share.Body.String())
		}
	}
	passwordRequest := performRequest(t, fixture.handler, http.MethodPost, "/api/account/password-reset-request", map[string]string{
		"new_password": "management-password",
	}, fixture.userCookie)
	if passwordRequest.Code != http.StatusAccepted {
		t.Fatalf("password request = %d, %s", passwordRequest.Code, passwordRequest.Body.String())
	}
	detail := performRequest(t, fixture.handler, http.MethodGet,
		"/api/admin/users/"+strconv.FormatInt(fixture.userID, 10), nil, fixture.adminCookie)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"password_request":{"id":`) ||
		!strings.Contains(detail.Body.String(), "alice-SG") || !strings.Contains(detail.Body.String(), "alice-SS") {
		t.Fatalf("user detail = %d, %s", detail.Code, detail.Body.String())
	}

	relayCreated := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"mode": "custom", "source_client_id": vlessBody.Client.ID, "name": "Managed User Relay", "target_ip": "1.1.1.1", "target_port": 443,
	}, fixture.userCookie)
	if relayCreated.Code != http.StatusCreated {
		t.Fatalf("create managed user relay = %d, %s", relayCreated.Code, relayCreated.Body.String())
	}
	var versionBeforeDelete int64
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, vlessServer.Server.ID).Scan(&versionBeforeDelete); err != nil {
		t.Fatal(err)
	}
	deleted := performRequest(t, fixture.handler, http.MethodDelete,
		"/api/clients/"+strconv.FormatInt(vlessBody.Client.ID, 10), nil, fixture.adminCookie)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("remove user node = %d, %s", deleted.Code, deleted.Body.String())
	}
	var versionAfterDelete int64
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, vlessServer.Server.ID).Scan(&versionAfterDelete); err != nil || versionAfterDelete != versionBeforeDelete+2 {
		t.Fatalf("remove node version = %d, %v; want %d", versionAfterDelete, err, versionBeforeDelete+2)
	}
	afterNodes := performRequest(t, fixture.handler, http.MethodGet, "/api/me/nodes", nil, fixture.userCookie)
	afterRelays := performRequest(t, fixture.handler, http.MethodGet, "/api/me/relays", nil, fixture.userCookie)
	afterProxyClients := performRequest(t, fixture.handler, http.MethodGet,
		"/api/proxies/"+strconv.FormatInt(vlessProxy.ID, 10)+"/clients", nil, fixture.adminCookie)
	if strings.Contains(afterNodes.Body.String(), `"client_id":`+strconv.FormatInt(vlessBody.Client.ID, 10)) ||
		strings.Contains(afterRelays.Body.String(), "Managed User Relay") ||
		strings.Contains(afterProxyClients.Body.String(), `"id":`+strconv.FormatInt(vlessBody.Client.ID, 10)) {
		t.Fatalf("removed node remains = nodes %s relays %s proxy clients %s",
			afterNodes.Body.String(), afterRelays.Body.String(), afterProxyClients.Body.String())
	}
	config := performAgentRequest(t, fixture.handler, http.MethodGet, "/api/agent/config", nil, registered.AgentToken)
	var desired agentDesiredStateResponse
	if config.Code != http.StatusOK || json.Unmarshal(config.Body.Bytes(), &desired) != nil || len(desired.Realm.Relays) != 0 ||
		len(desired.Xray.Proxies) != 1 || len(desired.Xray.Proxies[0].Clients) != 1 {
		t.Fatalf("user management desired state = %d, %s", config.Code, config.Body.String())
	}
}

func TestAdminUserManagementOnlyOffersAdminCreatedServersButKeepsLegacyAssignments(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	_, adminProxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 8443)

	vipInvitation := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/invitations",
		map[string]string{"role": "vip"}, fixture.adminCookie)
	var invitation invitationResponse
	if vipInvitation.Code != http.StatusCreated || json.Unmarshal(vipInvitation.Body.Bytes(), &invitation) != nil {
		t.Fatalf("create VIP invitation = %d, %s", vipInvitation.Code, vipInvitation.Body.String())
	}
	vipRegistration := performRequest(t, fixture.handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "resource-vip", "password": "current-password",
	}, nil)
	if vipRegistration.Code != http.StatusCreated {
		t.Fatalf("register VIP = %d, %s", vipRegistration.Code, vipRegistration.Body.String())
	}
	vipCookie := vipRegistration.Result().Cookies()[0]
	vipServerResponse := performRequest(t, fixture.handler, http.MethodPost, "/api/servers",
		map[string]string{"name": "VIP Public Server"}, vipCookie)
	var vipServer createdServerResponse
	if vipServerResponse.Code != http.StatusCreated || json.Unmarshal(vipServerResponse.Body.Bytes(), &vipServer) != nil {
		t.Fatalf("create VIP server = %d, %s", vipServerResponse.Code, vipServerResponse.Body.String())
	}
	vipProxyResponse := performRequest(t, fixture.handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: vipServer.Server.ID, Name: "VIP Proxy", Protocol: proxystore.ProtocolVLESS, ListenPort: 9443,
		EntryHostMode: "manual", EntryHost: "vip.example.com", FirstClientName: "Legacy",
		Security: proxystore.SecurityReality, ServerName: "www.example.com", RealityTarget: "www.example.com:443",
	}, vipCookie)
	var vipProxyBody struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if vipProxyResponse.Code != http.StatusCreated || json.Unmarshal(vipProxyResponse.Body.Bytes(), &vipProxyBody) != nil {
		t.Fatalf("create VIP proxy = %d, %s", vipProxyResponse.Code, vipProxyResponse.Body.String())
	}

	if denied := performRequest(t, fixture.handler, http.MethodGet, "/api/admin/distributable-proxies", nil, vipCookie); denied.Code != http.StatusForbidden {
		t.Fatalf("VIP distributable proxy list = %d, %s", denied.Code, denied.Body.String())
	}
	distributable := performRequest(t, fixture.handler, http.MethodGet, "/api/admin/distributable-proxies", nil, fixture.adminCookie)
	if distributable.Code != http.StatusOK || !strings.Contains(distributable.Body.String(), `"id":`+strconv.FormatInt(adminProxy.ID, 10)) ||
		strings.Contains(distributable.Body.String(), `"id":`+strconv.FormatInt(vipProxyBody.Proxy.ID, 10)) {
		t.Fatalf("distributable proxy list = %d, %s", distributable.Code, distributable.Body.String())
	}
	blocked := performRequest(t, fixture.handler, http.MethodPost,
		"/api/admin/users/"+strconv.FormatInt(fixture.userID, 10)+"/nodes", map[string]any{
			"proxy_id": vipProxyBody.Proxy.ID, "name": "blocked-client",
		}, fixture.adminCookie)
	if blocked.Code != http.StatusBadRequest || !strings.Contains(blocked.Body.String(), "仅管理员创建的服务器节点可分配给普通用户") {
		t.Fatalf("VIP-origin assignment = %d, %s", blocked.Code, blocked.Body.String())
	}
	blockedPublishedNode := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/subscription/nodes", map[string]any{
		"name": "blocked", "mode": "direct", "target_proxy_id": vipProxyBody.Proxy.ID,
	}, fixture.adminCookie)
	if blockedPublishedNode.Code != http.StatusBadRequest || !strings.Contains(blockedPublishedNode.Body.String(), "订阅发布节点只能使用管理员创建的服务器") {
		t.Fatalf("VIP-origin published node = %d, %s", blockedPublishedNode.Code, blockedPublishedNode.Body.String())
	}
	blockedRelayNode := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/subscription/nodes", map[string]any{
		"name": "blocked-relay", "mode": "relay", "source_proxy_id": vipProxyBody.Proxy.ID, "target_proxy_id": adminProxy.ID,
	}, fixture.adminCookie)
	if blockedRelayNode.Code != http.StatusBadRequest || !strings.Contains(blockedRelayNode.Body.String(), "订阅发布节点只能使用管理员创建的服务器") {
		t.Fatalf("VIP-origin relay source = %d, %s", blockedRelayNode.Code, blockedRelayNode.Body.String())
	}
	legacyClientID := vipProxyBody.Proxy.Clients[0].ID
	if _, err := fixture.db.Exec(`UPDATE clients SET assigned_user_id = ? WHERE id = ?`, fixture.userID, legacyClientID); err != nil {
		t.Fatal(err)
	}
	detail := performRequest(t, fixture.handler, http.MethodGet,
		"/api/admin/users/"+strconv.FormatInt(fixture.userID, 10), nil, fixture.adminCookie)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"proxy_id":`+strconv.FormatInt(adminProxy.ID, 10)) ||
		!strings.Contains(detail.Body.String(), `"proxy_id":`+strconv.FormatInt(vipProxyBody.Proxy.ID, 10)) ||
		!strings.Contains(detail.Body.String(), `"id":`+strconv.FormatInt(legacyClientID, 10)) {
		t.Fatalf("legacy assignment detail = %d, %s", detail.Code, detail.Body.String())
	}
}
