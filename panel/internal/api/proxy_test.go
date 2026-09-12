package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if len(created.Proxy.Clients) != 1 || created.Proxy.Config.RealityPublicKey == "" ||
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
	list := performRequest(t, handler, http.MethodGet, "/api/proxies", nil, cookie)
	detail := performRequest(t, handler, http.MethodGet, "/api/proxies/"+strconv.FormatInt(created.Proxy.ID, 10), nil, cookie)
	if strings.Contains(list.Body.String(), privateKey) || strings.Contains(detail.Body.String(), privateKey) {
		t.Fatal("ordinary proxy API leaked REALITY private key")
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
	client := performRequest(t, handler, http.MethodGet, "/api/clients/"+strconv.FormatInt(clientID, 10), nil, cookie)
	if client.Code != http.StatusOK || !strings.Contains(client.Body.String(), `"uuid"`) {
		t.Fatalf("client detail = %d, %s", client.Code, client.Body.String())
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
		{"tls", createProxyRequest{ServerID: createdServer.Server.ID, Name: "bad", ListenPort: 443, Security: "tls", ServerName: "example.com"}, http.StatusBadRequest, "TLS 证书"},
	} {
		response := performRequest(t, handler, http.MethodPost, "/api/proxies", test.body, cookie)
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.message) {
			t.Fatalf("%s response = %d, %s", test.name, response.Code, response.Body.String())
		}
	}
}
