package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSingboxProxy(t *testing.T, id int64, port int) desiredProxy {
	t.Helper()
	cert, key := testACMECertificate(t, "hy2.example.com", time.Now().Add(24*time.Hour))
	return desiredProxy{ID: id, Listen: "0.0.0.0", Port: port, Protocol: "hysteria2", Security: "tls", ServerName: "hy2.example.com", TLS: &desiredTLS{Mode: "manual", Certificate: string(cert), PrivateKey: string(key)}, Hysteria2: &desiredHysteria2{UpMbps: 100, DownMbps: 200, ObfsPassword: "obfs-secret"}, Clients: []desiredClient{{ID: 2, Password: "client-two"}, {ID: 1, Password: "client-one"}}}
}

func TestSingboxRenderAndValidation(t *testing.T) {
	first, second := testSingboxProxy(t, 1, 443), testSingboxProxy(t, 2, 8443)
	data, err := renderManagedSingboxConfig([]desiredProxy{second, first})
	if err != nil {
		t.Fatal(err)
	}
	var config singboxConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Inbounds) != 2 || config.Inbounds[0].Tag != "proxy-1" || len(config.Inbounds[0].Users) != 2 || config.Inbounds[0].Users[0].Password != "client-one" || config.Inbounds[0].Obfs.Type != "salamander" || !config.Inbounds[0].TLS.Enabled {
		t.Fatal("unexpected rendered Hysteria2 configuration")
	}
	for _, invalid := range []desiredProxy{
		func() desiredProxy { p := first; p.Clients = nil; return p }(),
		func() desiredProxy { p := first; p.Port = 0; return p }(),
		func() desiredProxy { p := first; p.Hysteria2 = &desiredHysteria2{UpMbps: -1}; return p }(),
		func() desiredProxy {
			p := first
			p.Clients = []desiredClient{{ID: 1, Password: "same"}, {ID: 2, Password: "same"}}
			return p
		}(),
	} {
		if _, err := renderManagedSingboxConfig([]desiredProxy{invalid}); !errors.Is(err, errUnsupportedManagedConfig) {
			t.Fatal("invalid configuration accepted")
		}
	}
	wrongName := first
	wrongName.ServerName = "wrong.example.com"
	if _, err := renderManagedSingboxConfig([]desiredProxy{wrongName}); !errors.Is(err, errSingboxTLS) {
		t.Fatal("mismatched certificate was accepted")
	}
	if binary := os.Getenv("VPS_PANEL_TEST_SINGBOX"); binary != "" {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(binary, "check", "-c", path).CombinedOutput(); err != nil {
			t.Fatalf("sing-box check failed: %v: %s", err, out)
		}
	}
}

func TestSingboxPortHoppingDoesNotChangeInboundSchema(t *testing.T) {
	proxy := testSingboxProxy(t, 1, 443)
	base, err := renderManagedSingboxConfig([]desiredProxy{proxy})
	if err != nil {
		t.Fatal(err)
	}
	proxy.Hysteria2.PortHoppingEnabled = true
	proxy.Hysteria2.PortHoppingStart, proxy.Hysteria2.PortHoppingEnd = 20000, 30000
	hopping, err := renderManagedSingboxConfig([]desiredProxy{proxy})
	if err != nil || !bytes.Equal(base, hopping) {
		t.Fatalf("port hopping changed Sing-box inbound schema: %v", err)
	}
	if strings.Contains(string(hopping), "server_ports") || strings.Contains(string(hopping), "hop_interval") {
		t.Fatal("client-only hopping fields leaked into Sing-box inbound")
	}
}

type testSingboxService struct {
	active      bool
	failRestart bool
	installErr  error
	restarts    int
	path        string
}

func (s *testSingboxService) Path() string                       { return s.path }
func (s *testSingboxService) Install() (bool, error)             { return false, s.installErr }
func (s *testSingboxService) DaemonReload(context.Context) error { return nil }
func (s *testSingboxService) Enable(context.Context) error       { return nil }
func (s *testSingboxService) Disable(context.Context) error      { return nil }
func (s *testSingboxService) Start(context.Context) error        { s.active = true; return nil }
func (s *testSingboxService) Stop(context.Context) error         { s.active = false; return nil }
func (s *testSingboxService) Restart(context.Context) error {
	s.restarts++
	if s.failRestart {
		s.failRestart = false
		s.active = false
		return errors.New("restart failed")
	}
	s.active = true
	return nil
}

func TestSingboxApplyReportsSafeDeploymentAndServiceErrors(t *testing.T) {
	state := desiredSingboxState{Enabled: true, Proxies: []desiredProxy{testSingboxProxy(t, 1, 443)}}
	t.Run("installation directory", func(t *testing.T) {
		m, _ := newTestSingboxManager(t)
		m.binaryPath = t.TempDir()
		if got := desiredStateErrorMessage(m.apply(t.Context(), state)); got != errSingboxInstall.Error() {
			t.Fatalf("installation error = %q", got)
		}
	})
	t.Run("certificate directory", func(t *testing.T) {
		m, _ := newTestSingboxManager(t)
		blocked := filepath.Join(t.TempDir(), "blocked")
		if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
			t.Fatal(err)
		}
		m.acme = newACMEManager()
		m.acme.certDir = filepath.Join(blocked, "certificates")
		proxy := testSingboxProxy(t, 1, 443)
		proxy.TLS = &desiredTLS{Mode: "acme"}
		if got := desiredStateErrorMessage(m.apply(t.Context(), desiredSingboxState{Enabled: true, Proxies: []desiredProxy{proxy}})); got != errSingboxTLSSetup.Error() {
			t.Fatalf("certificate error = %q", got)
		}
	})
	t.Run("configuration directory", func(t *testing.T) {
		m, _ := newTestSingboxManager(t)
		if err := os.WriteFile(m.configDir, []byte("blocked"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := desiredStateErrorMessage(m.apply(t.Context(), state)); got != errSingboxConfigWrite.Error() {
			t.Fatalf("configuration error = %q", got)
		}
	})
	t.Run("service installation", func(t *testing.T) {
		m, service := newTestSingboxManager(t)
		service.installErr = errors.New("private diagnostic")
		if got := desiredStateErrorMessage(m.apply(t.Context(), state)); got != errSingboxServiceSetup.Error() {
			t.Fatalf("service error = %q", got)
		}
	})
	t.Run("service restart", func(t *testing.T) {
		m, service := newTestSingboxManager(t)
		service.failRestart = true
		if got := desiredStateErrorMessage(m.apply(t.Context(), state)); got != errSingboxStart.Error() {
			t.Fatalf("restart error = %q", got)
		}
	})
}
func (s *testSingboxService) IsActive(context.Context) (bool, error) { return s.active, nil }

func newTestSingboxManager(t *testing.T) (*singboxManager, *testSingboxService) {
	t.Helper()
	root := t.TempDir()
	service := &testSingboxService{active: true, path: filepath.Join(root, "service")}
	m := &singboxManager{installDir: filepath.Join(root, "install"), binaryPath: filepath.Join(root, "install", "sing-box"), markerPath: filepath.Join(root, "install", ".managed"), configDir: filepath.Join(root, "config"), configPath: filepath.Join(root, "config", "config.json"), previousPath: filepath.Join(root, "config", "previous.json"), goos: "linux", goarch: "amd64", service: service, client: http.DefaultClient,
		runCommand: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) > 0 && args[0] == "version" {
				return []byte("sing-box version " + managedSingboxVersion), nil
			}
			return nil, nil
		},
		firewall: &proxyFirewall{owner: "singbox", lookPath: func(string) (string, error) { return "", errors.New("not found") }}, probeUDP: func(string, int) error { return nil }, wait: func(context.Context, time.Duration) error { return nil },
	}
	if err := os.MkdirAll(m.installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.markerPath, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.binaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return m, service
}

func TestSingboxApplyRollback(t *testing.T) {
	m, service := newTestSingboxManager(t)
	state := desiredSingboxState{Enabled: true, Proxies: []desiredProxy{testSingboxProxy(t, 1, 443)}}
	if err := m.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	state.Proxies[0].Port = 8443
	service.failRestart = true
	if err := m.apply(t.Context(), state); err == nil {
		t.Fatal("expected restart failure")
	}
	after, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(previous, after) || !service.active {
		t.Fatal("previous configuration was not restored")
	}
	if err := m.apply(t.Context(), desiredSingboxState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("configuration was not removed")
	}
}

func TestSingboxDownloadRejectsWrongChecksum(t *testing.T) {
	m, _ := newTestSingboxManager(t)
	if err := os.Remove(m.binaryPath); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("corrupt")) }))
	defer server.Close()
	m.releaseBaseURL = server.URL
	_, _, err := m.ensureBinary(t.Context())
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum error = %v", err)
	}
	if _, err := os.Stat(m.binaryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unverified binary was installed")
	}
}

func TestSingboxUpgradeFailureRestoresBinaryAndConfiguration(t *testing.T) {
	m, service := newTestSingboxManager(t)
	state := desiredSingboxState{Enabled: true, Proxies: []desiredProxy{testSingboxProxy(t, 1, 443)}}
	if err := m.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	previousConfig, err := os.ReadFile(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	previousBinary := []byte("old working binary")
	if err := os.WriteFile(m.binaryPath, previousBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tarball := tar.NewWriter(gz)
	newBinary := []byte("new binary")
	if err := tarball.WriteHeader(&tar.Header{Name: "sing-box/sing-box", Mode: 0o755, Size: int64(len(newBinary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarball.Write(newBinary); err != nil {
		t.Fatal(err)
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())
	m.hashes = map[string]string{"amd64": hex.EncodeToString(sum[:])}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive.Bytes()) }))
	defer server.Close()
	m.releaseBaseURL = server.URL
	m.runCommand = func(_ context.Context, path string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "version" {
			if path == m.binaryPath {
				return []byte("sing-box version 1.0.0"), nil
			}
			return []byte("sing-box version " + managedSingboxVersion), nil
		}
		return nil, errors.New("candidate invalid")
	}
	if err := m.apply(t.Context(), state); !errors.Is(err, errSingboxValidation) {
		t.Fatalf("expected validation failure: %v", err)
	}
	restoredBinary, err := os.ReadFile(m.binaryPath)
	if err != nil || !bytes.Equal(restoredBinary, previousBinary) {
		t.Fatal("previous executable not restored")
	}
	restoredConfig, err := os.ReadFile(m.configPath)
	if err != nil || !bytes.Equal(restoredConfig, previousConfig) || !service.active {
		t.Fatal("previous configuration or service lost")
	}
}

func TestSingboxOfficialArchiveExtraction(t *testing.T) {
	path := os.Getenv("VPS_PANEL_TEST_SINGBOX_ARCHIVE")
	if path == "" {
		t.Skip("set VPS_PANEL_TEST_SINGBOX_ARCHIVE to inspect the official Linux archive")
	}
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != managedSingboxHashes["amd64"] {
		t.Fatal("unexpected Linux archive checksum")
	}
	binary, err := extractSingboxBinary(archive)
	if err != nil || len(binary) < 4 || !bytes.Equal(binary[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		t.Fatalf("Linux executable extraction failed: %v", err)
	}
}

func TestSingboxFirewallIsolationAndChinaPorts(t *testing.T) {
	state := desiredState{Xray: desiredXrayState{Enabled: true, Proxies: []desiredProxy{{Port: 443, Protocol: "vless"}}}, Singbox: &desiredSingboxState{Enabled: true, Proxies: []desiredProxy{{Port: 443, Protocol: "hysteria2"}, {Port: 8443, Protocol: "hysteria2"}}}}
	tcp, udp := managedChinaInboundPorts(state)
	if len(tcp) != 1 || tcp[0] != 443 || len(udp) != 2 || udp[0] != 443 || udp[1] != 8443 {
		t.Fatalf("China firewall ports: TCP %v UDP %v", tcp, udp)
	}
	firewall := newSingboxFirewall()
	if firewall.firewallComment("udp") == newProxyFirewall().firewallComment("udp") || firewall.firewallComment("udp") == newRealmFirewall().firewallComment("udp") {
		t.Fatal("Sing-box firewall ownership overlaps another component")
	}
	for _, rule := range singboxFirewallRules(state.Singbox.Proxies) {
		if rule.protocol != "udp" {
			t.Fatal("HY2 opened a non-UDP port")
		}
	}
}

func TestSingboxLocalHysteria2Connection(t *testing.T) {
	binary := os.Getenv("VPS_PANEL_TEST_SINGBOX")
	if binary == "" {
		t.Skip("set VPS_PANEL_TEST_SINGBOX to run the real Sing-box connection test")
	}
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpPort := udp.LocalAddr().(*net.UDPAddr).Port
	udp.Close()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpPort := tcp.Addr().(*net.TCPAddr).Port
	tcp.Close()
	proxy := testSingboxProxy(t, 1, udpPort)
	proxy.Listen = "127.0.0.1"
	serverConfig, err := renderManagedSingboxConfig([]desiredProxy{proxy})
	if err != nil {
		t.Fatal(err)
	}
	clientConfig := map[string]any{
		"inbounds":  []any{map[string]any{"type": "http", "listen": "127.0.0.1", "listen_port": httpPort}},
		"outbounds": []any{map[string]any{"type": "hysteria2", "tag": "hy2", "server": "127.0.0.1", "server_port": udpPort, "password": proxy.Clients[0].Password, "obfs": map[string]any{"type": "salamander", "password": proxy.Hysteria2.ObfsPassword}, "tls": map[string]any{"enabled": true, "server_name": proxy.ServerName, "certificate": strings.Split(strings.TrimSpace(proxy.TLS.Certificate), "\n")}}},
		"route":     map[string]any{"final": "hy2"},
	}
	clientJSON, err := json.Marshal(clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	start := func(name string, config []byte) {
		path := filepath.Join(root, name+".json")
		if err := os.WriteFile(path, config, 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(binary, "run", "-c", path)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = command.Process.Kill(); _, _ = command.Process.Wait() })
	}
	start("server", serverConfig)
	start("client", clientJSON)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hy2-ok")) }))
	defer target.Close()
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", httpPort))
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	var response *http.Response
	for i := 0; i < 20; i++ {
		response, err = client.Get(target.URL)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("local Hysteria2 request failed: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "hy2-ok" {
		t.Fatalf("unexpected proxied response: %v", err)
	}
}
