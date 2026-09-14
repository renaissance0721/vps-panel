package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestAgentRegistrationAPI(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	created, err := serverstore.NewService(db).Create(t.Context(), "JP Agent 01")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	handler := NewHandler(db, t.TempDir())
	bypass := performRequest(t, handler, http.MethodPost, "/api/agent/register", map[string]any{
		"enrollment_token": created.EnrollmentToken,
		"agent_version":    "v0.4.0",
		"server_id":        created.ID + 1,
	}, nil)
	if bypass.Code != http.StatusBadRequest {
		t.Fatalf("server ID bypass status = %d, want %d", bypass.Code, http.StatusBadRequest)
	}
	existingConfig := performRequest(t, handler, http.MethodPost, "/api/agent/register", map[string]any{
		"enrollment_token": created.EnrollmentToken,
		"agent_version":    "v0.4.0",
		"existing_config":  true,
	}, nil)
	if existingConfig.Code != http.StatusConflict {
		t.Fatalf("initial enrollment with existing config status = %d, body = %q", existingConfig.Code, existingConfig.Body.String())
	}
	var rejectedUsedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT used_at FROM agent_enrollments WHERE server_id = ?`, created.ID,
	).Scan(&rejectedUsedAt); err != nil {
		t.Fatalf("read rejected enrollment: %v", err)
	}
	if rejectedUsedAt.Valid {
		t.Fatal("rejected initial enrollment was consumed")
	}
	response := performRequest(t, handler, http.MethodPost, "/api/agent/register", map[string]string{
		"enrollment_token": created.EnrollmentToken,
		"agent_version":    "v0.4.0",
	}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("registration status = %d, body = %q", response.Code, response.Body.String())
	}
	var registered agentRegistrationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &registered); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if registered.AgentID <= 0 || registered.ServerID != created.ID || registered.AgentToken == "" {
		t.Fatalf("registration response = %+v", registered)
	}

	var storedHash, status string
	var usedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT agents.token_hash, enrollments.used_at, servers.status
		 FROM agents
		 JOIN agent_enrollments AS enrollments ON enrollments.server_id = agents.server_id
		 JOIN servers ON servers.id = agents.server_id
		 WHERE agents.id = ?`, registered.AgentID,
	).Scan(&storedHash, &usedAt, &status); err != nil {
		t.Fatalf("read registered state: %v", err)
	}
	if storedHash == registered.AgentToken || storedHash != token.Hash(registered.AgentToken) {
		t.Fatal("Agent API stored the long-term token in plaintext")
	}
	if !usedAt.Valid || status != serverstore.StatusOffline {
		t.Fatalf("registered state = (used %v, status %q), want used and offline", usedAt.Valid, status)
	}

	reused := performRequest(t, handler, http.MethodPost, "/api/agent/register", map[string]string{
		"enrollment_token": created.EnrollmentToken,
		"agent_version":    "v0.4.0",
	}, nil)
	if reused.Code != http.StatusUnauthorized {
		t.Fatalf("reused registration status = %d, want %d", reused.Code, http.StatusUnauthorized)
	}
}

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
	firstAgent, err := service.RegisterAgent(t.Context(), firstServer.EnrollmentToken, "v0.10.0", false)
	if err != nil {
		t.Fatalf("register first Agent: %v", err)
	}
	secondServer, err := service.Create(t.Context(), "Config API Two")
	if err != nil {
		t.Fatalf("create second Server: %v", err)
	}
	if _, err := service.RegisterAgent(t.Context(), secondServer.EnrollmentToken, "v0.10.0", false); err != nil {
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
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.11.0", false)
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
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.10.0", false)
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
	if appliedVersion != 0 || status != serverstore.ConfigSyncFailed || message != "safe apply error" {
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
	if appliedVersion != 1 || status != serverstore.ConfigSyncSuccess || message != "" {
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
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.10.0", false)
	if err != nil {
		t.Fatalf("register Agent: %v", err)
	}
	if _, err := db.Exec(`UPDATE servers SET desired_state_version = 7 WHERE id = ?`, created.ID); err != nil {
		t.Fatalf("set desired state version: %v", err)
	}
	handler := &server{servers: service, connections: make(map[int64]*agentConnection)}
	panel := httptest.NewServer(http.HandlerFunc(handler.agentWebSocket))
	defer panel.Close()
	header := http.Header{"Authorization": []string{"Bearer " + registered.Token}}
	connection, response, err := websocket.Dial(t.Context(), panel.URL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("connect Agent WebSocket: %v, response = %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	if err := handler.notifyConfigChanged(created.ID, 7); err != nil {
		t.Fatalf("notify config changed: %v", err)
	}
	messageType, message, err := connection.Read(t.Context())
	if err != nil {
		t.Fatalf("read config notification: %v", err)
	}
	var notification agentConfigChangedMessage
	if messageType != websocket.MessageText || json.Unmarshal(message, &notification) != nil ||
		notification.Type != "config_changed" || notification.Version != 7 {
		t.Fatalf("config notification = %q", message)
	}
}

func TestInstallAgentScript(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	response := httptest.NewRecorder()
	NewHandler(db, t.TempDir()).ServeHTTP(
		response, httptest.NewRequest(http.MethodGet, "/install-agent.sh", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("installer status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/x-shellscript") {
		t.Fatalf("installer content type = %q", contentType)
	}
	for _, required := range []string{
		"--server",
		"--token",
		"--version",
		"vps-panel-agent-linux-${architecture}",
		"${RELEASES_BASE}/download/${agent_version}",
		"${RELEASES_BASE}/latest/download",
		"/opt/vps-panel/agent",
		"/etc/systemd/system/vps-panel-agent.service",
		"Restart=on-failure",
		"RestartSec=3",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ReadWritePaths=/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /etc/systemd/system",
		"systemctl enable",
		"systemctl restart",
	} {
		if !strings.Contains(response.Body.String(), required) {
			t.Fatalf("installer does not contain %q", required)
		}
	}
	for _, removed := range []string{"--force", "already registered", "CONFIG_FILE"} {
		if strings.Contains(response.Body.String(), removed) {
			t.Fatalf("installer still contains removed behavior %q", removed)
		}
	}
	if strings.Contains(response.Body.String(), "Restart=always") {
		t.Fatal("installer restarts a deliberately stopped Agent")
	}
	registrationIndex := strings.Index(response.Body.String(), `"$download_path" register --server "$server_url" --token "$enrollment_token"`)
	installIndex := strings.Index(response.Body.String(), `install -m 0755 "$download_path" "$BINARY_PATH"`)
	if registrationIndex < 0 || installIndex < 0 || registrationIndex >= installIndex {
		t.Fatal("installer must complete registration before replacing the installed Agent binary")
	}
}

func TestAgentWebSocketAuthenticationAndStatus(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "WebSocket Agent")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.5.0", false)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	handler := NewHandler(db, t.TempDir())
	initializeResponse := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initializeResponse.Code != http.StatusCreated {
		t.Fatalf("initialize status = %d, body = %q", initializeResponse.Code, initializeResponse.Body.String())
	}
	adminCookie := initializeResponse.Result().Cookies()[0]
	panel := httptest.NewServer(handler)
	defer panel.Close()

	invalidHeader := http.Header{}
	invalidHeader.Set("Authorization", "Bearer invalid-token")
	invalidConnection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{
		HTTPHeader: invalidHeader,
	})
	if err == nil {
		invalidConnection.CloseNow()
		t.Fatal("invalid Agent Token opened a WebSocket")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid Agent Token response = %+v, want 401", response)
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOffline)

	header := http.Header{}
	header.Set("Authorization", "Bearer "+registered.Token)
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		t.Fatalf("connect Agent WebSocket: %v, response = %+v", err, response)
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	connectedServer, err := service.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("get connected server: %v", err)
	}
	if connectedServer.LastSeenAt == nil {
		t.Fatal("WebSocket connection did not set last_seen_at")
	}
	if err := connection.Write(t.Context(), websocket.MessageText, []byte(`{
		"type":"system_info",
		"hostname":"jp-01",
		"os_name":"Debian GNU/Linux",
		"os_version":"12",
		"kernel":"6.1.0-amd64",
		"arch":"amd64",
		"ipv4":["203.0.113.10"],
		"ipv6":["2001:db8::10"],
		"public_ipv4":"198.51.100.20",
		"agent_version":"forged-version"
	}`)); err != nil {
		t.Fatalf("write Agent system information: %v", err)
	}
	waitForSystemInfo(t, service, created.ID, "jp-01")
	serverWithInfo, err := service.Get(t.Context(), created.ID)
	if err != nil || serverWithInfo.SystemInfo == nil || serverWithInfo.SystemInfo.AgentVersion != "v0.5.0" {
		t.Fatalf("stored system information = (%+v, %v)", serverWithInfo.SystemInfo, err)
	}
	if err := connection.Write(t.Context(), websocket.MessageText, []byte(`{
		"type":"metrics",
		"cpu_percent":32.4,
		"memory_used_bytes":134217728,
		"memory_total_bytes":536870912,
		"disk_used_bytes":5368709120,
		"disk_total_bytes":10737418240,
		"uptime_seconds":86400,
		"nic_rx_bytes":1000,
		"nic_tx_bytes":2000
	}`)); err != nil {
		t.Fatalf("write Agent metrics: %v", err)
	}
	waitForMetrics(t, service, created.ID, 32.4)
	if err := connection.Write(t.Context(), websocket.MessageText, []byte(`{
		"type":"metrics",
		"cpu_percent":33.4,
		"memory_used_bytes":134217728,
		"memory_total_bytes":536870912,
		"disk_used_bytes":5368709120,
		"disk_total_bytes":10737418240,
		"uptime_seconds":86405,
		"nic_rx_bytes":1300,
		"nic_tx_bytes":2600
	}`)); err != nil {
		t.Fatalf("write second Agent metrics: %v", err)
	}
	waitForMetrics(t, service, created.ID, 33.4)
	listResponse := performRequest(t, handler, http.MethodGet, "/api/servers", nil, adminCookie)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list server status = %d, body = %q", listResponse.Code, listResponse.Body.String())
	}
	var listed struct {
		Servers []serverResponse `json:"servers"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode server list: %v", err)
	}
	if len(listed.Servers) != 1 || listed.Servers[0].SystemInfo == nil ||
		strings.Join(listed.Servers[0].SystemInfo.IPv4, ",") != "203.0.113.10" ||
		strings.Join(listed.Servers[0].SystemInfo.IPv6, ",") != "2001:db8::10" ||
		listed.Servers[0].SystemInfo.PublicIPv4 != "198.51.100.20" ||
		listed.Servers[0].Metrics == nil || listed.Servers[0].Metrics.CPUPercent != 33.4 ||
		listed.Servers[0].Metrics.MemoryUsedBytes != 134217728 ||
		listed.Servers[0].Metrics.MemoryTotalBytes != 536870912 ||
		listed.Servers[0].Metrics.DiskUsedBytes != 5368709120 ||
		listed.Servers[0].Metrics.DiskTotalBytes != 10737418240 ||
		listed.Servers[0].Metrics.UptimeSeconds != 86405 ||
		listed.Servers[0].Metrics.NICRXBytes != 1300 || listed.Servers[0].Metrics.NICTXBytes != 2600 ||
		listed.Servers[0].Metrics.CycleRXBytes != 300 || listed.Servers[0].Metrics.CycleTXBytes != 600 ||
		listed.Servers[0].Metrics.CycleStartedAt == nil || listed.Servers[0].TrafficUsedBytes != 600 ||
		listed.Servers[0].Metrics.UpdatedAt.IsZero() {
		t.Fatalf("server API system information = %+v", listed.Servers)
	}
	if _, err := db.Exec(`UPDATE agents SET last_seen_at = NULL WHERE id = ?`, registered.ID); err != nil {
		t.Fatalf("clear last_seen_at before heartbeat: %v", err)
	}
	if err := connection.Write(t.Context(), websocket.MessageText, []byte(`{"type":"heartbeat"}`)); err != nil {
		t.Fatalf("write Agent heartbeat: %v", err)
	}
	waitForLastSeen(t, service, created.ID)
	var heartbeatTableCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name IN ('agent_heartbeats', 'heartbeat_history', 'agent_events')`,
	).Scan(&heartbeatTableCount); err != nil {
		t.Fatalf("inspect heartbeat history tables: %v", err)
	}
	if heartbeatTableCount != 0 {
		t.Fatalf("heartbeat history table count = %d, want 0", heartbeatTableCount)
	}

	if err := connection.Close(websocket.StatusNormalClosure, "test complete"); err != nil {
		t.Fatalf("close Agent WebSocket: %v", err)
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOffline)
}

func TestNewAgentConnectionReplacesOldConnectionWithoutFalseOffline(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Replacement Agent")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.6.0", false)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	panel := httptest.NewServer(NewHandler(db, t.TempDir()))
	defer panel.Close()
	header := http.Header{"Authorization": []string{"Bearer " + registered.Token}}

	first, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("connect first Agent WebSocket: %v, response = %+v", err, response)
	}
	defer first.CloseNow()
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	if err := first.Write(t.Context(), websocket.MessageText, []byte(`{
		"type":"system_info","hostname":"old-connection","arch":"amd64","ipv4":[],"ipv6":[]
	}`)); err != nil {
		t.Fatalf("write first system information: %v", err)
	}
	waitForSystemInfo(t, service, created.ID, "old-connection")
	firstDisconnected := first.CloseRead(context.Background())

	second, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("connect replacement Agent WebSocket: %v, response = %+v", err, response)
	}
	defer second.CloseNow()
	select {
	case <-firstDisconnected.Done():
	case <-time.After(time.Second):
		t.Fatal("replacement connection did not close the old WebSocket")
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	if err := second.Write(t.Context(), websocket.MessageText, []byte(`{
		"type":"system_info","hostname":"current-connection","arch":"amd64","ipv4":[],"ipv6":[]
	}`)); err != nil {
		t.Fatalf("write replacement system information: %v", err)
	}
	waitForSystemInfo(t, service, created.ID, "current-connection")
	time.Sleep(30 * time.Millisecond)
	value, err := service.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("get server after old connection closed: %v", err)
	}
	if value.Status != serverstore.StatusOnline {
		t.Fatalf("old connection marked replacement offline: status = %q", value.Status)
	}

	if err := second.Close(websocket.StatusNormalClosure, "test complete"); err != nil {
		t.Fatalf("close replacement WebSocket: %v", err)
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOffline)
}

func TestAgentWebSocketRejectsUnknownMessageType(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Unknown Message")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.7.0", false)
	if err != nil {
		t.Fatalf("register Agent: %v", err)
	}
	panel := httptest.NewServer(NewHandler(db, t.TempDir()))
	defer panel.Close()
	header := http.Header{"Authorization": []string{"Bearer " + registered.Token}}
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("connect Agent WebSocket: %v, response = %+v", err, response)
	}
	disconnected := connection.CloseRead(context.Background())
	if err := connection.Write(t.Context(), websocket.MessageText, []byte(`{"type":"future_message"}`)); err != nil {
		t.Fatalf("write unknown Agent message: %v", err)
	}
	select {
	case <-disconnected.Done():
	case <-time.After(time.Second):
		t.Fatal("unknown Agent message did not close WebSocket")
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOffline)
}

func TestOldConnectionCannotOverwriteSystemInfo(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Connection Race")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.7.0", false)
	if err != nil {
		t.Fatalf("register Agent: %v", err)
	}
	oldConnection := new(websocket.Conn)
	currentConnection := new(websocket.Conn)
	handler := &server{
		servers: service,
		connections: map[int64]*agentConnection{
			created.ID: {socket: currentConnection},
		},
	}
	oldTrackedConnection := &agentConnection{socket: oldConnection}
	currentTrackedConnection := handler.connections[created.ID]
	current, _, err := handler.reportCurrentSystemInfo(created.ID, registered.ID, oldTrackedConnection, serverstore.SystemInfoReport{
		Hostname: "stale-host", IPv4: []string{}, IPv6: []string{},
	})
	if err != nil || current {
		t.Fatalf("old connection report = (current %v, error %v), want ignored", current, err)
	}
	current, _, err = handler.reportCurrentSystemInfo(created.ID, registered.ID, currentTrackedConnection, serverstore.SystemInfoReport{
		Hostname: "current-host", IPv4: []string{}, IPv6: []string{},
	})
	if err != nil || !current {
		t.Fatalf("current connection report = (current %v, error %v)", current, err)
	}
	value, err := service.Get(t.Context(), created.ID)
	if err != nil || value.SystemInfo == nil || value.SystemInfo.Hostname != "current-host" {
		t.Fatalf("stored system information = (%+v, %v)", value.SystemInfo, err)
	}
}

func TestAgentWebSocketRejectsInvalidMetrics(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Invalid Metrics")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.8.0", false)
	if err != nil {
		t.Fatalf("register Agent: %v", err)
	}
	panel := httptest.NewServer(NewHandler(db, t.TempDir()))
	defer panel.Close()
	header := http.Header{"Authorization": []string{"Bearer " + registered.Token}}
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("connect Agent WebSocket: %v, response = %+v", err, response)
	}
	disconnected := connection.CloseRead(context.Background())
	if err := connection.Write(t.Context(), websocket.MessageText, []byte(`{
		"type":"metrics","cpu_percent":101,
		"memory_used_bytes":0,"memory_total_bytes":0,
		"disk_used_bytes":0,"disk_total_bytes":0,"uptime_seconds":0
	}`)); err != nil {
		t.Fatalf("write invalid metrics: %v", err)
	}
	select {
	case <-disconnected.Done():
	case <-time.After(time.Second):
		t.Fatal("invalid metrics did not close WebSocket")
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOffline)
	value, err := service.Get(t.Context(), created.ID)
	if err != nil || value.Metrics != nil {
		t.Fatalf("invalid metrics stored = (%+v, %v)", value.Metrics, err)
	}
}

func TestOldConnectionCannotOverwriteMetrics(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Metrics Connection Race")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.8.0", false)
	if err != nil {
		t.Fatalf("register Agent: %v", err)
	}
	oldConnection := new(websocket.Conn)
	currentConnection := new(websocket.Conn)
	handler := &server{
		servers: service,
		connections: map[int64]*agentConnection{
			created.ID: {socket: currentConnection},
		},
	}
	oldTrackedConnection := &agentConnection{socket: oldConnection}
	currentTrackedConnection := handler.connections[created.ID]
	current, err := handler.reportCurrentMetrics(created.ID, registered.ID, oldTrackedConnection, serverstore.MetricsReport{
		CPUPercent: 99,
	})
	if err != nil || current {
		t.Fatalf("old connection metrics = (current %v, error %v), want ignored", current, err)
	}
	current, err = handler.reportCurrentMetrics(created.ID, registered.ID, currentTrackedConnection, serverstore.MetricsReport{
		CPUPercent: 25,
	})
	if err != nil || !current {
		t.Fatalf("current connection metrics = (current %v, error %v)", current, err)
	}
	value, err := service.Get(t.Context(), created.ID)
	if err != nil || value.Metrics == nil || value.Metrics.CPUPercent != 25 {
		t.Fatalf("stored metrics = (%+v, %v)", value.Metrics, err)
	}
}

func TestAgentInstallationCommandPinsReleaseVersion(t *testing.T) {
	created := serverstore.CreatedServer{
		Server:          serverstore.Server{ID: 1, Name: "Versioned", Status: serverstore.StatusPending},
		EnrollmentToken: "one-time-token",
	}
	versioned := (&server{panelVersion: "v0.6.0"}).toCreatedServerResponse(created, "https://panel.example")
	if !strings.Contains(versioned.AgentInstallationCommand, "--version v0.6.0") {
		t.Fatalf("versioned command = %q", versioned.AgentInstallationCommand)
	}
	for _, version := range []string{"", "dev", "unknown", "v0.6.0;bad"} {
		response := (&server{panelVersion: version}).toCreatedServerResponse(created, "https://panel.example")
		if strings.Contains(response.AgentInstallationCommand, "--version") {
			t.Fatalf("development version %q leaked into command %q", version, response.AgentInstallationCommand)
		}
	}
}

func TestCreatingEnrollmentClosesWebSocketAndKeepsServerPending(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Rotate Online Agent")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.5.3", false)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	handler := NewHandler(db, t.TempDir())
	initializeResponse := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initializeResponse.Code != http.StatusCreated {
		t.Fatalf("initialize status = %d, body = %q", initializeResponse.Code, initializeResponse.Body.String())
	}
	adminCookie := initializeResponse.Result().Cookies()[0]
	panel := httptest.NewServer(handler)
	defer panel.Close()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+registered.Token)
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		t.Fatalf("connect Agent WebSocket: %v, response = %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	disconnected := connection.CloseRead(context.Background())

	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, panel.URL+"/api/servers/"+strconv.FormatInt(created.ID, 10)+"/enrollment", nil,
	)
	if err != nil {
		t.Fatalf("create enrollment request: %v", err)
	}
	request.AddCookie(adminCookie)
	enrollmentResponse, err := panel.Client().Do(request)
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}
	enrollmentResponse.Body.Close()
	if enrollmentResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create enrollment status = %d, want %d", enrollmentResponse.StatusCode, http.StatusCreated)
	}
	select {
	case <-disconnected.Done():
	case <-time.After(time.Second):
		t.Fatal("creating enrollment did not close Agent WebSocket")
	}
	if _, err := service.AuthenticateAgent(t.Context(), registered.Token); !errors.Is(err, serverstore.ErrInvalidAgentToken) {
		t.Fatalf("old Agent Token authentication error = %v, want ErrInvalidAgentToken", err)
	}
	time.Sleep(20 * time.Millisecond)
	var status, purpose string
	if err := db.QueryRow(
		`SELECT servers.status, enrollments.purpose
		 FROM servers JOIN agent_enrollments AS enrollments ON enrollments.server_id = servers.id
		 WHERE servers.id = ? AND enrollments.used_at IS NULL`, created.ID,
	).Scan(&status, &purpose); err != nil {
		t.Fatalf("read enrollment server state: %v", err)
	}
	if status != serverstore.StatusPending || purpose != serverstore.PurposeRebind {
		t.Fatalf("enrollment server state = (%q, %q), want pending rebind", status, purpose)
	}
}

func TestAgentUpgradeRequiresAdminOnlineFormalVersionAndUsesTypedMessage(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	handler := NewHandlerWithVersion(db, t.TempDir(), "v0.12.0")
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initialization.Code != http.StatusCreated {
		t.Fatalf("initialize = %d, %s", initialization.Code, initialization.Body.String())
	}
	adminCookie := initialization.Result().Cookies()[0]
	invitationRecorder := performRequest(t, handler, http.MethodPost, "/api/admin/invitations", nil, adminCookie)
	var invitation invitationResponse
	if invitationRecorder.Code != http.StatusCreated || json.Unmarshal(invitationRecorder.Body.Bytes(), &invitation) != nil {
		t.Fatalf("create invitation = %d, %s", invitationRecorder.Code, invitationRecorder.Body.String())
	}
	vipRegistration := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "vip-upgrade", "password": "another-password",
	}, nil)
	if vipRegistration.Code != http.StatusCreated {
		t.Fatalf("register VIP = %d, %s", vipRegistration.Code, vipRegistration.Body.String())
	}
	vipCookie := vipRegistration.Result().Cookies()[0]

	created, err := service.Create(t.Context(), "Upgrade Agent")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.11.0", false)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/servers/" + strconv.FormatInt(created.ID, 10) + "/agent-upgrade"
	if response := performRequest(t, handler, http.MethodPost, path, nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upgrade = %d", response.Code)
	}
	if response := performRequest(t, handler, http.MethodPost, path, nil, vipCookie); response.Code != http.StatusForbidden {
		t.Fatalf("VIP upgrade = %d, %s", response.Code, response.Body.String())
	}
	if response := performRequest(t, handler, http.MethodPost, path, nil, adminCookie); response.Code != http.StatusConflict {
		t.Fatalf("offline upgrade = %d, %s", response.Code, response.Body.String())
	}
	devHandler := NewHandlerWithVersion(db, t.TempDir(), "dev")
	if response := performRequest(t, devHandler, http.MethodPost, path, nil, adminCookie); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "开发版本") {
		t.Fatalf("development Panel upgrade = %d, %s", response.Code, response.Body.String())
	}

	panel := httptest.NewServer(handler)
	defer panel.Close()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+registered.Token)
	header.Set("X-VPS-Panel-Agent-Version", "v0.11.0")
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("connect Agent = %v, response = %+v", err, response)
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	upgrade := performRequest(t, handler, http.MethodPost, path, nil, adminCookie)
	if upgrade.Code != http.StatusAccepted {
		t.Fatalf("start upgrade = %d, %s", upgrade.Code, upgrade.Body.String())
	}
	readContext, cancelRead := context.WithTimeout(t.Context(), time.Second)
	messageType, message, err := connection.Read(readContext)
	cancelRead()
	if err != nil || messageType != websocket.MessageText || string(message) != `{"type":"agent_upgrade","version":"v0.12.0"}` {
		t.Fatalf("upgrade message = (%d, %q, %v)", messageType, message, err)
	}
	var agentID, serverID int64
	var tokenHash, target, status string
	if err := db.QueryRow(`SELECT id, server_id, token_hash, upgrade_target_version, upgrade_status
		FROM agents WHERE server_id = ?`, created.ID).Scan(&agentID, &serverID, &tokenHash, &target, &status); err != nil {
		t.Fatal(err)
	}
	if agentID != registered.ID || serverID != created.ID || tokenHash != token.Hash(registered.Token) ||
		target != "v0.12.0" || status != serverstore.AgentUpgradeUpgrading {
		t.Fatalf("upgrade state = id %d server %d token %q target %q status %q", agentID, serverID, tokenHash, target, status)
	}
	if err := connection.Close(websocket.StatusNormalClosure, "upgrade test reconnect"); err != nil {
		t.Fatal(err)
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOffline)

	header.Set("X-VPS-Panel-Agent-Version", "v0.12.0")
	currentConnection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("reconnect upgraded Agent = %v, response = %+v", err, response)
	}
	defer currentConnection.CloseNow()
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	current, err := service.Get(t.Context(), created.ID)
	if err != nil || current.AgentVersion != "v0.12.0" || current.AgentUpgradeStatus != "" || current.AgentUpgradeTarget != "" {
		t.Fatalf("upgraded server = (%+v, %v)", current, err)
	}
	if response := performRequest(t, handler, http.MethodPost, path, nil, adminCookie); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"status":"already_current"`) {
		t.Fatalf("same-version upgrade = %d, %s", response.Code, response.Body.String())
	}
	var finalAgentID, finalServerID int64
	var finalTokenHash string
	if err := db.QueryRow(`SELECT id, server_id, token_hash FROM agents WHERE server_id = ?`, created.ID).
		Scan(&finalAgentID, &finalServerID, &finalTokenHash); err != nil {
		t.Fatal(err)
	}
	if finalAgentID != agentID || finalServerID != serverID || finalTokenHash != tokenHash {
		t.Fatalf("Agent identity changed after upgrade: before %d/%d/%q, after %d/%d/%q",
			agentID, serverID, tokenHash, finalAgentID, finalServerID, finalTokenHash)
	}
}

func TestBootstrapAgentUpgradeScriptPreservesRegistrationAndDoesNotRegister(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	formal := NewHandlerWithVersion(db, t.TempDir(), "v0.12.0")
	response := performRequest(t, formal, http.MethodGet, "/upgrade-agent.sh", nil, nil)
	body := response.Body.String()
	if response.Code != http.StatusOK ||
		!strings.Contains(body, `VERSION="v0.12.0"`) ||
		!strings.Contains(body, "/etc/vps-panel-agent/config.json") ||
		!strings.Contains(body, `AGENT_DIR="/opt/vps-panel/agent"`) ||
		!strings.Contains(body, `BINARY_PATH="${AGENT_DIR}/vps-panel-agent"`) ||
		!strings.Contains(body, "SHA256SUMS") ||
		strings.Contains(body, "/api/agent/register") || strings.Contains(body, "--token") ||
		strings.Contains(body, " register --server") {
		t.Fatalf("bootstrap upgrade script = %d, %s", response.Code, body)
	}
	dev := NewHandlerWithVersion(db, t.TempDir(), "dev")
	if response := performRequest(t, dev, http.MethodGet, "/upgrade-agent.sh", nil, nil); response.Code != http.StatusConflict {
		t.Fatalf("development bootstrap script = %d, %s", response.Code, response.Body.String())
	}
}

func TestReleaseWorkflowPublishesAgentChecksums(t *testing.T) {
	workflow, err := os.ReadFile("../../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	source := string(workflow)
	for _, expected := range []string{
		"sha256sum", "vps-panel-agent-linux-amd64", "vps-panel-agent-linux-arm64",
		"> SHA256SUMS", "gh release upload", "release-assets/*",
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("release workflow is missing %q", expected)
		}
	}
}

func TestArchiveClosesAgentWebSocketAndRevokesToken(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	service := serverstore.NewService(db)
	created, err := service.Create(t.Context(), "Archived WebSocket Agent")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.5.1", false)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	handler := NewHandler(db, t.TempDir())
	initializeResponse := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initializeResponse.Code != http.StatusCreated {
		t.Fatalf("initialize status = %d, body = %q", initializeResponse.Code, initializeResponse.Body.String())
	}
	adminCookie := initializeResponse.Result().Cookies()[0]
	panel := httptest.NewServer(handler)
	defer panel.Close()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+registered.Token)
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		t.Fatalf("connect Agent WebSocket: %v, response = %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, service, created.ID, serverstore.StatusOnline)
	disconnected := connection.CloseRead(context.Background())

	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodDelete, panel.URL+"/api/servers/"+strconv.FormatInt(created.ID, 10), nil,
	)
	if err != nil {
		t.Fatalf("create archive request: %v", err)
	}
	request.AddCookie(adminCookie)
	archiveResponse, err := panel.Client().Do(request)
	if err != nil {
		t.Fatalf("archive server: %v", err)
	}
	archiveResponse.Body.Close()
	if archiveResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("archive status = %d, want %d", archiveResponse.StatusCode, http.StatusNoContent)
	}
	select {
	case <-disconnected.Done():
	case <-time.After(time.Second):
		t.Fatal("archiving server did not close Agent WebSocket")
	}
	if _, err := service.AuthenticateAgent(t.Context(), registered.Token); !errors.Is(err, serverstore.ErrInvalidAgentToken) {
		t.Fatalf("old Agent Token authentication error = %v, want ErrInvalidAgentToken", err)
	}
	time.Sleep(20 * time.Millisecond)
	var status string
	var archivedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT status, archived_at FROM servers WHERE id = ?`, created.ID,
	).Scan(&status, &archivedAt); err != nil {
		t.Fatalf("read archived server state: %v", err)
	}
	if status != serverstore.StatusOffline || !archivedAt.Valid {
		t.Fatalf("archived server state = (%q, %v), want offline and archived", status, archivedAt.Valid)
	}

	rejectedConnection, rejectedResponse, err := websocket.Dial(
		t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header},
	)
	if err == nil {
		rejectedConnection.CloseNow()
		t.Fatal("revoked Agent Token opened a new WebSocket")
	}
	if rejectedResponse == nil || rejectedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked Agent Token response = %+v, want 401", rejectedResponse)
	}
}

func waitForServerStatus(t *testing.T, service *serverstore.Service, serverID int64, expected string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		value, err := service.Get(ctx, serverID)
		if err != nil {
			t.Fatalf("get server status: %v", err)
		}
		if value.Status == expected {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("server status = %q, want %q", value.Status, expected)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForLastSeen(t *testing.T, service *serverstore.Service, serverID int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		value, err := service.Get(ctx, serverID)
		if err != nil {
			t.Fatalf("get server last seen: %v", err)
		}
		if value.LastSeenAt != nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("server last_seen_at was not updated")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForSystemInfo(t *testing.T, service *serverstore.Service, serverID int64, hostname string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		value, err := service.Get(ctx, serverID)
		if err != nil {
			t.Fatalf("get server system information: %v", err)
		}
		if value.SystemInfo != nil && value.SystemInfo.Hostname == hostname {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("server system information hostname did not become %q", hostname)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForMetrics(t *testing.T, service *serverstore.Service, serverID int64, cpuPercent float64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for {
		value, err := service.Get(ctx, serverID)
		if err != nil {
			t.Fatalf("get server metrics: %v", err)
		}
		if value.Metrics != nil && value.Metrics.CPUPercent == cpuPercent {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("server CPU metrics did not become %v", cpuPercent)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func performAgentRequest(
	t *testing.T,
	handler http.Handler,
	method, target string,
	body any,
	agentToken string,
) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode Agent request: %v", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, target, requestBody)
	if agentToken != "" {
		request.Header.Set("Authorization", "Bearer "+agentToken)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
