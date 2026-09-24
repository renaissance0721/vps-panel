package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestConfigSynchronizerReportsSuccessOnceAndRetriesFailure(t *testing.T) {
	var mu sync.Mutex
	state := desiredState{Version: 1}
	getCount := 0
	results := make([]configResult, 0)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer config-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agent/config":
			mu.Lock()
			getCount++
			current := state
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(current)
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent/config/result":
			var result configResult
			if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
				t.Errorf("decode config result: %v", err)
				return
			}
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer panel.Close()

	synchronizer := newConfigSynchronizer(config{PanelURL: panel.URL, AgentToken: "config-secret"}, panel.Client())
	synchronizer.applyState = func(_ context.Context, state desiredState) error {
		if state.Xray.Enabled {
			return errors.New(unsupportedManagedConfigMessage)
		}
		return nil
	}
	if err := synchronizer.sync(t.Context()); err != nil {
		t.Fatalf("initial config sync: %v", err)
	}
	if err := synchronizer.sync(t.Context()); err != nil {
		t.Fatalf("duplicate config sync: %v", err)
	}
	mu.Lock()
	if getCount != 2 || len(results) != 1 || results[0].Version != 1 ||
		results[0].Status != "success" || results[0].Message != "" {
		t.Fatalf("initial sync = (GET %d, results %+v)", getCount, results)
	}
	state = desiredState{
		Version: 2,
		Xray: desiredXrayState{
			Enabled: true,
		},
	}
	mu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		if err := synchronizer.sync(t.Context()); err == nil {
			t.Fatal("unsupported desired state was reported as successful")
		}
	}
	mu.Lock()
	if synchronizer.lastSuccessfulVersion != 1 || len(results) != 3 {
		t.Fatalf("failed sync state = (last %d, results %+v)", synchronizer.lastSuccessfulVersion, results)
	}
	for _, result := range results[1:] {
		if result.Version != 2 || result.Status != "failed" || result.Message != unsupportedManagedConfigMessage {
			t.Fatalf("failed config result = %+v", result)
		}
	}
	state = desiredState{Version: 2}
	mu.Unlock()
	if err := synchronizer.sync(t.Context()); err != nil {
		t.Fatalf("retry supported desired state: %v", err)
	}
	if synchronizer.lastSuccessfulVersion != 2 {
		t.Fatalf("last successful version = %d, want 2", synchronizer.lastSuccessfulVersion)
	}
}

func TestDesiredStateErrorMessageDoesNotExposeDiagnostics(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("%w: private diagnostic", errManagedXrayDownload), errManagedXrayDownload.Error()},
		{fmt.Errorf("%w: candidate details", errManagedXrayValidation), errManagedXrayValidation.Error()},
		{fmt.Errorf("%w: systemctl details", errManagedXrayStart), errManagedXrayStart.Error()},
		{fmt.Errorf("%w: missing port", errManagedXrayHealth), errManagedXrayHealth.Error()},
		{fmt.Errorf("%w: iptables details", errManagedProxyFirewall), errManagedProxyFirewall.Error()},
		{fmt.Errorf("%w: certificate private diagnostic", errManagedACME), errManagedACME.Error()},
		{fmt.Errorf("%w: nft exit status", errManagedChinaInboundFirewall), errManagedChinaInboundFirewall.Error()},
		{errManagedChinaInboundRequiresNFT, errManagedChinaInboundRequiresNFT.Error()},
		{fmt.Errorf("%w: private diagnostic", errUnsupportedInitSystem), errUnsupportedInitSystem.Error()},
		{errors.New("internal path detail"), "managed runtime apply failed"},
	}
	for _, test := range tests {
		if got := desiredStateErrorMessage(test.err); got != test.want {
			t.Fatalf("desired state message = %q, want %q", got, test.want)
		}
	}
}

func TestConfigChangedFetchesCurrentDesiredStateAndPollsWithoutDuplicateResult(t *testing.T) {
	originalHeartbeatInterval := agentHeartbeatInterval
	originalMetricsInterval := agentMetricsInterval
	originalConfigInterval := agentConfigPollInterval
	originalMetricsFactory := newAgentMetrics
	t.Cleanup(func() {
		agentHeartbeatInterval = originalHeartbeatInterval
		agentMetricsInterval = originalMetricsInterval
		agentConfigPollInterval = originalConfigInterval
		newAgentMetrics = originalMetricsFactory
	})
	agentHeartbeatInterval = time.Hour
	agentMetricsInterval = time.Hour
	agentConfigPollInterval = 20 * time.Millisecond
	newAgentMetrics = func() *metricsCollector { return &metricsCollector{} }

	var desiredVersion atomic.Int64
	desiredVersion.Store(1)
	var getCount atomic.Int32
	results := make(chan configResult, 8)
	sendNotifications := make(chan struct{})
	handlerErrors := make(chan error, 1)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agent/config":
			getCount.Add(1)
			_ = json.NewEncoder(w).Encode(desiredState{Version: desiredVersion.Load()})
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent/config/result":
			var result configResult
			if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
				handlerErrors <- err
				return
			}
			results <- result
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/agent/ws":
			connection, err := websocket.Accept(w, r, nil)
			if err != nil {
				handlerErrors <- err
				return
			}
			defer connection.CloseNow()
			if _, _, err := connection.Read(context.Background()); err != nil {
				handlerErrors <- err
				return
			}
			<-sendNotifications
			for _, version := range []int64{2, 3, 999} {
				message, _ := json.Marshal(struct {
					Type    string `json:"type"`
					Version int64  `json:"version"`
				}{Type: "config_changed", Version: version})
				if err := connection.Write(context.Background(), websocket.MessageText, message); err != nil {
					handlerErrors <- err
					return
				}
			}
			<-connection.CloseRead(context.Background()).Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer panel.Close()

	configPath := writeAgentConfig(t, config{
		PanelURL: panel.URL, ServerID: 31, AgentID: 32, AgentToken: "notification-secret",
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- connectAgent(ctx, configPath) }()
	first := waitForConfigResult(t, results, handlerErrors)
	if first.Version != 1 || first.Status != "success" {
		t.Fatalf("initial config result = %+v", first)
	}
	desiredVersion.Store(4)
	close(sendNotifications)
	second := waitForConfigResult(t, results, handlerErrors)
	if second.Version != 4 || second.Status != "success" {
		t.Fatalf("notification config result = %+v, want current REST version 4", second)
	}

	deadline := time.Now().Add(time.Second)
	for getCount.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if getCount.Load() < 3 {
		t.Fatal("connected Agent did not perform fallback config polling")
	}
	select {
	case duplicate := <-results:
		t.Fatalf("same successful version produced duplicate result %+v", duplicate)
	case err := <-handlerErrors:
		t.Fatalf("Panel handler error: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("connectAgent() cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("connectAgent() did not stop after cancellation")
	}
}

func TestWebSocketUnavailableStillPerformsRESTConfigSync(t *testing.T) {
	originalDial := dialAgentWebSocket
	originalWait := waitAgentReconnect
	t.Cleanup(func() {
		dialAgentWebSocket = originalDial
		waitAgentReconnect = originalWait
	})
	dialAgentWebSocket = func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		return nil, nil, errors.New("WebSocket unavailable")
	}
	waitAgentReconnect = func(context.Context, time.Duration) bool { return false }
	reported := make(chan configResult, 1)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/config":
			_ = json.NewEncoder(w).Encode(desiredState{Version: 1})
		case "/api/agent/config/result":
			var result configResult
			_ = json.NewDecoder(r.Body).Decode(&result)
			reported <- result
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer panel.Close()
	configPath := filepath.Join(t.TempDir(), "agent", "config.json")
	if _, err := prepareConfigTarget(configPath); err != nil {
		t.Fatalf("prepare config: %v", err)
	}
	if err := saveConfig(configPath, config{
		PanelURL: panel.URL, ServerID: 41, AgentID: 42, AgentToken: "fallback-secret",
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := connectAgent(context.Background(), configPath); err != nil {
		t.Fatalf("connectAgent() error = %v", err)
	}
	select {
	case result := <-reported:
		if result.Version != 1 || result.Status != "success" {
			t.Fatalf("fallback config result = %+v", result)
		}
	default:
		t.Fatal("WebSocket failure did not trigger REST config sync")
	}
}

func TestAgentReconnectRechecksDesiredState(t *testing.T) {
	originalHeartbeatInterval := agentHeartbeatInterval
	originalMetricsInterval := agentMetricsInterval
	originalWait := waitAgentReconnect
	t.Cleanup(func() {
		agentHeartbeatInterval = originalHeartbeatInterval
		agentMetricsInterval = originalMetricsInterval
		waitAgentReconnect = originalWait
	})
	agentHeartbeatInterval = time.Hour
	agentMetricsInterval = time.Hour
	waitAgentReconnect = func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil }

	var getCount atomic.Int32
	var connectionCount atomic.Int32
	firstReported := make(chan struct{})
	secondConnected := make(chan struct{})
	results := make(chan configResult, 2)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/config":
			getCount.Add(1)
			_ = json.NewEncoder(w).Encode(desiredState{Version: 1})
		case "/api/agent/config/result":
			var result configResult
			_ = json.NewDecoder(r.Body).Decode(&result)
			results <- result
			select {
			case <-firstReported:
			default:
				close(firstReported)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/api/agent/ws":
			connection, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer connection.CloseNow()
			if _, _, err := connection.Read(context.Background()); err != nil {
				return
			}
			if connectionCount.Add(1) == 1 {
				<-firstReported
				_ = connection.Close(websocket.StatusNormalClosure, "reconnect test")
				return
			}
			close(secondConnected)
			<-connection.CloseRead(context.Background()).Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer panel.Close()
	configPath := writeAgentConfig(t, config{
		PanelURL: panel.URL, ServerID: 51, AgentID: 52, AgentToken: "reconnect-config-secret",
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- connectAgent(ctx, configPath) }()
	select {
	case <-secondConnected:
	case <-time.After(time.Second):
		t.Fatal("Agent did not reconnect")
	}
	deadline := time.Now().Add(time.Second)
	for getCount.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if getCount.Load() < 2 {
		t.Fatalf("config GET count after reconnect = %d, want at least 2", getCount.Load())
	}
	select {
	case duplicate := <-results:
		first := duplicate
		select {
		case duplicate = <-results:
			t.Fatalf("reconnect repeated successful config result: first %+v, duplicate %+v", first, duplicate)
		default:
		}
	default:
		t.Fatal("initial config result was not reported")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("connectAgent() cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("connectAgent() did not stop after cancellation")
	}
}

func waitForConfigResult(t *testing.T, results <-chan configResult, handlerErrors <-chan error) configResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case err := <-handlerErrors:
		t.Fatalf("Panel handler error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("Agent did not report config result")
	}
	return configResult{}
}
