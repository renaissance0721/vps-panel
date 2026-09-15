package api

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func upgradeScriptSection(t *testing.T, source, start, end string) string {
	t.Helper()
	from := strings.Index(source, start)
	if from < 0 {
		t.Fatalf("script section %q not found", start)
	}
	to := strings.Index(source[from:], end)
	if to < 0 {
		t.Fatalf("script section end %q not found", end)
	}
	return source[from : from+to]
}

func upgradeTestShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("filesystem shell test requires POSIX paths")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX sh is unavailable")
	}
	return sh
}

func TestUpgradeAgentScriptMatchesInstallerAndPreparesSandbox(t *testing.T) {
	upgrade := upgradeAgentScript
	installer := string(installAgentScript)
	for _, section := range []struct{ start, installEnd, upgradeEnd string }{
		{"write_systemd_service() {", "\nwrite_openrc_service() {", "\nwrite_openrc_service() {"},
		{"write_openrc_service() {", "\nstart_service() {", "\nrestart_service() {"},
	} {
		if upgradeScriptSection(t, upgrade, section.start, section.upgradeEnd) !=
			upgradeScriptSection(t, installer, section.start, section.installEnd) {
			t.Fatalf("%s differs between upgrade and installation", section.start)
		}
	}
	directoryCommands := upgradeScriptSection(t, upgrade,
		`install -d -m 0755 "$AGENT_DIR"`, `if [ -f "$BINARY_PATH" ]; then`)
	for _, command := range []string{
		`install -d -m 0755 "$AGENT_DIR"`,
		`install -d -m 0755 /opt/vps-panel/xray /opt/vps-panel/realm`,
		`install -d -m 0700 /etc/vps-panel/xray /etc/vps-panel/realm`,
	} {
		if !strings.Contains(directoryCommands, command) || !strings.Contains(installer, command) {
			t.Fatalf("upgrade and installation must both prepare %q", command)
		}
	}
	if strings.Index(upgrade, directoryCommands) >= strings.Index(upgrade, "changed=true\ninstall -m 0755") ||
		strings.Index(upgrade, directoryCommands) >= strings.LastIndex(upgrade, "\nrestart_service\nchanged=false") {
		t.Fatal("sandbox directories must exist before binary/service replacement and restart")
	}
	for _, required := range []string{
		`systemctl status "${SERVICE_NAME}.service" --no-pager --lines=0 >&2 || true`,
		"Agent service did not start after upgrade",
		`install -m 0755 "${temporary_dir}/previous-agent" "$BINARY_PATH"`,
		`"${temporary_dir}/previous-service" "$service_file"`,
		"restart_service || true",
	} {
		if !strings.Contains(upgrade, required) {
			t.Fatalf("upgrade script is missing %q", required)
		}
	}
	if strings.Contains(upgrade, "journalctl") || strings.Contains(upgrade, `rm -f "$CONFIG_FILE"`) ||
		strings.Contains(upgrade, `"$CONFIG_FILE" register`) || strings.Contains(upgrade, "--token") {
		t.Fatal("upgrade must not depend on journalctl or modify Agent registration/config")
	}
}

func TestUpgradeAgentSandboxDirectoriesPreserveExistingConfigs(t *testing.T) {
	sh := upgradeTestShell(t)
	commands := upgradeScriptSection(t, upgradeAgentScript,
		`install -d -m 0755 "$AGENT_DIR"`, `if [ -f "$BINARY_PATH" ]; then`)
	for _, path := range []string{
		"/opt/vps-panel/xray", "/opt/vps-panel/realm",
		"/etc/vps-panel/xray", "/etc/vps-panel/realm",
	} {
		commands = strings.ReplaceAll(commands, path, `"${TEST_ROOT}`+path+`"`)
	}
	for _, test := range []struct {
		name          string
		xrayExisting  bool
		realmExisting bool
	}{
		{"all missing", false, false},
		{"Realm exists, Xray missing", false, true},
		{"Xray and Realm exist", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "etc", "vps-panel-agent", "config.json")
			if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
				t.Fatal(err)
			}
			originalConfig := []byte(`{"agent_token":"existing-credential"}`)
			if err := os.WriteFile(configPath, originalConfig, 0o600); err != nil {
				t.Fatal(err)
			}
			existingFiles := map[string][]byte{}
			for _, existing := range []struct {
				name string
				yes  bool
			}{
				{"xray", test.xrayExisting}, {"realm", test.realmExisting},
			} {
				if !existing.yes {
					continue
				}
				for _, path := range []string{
					filepath.Join(root, "etc", "vps-panel", existing.name, "config.json"),
					filepath.Join(root, "opt", "vps-panel", existing.name, "managed-core"),
				} {
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					content := []byte(existing.name + " existing managed file")
					if err := os.WriteFile(path, content, 0o600); err != nil {
						t.Fatal(err)
					}
					existingFiles[path] = content
				}
			}
			script := "set -eu\nAGENT_DIR=\"${TEST_ROOT}/opt/vps-panel/agent\"\n" + commands
			for range 2 {
				command := exec.Command(sh, "-c", script)
				command.Env = append(os.Environ(), "TEST_ROOT="+root)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("prepare sandbox directories: %v, output %q", err, output)
				}
			}
			for _, directory := range []struct {
				path string
				mode os.FileMode
			}{
				{filepath.Join(root, "opt", "vps-panel", "agent"), 0o755},
				{filepath.Join(root, "opt", "vps-panel", "xray"), 0o755},
				{filepath.Join(root, "opt", "vps-panel", "realm"), 0o755},
				{filepath.Join(root, "etc", "vps-panel", "xray"), 0o700},
				{filepath.Join(root, "etc", "vps-panel", "realm"), 0o700},
			} {
				info, err := os.Stat(directory.path)
				if err != nil || !info.IsDir() || info.Mode().Perm() != directory.mode {
					t.Fatalf("directory %s = (%v, %v), want mode %o", directory.path, info, err, directory.mode)
				}
			}
			if content, err := os.ReadFile(configPath); err != nil || !bytes.Equal(content, originalConfig) {
				t.Fatalf("Agent config changed during upgrade directory preparation: %v", err)
			}
			for path, want := range existingFiles {
				if content, err := os.ReadFile(path); err != nil || !bytes.Equal(content, want) {
					t.Fatalf("existing managed config %s changed: %v", path, err)
				}
			}
		})
	}
}

func TestUpgradeAgentRestartFailureDiagnosesAndRestoresPreviousFiles(t *testing.T) {
	sh := upgradeTestShell(t)
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	oldBinary := []byte("previous Agent binary")
	oldService := []byte("previous systemd service")
	config := []byte(`{"agent_token":"keep-this-token"}`)
	for path, content := range map[string][]byte{
		filepath.Join(backup, "previous-agent"):   oldBinary,
		filepath.Join(backup, "previous-service"): oldService,
		filepath.Join(root, "agent"):              []byte("new Agent binary"),
		filepath.Join(root, "service"):            []byte("new systemd service"),
		filepath.Join(root, "config.json"):        config,
	} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	restart := upgradeScriptSection(t, upgradeAgentScript, "restart_service() {", "\nrollback() {")
	rollback := upgradeScriptSection(t, upgradeAgentScript, "rollback() {", "\non_exit() {")
	onExit := upgradeScriptSection(t, upgradeAgentScript, "on_exit() {", "\n[ \"$(id -u)\" -eq 0 ]")
	script := `set -eu
SERVICE_NAME=vps-panel-agent
init_system=systemd
BINARY_PATH="${TEST_ROOT}/agent"
service_file="${TEST_ROOT}/service"
temporary_dir="${TEST_ROOT}/backup"
had_binary=true
had_service=true
changed=true
restart_count=0
systemctl() {
  printf '%s\n' "$*" >> "$TRACE_FILE"
  case "$1" in
    restart)
      restart_count=$((restart_count + 1))
      [ "$restart_count" -gt 1 ]
      ;;
    status)
      printf 'status=226/NAMESPACE\n' >&2
      return 1
      ;;
    *) return 0 ;;
  esac
}
cleanup() { :; }
log() { printf '[vps-panel-agent] %s\n' "$*"; }
` + restart + rollback + onExit + `
trap on_exit 0
restart_service
changed=false
`
	tracePath := filepath.Join(root, "systemctl-commands")
	command := exec.Command(sh, "-c", script)
	command.Env = append(os.Environ(), "TEST_ROOT="+root, "TRACE_FILE="+tracePath)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("failed service restart unexpectedly completed upgrade")
	}
	for _, diagnostic := range []string{
		"status=226/NAMESPACE", "Agent service did not start after upgrade",
		"Upgrade failed; restoring the previous Agent",
	} {
		if !bytes.Contains(output, []byte(diagnostic)) {
			t.Fatalf("missing %q from restart failure output %q", diagnostic, output)
		}
	}
	for path, want := range map[string][]byte{
		filepath.Join(root, "agent"):       oldBinary,
		filepath.Join(root, "service"):     oldService,
		filepath.Join(root, "config.json"): config,
	} {
		if content, err := os.ReadFile(path); err != nil || !bytes.Equal(content, want) {
			t.Fatalf("rollback did not preserve %s: %v", path, err)
		}
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(trace, []byte("restart vps-panel-agent.service")) != 2 ||
		!bytes.Contains(trace, []byte("status vps-panel-agent.service --no-pager --lines=0")) {
		t.Fatalf("failed upgrade service commands = %q, want diagnosis and restart of restored service", trace)
	}
}

func TestUpgradeAgentRestartSystemdAndOpenRCSuccess(t *testing.T) {
	sh := upgradeTestShell(t)
	restart := upgradeScriptSection(t, upgradeAgentScript, "restart_service() {", "\nrollback() {")
	for _, test := range []struct {
		name string
		want string
	}{
		{"systemd", "systemctl restart vps-panel-agent.service"},
		{"OpenRC", "rc-service vps-panel-agent restart"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			tracePath := filepath.Join(root, "commands")
			mockDir := filepath.Join(root, "mock-bin")
			if err := os.Mkdir(mockDir, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"systemctl", "rc-service", "rc-update"} {
				mock := "#!/bin/sh\nprintf '" + name + " %s\\n' \"$*\" >> \"$TRACE_FILE\"\nexit 0\n"
				if err := os.WriteFile(filepath.Join(mockDir, name), []byte(mock), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			init := "systemd"
			if test.name == "OpenRC" {
				init = "openrc"
			}
			script := "set -eu\nSERVICE_NAME=vps-panel-agent\ninit_system=" + init + "\n" + restart + "\nrestart_service\n"
			command := exec.Command(sh, "-c", script)
			command.Env = append(os.Environ(), "PATH="+mockDir+string(os.PathListSeparator)+os.Getenv("PATH"), "TRACE_FILE="+tracePath)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("restart %s service: %v, output %q", test.name, err, output)
			}
			trace, err := os.ReadFile(tracePath)
			if err != nil || !bytes.Contains(trace, []byte(test.want)) {
				t.Fatalf("%s commands = (%q, %v), want %q", test.name, trace, err, test.want)
			}
		})
	}
}
