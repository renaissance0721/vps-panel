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
		"--force",
		"vps-panel-agent-linux-${architecture}",
		"/usr/local/bin/vps-panel-agent",
		"/etc/systemd/system/vps-panel-agent.service",
		"systemctl enable",
		"systemctl restart",
	} {
		if !strings.Contains(response.Body.String(), required) {
			t.Fatalf("installer does not contain %q", required)
		}
	}
	if strings.Contains(response.Body.String(), "Restart=on-failure") {
		t.Fatal("installer enables automatic Agent reconnection")
	}
	registrationIndex := strings.Index(response.Body.String(), `"$download_path" "${registration_arguments[@]}"`)
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
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.5.0")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	panel := httptest.NewServer(NewHandler(db, t.TempDir()))
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

	if err := connection.Close(websocket.StatusNormalClosure, "test complete"); err != nil {
		t.Fatalf("close Agent WebSocket: %v", err)
	}
	waitForServerStatus(t, service, created.ID, serverstore.StatusOffline)
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
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.5.1")
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
