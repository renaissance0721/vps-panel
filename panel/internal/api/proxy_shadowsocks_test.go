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
		created.Proxy.Config.Network != "tcp,udp" || len(created.Proxy.Clients) != 1 {
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
