package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentSystemdSandboxMigrationInstallsDropInAndRunsOnlyOnce(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()

	root := t.TempDir()
	agentSystemdDropInPath = filepath.Join(root, "etc", "systemd", "system", "vps-panel-agent.service.d", "realm.conf")
	unitPath := filepath.Join(root, "etc", "systemd", "system", "vps-panel-agent.service")
	oldUnit := []byte("[Service]\nProtectSystem=strict\nReadWritePaths=/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /etc/systemd/system\n")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, oldUnit, 0o644); err != nil {
		t.Fatal(err)
	}
	detectAgentMigrationEnvironment = func() (hostEnvironment, error) {
		return hostEnvironment{InitSystem: initSystemSystemd, Libc: libcGlibc}, nil
	}
	oldPaths := "/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /etc/systemd/system\n"
	currentPaths := oldPaths
	var commands []string
	runAgentMigrationCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		command := strings.Join(append([]string{name}, arguments...), " ")
		commands = append(commands, command)
		if command == "systemctl daemon-reload" {
			currentPaths = oldPaths + "/opt/vps-panel/realm /etc/vps-panel/realm\n"
			return nil, nil
		}
		return []byte(currentPaths), nil
	}
	restarts := 0
	scheduleAgentMigrationRestart = func() error {
		restarts++
		return nil
	}

	configPath := filepath.Join(root, "etc", "vps-panel-agent", "config.json")
	configContents := []byte(`{"panel_url":"https://panel.example","server_id":1,"agent_id":2,"agent_token":"secret"}`)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configContents, 0o600); err != nil {
		t.Fatal(err)
	}

	migrated, err := migrateAgentSystemdSandbox()
	if err != nil || !migrated || restarts != 1 {
		t.Fatalf("first migration = (%v, %v), restarts = %d, commands = %v", migrated, err, restarts, commands)
	}
	dropIn, err := os.ReadFile(agentSystemdDropInPath)
	if err != nil || string(dropIn) != agentRealmSandboxDropIn {
		t.Fatalf("migration drop-in = %q, %v", dropIn, err)
	}
	if strings.Contains(string(dropIn), "ProtectSystem=false") ||
		strings.Contains(string(dropIn), "ReadWritePaths=/opt\n") ||
		strings.Contains(string(dropIn), "ReadWritePaths=/etc\n") {
		t.Fatalf("migration weakened systemd sandbox: %q", dropIn)
	}
	if current, err := os.ReadFile(configPath); err != nil || string(current) != string(configContents) {
		t.Fatalf("Agent config changed during migration: %q, %v", current, err)
	}
	if current, err := os.ReadFile(unitPath); err != nil || string(current) != string(oldUnit) ||
		!strings.Contains(string(current), "ProtectSystem=strict") {
		t.Fatalf("canonical Agent unit changed during migration: %q, %v", current, err)
	}

	migrated, err = migrateAgentSystemdSandbox()
	if err != nil || migrated || restarts != 1 {
		t.Fatalf("second migration = (%v, %v), restarts = %d", migrated, err, restarts)
	}
}

func TestAgentSystemdSandboxMigrationNoOpsWhenAlreadyCompatible(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()

	agentSystemdDropInPath = filepath.Join(t.TempDir(), "realm.conf")
	detectAgentMigrationEnvironment = func() (hostEnvironment, error) {
		return hostEnvironment{InitSystem: initSystemSystemd, Libc: libcGlibc}, nil
	}
	runAgentMigrationCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("/opt/vps-panel/agent /opt/vps-panel/realm /etc/vps-panel/realm /etc/systemd/system\n"), nil
	}
	scheduleAgentMigrationRestart = func() error {
		t.Fatal("compatible systemd sandbox scheduled a restart")
		return nil
	}

	migrated, err := migrateAgentSystemdSandbox()
	if err != nil || migrated {
		t.Fatalf("compatible migration = (%v, %v)", migrated, err)
	}
	if _, err := os.Stat(agentSystemdDropInPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compatible migration created a drop-in: %v", err)
	}
}

func TestAgentSystemdSandboxMigrationSkipsOpenRC(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()

	agentSystemdDropInPath = filepath.Join(t.TempDir(), "realm.conf")
	detectAgentMigrationEnvironment = func() (hostEnvironment, error) {
		return hostEnvironment{InitSystem: initSystemOpenRC, Libc: libcMusl}, nil
	}
	runAgentMigrationCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("OpenRC migration ran a systemd command")
		return nil, nil
	}
	scheduleAgentMigrationRestart = func() error {
		t.Fatal("OpenRC migration scheduled a systemd restart")
		return nil
	}

	migrated, err := migrateAgentSystemdSandbox()
	if err != nil || migrated {
		t.Fatalf("OpenRC migration = (%v, %v)", migrated, err)
	}
}

func TestAgentSystemdSandboxMigrationReportsWriteAndReloadFailures(t *testing.T) {
	t.Run("write", func(t *testing.T) {
		restore := replaceAgentMigrationDependencies(t)
		defer restore()
		root := t.TempDir()
		blocked := filepath.Join(root, "blocked")
		if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		agentSystemdDropInPath = filepath.Join(blocked, "realm.conf")
		detectAgentMigrationEnvironment = systemdMigrationTestEnvironment
		runAgentMigrationCommand = oldSystemdMigrationTestCommand
		if _, err := migrateAgentSystemdSandbox(); err == nil || !strings.Contains(err.Error(), "prepare Agent systemd drop-in directory") {
			t.Fatalf("write migration error = %v", err)
		}
	})

	t.Run("daemon-reload", func(t *testing.T) {
		restore := replaceAgentMigrationDependencies(t)
		defer restore()
		agentSystemdDropInPath = filepath.Join(t.TempDir(), "service.d", "realm.conf")
		detectAgentMigrationEnvironment = systemdMigrationTestEnvironment
		runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			if len(arguments) != 0 && arguments[0] == "daemon-reload" {
				return []byte("reload denied"), errors.New("permission denied")
			}
			return []byte("/opt/vps-panel/agent /etc/systemd/system\n"), nil
		}
		if _, err := migrateAgentSystemdSandbox(); err == nil ||
			!strings.Contains(err.Error(), "reload systemd manager") || !strings.Contains(err.Error(), "reload denied") {
			t.Fatalf("reload migration error = %v", err)
		}
	})

	t.Run("restart scheduling", func(t *testing.T) {
		restore := replaceAgentMigrationDependencies(t)
		defer restore()
		agentSystemdDropInPath = filepath.Join(t.TempDir(), "service.d", "realm.conf")
		detectAgentMigrationEnvironment = systemdMigrationTestEnvironment
		reloaded := false
		runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			if len(arguments) != 0 && arguments[0] == "daemon-reload" {
				reloaded = true
				return nil, nil
			}
			if reloaded {
				return []byte("/opt/vps-panel/agent /opt/vps-panel/realm /etc/vps-panel/realm /etc/systemd/system\n"), nil
			}
			return []byte("/opt/vps-panel/agent /etc/systemd/system\n"), nil
		}
		scheduleAgentMigrationRestart = func() error {
			return errors.New("systemd-run failed")
		}
		if _, err := migrateAgentSystemdSandbox(); err == nil || !strings.Contains(err.Error(), "schedule Agent systemd restart") {
			t.Fatalf("restart scheduling migration error = %v", err)
		}
	})
}

func TestAgentSystemdMigrationRestartUsesTransientSystemdUnit(t *testing.T) {
	originalLauncher := runUpgradeLauncher
	t.Cleanup(func() { runUpgradeLauncher = originalLauncher })
	var name string
	var arguments []string
	runUpgradeLauncher = func(command string, values ...string) ([]byte, error) {
		name = command
		arguments = append([]string(nil), values...)
		return nil, nil
	}
	if err := launchAgentSystemdMigrationRestart(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	if name != "systemd-run" || !strings.Contains(joined, "--collect") ||
		!strings.Contains(joined, agentSystemdMigrationRestartCommand) {
		t.Fatalf("migration restart helper = %q %v", name, arguments)
	}
}

func replaceAgentMigrationDependencies(t *testing.T) func() {
	t.Helper()
	originalPath := agentSystemdDropInPath
	originalDetect := detectAgentMigrationEnvironment
	originalRun := runAgentMigrationCommand
	originalSchedule := scheduleAgentMigrationRestart
	return func() {
		agentSystemdDropInPath = originalPath
		detectAgentMigrationEnvironment = originalDetect
		runAgentMigrationCommand = originalRun
		scheduleAgentMigrationRestart = originalSchedule
	}
}

func systemdMigrationTestEnvironment() (hostEnvironment, error) {
	return hostEnvironment{InitSystem: initSystemSystemd, Libc: libcGlibc}, nil
}

func oldSystemdMigrationTestCommand(context.Context, string, ...string) ([]byte, error) {
	return []byte("/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /etc/systemd/system\n"), nil
}
