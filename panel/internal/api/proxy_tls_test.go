package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestACMETLSProxyAPIKeepsCertificateMaterialOnAgent(t *testing.T) {
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
	serverCreation := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "ACME Server"}, cookie)
	var createdServer createdServerResponse
	if err := json.Unmarshal(serverCreation.Body.Bytes(), &createdServer); err != nil {
		t.Fatal(err)
	}
	registration := performRequest(t, handler, http.MethodPost, "/api/agent/register", agentRegistrationRequest{
		EnrollmentToken: createdServer.EnrollmentToken, AgentVersion: "test", ExistingConfig: false,
	}, nil)
	var registered agentRegistrationResponse
	if registration.Code != http.StatusCreated || json.Unmarshal(registration.Body.Bytes(), &registered) != nil {
		t.Fatalf("register Agent = %d, %s", registration.Code, registration.Body.String())
	}
	creation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: createdServer.Server.ID, Name: "ACME", ListenPort: 443, Security: "tls", TLSMode: "acme",
		ServerName: "jp.example.com", FirstClientName: "default",
	}, cookie)
	if creation.Code != http.StatusCreated || !strings.Contains(creation.Body.String(), `"tls_mode":"acme"`) {
		t.Fatalf("create ACME TLS proxy = %d, %s", creation.Code, creation.Body.String())
	}
	var stored string
	if err := db.QueryRow(`SELECT config_json FROM proxies WHERE name = 'ACME'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "private_key") || strings.Contains(stored, "certificate") {
		t.Fatalf("ACME proxy stored certificate material: %s", stored)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/agent/config", nil)
	request.Header.Set("Authorization", "Bearer "+registered.AgentToken)
	desired := httptest.NewRecorder()
	handler.ServeHTTP(desired, request)
	if desired.Code != http.StatusOK || !strings.Contains(desired.Body.String(), `"mode":"acme"`) ||
		strings.Contains(desired.Body.String(), `"certificate"`) || strings.Contains(desired.Body.String(), `"private_key"`) {
		t.Fatalf("ACME desired state exposed certificate material: %d, %s", desired.Code, desired.Body.String())
	}
	invalid := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: createdServer.Server.ID, Name: "bad", ListenPort: 8443, Security: "tls", TLSMode: "acme",
		ServerName: "1.2.3.4", FirstClientName: "default",
	}, cookie)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "公网域名") {
		t.Fatalf("invalid ACME domain = %d, %s", invalid.Code, invalid.Body.String())
	}
}
