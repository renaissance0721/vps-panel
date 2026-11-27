package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	managedSingboxVersion     = "1.14.4"
	managedSingboxBinary      = "/opt/vps-panel/sing-box/sing-box"
	managedSingboxConfig      = "/etc/vps-panel/sing-box/config.json"
	managedSingboxService     = "vps-panel-sing-box"
	managedSingboxACMECertDir = "/etc/vps-panel/acme/sing-box"
	managedSingboxMaxArchive  = 64 << 20
	managedSingboxMaxBinary   = 128 << 20
)

var managedSingboxHashes = map[string]string{
	"amd64": "699a1b510aa4396b2176d32d1181920399ebf1e267835da6f3b598d0f7c24968",
	"arm64": "436441f76ee2f7fe21b5b2d426d3cb261a480455a704865789ad533e4273ec26",
}

var (
	errSingboxDownload     = errors.New("Sing-box download failed")
	errSingboxChecksum     = errors.New("Sing-box checksum verification failed")
	errSingboxValidation   = errors.New("Sing-box configuration validation failed")
	errSingboxStart        = errors.New("Sing-box service restart failed")
	errSingboxHealth       = errors.New("Sing-box service or UDP listener is not healthy")
	errSingboxTLS          = errors.New("Sing-box TLS certificate is invalid, expired, or does not match its domain")
	errSingboxArchitecture = errors.New("Sing-box is unsupported on this architecture")
	errSingboxConflict     = errors.New("unmanaged Sing-box files or service conflict with managed paths")
	errSingboxBinary       = errors.New("Sing-box executable or archive is invalid")
	errSingboxInstall      = errors.New("Sing-box installation failed: check managed directory access and available disk space")
	errSingboxConfigWrite  = errors.New("Sing-box configuration deployment failed: check managed directory access and available disk space")
	errSingboxTLSSetup     = errors.New("Sing-box certificate preparation failed: check managed certificate and ACME directory access")
	errSingboxServiceSetup = errors.New("Sing-box service installation or enablement failed")
)

type singboxManager struct {
	installDir, binaryPath, markerPath, configDir, configPath, previousPath string
	goos, goarch, releaseBaseURL                                            string
	hashes                                                                  map[string]string
	service                                                                 serviceManager
	client                                                                  *http.Client
	runCommand                                                              func(context.Context, string, ...string) ([]byte, error)
	firewall                                                                *proxyFirewall
	acme                                                                    *acmeManager
	domains                                                                 []string
	probeUDP                                                                func(string, int) error
	wait                                                                    func(context.Context, time.Duration) error
}

func singboxServiceDefinition() serviceDefinition {
	return serviceDefinition{Name: managedSingboxService, Description: "VPS Panel Managed Sing-box", Command: managedSingboxBinary, Arguments: []string{"run", "-c", managedSingboxConfig}}
}

func newSingboxManager(acme *acmeManager) *singboxManager {
	return &singboxManager{
		installDir: "/opt/vps-panel/sing-box", binaryPath: managedSingboxBinary, markerPath: "/opt/vps-panel/sing-box/.managed-by-vps-panel",
		configDir: "/etc/vps-panel/sing-box", configPath: managedSingboxConfig, previousPath: "/etc/vps-panel/sing-box/config.previous.json",
		goos: runtime.GOOS, goarch: runtime.GOARCH, releaseBaseURL: "https://github.com/SagerNet/sing-box/releases/download",
		hashes:  managedSingboxHashes,
		service: newHostServiceManager(singboxServiceDefinition(), runXrayCommand), client: &http.Client{Timeout: 60 * time.Second},
		runCommand: runXrayCommand, firewall: newSingboxFirewall(), acme: acme, probeUDP: probeSingboxUDPListener, wait: waitForXray,
	}
}

func (m *singboxManager) apply(ctx context.Context, state desiredSingboxState) (applyErr error) {
	if state.Purge && (state.Enabled || len(state.Proxies) != 0) || !state.Enabled && len(state.Proxies) != 0 {
		return errUnsupportedManagedConfig
	}
	if state.Purge {
		return m.purge(ctx)
	}
	if !state.Enabled {
		return m.disable(ctx)
	}
	if len(state.Proxies) == 0 {
		return errUnsupportedManagedConfig
	}
	candidate, err := renderManagedSingboxConfig(state.Proxies)
	if err != nil {
		return err
	}
	domains := make([]string, 0)
	seen := make(map[string]bool)
	for _, proxy := range state.Proxies {
		if desiredTLSMode(proxy.TLS) == "acme" && !seen[proxy.ServerName] {
			if m.acme == nil {
				return errManagedACME
			}
			if _, err := m.acme.ensureCertificate(ctx, proxy.ServerName); err != nil {
				return fmt.Errorf("%w: %w", errSingboxTLSSetup, err)
			}
			seen[proxy.ServerName] = true
			domains = append(domains, proxy.ServerName)
		}
	}
	wasActive, err := m.service.IsActive(ctx)
	if err != nil {
		return err
	}
	previousBinary, upgraded, err := m.ensureBinary(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", errSingboxInstall, err)
	}
	defer func() {
		if applyErr != nil && upgraded && len(previousBinary) != 0 {
			if err := writeFileAtomically(m.binaryPath, previousBinary, 0o755); err != nil {
				applyErr = errors.Join(applyErr, errors.New("failed to restore previous Sing-box binary"))
			} else if wasActive {
				rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
				defer cancel()
				if err := m.service.Restart(rollbackCtx); err != nil {
					applyErr = errors.Join(applyErr, errors.New("failed to restart previous Sing-box binary"))
				}
			}
		}
	}()
	if err := ensureSecureDirectory(m.configDir, 0o700); err != nil {
		return fmt.Errorf("%w: prepare configuration directory: %w", errSingboxConfigWrite, err)
	}
	path, err := writeTemporaryFile(m.configDir, ".candidate-*.json", candidate, 0o600)
	if err != nil {
		return fmt.Errorf("%w: write candidate: %w", errSingboxConfigWrite, err)
	}
	defer os.Remove(path)
	if _, err := m.runCommand(ctx, m.binaryPath, "check", "-c", path); err != nil {
		return errSingboxValidation
	}
	changed, err := m.service.Install()
	if err != nil {
		return fmt.Errorf("%w: install service: %w", errSingboxServiceSetup, err)
	}
	if changed {
		if err := m.service.DaemonReload(ctx); err != nil {
			return fmt.Errorf("%w: reload service: %w", errSingboxServiceSetup, err)
		}
	}
	if err := m.service.Enable(ctx); err != nil {
		return fmt.Errorf("%w: enable service: %w", errSingboxServiceSetup, err)
	}
	previous, exists, err := readOptionalFile(m.configPath)
	if err != nil {
		return fmt.Errorf("%w: read configuration: %w", errSingboxConfigWrite, err)
	}
	active, err := m.service.IsActive(ctx)
	if err != nil {
		return err
	}
	if exists && bytes.Equal(previous, candidate) && !upgraded && active {
		if err := m.checkHealthy(ctx, state.Proxies); err == nil {
			m.domains = domains
			return m.firewall.reconcileRules(ctx, singboxFirewallRules(state.Proxies))
		}
	}
	if exists {
		if err := writeFileAtomically(m.previousPath, previous, 0o600); err != nil {
			return fmt.Errorf("%w: save previous configuration: %w", errSingboxConfigWrite, err)
		}
	}
	if err := replaceFile(path, m.configPath, 0o600); err != nil {
		return fmt.Errorf("%w: install configuration: %w", errSingboxConfigWrite, err)
	}
	if err := m.service.Restart(ctx); err != nil {
		return m.rollback(ctx, previous, exists, errSingboxStart)
	}
	if err := m.checkHealthy(ctx, state.Proxies); err != nil {
		return m.rollback(ctx, previous, exists, err)
	}
	if err := m.firewall.reconcileRules(ctx, singboxFirewallRules(state.Proxies)); err != nil {
		return m.rollback(ctx, previous, exists, fmt.Errorf("Sing-box firewall: %w", err))
	}
	m.domains = domains
	return nil
}

func (m *singboxManager) rollback(ctx context.Context, previous []byte, exists bool, cause error) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if !exists {
		stopErr := m.service.Stop(rollbackCtx)
		removeErr := os.Remove(m.configPath)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		firewallErr := m.firewall.reconcileRules(rollbackCtx, nil)
		return errors.Join(cause, stopErr, removeErr, firewallErr)
	}
	if err := writeFileAtomically(m.configPath, previous, 0o600); err != nil {
		return errors.Join(cause, fmt.Errorf("restore Sing-box configuration: %w", err))
	}
	if err := m.service.Restart(rollbackCtx); err != nil {
		return errors.Join(cause, errors.New("restart previous Sing-box configuration failed"))
	}
	var config singboxConfig
	if json.Unmarshal(previous, &config) == nil {
		rules := make([]firewallRule, 0, len(config.Inbounds))
		listeners := make([]desiredProxy, 0, len(config.Inbounds))
		for _, inbound := range config.Inbounds {
			rules = append(rules, firewallRule{port: inbound.ListenPort, protocol: "udp"})
			listeners = append(listeners, desiredProxy{Listen: inbound.Listen, Port: inbound.ListenPort})
		}
		if err := m.checkHealthy(rollbackCtx, listeners); err != nil {
			return errors.Join(cause, errors.New("previous Sing-box service is not healthy"))
		}
		if err := m.firewall.reconcileRules(rollbackCtx, rules); err != nil {
			return errors.Join(cause, err)
		}
	}
	return cause
}

func singboxFirewallRules(proxies []desiredProxy) []firewallRule {
	rules := make([]firewallRule, 0, len(proxies))
	for _, p := range proxies {
		rules = append(rules, firewallRule{port: p.Port, protocol: "udp"})
	}
	return rules
}

func (m *singboxManager) checkHealthy(ctx context.Context, proxies []desiredProxy) error {
	consecutive := 0
	for attempt := 0; attempt < 8; attempt++ {
		active, err := m.service.IsActive(ctx)
		if err != nil {
			return err
		}
		valid := active
		for _, p := range proxies {
			if m.probeUDP(p.Listen, p.Port) != nil {
				valid = false
			}
		}
		if valid {
			consecutive++
			if consecutive == 2 {
				return nil
			}
		} else {
			consecutive = 0
		}
		if attempt < 7 {
			if err := m.wait(ctx, 500*time.Millisecond); err != nil {
				return err
			}
		}
	}
	return errSingboxHealth
}

func probeSingboxUDPListener(_ string, port int) error {
	wanted := fmt.Sprintf("%04X", port)
	readable := false
	for _, path := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		readable = true
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && strings.HasSuffix(strings.ToUpper(fields[1]), ":"+wanted) {
				return nil
			}
		}
	}
	if !readable {
		return errors.New("UDP listener table unavailable")
	}
	return errors.New("UDP listener not found")
}

func (m *singboxManager) renewCertificates(ctx context.Context) error {
	for _, domain := range m.domains {
		if _, err := m.acme.ensureCertificate(ctx, domain); err != nil {
			return err
		}
	}
	return nil
}

func (m *singboxManager) disable(ctx context.Context) error {
	managed, err := regularFileExists(m.markerPath)
	if err != nil || !managed {
		return err
	}
	unitExists, err := pathExists(m.service.Path())
	if err != nil {
		return err
	}
	if unitExists {
		if err := m.service.Stop(ctx); err != nil {
			return err
		}
		if err := m.service.Disable(ctx); err != nil {
			return err
		}
	}
	for _, path := range []string{m.configPath, m.previousPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	m.domains = nil
	return m.firewall.reconcileRules(ctx, nil)
}

func (m *singboxManager) purge(ctx context.Context) error {
	managed, err := regularFileExists(m.markerPath)
	if err != nil || !managed {
		return err
	}
	if err := m.disable(ctx); err != nil {
		return err
	}
	if err := m.firewall.purge(ctx); err != nil {
		return err
	}
	unit := m.service.Path()
	if unit != "" {
		if err := os.Remove(unit); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := m.service.DaemonReload(ctx); err != nil {
			return err
		}
	}
	for _, path := range []string{m.binaryPath, m.markerPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (m *singboxManager) ensureBinary(ctx context.Context) ([]byte, bool, error) {
	hashes := m.hashes
	if hashes == nil {
		hashes = managedSingboxHashes
	}
	hash, ok := hashes[m.goarch]
	if m.goos != "linux" || !ok {
		return nil, false, errSingboxArchitecture
	}
	managed, err := regularFileExists(m.markerPath)
	if err != nil {
		return nil, false, err
	}
	if !managed {
		for _, dir := range []string{m.installDir, m.configDir} {
			occupied, err := directoryHasEntries(dir)
			if err != nil {
				return nil, false, err
			}
			if occupied {
				return nil, false, errSingboxConflict
			}
		}
		if exists, err := pathExists(m.service.Path()); err != nil || exists {
			if err != nil {
				return nil, false, err
			}
			return nil, false, errSingboxConflict
		}
	}
	previous, exists, err := readOptionalFile(m.binaryPath)
	if err != nil {
		return nil, false, err
	}
	if exists && m.verifyBinary(ctx, m.binaryPath) == nil {
		return nil, false, nil
	}
	if err := ensureSecureDirectory(m.installDir, 0o755); err != nil {
		return nil, false, err
	}
	url := fmt.Sprintf("%s/v%s/sing-box-%s-linux-%s.tar.gz", strings.TrimRight(m.releaseBaseURL, "/"), managedSingboxVersion, managedSingboxVersion, m.goarch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, false, errSingboxDownload
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("%w: HTTP %d", errSingboxDownload, resp.StatusCode)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, managedSingboxMaxArchive+1))
	if err != nil || len(archive) > managedSingboxMaxArchive {
		return nil, false, errSingboxBinary
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, false, errSingboxChecksum
	}
	binary, err := extractSingboxBinary(archive)
	if err != nil {
		return nil, false, err
	}
	staged, err := writeTemporaryFile(m.installDir, ".singbox-candidate-*", binary, 0o755)
	if err != nil {
		return nil, false, err
	}
	defer os.Remove(staged)
	if err := m.verifyBinary(ctx, staged); err != nil {
		return nil, false, err
	}
	if !managed {
		if err := writeFileAtomically(m.markerPath, []byte("managed by vps-panel\n"), 0o644); err != nil {
			return nil, false, err
		}
	}
	if err := replaceFile(staged, m.binaryPath, 0o755); err != nil {
		return nil, false, err
	}
	return previous, true, nil
}

func (m *singboxManager) verifyBinary(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errSingboxBinary
	}
	output, err := m.runCommand(ctx, path, "version")
	fields := strings.Fields(string(output))
	if err != nil || len(fields) < 3 || fields[0] != "sing-box" || fields[1] != "version" || fields[2] != managedSingboxVersion {
		return errSingboxBinary
	}
	return nil
}

func extractSingboxBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, errSingboxBinary
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var binary []byte
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errSingboxBinary
		}
		if filepath.Base(header.Name) != "sing-box" || header.Typeflag != tar.TypeReg {
			continue
		}
		if binary != nil || header.Size <= 0 || header.Size > managedSingboxMaxBinary {
			return nil, errSingboxBinary
		}
		binary, err = io.ReadAll(io.LimitReader(reader, managedSingboxMaxBinary+1))
		if err != nil || int64(len(binary)) != header.Size {
			return nil, errSingboxBinary
		}
	}
	if binary == nil {
		return nil, errSingboxBinary
	}
	return binary, nil
}
