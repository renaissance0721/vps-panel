package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAgentSelfUninstallRemovesOnlyAgentPaths(t *testing.T) {
	for _, test := range []struct {
		name        string
		initSystem  initSystem
		unitPath    string
		wantCommand [][]string
	}{
		{
			name:       "systemd",
			initSystem: initSystemSystemd,
			unitPath:   "/etc/systemd/system/vps-panel-agent.service",
			wantCommand: [][]string{
				{"systemctl", "stop", "vps-panel-agent.service"},
				{"systemctl", "disable", "vps-panel-agent.service"},
				{"systemctl", "daemon-reload"},
			},
		},
		{
			name:       "openrc",
			initSystem: initSystemOpenRC,
			unitPath:   "/etc/init.d/vps-panel-agent",
			wantCommand: [][]string{
				{"rc-service", "vps-panel-agent", "stop"},
				{"rc-update", "del", "vps-panel-agent", "default"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			helper := filepath.Join(root, "var", "tmp", "uninstall-helper")
			for _, path := range []string{
				rootedPath(root, agentConfigDir+"/config.json"),
				rootedPath(root, agentStateDir+"/state"),
				rootedPath(root, agentManagedDir+"/vps-panel-agent"),
				rootedPath(root, test.unitPath),
				helper,
				rootedPath(root, "/etc/vps-panel/keep"),
				rootedPath(root, "/var/lib/vps-panel/keep"),
				rootedPath(root, "/opt/vps-panel/keep"),
			} {
				writeTestFile(t, path, []byte("keep or remove"), 0o600)
			}
			var calls [][]string
			previous := runSelfUninstallCommand
			runSelfUninstallCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
				calls = append(calls, append([]string{name}, arguments...))
				return nil, nil
			}
			defer func() { runSelfUninstallCommand = previous }()

			uninstaller := newAgentSelfUninstaller(hostEnvironment{InitSystem: test.initSystem}, root, helper)
			if err := uninstaller.run(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, test.wantCommand) {
				t.Fatalf("service commands = %v, want %v", calls, test.wantCommand)
			}
			if err := uninstaller.run(t.Context()); err != nil {
				t.Fatalf("repeat self-uninstall: %v", err)
			}
			if !reflect.DeepEqual(calls, test.wantCommand) {
				t.Fatalf("repeat self-uninstall changed services: %v", calls)
			}
			for _, path := range []string{
				rootedPath(root, agentConfigDir), rootedPath(root, agentStateDir), rootedPath(root, agentManagedDir),
				rootedPath(root, test.unitPath), helper,
			} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Agent path remained: %s (%v)", path, err)
				}
			}
			for _, path := range []string{
				rootedPath(root, "/etc/vps-panel/keep"),
				rootedPath(root, "/var/lib/vps-panel/keep"),
				rootedPath(root, "/opt/vps-panel/keep"),
			} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("shared root content was removed: %s (%v)", path, err)
				}
			}
		})
	}
}

func TestPrepareAgentSelfUninstallRejectsUnsupportedInitBeforeStaging(t *testing.T) {
	previousDetect := detectSelfUninstallHostEnvironment
	previousLocate := locateSelfUninstallExecutable
	detectSelfUninstallHostEnvironment = func() (hostEnvironment, error) {
		return hostEnvironment{InitSystem: initSystem("unsupported")}, nil
	}
	locateSelfUninstallExecutable = func() (string, error) {
		t.Fatal("unsupported init system staged an uninstall helper")
		return "", nil
	}
	defer func() {
		detectSelfUninstallHostEnvironment = previousDetect
		locateSelfUninstallExecutable = previousLocate
	}()
	if _, err := prepareAgentSelfUninstall(); !errors.Is(err, errUnsupportedInitSystem) {
		t.Fatalf("prepare error = %v, want errUnsupportedInitSystem", err)
	}
}
