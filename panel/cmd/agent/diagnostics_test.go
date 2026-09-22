package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/diagnostic"
)

type diagnosticServiceManager struct {
	active        bool
	activeErr     error
	mutationCalls int
}

func (*diagnosticServiceManager) Path() string { return "" }
func (m *diagnosticServiceManager) Install() (bool, error) {
	m.mutationCalls++
	return false, nil
}
func (m *diagnosticServiceManager) DaemonReload(context.Context) error { m.mutationCalls++; return nil }
func (m *diagnosticServiceManager) Enable(context.Context) error       { m.mutationCalls++; return nil }
func (m *diagnosticServiceManager) Disable(context.Context) error      { m.mutationCalls++; return nil }
func (m *diagnosticServiceManager) Start(context.Context) error        { m.mutationCalls++; return nil }
func (m *diagnosticServiceManager) Stop(context.Context) error         { m.mutationCalls++; return nil }
func (m *diagnosticServiceManager) Restart(context.Context) error      { m.mutationCalls++; return nil }
func (m *diagnosticServiceManager) IsActive(context.Context) (bool, error) {
	return m.active, m.activeErr
}

func TestXrayDiagnosticsAreReadOnlyAndReportServiceConfigAndListeners(t *testing.T) {
	root := t.TempDir()
	service := &diagnosticServiceManager{active: true}
	manager := &xrayManager{
		markerPath: filepath.Join(root, "managed"), binaryPath: filepath.Join(root, "xray"),
		configPath: filepath.Join(root, "config.json"), service: service,
		runCommand:    func(context.Context, string, ...string) ([]byte, error) { return nil, nil },
		probeListener: func(context.Context, int) error { return nil },
	}
	for _, path := range []string{manager.markerPath, manager.binaryPath, manager.configPath} {
		if err := os.WriteFile(path, []byte("present"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &agentDiagnosticRunner{xray: manager}
	state := desiredXrayState{Enabled: true, Proxies: []desiredProxy{{ID: 7, Port: 443}}}
	checks := runner.diagnoseXray(t.Context(), state)
	assertDiagnosticStatus(t, checks, "xray.service", diagnostic.StatusPass)
	assertDiagnosticStatus(t, checks, "xray.config", diagnostic.StatusPass)
	assertDiagnosticStatus(t, checks, "xray.listener", diagnostic.StatusPass)
	if service.mutationCalls != 0 {
		t.Fatalf("diagnostics invoked %d service mutations", service.mutationCalls)
	}

	service.active = false
	manager.runCommand = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("invalid") }
	manager.probeListener = func(context.Context, int) error { return errors.New("closed") }
	checks = runner.diagnoseXray(t.Context(), state)
	assertDiagnosticStatus(t, checks, "xray.service", diagnostic.StatusFail)
	assertDiagnosticStatus(t, checks, "xray.config", diagnostic.StatusFail)
	assertDiagnosticStatus(t, checks, "xray.listener", diagnostic.StatusFail)
	if service.mutationCalls != 0 {
		t.Fatalf("failed diagnostics invoked %d service mutations", service.mutationCalls)
	}
}

func TestRealmDiagnosticsReportTCPAndUDPListenersWithoutMutation(t *testing.T) {
	service := &diagnosticServiceManager{active: true}
	manager := &realmManager{
		service: service,
		probeTCP: func(_ context.Context, _ string, port int) error {
			if port == 9503 {
				return errors.New("closed")
			}
			return nil
		},
		probeUDP: func(_ string, port int) error {
			if port == 9504 {
				return errors.New("missing")
			}
			return nil
		},
	}
	runner := &agentDiagnosticRunner{realm: manager}
	checks := runner.diagnoseRealm(t.Context(), desiredRealmState{Enabled: true, Relays: []desiredRelay{
		{ID: 1, ListenAddress: "0.0.0.0", ListenPort: 9502, Network: "tcp,udp"},
		{ID: 2, ListenAddress: "0.0.0.0", ListenPort: 9503, Network: "tcp"},
		{ID: 3, ListenAddress: "0.0.0.0", ListenPort: 9504, Network: "udp"},
	}})
	assertDiagnosticStatus(t, checks, "realm.service", diagnostic.StatusPass)
	assertResourceDiagnosticStatus(t, checks, "realm.listener", 1, "tcp", diagnostic.StatusPass)
	assertResourceDiagnosticStatus(t, checks, "realm.listener", 1, "udp", diagnostic.StatusPass)
	assertResourceDiagnosticStatus(t, checks, "realm.listener", 2, "tcp", diagnostic.StatusFail)
	assertResourceDiagnosticStatus(t, checks, "realm.listener", 3, "udp", diagnostic.StatusFail)
	if service.mutationCalls != 0 {
		t.Fatalf("Realm diagnostics invoked %d service mutations", service.mutationCalls)
	}
}

func TestRelayTargetDiagnosticsCoverDNSConnectFailureAndUDPOnly(t *testing.T) {
	var mutex sync.Mutex
	dialed := make(map[string]int)
	runner := &agentDiagnosticRunner{
		lookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
			if host == "good.example.com" {
				return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
			}
			return nil, errors.New("not found")
		},
		dialTCP: func(_ context.Context, endpoint string) (net.Conn, error) {
			mutex.Lock()
			dialed[endpoint]++
			mutex.Unlock()
			if endpoint == "good.example.com:443" {
				client, server := net.Pipe()
				_ = server.Close()
				return client, nil
			}
			return nil, context.DeadlineExceeded
		},
	}
	checks := runner.diagnoseRelayTargets(t.Context(), []desiredRelay{
		{ID: 1, TargetHost: "good.example.com", TargetPort: 443, Network: "tcp"},
		{ID: 2, TargetHost: "bad.example.com", TargetPort: 8443, Network: "tcp"},
		{ID: 3, TargetHost: "192.0.2.5", TargetPort: 53, Network: "udp"},
	})
	assertResourceDiagnosticStatus(t, checks, "relay.dns", 1, "", diagnostic.StatusPass)
	assertResourceDiagnosticStatus(t, checks, "relay.target_tcp", 1, "tcp", diagnostic.StatusPass)
	assertResourceDiagnosticStatus(t, checks, "relay.dns", 2, "", diagnostic.StatusFail)
	assertResourceDiagnosticStatus(t, checks, "relay.target_tcp", 2, "tcp", diagnostic.StatusFail)
	assertResourceDiagnosticStatus(t, checks, "relay.dns", 3, "", diagnostic.StatusSkipped)
	assertResourceDiagnosticStatus(t, checks, "relay.target_tcp", 3, "udp", diagnostic.StatusSkipped)
	mutex.Lock()
	defer mutex.Unlock()
	if dialed["192.0.2.5:53"] != 0 {
		t.Fatal("UDP-only Relay triggered a TCP probe")
	}
}

func TestTLSDiagnosticsReportValidExpiredAndHostnameMismatch(t *testing.T) {
	now := time.Now().UTC()
	valid, _ := testACMECertificate(t, "valid.example.com", now.Add(90*24*time.Hour))
	expired, _ := testACMECertificate(t, "expired.example.com", now.Add(-time.Minute))
	mismatch, _ := testACMECertificate(t, "other.example.com", now.Add(90*24*time.Hour))
	config := map[string]any{"inbounds": []any{
		testTLSInbound(1, valid), testTLSInbound(2, expired), testTLSInbound(3, mismatch),
	}}
	value, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, value, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &agentDiagnosticRunner{xray: &xrayManager{configPath: path}, now: func() time.Time { return now }}
	checks := runner.diagnoseTLS([]desiredProxy{
		{ID: 1, Security: "tls", ServerName: "valid.example.com"},
		{ID: 2, Security: "tls", ServerName: "expired.example.com"},
		{ID: 3, Security: "tls", ServerName: "mismatch.example.com"},
	})
	assertResourceDiagnosticStatus(t, checks, "tls.certificate", 1, "", diagnostic.StatusPass)
	assertResourceDiagnosticStatus(t, checks, "tls.certificate", 2, "", diagnostic.StatusFail)
	assertResourceDiagnosticStatus(t, checks, "tls.certificate", 3, "", diagnostic.StatusFail)
	if checks[0].ExpiresAt == nil || checks[0].RemainingDays == nil {
		t.Fatalf("valid TLS check lacks expiry: %+v", checks[0])
	}
}

func testTLSInbound(id int64, certificate []byte) map[string]any {
	return map[string]any{
		"tag": "proxy-" + strconv.FormatInt(id, 10),
		"streamSettings": map[string]any{"tlsSettings": map[string]any{
			"certificates": []any{map[string]any{"certificate": strings.Split(strings.TrimSpace(string(certificate)), "\n")}},
		}},
	}
}

func assertDiagnosticStatus(t *testing.T, checks []diagnostic.Check, code, status string) {
	t.Helper()
	for _, check := range checks {
		if check.Code == code {
			if check.Status != status {
				t.Fatalf("%s status = %q, want %q (%+v)", code, check.Status, status, check)
			}
			return
		}
	}
	t.Fatalf("missing diagnostic check %s: %+v", code, checks)
}

func assertResourceDiagnosticStatus(t *testing.T, checks []diagnostic.Check, code string, resourceID int64, protocol, status string) {
	t.Helper()
	for _, check := range checks {
		if check.Code == code && check.ResourceID != nil && *check.ResourceID == resourceID && check.Protocol == protocol {
			if check.Status != status {
				t.Fatalf("%s/%d/%s status = %q, want %q", code, resourceID, protocol, check.Status, status)
			}
			return
		}
	}
	t.Fatalf("missing diagnostic check %s/%d/%s: %+v", code, resourceID, protocol, checks)
}
