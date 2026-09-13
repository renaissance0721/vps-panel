package main

import (
	"context"
	"crypto/sha256"
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
