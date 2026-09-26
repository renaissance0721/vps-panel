package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagedRealmPinnedOfficialAssets(t *testing.T) {
	if managedRealmVersion != "v2.9.4" {
		t.Fatalf("Realm version = %q", managedRealmVersion)
	}
	want := map[realmRuntimeTarget]managedRealmAsset{
		{goos: "linux", goarch: "amd64", libc: libcGlibc}: {"realm-x86_64-unknown-linux-gnu.tar.gz", "9dec109386b8abc828b452d0d1cecde35b7a2f8cfa93eae757fe9c248ad07ddd"},
		{goos: "linux", goarch: "arm64", libc: libcGlibc}: {"realm-aarch64-unknown-linux-gnu.tar.gz", "1f7f06e82fe0ea798b5c8e8e32906ee212a7085629a1c5cef9957ca270fcad99"},
		{goos: "linux", goarch: "amd64", libc: libcMusl}:  {"realm-x86_64-unknown-linux-musl.tar.gz", "a19b86c4ae4642d5864821b41d23633c0c91df279a88496c05834dc584169175"},
		{goos: "linux", goarch: "arm64", libc: libcMusl}:  {"realm-aarch64-unknown-linux-musl.tar.gz", "0195e77ca99713166e25ff85fefe042049c79fdaddf500e8ffd9ba77494a029c"},
	}
	for target, expected := range want {
		if managedRealmAssets[target] != expected {
			t.Fatalf("Realm asset %+v = %+v", target, managedRealmAssets[target])
		}
	}
}

func TestManagedRealmInstallsValidatesStartsAndUsesProtocolFirewall(t *testing.T) {
	manager, commands := newTestRealmManager(t)
	archive := makeRealmArchive(t, []byte("test Realm binary"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+managedRealmVersion+"/realm-x86_64-unknown-linux-gnu.tar.gz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	manager.releaseBaseURL = server.URL
	manager.client = server.Client()
	manager.assets = map[realmRuntimeTarget]managedRealmAsset{{goos: "linux", goarch: "amd64", libc: libcGlibc}: {"realm-x86_64-unknown-linux-gnu.tar.gz", realmChecksum(archive)}}
	validated := ""
	manager.validateConfig = func(_ context.Context, _, path string) error {
		validated = path
		return nil
	}
	var firewallRules []firewallRule
	manager.reconcileFirewall = func(_ context.Context, rules []firewallRule) error {
		firewallRules = append([]firewallRule(nil), rules...)
		return nil
	}
	state := desiredRealmState{Enabled: true, Relays: []desiredRelay{
		{ID: 1, ListenAddress: "0.0.0.0", ListenPort: 9502, TargetHost: "example.com", TargetPort: 443, Network: "tcp,udp"},
	}}
	if err := manager.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(validated, ".toml") || commands.count("systemctl", "restart") != 1 || !commands.active {
		t.Fatalf("validation=%q calls=%v active=%v", validated, commands.calls, commands.active)
	}
	if len(firewallRules) != 2 || firewallRules[0].protocol != "tcp" || firewallRules[1].protocol != "udp" {
		t.Fatalf("Realm firewall rules = %+v", firewallRules)
	}
	if got, err := os.ReadFile(manager.binaryPath); err != nil || string(got) != "test Realm binary" {
		t.Fatalf("installed Realm = %q, %v", got, err)
	}
	unit, err := os.ReadFile(manager.unitPath)
	if err != nil || !strings.Contains(string(unit), "ExecStart=/opt/vps-panel/realm/realm --config /etc/vps-panel/realm/config.toml") || strings.Contains(string(unit), "sh -c") {
		t.Fatalf("Realm unit = %s, %v", unit, err)
	}
}

func TestManagedRealmUsesMuslAssetAndOpenRCService(t *testing.T) {
	manager, commands := newTestRealmManager(t)
	root := filepath.Dir(filepath.Dir(filepath.Dir(manager.installDir)))
	manager.libc = libcMusl
	manager.service = newServiceManager(initSystemOpenRC, realmServiceDefinition(), root, commands.run)
	manager.unitPath = manager.service.Path()
	archive := makeRealmArchive(t, []byte("test Realm musl binary"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+managedRealmVersion+"/realm-x86_64-unknown-linux-musl.tar.gz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	manager.releaseBaseURL = server.URL
	manager.client = server.Client()
	manager.assets = map[realmRuntimeTarget]managedRealmAsset{
		{goos: "linux", goarch: "amd64", libc: libcMusl}: {"realm-x86_64-unknown-linux-musl.tar.gz", realmChecksum(archive)},
	}
	if err := manager.apply(t.Context(), testRealmState(9502, "tcp,udp")); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(manager.unitPath)
	if err != nil || !strings.Contains(string(script), `supervisor="supervise-daemon"`) ||
		!strings.Contains(string(script), `command_args="--config /etc/vps-panel/realm/config.toml"`) {
		t.Fatalf("Realm OpenRC service = %q, %v", script, err)
	}
	if commands.count("rc-update", "add") != 1 || commands.count("rc-service", managedRealmServiceName) < 2 || !commands.active {
		t.Fatalf("Realm OpenRC calls = %v, active = %v", commands.calls, commands.active)
	}
}

func TestManagedRealmRefusesUnmanagedUnitAndBadChecksum(t *testing.T) {
	manager, commands := newTestRealmManager(t)
	writeTestFile(t, manager.unmanagedUnits[0], []byte("user unit"), 0o644)
	if err := manager.ensureManagedRealm(t.Context()); !errors.Is(err, errManagedRealmConflict) {
		t.Fatalf("unmanaged conflict = %v", err)
	}
	if len(commands.calls) != 0 {
		t.Fatalf("unmanaged conflict ran commands: %v", commands.calls)
	}
	occupied, occupiedCommands := newTestRealmManager(t)
	writeTestFile(t, occupied.binaryPath, []byte("unknown binary"), 0o755)
	if err := occupied.ensureManagedRealm(t.Context()); !errors.Is(err, errManagedRealmConflict) {
		t.Fatalf("occupied managed path conflict = %v", err)
	}
	if len(occupiedCommands.calls) != 0 {
		t.Fatalf("occupied path conflict ran commands: %v", occupiedCommands.calls)
	}
	os.Remove(manager.unmanagedUnits[0])
	archive := makeRealmArchive(t, []byte("bad checksum"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	manager.releaseBaseURL = server.URL
	manager.client = server.Client()
	manager.assets = map[realmRuntimeTarget]managedRealmAsset{{goos: "linux", goarch: "amd64", libc: libcGlibc}: {"realm.tar.gz", strings.Repeat("0", 64)}}
	if err := manager.ensureManagedRealm(t.Context()); !errors.Is(err, errManagedRealmChecksum) {
		t.Fatalf("checksum error = %v", err)
	}
}

func TestManagedRealmDisableWithoutUnitStillCleansRuntimeState(t *testing.T) {
	manager, commands := newTestRealmManager(t)
	seedManagedRealm(t, manager)
	writeTestFile(t, manager.configPath, []byte("current"), 0o600)
	writeTestFile(t, manager.previousPath, []byte("previous"), 0o600)
	cleared := false
	manager.reconcileFirewall = func(_ context.Context, rules []firewallRule) error {
		cleared = len(rules) == 0
		return nil
	}
	if err := manager.apply(t.Context(), desiredRealmState{}); err != nil {
		t.Fatal(err)
	}
	if commands.count("systemctl", "disable") != 0 || !cleared {
		t.Fatalf("disable without unit calls=%v cleared=%v", commands.calls, cleared)
	}
	for _, path := range []string{manager.configPath, manager.previousPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Realm runtime file retained: %s", path)
		}
	}
}

func TestManagedRealmValidationFailureKeepsCurrentConfig(t *testing.T) {
	manager, commands := newTestRealmManager(t)
	seedManagedRealm(t, manager)
	old := []byte("old Realm config\n")
	writeTestFile(t, manager.configPath, old, 0o600)
	manager.validateConfig = func(context.Context, string, string) error { return errors.New("invalid") }
	err := manager.apply(t.Context(), testRealmState(9502, "tcp"))
	if !errors.Is(err, errManagedRealmValidation) {
		t.Fatalf("validation error = %v", err)
	}
	assertFileEquals(t, manager.configPath, old)
	if commands.count("systemctl", "restart") != 0 {
		t.Fatalf("validation failure restarted: %v", commands.calls)
	}
}

func TestManagedRealmHealthFailureRollsBackConfigAndFirewall(t *testing.T) {
	manager, commands := newTestRealmManager(t)
	seedManagedRealm(t, manager)
	oldState := testRealmState(9400, "udp")
	old, err := renderManagedRealmConfig(oldState.Relays)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, manager.configPath, old, 0o600)
	manager.probeTCP = func(context.Context, string, int) error { return errors.New("not listening") }
	manager.probeUDP = func(string, int) error { return nil }
	var firewallCalls [][]firewallRule
	manager.reconcileFirewall = func(_ context.Context, rules []firewallRule) error {
		firewallCalls = append(firewallCalls, append([]firewallRule(nil), rules...))
		return nil
	}
	err = manager.apply(t.Context(), testRealmState(9502, "tcp"))
	if !errors.Is(err, errManagedRealmHealth) {
		t.Fatalf("health error = %v", err)
	}
	assertFileEquals(t, manager.configPath, old)
	if commands.count("systemctl", "restart") != 2 || len(firewallCalls) != 1 || firewallCalls[0][0].port != 9400 || firewallCalls[0][0].protocol != "udp" {
		t.Fatalf("rollback calls=%v firewall=%v", commands.calls, firewallCalls)
	}
}

func TestProbeRealmTCPListenerUsesIPv6LoopbackForWildcard(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	defer listener.Close()
	accepted := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			err = connection.Close()
		}
		accepted <- err
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := probeRealmTCPListener(t.Context(), "::", port); err != nil {
		t.Fatalf("probe IPv6 wildcard: %v", err)
	}
	if err := <-accepted; err != nil {
		t.Fatalf("accept IPv6 health probe: %v", err)
	}
}

func TestManagedRealmSameConfigNoOpAndDisableCleanup(t *testing.T) {
	manager, commands := newTestRealmManager(t)
	seedManagedRealm(t, manager)
	state := testRealmState(9502, "tcp")
	config, _ := renderManagedRealmConfig(state.Relays)
	writeTestFile(t, manager.configPath, config, 0o600)
	writeTestFile(t, manager.previousPath, []byte("previous"), 0o600)
	writeTestFile(t, manager.unitPath, []byte("unit"), 0o644)
	commands.active = true
	if err := manager.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if commands.count("systemctl", "restart") != 0 {
		t.Fatalf("same config restarted: %v", commands.calls)
	}
	var cleared bool
	manager.reconcileFirewall = func(_ context.Context, rules []firewallRule) error {
		cleared = len(rules) == 0
		return nil
	}
	if err := manager.apply(t.Context(), desiredRealmState{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{manager.configPath, manager.previousPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Realm runtime file retained: %s", path)
		}
	}
	for _, path := range []string{manager.binaryPath, manager.markerPath, manager.unitPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("Realm managed install removed: %s: %v", path, err)
		}
	}
	if !cleared || commands.count("systemctl", "disable") != 1 {
		t.Fatalf("Realm disable calls=%v cleared=%v", commands.calls, cleared)
	}
}

func TestRealmFirewallOwnerDoesNotParseProxyRules(t *testing.T) {
	firewall := newRealmFirewall()
	rules := firewall.parseManagedFirewallRules(`-A INPUT -p tcp --dport 443 -m comment --comment vps-panel-proxy-tcp -j ACCEPT
-A INPUT -p udp --dport 9502 -m comment --comment vps-panel-realm-udp -j ACCEPT`)
	if len(rules) != 1 {
		t.Fatalf("Realm-owned rules = %+v", rules)
	}
	if _, ok := rules[firewallRule{port: 9502, protocol: "udp"}]; !ok {
		t.Fatalf("Realm UDP rule missing: %+v", rules)
	}
}

func TestManagedRealmPurgeRemovesOnlyOwnedRuntimeAndIsIdempotent(t *testing.T) {
	manager, commands := newTestRealmManager(t)
	seedManagedRealm(t, manager)
	writeTestFile(t, manager.configPath, []byte("current"), 0o600)
	writeTestFile(t, manager.previousPath, []byte("previous"), 0o600)
	writeTestFile(t, filepath.Join(manager.configDir, "nested", "managed.toml"), []byte("managed"), 0o600)
	writeTestFile(t, filepath.Join(manager.installDir, "data", "managed.db"), []byte("managed"), 0o600)
	writeTestFile(t, manager.unitPath, []byte("unit"), 0o644)
	commands.active = true
	firewallPurges := 0
	manager.purgeFirewall = func(context.Context) error {
		firewallPurges++
		return nil
	}
	state := desiredRealmState{Purge: true}
	if err := manager.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if commands.count("systemctl", "stop") != 1 || commands.count("systemctl", "disable") != 1 ||
		commands.count("systemctl", "daemon-reload") != 1 || firewallPurges != 1 {
		t.Fatalf("purge calls = %v, firewall purges = %d", commands.calls, firewallPurges)
	}
	if _, err := os.Stat(manager.unitPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed Realm service remained: %v", err)
	}
	for _, path := range []string{manager.installDir, manager.configDir} {
		assertDirectoryEmpty(t, path)
	}
	if err := manager.apply(t.Context(), state); err != nil {
		t.Fatalf("repeat purge: %v", err)
	}
	if firewallPurges != 2 {
		t.Fatalf("repeat purge did not verify firewall cleanup: %d", firewallPurges)
	}
}

func TestManagedRealmPurgeMissingPathsIsIdempotent(t *testing.T) {
	manager, commands := newTestRealmManager(t)
	firewallPurges := 0
	manager.purgeFirewall = func(context.Context) error {
		firewallPurges++
		return nil
	}
	state := desiredRealmState{Purge: true}
	for range 2 {
		if err := manager.apply(t.Context(), state); err != nil {
			t.Fatal(err)
		}
	}
	if firewallPurges != 2 || len(commands.calls) != 0 {
		t.Fatalf("missing-path purges = %d, service calls = %v", firewallPurges, commands.calls)
	}
}

func TestManagedRealmPurgePreservesRuntimeWithoutOwnershipMarker(t *testing.T) {
	manager, commands := newTestRealmManager(t)
	writeTestFile(t, manager.binaryPath, []byte("user content"), 0o755)
	writeTestFile(t, manager.configPath, []byte("user config"), 0o600)
	writeTestFile(t, manager.unitPath, []byte("user unit"), 0o644)
	manager.purgeFirewall = func(context.Context) error { return nil }
	if err := manager.apply(t.Context(), desiredRealmState{Purge: true}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{manager.binaryPath, manager.configPath, manager.unitPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unmanaged Realm path was removed: %s (%v)", path, err)
		}
	}
	if len(commands.calls) != 0 {
		t.Fatalf("unmanaged Realm purge changed service: %v", commands.calls)
	}
}

func newTestRealmManager(t *testing.T) (*realmManager, *realmCommandRecorder) {
	t.Helper()
	root := t.TempDir()
	commands := &realmCommandRecorder{}
	manager := &realmManager{
		installDir:     filepath.Join(root, "opt", "vps-panel", "realm"),
		binaryPath:     filepath.Join(root, "opt", "vps-panel", "realm", "realm"),
		markerPath:     filepath.Join(root, "opt", "vps-panel", "realm", ".managed-by-vps-panel"),
		configDir:      filepath.Join(root, "etc", "vps-panel", "realm"),
		configPath:     filepath.Join(root, "etc", "vps-panel", "realm", "config.toml"),
		previousPath:   filepath.Join(root, "etc", "vps-panel", "realm", "config.previous.toml"),
		unitPath:       filepath.Join(root, "etc", "systemd", "system", "vps-panel-realm.service"),
		unmanagedUnits: []string{filepath.Join(root, "etc", "systemd", "system", "realm.service")},
		goos:           "linux", goarch: "amd64", libc: libcGlibc, releaseBaseURL: "http://127.0.0.1:1",
		assets: managedRealmAssets, client: &http.Client{Timeout: time.Second}, runCommand: commands.run,
		validateConfig:    func(context.Context, string, string) error { return nil },
		probeTCP:          func(context.Context, string, int) error { return nil },
		probeUDP:          func(string, int) error { return nil },
		reconcileFirewall: func(context.Context, []firewallRule) error { return nil },
		wait:              func(context.Context, time.Duration) error { return nil }, healthAttempts: 2,
	}
	return manager, commands
}

type realmCommandRecorder struct {
	mu     sync.Mutex
	calls  [][]string
	active bool
}

func (r *realmCommandRecorder) run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, arguments...))
	if len(arguments) == 1 && arguments[0] == "--version" {
		return []byte("realm 2.9.4"), nil
	}
	if name == "systemctl" && len(arguments) != 0 {
		switch arguments[0] {
		case "start", "restart":
			r.active = true
		case "disable", "stop":
			r.active = false
		case "is-active":
			if !r.active {
				return nil, errors.New("inactive")
			}
		}
	}
	if name == "rc-service" && len(arguments) >= 2 {
		switch arguments[1] {
		case "start", "restart":
			r.active = true
		case "stop":
			r.active = false
		case "status":
			if !r.active {
				return nil, errors.New("inactive")
			}
		}
	}
	return nil, nil
}

func (r *realmCommandRecorder) count(name, first string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, call := range r.calls {
		if len(call) >= 2 && call[0] == name && call[1] == first {
			count++
		}
	}
	return count
}

func seedManagedRealm(t *testing.T, manager *realmManager) {
	t.Helper()
	writeTestFile(t, manager.markerPath, []byte("managed by vps-panel\n"), 0o644)
	writeTestFile(t, manager.binaryPath, []byte("realm binary"), 0o755)
}

func testRealmState(port int, network string) desiredRealmState {
	return desiredRealmState{Enabled: true, Relays: []desiredRelay{{
		ID: 1, ListenAddress: "0.0.0.0", ListenPort: port,
		TargetHost: "example.com", TargetPort: 443, Network: network,
	}}}
}

func makeRealmArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(gzipWriter)
	if err := archive.WriteHeader(&tar.Header{Name: "realm", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func realmChecksum(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
