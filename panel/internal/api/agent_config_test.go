package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	if state.Version != 1 || state.Decommission || state.Xray.Enabled || !state.Xray.Purge || state.Xray.OutboundPreference != serverstore.OutboundAuto || state.Xray.Proxies == nil || len(state.Xray.Proxies) != 0 ||
		state.Realm.Enabled || !state.Realm.Purge || state.Realm.Relays == nil || len(state.Realm.Relays) != 0 {
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
	if !state.Realm.Enabled || state.Realm.Purge || len(state.Realm.Relays) != 1 {
		t.Fatalf("Realm desired state = %+v", state.Realm)
	}
	relay := state.Realm.Relays[0]
	if relay.ListenAddress != "0.0.0.0" || relay.ListenPort != 9502 || relay.TargetHost != "relay.example.com" ||
		relay.TargetPort != 443 || relay.Network != "tcp,udp" {
		t.Fatalf("typed Relay = %+v", relay)
	}
}

func TestAgentConfigPurgeUsesRowExistenceIncludingDisabledRows(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	created, err := serverstore.NewService(db).Create(t.Context(), "Disabled managed rows")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v0.20.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO proxies
		(server_id, name, protocol, listen_port, enabled, config_json, created_at, updated_at)
		VALUES (?, 'Disabled Proxy', 'vless', 443, 0, '{}', 1, 1)`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO relays
		(server_id, name, listen_address, listen_port, target_type, target_host, target_port, network, enabled, created_at, updated_at)
		VALUES (?, 'Disabled Relay', '0.0.0.0', 9502, 'manual', 'example.com', 443, 'tcp', 0, 1, 1)`, created.ID); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	response := performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, agent.Token)
	var state agentDesiredStateResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil {
		t.Fatalf("disabled rows desired state = %d, %s", response.Code, response.Body.String())
	}
	if state.Xray.Enabled || state.Xray.Purge || state.Realm.Enabled || state.Realm.Purge {
		t.Fatalf("disabled rows must disable without purge: %+v", state)
	}
	if _, err := db.Exec(`DELETE FROM proxies WHERE server_id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM relays WHERE server_id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}
	response = performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, agent.Token)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil {
		t.Fatalf("empty desired state = %d, %s", response.Code, response.Body.String())
	}
	if state.Xray.Enabled || !state.Xray.Purge || state.Realm.Enabled || !state.Realm.Purge {
		t.Fatalf("empty rows must request purge: %+v", state)
	}
}

func TestServerDecommissionWaitsForCurrentAgentCleanupResult(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	servers := serverstore.NewService(db)
	created, err := servers.Create(t.Context(), "Decommission")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := agentcontrol.NewService(db, time.Now).RegisterAgentWithMetadata(t.Context(), created.EnrollmentToken, agentcontrol.Metadata{
		Implementation: agentcontrol.OfficialImplementation,
		Version:        "v0.20.0",
		APIVersion:     agentcontrol.CurrentAPIVersion,
		Capabilities: []string{
			agentcontrol.CapabilityManagedRuntimePurge,
			agentcontrol.CapabilitySelfDecommission,
		},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialized.Result().Cookies()[0]
	path := "/api/servers/" + strconv.FormatInt(created.ID, 10)
	response := performRequest(t, handler, http.MethodDelete, path, nil, cookie)
	if response.Code != http.StatusNoContent {
		t.Fatalf("request decommission = %d, %s", response.Code, response.Body.String())
	}

	var desiredVersion int64
	var decommissionStatus string
	var archivedAt sql.NullInt64
	var agentCount int
	if err := db.QueryRow(`SELECT desired_state_version, decommission_status, archived_at FROM servers WHERE id = ?`, created.ID).
		Scan(&desiredVersion, &decommissionStatus, &archivedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM agents WHERE server_id = ?`, created.ID).Scan(&agentCount); err != nil {
		t.Fatal(err)
	}
	if desiredVersion != 2 || decommissionStatus != serverstore.DecommissionPending || archivedAt.Valid || agentCount != 1 {
		t.Fatalf("pending state = version %d status %q archived %v agents %d", desiredVersion, decommissionStatus, archivedAt.Valid, agentCount)
	}

	config := performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, agent.Token)
	var state agentDesiredStateResponse
	if config.Code != http.StatusOK || json.Unmarshal(config.Body.Bytes(), &state) != nil {
		t.Fatalf("decommission desired state = %d, %s", config.Code, config.Body.String())
	}
	if !state.Decommission || state.BlockChinaInbound || state.Xray.Enabled || !state.Xray.Purge || len(state.Xray.Proxies) != 0 ||
		state.Realm.Enabled || !state.Realm.Purge || len(state.Realm.Relays) != 0 {
		t.Fatalf("decommission desired state = %+v", state)
	}

	failed := performAgentRequest(t, handler, http.MethodPost, "/api/agent/config/result", map[string]any{
		"version": desiredVersion, "status": "failed", "message": "managed runtime purge failed",
	}, agent.Token)
	if failed.Code != http.StatusNoContent {
		t.Fatalf("failed decommission result = %d, %s", failed.Code, failed.Body.String())
	}
	if err := db.QueryRow(`SELECT decommission_status, archived_at FROM servers WHERE id = ?`, created.ID).
		Scan(&decommissionStatus, &archivedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM agents WHERE server_id = ?`, created.ID).Scan(&agentCount); err != nil {
		t.Fatal(err)
	}
	if decommissionStatus != serverstore.DecommissionFailed || archivedAt.Valid || agentCount != 1 {
		t.Fatalf("failed state = status %q archived %v agents %d", decommissionStatus, archivedAt.Valid, agentCount)
	}

	stale := performAgentRequest(t, handler, http.MethodPost, "/api/agent/config/result", map[string]any{
		"version": desiredVersion - 1, "status": "success", "message": "",
	}, agent.Token)
	if stale.Code != http.StatusNoContent {
		t.Fatalf("stale success = %d, %s", stale.Code, stale.Body.String())
	}
	if err := db.QueryRow(`SELECT archived_at FROM servers WHERE id = ?`, created.ID).Scan(&archivedAt); err != nil || archivedAt.Valid {
		t.Fatalf("stale success archived server: %v, %v", archivedAt.Valid, err)
	}

	success := performAgentRequest(t, handler, http.MethodPost, "/api/agent/config/result", map[string]any{
		"version": desiredVersion, "status": "success", "message": "",
	}, agent.Token)
	if success.Code != http.StatusNoContent {
		t.Fatalf("successful decommission result = %d, %s", success.Code, success.Body.String())
	}
	if err := db.QueryRow(`SELECT archived_at FROM servers WHERE id = ?`, created.ID).Scan(&archivedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM agents WHERE server_id = ?`, created.ID).Scan(&agentCount); err != nil {
		t.Fatal(err)
	}
	if !archivedAt.Valid || agentCount != 0 {
		t.Fatalf("completed state = archived %v agents %d", archivedAt.Valid, agentCount)
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

func TestServerOutboundPreferencePatchNotifiesOnlineAgent(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Outbound API")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v0.21.0", false)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initialized.Code != http.StatusCreated {
		t.Fatalf("initialize: %d %s", initialized.Code, initialized.Body.String())
	}
	cookie := initialized.Result().Cookies()[0]
	panel := httptest.NewServer(handler)
	defer panel.Close()
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + registered.Token}},
	})
	if err != nil {
		t.Fatalf("connect Agent: %v, response = %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	path := "/api/servers/" + strconv.FormatInt(created.ID, 10)
	for _, payload := range []map[string]any{
		{"name": "changed", "outbound_preference": "prefer_ipv4"},
		{"outbound_preference": "invalid"},
	} {
		response := performRequest(t, handler, http.MethodPatch, path, payload, cookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid PATCH %v = %d %s", payload, response.Code, response.Body.String())
		}
	}
	changed := performRequest(t, handler, http.MethodPatch, path, map[string]string{
		"outbound_preference": "prefer_ipv4",
	}, cookie)
	if changed.Code != http.StatusOK {
		t.Fatalf("preference PATCH = %d %s", changed.Code, changed.Body.String())
	}
	var updated struct {
		Server serverResponse `json:"server"`
	}
	if err := json.Unmarshal(changed.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Server.OutboundPreference != serverstore.OutboundPreferIPv4 {
		t.Fatalf("server response preference = %q", updated.Server.OutboundPreference)
	}
	readContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, message, err := connection.Read(readContext)
	if err != nil {
		t.Fatal(err)
	}
	var notification struct {
		Type    string `json:"type"`
		Version int64  `json:"version"`
	}
	if err := json.Unmarshal(message, &notification); err != nil || notification.Type != "config_changed" || notification.Version != 2 {
		t.Fatalf("config notification = %s, error = %v", message, err)
	}
	stateResponse := performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, registered.Token)
	var state agentDesiredStateResponse
	if stateResponse.Code != http.StatusOK || json.Unmarshal(stateResponse.Body.Bytes(), &state) != nil ||
		state.Version != 2 || state.Xray.OutboundPreference != serverstore.OutboundPreferIPv4 {
		t.Fatalf("desired state = (%d, %s)", stateResponse.Code, stateResponse.Body.String())
	}
}
