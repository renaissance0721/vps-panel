package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestProxyAPIAuthenticationLifecycleAndDesiredState(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/api/proxies"}, {http.MethodPost, "/api/proxies"},
		{http.MethodGet, "/api/proxies/1"}, {http.MethodPatch, "/api/proxies/1"},
		{http.MethodDelete, "/api/proxies/1"}, {http.MethodGet, "/api/proxies/1/clients"},
		{http.MethodPost, "/api/proxies/1/clients"}, {http.MethodGet, "/api/clients/1"},
		{http.MethodPatch, "/api/clients/1"}, {http.MethodDelete, "/api/clients/1"},
		{http.MethodPost, "/api/clients/1/traffic/reset"},
		{http.MethodGet, "/api/clients/1/share"},
	} {
		response := performRequest(t, handler, target.method, target.path, nil, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s status = %d", target.method, target.path, response.Code)
		}
	}

	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{"username": "admin", "password": "strong-password"}, nil)
	if initialization.Code != http.StatusCreated {
		t.Fatalf("initialize = %d, %s", initialization.Code, initialization.Body.String())
	}
	cookie := initialization.Result().Cookies()[0]
	serverCreation := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "Proxy Server"}, cookie)
	var createdServer createdServerResponse
	if err := json.Unmarshal(serverCreation.Body.Bytes(), &createdServer); err != nil {
		t.Fatal(err)
	}
	registration := performRequest(t, handler, http.MethodPost, "/api/agent/register", agentRegistrationRequest{EnrollmentToken: createdServer.EnrollmentToken, AgentVersion: "test", ExistingConfig: false}, nil)
	if registration.Code != http.StatusCreated {
		t.Fatalf("register Agent = %d, %s", registration.Code, registration.Body.String())
	}
	var registered agentRegistrationResponse
	if err := json.Unmarshal(registration.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	setAgentCapabilities(t, db, createdServer.Server.ID, "vps-panel-agent", []string{
		agentcontrol.CapabilityManagedRuntimePurge,
		agentcontrol.CapabilityProxyVLESSReality,
	})

	creation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: createdServer.Server.ID, Name: "Reality", ListenPort: 443, Security: "reality",
		ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Phone",
	}, cookie)
	if creation.Code != http.StatusCreated {
		t.Fatalf("create proxy = %d, %s", creation.Code, creation.Body.String())
	}
	var created struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if err := json.Unmarshal(creation.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Proxy.Clients) != 1 ||
		created.Proxy.EntryHostMode != "auto" || created.Proxy.EntryHost != "" || created.Proxy.EntryAddress != "" {
		t.Fatalf("created proxy = %+v", created.Proxy)
	}
	var configJSON string
	if err := db.QueryRow(`SELECT config_json FROM proxies WHERE id = ?`, created.Proxy.ID).Scan(&configJSON); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(configJSON), &stored); err != nil {
		t.Fatal(err)
	}
	privateKey := stored["reality"].(map[string]any)["private_key"].(string)
	publicKey := stored["reality"].(map[string]any)["public_key"].(string)
	list := performRequest(t, handler, http.MethodGet, "/api/proxies", nil, cookie)
	detail := performRequest(t, handler, http.MethodGet, "/api/proxies/"+strconv.FormatInt(created.Proxy.ID, 10), nil, cookie)
	if strings.Contains(list.Body.String(), privateKey) || strings.Contains(detail.Body.String(), privateKey) ||
		strings.Contains(list.Body.String(), publicKey) || strings.Contains(detail.Body.String(), publicKey) {
		t.Fatal("ordinary proxy API leaked REALITY key material")
	}

	agentConfigRequest := httptest.NewRequest(http.MethodGet, "/api/agent/config", nil)
	agentConfigRequest.Header.Set("Authorization", "Bearer "+registered.AgentToken)
	agentConfigResponse := httptest.NewRecorder()
	handler.ServeHTTP(agentConfigResponse, agentConfigRequest)
	if agentConfigResponse.Code != http.StatusOK || !strings.Contains(agentConfigResponse.Body.String(), privateKey) ||
		!strings.Contains(agentConfigResponse.Body.String(), `"enabled":true`) ||
		!strings.Contains(agentConfigResponse.Body.String(), `"uuid"`) {
		t.Fatalf("Agent desired config = %d, %s", agentConfigResponse.Code, agentConfigResponse.Body.String())
	}

	clientID := created.Proxy.Clients[0].ID
	var credentialJSON string
	if err := db.QueryRow(`SELECT credential_json FROM clients WHERE id = ?`, clientID).Scan(&credentialJSON); err != nil {
		t.Fatal(err)
	}
	var storedClientCredential struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal([]byte(credentialJSON), &storedClientCredential); err != nil || storedClientCredential.UUID == "" {
		t.Fatalf("stored Client credential = %q, %v", credentialJSON, err)
	}
	client := performRequest(t, handler, http.MethodGet, "/api/clients/"+strconv.FormatInt(clientID, 10), nil, cookie)
	if client.Code != http.StatusOK || strings.Contains(client.Body.String(), `"uuid":`) ||
		strings.Contains(client.Body.String(), `"uuid_summary":`) {
		t.Fatalf("client detail = %d, %s", client.Code, client.Body.String())
	}
	for name, response := range map[string]*httptest.ResponseRecorder{
		"create proxy": creation, "list proxies": list, "proxy detail": detail, "client detail": client,
	} {
		if strings.Contains(response.Body.String(), `"uuid":`) || strings.Contains(response.Body.String(), `"uuid_summary":`) {
			t.Fatalf("%s leaked Client UUID field: %s", name, response.Body.String())
		}
	}
	if _, err := db.Exec(`UPDATE proxies SET entry_host_mode = 'manual', entry_host = 'node.example.com' WHERE id = ?`, created.Proxy.ID); err != nil {
		t.Fatal(err)
	}
	shareResponse := performRequest(t, handler, http.MethodGet, "/api/clients/"+strconv.FormatInt(clientID, 10)+"/share", nil, cookie)
	var share struct {
		Share clientShareResponse `json:"share"`
	}
	if shareResponse.Code != http.StatusOK || json.Unmarshal(shareResponse.Body.Bytes(), &share) != nil {
		t.Fatalf("client share = %d, %s", shareResponse.Code, shareResponse.Body.String())
	}
	parsedURI, err := url.Parse(share.Share.URI)
	if err != nil || parsedURI.User.Username() != storedClientCredential.UUID || parsedURI.Query().Get("pbk") != publicKey ||
		strings.Contains(shareResponse.Body.String(), `"uuid":`) ||
		strings.Contains(shareResponse.Body.String(), `"uuid_summary":`) ||
		strings.Contains(shareResponse.Body.String(), `"reality_public_key"`) ||
		strings.Contains(shareResponse.Body.String(), `"reality_short_id"`) ||
		strings.Contains(shareResponse.Body.String(), `"private_key"`) {
		t.Fatalf("client share did not keep key material confined to URI: %s", shareResponse.Body.String())
	}
	var versionBeforeExpiry int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, createdServer.Server.ID).Scan(&versionBeforeExpiry); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE clients SET expires_at = 1, effective_enabled_snapshot = 1 WHERE id = ?`, clientID); err != nil {
		t.Fatal(err)
	}
	expiredConfigRequest := httptest.NewRequest(http.MethodGet, "/api/agent/config", nil)
	expiredConfigRequest.Header.Set("Authorization", "Bearer "+registered.AgentToken)
	expiredConfig := httptest.NewRecorder()
	handler.ServeHTTP(expiredConfig, expiredConfigRequest)
	if expiredConfig.Code != http.StatusOK || strings.Contains(expiredConfig.Body.String(), storedClientCredential.UUID) {
		t.Fatalf("expired client remained in polled desired state: %d, %s", expiredConfig.Code, expiredConfig.Body.String())
	}
	var versionAfterExpiry int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, createdServer.Server.ID).Scan(&versionAfterExpiry); err != nil || versionAfterExpiry != versionBeforeExpiry+1 {
		t.Fatalf("expiry reconciliation version = %d, want %d, error %v", versionAfterExpiry, versionBeforeExpiry+1, err)
	}
	shareAfterExpiry := performRequest(t, handler, http.MethodGet, "/api/clients/"+strconv.FormatInt(clientID, 10)+"/share", nil, cookie)
	var expiredShare struct {
		Share clientShareResponse `json:"share"`
	}
	if shareAfterExpiry.Code != http.StatusOK || json.Unmarshal(shareAfterExpiry.Body.Bytes(), &expiredShare) != nil || expiredShare.Share.URI != share.Share.URI {
		t.Fatalf("expired client share changed = %d, %s", shareAfterExpiry.Code, shareAfterExpiry.Body.String())
	}
	deletion := performRequest(t, handler, http.MethodDelete, "/api/proxies/"+strconv.FormatInt(created.Proxy.ID, 10), nil, cookie)
	if deletion.Code != http.StatusNoContent {
		t.Fatalf("delete proxy = %d, %s", deletion.Code, deletion.Body.String())
	}
	var clients int
	if err := db.QueryRow(`SELECT COUNT(*) FROM clients WHERE proxy_id = ?`, created.Proxy.ID).Scan(&clients); err != nil || clients != 0 {
		t.Fatalf("clients after proxy delete = %d, %v", clients, err)
	}
}

func TestProxyAPIReturnsUsefulValidationErrors(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{"username": "admin", "password": "strong-password"}, nil)
	cookie := initialization.Result().Cookies()[0]
	serverCreation := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "Server"}, cookie)
	var createdServer createdServerResponse
	_ = json.Unmarshal(serverCreation.Body.Bytes(), &createdServer)
	for _, test := range []struct {
		name    string
		body    createProxyRequest
		status  int
		message string
	}{
		{"port", createProxyRequest{ServerID: createdServer.Server.ID, Name: "bad", ListenPort: 0}, http.StatusBadRequest, "监听端口"},
		{"mode", createProxyRequest{ServerID: createdServer.Server.ID, Name: "bad", ListenPort: 443, EntryHostMode: "invalid", Security: "reality", ServerName: "example.com", RealityTarget: "example.com:443"}, http.StatusBadRequest, "入口地址模式"},
		{"host", createProxyRequest{ServerID: createdServer.Server.ID, Name: "bad", ListenPort: 443, EntryHostMode: "manual", EntryHost: "https://example.com", Security: "reality", ServerName: "example.com", RealityTarget: "example.com:443"}, http.StatusBadRequest, "手动入口地址"},
		{"tls", createProxyRequest{ServerID: createdServer.Server.ID, Name: "bad", ListenPort: 443, Security: "tls", TLSMode: "manual", ServerName: "example.com"}, http.StatusBadRequest, "TLS 证书"},
	} {
		response := performRequest(t, handler, http.MethodPost, "/api/proxies", test.body, cookie)
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.message) {
			t.Fatalf("%s response = %d, %s", test.name, response.Code, response.Body.String())
		}
	}
}

func TestProxyDeleteReferenceHandling(t *testing.T) {
	const subscriptionMessage = "代理节点正在被订阅发布节点使用，请先在订阅管理中删除或调整相关发布节点"

	t.Run("unreferenced proxy", func(t *testing.T) {
		fixture := newProxyDeleteFixture(t)
		serverID := fixture.createServer(t, "unreferenced")
		proxyValue := fixture.createProxy(t, serverID, 443, "unreferenced")

		response := fixture.deleteProxy(t, proxyValue.ID)
		if response.Code != http.StatusNoContent {
			t.Fatalf("delete unreferenced proxy = %d, %s", response.Code, response.Body.String())
		}
		assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM proxies WHERE id = ?`, 0, proxyValue.ID)
	})

	t.Run("ordinary relay target", func(t *testing.T) {
		fixture := newProxyDeleteFixture(t)
		serverID := fixture.createServer(t, "ordinary relay")
		proxyValue := fixture.createProxy(t, serverID, 443, "target")
		result, err := fixture.db.Exec(`INSERT INTO relays
			(server_id, name, listen_address, listen_port, target_type, target_proxy_id, network, created_at, updated_at)
			VALUES (?, 'ordinary', '0.0.0.0', 20000, 'proxy', ?, 'tcp', 1, 1)`, serverID, proxyValue.ID)
		if err != nil {
			t.Fatal(err)
		}
		relayID, _ := result.LastInsertId()

		response := fixture.deleteProxy(t, proxyValue.ID)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "代理节点正在被中转规则使用") {
			t.Fatalf("delete relay target = %d, %s", response.Code, response.Body.String())
		}
		assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM proxies WHERE id = ?`, 1, proxyValue.ID)
		assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM relays WHERE id = ?`, 1, relayID)
	})

	t.Run("direct published target preserves related data", func(t *testing.T) {
		fixture := newProxyDeleteFixture(t)
		serverID := fixture.createServer(t, "direct")
		proxyValue := fixture.createProxy(t, serverID, 443, "direct target")
		plan, err := fixture.subscriptions.CreatePlan(t.Context(), subscriptionstore.CreatePlanInput{Name: "plan", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.db.Exec(`INSERT INTO users
			(id, username, password_hash, role, created_at, updated_at)
			VALUES (100, 'subscriber', 'hash', 'subscriber', 1, 1),
			       (101, 'relay-user', 'hash', 'user', 1, 1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.db.Exec(`INSERT INTO subscriber_profiles
			(user_id, plan_id, enabled, subscription_token, created_at, updated_at)
			VALUES (100, ?, 1, 'subscriber-token', 1, 1)`, plan.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.db.Exec(`INSERT INTO subscriber_usage
			(user_id, cycle_started_at, updated_at) VALUES (100, 1, 1)`); err != nil {
			t.Fatal(err)
		}
		node, _, _, err := fixture.subscriptions.CreatePublishedNodeWithPlans(t.Context(), subscriptionstore.CreatePublishedNodeInput{
			Name: "direct node", Mode: subscriptionstore.NodeModeDirect, TargetProxyID: proxyValue.ID,
			PlanIDs: []int64{plan.ID}, Enabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := fixture.db.Exec(`INSERT INTO relays
			(server_id, owner_user_id, source_client_id, name, listen_address, listen_port,
			 target_type, target_host, target_port, network, created_at, updated_at)
			VALUES (?, 101, ?, 'user relay', '0.0.0.0', 20000,
			 'manual', 'example.com', 443, 'tcp', 1, 1)`, serverID, proxyValue.Clients[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		userRelayID, _ := result.LastInsertId()

		response := fixture.deleteProxy(t, proxyValue.ID)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), subscriptionMessage) {
			t.Fatalf("delete direct published target = %d, %s", response.Code, response.Body.String())
		}
		assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM proxies WHERE id = ?`, 1, proxyValue.ID)
		assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM subscription_published_nodes WHERE id = ?`, 1, node.ID)
		assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM subscription_plan_nodes WHERE plan_id = ? AND published_node_id = ?`, 1, plan.ID, node.ID)
		assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM subscriber_clients WHERE user_id = 100 AND proxy_id = ?`, 1, proxyValue.ID)
		assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM relays WHERE id = ?`, 1, userRelayID)

		if _, _, err := fixture.subscriptions.SetPlanNodes(t.Context(), plan.ID, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.subscriptions.DeletePublishedNode(t.Context(), node.ID); err != nil {
			t.Fatal(err)
		}
		response = fixture.deleteProxy(t, proxyValue.ID)
		if response.Code != http.StatusNoContent {
			t.Fatalf("delete proxy after published node removal = %d, %s", response.Code, response.Body.String())
		}
		assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM proxies WHERE id = ?`, 0, proxyValue.ID)
		assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM relays WHERE id = ?`, 0, userRelayID)
	})

	for _, test := range []struct {
		name       string
		deleteID   func(source, target proxystore.Proxy) int64
		wantStatus int
	}{
		{name: "relay source proxy is independent", deleteID: func(source, _ proxystore.Proxy) int64 { return source.ID }, wantStatus: http.StatusNoContent},
		{name: "relay published target", deleteID: func(_, target proxystore.Proxy) int64 { return target.ID }, wantStatus: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProxyDeleteFixture(t)
			sourceServerID := fixture.createServer(t, "source")
			targetServerID := fixture.createServer(t, "target")
			sourceProxy := fixture.createProxy(t, sourceServerID, 8443, "source")
			targetProxy := fixture.createProxy(t, targetServerID, 443, "target")
			node, _, err := fixture.subscriptions.CreatePublishedNode(t.Context(), subscriptionstore.CreatePublishedNodeInput{
				Name: "relay node", Mode: subscriptionstore.NodeModeRelay, SourceServerID: &sourceServerID,
				TargetProxyID: targetProxy.ID, Enabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			deleteID := test.deleteID(sourceProxy, targetProxy)
			response := fixture.deleteProxy(t, deleteID)
			if response.Code != test.wantStatus || test.wantStatus == http.StatusConflict && !strings.Contains(response.Body.String(), subscriptionMessage) {
				t.Fatalf("delete subscription proxy = %d, %s", response.Code, response.Body.String())
			}
			wantProxyCount := 1
			if test.wantStatus == http.StatusNoContent {
				wantProxyCount = 0
			}
			assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM proxies WHERE id = ?`, wantProxyCount, deleteID)
			assertDatabaseCount(t, fixture.db, `SELECT COUNT(*) FROM subscription_published_nodes WHERE id = ?`, 1, node.ID)
		})
	}
}

type proxyDeleteFixture struct {
	db            *sql.DB
	handler       http.Handler
	cookie        *http.Cookie
	proxies       *proxystore.Service
	subscriptions *subscriptionstore.Service
}

func newProxyDeleteFixture(t *testing.T) proxyDeleteFixture {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	handler := NewHandler(db, t.TempDir())
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initialization.Code != http.StatusCreated {
		t.Fatalf("initialize = %d, %s", initialization.Code, initialization.Body.String())
	}
	return proxyDeleteFixture{
		db: db, handler: handler, cookie: initialization.Result().Cookies()[0],
		proxies: proxystore.NewService(db), subscriptions: subscriptionstore.NewService(db, relaystore.NewService(db)),
	}
}

func (f proxyDeleteFixture) createServer(t *testing.T, name string) int64 {
	t.Helper()
	result, err := f.db.Exec(`INSERT INTO servers
		(name, created_by_role, status, created_at, updated_at) VALUES (?, 'admin', 'offline', 1, 1)`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return id
}

func (f proxyDeleteFixture) createProxy(t *testing.T, serverID int64, port int, name string) proxystore.Proxy {
	t.Helper()
	value, _, err := f.proxies.Create(t.Context(), proxystore.CreateInput{
		ServerID: serverID, Name: name, ListenPort: port, EntryHostMode: proxystore.EntryHostAuto,
		Enabled: true, Security: proxystore.SecurityReality, ServerName: "www.example.com",
		RealityTarget: "www.example.com:443", FirstClientName: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (f proxyDeleteFixture) deleteProxy(t *testing.T, proxyID int64) *httptest.ResponseRecorder {
	t.Helper()
	return performRequest(t, f.handler, http.MethodDelete, "/api/proxies/"+strconv.FormatInt(proxyID, 10), nil, f.cookie)
}

func assertDatabaseCount(t *testing.T, db *sql.DB, query string, want int, arguments ...any) {
	t.Helper()
	var got int
	if err := db.QueryRow(query, arguments...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("database count = %d, want %d for %s", got, want, query)
	}
}

func boolPointer(value bool) *bool { return &value }
