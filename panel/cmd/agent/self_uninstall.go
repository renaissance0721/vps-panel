package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	agentConfigDir = "/etc/vps-panel-agent"
	agentStateDir  = "/var/lib/vps-panel/agent"
)

var (
	selfUninstallTempDir               = "/var/tmp"
	detectSelfUninstallHostEnvironment = detectHostEnvironment
	locateSelfUninstallExecutable      = os.Executable
	lookPathSelfUninstall              = exec.LookPath
	runSelfUninstallLauncher           = func(name string, arguments ...string) ([]byte, error) {
		return exec.Command(name, arguments...).CombinedOutput()
	}
	startOpenRCSelfUninstallProcess = startDetachedUpgradeProcess
	runSelfUninstallCommand         = func(ctx context.Context, name string, arguments ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, arguments...).CombinedOutput()
	}
)

type preparedSelfUninstall struct {
	helperPath  string
	environment hostEnvironment
	systemdRun  string
}

func prepareAgentSelfUninstall() (*preparedSelfUninstall, error) {
	environment, err := detectSelfUninstallHostEnvironment()
	if err != nil {
		return nil, err
	}
	systemdRun := ""
	if environment.InitSystem == initSystemSystemd {
		systemdRun, err = lookPathSelfUninstall("systemd-run")
		if err != nil {
			return nil, fmt.Errorf("locate systemd-run: %w", err)
		}
	} else if environment.InitSystem != initSystemOpenRC {
		return nil, errUnsupportedInitSystem
	}
	executable, err := locateSelfUninstallExecutable()
	if err != nil {
		return nil, fmt.Errorf("locate Agent binary: %w", err)
	}
	helperPath, err := stageSelfUninstallHelper(executable)
	if err != nil {
		return nil, err
	}
	return &preparedSelfUninstall{helperPath: helperPath, environment: environment, systemdRun: systemdRun}, nil
}

func stageSelfUninstallHelper(source string) (string, error) {
	input, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open Agent for self-uninstall: %w", err)
	}
	defer input.Close()
	output, err := os.CreateTemp(selfUninstallTempDir, ".vps-panel-agent-uninstall-*")
	if err != nil {
		return "", fmt.Errorf("stage Agent self-uninstall helper: %w", err)
	}
	path := output.Name()
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return "", fmt.Errorf("copy Agent self-uninstall helper: %w", err)
	}
	if err := output.Chmod(0o700); err != nil {
		return "", fmt.Errorf("secure Agent self-uninstall helper: %w", err)
	}
	if err := output.Sync(); err != nil {
		return "", fmt.Errorf("sync Agent self-uninstall helper: %w", err)
	}
	if err := output.Close(); err != nil {
		return "", fmt.Errorf("close Agent self-uninstall helper: %w", err)
	}
	ok = true
	return path, nil
}

func (p *preparedSelfUninstall) activate() error {
	arguments := []string{"_self-uninstall"}
	if p.environment.InitSystem == initSystemOpenRC {
		if err := startOpenRCSelfUninstallProcess(p.helperPath, arguments...); err != nil {
			return fmt.Errorf("start detached OpenRC self-uninstall helper: %w", err)
		}
		return nil
	}
	if p.environment.InitSystem != initSystemSystemd || p.systemdRun == "" {
		return errUnsupportedInitSystem
	}
	unit := fmt.Sprintf("vps-panel-agent-uninstall-%d", time.Now().UnixNano())
	launcherArguments := append([]string{"--quiet", "--collect", "--unit=" + unit, p.helperPath}, arguments...)
	output, err := runSelfUninstallLauncher(p.systemdRun, launcherArguments...)
	if err != nil {
		return fmt.Errorf("start systemd self-uninstall helper: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (p *preparedSelfUninstall) cleanup() {
	if p != nil && p.helperPath != "" {
		_ = os.Remove(p.helperPath)
	}
}

type agentSelfUninstaller struct {
	service    serviceManager
	root       string
	helperPath string
	remove     func(string) error
	removeAll  func(string) error
}

func newAgentSelfUninstaller(environment hostEnvironment, root, helperPath string) *agentSelfUninstaller {
	return &agentSelfUninstaller{
		service:    newServiceManager(environment.InitSystem, agentServiceDefinition(), root, runSelfUninstallCommand),
		root:       root,
		helperPath: helperPath,
		remove:     os.Remove,
		removeAll:  os.RemoveAll,
	}
}

func (u *agentSelfUninstaller) run(ctx context.Context) error {
	configDir := rootedPath(u.root, agentConfigDir)
	stateDir := rootedPath(u.root, agentStateDir)
	installDir := rootedPath(u.root, agentManagedDir)
	unitPath := u.service.Path()
	if unitPath == "" {
		return errUnsupportedInitSystem
	}
	unitExists, err := pathExists(unitPath)
	if err != nil {
		return fmt.Errorf("inspect Agent service: %w", err)
	}
	if unitExists {
		if err := u.service.Stop(ctx); err != nil {
			return fmt.Errorf("stop Agent service: %w", err)
		}
		if err := u.service.Disable(ctx); err != nil {
			return fmt.Errorf("disable Agent service: %w", err)
		}
		if err := u.remove(unitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove Agent service: %w", err)
		}
		if err := u.service.DaemonReload(ctx); err != nil {
			return fmt.Errorf("reload services after Agent removal: %w", err)
		}
	}
	for _, path := range []string{configDir, stateDir, installDir} {
		if err := u.removeAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove Agent managed directory: %w", err)
		}
	}
	if u.helperPath != "" {
		_ = u.remove(u.helperPath)
	}
	return nil
}

func runAgentSelfUninstall() error {
	environment, err := detectSelfUninstallHostEnvironment()
	if err != nil {
		return err
	}
	helperPath, err := locateSelfUninstallExecutable()
	if err != nil {
		return fmt.Errorf("locate self-uninstall helper: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return newAgentSelfUninstaller(environment, "", filepath.Clean(helperPath)).run(ctx)
}
