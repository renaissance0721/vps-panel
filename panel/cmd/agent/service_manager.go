package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type serviceDefinition struct {
	Name               string
	Description        string
	Command            string
	Arguments          []string
	TimeoutStopSeconds int
}

type serviceManager interface {
	Path() string
	Install() (bool, error)
	DaemonReload(context.Context) error
	Enable(context.Context) error
	Disable(context.Context) error
	Start(context.Context) error
	Stop(context.Context) error
	Restart(context.Context) error
	IsActive(context.Context) (bool, error)
}

type systemdServiceManager struct {
	definition serviceDefinition
	path       string
	runCommand func(context.Context, string, ...string) ([]byte, error)
}

type openRCServiceManager struct {
	definition serviceDefinition
	path       string
	runCommand func(context.Context, string, ...string) ([]byte, error)
}

type unsupportedServiceManager struct {
	err error
}

func newHostServiceManager(definition serviceDefinition, runCommand func(context.Context, string, ...string) ([]byte, error)) serviceManager {
	environment, err := detectHostEnvironment()
	if err != nil {
		return &unsupportedServiceManager{err: err}
	}
	return newServiceManager(environment.InitSystem, definition, "", runCommand)
}

func newServiceManager(system initSystem, definition serviceDefinition, root string, runCommand func(context.Context, string, ...string) ([]byte, error)) serviceManager {
	switch system {
	case initSystemSystemd:
		return &systemdServiceManager{
			definition: definition,
			path:       rootedPath(root, "/etc/systemd/system/"+definition.Name+".service"),
			runCommand: runCommand,
		}
	case initSystemOpenRC:
		return &openRCServiceManager{
			definition: definition,
			path:       rootedPath(root, "/etc/init.d/"+definition.Name),
			runCommand: runCommand,
		}
	default:
		return &unsupportedServiceManager{err: errUnsupportedInitSystem}
	}
}

func rootedPath(root, path string) string {
	if root == "" {
		return path
	}
	return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(path, "/")))
}

func (m *systemdServiceManager) Path() string { return m.path }

func (m *systemdServiceManager) Install() (bool, error) {
	return installServiceFile(m.path, renderSystemdService(m.definition), 0o644)
}

func (m *systemdServiceManager) DaemonReload(ctx context.Context) error {
	return m.run(ctx, "daemon-reload")
}

func (m *systemdServiceManager) Enable(ctx context.Context) error {
	return m.run(ctx, "enable", m.definition.Name+".service")
}

func (m *systemdServiceManager) Disable(ctx context.Context) error {
	return m.run(ctx, "disable", m.definition.Name+".service")
}

func (m *systemdServiceManager) Start(ctx context.Context) error {
	return m.run(ctx, "start", m.definition.Name+".service")
}

func (m *systemdServiceManager) Stop(ctx context.Context) error {
	return m.run(ctx, "stop", m.definition.Name+".service")
}

func (m *systemdServiceManager) Restart(ctx context.Context) error {
	return m.run(ctx, "restart", m.definition.Name+".service")
}

func (m *systemdServiceManager) IsActive(ctx context.Context) (bool, error) {
	_, err := m.runCommand(ctx, "systemctl", "is-active", "--quiet", m.definition.Name+".service")
	return err == nil, nil
}

func (m *systemdServiceManager) run(ctx context.Context, arguments ...string) error {
	output, err := m.runCommand(ctx, "systemctl", arguments...)
	if err != nil {
		return fmt.Errorf("systemctl %s: %s: %w", arguments[0], truncateDiagnostic(output), err)
	}
	return nil
}

func (m *openRCServiceManager) Path() string { return m.path }

func (m *openRCServiceManager) Install() (bool, error) {
	return installServiceFile(m.path, renderOpenRCService(m.definition), 0o755)
}

func (m *openRCServiceManager) DaemonReload(context.Context) error { return nil }

func (m *openRCServiceManager) Enable(ctx context.Context) error {
	output, err := m.runCommand(ctx, "rc-update", "add", m.definition.Name, "default")
	if err != nil {
		return fmt.Errorf("rc-update add: %s: %w", truncateDiagnostic(output), err)
	}
	return nil
}

func (m *openRCServiceManager) Disable(ctx context.Context) error {
	output, err := m.runCommand(ctx, "rc-update", "del", m.definition.Name, "default")
	if err != nil {
		return fmt.Errorf("rc-update del: %s: %w", truncateDiagnostic(output), err)
	}
	return nil
}

func (m *openRCServiceManager) Start(ctx context.Context) error {
	return m.run(ctx, "start")
}

func (m *openRCServiceManager) Stop(ctx context.Context) error {
	return m.run(ctx, "stop")
}

func (m *openRCServiceManager) Restart(ctx context.Context) error {
	return m.run(ctx, "restart")
}

func (m *openRCServiceManager) IsActive(ctx context.Context) (bool, error) {
	_, err := m.runCommand(ctx, "rc-service", m.definition.Name, "status")
	return err == nil, nil
}

func (m *openRCServiceManager) run(ctx context.Context, action string) error {
	output, err := m.runCommand(ctx, "rc-service", m.definition.Name, action)
	if err != nil {
		return fmt.Errorf("rc-service %s: %s: %w", action, truncateDiagnostic(output), err)
	}
	return nil
}

func (m *unsupportedServiceManager) Path() string { return "" }

func (m *unsupportedServiceManager) Install() (bool, error) { return false, m.err }

func (m *unsupportedServiceManager) DaemonReload(context.Context) error { return m.err }
func (m *unsupportedServiceManager) Enable(context.Context) error       { return m.err }
func (m *unsupportedServiceManager) Disable(context.Context) error      { return m.err }
func (m *unsupportedServiceManager) Start(context.Context) error        { return m.err }
func (m *unsupportedServiceManager) Stop(context.Context) error         { return m.err }
func (m *unsupportedServiceManager) Restart(context.Context) error      { return m.err }
func (m *unsupportedServiceManager) IsActive(context.Context) (bool, error) {
	return false, m.err
}

func installServiceFile(path string, content []byte, mode os.FileMode) (bool, error) {
	existing, exists, err := readOptionalFile(path)
	if err != nil {
		return false, err
	}
	if exists && bytes.Equal(existing, content) {
		return false, os.Chmod(path, mode)
	}
	if err := writeFileAtomically(path, content, mode); err != nil {
		return false, err
	}
	return true, nil
}

func renderSystemdService(definition serviceDefinition) []byte {
	var builder strings.Builder
	builder.WriteString("[Unit]\nDescription=")
	builder.WriteString(definition.Description)
	builder.WriteString("\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=simple\nExecStart=")
	builder.WriteString(definition.Command)
	if len(definition.Arguments) != 0 {
		builder.WriteByte(' ')
		builder.WriteString(strings.Join(definition.Arguments, " "))
	}
	builder.WriteString("\nRestart=on-failure\nRestartSec=3\n")
	if definition.TimeoutStopSeconds > 0 {
		builder.WriteString("TimeoutStopSec=")
		builder.WriteString(strconv.Itoa(definition.TimeoutStopSeconds))
		builder.WriteByte('\n')
	}
	builder.WriteString("NoNewPrivileges=true\nPrivateTmp=true\nProtectHome=true\nProtectSystem=strict\nUMask=0077\n\n[Install]\nWantedBy=multi-user.target\n")
	return []byte(builder.String())
}

func renderOpenRCService(definition serviceDefinition) []byte {
	var builder strings.Builder
	builder.WriteString("#!/sbin/openrc-run\n\nname=\"")
	builder.WriteString(definition.Description)
	builder.WriteString("\"\ndescription=\"")
	builder.WriteString(definition.Description)
	builder.WriteString("\"\nsupervisor=\"supervise-daemon\"\ncommand=\"")
	builder.WriteString(definition.Command)
	builder.WriteString("\"\n")
	if len(definition.Arguments) != 0 {
		builder.WriteString("command_args=\"")
		builder.WriteString(strings.Join(definition.Arguments, " "))
		builder.WriteString("\"\n")
	}
	builder.WriteString("respawn_delay=3\nrespawn_max=0\nretry=\"TERM/10/KILL/5\"\numask=0077\n\ndepend() {\n  need net\n}\n")
	return []byte(builder.String())
}
