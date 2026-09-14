package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentReleaseAssetSupportsReleaseArchitectures(t *testing.T) {
	for architecture, expected := range map[string]string{
		"amd64": "vps-panel-agent-linux-amd64",
		"arm64": "vps-panel-agent-linux-arm64",
	} {
		asset, err := agentReleaseAsset(architecture)
		if err != nil || asset != expected {
			t.Fatalf("asset for %s = (%q, %v), want %q", architecture, asset, err, expected)
		}
	}
	if _, err := agentReleaseAsset("386"); err == nil {
		t.Fatal("unsupported architecture was accepted")
	}
}

func TestVerifyAgentChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent")
	data := []byte("verified Agent binary")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	asset := "vps-panel-agent-linux-amd64"
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(data), asset)
	if err := verifyAgentChecksum(path, asset, []byte(checksum)); err != nil {
		t.Fatalf("valid checksum: %v", err)
	}
	if err := verifyAgentChecksum(path, asset, []byte(strings.Repeat("0", 64)+"  "+asset+"\n")); err == nil {
		t.Fatal("incorrect checksum was accepted")
	}
	if err := verifyAgentChecksum(path, asset, []byte(checksum+"\n")); err != nil {
		t.Fatalf("checksum file with trailing line: %v", err)
	}
}

func TestVerifyAgentBinaryVersionRejectsMismatch(t *testing.T) {
	original := runUpgradeCommand
	t.Cleanup(func() { runUpgradeCommand = original })
	runUpgradeCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("vps-panel-agent v0.12.0\n"), nil
	}
	if err := verifyAgentBinaryVersion(t.Context(), "agent", "v0.12.0"); err != nil {
		t.Fatalf("matching version: %v", err)
	}
	if err := verifyAgentBinaryVersion(t.Context(), "agent", "v0.12.1"); err == nil {
		t.Fatal("mismatched Agent version was accepted")
	}
}

func TestPrepareAgentUpgradeVerifiesBeforeLaunchingAndPreservesConfig(t *testing.T) {
	managedDirectory := t.TempDir()
	originalManagedDir, originalManagedPath, originalBaseURL := agentManagedDir, agentManagedPath, releasesBaseURL
	originalRun, originalLaunch := runUpgradeCommand, launchAgentUpgrade
	t.Cleanup(func() {
		agentManagedDir, agentManagedPath, releasesBaseURL = originalManagedDir, originalManagedPath, originalBaseURL
		runUpgradeCommand, launchAgentUpgrade = originalRun, originalLaunch
	})
	agentManagedDir = managedDirectory
	agentManagedPath = filepath.Join(managedDirectory, "vps-panel-agent")

	asset, err := agentReleaseAsset(runtime.GOARCH)
	if err != nil {
		t.Skipf("test architecture is not a release architecture: %v", err)
	}
	binary := []byte("new Agent binary")
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(binary), asset)
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case asset:
			_, _ = w.Write(binary)
		case "SHA256SUMS":
			_, _ = w.Write([]byte(checksum))
		default:
			http.NotFound(w, r)
		}
	}))
	defer release.Close()
	releasesBaseURL = release.URL
	runUpgradeCommand = func(_ context.Context, path string, arguments ...string) ([]byte, error) {
		if len(arguments) != 1 || arguments[0] != "version" {
			return nil, fmt.Errorf("unexpected version arguments: %v", arguments)
		}
		if contents, err := os.ReadFile(path); err != nil || string(contents) != string(binary) {
			return nil, fmt.Errorf("version check received unverified binary")
		}
		return []byte("vps-panel-agent v0.12.0\n"), nil
	}
	var stagedPath string
	launchAgentUpgrade = func(path, version string) error {
		if version != "v0.12.0" {
			return fmt.Errorf("target version = %q", version)
		}
		stagedPath = path
		return nil
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	configContents := []byte(`{"panel_url":"https://panel.example","server_id":7,"agent_id":8,"agent_token":"secret"}`)
	if err := os.WriteFile(configPath, configContents, 0o600); err != nil {
		t.Fatal(err)
	}
	identity := config{PanelURL: "https://panel.example", ServerID: 7, AgentID: 8, AgentToken: "secret"}
	if err := prepareAgentUpgrade(t.Context(), release.Client(), identity, "v0.12.0"); err != nil {
		t.Fatalf("prepare upgrade: %v", err)
	}
	defer os.Remove(stagedPath)
	if stagedPath == "" {
		t.Fatal("verified Agent was not passed to upgrade helper")
	}
	if current, err := os.ReadFile(configPath); err != nil || string(current) != string(configContents) {
		t.Fatalf("Agent config changed during upgrade preparation: (%q, %v)", current, err)
	}

	checksum = strings.Repeat("0", 64) + "  " + asset + "\n"
	stagedPath = ""
	if err := prepareAgentUpgrade(t.Context(), release.Client(), identity, "v0.12.0"); err == nil || stagedPath != "" {
		t.Fatalf("checksum failure = %v, staged path = %q", err, stagedPath)
	}
}

func TestLaunchUpgradeHelperUsesDetachedProcessOnOpenRC(t *testing.T) {
	originalDetect := detectUpgradeHostEnvironment
	originalStart := startOpenRCUpgradeProcess
	t.Cleanup(func() {
		detectUpgradeHostEnvironment = originalDetect
		startOpenRCUpgradeProcess = originalStart
	})
	detectUpgradeHostEnvironment = func() (hostEnvironment, error) {
		return hostEnvironment{InitSystem: initSystemOpenRC, Libc: libcMusl}, nil
	}
	var executable string
	var arguments []string
	startOpenRCUpgradeProcess = func(path string, values ...string) error {
		executable = path
		arguments = append([]string(nil), values...)
		return nil
	}
	if err := launchUpgradeHelper("/opt/vps-panel/agent/.agent-upgrade-test", "v0.16.1"); err != nil {
		t.Fatal(err)
	}
	if executable == "" || strings.Join(arguments, " ") != "_apply-upgrade --staged /opt/vps-panel/agent/.agent-upgrade-test --target v0.16.1" {
		t.Fatalf("OpenRC helper = %q %v", executable, arguments)
	}
}

func TestLaunchUpgradeHelperKeepsSystemdRun(t *testing.T) {
	originalDetect := detectUpgradeHostEnvironment
	originalLauncher := runUpgradeLauncher
	t.Cleanup(func() {
		detectUpgradeHostEnvironment = originalDetect
		runUpgradeLauncher = originalLauncher
	})
	detectUpgradeHostEnvironment = func() (hostEnvironment, error) {
		return hostEnvironment{InitSystem: initSystemSystemd, Libc: libcGlibc}, nil
	}
	var name string
	var arguments []string
	runUpgradeLauncher = func(command string, values ...string) ([]byte, error) {
		name = command
		arguments = append([]string(nil), values...)
		return nil, nil
	}
	if err := launchUpgradeHelper("/opt/vps-panel/agent/.agent-upgrade-test", "v0.16.1"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	if name != "systemd-run" || !strings.Contains(joined, "--collect") ||
		!strings.Contains(joined, "_apply-upgrade --staged /opt/vps-panel/agent/.agent-upgrade-test --target v0.16.1") {
		t.Fatalf("systemd helper = %q %v", name, arguments)
	}
}

func TestRestartAgentServiceUsesOpenRC(t *testing.T) {
	originalDetect := detectUpgradeHostEnvironment
	originalRun := runAgentServiceCommand
	t.Cleanup(func() {
		detectUpgradeHostEnvironment = originalDetect
		runAgentServiceCommand = originalRun
	})
	detectUpgradeHostEnvironment = func() (hostEnvironment, error) {
		return hostEnvironment{InitSystem: initSystemOpenRC, Libc: libcMusl}, nil
	}
	var calls []string
	runAgentServiceCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		calls = append(calls, strings.Join(append([]string{name}, arguments...), " "))
		return nil, nil
	}
	if err := restartAgentService(); err != nil {
		t.Fatal(err)
	}
	want := []string{"rc-service vps-panel-agent restart", "rc-service vps-panel-agent status"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("OpenRC restart calls = %v", calls)
	}
}

func TestApplyStagedAgentUpgradeRollsBackFailedRestart(t *testing.T) {
	directory := t.TempDir()
	originalDir, originalPath := agentManagedDir, agentManagedPath
	originalDetect := detectUpgradeHostEnvironment
	originalRun := runAgentServiceCommand
	t.Cleanup(func() {
		agentManagedDir, agentManagedPath = originalDir, originalPath
		detectUpgradeHostEnvironment = originalDetect
		runAgentServiceCommand = originalRun
	})
	agentManagedDir = directory
	agentManagedPath = filepath.Join(directory, "vps-panel-agent")
	if err := os.WriteFile(agentManagedPath, []byte("old Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(directory, ".agent-upgrade-staged")
	if err := os.WriteFile(staged, []byte("new Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	detectUpgradeHostEnvironment = func() (hostEnvironment, error) {
		return hostEnvironment{InitSystem: initSystemOpenRC, Libc: libcMusl}, nil
	}
	restarts := 0
	runAgentServiceCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 1 && arguments[1] == "restart" {
			restarts++
			if restarts == 1 {
				return []byte("restart failed"), errors.New("restart failed")
			}
		}
		return nil, nil
	}
	if err := applyStagedAgentUpgrade(staged, "v0.16.1"); err == nil {
		t.Fatal("failed service restart did not fail the upgrade")
	}
	current, err := os.ReadFile(agentManagedPath)
	if err != nil || string(current) != "old Agent" || restarts != 2 {
		t.Fatalf("rolled back Agent = %q, restarts = %d, error = %v", current, restarts, err)
	}
}
