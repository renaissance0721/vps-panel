package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
		"/usr/local/bin/vps-panel-agent",
		"/etc/systemd/system/vps-panel-agent.service",
		"Restart=on-failure",
		"RestartSec=3",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
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
		connections: map[int64]*websocket.Conn{
			created.ID: currentConnection,
		},
	}
	current, err := handler.reportCurrentSystemInfo(created.ID, registered.ID, oldConnection, serverstore.SystemInfoReport{
		Hostname: "stale-host", IPv4: []string{}, IPv6: []string{},
	})
	if err != nil || current {
		t.Fatalf("old connection report = (current %v, error %v), want ignored", current, err)
	}
	current, err = handler.reportCurrentSystemInfo(created.ID, registered.ID, currentConnection, serverstore.SystemInfoReport{
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
		connections: map[int64]*websocket.Conn{
			created.ID: currentConnection,
		},
	}
	current, err := handler.reportCurrentMetrics(created.ID, registered.ID, oldConnection, serverstore.MetricsReport{
		CPUPercent: 99,
	})
	if err != nil || current {
		t.Fatalf("old connection metrics = (current %v, error %v), want ignored", current, err)
	}
	current, err = handler.reportCurrentMetrics(created.ID, registered.ID, currentConnection, serverstore.MetricsReport{
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
