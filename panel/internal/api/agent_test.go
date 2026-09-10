package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		"vps-panel-agent-linux-${architecture}",
		"/usr/local/bin/vps-panel-agent",
		"/etc/systemd/system/vps-panel-agent.service",
		"systemctl enable --now",
	} {
		if !strings.Contains(response.Body.String(), required) {
			t.Fatalf("installer does not contain %q", required)
		}
	}
	if strings.Contains(response.Body.String(), "Restart=on-failure") {
		t.Fatal("installer enables automatic Agent reconnection")
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
