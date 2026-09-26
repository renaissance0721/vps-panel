package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/diagnostic"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestDiagnosticsAPIRejectsOfflineAndUnsupportedAgents(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	servers := serverstore.NewService(db)
	created, err := servers.Create(t.Context(), "Diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v0.20.0", false)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialized.Result().Cookies()[0]
	path := "/api/servers/" + strconv.FormatInt(created.ID, 10) + "/diagnostics"
	offline := performRequest(t, handler, http.MethodPost, path, nil, cookie)
	if offline.Code != http.StatusConflict || !strings.Contains(offline.Body.String(), "Agent 当前不在线") {
		t.Fatalf("offline diagnostics = %d, %s", offline.Code, offline.Body.String())
	}

	panel := httptest.NewServer(handler)
	defer panel.Close()
	header := http.Header{"Authorization": []string{"Bearer " + registered.Token}}
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("connect old Agent: %v, %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, servers, created.ID, serverstore.StatusOnline)
	unsupported := performRequest(t, handler, http.MethodPost, path, nil, cookie)
	if unsupported.Code != http.StatusConflict || !strings.Contains(unsupported.Body.String(), "当前 Agent 不支持一键诊断，请升级 Agent。") {
		t.Fatalf("unsupported diagnostics = %d, %s", unsupported.Code, unsupported.Body.String())
	}
}

func TestDiagnosticsAPIMatchesRequestAndBuildsPanelChecks(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	servers := serverstore.NewService(db)
	created, err := servers.Create(t.Context(), "Diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgent(t.Context(), created.EnrollmentToken, "v0.20.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agents SET applied_config_version = 1, config_sync_status = 'success' WHERE id = ?`, registered.ID); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialized.Result().Cookies()[0]
	panel := httptest.NewServer(handler)
	defer panel.Close()
	header := http.Header{"Authorization": []string{"Bearer " + registered.Token}}
	header.Set("X-VPS-Panel-Agent-Capabilities", diagnostic.CapabilityV1)
	connection, response, err := websocket.Dial(t.Context(), panel.URL+"/api/agent/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("connect Agent: %v, %+v", err, response)
	}
	defer connection.CloseNow()
	waitForServerStatus(t, servers, created.ID, serverstore.StatusOnline)

	unknownID, _ := diagnostic.NewRequestID()
	unknown, _ := diagnostic.EncodeResult(diagnostic.Result{
		RequestID: unknownID, StartedAt: time.Now().Unix(), Checks: []diagnostic.Check{{
			Code: "xray.service", Status: diagnostic.StatusPass, Detail: "ignored",
		}},
	})
	if err := connection.Write(t.Context(), websocket.MessageText, unknown); err != nil {
		t.Fatal(err)
	}

	responseBody := make(chan struct {
		status int
		body   []byte
		err    error
	}, 1)
	go func() {
		request, requestErr := http.NewRequest(
			http.MethodPost, panel.URL+"/api/servers/"+strconv.FormatInt(created.ID, 10)+"/diagnostics", nil,
		)
		if requestErr != nil {
			responseBody <- struct {
				status int
				body   []byte
				err    error
			}{err: requestErr}
			return
		}
		request.AddCookie(cookie)
		request.Header.Set("Origin", panel.URL)
		response, requestErr := panel.Client().Do(request)
		if requestErr != nil {
			responseBody <- struct {
				status int
				body   []byte
				err    error
			}{err: requestErr}
			return
		}
		defer response.Body.Close()
		body, requestErr := io.ReadAll(response.Body)
		responseBody <- struct {
			status int
			body   []byte
			err    error
		}{status: response.StatusCode, body: body, err: requestErr}
	}()

	messageType, message, err := connection.Read(t.Context())
	var request diagnostic.Request
	if err != nil || messageType != websocket.MessageText || json.Unmarshal(message, &request) != nil ||
		request.Type != "diagnostic_request" || !diagnostic.ValidRequestID(request.RequestID) || request.RequestID == unknownID {
		t.Fatalf("diagnostic request = %q, %v", message, err)
	}
	payload, err := diagnostic.EncodeResult(diagnostic.Result{
		RequestID: request.RequestID, StartedAt: time.Now().Unix(), DurationMS: 5,
		Checks: []diagnostic.Check{{Code: "xray.service", Status: diagnostic.StatusPass, Detail: "Xray service 正在运行"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(t.Context(), websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
	result := <-responseBody
	if result.err != nil || result.status != http.StatusOK {
		t.Fatalf("diagnostics response = %d, %s, %v", result.status, result.body, result.err)
	}
	var report serverDiagnosticResponse
	if err := json.Unmarshal(result.body, &report); err != nil {
		t.Fatal(err)
	}
	if report.ServerID != created.ID || report.StartedAt.IsZero() || report.DurationMS < 0 ||
		findCheck(report.Checks, "agent.connected") == nil ||
		findCheck(report.Checks, "config.version").Status != diagnostic.StatusPass ||
		findCheck(report.Checks, "config.sync").Status != diagnostic.StatusPass ||
		findCheck(report.Checks, "xray.service") == nil ||
		findCheck(report.Checks, "protocol.end_to_end").Status != diagnostic.StatusSkipped {
		t.Fatalf("diagnostic report = %+v", report)
	}
}

func TestPanelEntryProbeAndConfigFailureChecks(t *testing.T) {
	dial := func(ctx context.Context, endpoint string) (net.Conn, error) {
		return dialPanelEntryWith(ctx, endpoint, func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.New("unexpected DNS lookup")
		}, func(_ context.Context, _, endpoint string) (net.Conn, error) {
			if endpoint != "1.1.1.1:443" {
				return nil, errors.New("connection refused")
			}
			connection, peer := net.Pipe()
			_ = peer.Close()
			return connection, nil
		})
	}
	checks := probePanelEntries(t.Context(), []panelEntry{
		{resourceID: 1, label: "reachable", host: "1.1.1.1", port: 443, protocol: "tcp"},
		{resourceID: 2, label: "failed", host: "8.8.8.8", port: 443, protocol: "tcp"},
		{resourceID: 3, label: "blocked", host: "127.0.0.1", port: 22, protocol: "tcp"},
		{resourceID: 4, label: "udp", host: "1.1.1.1", port: 53, protocol: "udp"},
	}, dial)
	if checks[0].Status != diagnostic.StatusPass || checks[0].LatencyMS == nil ||
		checks[1].Status != diagnostic.StatusFail || checks[2].Status != diagnostic.StatusSkipped ||
		checks[2].Endpoint != "" || strings.Contains(checks[2].Detail, "127.0.0.1") ||
		checks[3].Status != diagnostic.StatusSkipped {
		t.Fatalf("Panel entry checks = %+v", checks)
	}
	if !strings.Contains(safePanelProbeError(context.DeadlineExceeded), "timeout") {
		t.Fatal("Panel timeout error was not normalized")
	}
	status := agentcontrol.DiagnosticStatus{
		DesiredVersion: 142, AppliedVersion: 141,
		SyncStatus: agentcontrol.ConfigSyncFailed, SyncError: "safe failure\n" + strings.Repeat("x", 600),
	}
	if diagnosticVersionCheck(status).Status != diagnostic.StatusFail ||
		diagnosticSyncCheck(status).Status != diagnostic.StatusFail ||
		len(diagnosticSyncCheck(status).Detail) > diagnostic.MaxDetailBytes {
		t.Fatalf("config failure checks = %+v, %+v", diagnosticVersionCheck(status), diagnosticSyncCheck(status))
	}
}

func TestSafePanelDialRejectsNonPublicLiteralAddresses(t *testing.T) {
	tests := []struct {
		address string
		allowed bool
	}{
		{"0.0.0.0", false},
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.1.1", false},
		{"100.64.0.1", false},
		{"224.0.0.1", false},
		{"::", false},
		{"::1", false},
		{"fc00::1", false},
		{"fe80::1", false},
		{"ff02::1", false},
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			dialed := ""
			connection, err := dialPanelEntryWith(
				t.Context(), net.JoinHostPort(test.address, "443"),
				func(context.Context, string) ([]netip.Addr, error) {
					t.Fatal("literal IP triggered DNS resolution")
					return nil, nil
				},
				func(_ context.Context, _, endpoint string) (net.Conn, error) {
					dialed = endpoint
					client, peer := net.Pipe()
					_ = peer.Close()
					return client, nil
				},
			)
			if test.allowed {
				if err != nil || dialed != net.JoinHostPort(test.address, "443") {
					t.Fatalf("allowed address dial = %q, %v", dialed, err)
				}
				_ = connection.Close()
				return
			}
			if !errors.Is(err, errUnsafePanelDiagnosticTarget) || dialed != "" || connection != nil {
				t.Fatalf("blocked address dial = (%v, %q, %v)", connection, dialed, err)
			}
		})
	}
}

func TestSafePanelDialRejectsUnsafeDNSAndPinsPublicAddress(t *testing.T) {
	resolved := map[string][]netip.Addr{
		"localhost.example": {netip.MustParseAddr("127.0.0.1")},
		"private.example":   {netip.MustParseAddr("10.0.0.1")},
		"mixed.example":     {netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("10.0.0.1")},
		"public.example":    {netip.MustParseAddr("1.1.1.1")},
	}
	resolve := func(_ context.Context, host string) ([]netip.Addr, error) {
		return resolved[host], nil
	}
	for _, host := range []string{"localhost.example", "private.example", "mixed.example"} {
		t.Run(host, func(t *testing.T) {
			dialed := false
			_, err := dialPanelEntryWith(t.Context(), host+":443", resolve, func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("unexpected dial")
			})
			if !errors.Is(err, errUnsafePanelDiagnosticTarget) || dialed {
				t.Fatalf("unsafe DNS result = %v, dialed=%t", err, dialed)
			}
		})
	}

	var dialedEndpoint string
	connection, err := dialPanelEntryWith(t.Context(), "public.example:443", resolve, func(_ context.Context, network, endpoint string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("network = %q", network)
		}
		dialedEndpoint = endpoint
		client, peer := net.Pipe()
		_ = peer.Close()
		return client, nil
	})
	if err != nil || dialedEndpoint != "1.1.1.1:443" || strings.Contains(dialedEndpoint, "public.example") {
		t.Fatalf("public DNS dial = %q, %v", dialedEndpoint, err)
	}
	_ = connection.Close()
}

func TestPanelEntryProbeUsesOnlySuppliedServerResources(t *testing.T) {
	called := false
	check := probePanelEntry(t.Context(), panelEntry{resourceID: 1, label: "missing", protocol: "tcp"}, func(context.Context, string) (net.Conn, error) {
		called = true
		return nil, errors.New("unexpected")
	})
	if called || check.Status != diagnostic.StatusSkipped || check.Endpoint != "" {
		t.Fatalf("missing entry probe = %+v, called=%v", check, called)
	}
}

func TestDiagnosticLimitReservesProtocolResult(t *testing.T) {
	checks := make([]diagnostic.Check, diagnostic.MaxChecks)
	limited := limitDiagnosticChecks(checks, diagnostic.MaxChecks-1)
	limited = append(limited, diagnostic.Check{Code: "protocol.end_to_end", Status: diagnostic.StatusSkipped})
	if len(limited) != diagnostic.MaxChecks || limited[len(limited)-2].Code != "diagnostic.truncated" ||
		limited[len(limited)-1].Code != "protocol.end_to_end" {
		t.Fatalf("limited checks = %+v", limited)
	}
}

func TestFilterAgentDiagnosticChecksRequiresVisibleResourceIDs(t *testing.T) {
	visibleProxyID, hiddenRelayID := int64(1), int64(99)
	checks := filterAgentDiagnosticChecks([]diagnostic.Check{
		{Code: "xray.service", Status: diagnostic.StatusPass},
		{Code: "agent.connected", Status: diagnostic.StatusFail},
		{Code: "relay.target_tcp", Status: diagnostic.StatusPass, Endpoint: "hidden.example:443"},
		{Code: "xray.listener", Status: diagnostic.StatusPass, ResourceID: &visibleProxyID},
		{Code: "relay.dns", Status: diagnostic.StatusPass, ResourceID: &hiddenRelayID, Endpoint: "hidden.example"},
	}, map[int64]string{visibleProxyID: "visible"}, map[int64]string{})
	if len(checks) != 2 || checks[0].Code != "xray.service" || checks[1].Code != "xray.listener" ||
		checks[1].Label != "visible" {
		t.Fatalf("filtered Agent checks = %+v", checks)
	}
}

func findCheck(checks []diagnostic.Check, code string) *diagnostic.Check {
	for index := range checks {
		if checks[index].Code == code {
			return &checks[index]
		}
	}
	return nil
}
