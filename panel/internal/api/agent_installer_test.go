package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestInstallAgentScript(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	response := httptest.NewRecorder()
	NewHandler(db, t.TempDir()).ServeHTTP(
		response, httptest.NewRequest(http.MethodGet, "/install-agent.sh", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("installer status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/x-shellscript") {
		t.Fatalf("installer content type = %q", contentType)
	}
	for _, required := range []string{
		"#!/bin/sh",
		"--server",
		"--token",
		"--version",
		"vps-panel-agent-linux-${architecture}",
		"${RELEASES_BASE}/download/${agent_version}",
		"${RELEASES_BASE}/latest/download",
		"/opt/vps-panel/agent",
		`service_file="/etc/systemd/system/${SERVICE_NAME}.service"`,
		"/etc/init.d/${SERVICE_NAME}",
		`supervisor="supervise-daemon"`,
		"rc-update add",
		"rc-service",
		"apk add --no-cache curl ca-certificates",
		"apk add --no-cache coreutils",
		"Restart=on-failure",
		"RestartSec=3",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ReadWritePaths=/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme /etc/systemd/system",
		"systemctl enable",
		"systemctl restart",
		`systemctl restart "${SERVICE_NAME}.service"`,
		`rc-service "$SERVICE_NAME" restart`,
		`staged_binary=$(mktemp "${AGENT_DIR}/.vps-panel-agent.XXXXXX")`,
		`mv -f "$staged_binary" "$BINARY_PATH"`,
	} {
		if !strings.Contains(response.Body.String(), required) {
			t.Fatalf("installer does not contain %q", required)
		}
	}
	for _, removed := range []string{"--force", "already registered", "CONFIG_FILE"} {
		if strings.Contains(response.Body.String(), removed) {
			t.Fatalf("installer still contains removed behavior %q", removed)
		}
	}
	for _, bashism := range []string{"#!/usr/bin/env bash", "\n[[", "${EUID}", "set -E", "pipefail"} {
		if strings.Contains(response.Body.String(), bashism) {
			t.Fatalf("installer is not POSIX sh: contains %q", bashism)
		}
	}
	if strings.Contains(response.Body.String(), "Restart=always") {
		t.Fatal("installer restarts a deliberately stopped Agent")
	}
	registrationIndex := strings.Index(response.Body.String(), `"$download_path" register --server "$server_url" --token "$enrollment_token"`)
	stagingIndex := strings.Index(response.Body.String(), `staged_binary=$(mktemp "${AGENT_DIR}/.vps-panel-agent.XXXXXX")`)
	replacementIndex := strings.Index(response.Body.String(), `mv -f "$staged_binary" "$BINARY_PATH"`)
	serviceIndex := strings.Index(response.Body.String(), `install -m 0644 "$service_path" "$service_file"`)
	if stagingIndex < 0 || registrationIndex < 0 || replacementIndex < 0 || serviceIndex < 0 ||
		stagingIndex >= registrationIndex || registrationIndex >= replacementIndex || replacementIndex >= serviceIndex {
		t.Fatal("installer must stage the new binary before registration and replace binary/service only after registration")
	}
}

func TestInstallerRestartsExistingSystemdAndOpenRCServices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX sh")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX sh is unavailable")
	}
	source := string(installAgentScript)
	start := strings.Index(source, "start_service() {")
	if start < 0 {
		t.Fatal("installer start_service function not found")
	}
	end := strings.Index(source[start:], "\nwhile [ \"$#\" -gt 0 ]; do")
	if end < 0 {
		t.Fatal("installer start_service function end not found")
	}
	startService := source[start : start+end]
	for _, test := range []struct {
		name          string
		initSystem    string
		wantRestart   string
		unwantedStart string
	}{
		{"systemd", "systemd", "systemctl restart vps-panel-agent.service", "systemctl start vps-panel-agent.service"},
		{"OpenRC", "openrc", "rc-service vps-panel-agent restart", "rc-service vps-panel-agent start"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mockDir := t.TempDir()
			tracePath := filepath.Join(t.TempDir(), "commands")
			for _, name := range []string{"systemctl", "rc-service", "rc-update"} {
				mock := "#!/bin/sh\nprintf '%s %s\\n' '" + name + "' \"$*\" >> \"$TRACE_FILE\"\nexit 0\n"
				if err := os.WriteFile(filepath.Join(mockDir, name), []byte(mock), 0o755); err != nil {
					t.Fatalf("write %s mock: %v", name, err)
				}
			}
			script := "set -eu\nSERVICE_NAME=vps-panel-agent\ninit_system=" + test.initSystem +
				"\nfail() { exit 1; }\n" + startService + "\nstart_service\n"
			command := exec.Command(sh, "-c", script)
			command.Env = append(os.Environ(), "PATH="+mockDir+string(os.PathListSeparator)+os.Getenv("PATH"), "TRACE_FILE="+tracePath)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("start_service failed: %v, output %q", err, output)
			}
			trace, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatalf("read service commands: %v", err)
			}
			if !strings.Contains(string(trace), test.wantRestart) || strings.Contains(string(trace), test.unwantedStart) {
				t.Fatalf("existing %s service commands = %q, want restart without start", test.name, trace)
			}
		})
	}
}

func TestAgentInstallationCommandPinsReleaseVersion(t *testing.T) {
	created := serverstore.CreatedServer{
		Server:          serverstore.Server{ID: 1, Name: "Versioned", Status: serverstore.StatusPending},
		EnrollmentToken: "one-time-token",
	}
	versioned := (&server{panelVersion: "v0.6.0"}).toCreatedServerResponse(created, "https://panel.example")
	if !strings.Contains(versioned.AgentInstallationCommand, "--version v0.6.0") {
		t.Fatalf("versioned command = %q", versioned.AgentInstallationCommand)
	}
	for _, version := range []string{"", "dev", "unknown", "v0.6.0;bad"} {
		response := (&server{panelVersion: version}).toCreatedServerResponse(created, "https://panel.example")
		if strings.Contains(response.AgentInstallationCommand, "--version") {
			t.Fatalf("development version %q leaked into command %q", version, response.AgentInstallationCommand)
		}
	}
}

func TestBootstrapAgentUpgradeScriptPreservesRegistrationAndDoesNotRegister(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	formal := NewHandlerWithVersion(db, t.TempDir(), "v0.12.0")
	response := performRequest(t, formal, http.MethodGet, "/upgrade-agent.sh", nil, nil)
	body := response.Body.String()
	if response.Code != http.StatusOK ||
		!strings.Contains(body, `VERSION="v0.12.0"`) ||
		!strings.Contains(body, "/etc/vps-panel-agent/config.json") ||
		!strings.Contains(body, `AGENT_DIR="/opt/vps-panel/agent"`) ||
		!strings.Contains(body, `BINARY_PATH="${AGENT_DIR}/vps-panel-agent"`) ||
		!strings.Contains(body, "SHA256SUMS") ||
		!strings.Contains(body, "#!/bin/sh") ||
		!strings.Contains(body, "/etc/init.d/${SERVICE_NAME}") ||
		!strings.Contains(body, `supervisor="supervise-daemon"`) ||
		!strings.Contains(body, `rc-update add "$SERVICE_NAME" default`) ||
		!strings.Contains(body, `rc-service "$SERVICE_NAME" restart`) ||
		!strings.Contains(body, "apk add --no-cache curl ca-certificates") ||
		!strings.Contains(body, "apk add --no-cache coreutils") ||
		!strings.Contains(body, "/opt/vps-panel/realm /etc/vps-panel/realm") ||
		strings.Contains(body, "#!/usr/bin/env bash") || strings.Contains(body, "\n[[") ||
		strings.Contains(body, "/api/agent/register") || strings.Contains(body, "--token") ||
		strings.Contains(body, " register --server") {
		t.Fatalf("bootstrap upgrade script = %d, %s", response.Code, body)
	}
	dev := NewHandlerWithVersion(db, t.TempDir(), "dev")
	if response := performRequest(t, dev, http.MethodGet, "/upgrade-agent.sh", nil, nil); response.Code != http.StatusConflict {
		t.Fatalf("development bootstrap script = %d, %s", response.Code, response.Body.String())
	}
}

func TestReleaseWorkflowPublishesAgentChecksums(t *testing.T) {
	workflow, err := os.ReadFile("../../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	source := string(workflow)
	for _, expected := range []string{
		"sha256sum", "vps-panel-agent-linux-amd64", "vps-panel-agent-linux-arm64",
		"> SHA256SUMS", "gh release upload", "release-assets/*",
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("release workflow is missing %q", expected)
		}
	}
}
