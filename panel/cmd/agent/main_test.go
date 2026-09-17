package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/api"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestRegisterAgentSavesLongTermCredentials(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodPost || r.URL.Path != "/api/agent/register" {
			t.Fatalf("request = %s %s, want POST /api/agent/register", r.Method, r.URL.Path)
		}
		var request registrationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.EnrollmentToken != "one-time-token" || request.AgentVersion != agentVersion || request.ExistingConfig {
			t.Fatalf("registration request = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"agent_id":7,"server_id":11,"agent_token":"long-term-token"}`))
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	registered, err := registerAgent(
		context.Background(), server.Client(), server.URL+"/", "one-time-token", configPath,
	)
	if err != nil {
		t.Fatalf("registerAgent() error = %v", err)
	}
	if registered.PanelURL != server.URL || registered.AgentID != 7 ||
		registered.ServerID != 11 || registered.AgentToken != "long-term-token" {
		t.Fatalf("registerAgent() = %+v", registered)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var stored config
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if stored != registered {
		t.Fatalf("stored config = %+v, want %+v", stored, registered)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(configPath)
		if err != nil {
			t.Fatalf("stat config: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
		}
	}

	if requestCount != 1 {
		t.Fatalf("registration request count = %d, want 1", requestCount)
	}
}

func TestRegisterAgentRejectsInvalidServerURL(t *testing.T) {
	_, err := registerAgent(
		context.Background(), http.DefaultClient, "file:///tmp/panel", "token", filepath.Join(t.TempDir(), "config.json"),
	)
	if err == nil {
		t.Fatal("registerAgent() accepted a non-HTTP server URL")
	}
}

func TestRegistrationRejectsForceArgument(t *testing.T) {
	if err := runRegistration([]string{"--force"}); err == nil {
		t.Fatal("register command accepted removed --force argument")
	}
}

func TestRegistrationAtomicallyReplacesExistingConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	if _, err := prepareConfigTarget(configPath); err != nil {
		t.Fatalf("prepare config: %v", err)
	}
	oldConfig := config{PanelURL: "https://old.example.com", ServerID: 3, AgentID: 4, AgentToken: "old-token"}
	if err := saveConfig(configPath, oldConfig); err != nil {
		t.Fatalf("save old config: %v", err)
	}

	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request registrationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode registration request: %v", err)
		}
		if !request.ExistingConfig {
			t.Fatal("registration did not report the existing Agent config")
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read old config during registration: %v", err)
		}
		var current config
		if err := json.Unmarshal(data, &current); err != nil || current != oldConfig {
			t.Fatalf("config during registration = (%+v, %v), want old config", current, err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"agent_id":8,"server_id":3,"agent_token":"new-token"}`))
	}))
	defer panel.Close()

	replaced, err := registerAgent(
		context.Background(), panel.Client(), panel.URL, "new-enrollment", configPath,
	)
	if err != nil {
		t.Fatalf("registerAgent() error = %v", err)
	}
	if replaced.AgentID != 8 || replaced.ServerID != oldConfig.ServerID || replaced.AgentToken != "new-token" {
		t.Fatalf("registerAgent() = %+v", replaced)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read replaced config: %v", err)
	}
	var stored config
	if err := json.Unmarshal(data, &stored); err != nil || stored != replaced {
		t.Fatalf("replaced config = (%+v, %v), want %+v", stored, err, replaced)
	}
	if temporaryFiles, err := filepath.Glob(filepath.Join(filepath.Dir(configPath), ".config-*")); err != nil || len(temporaryFiles) != 0 {
		t.Fatalf("temporary config files = (%v, %v), want none", temporaryFiles, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(configPath)
		if err != nil {
			t.Fatalf("stat replaced config: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("replaced config permissions = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestRegistrationFailurePreservesExistingConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	if _, err := prepareConfigTarget(configPath); err != nil {
		t.Fatalf("prepare config: %v", err)
	}
	oldConfig := config{PanelURL: "https://old.example.com", ServerID: 3, AgentID: 4, AgentToken: "old-token"}
	if err := saveConfig(configPath, oldConfig); err != nil {
		t.Fatalf("save old config: %v", err)
	}
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read original config: %v", err)
	}

	rejectingPanel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Enrollment Token 无效、已使用或已过期"}`))
	}))
	defer rejectingPanel.Close()
	secretEnrollment := "secret-enrollment-token"
	_, err = registerAgent(
		context.Background(), rejectingPanel.Client(), rejectingPanel.URL, secretEnrollment, configPath,
	)
	if err == nil {
		t.Fatal("registration unexpectedly succeeded with invalid enrollment")
	}
	if !strings.Contains(err.Error(), "Enrollment Token 无效") || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("registration error = %q, want concrete Panel error and status", err)
	}
	if strings.Contains(err.Error(), secretEnrollment) {
		t.Fatal("registration error exposed the Enrollment Token")
	}
	assertFileContents(t, configPath, original)

	echoingPanel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"invalid token: secret-enrollment-token"}`))
	}))
	defer echoingPanel.Close()
	_, err = registerAgent(
		context.Background(), echoingPanel.Client(), echoingPanel.URL, secretEnrollment, configPath,
	)
	if err == nil || err.Error() != "Panel rejected registration: 409 Conflict" {
		t.Fatalf("token-echoing registration error = %v, want HTTP status fallback", err)
	}
	if strings.Contains(err.Error(), secretEnrollment) {
		t.Fatal("token-echoing registration error exposed the Enrollment Token")
	}
	assertFileContents(t, configPath, original)

	nonJSONPanel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream failed", http.StatusBadGateway)
	}))
	defer nonJSONPanel.Close()
	_, err = registerAgent(
		context.Background(), nonJSONPanel.Client(), nonJSONPanel.URL, secretEnrollment, configPath,
	)
	if err == nil || err.Error() != "Panel rejected registration: 502 Bad Gateway" {
		t.Fatalf("non-JSON registration error = %v, want HTTP status fallback", err)
	}
	assertFileContents(t, configPath, original)

	invalidResponsePanel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"agent_id":`))
	}))
	defer invalidResponsePanel.Close()
	if _, err := registerAgent(
		context.Background(), invalidResponsePanel.Client(), invalidResponsePanel.URL, "token", configPath,
	); err == nil {
		t.Fatal("registration unexpectedly succeeded with an invalid Panel response")
	}
	assertFileContents(t, configPath, original)

	unavailableClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("Panel unavailable")
	})}
	if _, err := registerAgent(
		context.Background(), unavailableClient, "https://panel.example.com", "token", configPath,
	); err == nil {
		t.Fatal("registration unexpectedly succeeded while Panel was unavailable")
	}
	assertFileContents(t, configPath, original)
}

func newTestRegistrationPanel(t *testing.T) (*httptest.Server, *serverstore.Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open Panel database: %v", err)
	}
	panel := httptest.NewServer(api.NewHandler(db, t.TempDir()))
	t.Cleanup(func() {
		panel.Close()
		db.Close()
	})
	return panel, serverstore.NewService(db), db
}

func TestRegistrationOverwritesExistingAgentAcrossServersAndPanels(t *testing.T) {
	panelA, serviceA, dbA := newTestRegistrationPanel(t)
	serverA, err := serviceA.Create(t.Context(), "Server A")
	if err != nil {
		t.Fatalf("create Server A: %v", err)
	}
	serverB, err := serviceA.Create(t.Context(), "Server B")
	if err != nil {
		t.Fatalf("create Server B: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	first, err := registerAgent(t.Context(), panelA.Client(), panelA.URL, serverA.EnrollmentToken, configPath)
	if err != nil || first.ServerID != serverA.ID {
		t.Fatalf("first registration = (%+v, %v), want Server A", first, err)
	}
	rebind, err := serviceA.CreateEnrollment(t.Context(), serverA.ID)
	if err != nil {
		t.Fatalf("create same-Server rebind enrollment: %v", err)
	}
	rebound, err := registerAgent(t.Context(), panelA.Client(), panelA.URL, rebind.EnrollmentToken, configPath)
	if err != nil || rebound.ServerID != serverA.ID || rebound.AgentToken == first.AgentToken {
		t.Fatalf("same-Server rebind = (%+v, %v), want replacement credentials", rebound, err)
	}
	assertRegisteredConfig(t, configPath, rebound, first.AgentToken)
	if _, err := agentcontrol.NewService(dbA, time.Now).AuthenticateAgent(t.Context(), first.AgentToken); !errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
		t.Fatalf("old same-Server credential error = %v, want ErrInvalidAgentToken", err)
	}
	second, err := registerAgent(t.Context(), panelA.Client(), panelA.URL, serverB.EnrollmentToken, configPath)
	if err != nil || second.ServerID != serverB.ID || second.AgentToken == rebound.AgentToken {
		t.Fatalf("initial enrollment on existing VPS = (%+v, %v), want Server B with new credentials", second, err)
	}
	assertRegisteredConfig(t, configPath, second, rebound.AgentToken)
	var serverCount int
	if err := dbA.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&serverCount); err != nil || serverCount != 2 {
		t.Fatalf("Panel A server count = (%d, %v), want 2", serverCount, err)
	}

	panelB, serviceB, _ := newTestRegistrationPanel(t)
	serverOnB, err := serviceB.Create(t.Context(), "Server on Panel B")
	if err != nil {
		t.Fatalf("create Server on Panel B: %v", err)
	}
	third, err := registerAgent(t.Context(), panelB.Client(), panelB.URL, serverOnB.EnrollmentToken, configPath)
	if err != nil || third.PanelURL != panelB.URL || third.ServerID != serverOnB.ID {
		t.Fatalf("cross-Panel registration = (%+v, %v), want Panel B Server", third, err)
	}
	assertRegisteredConfig(t, configPath, third, second.AgentToken)
}

func TestRejectedNewEnrollmentPreservesExistingAgentConfig(t *testing.T) {
	panel, service, db := newTestRegistrationPanel(t)
	oldServer, err := service.Create(t.Context(), "Existing Agent")
	if err != nil {
		t.Fatalf("create existing Server: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	if _, err := registerAgent(t.Context(), panel.Client(), panel.URL, oldServer.EnrollmentToken, configPath); err != nil {
		t.Fatalf("register existing Agent: %v", err)
	}
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read existing config: %v", err)
	}

	expired, err := service.Create(t.Context(), "Expired enrollment")
	if err != nil {
		t.Fatalf("create expired Server: %v", err)
	}
	if _, err := db.Exec(`UPDATE agent_enrollments SET expires_at = 0 WHERE server_id = ?`, expired.ID); err != nil {
		t.Fatalf("expire enrollment: %v", err)
	}
	used, err := service.Create(t.Context(), "Used enrollment")
	if err != nil {
		t.Fatalf("create used Server: %v", err)
	}
	if _, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), used.EnrollmentToken, "test", false); err != nil {
		t.Fatalf("consume enrollment: %v", err)
	}
	for _, test := range []struct {
		name  string
		token string
	}{
		{"invalid", "invalid-token"},
		{"expired", expired.EnrollmentToken},
		{"used", used.EnrollmentToken},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := registerAgent(t.Context(), panel.Client(), panel.URL, test.token, configPath); err == nil {
				t.Fatal("rejected enrollment unexpectedly replaced existing Agent")
			}
			assertFileContents(t, configPath, original)
		})
	}
}

func TestFailedAtomicConfigReplacementLeavesNoPartialFile(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing", "config.json")
	if err := saveConfig(missingPath, config{PanelURL: "https://panel.example", ServerID: 12, AgentID: 20, AgentToken: "new-token"}); err == nil {
		t.Fatal("saveConfig unexpectedly created a temporary file in a missing directory")
	}
	if _, err := os.Stat(missingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config after temporary file creation failure = %v, want no file", err)
	}

	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	if _, err := prepareConfigTarget(configPath); err != nil {
		t.Fatalf("prepare config target: %v", err)
	}
	if err := os.Mkdir(configPath, 0o700); err != nil {
		t.Fatalf("block config replacement: %v", err)
	}
	if err := saveConfig(configPath, config{PanelURL: "https://panel.example", ServerID: 12, AgentID: 20, AgentToken: "new-token"}); err == nil {
		t.Fatal("saveConfig unexpectedly replaced a directory")
	}
	if info, err := os.Stat(configPath); err != nil || !info.IsDir() {
		t.Fatalf("config target after failure = (%v, %v), want original directory", info, err)
	}
	if temporaryFiles, err := filepath.Glob(filepath.Join(filepath.Dir(configPath), ".config-*")); err != nil || len(temporaryFiles) != 0 {
		t.Fatalf("temporary files after failed replacement = (%v, %v), want none", temporaryFiles, err)
	}
}

func assertRegisteredConfig(t *testing.T, path string, want config, oldToken string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Agent config: %v", err)
	}
	var stored config
	if err := json.Unmarshal(data, &stored); err != nil || stored != want || bytes.Contains(data, []byte(oldToken)) {
		t.Fatalf("Agent config after rebind = (%+v, %v), want new credentials only", stored, err)
	}
}

func TestConnectAgentUsesStoredTokenAndKeepsConnection(t *testing.T) {
	connected := make(chan string, 1)
	handlerResult := make(chan error, 1)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveTestAgentConfig(w, r) {
			return
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			handlerResult <- err
			return
		}
		defer connection.CloseNow()
		messageType, message, err := connection.Read(context.Background())
		if err != nil || messageType != websocket.MessageText || !strings.Contains(string(message), `"type":"system_info"`) {
			handlerResult <- fmt.Errorf("read system information: type %d, message %q, error %v", messageType, message, err)
			return
		}
		connected <- r.Header.Get("Authorization")
		<-connection.CloseRead(context.Background()).Done()
		handlerResult <- nil
	}))
	defer panel.Close()

	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	if _, err := prepareConfigTarget(configPath); err != nil {
		t.Fatalf("prepare config: %v", err)
	}
	if err := saveConfig(configPath, config{
		PanelURL:   panel.URL,
		ServerID:   11,
		AgentID:    7,
		AgentToken: "long-term-token",
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- connectAgent(ctx, configPath) }()

	select {
	case authorization := <-connected:
		if authorization != "Bearer long-term-token" {
			t.Fatalf("Authorization = %q, want Bearer token", authorization)
		}
	case err := <-handlerResult:
		t.Fatalf("WebSocket handler error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("Agent did not establish WebSocket connection")
	}

	select {
	case err := <-result:
		t.Fatalf("connectAgent() returned before cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("connectAgent() after cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("connectAgent() did not stop after cancellation")
	}
	select {
	case err := <-handlerResult:
		if err != nil {
			t.Fatalf("WebSocket handler error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WebSocket handler did not observe disconnect")
	}
}

func TestReconnectBackoffSequence(t *testing.T) {
	delay := initialReconnectDelay
	want := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for index, expected := range want {
		if delay != expected {
			t.Fatalf("delay %d = %s, want %s", index, delay, expected)
		}
		delay = nextReconnectDelay(delay)
	}
}

func TestConnectAgentRetriesUnavailablePanelAndCancelsWait(t *testing.T) {
	originalDial := dialAgentWebSocket
	originalWait := waitAgentReconnect
	t.Cleanup(func() {
		dialAgentWebSocket = originalDial
		waitAgentReconnect = originalWait
	})
	dialAgentWebSocket = func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		return nil, nil, errors.New("unavailable")
	}
	waiting := make(chan time.Duration, 1)
	waitAgentReconnect = func(ctx context.Context, delay time.Duration) bool {
		waiting <- delay
		<-ctx.Done()
		return false
	}
	fallbackPanel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveTestAgentConfig(w, r)
	}))
	defer fallbackPanel.Close()

	configPath := writeAgentConfig(t, config{
		PanelURL: fallbackPanel.URL, ServerID: 5, AgentID: 6, AgentToken: "retry-secret",
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- connectAgent(ctx, configPath) }()
	select {
	case delay := <-waiting:
		if delay != time.Second {
			t.Fatalf("first retry delay = %s, want 1s", delay)
		}
	case err := <-result:
		t.Fatalf("connectAgent() exited while Panel was unavailable: %v", err)
	case <-time.After(time.Second):
		t.Fatal("Agent did not schedule a reconnect")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("connectAgent() cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt reconnect wait")
	}
}

func TestConnectAgentRetriesUnauthorizedWithoutChangingConfigOrLoggingToken(t *testing.T) {
	originalDial := dialAgentWebSocket
	originalWait := waitAgentReconnect
	originalLogWriter := log.Writer()
	t.Cleanup(func() {
		dialAgentWebSocket = originalDial
		waitAgentReconnect = originalWait
		log.SetOutput(originalLogWriter)
	})
	dialAgentWebSocket = func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		return nil, &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     "401 Unauthorized",
			Body:       io.NopCloser(strings.NewReader("")),
		}, errors.New("rejected")
	}
	var retryDelay time.Duration
	waitAgentReconnect = func(_ context.Context, delay time.Duration) bool {
		retryDelay = delay
		return false
	}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	fallbackPanel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveTestAgentConfig(w, r)
	}))
	defer fallbackPanel.Close()

	configPath := writeAgentConfig(t, config{
		PanelURL: fallbackPanel.URL, ServerID: 8, AgentID: 9, AgentToken: "auth-secret",
	})
	originalConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Agent config: %v", err)
	}
	if err := connectAgent(context.Background(), configPath); err != nil {
		t.Fatalf("connectAgent() error = %v", err)
	}
	if retryDelay != maximumReconnectDelay {
		t.Fatalf("401 retry delay = %s, want %s", retryDelay, maximumReconnectDelay)
	}
	if !strings.Contains(logs.String(), "Agent authentication rejected") {
		t.Fatalf("401 log = %q, want fixed authentication message", logs.String())
	}
	if strings.Contains(logs.String(), "auth-secret") {
		t.Fatal("401 retry log exposed the Agent Token")
	}
	assertFileContents(t, configPath, originalConfig)
}

func TestConnectAgentSendsHeartbeatAndReconnectsAfterDisconnect(t *testing.T) {
	originalInterval := agentHeartbeatInterval
	originalMetricsFactory := newAgentMetrics
	originalWait := waitAgentReconnect
	t.Cleanup(func() {
		agentHeartbeatInterval = originalInterval
		newAgentMetrics = originalMetricsFactory
		waitAgentReconnect = originalWait
	})
	agentHeartbeatInterval = 10 * time.Millisecond
	var metricsCollectorCount atomic.Int32
	newAgentMetrics = func() *metricsCollector {
		metricsCollectorCount.Add(1)
		return &metricsCollector{}
	}
	reconnectDelay := make(chan time.Duration, 1)
	waitAgentReconnect = func(ctx context.Context, delay time.Duration) bool {
		reconnectDelay <- delay
		return ctx.Err() == nil
	}

	var connectionCount atomic.Int32
	heartbeat := make(chan string, 1)
	secondConnected := make(chan struct{}, 1)
	handlerErrors := make(chan error, 2)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveTestAgentConfig(w, r) {
			return
		}
		if r.Header.Get("Authorization") != "Bearer heartbeat-secret" {
			handlerErrors <- errors.New("missing Agent authorization")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			handlerErrors <- err
			return
		}
		defer connection.CloseNow()
		connectionNumber := connectionCount.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		messageType, message, err := connection.Read(ctx)
		if err != nil {
			handlerErrors <- err
			return
		}
		var systemInfo systemInfoMessage
		if messageType != websocket.MessageText || json.Unmarshal(message, &systemInfo) != nil || systemInfo.Type != "system_info" {
			handlerErrors <- fmt.Errorf("invalid system information message: %q", message)
			return
		}
		if connectionNumber == 1 {
			messageType, message, err = connection.Read(ctx)
			if err != nil {
				handlerErrors <- err
				return
			}
			if messageType != websocket.MessageText {
				handlerErrors <- errors.New("heartbeat was not a text message")
				return
			}
			heartbeat <- string(message)
			_ = connection.Close(websocket.StatusNormalClosure, "reconnect test")
			return
		}
		secondConnected <- struct{}{}
		<-connection.CloseRead(context.Background()).Done()
	}))
	defer panel.Close()

	configPath := writeAgentConfig(t, config{
		PanelURL: panel.URL, ServerID: 10, AgentID: 11, AgentToken: "heartbeat-secret",
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- connectAgent(ctx, configPath) }()
	select {
	case message := <-heartbeat:
		if message != `{"type":"heartbeat"}` {
			t.Fatalf("heartbeat = %q", message)
		}
	case err := <-handlerErrors:
		t.Fatalf("first WebSocket handler error = %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Agent did not send a heartbeat")
	}
	select {
	case delay := <-reconnectDelay:
		if delay != initialReconnectDelay {
			t.Fatalf("post-connection retry delay = %s, want %s", delay, initialReconnectDelay)
		}
	case <-time.After(time.Second):
		t.Fatal("Agent did not schedule reconnect after disconnect")
	}
	select {
	case <-secondConnected:
	case err := <-handlerErrors:
		t.Fatalf("replacement WebSocket handler error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("Agent did not reconnect")
	}
	if connectionCount.Load() != 2 {
		t.Fatalf("system information connection count = %d, want 2", connectionCount.Load())
	}
	if metricsCollectorCount.Load() != 2 {
		t.Fatalf("metrics collector count = %d, want one fresh baseline per connection", metricsCollectorCount.Load())
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("connectAgent() cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Agent did not stop after cancellation")
	}
}

func TestConnectAgentSendsMetricsAndHeartbeat(t *testing.T) {
	originalMetricsInterval := agentMetricsInterval
	originalHeartbeatInterval := agentHeartbeatInterval
	originalMetricsFactory := newAgentMetrics
	originalCollect := collectAgentMetrics
	t.Cleanup(func() {
		agentMetricsInterval = originalMetricsInterval
		agentHeartbeatInterval = originalHeartbeatInterval
		newAgentMetrics = originalMetricsFactory
		collectAgentMetrics = originalCollect
	})
	agentMetricsInterval = 5 * time.Millisecond
	agentHeartbeatInterval = 7 * time.Millisecond
	newAgentMetrics = func() *metricsCollector { return &metricsCollector{} }
	collectAgentMetrics = func(*metricsCollector) (metricsMessage, bool) {
		return metricsMessage{
			Type:             "metrics",
			CPUPercent:       32.4,
			MemoryUsedBytes:  128 << 20,
			MemoryTotalBytes: 512 << 20,
			DiskUsedBytes:    5 << 30,
			DiskTotalBytes:   10 << 30,
			UptimeSeconds:    86400,
			NICRXBytes:       12 << 30,
			NICTXBytes:       34 << 30,
		}, true
	}

	received := make(chan metricsMessage, 1)
	handlerErrors := make(chan error, 1)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveTestAgentConfig(w, r) {
			return
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			handlerErrors <- err
			return
		}
		defer connection.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, first, err := connection.Read(ctx)
		if err != nil || !strings.Contains(string(first), `"type":"system_info"`) {
			handlerErrors <- fmt.Errorf("first message = %q, error %v", first, err)
			return
		}
		var metrics metricsMessage
		heartbeatReceived := false
		for metrics.Type == "" || !heartbeatReceived {
			messageType, message, err := connection.Read(ctx)
			if err != nil || messageType != websocket.MessageText {
				handlerErrors <- fmt.Errorf("read Agent message: %v", err)
				return
			}
			var envelope struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(message, &envelope) != nil {
				handlerErrors <- fmt.Errorf("decode Agent message: %q", message)
				return
			}
			switch envelope.Type {
			case "metrics":
				if err := json.Unmarshal(message, &metrics); err != nil {
					handlerErrors <- err
					return
				}
			case "heartbeat":
				heartbeatReceived = true
			}
		}
		received <- metrics
		<-connection.CloseRead(context.Background()).Done()
	}))
	defer panel.Close()

	configPath := writeAgentConfig(t, config{
		PanelURL: panel.URL, ServerID: 20, AgentID: 21, AgentToken: "metrics-secret",
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- connectAgent(ctx, configPath) }()
	select {
	case metrics := <-received:
		if metrics.CPUPercent != 32.4 || metrics.MemoryUsedBytes != 128<<20 ||
			metrics.MemoryTotalBytes != 512<<20 || metrics.DiskUsedBytes != 5<<30 ||
			metrics.DiskTotalBytes != 10<<30 || metrics.UptimeSeconds != 86400 ||
			metrics.NICRXBytes != 12<<30 || metrics.NICTXBytes != 34<<30 {
			t.Fatalf("metrics = %+v", metrics)
		}
	case err := <-handlerErrors:
		t.Fatalf("WebSocket handler error = %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Agent did not send metrics and heartbeat")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("connectAgent() cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Agent did not stop after cancellation")
	}
}

func TestMetricsCollectionFailureDoesNotStopHeartbeat(t *testing.T) {
	originalMetricsInterval := agentMetricsInterval
	originalHeartbeatInterval := agentHeartbeatInterval
	originalMetricsFactory := newAgentMetrics
	originalCollect := collectAgentMetrics
	t.Cleanup(func() {
		agentMetricsInterval = originalMetricsInterval
		agentHeartbeatInterval = originalHeartbeatInterval
		newAgentMetrics = originalMetricsFactory
		collectAgentMetrics = originalCollect
	})
	agentMetricsInterval = time.Millisecond
	agentHeartbeatInterval = 5 * time.Millisecond
	newAgentMetrics = func() *metricsCollector { return &metricsCollector{} }
	collectAgentMetrics = func(*metricsCollector) (metricsMessage, bool) {
		return metricsMessage{}, false
	}

	heartbeat := make(chan struct{}, 1)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveTestAgentConfig(w, r) {
			return
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, _, err := connection.Read(ctx); err != nil {
			return
		}
		for {
			_, message, err := connection.Read(ctx)
			if err != nil {
				return
			}
			if string(message) == `{"type":"heartbeat"}` {
				heartbeat <- struct{}{}
				<-connection.CloseRead(context.Background()).Done()
				return
			}
		}
	}))
	defer panel.Close()

	configPath := writeAgentConfig(t, config{
		PanelURL: panel.URL, ServerID: 22, AgentID: 23, AgentToken: "best-effort-secret",
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- connectAgent(ctx, configPath) }()
	select {
	case <-heartbeat:
	case <-time.After(time.Second):
		t.Fatal("metrics collection failure stopped heartbeat")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("connectAgent() cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Agent did not stop after cancellation")
	}
}

func TestMetricsWriteFailureStartsReconnect(t *testing.T) {
	originalMetricsInterval := agentMetricsInterval
	originalHeartbeatInterval := agentHeartbeatInterval
	originalMetricsFactory := newAgentMetrics
	originalCollect := collectAgentMetrics
	originalWrite := writeAgentMetrics
	originalWait := waitAgentReconnect
	t.Cleanup(func() {
		agentMetricsInterval = originalMetricsInterval
		agentHeartbeatInterval = originalHeartbeatInterval
		newAgentMetrics = originalMetricsFactory
		collectAgentMetrics = originalCollect
		writeAgentMetrics = originalWrite
		waitAgentReconnect = originalWait
	})
	agentMetricsInterval = time.Millisecond
	agentHeartbeatInterval = time.Hour
	newAgentMetrics = func() *metricsCollector { return &metricsCollector{} }
	collectAgentMetrics = func(*metricsCollector) (metricsMessage, bool) {
		return metricsMessage{Type: "metrics"}, true
	}
	metricsAttempted := make(chan struct{}, 1)
	writeAgentMetrics = func(context.Context, *websocket.Conn, metricsMessage) error {
		metricsAttempted <- struct{}{}
		return errors.New("write failed")
	}
	reconnectDelay := make(chan time.Duration, 1)
	waitAgentReconnect = func(_ context.Context, delay time.Duration) bool {
		reconnectDelay <- delay
		return false
	}

	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveTestAgentConfig(w, r) {
			return
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		if _, _, err := connection.Read(context.Background()); err != nil {
			return
		}
		<-connection.CloseRead(context.Background()).Done()
	}))
	defer panel.Close()
	configPath := writeAgentConfig(t, config{
		PanelURL: panel.URL, ServerID: 24, AgentID: 25, AgentToken: "metrics-failure-secret",
	})
	if err := connectAgent(context.Background(), configPath); err != nil {
		t.Fatalf("connectAgent() error = %v", err)
	}
	select {
	case <-metricsAttempted:
	default:
		t.Fatal("Agent did not attempt a metrics write")
	}
	select {
	case delay := <-reconnectDelay:
		if delay != initialReconnectDelay {
			t.Fatalf("metrics failure retry delay = %s, want %s", delay, initialReconnectDelay)
		}
	default:
		t.Fatal("metrics write failure did not schedule reconnect")
	}
}

func TestHeartbeatWriteFailureStartsReconnect(t *testing.T) {
	originalInterval := agentHeartbeatInterval
	originalWrite := writeAgentHeartbeat
	originalWait := waitAgentReconnect
	t.Cleanup(func() {
		agentHeartbeatInterval = originalInterval
		writeAgentHeartbeat = originalWrite
		waitAgentReconnect = originalWait
	})
	agentHeartbeatInterval = time.Millisecond
	heartbeatAttempted := make(chan struct{}, 1)
	writeAgentHeartbeat = func(context.Context, *websocket.Conn) error {
		heartbeatAttempted <- struct{}{}
		return errors.New("write failed")
	}
	reconnectDelay := make(chan time.Duration, 1)
	waitAgentReconnect = func(_ context.Context, delay time.Duration) bool {
		reconnectDelay <- delay
		return false
	}

	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveTestAgentConfig(w, r) {
			return
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		if _, _, err := connection.Read(context.Background()); err != nil {
			return
		}
		<-connection.CloseRead(context.Background()).Done()
	}))
	defer panel.Close()
	configPath := writeAgentConfig(t, config{
		PanelURL: panel.URL, ServerID: 12, AgentID: 13, AgentToken: "write-failure-secret",
	})
	if err := connectAgent(context.Background(), configPath); err != nil {
		t.Fatalf("connectAgent() error = %v", err)
	}
	select {
	case <-heartbeatAttempted:
	default:
		t.Fatal("Agent did not attempt a heartbeat write")
	}
	select {
	case delay := <-reconnectDelay:
		if delay != initialReconnectDelay {
			t.Fatalf("heartbeat failure retry delay = %s, want %s", delay, initialReconnectDelay)
		}
	default:
		t.Fatal("heartbeat write failure did not schedule reconnect")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func assertFileContents(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preserved config: %v", err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("config changed after failed registration\ngot:  %q\nwant: %q", actual, expected)
	}
}

func writeAgentConfig(t *testing.T, value config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent", "config.json")
	if _, err := prepareConfigTarget(path); err != nil {
		t.Fatalf("prepare Agent config: %v", err)
	}
	if err := saveConfig(path, value); err != nil {
		t.Fatalf("save Agent config: %v", err)
	}
	return path
}

func serveTestAgentConfig(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/agent/config":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":1,"xray":{"enabled":false,"proxies":[]},"realm":{"enabled":false,"relays":[]}}`))
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/agent/config/result":
		w.WriteHeader(http.StatusNoContent)
		return true
	default:
		return false
	}
}
