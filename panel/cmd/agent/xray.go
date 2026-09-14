package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	managedXrayVersion          = "v26.3.27"
	managedXrayReleaseBaseURL   = "https://github.com/XTLS/Xray-core/releases/download"
	managedXrayServiceName      = "vps-panel-xray.service"
	managedXrayBinaryPath       = "/opt/vps-panel/xray/xray"
	managedXrayConfigPath       = "/etc/vps-panel/xray/config.json"
	managedXrayStatsAPIAddress  = "127.0.0.1:10085"
	managedXrayMaxDownloadBytes = 128 << 20
	managedXrayMaxBinaryBytes   = 128 << 20
	managedXrayCommandOutputMax = 4 << 10
	managedXrayRollbackTimeout  = 15 * time.Second
)

var (
	errManagedXrayDownload   = errors.New("managed Xray download failed")
	errManagedXrayChecksum   = errors.New("managed Xray checksum verification failed")
	errManagedXrayValidation = errors.New("managed Xray configuration validation failed")
	errManagedXrayStart      = errors.New("managed Xray service failed to start")
	errManagedXrayHealth     = errors.New("managed Xray listener health check failed")
	errManagedXrayStop       = errors.New("managed Xray service failed to stop")
	errManagedXrayConflict   = errors.New("existing unmanaged Xray installation detected")
	errManagedXrayArch       = errors.New("managed Xray is unsupported on this architecture")
)

type managedXrayAsset struct {
	name   string
	sha256 string
}

var managedXrayAssets = map[string]managedXrayAsset{
	"amd64": {
		name:   "Xray-linux-64.zip",
		sha256: "23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae",
	},
	"arm64": {
		name:   "Xray-linux-arm64-v8a.zip",
		sha256: "4d30283ae614e3057f730f67cd088a42be6fdf91f8639d82cb69e48cde80413c",
	},
}

type xrayManager struct {
	installDir        string
	binaryPath        string
	markerPath        string
	configDir         string
	configPath        string
	previousPath      string
	unitPath          string
	serviceName       string
	unmanagedUnits    []string
	goos              string
	goarch            string
	releaseBaseURL    string
	assets            map[string]managedXrayAsset
	client            *http.Client
	runCommand        func(context.Context, string, ...string) ([]byte, error)
	probeListener     func(context.Context, int) error
	reconcileFirewall func(context.Context, []firewallRule) error
	wait              func(context.Context, time.Duration) error
	healthAttempts    int
	healthCheckDelay  time.Duration
}

func newXrayManager() *xrayManager {
	firewall := newProxyFirewall()
	return &xrayManager{
		installDir:        "/opt/vps-panel/xray",
		binaryPath:        managedXrayBinaryPath,
		markerPath:        "/opt/vps-panel/xray/.managed-by-vps-panel",
		configDir:         "/etc/vps-panel/xray",
		configPath:        managedXrayConfigPath,
		previousPath:      "/etc/vps-panel/xray/config.previous.json",
		unitPath:          "/etc/systemd/system/vps-panel-xray.service",
		serviceName:       managedXrayServiceName,
		unmanagedUnits:    []string{"/etc/systemd/system/xray.service", "/lib/systemd/system/xray.service", "/usr/lib/systemd/system/xray.service"},
		goos:              runtime.GOOS,
		goarch:            runtime.GOARCH,
		releaseBaseURL:    managedXrayReleaseBaseURL,
		assets:            managedXrayAssets,
		client:            &http.Client{Timeout: 60 * time.Second},
		runCommand:        runXrayCommand,
		probeListener:     probeXrayListener,
		reconcileFirewall: firewall.reconcileRules,
		wait:              waitForXray,
		healthAttempts:    6,
		healthCheckDelay:  500 * time.Millisecond,
	}
}

func (m *xrayManager) apply(ctx context.Context, state desiredState) error {
	if !state.Xray.Enabled && len(state.Xray.Proxies) != 0 {
		return errUnsupportedManagedConfig
	}
	if !state.Xray.Enabled {
		return m.disable(ctx)
	}
	return m.enable(ctx, state.Xray.Proxies)
}

func (m *xrayManager) disable(ctx context.Context) error {
	managed, err := regularFileExists(m.markerPath)
	if err != nil {
		return fmt.Errorf("inspect managed Xray marker: %w", err)
	}
	if !managed {
		return nil
	}
	unitExists, err := pathExists(m.unitPath)
	if err != nil {
		return fmt.Errorf("inspect managed Xray systemd unit: %w", err)
	}
	if unitExists {
		if _, err := m.runCommand(ctx, "systemctl", "disable", "--now", m.serviceName); err != nil {
			return fmt.Errorf("%w: %v", errManagedXrayStop, err)
		}
	}
	for _, path := range []string{m.configPath, m.previousPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove managed Xray config: %w", err)
		}
	}
	if err := m.reconcileFirewall(ctx, nil); err != nil {
		return err
	}
	return nil
}

func (m *xrayManager) enable(ctx context.Context, proxies []desiredProxy) error {
	expectedPorts := expectedProxyPorts(proxies)
	expectedRules := expectedProxyFirewallRules(proxies)
	candidate, err := renderManagedXrayConfig(proxies)
	if err != nil {
		return err
	}
	if err := m.ensureManagedXray(ctx); err != nil {
		return err
	}
	if err := ensureSecureDirectory(m.configDir, 0o700); err != nil {
		return fmt.Errorf("prepare managed Xray config directory: %w", err)
	}

	candidatePath, err := writeTemporaryFile(m.configDir, ".config-candidate-*.json", candidate, 0o600)
	if err != nil {
		return fmt.Errorf("write managed Xray candidate: %w", err)
	}
	defer os.Remove(candidatePath)

	if err := m.validateConfig(ctx, candidatePath); err != nil {
		return err
	}
	unitChanged, err := m.ensureUnit()
	if err != nil {
		return err
	}
	if unitChanged {
		if _, err := m.runCommand(ctx, "systemctl", "daemon-reload"); err != nil {
			return fmt.Errorf("install managed Xray systemd unit: %w", err)
		}
	}
	if _, err := m.runCommand(ctx, "systemctl", "enable", m.serviceName); err != nil {
		return fmt.Errorf("%w: %v", errManagedXrayStart, err)
	}

	current, exists, err := readOptionalFile(m.configPath)
	if err != nil {
		return fmt.Errorf("read managed Xray config: %w", err)
	}
	if exists && bytes.Equal(current, candidate) {
		if err := os.Chmod(m.configPath, 0o600); err != nil {
			return fmt.Errorf("secure managed Xray config: %w", err)
		}
		active, err := m.isActive(ctx)
		if err != nil {
			return err
		}
		if active && m.listenersHealthy(ctx, expectedPorts) {
			return m.reconcileFirewall(ctx, expectedRules)
		}
		action := "start"
		if active {
			action = "restart"
		}
		if _, err := m.runCommand(ctx, "systemctl", action, m.serviceName); err != nil {
			return fmt.Errorf("%w: %v", errManagedXrayStart, err)
		}
		if err := m.waitUntilHealthy(ctx, expectedPorts); err != nil {
			return err
		}
		return m.reconcileFirewall(ctx, expectedRules)
	}

	previousPorts, previousRules, previousStateKnown := renderedConfigState(current)
	if exists {
		if err := writeFileAtomically(m.previousPath, current, 0o600); err != nil {
			return fmt.Errorf("save previous managed Xray config: %w", err)
		}
	}
	if err := replaceFile(candidatePath, m.configPath, 0o600); err != nil {
		return fmt.Errorf("install managed Xray config: %w", err)
	}

	if _, err := m.runCommand(ctx, "systemctl", "restart", m.serviceName); err != nil {
		return m.rollbackFailedApply(ctx, exists, previousPorts, previousRules, previousStateKnown, fmt.Errorf("%w: %v", errManagedXrayStart, err))
	}
	if err := m.waitUntilHealthy(ctx, expectedPorts); err != nil {
		return m.rollbackFailedApply(ctx, exists, previousPorts, previousRules, previousStateKnown, err)
	}
	if err := m.reconcileFirewall(ctx, expectedRules); err != nil {
		return m.rollbackFailedApply(ctx, exists, previousPorts, previousRules, previousStateKnown, err)
	}
	return nil
}

func (m *xrayManager) rollbackFailedApply(ctx context.Context, hasPrevious bool, previousPorts []int, previousRules []firewallRule, previousStateKnown bool, applyErr error) error {
	log.Printf("new config apply failed: %v", applyErr)
	rollbackContext, cancelRollback := context.WithTimeout(context.WithoutCancel(ctx), managedXrayRollbackTimeout)
	defer cancelRollback()
	if !hasPrevious {
		if _, err := m.runCommand(rollbackContext, "systemctl", "disable", "--now", m.serviceName); err != nil {
			log.Printf("rollback failed: stop managed Xray service: %v", err)
		}
		if err := os.Remove(m.configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("remove failed managed Xray config: %v", err)
		}
		return applyErr
	}

	previous, exists, err := readOptionalFile(m.previousPath)
	if err != nil {
		log.Printf("rollback failed: read previous managed Xray config: %v", err)
		return applyErr
	}
	if !exists {
		log.Print("rollback failed: previous managed Xray config is missing")
		return applyErr
	}
	if err := writeFileAtomically(m.configPath, previous, 0o600); err != nil {
		log.Printf("rollback failed: restore previous managed Xray config: %v", err)
		return applyErr
	}
	if _, err := m.runCommand(rollbackContext, "systemctl", "restart", m.serviceName); err != nil {
		log.Printf("rollback failed: restart previous managed Xray config: %v", err)
		return applyErr
	}
	if err := m.waitUntilHealthy(rollbackContext, previousPorts); err != nil {
		log.Printf("rollback failed: %v", err)
	}
	if previousStateKnown {
		if err := m.reconcileFirewall(rollbackContext, previousRules); err != nil {
			log.Printf("rollback failed: restore managed firewall rules: %v", err)
		}
	}
	return applyErr
}

func (m *xrayManager) ensureManagedXray(ctx context.Context) error {
	if m.goos != "linux" {
		return fmt.Errorf("%w: %s/%s", errManagedXrayArch, m.goos, m.goarch)
	}
	asset, ok := m.assets[m.goarch]
	if !ok {
		return fmt.Errorf("%w: %s/%s", errManagedXrayArch, m.goos, m.goarch)
	}
	if err := m.checkConflicts(); err != nil {
		return err
	}

	managed, err := regularFileExists(m.markerPath)
	if err != nil {
		return fmt.Errorf("inspect managed Xray marker: %w", err)
	}
	if managed {
		binaryExists, err := regularFileExists(m.binaryPath)
		if err != nil {
			return fmt.Errorf("inspect managed Xray binary: %w", err)
		}
		if binaryExists && m.verifyBinary(ctx, m.binaryPath) == nil {
			if err := os.Chmod(m.binaryPath, 0o755); err != nil {
				return fmt.Errorf("secure managed Xray binary: %w", err)
			}
			return nil
		}
	}

	if err := ensureSecureDirectory(m.installDir, 0o755); err != nil {
		return fmt.Errorf("prepare managed Xray install directory: %w", err)
	}
	archivePath, err := m.download(ctx, asset)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	stagedBinary, cleanupExtract, err := m.extractBinary(archivePath)
	if err != nil {
		return fmt.Errorf("%w: %v", errManagedXrayDownload, err)
	}
	defer cleanupExtract()
	if err := m.verifyBinary(ctx, stagedBinary); err != nil {
		return fmt.Errorf("install managed Xray binary: %w", err)
	}
	if err := writeFileAtomically(m.markerPath, []byte("managed by vps-panel\n"), 0o644); err != nil {
		return fmt.Errorf("write managed Xray marker: %w", err)
	}
	if err := replaceFile(stagedBinary, m.binaryPath, 0o755); err != nil {
		return fmt.Errorf("install managed Xray binary: %w", err)
	}
	return nil
}

func (m *xrayManager) checkConflicts() error {
	for _, unit := range m.unmanagedUnits {
		exists, err := pathExists(unit)
		if err != nil {
			return fmt.Errorf("inspect unmanaged Xray unit: %w", err)
		}
		if exists {
			return errManagedXrayConflict
		}
	}

	managed, err := regularFileExists(m.markerPath)
	if err != nil {
		return fmt.Errorf("inspect managed Xray marker: %w", err)
	}
	if managed {
		return nil
	}
	for _, directory := range []string{m.installDir, m.configDir} {
		hasEntries, err := directoryHasEntries(directory)
		if err != nil {
			return fmt.Errorf("inspect managed Xray directory: %w", err)
		}
		if hasEntries {
			return errManagedXrayConflict
		}
	}
	for _, managedPath := range []string{m.binaryPath, m.configPath, m.previousPath, m.unitPath} {
		exists, err := pathExists(managedPath)
		if err != nil {
			return fmt.Errorf("inspect managed Xray path: %w", err)
		}
		if exists {
			return errManagedXrayConflict
		}
	}
	return nil
}

func (m *xrayManager) download(ctx context.Context, asset managedXrayAsset) (string, error) {
	requestURL := strings.TrimRight(m.releaseBaseURL, "/") + "/" + managedXrayVersion + "/" + asset.name
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: create request: %v", errManagedXrayDownload, err)
	}
	response, err := m.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errManagedXrayDownload, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: unexpected HTTP status %s", errManagedXrayDownload, response.Status)
	}
	if response.ContentLength > managedXrayMaxDownloadBytes {
		return "", fmt.Errorf("%w: response is too large", errManagedXrayDownload)
	}

	temporary, err := os.CreateTemp(m.installDir, ".xray-download-*")
	if err != nil {
		return "", fmt.Errorf("%w: create temporary archive: %v", errManagedXrayDownload, err)
	}
	temporaryPath := temporary.Name()
	remove := true
	defer func() {
		temporary.Close()
		if remove {
			os.Remove(temporaryPath)
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, managedXrayMaxDownloadBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: read response: %v", errManagedXrayDownload, err)
	}
	if written > managedXrayMaxDownloadBytes {
		return "", fmt.Errorf("%w: response is too large", errManagedXrayDownload)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, asset.sha256) {
		return "", fmt.Errorf("%w: got %s", errManagedXrayChecksum, got)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("%w: sync archive: %v", errManagedXrayDownload, err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("%w: close archive: %v", errManagedXrayDownload, err)
	}
	remove = false
	return temporaryPath, nil
}

func (m *xrayManager) extractBinary(archivePath string) (string, func(), error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", nil, fmt.Errorf("open archive: %w", err)
	}
	defer archive.Close()

	var binary *zip.File
	for _, entry := range archive.File {
		if filepath.ToSlash(entry.Name) == "xray" {
			binary = entry
			break
		}
	}
	if binary == nil || binary.FileInfo().IsDir() || binary.UncompressedSize64 > managedXrayMaxBinaryBytes {
		return "", nil, errors.New("archive does not contain a valid xray binary")
	}

	input, err := binary.Open()
	if err != nil {
		return "", nil, fmt.Errorf("open archived binary: %w", err)
	}
	defer input.Close()
	extractionDir, err := os.MkdirTemp(m.installDir, ".xray-extract-*")
	if err != nil {
		return "", nil, fmt.Errorf("create extraction directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(extractionDir) }
	outputPath := filepath.Join(extractionDir, "xray")
	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create staged binary: %w", err)
	}
	success := false
	defer func() {
		output.Close()
		if !success {
			cleanup()
		}
	}()

	written, err := io.Copy(output, io.LimitReader(input, managedXrayMaxBinaryBytes+1))
	if err != nil {
		return "", nil, fmt.Errorf("extract binary: %w", err)
	}
	if written > managedXrayMaxBinaryBytes {
		return "", nil, errors.New("archived xray binary is too large")
	}
	if err := output.Chmod(0o755); err != nil {
		return "", nil, fmt.Errorf("secure staged binary: %w", err)
	}
	if err := output.Sync(); err != nil {
		return "", nil, fmt.Errorf("sync staged binary: %w", err)
	}
	if err := output.Close(); err != nil {
		return "", nil, fmt.Errorf("close staged binary: %w", err)
	}
	success = true
	return outputPath, cleanup, nil
}

func (m *xrayManager) verifyBinary(ctx context.Context, binaryPath string) error {
	info, err := os.Stat(binaryPath)
	if err != nil {
		return fmt.Errorf("inspect binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("binary is not a regular file")
	}
	output, err := m.runCommand(ctx, binaryPath, "version")
	if err != nil {
		return fmt.Errorf("run version check: %w", err)
	}
	fields := strings.Fields(string(output))
	wantVersion := strings.TrimPrefix(managedXrayVersion, "v")
	if len(fields) < 2 || fields[0] != "Xray" || fields[1] != wantVersion {
		return fmt.Errorf("unexpected version output: %s", truncateDiagnostic(output))
	}
	return nil
}

func (m *xrayManager) validateConfig(ctx context.Context, candidatePath string) error {
	output, err := m.runCommand(ctx, m.binaryPath, "run", "-test", "-config", candidatePath)
	if err != nil {
		return fmt.Errorf("%w: %s", errManagedXrayValidation, truncateDiagnostic(output))
	}
	return nil
}

func (m *xrayManager) ensureUnit() (bool, error) {
	unit := []byte(`[Unit]
Description=VPS Panel Managed Xray
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/opt/vps-panel/xray/xray run -config /etc/vps-panel/xray/config.json
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
UMask=0077

[Install]
WantedBy=multi-user.target
`)
	existing, exists, err := readOptionalFile(m.unitPath)
	if err != nil {
		return false, fmt.Errorf("read managed Xray systemd unit: %w", err)
	}
	if exists && bytes.Equal(existing, unit) {
		if err := os.Chmod(m.unitPath, 0o644); err != nil {
			return false, fmt.Errorf("secure managed Xray systemd unit: %w", err)
		}
		return false, nil
	}
	if err := writeFileAtomically(m.unitPath, unit, 0o644); err != nil {
		return false, fmt.Errorf("write managed Xray systemd unit: %w", err)
	}
	return true, nil
}

func (m *xrayManager) isActive(ctx context.Context) (bool, error) {
	_, err := m.runCommand(ctx, "systemctl", "is-active", "--quiet", m.serviceName)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (m *xrayManager) waitUntilHealthy(ctx context.Context, ports []int) error {
	consecutiveHealthy := 0
	wasActive := false
	for attempt := 0; attempt < m.healthAttempts; attempt++ {
		active, err := m.isActive(ctx)
		if err != nil {
			return err
		}
		if active {
			wasActive = true
			if m.listenersHealthy(ctx, ports) {
				consecutiveHealthy++
				if consecutiveHealthy == 2 || m.healthAttempts == 1 {
					return nil
				}
			} else {
				consecutiveHealthy = 0
			}
		} else {
			consecutiveHealthy = 0
		}
		if attempt+1 < m.healthAttempts {
			if err := m.wait(ctx, m.healthCheckDelay); err != nil {
				return fmt.Errorf("%w: %v", errManagedXrayStart, err)
			}
		}
	}
	if wasActive {
		return errManagedXrayHealth
	}
	return errManagedXrayStart
}

func (m *xrayManager) listenersHealthy(ctx context.Context, ports []int) bool {
	for _, port := range ports {
		if err := m.probeListener(ctx, port); err != nil {
			return false
		}
	}
	return true
}

func probeXrayListener(ctx context.Context, port int) error {
	connection, err := (&net.Dialer{Timeout: 300 * time.Millisecond}).DialContext(
		ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
	)
	if err != nil {
		return err
	}
	return connection.Close()
}

func expectedProxyPorts(proxies []desiredProxy) []int {
	ports := make([]int, 0, len(proxies))
	seen := make(map[int]struct{}, len(proxies))
	for _, proxy := range proxies {
		if proxy.Protocol == "shadowsocks" && len(proxy.Clients) == 0 {
			continue
		}
		if _, exists := seen[proxy.Port]; exists {
			continue
		}
		seen[proxy.Port] = struct{}{}
		ports = append(ports, proxy.Port)
	}
	sort.Ints(ports)
	return ports
}

func expectedProxyFirewallRules(proxies []desiredProxy) []firewallRule {
	rules := make([]firewallRule, 0, len(proxies)*2)
	for _, proxy := range proxies {
		if proxy.Protocol == "shadowsocks" && len(proxy.Clients) == 0 {
			continue
		}
		rules = append(rules, firewallRule{port: proxy.Port, protocol: "tcp"})
		if proxy.Protocol == "shadowsocks" {
			rules = append(rules, firewallRule{port: proxy.Port, protocol: "udp"})
		}
	}
	return rules
}

func renderedConfigPorts(value []byte) ([]int, bool) {
	ports, _, ok := renderedConfigState(value)
	return ports, ok
}

func renderedConfigState(value []byte) ([]int, []firewallRule, bool) {
	var config renderedXrayConfig
	if len(value) == 0 || json.Unmarshal(value, &config) != nil {
		return nil, nil, false
	}
	ports := make([]int, 0, len(config.Inbounds))
	rules := make([]firewallRule, 0, len(config.Inbounds)*2)
	for _, inbound := range config.Inbounds {
		ports = append(ports, inbound.Port)
		rules = append(rules, firewallRule{port: inbound.Port, protocol: "tcp"})
		if inbound.Protocol == "shadowsocks" {
			rules = append(rules, firewallRule{port: inbound.Port, protocol: "udp"})
		}
	}
	return ports, rules, true
}

func renderManagedXrayBaseConfig() []byte {
	value, _ := renderManagedXrayConfig(nil)
	return value
}

func writeTemporaryFile(directory, pattern string, data []byte, mode os.FileMode) (string, error) {
	temporary, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	remove := true
	defer func() {
		temporary.Close()
		if remove {
			os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return "", err
	}
	if _, err := temporary.Write(data); err != nil {
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	remove = false
	return temporaryPath, nil
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	if err := ensureParentDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	temporaryPath, err := writeTemporaryFile(filepath.Dir(path), ".vps-panel-xray-*", data, mode)
	if err != nil {
		return err
	}
	defer os.Remove(temporaryPath)
	return replaceFile(temporaryPath, path, mode)
}

func ensureParentDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("parent path exists and is not a directory")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

func replaceFile(source, destination string, mode os.FileMode) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	return os.Chmod(destination, mode)
}

func ensureSecureDirectory(path string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("path exists and is not a directory")
		}
		return os.Chmod(path, mode)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func readOptionalFile(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, errors.New("path is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func directoryHasEntries(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return true, nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer directory.Close()
	_, err = directory.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	return err == nil, err
}

func runXrayCommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output := &cappedBuffer{limit: managedXrayCommandOutputMax}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	return output.Bytes(), err
}

type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return written, nil
}

func (b *cappedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

func truncateDiagnostic(value []byte) string {
	value = bytes.TrimSpace(value)
	if len(value) > managedXrayCommandOutputMax {
		value = value[:managedXrayCommandOutputMax]
	}
	if len(value) == 0 {
		return "command failed"
	}
	return string(value)
}

func waitForXray(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
