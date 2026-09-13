package api

import (
	"net/http"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestAgentClientTrafficRequiresAuthenticationAndOwnsClients(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	servers := serverstore.NewService(db)
	firstServer, err := servers.Create(t.Context(), "First")
	if err != nil {
		t.Fatal(err)
	}
	firstAgent, err := servers.RegisterAgent(t.Context(), firstServer.EnrollmentToken, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	secondServer, err := servers.Create(t.Context(), "Second")
	if err != nil {
		t.Fatal(err)
	}
	secondAgent, err := servers.RegisterAgent(t.Context(), secondServer.EnrollmentToken, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	proxies := proxystore.NewService(db)
	firstProxy := createAgentTrafficProxy(t, proxies, firstServer.ID, 443)
	secondProxy := createAgentTrafficProxy(t, proxies, secondServer.ID, 443)
	handler := NewHandler(db, t.TempDir())

	body := map[string]any{"clients": []map[string]any{{
		"client_id": firstProxy.Clients[0].ID, "uplink_bytes": 10, "downlink_bytes": 20,
	}}}
	if response := performAgentRequest(t, handler, http.MethodPost, "/api/agent/traffic", body, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated report status = %d", response.Code)
	}
	if response := performAgentRequest(t, handler, http.MethodPost, "/api/agent/traffic", body, firstAgent.Token); response.Code != http.StatusNoContent {
		t.Fatalf("authenticated report status = %d, body = %q", response.Code, response.Body.String())
	}
	client, err := proxies.GetClient(t.Context(), firstProxy.Clients[0].ID)
	if err != nil || client.Metrics == nil || client.Metrics.CycleUplinkBytes != 0 || client.Metrics.CycleDownlinkBytes != 0 {
		t.Fatalf("first client metrics = (%+v, %v)", client.Metrics, err)
	}
	body = map[string]any{"clients": []map[string]any{{
		"client_id": firstProxy.Clients[0].ID, "uplink_bytes": 15, "downlink_bytes": 27,
	}}}
	if response := performAgentRequest(t, handler, http.MethodPost, "/api/agent/traffic", body, firstAgent.Token); response.Code != http.StatusNoContent {
		t.Fatalf("second report status = %d, body = %q", response.Code, response.Body.String())
	}
	client, err = proxies.GetClient(t.Context(), firstProxy.Clients[0].ID)
	if err != nil || client.Metrics == nil || client.Metrics.CycleUplinkBytes != 5 || client.Metrics.CycleDownlinkBytes != 7 {
		t.Fatalf("accumulated client metrics = (%+v, %v)", client.Metrics, err)
	}

	crossServer := map[string]any{"clients": []map[string]any{{
		"client_id": firstProxy.Clients[0].ID, "uplink_bytes": 30, "downlink_bytes": 40,
	}}}
	if response := performAgentRequest(t, handler, http.MethodPost, "/api/agent/traffic", crossServer, secondAgent.Token); response.Code != http.StatusBadRequest {
		t.Fatalf("cross-server report status = %d, body = %q", response.Code, response.Body.String())
	}
	if client, err := proxies.GetClient(t.Context(), secondProxy.Clients[0].ID); err != nil || client.Metrics != nil {
		t.Fatalf("unreported second client metrics = (%+v, %v)", client.Metrics, err)
	}
}

func TestAgentClientTrafficValidatesPayload(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	servers := serverstore.NewService(db)
	created, err := servers.Create(t.Context(), "Validation")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := servers.RegisterAgent(t.Context(), created.EnrollmentToken, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	proxies := proxystore.NewService(db)
	proxyValue := createAgentTrafficProxy(t, proxies, created.ID, 443)
	handler := NewHandler(db, t.TempDir())

	for name, body := range map[string]any{
		"negative": map[string]any{"clients": []map[string]any{{
			"client_id": proxyValue.Clients[0].ID, "uplink_bytes": -1, "downlink_bytes": 0,
		}}},
		"server bypass": map[string]any{
			"server_id": created.ID,
			"clients":   []map[string]any{{"client_id": proxyValue.Clients[0].ID, "uplink_bytes": 1, "downlink_bytes": 2}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := performAgentRequest(t, handler, http.MethodPost, "/api/agent/traffic", body, agent.Token)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid report status = %d, body = %q", response.Code, response.Body.String())
			}
		})
	}
}

func createAgentTrafficProxy(t *testing.T, service *proxystore.Service, serverID int64, port int) proxystore.Proxy {
	t.Helper()
	value, _, err := service.Create(t.Context(), proxystore.CreateInput{
		ServerID: serverID, Name: "Traffic", ListenPort: port, EntryHostMode: proxystore.EntryHostAuto,
		Enabled: true, Security: proxystore.SecurityReality, ServerName: "www.example.com",
		RealityTarget: "www.example.com:443", FirstClientName: "Client",
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
