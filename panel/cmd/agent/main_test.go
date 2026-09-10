package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		if request.EnrollmentToken != "one-time-token" || request.AgentVersion != agentVersion {
			t.Fatalf("registration request = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"agent_id":7,"server_id":11,"agent_token":"long-term-token"}`))
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	registered, err := registerAgent(
		context.Background(), server.Client(), server.URL+"/", "one-time-token", configPath, false,
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

	if _, err := registerAgent(
		context.Background(), server.Client(), server.URL, "another-token", configPath, false,
	); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("second registerAgent() error = %v, want already registered", err)
	}
	if requestCount != 1 {
		t.Fatalf("registration request count = %d, want 1", requestCount)
	}
}

func TestRegisterAgentRejectsInvalidServerURL(t *testing.T) {
	_, err := registerAgent(
		context.Background(), http.DefaultClient, "file:///tmp/panel", "token", filepath.Join(t.TempDir(), "config.json"), false,
	)
	if err == nil {
		t.Fatal("registerAgent() accepted a non-HTTP server URL")
	}
}

func TestForceRegistrationAtomicallyReplacesExistingConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	if err := prepareConfigTarget(configPath, false); err != nil {
		t.Fatalf("prepare config: %v", err)
	}
	oldConfig := config{PanelURL: "https://old.example.com", ServerID: 3, AgentID: 4, AgentToken: "old-token"}
	if err := saveConfig(configPath, oldConfig); err != nil {
		t.Fatalf("save old config: %v", err)
	}

	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		context.Background(), panel.Client(), panel.URL, "new-enrollment", configPath, true,
	)
	if err != nil {
		t.Fatalf("force registerAgent() error = %v", err)
	}
	if replaced.AgentID != 8 || replaced.ServerID != oldConfig.ServerID || replaced.AgentToken != "new-token" {
		t.Fatalf("force registerAgent() = %+v", replaced)
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

func TestForceRegistrationFailurePreservesExistingConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	if err := prepareConfigTarget(configPath, false); err != nil {
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
		http.Error(w, "invalid enrollment", http.StatusUnauthorized)
	}))
	defer rejectingPanel.Close()
	if _, err := registerAgent(
		context.Background(), rejectingPanel.Client(), rejectingPanel.URL, "invalid", configPath, true,
	); err == nil {
		t.Fatal("force registration unexpectedly succeeded with invalid enrollment")
	}
	assertFileContents(t, configPath, original)

	unavailableClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("Panel unavailable")
	})}
	if _, err := registerAgent(
		context.Background(), unavailableClient, "https://panel.example.com", "token", configPath, true,
	); err == nil {
		t.Fatal("force registration unexpectedly succeeded while Panel was unavailable")
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
	if err := prepareConfigTarget(configPath, false); err != nil {
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
