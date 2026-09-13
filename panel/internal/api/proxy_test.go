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
	client := performRequest(t, handler, http.MethodGet, "/api/clients/"+strconv.FormatInt(clientID, 10), nil, cookie)
	if client.Code != http.StatusOK || !strings.Contains(client.Body.String(), `"uuid"`) {
		t.Fatalf("client detail = %d, %s", client.Code, client.Body.String())
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
	if err != nil || parsedURI.Query().Get("pbk") != publicKey ||
		strings.Contains(shareResponse.Body.String(), `"reality_public_key"`) ||
		strings.Contains(shareResponse.Body.String(), `"reality_short_id"`) ||
		strings.Contains(shareResponse.Body.String(), `"private_key"`) {
		t.Fatalf("client share did not keep key material confined to URI: %s", shareResponse.Body.String())
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

func TestClientTrafficConfigurationAndManualResetAPI(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialization.Result().Cookies()[0]
	serverCreation := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "Traffic Server"}, cookie)
	var createdServer createdServerResponse
	if json.Unmarshal(serverCreation.Body.Bytes(), &createdServer) != nil {
		t.Fatal("decode created server")
	}
	proxyCreation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: createdServer.Server.ID, Name: "Traffic", ListenPort: 443, Security: "reality",
		ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Initial",
	}, cookie)
	var created struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if proxyCreation.Code != http.StatusCreated || json.Unmarshal(proxyCreation.Body.Bytes(), &created) != nil {
		t.Fatalf("create proxy = %d, %s", proxyCreation.Code, proxyCreation.Body.String())
	}
	proxyID := strconv.FormatInt(created.Proxy.ID, 10)

	createClient := func(name string, limit any, unit, mode string, weekday, day int, resetTime string) *httptest.ResponseRecorder {
		return performRequest(t, handler, http.MethodPost, "/api/proxies/"+proxyID+"/clients", map[string]any{
			"name": name, "enabled": true, "traffic_limit": limit, "limit_unit": unit,
			"traffic_reset_mode": mode, "traffic_reset_weekday": weekday,
			"traffic_reset_day": day, "traffic_reset_time": resetTime,
		}, cookie)
	}
	numberResponse := createClient("Number", 100, "G", "daily", 1, 1, "03:30")
	var numberClient struct {
		Client clientResponse `json:"client"`
	}
	if numberResponse.Code != http.StatusCreated || json.Unmarshal(numberResponse.Body.Bytes(), &numberClient) != nil ||
		numberClient.Client.TrafficLimitBytes == nil || *numberClient.Client.TrafficLimitBytes != int64(100)<<30 ||
		numberClient.Client.TrafficResetMode != "daily" || numberClient.Client.NextResetAt == nil {
		t.Fatalf("number traffic limit response = %d, %s", numberResponse.Code, numberResponse.Body.String())
	}
	stringResponse := createClient("String", "1.5", "T", "weekly", 7, 1, "00:00")
	var stringClient struct {
		Client clientResponse `json:"client"`
	}
	if stringResponse.Code != http.StatusCreated || json.Unmarshal(stringResponse.Body.Bytes(), &stringClient) != nil ||
		stringClient.Client.TrafficLimitBytes == nil || *stringClient.Client.TrafficLimitBytes != int64(3)<<39 ||
		stringClient.Client.TrafficResetMode != "weekly" || stringClient.Client.TrafficResetWeekday != 7 {
		t.Fatalf("string traffic limit response = %d, %s", stringResponse.Code, stringResponse.Body.String())
	}
	unlimitedResponse := createClient("Unlimited", "", "G", "never", 1, 1, "00:00")
	var unlimitedClient struct {
		Client clientResponse `json:"client"`
	}
	if unlimitedResponse.Code != http.StatusCreated || json.Unmarshal(unlimitedResponse.Body.Bytes(), &unlimitedClient) != nil ||
		unlimitedClient.Client.TrafficLimitBytes != nil || unlimitedClient.Client.NextResetAt != nil {
		t.Fatalf("unlimited response = %d, %s", unlimitedResponse.Code, unlimitedResponse.Body.String())
	}
	for _, invalid := range []map[string]any{
		{"name": "Bad unit", "traffic_limit": 1, "limit_unit": "M"},
		{"name": "Bad amount", "traffic_limit": "1e2", "limit_unit": "G"},
		{"name": "Bad day", "traffic_reset_mode": "monthly", "traffic_reset_day": 32, "traffic_reset_time": "00:00"},
		{"name": "Bad time", "traffic_reset_mode": "daily", "traffic_reset_time": "24:00"},
	} {
		response := performRequest(t, handler, http.MethodPost, "/api/proxies/"+proxyID+"/clients", invalid, cookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid traffic config = %d, %s", response.Code, response.Body.String())
		}
	}

	clientID := numberClient.Client.ID
	if _, err := db.Exec(`INSERT INTO client_metrics
		(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes,
		 cycle_started_at, last_activity_at, updated_at) VALUES (?, 100, 200, 30, 40, 1, 2, 3)`, clientID); err != nil {
		t.Fatal(err)
	}
	reset := performRequest(t, handler, http.MethodPost, "/api/clients/"+strconv.FormatInt(clientID, 10)+"/traffic/reset", nil, cookie)
	if reset.Code != http.StatusOK {
		t.Fatalf("manual reset = %d, %s", reset.Code, reset.Body.String())
	}
	var uplink, downlink, cycleUplink, cycleDownlink int64
	if err := db.QueryRow(`SELECT xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes
		FROM client_metrics WHERE client_id = ?`, clientID).Scan(&uplink, &downlink, &cycleUplink, &cycleDownlink); err != nil {
		t.Fatal(err)
	}
	if uplink != 100 || downlink != 200 || cycleUplink != 0 || cycleDownlink != 0 {
		t.Fatalf("metrics after manual reset = %d/%d/%d/%d", uplink, downlink, cycleUplink, cycleDownlink)
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

func TestShadowsocksProxyAPIKeepsSecretsOutOfOrdinaryResponses(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{"username": "admin", "password": "strong-password"}, nil)
	cookie := initialization.Result().Cookies()[0]
	serverCreation := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "SS Server"}, cookie)
	var createdServer createdServerResponse
	if err := json.Unmarshal(serverCreation.Body.Bytes(), &createdServer); err != nil {
		t.Fatal(err)
	}
	creation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: createdServer.Server.ID, Name: "SS 256", Protocol: "shadowsocks",
		Method: "2022-blake3-aes-256-gcm", ListenPort: 8388, EntryHostMode: "manual",
		EntryHost: "ss.example.com", FirstClientName: "Phone",
	}, cookie)
	if creation.Code != http.StatusCreated {
		t.Fatalf("create Shadowsocks proxy = %d, %s", creation.Code, creation.Body.String())
	}
	var created struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if err := json.Unmarshal(creation.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Proxy.Protocol != "shadowsocks" || created.Proxy.Config.Method != "2022-blake3-aes-256-gcm" ||
		created.Proxy.Config.Network != "tcp,udp" || len(created.Proxy.Clients) != 1 || created.Proxy.Clients[0].UUIDSummary != "" {
		t.Fatalf("created Shadowsocks response = %+v", created.Proxy)
	}
	var configJSON, credentialJSON string
	if err := db.QueryRow(`SELECT config_json FROM proxies WHERE id = ?`, created.Proxy.ID).Scan(&configJSON); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT credential_json FROM clients WHERE proxy_id = ?`, created.Proxy.ID).Scan(&credentialJSON); err != nil {
		t.Fatal(err)
	}
	var storedConfig struct {
		Shadowsocks struct {
			Password string `json:"password"`
		} `json:"shadowsocks"`
	}
	var storedCredential struct {
		Password string `json:"password"`
	}
	if json.Unmarshal([]byte(configJSON), &storedConfig) != nil || json.Unmarshal([]byte(credentialJSON), &storedCredential) != nil ||
		storedConfig.Shadowsocks.Password == "" || storedCredential.Password == "" {
		t.Fatal("Shadowsocks secrets were not stored")
	}
	for _, response := range []*httptest.ResponseRecorder{
		creation,
		performRequest(t, handler, http.MethodGet, "/api/proxies", nil, cookie),
		performRequest(t, handler, http.MethodGet, "/api/proxies/"+strconv.FormatInt(created.Proxy.ID, 10), nil, cookie),
		performRequest(t, handler, http.MethodGet, "/api/clients/"+strconv.FormatInt(created.Proxy.Clients[0].ID, 10), nil, cookie),
	} {
		if strings.Contains(response.Body.String(), storedConfig.Shadowsocks.Password) ||
			strings.Contains(response.Body.String(), storedCredential.Password) || strings.Contains(response.Body.String(), "credential_json") {
			t.Fatalf("ordinary API leaked Shadowsocks secret: %s", response.Body.String())
		}
	}
	client := performRequest(t, handler, http.MethodPost, "/api/proxies/"+strconv.FormatInt(created.Proxy.ID, 10)+"/clients", createClientRequest{Name: "Laptop", Enabled: boolPointer(true)}, cookie)
	if client.Code != http.StatusCreated || strings.Contains(client.Body.String(), `"password"`) || strings.Contains(client.Body.String(), `"uuid"`) {
		t.Fatalf("create Shadowsocks client response = %d, %s", client.Code, client.Body.String())
	}
	invalidUDP := performRequest(t, handler, http.MethodPost, "/api/proxies/"+strconv.FormatInt(created.Proxy.ID, 10)+"/clients", createClientRequest{Name: "Bad", ClientUDP443: true}, cookie)
	if invalidUDP.Code != http.StatusBadRequest || !strings.Contains(invalidUDP.Body.String(), "UDP 443") {
		t.Fatalf("Shadowsocks UDP443 response = %d, %s", invalidUDP.Code, invalidUDP.Body.String())
	}
	method := "2022-blake3-aes-128-gcm"
	methodUpdate := performRequest(t, handler, http.MethodPatch, "/api/proxies/"+strconv.FormatInt(created.Proxy.ID, 10), updateProxyRequest{Method: &method}, cookie)
	if methodUpdate.Code != http.StatusConflict {
		t.Fatalf("Shadowsocks method update = %d, %s", methodUpdate.Code, methodUpdate.Body.String())
	}
}

func boolPointer(value bool) *bool { return &value }
