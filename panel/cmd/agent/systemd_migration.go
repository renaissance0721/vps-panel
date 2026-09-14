package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	agentSystemdMigrationRestartCommand = "_restart-after-systemd-migration"
	agentSystemdMigrationOutputMax      = 4 << 10
	agentSystemdMigrationTimeout        = 10 * time.Second
	agentRealmSandboxDropIn             = "[Service]\nReadWritePaths=/opt/vps-panel/realm /etc/vps-panel/realm\n"
)

var (
	agentSystemdDropInPath          = "/etc/systemd/system/vps-panel-agent.service.d/realm.conf"
	detectAgentMigrationEnvironment = detectHostEnvironment
	runAgentMigrationCommand        = runSystemdMigrationCommand
	scheduleAgentMigrationRestart   = launchAgentSystemdMigrationRestart
)

func migrateAgentSystemdSandbox() (bool, error) {
	environment, err := detectAgentMigrationEnvironment()
	if err != nil {
		return false, fmt.Errorf("detect host environment: %w", err)
	}
	if environment.InitSystem != initSystemSystemd {
		return false, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), agentSystemdMigrationTimeout)
	defer cancel()
	writablePaths, err := currentAgentSystemdWritablePaths(ctx)
	if err != nil {
		return false, err
	}
	if hasAgentRealmWritablePaths(writablePaths) {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(agentSystemdDropInPath), 0o755); err != nil {
		return false, fmt.Errorf("prepare Agent systemd drop-in directory: %w", err)
	}
	if _, err := installServiceFile(agentSystemdDropInPath, []byte(agentRealmSandboxDropIn), 0o644); err != nil {
		return false, fmt.Errorf("write Agent systemd sandbox drop-in: %w", err)
	}
	if output, err := runAgentMigrationCommand(ctx, "systemctl", "daemon-reload"); err != nil {
		return false, fmt.Errorf("reload systemd manager: %s: %w", truncateDiagnostic(output), err)
	}
	writablePaths, err = currentAgentSystemdWritablePaths(ctx)
	if err != nil {
		return false, err
	}
	if !hasAgentRealmWritablePaths(writablePaths) {
		return false, errors.New("verify Agent systemd sandbox: Realm writable paths are still unavailable")
	}
	if err := scheduleAgentMigrationRestart(); err != nil {
		return false, fmt.Errorf("schedule Agent systemd restart: %w", err)
	}
	return true, nil
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
		"/opt/vps-panel/realm": false,
		"/etc/vps-panel/realm": false,
	}
	for _, field := range strings.Fields(value) {
		path := strings.TrimLeft(field, "-+!")
		if _, ok := required[path]; ok {
			required[path] = true
		}
	}
	return required["/opt/vps-panel/realm"] && required["/etc/vps-panel/realm"]
}

func runSystemdMigrationCommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output := &cappedBuffer{limit: agentSystemdMigrationOutputMax}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	return output.Bytes(), err
}

func launchAgentSystemdMigrationRestart() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current Agent binary: %w", err)
	}
	unit := fmt.Sprintf("vps-panel-agent-migration-%d", time.Now().UnixNano())
	output, err := runUpgradeLauncher(
		"systemd-run",
		"--quiet",
		"--collect",
		"--unit="+unit,
		executable,
		agentSystemdMigrationRestartCommand,
	)
	if err != nil {
		return fmt.Errorf("start detached Agent restart helper: %s: %w", truncateDiagnostic(output), err)
	}
	return nil
}

func runAgentSystemdMigrationRestart(arguments []string) error {
	if len(arguments) != 0 {
		return errors.New("invalid Agent systemd migration restart arguments")
	}
	return restartAgentService()
}
