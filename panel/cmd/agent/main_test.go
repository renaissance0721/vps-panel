package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"此注册令牌仅用于首次安装，当前 VPS 已存在 Agent 配置"}`))
	}))
	defer rejectingPanel.Close()
	secretEnrollment := "secret-enrollment-token"
	_, err = registerAgent(
		context.Background(), rejectingPanel.Client(), rejectingPanel.URL, secretEnrollment, configPath,
	)
	if err == nil {
		t.Fatal("registration unexpectedly succeeded with invalid enrollment")
	}
	if !strings.Contains(err.Error(), "此注册令牌仅用于首次安装") || !strings.Contains(err.Error(), "409 Conflict") {
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

func TestConnectAgentUsesStoredTokenAndKeepsConnection(t *testing.T) {
	connected := make(chan string, 1)
	handlerResult := make(chan error, 1)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			handlerResult <- err
			return
		}
		defer connection.CloseNow()
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

	configPath := writeAgentConfig(t, config{
		PanelURL: "https://panel.example", ServerID: 5, AgentID: 6, AgentToken: "retry-secret",
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

	configPath := writeAgentConfig(t, config{
		PanelURL: "https://panel.example", ServerID: 8, AgentID: 9, AgentToken: "auth-secret",
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
	originalWait := waitAgentReconnect
	t.Cleanup(func() {
		agentHeartbeatInterval = originalInterval
		waitAgentReconnect = originalWait
	})
	agentHeartbeatInterval = 10 * time.Millisecond
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
		if connectionCount.Add(1) == 1 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			messageType, message, err := connection.Read(ctx)
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
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
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
