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

func TestAgentSystemdSandboxMigrationInstallsDropInAndRunsOnlyOnce(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()

	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
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
			currentPaths = oldPaths + "/opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme /var/lib/vps-panel/agent\n"
			return nil, nil
		}
		return []byte(currentPaths), nil
	}
	restarts := 0
	detectUpgradeHostEnvironment = systemdMigrationTestEnvironment
	runAgentServiceCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if name != "systemctl" || len(arguments) == 0 ||
			(arguments[0] != "restart" && arguments[0] != "is-active") {
			t.Fatalf("unexpected Agent service command: %s %v", name, arguments)
		}
		return nil, nil
	}
	waitForAgentMigrationRestart = func() {}
	scheduleAgentMigrationRestart = func(backup string) error {
		restarts++
		return runAgentSystemdMigrationRestart([]string{"--backup", backup})
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
	if err != nil || string(dropIn) != agentSystemdSandboxDropIn {
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
	for _, path := range []string{"/opt/vps-panel/realm", "/etc/vps-panel/realm", "/opt/vps-panel/xray", "/etc/vps-panel/xray", "/opt/vps-panel/acme", "/var/lib/vps-panel/acme", "/var/lib/vps-panel/agent"} {
		if info, err := os.Stat(rootedPath(root, path)); err != nil || !info.IsDir() {
			t.Fatalf("migration directory %s = (%v, %v)", path, info, err)
		}
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(rootedPath(root, "/var/lib/vps-panel/agent")); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("Agent state directory mode = (%v, %v), want 0700", info, err)
		}
	}

	migrated, err = migrateAgentSystemdSandbox()
	if err != nil || migrated || restarts != 1 {
		t.Fatalf("second migration = (%v, %v), restarts = %d", migrated, err, restarts)
	}
}

func TestAgentSystemdSandboxMigrationNoOpsWhenAlreadyCompatible(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()

	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
	if err := prepareAgentSystemdDirectories(); err != nil {
		t.Fatal(err)
	}
	agentSystemdDropInPath = filepath.Join(root, "realm.conf")
	detectAgentMigrationEnvironment = func() (hostEnvironment, error) {
		return hostEnvironment{InitSystem: initSystemSystemd, Libc: libcGlibc}, nil
	}
	runAgentMigrationCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("/opt/vps-panel/agent /opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme /var/lib/vps-panel/agent /etc/systemd/system\n"), nil
	}
	scheduleAgentMigrationRestart = func(string) error {
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
	scheduleAgentMigrationRestart = func(string) error {
		t.Fatal("OpenRC migration scheduled a systemd restart")
		return nil
	}

	migrated, err := migrateAgentSystemdSandbox()
	if err != nil || migrated {
		t.Fatalf("OpenRC migration = (%v, %v)", migrated, err)
	}
}

func TestAgentSystemdSandboxExtendsExistingRealmDropInForACMEAndAgentState(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentSystemdDropInPath = filepath.Join(root, "etc", "systemd", "system", "vps-panel-agent.service.d", "realm.conf")
	if err := os.MkdirAll(filepath.Dir(agentSystemdDropInPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentSystemdDropInPath, []byte(agentLegacyRealmSandboxDropIn), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := "/opt/vps-panel/realm /etc/vps-panel/realm"
	runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "daemon-reload" {
			paths += " /opt/vps-panel/acme /var/lib/vps-panel/acme /var/lib/vps-panel/agent"
			return nil, nil
		}
		return []byte(paths), nil
	}
	if _, err := applyAgentSystemdSandboxMigration(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(agentSystemdDropInPath)
	if err != nil || string(contents) != agentSystemdSandboxDropIn {
		t.Fatalf("Agent sandbox drop-in = %q, %v", contents, err)
	}
	for _, directory := range []string{"/opt/vps-panel/acme", "/var/lib/vps-panel/acme", "/var/lib/vps-panel/agent"} {
		if info, err := os.Stat(rootedPath(root, directory)); err != nil || !info.IsDir() {
			t.Fatalf("Agent sandbox directory %s = %v, %v", directory, info, err)
		}
	}
}

func TestAgentSystemdSandboxExtendsExistingRealmAndACMEDropInForAgentState(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentSystemdDropInPath = filepath.Join(root, "service.d", "realm.conf")
	if err := os.MkdirAll(filepath.Dir(agentSystemdDropInPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentSystemdDropInPath, []byte(agentRealmSandboxDropIn), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := "/opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme"
	runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "daemon-reload" {
			paths += " /var/lib/vps-panel/agent"
			return nil, nil
		}
		return []byte(paths), nil
	}
	if _, err := applyAgentSystemdSandboxMigration(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(agentSystemdDropInPath)
	if err != nil || string(contents) != agentSystemdSandboxDropIn {
		t.Fatalf("Agent state sandbox drop-in = %q, %v", contents, err)
	}
}

func TestAgentSystemdSandboxRejectsUnknownManagedDropIn(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentSystemdDropInPath = filepath.Join(root, "service.d", "realm.conf")
	if err := os.MkdirAll(filepath.Dir(agentSystemdDropInPath), 0o755); err != nil {
		t.Fatal(err)
	}
	unknown := []byte("[Service]\nReadWritePaths=/srv/custom\n")
	if err := os.WriteFile(agentSystemdDropInPath, unknown, 0o644); err != nil {
		t.Fatal(err)
	}
	runAgentMigrationCommand = oldSystemdMigrationTestCommand
	if _, err := applyAgentSystemdSandboxMigration(); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("unknown drop-in migration error = %v", err)
	}
	contents, err := os.ReadFile(agentSystemdDropInPath)
	if err != nil || string(contents) != string(unknown) {
		t.Fatalf("unknown drop-in changed = %q, %v", contents, err)
	}
}

func TestStagedAgentVersionCheckSkipsSystemdBootstrapOnOpenRC(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	agentManagedDir = t.TempDir()
	staged := filepath.Join(agentManagedDir, ".agent-upgrade-openrc")
	detectAgentMigrationEnvironment = func() (hostEnvironment, error) {
		return hostEnvironment{InitSystem: initSystemOpenRC, Libc: libcMusl}, nil
	}
	runAgentMigrationCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("OpenRC version check inspected systemd")
		return nil, nil
	}
	originalLauncher := runUpgradeLauncher
	defer func() { runUpgradeLauncher = originalLauncher }()
	runUpgradeLauncher = func(string, ...string) ([]byte, error) {
		t.Fatal("OpenRC version check launched a systemd helper")
		return nil, nil
	}
	if err := prepareStagedAgentSystemdSandbox(staged); err != nil {
		t.Fatal(err)
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
		agentSystemdMigrationRoot = root
		runAgentMigrationCommand = oldSystemdMigrationTestCommand
		if _, err := applyAgentSystemdSandboxMigration(); err == nil || !strings.Contains(err.Error(), "prepare Agent systemd drop-in directory") {
			t.Fatalf("write migration error = %v", err)
		}
	})

	t.Run("daemon-reload", func(t *testing.T) {
		restore := replaceAgentMigrationDependencies(t)
		defer restore()
		root := t.TempDir()
		agentSystemdMigrationRoot = root
		agentSystemdDropInPath = filepath.Join(root, "service.d", "realm.conf")
		runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			if len(arguments) != 0 && arguments[0] == "daemon-reload" {
				return []byte("reload denied"), errors.New("permission denied")
			}
			return []byte("/opt/vps-panel/agent /etc/systemd/system\n"), nil
		}
		if _, err := applyAgentSystemdSandboxMigration(); err == nil ||
			!strings.Contains(err.Error(), "reload systemd manager") || !strings.Contains(err.Error(), "reload denied") {
			t.Fatalf("reload migration error = %v", err)
		}
	})

	t.Run("restart scheduling", func(t *testing.T) {
		restore := replaceAgentMigrationDependencies(t)
		defer restore()
		root := t.TempDir()
		agentSystemdMigrationRoot = root
		agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
		agentSystemdDropInPath = filepath.Join(root, "service.d", "realm.conf")
		detectAgentMigrationEnvironment = systemdMigrationTestEnvironment
		reloaded := false
		runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			if len(arguments) != 0 && arguments[0] == "daemon-reload" {
				reloaded = true
				return nil, nil
			}
			if reloaded {
				return []byte("/opt/vps-panel/agent /opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme /var/lib/vps-panel/agent /etc/systemd/system\n"), nil
			}
			return []byte("/opt/vps-panel/agent /etc/systemd/system\n"), nil
		}
		scheduleAgentMigrationRestart = func(string) error {
			return errors.New("systemd-run failed")
		}
		if _, err := migrateAgentSystemdSandbox(); err == nil || !strings.Contains(err.Error(), "schedule Agent systemd restart") {
			t.Fatalf("restart scheduling migration error = %v", err)
		}
	})
}

func TestStagedSystemdBootstrapFailureRestoresManagedDropIn(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentSystemdDropInPath = filepath.Join(root, "service.d", "realm.conf")
	reloads := 0
	runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "daemon-reload" {
			reloads++
			if reloads == 1 {
				return []byte("reload failed"), errors.New("systemd unavailable")
			}
			return nil, nil
		}
		return []byte("/opt/vps-panel/agent /etc/systemd/system"), nil
	}
	if err := run([]string{"_prepare-systemd-sandbox"}); err == nil || !strings.Contains(err.Error(), "reload failed") {
		t.Fatalf("bootstrap error = %v", err)
	}
	if reloads != 2 {
		t.Fatalf("bootstrap rollback daemon-reloads = %d, want 2", reloads)
	}
	if _, err := os.Stat(agentSystemdDropInPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed bootstrap left managed drop-in: %v", err)
	}
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
	if err := launchAgentSystemdMigrationRestart(""); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	if name != "systemd-run" || !strings.Contains(joined, "--wait") || !strings.Contains(joined, "--collect") ||
		!strings.Contains(joined, agentSystemdMigrationRestartCommand) {
		t.Fatalf("migration restart helper = %q %v", name, arguments)
	}
}

func TestAgentSystemdMigrationDefersHostWritesToTransientHelper(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
	agentSystemdDropInPath = filepath.Join(root, "etc", "systemd", "system", "vps-panel-agent.service.d", "realm.conf")
	detectAgentMigrationEnvironment = systemdMigrationTestEnvironment
	runAgentMigrationCommand = oldSystemdMigrationTestCommand
	scheduled := 0
	scheduleAgentMigrationRestart = func(string) error {
		scheduled++
		if _, err := os.Stat(agentSystemdDropInPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old sandbox wrote drop-in before transient helper: %v", err)
		}
		if _, err := os.Stat(rootedPath(root, "/etc/vps-panel/realm")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old sandbox created Realm directory: %v", err)
		}
		return nil
	}
	if migrated, err := migrateAgentSystemdSandbox(); err != nil || !migrated || scheduled != 1 {
		t.Fatalf("legacy migration = (%v, %v), helpers = %d", migrated, err, scheduled)
	}
}

func TestAgentSystemdMigrationPreparesMissingDirectoriesWithoutOverwritingConfig(t *testing.T) {
	for _, existingRealm := range []string{"none", "opt", "etc", "both"} {
		t.Run(existingRealm, func(t *testing.T) {
			restore := replaceAgentMigrationDependencies(t)
			defer restore()
			root := t.TempDir()
			agentSystemdMigrationRoot = root
			agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
			agentSystemdDropInPath = filepath.Join(root, "etc", "systemd", "system", "vps-panel-agent.service.d", "realm.conf")
			for _, path := range []string{"/etc/vps-panel-agent/config.json", "/opt/vps-panel/xray/config.json"} {
				full := rootedPath(root, path)
				if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("preserve "+path), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{"/opt/vps-panel/realm", "/etc/vps-panel/realm"} {
				if existingRealm == "both" || (existingRealm == "opt" && strings.HasPrefix(path, "/opt")) ||
					(existingRealm == "etc" && strings.HasPrefix(path, "/etc")) {
					full := rootedPath(root, path)
					if err := os.MkdirAll(full, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(full, "existing.conf"), []byte("preserve Realm"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			paths := "/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /etc/systemd/system"
			runAgentMigrationCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if len(args) > 0 && args[0] == "daemon-reload" {
					paths += " /opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme /var/lib/vps-panel/agent"
					return nil, nil
				}
				return []byte(paths), nil
			}
			if _, err := applyAgentSystemdSandboxMigration(); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/etc/vps-panel-agent/config.json", "/opt/vps-panel/xray/config.json"} {
				full := rootedPath(root, path)
				if value, err := os.ReadFile(full); err != nil || string(value) != "preserve "+path {
					t.Fatalf("existing config %s changed: (%q, %v)", path, value, err)
				}
			}
			for _, path := range []string{"/opt/vps-panel/realm", "/etc/vps-panel/realm"} {
				full := rootedPath(root, path)
				if info, err := os.Stat(full); err != nil || !info.IsDir() {
					t.Fatalf("Realm directory %s = (%v, %v)", path, info, err)
				}
				if existing, err := os.ReadFile(filepath.Join(full, "existing.conf")); err == nil && string(existing) != "preserve Realm" {
					t.Fatalf("existing Realm config changed: %q", existing)
				}
			}
		})
	}
}

func TestAgentSystemdMigrationWithCompatibleUnitStillCreatesMissingDirectories(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
	agentSystemdDropInPath = filepath.Join(root, "realm.conf")
	detectAgentMigrationEnvironment = systemdMigrationTestEnvironment
	runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if len(arguments) != 0 && arguments[0] == "daemon-reload" {
			t.Fatal("compatible unit was rewritten")
		}
		return []byte("/opt/vps-panel/agent /opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme /var/lib/vps-panel/agent"), nil
	}
	scheduleAgentMigrationRestart = func(backup string) error {
		_, err := applyAgentSystemdSandboxMigration()
		return err
	}
	if migrated, err := migrateAgentSystemdSandbox(); err != nil || !migrated {
		t.Fatalf("missing-directory migration = (%v, %v)", migrated, err)
	}
	if _, err := os.Stat(agentSystemdDropInPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compatible unit acquired unnecessary drop-in: %v", err)
	}
}

func TestAgentSystemdMigrationPreservesOldBinaryForRollback(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
	agentManagedPath = filepath.Join(agentManagedDir, "vps-panel-agent")
	agentSystemdDropInPath = filepath.Join(root, "etc", "systemd", "system", "vps-panel-agent.service.d", "realm.conf")
	if err := os.MkdirAll(agentManagedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentManagedPath, []byte("new Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(agentManagedDir, ".agent-rollback-legacy")
	if err := os.WriteFile(legacy, []byte("old Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := rootedPath(root, "/etc/vps-panel-agent/config.json")
	configContents := []byte(`{"panel_url":"https://panel.example","server_id":10,"agent_id":12,"agent_token":"unchanged"}`)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configContents, 0o600); err != nil {
		t.Fatal(err)
	}
	backup, err := preserveLegacyAgentRollback()
	if err != nil || backup == "" {
		t.Fatalf("preserve old Agent = (%q, %v)", backup, err)
	}
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	if value, err := os.ReadFile(backup); err != nil || string(value) != "old Agent" {
		t.Fatalf("old helper removed backup identity: (%q, %v)", value, err)
	}
	detectUpgradeHostEnvironment = systemdMigrationTestEnvironment
	waitForAgentMigrationRestart = func() {}
	paths := "/opt/vps-panel/agent /etc/systemd/system"
	runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "daemon-reload" {
			paths += " /opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme /var/lib/vps-panel/agent"
			return nil, nil
		}
		return []byte(paths), nil
	}
	restarts := 0
	runAgentServiceCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if name != "systemctl" {
			t.Fatalf("unexpected service command: %s %v", name, arguments)
		}
		if len(arguments) > 0 && arguments[0] == "restart" {
			restarts++
			if restarts == 1 {
				return []byte("new Agent failed"), errors.New("restart failed")
			}
		}
		return nil, nil
	}
	if err := runAgentSystemdMigrationRestart([]string{"--backup", backup}); err == nil || !strings.Contains(err.Error(), "new Agent failed") {
		t.Fatalf("failed migration restart error = %v", err)
	}
	if value, err := os.ReadFile(agentManagedPath); err != nil || string(value) != "old Agent" || restarts != 2 {
		t.Fatalf("rollback = (%q, %v), restarts %d", value, err, restarts)
	}
	if _, err := os.Stat(agentSystemdDropInPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed migration left managed drop-in: %v", err)
	}
	if current, err := os.ReadFile(configPath); err != nil || string(current) != string(configContents) {
		t.Fatalf("legacy Agent config changed during rollback: (%q, %v)", current, err)
	}
}

func TestSuccessfulSystemdMigrationKeepsStagedBackupUntilAgentHeartbeat(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
	agentManagedPath = filepath.Join(agentManagedDir, "vps-panel-agent")
	backup := filepath.Join(agentManagedDir, ".agent-migration-backup")
	if err := os.MkdirAll(agentManagedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("old Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentManagedPath, []byte("new Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	detectUpgradeHostEnvironment = systemdMigrationTestEnvironment
	waitForAgentMigrationRestart = func() {}
	runAgentMigrationCommand = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("/opt/vps-panel/agent /opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme /var/lib/vps-panel/agent"), nil
	}
	runAgentServiceCommand = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	if err := runAgentSystemdMigrationRestart([]string{"--backup", backup}); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(backup); err != nil || string(current) != "old Agent" {
		t.Fatalf("helper removed rollback backup before Agent heartbeat: (%q, %v)", current, err)
	}
}

func TestLegacyStagedVersionCheckPreservesOldAgentBeforeReplacement(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	agentManagedDir = t.TempDir()
	agentManagedPath = filepath.Join(agentManagedDir, "vps-panel-agent")
	staged := filepath.Join(agentManagedDir, ".agent-upgrade-legacy")
	if err := os.WriteFile(agentManagedPath, []byte("old v0.16.0 Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := preserveCurrentAgentForStagedUpgrade(staged); err != nil {
		t.Fatalf("legacy version check: %v", err)
	}
	if err := os.Rename(staged, agentManagedPath); err != nil {
		t.Fatal(err)
	}
	backup, err := preserveLegacyAgentRollback()
	if err != nil || backup != filepath.Join(agentManagedDir, ".agent-migration-backup") {
		t.Fatalf("staged backup = (%q, %v)", backup, err)
	}
	if contents, err := os.ReadFile(backup); err != nil || string(contents) != "old v0.16.0 Agent" {
		t.Fatalf("old Agent after legacy replacement = (%q, %v)", contents, err)
	}
	if err := preserveCurrentAgentForStagedUpgrade(agentManagedPath); err != nil {
		t.Fatalf("normal Agent version check: %v", err)
	}
	if contents, err := os.ReadFile(backup); err != nil || string(contents) != "old v0.16.0 Agent" {
		t.Fatalf("normal version check replaced rollback: (%q, %v)", contents, err)
	}
}

func TestLegacyStagedVersionCheckBootstrapsRealmBeforeOldUpgradeHelperRestarts(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
	agentManagedPath = filepath.Join(agentManagedDir, "vps-panel-agent")
	agentSystemdDropInPath = filepath.Join(root, "etc", "systemd", "system", "vps-panel-agent.service.d", "realm.conf")
	if err := os.MkdirAll(agentManagedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldUnit := filepath.Join(root, "etc", "systemd", "system", "vps-panel-agent.service")
	if err := os.MkdirAll(filepath.Dir(oldUnit), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyContents := []byte("[Service]\nProtectSystem=strict\nReadWritePaths=/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /etc/systemd/system\n")
	if err := os.WriteFile(oldUnit, legacyContents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentManagedPath, []byte("old Agent v0.16.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(agentManagedDir, ".agent-upgrade-v019")
	if err := os.WriteFile(staged, []byte("new Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	detectAgentMigrationEnvironment = systemdMigrationTestEnvironment
	paths := "/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /etc/systemd/system"
	runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "daemon-reload" {
			paths += " /opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme /var/lib/vps-panel/agent"
			return nil, nil
		}
		return []byte(paths), nil
	}
	originalLauncher := runUpgradeLauncher
	defer func() { runUpgradeLauncher = originalLauncher }()
	helperCalls := 0
	runUpgradeLauncher = func(name string, arguments ...string) ([]byte, error) {
		helperCalls++
		if name != "systemd-run" || !strings.Contains(strings.Join(arguments, " "), "--wait") ||
			arguments[len(arguments)-2] != staged || arguments[len(arguments)-1] != "_prepare-systemd-sandbox" {
			t.Fatalf("legacy bootstrap helper = %s %v", name, arguments)
		}
		_, err := applyAgentSystemdSandboxMigration()
		return nil, err
	}
	if err := preserveCurrentAgentForStagedUpgrade(staged); err != nil {
		t.Fatal(err)
	}
	if err := prepareStagedAgentSystemdSandbox(staged); err != nil {
		t.Fatalf("v0.16.0 staged version bootstrap: %v", err)
	}
	if helperCalls != 1 {
		t.Fatalf("bootstrap helper calls = %d", helperCalls)
	}
	if ready, err := agentSystemdDirectoriesReady(); err != nil || !ready || !hasAgentWritablePaths(paths) {
		t.Fatalf("host before old restart = (dirs %v, paths %q, %v)", ready, paths, err)
	}
	if contents, err := os.ReadFile(oldUnit); err != nil || string(contents) != string(legacyContents) {
		t.Fatalf("v0.16.0 unit was replaced: (%q, %v)", contents, err)
	}
	if err := os.Rename(staged, agentManagedPath); err != nil {
		t.Fatal(err)
	}
	if backup, err := os.ReadFile(filepath.Join(agentManagedDir, ".agent-migration-backup")); err != nil || string(backup) != "old Agent v0.16.0" {
		t.Fatalf("previous Agent after replacement = (%q, %v)", backup, err)
	}
	if err := prepareStagedAgentSystemdSandbox(agentManagedPath); err != nil || helperCalls != 1 {
		t.Fatalf("normal Agent start repeated bootstrap: (%v, calls %d)", err, helperCalls)
	}
	scheduleAgentMigrationRestart = func(string) error {
		t.Fatal("preboot migration formed a restart loop")
		return nil
	}
	if migrated, err := migrateAgentSystemdSandbox(); err != nil || migrated {
		t.Fatalf("post-bootstrap Agent start = (%v, %v)", migrated, err)
	}
	if backup, err := os.ReadFile(filepath.Join(agentManagedDir, ".agent-migration-backup")); err != nil || string(backup) != "old Agent v0.16.0" {
		t.Fatalf("startup removed rollback backup before first heartbeat: (%q, %v)", backup, err)
	}
}

func TestStagedSystemdBootstrapUpgradesExistingRealmAndACMEUnit(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
	agentManagedPath = filepath.Join(agentManagedDir, "vps-panel-agent")
	agentSystemdDropInPath = filepath.Join(root, "realm.conf")
	if err := os.MkdirAll(agentManagedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(agentManagedDir, ".agent-upgrade-v019")
	detectAgentMigrationEnvironment = systemdMigrationTestEnvironment
	paths := "/opt/vps-panel/agent /opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme"
	runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "daemon-reload" {
			paths += " /var/lib/vps-panel/agent"
			return nil, nil
		}
		return []byte(paths), nil
	}
	originalLauncher := runUpgradeLauncher
	defer func() { runUpgradeLauncher = originalLauncher }()
	helperCalls := 0
	runUpgradeLauncher = func(name string, arguments ...string) ([]byte, error) {
		helperCalls++
		_, err := applyAgentSystemdSandboxMigration()
		return nil, err
	}
	if err := prepareStagedAgentSystemdSandbox(staged); err != nil {
		t.Fatal(err)
	}
	if ready, err := agentSystemdDirectoriesReady(); err != nil || !ready || !hasAgentWritablePaths(paths) {
		t.Fatalf("sandbox before restart = (directories %v, paths %q, %v)", ready, paths, err)
	}
	if contents, err := os.ReadFile(agentSystemdDropInPath); err != nil || string(contents) != agentSystemdSandboxDropIn {
		t.Fatalf("Agent state drop-in = %q, %v", contents, err)
	}
	if err := prepareStagedAgentSystemdSandbox(staged); err != nil || helperCalls != 1 {
		t.Fatalf("completed bootstrap repeated = (%v, calls %d)", err, helperCalls)
	}
}

func TestAgentSystemdMigrationLauncherFailureRestoresLegacyAgent(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
	agentManagedPath = filepath.Join(agentManagedDir, "vps-panel-agent")
	if err := os.MkdirAll(agentManagedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(agentManagedDir, ".agent-upgrade-v016")
	if err := os.WriteFile(agentManagedPath, []byte("legacy Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := preserveCurrentAgentForStagedUpgrade(staged); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, agentManagedPath); err != nil {
		t.Fatal(err)
	}
	detectAgentMigrationEnvironment = systemdMigrationTestEnvironment
	detectUpgradeHostEnvironment = systemdMigrationTestEnvironment
	runAgentMigrationCommand = oldSystemdMigrationTestCommand
	scheduleAgentMigrationRestart = func(string) error {
		return errors.New("systemd-run unavailable")
	}
	restarts := 0
	runAgentServiceCommand = func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if name != "systemctl" {
			t.Fatalf("unexpected service command: %s %v", name, arguments)
		}
		if len(arguments) > 0 && arguments[0] == "restart" {
			restarts++
		}
		return nil, nil
	}
	if migrated, err := migrateAgentSystemdSandbox(); err == nil || migrated ||
		!strings.Contains(err.Error(), "systemd-run unavailable") {
		t.Fatalf("launcher failure = (%v, %v)", migrated, err)
	}
	if current, err := os.ReadFile(agentManagedPath); err != nil || string(current) != "legacy Agent" || restarts != 1 {
		t.Fatalf("legacy rollback = (%q, %v), restarts %d", current, err, restarts)
	}
}

func TestAgentSystemdMigrationRollsBackWhenNewServiceDiesAfterInitialActiveCheck(t *testing.T) {
	restore := replaceAgentMigrationDependencies(t)
	defer restore()
	root := t.TempDir()
	agentSystemdMigrationRoot = root
	agentManagedDir = filepath.Join(root, "opt", "vps-panel", "agent")
	agentManagedPath = filepath.Join(agentManagedDir, "vps-panel-agent")
	agentSystemdDropInPath = filepath.Join(root, "etc", "systemd", "system", "vps-panel-agent.service.d", "realm.conf")
	if err := os.MkdirAll(agentManagedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentManagedPath, []byte("new Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(agentManagedDir, ".agent-migration-backup")
	if err := os.WriteFile(backup, []byte("old Agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	detectUpgradeHostEnvironment = systemdMigrationTestEnvironment
	waitForAgentMigrationRestart = func() {}
	paths := "/opt/vps-panel/agent /etc/systemd/system"
	runAgentMigrationCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "daemon-reload" {
			paths += " /opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme /var/lib/vps-panel/agent"
			return nil, nil
		}
		return []byte(paths), nil
	}
	restarts, activeChecks := 0, 0
	runAgentServiceCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if len(arguments) > 0 && arguments[0] == "restart" {
			restarts++
		}
		if len(arguments) > 0 && arguments[0] == "is-active" {
			activeChecks++
			if activeChecks == 2 {
				return []byte("inactive"), errors.New("inactive")
			}
		}
		return nil, nil
	}
	if err := runAgentSystemdMigrationRestart([]string{"--backup", backup}); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("delayed service failure = %v", err)
	}
	if current, err := os.ReadFile(agentManagedPath); err != nil || string(current) != "old Agent" || restarts != 2 || activeChecks != 3 {
		t.Fatalf("delayed rollback = (%q, %v), restarts %d checks %d", current, err, restarts, activeChecks)
	}
}

func replaceAgentMigrationDependencies(t *testing.T) func() {
	t.Helper()
	originalPath := agentSystemdDropInPath
	originalRoot := agentSystemdMigrationRoot
	originalManagedDir := agentManagedDir
	originalManagedPath := agentManagedPath
	originalDetect := detectAgentMigrationEnvironment
	originalUpgradeDetect := detectUpgradeHostEnvironment
	originalRun := runAgentMigrationCommand
	originalServiceRun := runAgentServiceCommand
	originalSchedule := scheduleAgentMigrationRestart
	originalWait := waitForAgentMigrationRestart
	return func() {
		agentSystemdDropInPath = originalPath
		agentSystemdMigrationRoot = originalRoot
		agentManagedDir = originalManagedDir
		agentManagedPath = originalManagedPath
		detectAgentMigrationEnvironment = originalDetect
		detectUpgradeHostEnvironment = originalUpgradeDetect
		runAgentMigrationCommand = originalRun
		runAgentServiceCommand = originalServiceRun
		scheduleAgentMigrationRestart = originalSchedule
		waitForAgentMigrationRestart = originalWait
	}
}

func systemdMigrationTestEnvironment() (hostEnvironment, error) {
	return hostEnvironment{InitSystem: initSystemSystemd, Libc: libcGlibc}, nil
}

func oldSystemdMigrationTestCommand(context.Context, string, ...string) ([]byte, error) {
	return []byte("/opt/vps-panel/agent /opt/vps-panel/xray /etc/vps-panel/xray /etc/systemd/system\n"), nil
}
