package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"strconv"
	"strings"
	"time"
)

const (
	managedRealmVersion          = "v2.9.4"
	managedRealmReleaseBaseURL   = "https://github.com/zhboner/realm/releases/download"
	managedRealmServiceName      = "vps-panel-realm"
	managedRealmBinaryPath       = "/opt/vps-panel/realm/realm"
	managedRealmConfigPath       = "/etc/vps-panel/realm/config.toml"
	managedRealmMaxDownloadBytes = 32 << 20
	managedRealmMaxBinaryBytes   = 32 << 20
	managedRealmRollbackTimeout  = 15 * time.Second
)

var (
	errManagedRealmDownload   = errors.New("managed Realm download failed")
	errManagedRealmChecksum   = errors.New("managed Realm checksum verification failed")
	errManagedRealmValidation = errors.New("managed Realm configuration validation failed")
	errManagedRealmStart      = errors.New("managed Realm service failed to start")
	errManagedRealmHealth     = errors.New("managed Realm listener health check failed")
	errManagedRealmStop       = errors.New("managed Realm service failed to stop")
	errManagedRealmConflict   = errors.New("existing unmanaged Realm installation detected")
	errManagedRealmArch       = errors.New("managed Realm is unsupported on this architecture")
	errManagedRealmFirewall   = errors.New("managed Realm firewall synchronization failed")
)

type managedRealmAsset struct {
	name   string
	sha256 string
}

type realmRuntimeTarget struct {
	goos   string
	goarch string
	libc   libcKind
}

var managedRealmAssets = map[realmRuntimeTarget]managedRealmAsset{
	{goos: "linux", goarch: "amd64", libc: libcGlibc}: {
		name:   "realm-x86_64-unknown-linux-gnu.tar.gz",
		sha256: "9dec109386b8abc828b452d0d1cecde35b7a2f8cfa93eae757fe9c248ad07ddd",
	},
	{goos: "linux", goarch: "arm64", libc: libcGlibc}: {
		name:   "realm-aarch64-unknown-linux-gnu.tar.gz",
		sha256: "1f7f06e82fe0ea798b5c8e8e32906ee212a7085629a1c5cef9957ca270fcad99",
	},
	{goos: "linux", goarch: "amd64", libc: libcMusl}: {
		name:   "realm-x86_64-unknown-linux-musl.tar.gz",
		sha256: "a19b86c4ae4642d5864821b41d23633c0c91df279a88496c05834dc584169175",
	},
	{goos: "linux", goarch: "arm64", libc: libcMusl}: {
		name:   "realm-aarch64-unknown-linux-musl.tar.gz",
		sha256: "0195e77ca99713166e25ff85fefe042049c79fdaddf500e8ffd9ba77494a029c",
	},
}

type realmListener struct {
	address string
	port    int
	tcp     bool
	udp     bool
}

type realmManager struct {
	installDir        string
	binaryPath        string
	markerPath        string
	configDir         string
	configPath        string
	previousPath      string
	unitPath          string
	service           serviceManager
	unmanagedUnits    []string
	goos              string
	goarch            string
	libc              libcKind
	releaseBaseURL    string
	assets            map[realmRuntimeTarget]managedRealmAsset
	client            *http.Client
	runCommand        func(context.Context, string, ...string) ([]byte, error)
	validateConfig    func(context.Context, string, string) error
	probeTCP          func(context.Context, string, int) error
	probeUDP          func(string, int) error
	reconcileFirewall func(context.Context, []firewallRule) error
	wait              func(context.Context, time.Duration) error
	healthAttempts    int
	healthCheckDelay  time.Duration
}

func newRealmManager() *realmManager {
	firewall := newRealmFirewall()
	environment, environmentErr := detectHostEnvironment()
	var service serviceManager
	libc := libcGlibc
	if environmentErr != nil {
		service = &unsupportedServiceManager{err: environmentErr}
	} else {
		service = newServiceManager(environment.InitSystem, realmServiceDefinition(), "", runXrayCommand)
		libc = environment.Libc
	}
	return &realmManager{
		installDir:     "/opt/vps-panel/realm",
		binaryPath:     managedRealmBinaryPath,
		markerPath:     "/opt/vps-panel/realm/.managed-by-vps-panel",
		configDir:      "/etc/vps-panel/realm",
		configPath:     managedRealmConfigPath,
		previousPath:   "/etc/vps-panel/realm/config.previous.toml",
		unitPath:       service.Path(),
		service:        service,
		unmanagedUnits: []string{"/etc/systemd/system/realm.service", "/lib/systemd/system/realm.service", "/usr/lib/systemd/system/realm.service", "/etc/init.d/realm"},
		goos:           runtime.GOOS,
		goarch:         runtime.GOARCH,
		libc:           libc,
		releaseBaseURL: managedRealmReleaseBaseURL,
		assets:         managedRealmAssets,
		client:         &http.Client{Timeout: 60 * time.Second},
		runCommand:     runXrayCommand,
		validateConfig: validateRealmConfig,
		probeTCP:       probeRealmTCPListener,
		probeUDP:       probeRealmUDPListener,
		reconcileFirewall: func(ctx context.Context, rules []firewallRule) error {
			if err := firewall.reconcileRules(ctx, rules); err != nil {
				return fmt.Errorf("%w: %v", errManagedRealmFirewall, err)
			}
			return nil
		},
		wait:             waitForXray,
		healthAttempts:   6,
		healthCheckDelay: 500 * time.Millisecond,
	}
}

func (m *realmManager) apply(ctx context.Context, state desiredRealmState) error {
	if !state.Enabled && len(state.Relays) != 0 {
		return errUnsupportedManagedConfig
	}
	if !state.Enabled {
		return m.disable(ctx)
	}
	return m.enable(ctx, state.Relays)
}

func (m *realmManager) disable(ctx context.Context) error {
	managed, err := regularFileExists(m.markerPath)
	if err != nil {
		return fmt.Errorf("inspect managed Realm marker: %w", err)
	}
	if !managed {
		return nil
	}
	unitExists, err := pathExists(m.unitPath)
	if err != nil {
		return fmt.Errorf("inspect managed Realm service: %w", err)
	}
	if unitExists {
		if err := m.serviceManager().Stop(ctx); err != nil {
			return fmt.Errorf("%w: %v", errManagedRealmStop, err)
		}
		if err := m.serviceManager().Disable(ctx); err != nil {
			return fmt.Errorf("%w: %v", errManagedRealmStop, err)
		}
	}
	for _, path := range []string{m.configPath, m.previousPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove managed Realm config: %w", err)
		}
	}
	return m.reconcileFirewall(ctx, nil)
}

func (m *realmManager) enable(ctx context.Context, relays []desiredRelay) error {
	candidate, err := renderManagedRealmConfig(relays)
	if err != nil {
		return err
	}
	validationCandidate, err := renderManagedRealmValidationConfig(relays)
	if err != nil {
		return err
	}
	listeners := expectedRealmListeners(relays)
	rules := expectedRealmFirewallRules(relays)
	if err := m.ensureManagedRealm(ctx); err != nil {
		return err
	}
	if err := ensureSecureDirectory(m.configDir, 0o700); err != nil {
		return fmt.Errorf("prepare managed Realm config directory: %w", err)
	}
	candidatePath, err := writeTemporaryFile(m.configDir, ".config-candidate-*.toml", candidate, 0o600)
	if err != nil {
		return fmt.Errorf("write managed Realm candidate: %w", err)
	}
	defer os.Remove(candidatePath)
	validationPath, err := writeTemporaryFile(m.configDir, ".config-validation-*.toml", validationCandidate, 0o600)
	if err != nil {
		return fmt.Errorf("write managed Realm validation candidate: %w", err)
	}
	defer os.Remove(validationPath)
	if err := m.validateConfig(ctx, m.binaryPath, validationPath); err != nil {
		return fmt.Errorf("%w: %v", errManagedRealmValidation, err)
	}
	unitChanged, err := m.ensureUnit()
	if err != nil {
		return err
	}
	if unitChanged {
		if err := m.serviceManager().DaemonReload(ctx); err != nil {
			return fmt.Errorf("install managed Realm service: %w", err)
		}
	}
	if err := m.serviceManager().Enable(ctx); err != nil {
		return fmt.Errorf("%w: %v", errManagedRealmStart, err)
	}
	current, exists, err := readOptionalFile(m.configPath)
	if err != nil {
		return fmt.Errorf("read managed Realm config: %w", err)
	}
	if exists && bytes.Equal(current, candidate) {
		if err := os.Chmod(m.configPath, 0o600); err != nil {
			return fmt.Errorf("secure managed Realm config: %w", err)
		}
		active, err := m.isActive(ctx)
		if err != nil {
			return err
		}
		if active && m.listenersHealthy(ctx, listeners) {
			return m.reconcileFirewall(ctx, rules)
		}
		var startErr error
		if active {
			startErr = m.serviceManager().Restart(ctx)
		} else {
			startErr = m.serviceManager().Start(ctx)
		}
		if startErr != nil {
			return fmt.Errorf("%w: %v", errManagedRealmStart, startErr)
		}
		if err := m.waitUntilHealthy(ctx, listeners); err != nil {
			return err
		}
		return m.reconcileFirewall(ctx, rules)
	}
	previousListeners, previousRules, previousKnown := parseRenderedRealmState(current)
	if exists {
		if err := writeFileAtomically(m.previousPath, current, 0o600); err != nil {
			return fmt.Errorf("save previous managed Realm config: %w", err)
		}
	}
	if err := replaceFile(candidatePath, m.configPath, 0o600); err != nil {
		return fmt.Errorf("install managed Realm config: %w", err)
	}
	if err := m.serviceManager().Restart(ctx); err != nil {
		return m.rollbackFailedApply(ctx, exists, previousListeners, previousRules, previousKnown, fmt.Errorf("%w: %v", errManagedRealmStart, err))
	}
	if err := m.waitUntilHealthy(ctx, listeners); err != nil {
		return m.rollbackFailedApply(ctx, exists, previousListeners, previousRules, previousKnown, err)
	}
	if err := m.reconcileFirewall(ctx, rules); err != nil {
		return m.rollbackFailedApply(ctx, exists, previousListeners, previousRules, previousKnown, err)
	}
	return nil
}

func (m *realmManager) rollbackFailedApply(ctx context.Context, hasPrevious bool, previousListeners []realmListener, previousRules []firewallRule, previousKnown bool, applyErr error) error {
	log.Printf("new Realm config apply failed: %v", applyErr)
	rollbackContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), managedRealmRollbackTimeout)
	defer cancel()
	if !hasPrevious {
		if err := m.serviceManager().Stop(rollbackContext); err != nil {
			log.Printf("Realm rollback failed: stop service: %v", err)
		}
		if err := m.serviceManager().Disable(rollbackContext); err != nil {
			log.Printf("Realm rollback failed: disable service: %v", err)
		}
		if err := os.Remove(m.configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("Realm rollback failed: remove config: %v", err)
		}
		if err := m.reconcileFirewall(rollbackContext, nil); err != nil {
			log.Printf("Realm rollback failed: clear firewall: %v", err)
		}
		return applyErr
	}
	previous, exists, err := readOptionalFile(m.previousPath)
	if err != nil || !exists {
		log.Printf("Realm rollback failed: previous config unavailable: %v", err)
		return applyErr
	}
	if err := writeFileAtomically(m.configPath, previous, 0o600); err != nil {
		log.Printf("Realm rollback failed: restore config: %v", err)
		return applyErr
	}
	if err := m.serviceManager().Restart(rollbackContext); err != nil {
		log.Printf("Realm rollback failed: restart previous config: %v", err)
		return applyErr
	}
	if previousKnown {
		if err := m.waitUntilHealthy(rollbackContext, previousListeners); err != nil {
			log.Printf("Realm rollback failed: %v", err)
		}
		if err := m.reconcileFirewall(rollbackContext, previousRules); err != nil {
			log.Printf("Realm rollback failed: restore firewall: %v", err)
		}
	}
	return applyErr
}

func (m *realmManager) ensureManagedRealm(ctx context.Context) error {
	if m.goos != "linux" {
		return fmt.Errorf("%w: %s/%s", errManagedRealmArch, m.goos, m.goarch)
	}
	asset, ok := m.assets[realmRuntimeTarget{goos: m.goos, goarch: m.goarch, libc: m.libc}]
	if !ok {
		return fmt.Errorf("%w: %s/%s/%s", errManagedRealmArch, m.goos, m.goarch, m.libc)
	}
	if err := m.checkConflicts(); err != nil {
		return err
	}
	managed, err := regularFileExists(m.markerPath)
	if err != nil {
		return fmt.Errorf("inspect managed Realm marker: %w", err)
	}
	if managed {
		binaryExists, err := regularFileExists(m.binaryPath)
		if err != nil {
			return fmt.Errorf("inspect managed Realm binary: %w", err)
		}
		if binaryExists && m.verifyBinary(ctx, m.binaryPath) == nil {
			return os.Chmod(m.binaryPath, 0o755)
		}
	}
	if err := ensureSecureDirectory(m.installDir, 0o755); err != nil {
		return fmt.Errorf("prepare managed Realm install directory: %w", err)
	}
	archivePath, err := m.download(ctx, asset)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)
	stagedBinary, cleanup, err := m.extractBinary(archivePath)
	if err != nil {
		return fmt.Errorf("%w: %v", errManagedRealmDownload, err)
	}
	defer cleanup()
	if err := m.verifyBinary(ctx, stagedBinary); err != nil {
		return fmt.Errorf("install managed Realm binary: %w", err)
	}
	if err := writeFileAtomically(m.markerPath, []byte("managed by vps-panel\n"), 0o644); err != nil {
		return fmt.Errorf("write managed Realm marker: %w", err)
	}
	if err := replaceFile(stagedBinary, m.binaryPath, 0o755); err != nil {
		return fmt.Errorf("install managed Realm binary: %w", err)
	}
	return nil
}

func (m *realmManager) checkConflicts() error {
	for _, unit := range m.unmanagedUnits {
		exists, err := pathExists(unit)
		if err != nil {
			return fmt.Errorf("inspect unmanaged Realm unit: %w", err)
		}
		if exists {
			return errManagedRealmConflict
		}
	}
	managed, err := regularFileExists(m.markerPath)
	if err != nil {
		return fmt.Errorf("inspect managed Realm marker: %w", err)
	}
	if managed {
		return nil
	}
	for _, directory := range []string{m.installDir, m.configDir} {
		hasEntries, err := directoryHasEntries(directory)
		if err != nil {
			return fmt.Errorf("inspect managed Realm directory: %w", err)
		}
		if hasEntries {
			return errManagedRealmConflict
		}
	}
	for _, path := range []string{m.binaryPath, m.configPath, m.previousPath, m.unitPath} {
		if path == "" {
			continue
		}
		exists, err := pathExists(path)
		if err != nil {
			return fmt.Errorf("inspect managed Realm path: %w", err)
		}
		if exists {
			return errManagedRealmConflict
		}
	}
	return nil
}

func (m *realmManager) download(ctx context.Context, asset managedRealmAsset) (string, error) {
	requestURL := strings.TrimRight(m.releaseBaseURL, "/") + "/" + managedRealmVersion + "/" + asset.name
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: create request: %v", errManagedRealmDownload, err)
	}
	response, err := m.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errManagedRealmDownload, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: HTTP %s", errManagedRealmDownload, response.Status)
	}
	file, err := os.CreateTemp(m.installDir, ".realm-download-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("%w: %v", errManagedRealmDownload, err)
	}
	path := file.Name()
	success := false
	defer func() {
		file.Close()
		if !success {
			os.Remove(path)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, managedRealmMaxDownloadBytes+1))
	if err != nil || written > managedRealmMaxDownloadBytes {
		return "", fmt.Errorf("%w: invalid asset body", errManagedRealmDownload)
	}
	if hex.EncodeToString(hash.Sum(nil)) != asset.sha256 {
		return "", errManagedRealmChecksum
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("%w: %v", errManagedRealmDownload, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("%w: %v", errManagedRealmDownload, err)
	}
	success = true
	return path, nil
}

func (m *realmManager) extractBinary(archivePath string) (string, func(), error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return "", nil, err
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return "", nil, err
	}
	defer gzipReader.Close()
	temporaryDir, err := os.MkdirTemp(m.installDir, ".realm-extract-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(temporaryDir) }
	outputPath := filepath.Join(temporaryDir, "realm")
	found := false
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return "", nil, err
		}
		if filepath.Base(header.Name) != "realm" || header.Typeflag != tar.TypeReg {
			continue
		}
		if found || header.Size <= 0 || header.Size > managedRealmMaxBinaryBytes {
			cleanup()
			return "", nil, errors.New("invalid Realm archive")
		}
		output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		written, copyErr := io.Copy(output, io.LimitReader(reader, managedRealmMaxBinaryBytes+1))
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || written != header.Size || written > managedRealmMaxBinaryBytes {
			cleanup()
			return "", nil, errors.New("extract Realm binary")
		}
		found = true
	}
	if !found {
		cleanup()
		return "", nil, errors.New("Realm binary is missing from archive")
	}
	return outputPath, cleanup, nil
}

func (m *realmManager) verifyBinary(ctx context.Context, binaryPath string) error {
	info, err := os.Stat(binaryPath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("Realm binary is not a regular file")
	}
	output, err := m.runCommand(ctx, binaryPath, "--version")
	if err != nil {
		return fmt.Errorf("run Realm version check: %w", err)
	}
	text := strings.ToLower(string(output))
	if !strings.Contains(text, "realm") || !strings.Contains(text, strings.TrimPrefix(managedRealmVersion, "v")) {
		return fmt.Errorf("unexpected Realm version output: %s", truncateDiagnostic(output))
	}
	return nil
}

func validateRealmConfig(ctx context.Context, binaryPath, candidatePath string) error {
	validationContext, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(validationContext, binaryPath, "--config", candidatePath)
	output := &cappedBuffer{limit: managedXrayCommandOutputMax}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return errors.New("Realm validation process exited unexpectedly")
		}
		return fmt.Errorf("%v: %s", err, truncateDiagnostic(output.Bytes()))
	case <-validationContext.Done():
		<-done
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
}

func (m *realmManager) ensureUnit() (bool, error) {
	changed, err := m.serviceManager().Install()
	if err != nil {
		return false, fmt.Errorf("write managed Realm service: %w", err)
	}
	return changed, nil
}

func (m *realmManager) isActive(ctx context.Context) (bool, error) {
	return m.serviceManager().IsActive(ctx)
}

func realmServiceDefinition() serviceDefinition {
	return serviceDefinition{
		Name:               managedRealmServiceName,
		Description:        "VPS Panel Managed Realm",
		Command:            managedRealmBinaryPath,
		Arguments:          []string{"--config", managedRealmConfigPath},
		TimeoutStopSeconds: 10,
	}
}

func (m *realmManager) serviceManager() serviceManager {
	if m.service != nil {
		return m.service
	}
	return &systemdServiceManager{
		definition: realmServiceDefinition(),
		path:       m.unitPath,
		runCommand: m.runCommand,
	}
}

func (m *realmManager) waitUntilHealthy(ctx context.Context, listeners []realmListener) error {
	wasActive, consecutive := false, 0
	for attempt := 0; attempt < m.healthAttempts; attempt++ {
		active, err := m.isActive(ctx)
		if err != nil {
			return err
		}
		if active {
			wasActive = true
			if m.listenersHealthy(ctx, listeners) {
				consecutive++
				if consecutive == 2 || m.healthAttempts == 1 {
					return nil
				}
			} else {
				consecutive = 0
			}
		}
		if attempt+1 < m.healthAttempts {
			if err := m.wait(ctx, m.healthCheckDelay); err != nil {
				return fmt.Errorf("%w: %v", errManagedRealmStart, err)
			}
		}
	}
	if wasActive {
		return errManagedRealmHealth
	}
	return errManagedRealmStart
}

func (m *realmManager) listenersHealthy(ctx context.Context, listeners []realmListener) bool {
	for _, listener := range listeners {
		if listener.tcp && m.probeTCP(ctx, listener.address, listener.port) != nil {
			return false
		}
		if listener.udp && m.probeUDP(listener.address, listener.port) != nil {
			return false
		}
	}
	return true
}

func expectedRealmListeners(relays []desiredRelay) []realmListener {
	listeners := make([]realmListener, 0, len(relays))
	for _, relay := range relays {
		noTCP, useUDP, _ := realmNetworkOptions(relay.Network)
		listeners = append(listeners, realmListener{address: relay.ListenAddress, port: relay.ListenPort, tcp: !noTCP, udp: useUDP})
	}
	return listeners
}

func expectedRealmFirewallRules(relays []desiredRelay) []firewallRule {
	rules := make([]firewallRule, 0, len(relays)*2)
	for _, listener := range expectedRealmListeners(relays) {
		if listener.tcp {
			rules = append(rules, firewallRule{port: listener.port, protocol: "tcp"})
		}
		if listener.udp {
			rules = append(rules, firewallRule{port: listener.port, protocol: "udp"})
		}
	}
	return rules
}

func probeRealmTCPListener(ctx context.Context, address string, port int) error {
	if address == "0.0.0.0" {
		address = "127.0.0.1"
	} else if address == "::" || address == "::0" {
		address = "::1"
	}
	connection, err := (&net.Dialer{Timeout: 300 * time.Millisecond}).DialContext(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return connection.Close()
}

func probeRealmUDPListener(_ string, port int) error {
	wanted := strings.ToUpper(fmt.Sprintf("%04X", port))
	for _, path := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			parts := strings.Split(fields[1], ":")
			if len(parts) == 2 && strings.EqualFold(parts[1], wanted) {
				return nil
			}
		}
	}
	return errors.New("UDP listener not found")
}
