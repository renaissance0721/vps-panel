package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
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

func boolPointer(value bool) *bool { return &value }
