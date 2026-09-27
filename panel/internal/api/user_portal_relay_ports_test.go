package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func createManagedPortalClient(t *testing.T, fixture userPortalFixture, proxyID int64, name string, count *int) clientResponse {
	t.Helper()
	payload := map[string]any{"proxy_id": proxyID, "name": name}
	if count != nil {
		payload["user_relay_port_count"] = *count
	}
	response := performRequest(t, fixture.handler, http.MethodPost,
		"/api/admin/users/"+strconv.FormatInt(fixture.userID, 10)+"/nodes", payload, fixture.adminCookie)
	var body struct {
		Client clientResponse `json:"client"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("create managed client %q = %d, %s", name, response.Code, response.Body.String())
	}
	return body.Client
}

func registerRelayCapableAgent(t *testing.T, fixture userPortalFixture, server createdServerResponse) agentRegistrationResponse {
	t.Helper()
	response := performRequest(t, fixture.handler, http.MethodPost, "/api/agent/register", agentRegistrationRequest{
		EnrollmentToken: server.EnrollmentToken, AgentVersion: "1.0.0",
		AgentImplementation: agentcontrol.OfficialImplementation, AgentAPIVersion: agentcontrol.CurrentAPIVersion,
		AgentCapabilities: []string{agentcontrol.CapabilityRelayRealm, agentcontrol.CapabilityManagedRuntimePurge},
	}, nil)
	var registered agentRegistrationResponse
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &registered) != nil {
		t.Fatalf("register relay-capable Agent = %d, %s", response.Code, response.Body.String())
	}
	return registered
}

func TestManagedClientRelayPortDefaultsValidationAndSafeReallocation(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	_, firstProxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 8443)
	defaultClient := createManagedPortalClient(t, fixture, firstProxy.ID, "Default Ports", nil)
	if defaultClient.UserRelayPortCount != 5 || defaultClient.UserRelayPortStart == nil || defaultClient.UserRelayPortEnd == nil ||
		*defaultClient.UserRelayPortEnd-*defaultClient.UserRelayPortStart != 4 {
		t.Fatalf("default relay port allocation = %+v", defaultClient)
	}

	for index, count := range []int{-1, 6} {
		_, proxyValue := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 8500+index)
		response := performRequest(t, fixture.handler, http.MethodPost,
			"/api/admin/users/"+strconv.FormatInt(fixture.userID, 10)+"/nodes",
			map[string]any{"proxy_id": proxyValue.ID, "name": "Invalid", "user_relay_port_count": count}, fixture.adminCookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid count %d = %d, %s", count, response.Code, response.Body.String())
		}
	}

	updated := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/admin/clients/"+strconv.FormatInt(defaultClient.ID, 10)+"/relay-ports",
		map[string]any{"user_relay_port_count": 0}, fixture.adminCookie)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"user_relay_port_count":0`) {
		t.Fatalf("release client relay ports = %d, %s", updated.Code, updated.Body.String())
	}
	var count int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM client_relay_ports WHERE client_id = ?`, defaultClient.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("released relay port rows = %d, %v", count, err)
	}
}

func TestUserRelaySourcesAndBothModesUseOnlyReservedPoolUntilExhausted(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	sourceServer, sourceProxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 8443)
	_, targetProxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 9443)
	_, zeroProxy := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 10443)
	two, one, zero := 2, 1, 0
	source := createManagedPortalClient(t, fixture, sourceProxy.ID, "Source", &two)
	target := createManagedPortalClient(t, fixture, targetProxy.ID, "Target", &one)
	zeroClient := createManagedPortalClient(t, fixture, zeroProxy.ID, "No Relay Ports", &zero)
	registerRelayCapableAgent(t, fixture, sourceServer)

	nodes := performRequest(t, fixture.handler, http.MethodGet, "/api/me/nodes", nil, fixture.userCookie)
	if nodes.Code != http.StatusOK || !strings.Contains(nodes.Body.String(), `"client_id":`+strconv.FormatInt(zeroClient.ID, 10)) ||
		strings.Contains(nodes.Body.String(), "user_relay_port") {
		t.Fatalf("zero-port client missing from nodes = %d, %s", nodes.Code, nodes.Body.String())
	}
	sources := performRequest(t, fixture.handler, http.MethodGet, "/api/me/relay-sources", nil, fixture.userCookie)
	if sources.Code != http.StatusOK || !strings.Contains(sources.Body.String(), `"client_id":`+strconv.FormatInt(source.ID, 10)) ||
		!strings.Contains(sources.Body.String(), `"client_id":`+strconv.FormatInt(target.ID, 10)) ||
		strings.Contains(sources.Body.String(), `"client_id":`+strconv.FormatInt(zeroClient.ID, 10)) {
		t.Fatalf("relay sources = %d, %s", sources.Code, sources.Body.String())
	}

	custom := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"name": "Custom", "mode": "custom", "source_client_id": source.ID,
		"target_ip": "1.1.1.1", "target_port": 443,
	}, fixture.userCookie)
	if custom.Code != http.StatusCreated {
		t.Fatalf("custom relay = %d, %s", custom.Code, custom.Body.String())
	}
	assigned := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"name": "Assigned", "mode": "assigned_node", "source_client_id": source.ID,
		"target_client_id": target.ID,
	}, fixture.userCookie)
	if assigned.Code != http.StatusCreated {
		t.Fatalf("assigned-node relay = %d, %s", assigned.Code, assigned.Body.String())
	}
	var relayCount, distinctRelayPorts, unreservedCount int
	if err := fixture.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT listen_port) FROM relays WHERE source_client_id = ?`, source.ID).
		Scan(&relayCount, &distinctRelayPorts); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM relays
		WHERE source_client_id = ? AND listen_port NOT IN (SELECT port FROM client_relay_ports WHERE client_id = ?)`,
		source.ID, source.ID).Scan(&unreservedCount); err != nil {
		t.Fatal(err)
	}
	if relayCount != 2 || distinctRelayPorts != 2 || unreservedCount != 0 {
		t.Fatalf("source relay pool usage = relays %d distinct %d unreserved %d", relayCount, distinctRelayPorts, unreservedCount)
	}
	exhausted := performRequest(t, fixture.handler, http.MethodPost, "/api/me/relays", map[string]any{
		"name": "Exhausted", "mode": "custom", "source_client_id": source.ID,
		"target_ip": "1.0.0.1", "target_port": 443,
	}, fixture.userCookie)
	if exhausted.Code != http.StatusConflict || !strings.Contains(exhausted.Body.String(), "该节点可用中转端口已用尽") {
		t.Fatalf("exhausted source = %d, %s", exhausted.Code, exhausted.Body.String())
	}
	blocked := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/admin/clients/"+strconv.FormatInt(source.ID, 10)+"/relay-ports",
		map[string]any{"user_relay_port_count": 1}, fixture.adminCookie)
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "请先删除中转后调整端口") {
		t.Fatalf("active relay reallocation = %d, %s", blocked.Code, blocked.Body.String())
	}
}

func TestUserCanRenameOnlyOwnedClientAndReceivesDesiredNotification(t *testing.T) {
	fixture := setupUserPortalFixture(t)
	defer fixture.db.Close()
	server, proxyValue := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 8443)
	clientID := proxyValue.Clients[0].ID
	assigned := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/admin/clients/"+strconv.FormatInt(clientID, 10)+"/assignment",
		map[string]any{"user_id": fixture.userID, "billing_period_months": 1}, fixture.adminCookie)
	if assigned.Code != http.StatusOK {
		t.Fatalf("assign client = %d, %s", assigned.Code, assigned.Body.String())
	}
	registered := registerRelayCapableAgent(t, fixture, server)
	panel := httptest.NewServer(fixture.handler)
	defer panel.Close()
	headers := http.Header{"Authorization": []string{"Bearer " + registered.AgentToken}}
	headers.Set("X-VPS-Panel-Agent-Implementation", agentcontrol.OfficialImplementation)
	headers.Set("X-VPS-Panel-Agent-Version", "1.0.0")
	headers.Set("X-VPS-Panel-Agent-API", strconv.Itoa(agentcontrol.CurrentAPIVersion))
	headers.Set("X-VPS-Panel-Agent-Capabilities", strings.Join([]string{
		agentcontrol.CapabilityRelayRealm, agentcontrol.CapabilityManagedRuntimePurge,
	}, ","))
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("connect Agent: %v, response = %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, serverstore.NewService(fixture.db), server.Server.ID, serverstore.StatusOnline)

	for name, payload := range map[string]map[string]any{
		"empty":   {"name": ""},
		"long":    {"name": strings.Repeat("a", 101)},
		"unknown": {"name": "Bypass", "enabled": false},
	} {
		invalid := performRequest(t, fixture.handler, http.MethodPatch,
			"/api/me/nodes/"+strconv.FormatInt(clientID, 10), payload, fixture.userCookie)
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf("%s rename = %d, %s", name, invalid.Code, invalid.Body.String())
		}
	}
	other := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/me/nodes/"+strconv.FormatInt(clientID, 10), map[string]any{"name": "Other"}, fixture.otherCookie)
	if other.Code != http.StatusNotFound {
		t.Fatalf("other user rename = %d, %s", other.Code, other.Body.String())
	}
	admin := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/me/nodes/"+strconv.FormatInt(clientID, 10), map[string]any{"name": "Admin"}, fixture.adminCookie)
	if admin.Code != http.StatusForbidden {
		t.Fatalf("admin rename = %d, %s", admin.Code, admin.Body.String())
	}
	invitationResult := performRequest(t, fixture.handler, http.MethodPost, "/api/admin/invitations", map[string]string{"role": "vip"}, fixture.adminCookie)
	var invitation invitationResponse
	if invitationResult.Code != http.StatusCreated || json.Unmarshal(invitationResult.Body.Bytes(), &invitation) != nil {
		t.Fatalf("create VIP invitation = %d, %s", invitationResult.Code, invitationResult.Body.String())
	}
	vipResult := performRequest(t, fixture.handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "rename-vip", "password": "current-password",
	}, nil)
	if vipResult.Code != http.StatusCreated {
		t.Fatalf("register VIP = %d, %s", vipResult.Code, vipResult.Body.String())
	}
	vip := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/me/nodes/"+strconv.FormatInt(clientID, 10), map[string]any{"name": "VIP"}, vipResult.Result().Cookies()[0])
	if vip.Code != http.StatusForbidden {
		t.Fatalf("VIP rename = %d, %s", vip.Code, vip.Body.String())
	}

	var credential string
	var udp443, enabled int
	var expiresAt, trafficLimit any
	if err := fixture.db.QueryRow(`SELECT credential_json, client_udp443, enabled, expires_at, traffic_limit_bytes
		FROM clients WHERE id = ?`, clientID).Scan(&credential, &udp443, &enabled, &expiresAt, &trafficLimit); err != nil {
		t.Fatal(err)
	}
	var versionBefore int64
	if err := fixture.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	renamed := performRequest(t, fixture.handler, http.MethodPatch,
		"/api/me/nodes/"+strconv.FormatInt(clientID, 10), map[string]any{"name": "Renamed Client"}, fixture.userCookie)
	if renamed.Code != http.StatusOK || !strings.Contains(renamed.Body.String(), `"name":"Renamed Client"`) {
		t.Fatalf("owned rename = %d, %s", renamed.Code, renamed.Body.String())
	}
	readContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, message, err := connection.Read(readContext)
	if err != nil {
		t.Fatalf("read rename notification: %v", err)
	}
	var notification struct {
		Type    string `json:"type"`
		Version int64  `json:"version"`
	}
	if json.Unmarshal(message, &notification) != nil || notification.Type != "config_changed" || notification.Version != versionBefore+1 {
		t.Fatalf("rename notification = %s", message)
	}
	var storedName, storedCredential string
	var storedUDP443, storedEnabled int
	var storedExpiresAt, storedTrafficLimit any
	if err := fixture.db.QueryRow(`SELECT name, credential_json, client_udp443, enabled, expires_at, traffic_limit_bytes
		FROM clients WHERE id = ?`, clientID).Scan(
		&storedName, &storedCredential, &storedUDP443, &storedEnabled, &storedExpiresAt, &storedTrafficLimit,
	); err != nil {
		t.Fatal(err)
	}
	if storedName != "Renamed Client" || storedCredential != credential || storedUDP443 != udp443 || storedEnabled != enabled ||
		storedExpiresAt != expiresAt || storedTrafficLimit != trafficLimit {
		t.Fatalf("rename changed other fields: name=%q credential=%t udp=%d/%d enabled=%d/%d expires=%v/%v traffic=%v/%v",
			storedName, storedCredential == credential, storedUDP443, udp443, storedEnabled, enabled,
			storedExpiresAt, expiresAt, storedTrafficLimit, trafficLimit)
	}
	nodes := performRequest(t, fixture.handler, http.MethodGet, "/api/me/nodes", nil, fixture.userCookie)
	if nodes.Code != http.StatusOK || !strings.Contains(nodes.Body.String(), `"client_name":"Renamed Client"`) {
		t.Fatalf("renamed node response = %d, %s", nodes.Code, nodes.Body.String())
	}
	share := performRequest(t, fixture.handler, http.MethodGet,
		"/api/me/nodes/"+strconv.FormatInt(clientID, 10)+"/share", nil, fixture.userCookie)
	var shareBody struct {
		Share struct {
			URI string `json:"uri"`
		} `json:"share"`
	}
	if share.Code != http.StatusOK || json.Unmarshal(share.Body.Bytes(), &shareBody) != nil {
		t.Fatalf("renamed share = %d, %s", share.Code, share.Body.String())
	}
	parsed, err := url.Parse(shareBody.Share.URI)
	if err != nil || parsed.Fragment != "Portal Proxy - Renamed Client" {
		t.Fatalf("renamed share fragment = %q, %v", parsed.Fragment, err)
	}
}
