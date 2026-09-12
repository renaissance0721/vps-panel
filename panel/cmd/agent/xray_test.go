package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagedXrayPinnedOfficialAssets(t *testing.T) {
	if managedXrayVersion != "v26.3.27" {
		t.Fatalf("managed Xray version = %q", managedXrayVersion)
	}
	want := map[string]managedXrayAsset{
		"amd64": {"Xray-linux-64.zip", "23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae"},
		"arm64": {"Xray-linux-arm64-v8a.zip", "4d30283ae614e3057f730f67cd088a42be6fdf91f8639d82cb69e48cde80413c"},
	}
	for architecture, expected := range want {
		if got := managedXrayAssets[architecture]; got != expected {
			t.Fatalf("asset %s = %+v, want %+v", architecture, got, expected)
		}
	}
}

func TestManagedXrayDisabledBeforeInstallDoesNothing(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("disabled Xray attempted a download")
		return nil, errors.New("unexpected download")
	})}

	if err := manager.apply(t.Context(), desiredState{}); err != nil {
		t.Fatalf("apply disabled Xray: %v", err)
	}
	if commands.count("systemctl", "disable") != 0 {
		t.Fatal("disabled uninstalled Xray changed systemd")
	}
}

func TestManagedXrayInstallsVerifiedArchiveAndStarts(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	archive := makeXrayArchive(t, []byte("test xray binary"))
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		if r.URL.Path != "/"+managedXrayVersion+"/Xray-linux-64.zip" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	manager.releaseBaseURL = server.URL
	manager.client = server.Client()
	manager.assets = map[string]managedXrayAsset{
		"amd64": {name: "Xray-linux-64.zip", sha256: checksum(archive)},
	}

	if err := manager.apply(t.Context(), enabledXrayState()); err != nil {
		t.Fatalf("apply enabled Xray: %v", err)
	}
	if downloads.Load() != 1 {
		t.Fatalf("download count = %d, want 1", downloads.Load())
	}
	if got, err := os.ReadFile(manager.binaryPath); err != nil || string(got) != "test xray binary" {
		t.Fatalf("installed binary = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(manager.binaryPath); err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("binary mode = %v, %v", infoMode(info), err)
		}
		if info, err := os.Stat(manager.configPath); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("config mode = %v, %v", infoMode(info), err)
		}
	}
	if !commands.active || commands.count("systemctl", "enable") != 1 || commands.count("systemctl", "restart") != 1 {
		t.Fatalf("systemd calls = %v, active = %v", commands.calls, commands.active)
	}
}

func TestManagedXrayRejectsBadChecksumBeforeExecution(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	archive := makeXrayArchive(t, []byte("must not execute"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	manager.releaseBaseURL = server.URL
	manager.client = server.Client()
	manager.assets = map[string]managedXrayAsset{
		"amd64": {name: "Xray-linux-64.zip", sha256: strings.Repeat("0", 64)},
	}

	err := manager.ensureManagedXray(t.Context())
	if !errors.Is(err, errManagedXrayChecksum) {
		t.Fatalf("ensure managed Xray error = %v", err)
	}
	if len(commands.calls) != 0 {
		t.Fatalf("checksum failure executed commands: %v", commands.calls)
	}
	if _, err := os.Stat(manager.binaryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("binary exists after checksum failure: %v", err)
	}
}

func TestManagedXrayRejectsUnsupportedArchitecture(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	manager.goarch = "riscv64"
	err := manager.ensureManagedXray(t.Context())
	if !errors.Is(err, errManagedXrayArch) {
		t.Fatalf("unsupported architecture error = %v", err)
	}
	if len(commands.calls) != 0 {
		t.Fatalf("unsupported architecture executed commands: %v", commands.calls)
	}
}

func TestManagedXrayReusesCorrectBinaryAndRedownloadsInvalidBinary(t *testing.T) {
	t.Run("correct managed binary", func(t *testing.T) {
		manager, commands := newTestXrayManager(t)
		seedManagedXray(t, manager, []byte("existing binary"))
		manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("correct managed binary was downloaded again")
			return nil, errors.New("unexpected download")
		})}
		if err := manager.ensureManagedXray(t.Context()); err != nil {
			t.Fatalf("reuse managed binary: %v", err)
		}
		if commands.countVersion() != 1 {
			t.Fatalf("version checks = %d, want 1", commands.countVersion())
		}
	})

	t.Run("invalid managed binary is not accepted", func(t *testing.T) {
		manager, commands := newTestXrayManager(t)
		seedManagedXray(t, manager, []byte("broken binary"))
		commands.versionOutput = "not Xray"
		manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("replacement unavailable")
		})}
		err := manager.ensureManagedXray(t.Context())
		if err == nil || !errors.Is(err, errManagedXrayDownload) {
			t.Fatalf("invalid binary error = %v", err)
		}
	})
}

func TestManagedXrayRefusesUnmanagedService(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	unmanagedUnit := manager.unmanagedUnits[0]
	if err := os.MkdirAll(filepath.Dir(unmanagedUnit), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte("user-owned unit\n")
	if err := os.WriteFile(unmanagedUnit, want, 0o644); err != nil {
		t.Fatal(err)
	}
	err := manager.ensureManagedXray(t.Context())
	if !errors.Is(err, errManagedXrayConflict) {
		t.Fatalf("unmanaged service error = %v", err)
	}
	if got, _ := os.ReadFile(unmanagedUnit); !bytes.Equal(got, want) {
		t.Fatalf("unmanaged unit changed to %q", got)
	}
	if len(commands.calls) != 0 {
		t.Fatalf("unmanaged service caused commands: %v", commands.calls)
	}
}

func TestManagedXrayBaseConfigIsStableAndUnitUsesFixedCommand(t *testing.T) {
	first := renderManagedXrayBaseConfig()
	second := renderManagedXrayBaseConfig()
	if !bytes.Equal(first, second) || !bytes.Contains(first, []byte(`"inbounds": []`)) ||
		!bytes.Contains(first, []byte(`"protocol": "freedom"`)) {
		t.Fatalf("base config = %s", first)
	}
	manager, _ := newTestXrayManager(t)
	changed, err := manager.ensureUnit()
	if err != nil || !changed {
		t.Fatalf("ensure unit = (%v, %v)", changed, err)
	}
	unit, err := os.ReadFile(manager.unitPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "ExecStart=/opt/vps-panel/xray/xray run -config /etc/vps-panel/xray/config.json"
	if !strings.Contains(string(unit), want) || strings.Contains(string(unit), "sh -c") {
		t.Fatalf("unit = %s", unit)
	}
}

func TestManagedXrayValidationFailureKeepsCurrentConfig(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	old := []byte("old successful config\n")
	writeTestFile(t, manager.configPath, old, 0o600)
	commands.failValidation = true

	err := manager.apply(t.Context(), enabledXrayState())
	if !errors.Is(err, errManagedXrayValidation) {
		t.Fatalf("validation error = %v", err)
	}
	if got, _ := os.ReadFile(manager.configPath); !bytes.Equal(got, old) {
		t.Fatalf("current config changed to %q", got)
	}
	if commands.count("systemctl", "restart") != 0 {
		t.Fatalf("validation failure restarted service: %v", commands.calls)
	}
}

func TestManagedXraySameConfigAvoidsRestartAndRepairsInactiveService(t *testing.T) {
	for _, test := range []struct {
		name         string
		active       bool
		wantStarts   int
		wantRestarts int
	}{
		{name: "active", active: true},
		{name: "inactive", active: false, wantStarts: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, commands := newTestXrayManager(t)
			seedManagedXray(t, manager, []byte("binary"))
			writeTestFile(t, manager.configPath, renderManagedXrayBaseConfig(), 0o600)
			commands.active = test.active
			if err := manager.apply(t.Context(), enabledXrayState()); err != nil {
				t.Fatalf("apply same config: %v", err)
			}
			if got := commands.count("systemctl", "start"); got != test.wantStarts {
				t.Fatalf("start calls = %d, want %d (%v)", got, test.wantStarts, commands.calls)
			}
			if got := commands.count("systemctl", "restart"); got != test.wantRestarts {
				t.Fatalf("restart calls = %d, want %d (%v)", got, test.wantRestarts, commands.calls)
			}
		})
	}
}

func TestManagedXraySameConfigActiveAndHealthyAvoidsRestart(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	proxy := testDesiredTLSProxy()
	state := desiredState{Xray: desiredXrayState{Enabled: true, Proxies: []desiredProxy{proxy}}}
	config, err := renderManagedXrayConfig(state.Xray.Proxies)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, manager.configPath, config, 0o600)
	commands.active = true
	var probed []int
	manager.probeListener = func(_ context.Context, port int) error {
		probed = append(probed, port)
		return nil
	}

	if err := manager.apply(t.Context(), state); err != nil {
		t.Fatalf("apply healthy same config: %v", err)
	}
	if commands.count("systemctl", "start") != 0 || commands.count("systemctl", "restart") != 0 || len(probed) != 1 || probed[0] != proxy.Port {
		t.Fatalf("healthy no-op calls = %v, probed = %v", commands.calls, probed)
	}
}

func TestManagedXraySameConfigMissingListenerRestartsAndFails(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	proxy := testDesiredTLSProxy()
	state := desiredState{Xray: desiredXrayState{Enabled: true, Proxies: []desiredProxy{proxy}}}
	config, err := renderManagedXrayConfig(state.Xray.Proxies)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, manager.configPath, config, 0o600)
	commands.active = true
	manager.probeListener = func(context.Context, int) error { return errors.New("not listening") }

	err = manager.apply(t.Context(), state)
	if !errors.Is(err, errManagedXrayHealth) || commands.count("systemctl", "restart") != 1 {
		t.Fatalf("missing listener error = %v, calls = %v", err, commands.calls)
	}
}

func TestManagedXrayNewConfigSavesPreviousAndStarts(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	old := []byte("old successful config\n")
	writeTestFile(t, manager.configPath, old, 0o600)
	if err := manager.apply(t.Context(), enabledXrayState()); err != nil {
		t.Fatalf("apply new config: %v", err)
	}
	assertFileEquals(t, manager.previousPath, old)
	assertFileEquals(t, manager.configPath, renderManagedXrayBaseConfig())
	if commands.count("systemctl", "restart") != 1 || !commands.active {
		t.Fatalf("systemd calls = %v, active = %v", commands.calls, commands.active)
	}
}

func TestManagedXrayRemovedProxyAppliesOnlyRemainingPort(t *testing.T) {
	manager, _ := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	removed := testDesiredTLSProxy()
	remaining := testDesiredTLSProxy()
	remaining.ID = 2
	remaining.Port = 8443
	remaining.Clients[0].ID = 2
	remaining.Clients[0].UUID = "123e4567-e89b-42d3-a456-426614174001"
	oldConfig, err := renderManagedXrayConfig([]desiredProxy{removed, remaining})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, manager.configPath, oldConfig, 0o600)
	var probed, firewallPorts []int
	manager.probeListener = func(_ context.Context, port int) error {
		probed = append(probed, port)
		return nil
	}
	manager.reconcileFirewall = func(_ context.Context, ports []int) error {
		firewallPorts = append([]int(nil), ports...)
		return nil
	}

	state := desiredState{Xray: desiredXrayState{Enabled: true, Proxies: []desiredProxy{remaining}}}
	if err := manager.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(manager.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var rendered renderedXrayConfig
	if err := json.Unmarshal(current, &rendered); err != nil {
		t.Fatal(err)
	}
	if len(rendered.Inbounds) != 1 || rendered.Inbounds[0].Port != remaining.Port ||
		strings.Contains(string(current), removed.Clients[0].UUID) {
		t.Fatalf("rendered config after Proxy removal = %s", current)
	}
	if len(probed) != 2 || probed[0] != remaining.Port || probed[1] != remaining.Port ||
		len(firewallPorts) != 1 || firewallPorts[0] != remaining.Port {
		t.Fatalf("removed Proxy health/firewall = probed %v, firewall %v", probed, firewallPorts)
	}
}

func TestManagedXrayMissingListenerRollsBackNewConfig(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	oldProxy := testDesiredTLSProxy()
	oldProxy.Port = 8443
	oldConfig, err := renderManagedXrayConfig([]desiredProxy{oldProxy})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, manager.configPath, oldConfig, 0o600)
	manager.probeListener = func(_ context.Context, port int) error {
		if port == oldProxy.Port {
			return nil
		}
		return errors.New("not listening")
	}

	err = manager.apply(t.Context(), desiredState{Xray: desiredXrayState{Enabled: true, Proxies: []desiredProxy{testDesiredTLSProxy()}}})
	if !errors.Is(err, errManagedXrayHealth) {
		t.Fatalf("missing listener error = %v", err)
	}
	assertFileEquals(t, manager.configPath, oldConfig)
	if !commands.active || commands.count("systemctl", "restart") != 2 {
		t.Fatalf("rollback systemd state = %v, calls %v", commands.active, commands.calls)
	}
}

func TestManagedXrayFirewallFailureRollsBackNewConfig(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	oldProxy := testDesiredTLSProxy()
	oldProxy.Port = 8443
	old, err := renderManagedXrayConfig([]desiredProxy{oldProxy})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, manager.configPath, old, 0o600)
	var firewallCalls [][]int
	manager.reconcileFirewall = func(_ context.Context, ports []int) error {
		firewallCalls = append(firewallCalls, append([]int(nil), ports...))
		if len(firewallCalls) == 1 {
			return errManagedProxyFirewall
		}
		return nil
	}

	err = manager.apply(t.Context(), desiredState{Xray: desiredXrayState{Enabled: true, Proxies: []desiredProxy{testDesiredTLSProxy()}}})
	if !errors.Is(err, errManagedProxyFirewall) {
		t.Fatalf("firewall failure error = %v", err)
	}
	assertFileEquals(t, manager.configPath, old)
	if !commands.active || commands.count("systemctl", "restart") != 2 {
		t.Fatalf("rollback systemd state = %v, calls %v", commands.active, commands.calls)
	}
	if len(firewallCalls) != 2 || len(firewallCalls[0]) != 1 || firewallCalls[0][0] != 443 ||
		len(firewallCalls[1]) != 1 || firewallCalls[1][0] != oldProxy.Port {
		t.Fatalf("firewall rollback calls = %v", firewallCalls)
	}
}

func TestManagedXrayRestartFailureRollsBackButStillFails(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	old := []byte("old successful config\n")
	writeTestFile(t, manager.configPath, old, 0o600)
	commands.restartOutcomes = []bool{false, true}

	err := manager.apply(t.Context(), enabledXrayState())
	if !errors.Is(err, errManagedXrayStart) {
		t.Fatalf("restart failure error = %v", err)
	}
	assertFileEquals(t, manager.configPath, old)
	if !commands.active || commands.count("systemctl", "restart") != 2 {
		t.Fatalf("rollback systemd state = %v, calls %v", commands.active, commands.calls)
	}
}

func TestManagedXrayFirstStartFailureRemovesCurrentAndStops(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	commands.restartOutcomes = []bool{false}

	err := manager.apply(t.Context(), enabledXrayState())
	if !errors.Is(err, errManagedXrayStart) {
		t.Fatalf("first start error = %v", err)
	}
	if _, err := os.Stat(manager.configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed current config remains: %v", err)
	}
	if commands.active || commands.count("systemctl", "disable") != 1 {
		t.Fatalf("failed first service state = %v, calls %v", commands.active, commands.calls)
	}
}

func TestManagedXrayRollbackFailureDoesNotPanic(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	old := []byte("old successful config\n")
	writeTestFile(t, manager.configPath, old, 0o600)
	commands.restartOutcomes = []bool{false, false}

	if err := manager.apply(t.Context(), enabledXrayState()); !errors.Is(err, errManagedXrayStart) {
		t.Fatalf("rollback failure error = %v", err)
	}
	assertFileEquals(t, manager.configPath, old)
}

func TestManagedXrayDisableCleansRuntimeFilesAndReenableAvoidsDownload(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	writeTestFile(t, manager.configPath, renderManagedXrayBaseConfig(), 0o600)
	writeTestFile(t, manager.previousPath, []byte("previous\n"), 0o600)
	writeTestFile(t, manager.unitPath, []byte("unit\n"), 0o644)
	commands.active = true
	var firewallCalls [][]int
	manager.reconcileFirewall = func(_ context.Context, ports []int) error {
		firewallCalls = append(firewallCalls, append([]int(nil), ports...))
		return nil
	}

	if err := manager.apply(t.Context(), desiredState{}); err != nil {
		t.Fatalf("disable Xray: %v", err)
	}
	for _, path := range []string{manager.binaryPath, manager.markerPath, manager.unitPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("disabled Xray removed %s: %v", path, err)
		}
	}
	for _, path := range []string{manager.configPath, manager.previousPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("disabled Xray retained %s: %v", path, err)
		}
	}
	if len(firewallCalls) != 1 || len(firewallCalls[0]) != 0 {
		t.Fatalf("disable firewall calls = %v", firewallCalls)
	}
	manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("re-enable downloaded correct managed binary")
		return nil, errors.New("unexpected download")
	})}
	if err := manager.apply(t.Context(), enabledXrayState()); err != nil {
		t.Fatalf("re-enable Xray: %v", err)
	}
	if !commands.active || commands.count("systemctl", "restart") != 1 {
		t.Fatalf("re-enabled service = %v, calls %v", commands.active, commands.calls)
	}
	assertFileEquals(t, manager.configPath, renderManagedXrayBaseConfig())
}

func TestManagedXrayDisableFailureKeepsRuntimeFiles(t *testing.T) {
	manager, commands := newTestXrayManager(t)
	seedManagedXray(t, manager, []byte("binary"))
	writeTestFile(t, manager.configPath, []byte("current\n"), 0o600)
	writeTestFile(t, manager.previousPath, []byte("previous\n"), 0o600)
	commands.active = true
	commands.failDisable = true
	firewallCalled := false
	manager.reconcileFirewall = func(context.Context, []int) error {
		firewallCalled = true
		return nil
	}

	err := manager.apply(t.Context(), desiredState{})
	if !errors.Is(err, errManagedXrayStop) {
		t.Fatalf("disable failure error = %v", err)
	}
	assertFileEquals(t, manager.configPath, []byte("current\n"))
	assertFileEquals(t, manager.previousPath, []byte("previous\n"))
	if firewallCalled {
		t.Fatal("failed disable reconciled firewall")
	}
}

func TestManagedXrayRejectsUnsupportedDesiredStates(t *testing.T) {
	manager, _ := newTestXrayManager(t)
	tests := []desiredState{
		{Xray: desiredXrayState{Enabled: true, Proxies: []desiredProxy{{Protocol: "unsupported"}}}},
		{Realm: desiredRealmState{Enabled: true}},
		{Realm: desiredRealmState{Relays: []json.RawMessage{[]byte(`{}`)}}},
	}
	for index, state := range tests {
		if err := manager.apply(t.Context(), state); err == nil || err.Error() != unsupportedManagedConfigMessage {
			t.Fatalf("unsupported state %d error = %v", index, err)
		}
	}
}

func enabledXrayState() desiredState {
	return desiredState{Xray: desiredXrayState{Enabled: true}}
}

func newTestXrayManager(t *testing.T) (*xrayManager, *xrayCommandRecorder) {
	t.Helper()
	root := t.TempDir()
	commands := &xrayCommandRecorder{versionOutput: "Xray " + strings.TrimPrefix(managedXrayVersion, "v") + " test"}
	manager := &xrayManager{
		installDir:        filepath.Join(root, "opt", "vps-panel", "xray"),
		binaryPath:        filepath.Join(root, "opt", "vps-panel", "xray", "xray"),
		markerPath:        filepath.Join(root, "opt", "vps-panel", "xray", ".managed-by-vps-panel"),
		configDir:         filepath.Join(root, "etc", "vps-panel", "xray"),
		configPath:        filepath.Join(root, "etc", "vps-panel", "xray", "config.json"),
		previousPath:      filepath.Join(root, "etc", "vps-panel", "xray", "config.previous.json"),
		unitPath:          filepath.Join(root, "etc", "systemd", "system", "vps-panel-xray.service"),
		serviceName:       managedXrayServiceName,
		unmanagedUnits:    []string{filepath.Join(root, "etc", "systemd", "system", "xray.service")},
		goos:              "linux",
		goarch:            "amd64",
		releaseBaseURL:    "http://127.0.0.1:1",
		assets:            managedXrayAssets,
		client:            &http.Client{Timeout: time.Second},
		runCommand:        commands.run,
		probeListener:     func(context.Context, int) error { return nil },
		reconcileFirewall: func(context.Context, []int) error { return nil },
		wait:              func(context.Context, time.Duration) error { return nil },
		healthAttempts:    2,
		healthCheckDelay:  0,
	}
	return manager, commands
}

type xrayCommandRecorder struct {
	mu              sync.Mutex
	calls           [][]string
	active          bool
	failValidation  bool
	failDisable     bool
	versionOutput   string
	restartOutcomes []bool
}

func (r *xrayCommandRecorder) run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	call := append([]string{name}, arguments...)
	r.calls = append(r.calls, call)
	if len(arguments) == 1 && arguments[0] == "version" {
		return []byte(r.versionOutput), nil
	}
	if name != "systemctl" {
		if r.failValidation {
			return []byte("invalid candidate"), errors.New("config test failed")
		}
		return []byte("Configuration OK."), nil
	}
	if len(arguments) == 0 {
		return nil, errors.New("missing systemctl command")
	}
	switch arguments[0] {
	case "restart":
		if len(r.restartOutcomes) != 0 {
			success := r.restartOutcomes[0]
			r.restartOutcomes = r.restartOutcomes[1:]
			r.active = success
			if !success {
				return []byte("restart failed"), errors.New("restart failed")
			}
		} else {
			r.active = true
		}
	case "start":
		r.active = true
	case "stop", "disable":
		if r.failDisable {
			return []byte("disable failed"), errors.New("disable failed")
		}
		r.active = false
	case "is-active":
		if !r.active {
			return []byte("inactive"), errors.New("inactive")
		}
	}
	return nil, nil
}

func (r *xrayCommandRecorder) count(name, firstArgument string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, call := range r.calls {
		if len(call) >= 2 && call[0] == name && call[1] == firstArgument {
			count++
		}
	}
	return count
}

func (r *xrayCommandRecorder) countVersion() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, call := range r.calls {
		if len(call) == 2 && call[1] == "version" {
			count++
		}
	}
	return count
}

func seedManagedXray(t *testing.T, manager *xrayManager, binary []byte) {
	t.Helper()
	writeTestFile(t, manager.markerPath, []byte("managed by vps-panel\n"), 0o644)
	writeTestFile(t, manager.binaryPath, binary, 0o755)
}

func writeTestFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func assertFileEquals(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func makeXrayArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, err := archive.Create("xray")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func checksum(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode().Perm()
}
