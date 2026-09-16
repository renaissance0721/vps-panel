package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	agentSystemdMigrationRestartCommand = "_migrate-systemd-sandbox"
	agentSystemdMigrationOutputMax      = 4 << 10
	agentSystemdMigrationTimeout        = 10 * time.Second
	agentRealmSandboxDropIn             = "[Service]\nReadWritePaths=/opt/vps-panel/realm /etc/vps-panel/realm /opt/vps-panel/acme /var/lib/vps-panel/acme\n"
	agentLegacyRealmSandboxDropIn       = "[Service]\nReadWritePaths=/opt/vps-panel/realm /etc/vps-panel/realm\n"
)

var (
	agentSystemdDropInPath          = "/etc/systemd/system/vps-panel-agent.service.d/realm.conf"
	agentSystemdMigrationRoot       = ""
	detectAgentMigrationEnvironment = detectHostEnvironment
	runAgentMigrationCommand        = runSystemdMigrationCommand
	scheduleAgentMigrationRestart   = launchAgentSystemdMigrationRestart
	waitForAgentMigrationRestart    = func() { time.Sleep(3 * time.Second) }
)

func migrateAgentSystemdSandbox() (bool, error) {
	environment, err := detectAgentMigrationEnvironment()
	if err != nil {
		return false, fmt.Errorf("detect host environment: %w", err)
	}
	if environment.InitSystem != initSystemSystemd {
		os.Remove(filepath.Join(agentManagedDir, ".agent-migration-backup"))
		return false, nil
	}
	backup, err := preserveLegacyAgentRollback()
	if err != nil {
		return false, fmt.Errorf("preserve previous Agent for systemd migration: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), agentSystemdMigrationTimeout)
	defer cancel()
	writablePaths, err := currentAgentSystemdWritablePaths(ctx)
	if err != nil {
		return false, failAgentSystemdMigration(backup, err)
	}
	ready, err := agentSystemdDirectoriesReady()
	if err != nil {
		return false, failAgentSystemdMigration(backup, err)
	}
	if hasAgentRealmWritablePaths(writablePaths) && ready {
		return false, nil
	}
	if err := scheduleAgentMigrationRestart(backup); err != nil {
		return false, failAgentSystemdMigration(backup, fmt.Errorf("schedule Agent systemd restart: %w", err))
	}
	return true, nil
}

func failAgentSystemdMigration(backup string, migrationErr error) error {
	if value, err := readAgentConfig(defaultConfigPath); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		reportAgentUpgradeFailure(ctx, &http.Client{Timeout: 10 * time.Second}, value, agentVersion, migrationErr)
		cancel()
	}
	if backup != "" {
		if _, err := os.Stat(backup); err == nil {
			return rollbackAgentBinary(backup, migrationErr)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%v; inspect previous Agent: %w", migrationErr, err)
		}
	}
	return migrationErr
}

func agentSystemdDirectoriesReady() (bool, error) {
	for _, directory := range []string{"/opt/vps-panel/agent", "/opt/vps-panel/xray", "/etc/vps-panel/xray", "/opt/vps-panel/realm", "/etc/vps-panel/realm", "/opt/vps-panel/acme", "/var/lib/vps-panel/acme"} {
		info, err := os.Stat(rootedPath(agentSystemdMigrationRoot, directory))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspect Agent systemd directory %s: %w", directory, err)
		}
		if !info.IsDir() {
			return false, fmt.Errorf("Agent systemd path %s is not a directory", directory)
		}
	}
	return true, nil
}

func prepareAgentSystemdDirectories() error {
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{
		{"/opt/vps-panel/agent", 0o755},
		{"/opt/vps-panel/xray", 0o755},
		{"/opt/vps-panel/realm", 0o755},
		{"/opt/vps-panel/acme", 0o755},
		{"/etc/vps-panel/xray", 0o700},
		{"/etc/vps-panel/realm", 0o700},
		{"/var/lib/vps-panel/acme", 0o700},
	} {
		path := rootedPath(agentSystemdMigrationRoot, entry.path)
		if err := os.MkdirAll(path, entry.mode); err != nil {
			return fmt.Errorf("create Agent systemd directory %s: %w", entry.path, err)
		}
		if err := os.Chmod(path, entry.mode); err != nil {
			return fmt.Errorf("secure Agent systemd directory %s: %w", entry.path, err)
		}
	}
	return nil
}

func preserveLegacyAgentRollback() (string, error) {
	stagedBackup := filepath.Join(agentManagedDir, ".agent-migration-backup")
	if info, err := os.Lstat(stagedBackup); err == nil {
		if !info.Mode().IsRegular() {
			return "", errors.New("Agent migration backup is not a regular file")
		}
		return stagedBackup, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	matches, err := filepath.Glob(filepath.Join(agentManagedDir, ".agent-rollback-*"))
	if err != nil {
		return "", err
	}
	var latest string
	var latestTime time.Time
	for _, path := range matches {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) > 5*time.Minute {
			continue
		}
		if latest == "" || info.ModTime().After(latestTime) {
			latest, latestTime = path, info.ModTime()
		}
	}
	if latest == "" {
		return "", nil
	}
	temporary, err := os.CreateTemp(agentManagedDir, ".agent-migration-backup-*")
	if err != nil {
		return "", err
	}
	backup := temporary.Name()
	if err := temporary.Close(); err != nil {
		os.Remove(backup)
		return "", err
	}
	if err := os.Remove(backup); err != nil {
		return "", err
	}
	if err := os.Link(latest, backup); err != nil {
		return "", err
	}
	return backup, nil
}

func preserveCurrentAgentForStagedUpgrade(executable string) error {
	if filepath.Dir(executable) != agentManagedDir ||
		!strings.HasPrefix(filepath.Base(executable), ".agent-upgrade-") {
		return nil
	}
	if info, err := os.Lstat(agentManagedPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	} else if !info.Mode().IsRegular() {
		return errors.New("current Agent binary is not a regular file")
	}
	backup := filepath.Join(agentManagedDir, ".agent-migration-backup")
	if existing, err := os.Lstat(backup); err == nil {
		current, err := os.Stat(agentManagedPath)
		if err != nil {
			return err
		}
		if os.SameFile(existing, current) {
			return nil
		}
		if !existing.Mode().IsRegular() {
			return errors.New("Agent migration backup is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(agentManagedDir, ".agent-migration-link-*")
	if err != nil {
		return err
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		os.Remove(path)
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := os.Link(agentManagedPath, path); err != nil {
		return err
	}
	defer os.Remove(path)
	if err := os.Rename(path, backup); err != nil {
		return err
	}
	return nil
}

func prepareStagedAgentSystemdSandbox(executable string) error {
	if filepath.Dir(executable) != agentManagedDir ||
		!strings.HasPrefix(filepath.Base(executable), ".agent-upgrade-") {
		return nil
	}
	environment, err := detectAgentMigrationEnvironment()
	if err != nil {
		return err
	}
	if environment.InitSystem != initSystemSystemd {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentSystemdMigrationTimeout)
	defer cancel()
	writablePaths, err := currentAgentSystemdWritablePaths(ctx)
	if err != nil {
		return err
	}
	ready, err := agentSystemdDirectoriesReady()
	if err != nil {
		return err
	}
	if hasAgentRealmWritablePaths(writablePaths) && ready {
		return nil
	}
	unit := fmt.Sprintf("vps-panel-agent-bootstrap-%d", time.Now().UnixNano())
	output, err := runUpgradeLauncher("systemd-run", "--quiet", "--wait", "--collect", "--unit="+unit,
		executable, "_prepare-systemd-sandbox")
	if err != nil {
		return fmt.Errorf("run Agent systemd bootstrap helper: %s: %w", truncateDiagnostic(output), err)
	}
	return nil
}

func currentAgentSystemdWritablePaths(ctx context.Context) (string, error) {
	output, err := runAgentMigrationCommand(
		ctx,
		"systemctl",
		"show",
		"--property=ReadWritePaths",
		"--value",
		agentServiceName+".service",
	)
	if err != nil {
		return "", fmt.Errorf("inspect Agent systemd sandbox: %s: %w", truncateDiagnostic(output), err)
	}
	return string(output), nil
}

func hasAgentRealmWritablePaths(value string) bool {
	required := map[string]bool{
		"/opt/vps-panel/realm":    false,
		"/etc/vps-panel/realm":    false,
		"/opt/vps-panel/acme":     false,
		"/var/lib/vps-panel/acme": false,
	}
	for _, field := range strings.Fields(value) {
		path := strings.TrimLeft(field, "-+!")
		if _, ok := required[path]; ok {
			required[path] = true
		}
	}
	return required["/opt/vps-panel/realm"] && required["/etc/vps-panel/realm"] &&
		required["/opt/vps-panel/acme"] && required["/var/lib/vps-panel/acme"]
}

func runSystemdMigrationCommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output := &cappedBuffer{limit: agentSystemdMigrationOutputMax}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	return output.Bytes(), err
}

func launchAgentSystemdMigrationRestart(backup string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current Agent binary: %w", err)
	}
	unit := fmt.Sprintf("vps-panel-agent-migration-%d", time.Now().UnixNano())
	output, err := runUpgradeLauncher(
		"systemd-run",
		"--quiet",
		"--wait",
		"--collect",
		"--unit="+unit,
		executable,
		agentSystemdMigrationRestartCommand,
		"--backup", backup,
	)
	if err != nil {
		return fmt.Errorf("start detached Agent restart helper: %s: %w", truncateDiagnostic(output), err)
	}
	return nil
}

func runAgentSystemdMigrationRestart(arguments []string) error {
	if len(arguments) != 2 || arguments[0] != "--backup" ||
		(arguments[1] != "" && (filepath.Dir(arguments[1]) != agentManagedDir ||
			!strings.HasPrefix(filepath.Base(arguments[1]), ".agent-migration-backup"))) {
		return errors.New("invalid Agent systemd migration restart arguments")
	}
	backup := arguments[1]
	value, configErr := readAgentConfig(defaultConfigPath)
	createdDropIn, err := applyAgentSystemdSandboxMigration()
	if err == nil {
		err = restartAgentService()
		if err == nil {
			waitForAgentMigrationRestart()
			active, checkErr := newServiceManager(initSystemSystemd, agentServiceDefinition(), "", runAgentServiceCommand).IsActive(context.Background())
			if checkErr != nil {
				err = fmt.Errorf("verify migrated Agent service: %w", checkErr)
			} else if !active {
				err = errors.New("verify migrated Agent service: service is not active")
			}
		}
	}
	if err != nil {
		if configErr == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			reportAgentUpgradeFailure(ctx, &http.Client{Timeout: 10 * time.Second}, value, agentVersion, err)
			cancel()
		}
		if createdDropIn {
			err = restoreAgentSystemdDropIn(err)
		}
		if backup != "" {
			return rollbackAgentBinary(backup, err)
		}
		return err
	}
	if backup != "" && filepath.Base(backup) != ".agent-migration-backup" {
		os.Remove(backup)
	}
	return nil
}

func restoreAgentSystemdDropIn(migrationErr error) error {
	if err := os.Remove(agentSystemdDropInPath); err != nil {
		migrationErr = fmt.Errorf("%v; remove migration drop-in: %w", migrationErr, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentSystemdMigrationTimeout)
	defer cancel()
	if output, err := runAgentMigrationCommand(ctx, "systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("%v; restore systemd manager: %s: %w", migrationErr, truncateDiagnostic(output), err)
	}
	return migrationErr
}

func applyAgentSystemdSandboxMigration() (bool, error) {
	if err := prepareAgentSystemdDirectories(); err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentSystemdMigrationTimeout)
	defer cancel()
	writablePaths, err := currentAgentSystemdWritablePaths(ctx)
	if err != nil {
		return false, err
	}
	createdDropIn := false
	if !hasAgentRealmWritablePaths(writablePaths) {
		if err := os.MkdirAll(filepath.Dir(agentSystemdDropInPath), 0o755); err != nil {
			return false, fmt.Errorf("prepare Agent systemd drop-in directory: %w", err)
		}
		existing, exists, err := readOptionalFile(agentSystemdDropInPath)
		if err != nil {
			return false, fmt.Errorf("read Agent systemd drop-in: %w", err)
		}
		if exists && string(existing) != agentRealmSandboxDropIn && string(existing) != agentLegacyRealmSandboxDropIn {
			return false, errors.New("Agent Realm systemd drop-in conflicts with existing configuration")
		}
		if _, err := installServiceFile(agentSystemdDropInPath, []byte(agentRealmSandboxDropIn), 0o644); err != nil {
			return false, fmt.Errorf("write Agent systemd sandbox drop-in: %w", err)
		}
		createdDropIn = !exists
		if output, err := runAgentMigrationCommand(ctx, "systemctl", "daemon-reload"); err != nil {
			return createdDropIn, fmt.Errorf("reload systemd manager: %s: %w", truncateDiagnostic(output), err)
		}
	}
	writablePaths, err = currentAgentSystemdWritablePaths(ctx)
	if err != nil {
		return createdDropIn, err
	}
	if !hasAgentRealmWritablePaths(writablePaths) {
		return createdDropIn, errors.New("verify Agent systemd sandbox: Realm writable paths are still unavailable")
	}
	ready, err := agentSystemdDirectoriesReady()
	if err != nil {
		return createdDropIn, err
	}
	if !ready {
		return createdDropIn, errors.New("verify Agent systemd sandbox: required directories are missing")
	}
	return createdDropIn, nil
}
