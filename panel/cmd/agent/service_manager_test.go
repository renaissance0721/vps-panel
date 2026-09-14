package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSystemdServiceManagerRendersAndRunsLifecycle(t *testing.T) {
	recorder := &serviceCommandRecorder{active: true}
	root := t.TempDir()
	manager := newServiceManager(initSystemSystemd, testServiceDefinition(), root, recorder.run)
	changed, err := manager.Install()
	if err != nil || !changed {
		t.Fatalf("install systemd service = (%v, %v)", changed, err)
	}
	content, err := os.ReadFile(manager.Path())
	if err != nil || !strings.Contains(string(content), "ExecStart=/opt/vps-panel/test/test --serve /etc/vps-panel/test.json") ||
		!strings.Contains(string(content), "ProtectSystem=strict") {
		t.Fatalf("systemd service = %q, %v", content, err)
	}
	if err := manager.DaemonReload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Restart(t.Context()); err != nil {
		t.Fatal(err)
	}
	if active, err := manager.IsActive(t.Context()); err != nil || !active {
		t.Fatalf("systemd active = (%v, %v)", active, err)
	}
	want := []string{
		"systemctl daemon-reload",
		"systemctl enable vps-panel-test.service",
		"systemctl restart vps-panel-test.service",
		"systemctl is-active --quiet vps-panel-test.service",
	}
	if strings.Join(recorder.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("systemd calls = %v", recorder.calls)
	}
	if manager.Path() != filepath.Join(root, "etc", "systemd", "system", "vps-panel-test.service") {
		t.Fatalf("systemd path = %q", manager.Path())
	}
}

func TestOpenRCServiceManagerRendersSupervisedServiceAndRunsLifecycle(t *testing.T) {
	recorder := &serviceCommandRecorder{active: true}
	root := t.TempDir()
	manager := newServiceManager(initSystemOpenRC, testServiceDefinition(), root, recorder.run)
	changed, err := manager.Install()
	if err != nil || !changed {
		t.Fatalf("install OpenRC service = (%v, %v)", changed, err)
	}
	content, err := os.ReadFile(manager.Path())
	text := string(content)
	for _, expected := range []string{
		"#!/sbin/openrc-run", `supervisor="supervise-daemon"`, `command="/opt/vps-panel/test/test"`,
		`command_args="--serve /etc/vps-panel/test.json"`, "respawn_delay=3", "respawn_max=0", "need net",
	} {
		if err != nil || !strings.Contains(text, expected) {
			t.Fatalf("OpenRC service missing %q: %q, %v", expected, text, err)
		}
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(manager.Path()); err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("OpenRC service mode = %v, %v", infoMode(info), err)
		}
	}
	if err := manager.DaemonReload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Enable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Restart(t.Context()); err != nil {
		t.Fatal(err)
	}
	if active, err := manager.IsActive(t.Context()); err != nil || !active {
		t.Fatalf("OpenRC active = (%v, %v)", active, err)
	}
	if err := manager.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Disable(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"rc-update add vps-panel-test default",
		"rc-service vps-panel-test restart",
		"rc-service vps-panel-test status",
		"rc-service vps-panel-test stop",
		"rc-update del vps-panel-test default",
	}
	if strings.Join(recorder.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("OpenRC calls = %v", recorder.calls)
	}
	if manager.Path() != filepath.Join(root, "etc", "init.d", "vps-panel-test") {
		t.Fatalf("OpenRC path = %q", manager.Path())
	}
}

func testServiceDefinition() serviceDefinition {
	return serviceDefinition{
		Name:        "vps-panel-test",
		Description: "VPS Panel Test",
		Command:     "/opt/vps-panel/test/test",
		Arguments:   []string{"--serve", "/etc/vps-panel/test.json"},
	}
}

type serviceCommandRecorder struct {
	calls  []string
	active bool
}

func (r *serviceCommandRecorder) run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(append([]string{name}, arguments...), " "))
	if (name == "systemctl" && len(arguments) > 0 && arguments[0] == "is-active") ||
		(name == "rc-service" && len(arguments) > 1 && arguments[1] == "status") {
		if !r.active {
			return []byte("inactive"), errors.New("inactive")
		}
	}
	return nil, nil
}
