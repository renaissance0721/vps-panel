package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestAgentConfigAPIAuthenticationAndInitialState(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	firstServer, err := service.Create(t.Context(), "Config API One")
	if err != nil {
		t.Fatalf("create first Server: %v", err)
	}
	firstAgent, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), firstServer.EnrollmentToken, "v0.10.0", false)
	if err != nil {
		t.Fatalf("register first Agent: %v", err)
	}
	secondServer, err := service.Create(t.Context(), "Config API Two")
	if err != nil {
		t.Fatalf("create second Server: %v", err)
	}
	if _, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), secondServer.EnrollmentToken, "v0.10.0", false); err != nil {
		t.Fatalf("register second Agent: %v", err)
	}
	if _, err := db.Exec(`UPDATE servers SET desired_state_version = 3 WHERE id = ?`, secondServer.ID); err != nil {
		t.Fatalf("set second desired state version: %v", err)
	}
	handler := NewHandler(db, t.TempDir())

	for name, tokenValue := range map[string]string{"missing": "", "invalid": "wrong-token"} {
		t.Run(name, func(t *testing.T) {
			response := performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, tokenValue)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("GET config status = %d, want 401", response.Code)
			}
		})
	}
	response := performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, firstAgent.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("GET config status = %d, body = %q", response.Code, response.Body.String())
	}
	var state agentDesiredStateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode desired state: %v", err)
	}
	if state.Version != 1 || state.Xray.Enabled || state.Xray.Proxies == nil || len(state.Xray.Proxies) != 0 ||
		state.Realm.Enabled || state.Realm.Relays == nil || len(state.Realm.Relays) != 0 {
		t.Fatalf("initial desired state = %+v", state)
	}
}

func TestAgentConfigIncludesOnlyEnabledTypedRelays(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Realm Source")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v0.11.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO relays
		(server_id, name, listen_address, listen_port, target_type, target_host, target_port, network, enabled, created_at, updated_at)
		VALUES (?, 'Enabled', '0.0.0.0', 9502, 'manual', 'relay.example.com', 443, 'tcp,udp', 1, 1, 1),
		       (?, 'Disabled', '0.0.0.0', 9503, 'manual', 'disabled.example.com', 443, 'tcp', 0, 2, 2)`,
		created.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	response := performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, registered.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("GET config = %d, %s", response.Code, response.Body.String())
	}
	var state agentDesiredStateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Realm.Enabled || len(state.Realm.Relays) != 1 {
		t.Fatalf("Realm desired state = %+v", state.Realm)
	}
	relay := state.Realm.Relays[0]
	if relay.ListenAddress != "0.0.0.0" || relay.ListenPort != 9502 || relay.TargetHost != "relay.example.com" ||
		relay.TargetPort != 443 || relay.Network != "tcp,udp" {
		t.Fatalf("typed Relay = %+v", relay)
	}
}

func TestAgentConfigResultAPIValidationAndPersistence(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Config Result")
	if err != nil {
		t.Fatalf("create Server: %v", err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v0.10.0", false)
	if err != nil {
		t.Fatalf("register Agent: %v", err)
	}
	handler := NewHandler(db, t.TempDir())

	unauthorized := performAgentRequest(t, handler, http.MethodPost, "/api/agent/config/result", map[string]any{
		"version": 1, "status": "success", "message": "",
	}, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized result status = %d, want 401", unauthorized.Code)
	}
	failed := performAgentRequest(t, handler, http.MethodPost, "/api/agent/config/result", map[string]any{
		"version": 1, "status": "failed", "message": "safe apply error",
	}, registered.Token)
	if failed.Code != http.StatusNoContent {
		t.Fatalf("failed result status = %d, body = %q", failed.Code, failed.Body.String())
	}
	var appliedVersion int64
	var status, message string
	if err := db.QueryRow(
		`SELECT applied_config_version, config_sync_status, config_sync_error FROM agents WHERE id = ?`, registered.ID,
	).Scan(&appliedVersion, &status, &message); err != nil {
		t.Fatalf("read failed config result: %v", err)
	}
	if appliedVersion != 0 || status != agentcontrol.ConfigSyncFailed || message != "safe apply error" {
		t.Fatalf("failed config state = (%d, %q, %q)", appliedVersion, status, message)
	}

	success := performAgentRequest(t, handler, http.MethodPost, "/api/agent/config/result", map[string]any{
		"version": 1, "status": "success", "message": "",
	}, registered.Token)
	if success.Code != http.StatusNoContent {
		t.Fatalf("success result status = %d, body = %q", success.Code, success.Body.String())
	}
	if err := db.QueryRow(
		`SELECT applied_config_version, config_sync_status, config_sync_error FROM agents WHERE id = ?`, registered.ID,
	).Scan(&appliedVersion, &status, &message); err != nil {
		t.Fatalf("read successful config result: %v", err)
	}
	if appliedVersion != 1 || status != agentcontrol.ConfigSyncSuccess || message != "" {
		t.Fatalf("successful config state = (%d, %q, %q)", appliedVersion, status, message)
	}

	for name, body := range map[string]map[string]any{
		"future version": {"version": 2, "status": "success", "message": ""},
		"invalid status": {"version": 1, "status": "other", "message": ""},
		"long message":   {"version": 1, "status": "failed", "message": strings.Repeat("x", 4097)},
		"token message":  {"version": 1, "status": "failed", "message": "secret " + registered.Token},
	} {
		t.Run(name, func(t *testing.T) {
			response := performAgentRequest(t, handler, http.MethodPost, "/api/agent/config/result", body, registered.Token)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid result status = %d, body = %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestPanelSendsConfigChangedToCurrentAgentConnection(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Config Notification")
	if err != nil {
		t.Fatalf("create Server: %v", err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v0.10.0", false)
	if err != nil {
		t.Fatalf("register Agent: %v", err)
	}
	if _, err := db.Exec(`UPDATE servers SET desired_state_version = 7 WHERE id = ?`, created.ID); err != nil {
		t.Fatalf("set desired state version: %v", err)
	}
	handler := &server{servers: service, agents: agentcontrol.NewService(db, time.Now)}
	panel := httptest.NewServer(http.HandlerFunc(handler.agentWebSocket))
	defer panel.Close()
	header := http.Header{"Authorization": []string{"Bearer " + registered.Token}}
	connection, response, err := websocket.Dial(t.Context(), panel.URL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("connect Agent WebSocket: %v, response = %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	if err := handler.agents.NotifyConfigChanged(created.ID, 7); err != nil {
		t.Fatalf("notify config changed: %v", err)
	}
	messageType, message, err := connection.Read(t.Context())
	if err != nil {
		t.Fatalf("read config notification: %v", err)
	}
	var notification struct {
		Type    string `json:"type"`
		Version int64  `json:"version"`
	}
	if messageType != websocket.MessageText || json.Unmarshal(message, &notification) != nil ||
		notification.Type != "config_changed" || notification.Version != 7 {
		t.Fatalf("config notification = %q", message)
	}
}
