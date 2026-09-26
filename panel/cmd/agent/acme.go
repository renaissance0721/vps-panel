package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	managedACMEInstallDir = "/opt/vps-panel/acme"
	managedACMEHomeDir    = "/var/lib/vps-panel/acme"
	managedACMECertDir    = "/etc/vps-panel/xray/certs"
	managedACMERevision   = "3661fd86b6304115e42f43910e6dd452ab9866d6" // acme.sh 3.1.4
	managedACMESHA256     = "fcabf274d4f96966ec933879ae0257266e8ef2f7d16161f14b84dd896c0cac32"
	managedACMEMaxBytes   = 1 << 20
)

var errManagedACME = errors.New("ACME certificate issuance failed: verify DNS points to this server and public TCP/80 is reachable")

type certificatePaths struct {
	Fullchain  string
	PrivateKey string
}

type acmeManager struct {
	mu                sync.Mutex
	installDir        string
	homeDir           string
	certDir           string
	client            *http.Client
	scriptURL         string
	scriptSHA256      string
	now               func() time.Time
	runCommand        func(context.Context, string, ...string) ([]byte, error)
	lookPath          func(string) (string, error)
	reconcileFirewall func(context.Context, []firewallRule) error
}

func newACMEManager() *acmeManager {
	firewall := &proxyFirewall{owner: "acme", lookPath: exec.LookPath, runCommand: runFirewallCommand}
	return &acmeManager{
		installDir: managedACMEInstallDir, homeDir: managedACMEHomeDir, certDir: managedACMECertDir,
		client: &http.Client{Timeout: 30 * time.Second}, now: time.Now,
		scriptURL:    "https://raw.githubusercontent.com/acmesh-official/acme.sh/" + managedACMERevision + "/acme.sh",
		scriptSHA256: managedACMESHA256,
		runCommand:   runACMECommand, lookPath: exec.LookPath, reconcileFirewall: firewall.reconcileRules,
	}
}

func managedACMECertificatePaths(root, domain string) certificatePaths {
	directory := filepath.Join(root, domain)
	return certificatePaths{Fullchain: filepath.Join(directory, "fullchain.pem"), PrivateKey: filepath.Join(directory, "private.key")}
}

func validACMEDomain(domain string) bool {
	if domain == "" || len(domain) > 253 || domain != strings.ToLower(domain) || net.ParseIP(domain) != nil || !strings.Contains(domain, ".") {
		return false
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 || tld == "local" || tld == "localhost" {
		return false
	}
	if !strings.HasPrefix(tld, "xn--") {
		for _, character := range tld {
			if character < 'a' || character > 'z' {
				return false
			}
		}
	}
	return true
}

func (m *acmeManager) ensureCertificate(ctx context.Context, domain string) (paths certificatePaths, err error) {
	if !validACMEDomain(domain) {
		return certificatePaths{}, errUnsupportedManagedConfig
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	paths = managedACMECertificatePaths(m.certDir, domain)
	if err := m.ensureManagedDomainDirectory(filepath.Dir(paths.Fullchain)); err != nil {
		return certificatePaths{}, err
	}
	if valid, checkErr := certificateValidForDomain(paths, domain, m.now(), 30*24*time.Hour); checkErr != nil {
		return certificatePaths{}, fmt.Errorf("inspect managed certificate: %w", checkErr)
	} else if valid {
		if err := os.Chmod(paths.PrivateKey, 0o600); err != nil {
			return certificatePaths{}, err
		}
		if err := os.Chmod(paths.Fullchain, 0o644); err != nil {
			return certificatePaths{}, err
		}
		return paths, nil
	}
	if err := m.ensureTool(ctx); err != nil {
		return certificatePaths{}, err
	}
	if err := ensureSecureDirectory(m.homeDir, 0o700); err != nil {
		return certificatePaths{}, fmt.Errorf("prepare ACME state: %w", err)
	}
	if err := m.ensureChallengeDependency(ctx); err != nil {
		return certificatePaths{}, err
	}
	if err := m.reconcileFirewall(ctx, []firewallRule{{port: 80, protocol: "tcp"}}); err != nil {
		return certificatePaths{}, fmt.Errorf("%w: open temporary TCP/80 rule: %v", errManagedACME, err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if cleanupErr := m.reconcileFirewall(cleanupContext, nil); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove temporary ACME TCP/80 rule: %w", cleanupErr))
		}
	}()

	arguments := []string{"--home", m.homeDir, "--config-home", m.homeDir, "--cert-home", filepath.Join(m.homeDir, "certs")}
	_, certExists, readErr := readOptionalFile(paths.Fullchain)
	if readErr != nil {
		return certificatePaths{}, fmt.Errorf("inspect existing certificate: %w", readErr)
	}
	if certExists {
		arguments = append(arguments, "--renew", "-d", domain, "--ecc", "--force", "--server", "letsencrypt")
	} else {
		arguments = append(arguments, "--issue", "--standalone", "-d", domain, "--keylength", "ec-256", "--force", "--server", "letsencrypt")
	}
	if _, runErr := m.runCommand(ctx, "sh", append([]string{filepath.Join(m.installDir, "acme.sh")}, arguments...)...); runErr != nil {
		return certificatePaths{}, errManagedACME
	}
	// acme.sh remembers --install-cert destinations for future renewals. Keep these
	// staging paths stable; Xray reads only the validated, promoted files above.
	staging := filepath.Join(filepath.Dir(paths.Fullchain), ".acme-output")
	if err := ensureSecureDirectory(staging, 0o700); err != nil {
		return certificatePaths{}, fmt.Errorf("stage managed certificate: %w", err)
	}
	staged := certificatePaths{Fullchain: filepath.Join(staging, "fullchain.pem"), PrivateKey: filepath.Join(staging, "private.key")}
	installArgs := append([]string{"--home", m.homeDir, "--config-home", m.homeDir, "--cert-home", filepath.Join(m.homeDir, "certs")},
		"--install-cert", "-d", domain, "--ecc", "--fullchain-file", staged.Fullchain, "--key-file", staged.PrivateKey)
	if _, runErr := m.runCommand(ctx, "sh", append([]string{filepath.Join(m.installDir, "acme.sh")}, installArgs...)...); runErr != nil {
		return certificatePaths{}, fmt.Errorf("%w: install certificate", errManagedACME)
	}
	if err := os.Chmod(staged.PrivateKey, 0o600); err != nil {
		return certificatePaths{}, fmt.Errorf("%w: secure staged private key", errManagedACME)
	}
	valid, checkErr := certificateValidForDomain(staged, domain, m.now(), time.Hour)
	if checkErr != nil || !valid {
		return certificatePaths{}, fmt.Errorf("%w: issued certificate is invalid", errManagedACME)
	}
	fullchain, readCertErr := os.ReadFile(staged.Fullchain)
	key, readKeyErr := os.ReadFile(staged.PrivateKey)
	if readCertErr != nil || readKeyErr != nil {
		return certificatePaths{}, fmt.Errorf("%w: read staged certificate", errManagedACME)
	}
	oldCert, hadCert, readErr := readOptionalFile(paths.Fullchain)
	if readErr != nil {
		return certificatePaths{}, readErr
	}
	if err := writeFileAtomically(paths.Fullchain, fullchain, 0o644); err != nil {
		return certificatePaths{}, fmt.Errorf("install managed certificate: %w", err)
	}
	if err := writeFileAtomically(paths.PrivateKey, key, 0o600); err != nil {
		if hadCert {
			_ = writeFileAtomically(paths.Fullchain, oldCert, 0o644)
		} else {
			_ = os.Remove(paths.Fullchain)
		}
		return certificatePaths{}, fmt.Errorf("install managed private key: %w", err)
	}
	return paths, nil
}

func (m *acmeManager) purge(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reconcileFirewall != nil {
		if err := m.reconcileFirewall(ctx, nil); err != nil {
			return fmt.Errorf("%w: clear managed ACME firewall: %v", errManagedRuntimePurge, err)
		}
	}
	marker := filepath.Join(m.installDir, ".managed-by-vps-panel")
	managed, err := regularFileExists(marker)
	if err != nil {
		return fmt.Errorf("%w: inspect ACME ownership: %v", errManagedRuntimePurge, err)
	}
	if managed {
		if err := removeDirectoryContentsExcept(m.installDir, filepath.Base(marker)); err != nil {
			return fmt.Errorf("%w: remove managed ACME runtime: %v", errManagedRuntimePurge, err)
		}
	}
	if err := removeDirectoryContents(m.homeDir); err != nil {
		return fmt.Errorf("%w: remove managed ACME state: %v", errManagedRuntimePurge, err)
	}
	entries, err := os.ReadDir(m.certDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect managed ACME certificates: %v", errManagedRuntimePurge, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(m.certDir, entry.Name())
		owned, markerErr := regularFileExists(filepath.Join(directory, ".managed-by-vps-panel"))
		if markerErr != nil {
			return fmt.Errorf("%w: inspect managed ACME certificate ownership: %v", errManagedRuntimePurge, markerErr)
		}
		if owned {
			if err := os.RemoveAll(directory); err != nil {
				return fmt.Errorf("%w: remove managed ACME certificate: %v", errManagedRuntimePurge, err)
			}
		}
	}
	if err := os.Remove(m.certDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		remaining, readErr := os.ReadDir(m.certDir)
		if readErr != nil || len(remaining) == 0 {
			return fmt.Errorf("%w: remove managed ACME certificate directory: %v", errManagedRuntimePurge, err)
		}
	}
	if managed {
		if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: remove managed ACME ownership marker: %v", errManagedRuntimePurge, err)
		}
	}
	return nil
}

func (m *acmeManager) ensureManagedDomainDirectory(directory string) error {
	if err := ensureSecureDirectory(m.certDir, 0o700); err != nil {
		return fmt.Errorf("prepare managed certificate directory: %w", err)
	}
	marker := filepath.Join(directory, ".managed-by-vps-panel")
	managed, err := regularFileExists(marker)
	if err != nil {
		return err
	}
	if !managed {
		occupied, err := directoryHasEntries(directory)
		if err != nil {
			return err
		}
		if occupied {
			return fmt.Errorf("%w: unmanaged certificate directory", errManagedACME)
		}
	}
	if err := ensureSecureDirectory(directory, 0o700); err != nil {
		return fmt.Errorf("prepare domain certificate directory: %w", err)
	}
	if !managed {
		return writeFileAtomically(marker, []byte("managed by vps-panel\n"), 0o644)
	}
	return nil
}

func certificateValidForDomain(paths certificatePaths, domain string, now time.Time, renewBefore time.Duration) (bool, error) {
	certificate, certExists, err := readOptionalFile(paths.Fullchain)
	if err != nil || !certExists {
		return false, err
	}
	privateKey, keyExists, err := readOptionalFile(paths.PrivateKey)
	if err != nil || !keyExists {
		return false, err
	}
	pair, err := tls.X509KeyPair(certificate, privateKey)
	if err != nil || len(pair.Certificate) == 0 {
		return false, nil
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.VerifyHostname(domain) != nil || now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) || leaf.NotAfter.Sub(now) <= renewBefore {
		return false, nil
	}
	return true, nil
}

func (m *acmeManager) ensureTool(ctx context.Context) error {
	marker := filepath.Join(m.installDir, ".managed-by-vps-panel")
	script := filepath.Join(m.installDir, "acme.sh")
	managed, err := regularFileExists(marker)
	if err != nil {
		return fmt.Errorf("inspect ACME ownership: %w", err)
	}
	if !managed {
		occupied, err := directoryHasEntries(m.installDir)
		if err != nil {
			return err
		}
		if occupied {
			return fmt.Errorf("%w: unmanaged ACME install directory", errManagedACME)
		}
	}
	if err := ensureSecureDirectory(m.installDir, 0o755); err != nil {
		return fmt.Errorf("prepare managed ACME tool directory: %w", err)
	}
	if existing, exists, err := readOptionalFile(script); err != nil {
		return err
	} else if exists && managed {
		sum := sha256.Sum256(existing)
		if hex.EncodeToString(sum[:]) == m.scriptSHA256 {
			return nil
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.scriptURL, nil)
	if err != nil {
		return err
	}
	response, err := m.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: download official acme.sh", errManagedACME)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: download official acme.sh: HTTP %d", errManagedACME, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, managedACMEMaxBytes+1))
	if err != nil || len(data) > managedACMEMaxBytes {
		return fmt.Errorf("%w: read official acme.sh", errManagedACME)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != m.scriptSHA256 {
		return fmt.Errorf("%w: official acme.sh checksum mismatch", errManagedACME)
	}
	if err := writeFileAtomically(script, data, 0o755); err != nil {
		return err
	}
	return writeFileAtomically(marker, []byte("managed by vps-panel\n"), 0o644)
}

func (m *acmeManager) ensureChallengeDependency(ctx context.Context) error {
	_, opensslErr := m.lookPath("openssl")
	challengeAvailable := false
	for _, name := range []string{"socat", "python3", "python2", "python"} {
		if _, err := m.lookPath(name); err == nil {
			challengeAvailable = true
			break
		}
	}
	if challengeAvailable && opensslErr == nil {
		return nil
	}
	packages := make([]string, 0, 2)
	if !challengeAvailable {
		packages = append(packages, "socat")
	}
	if opensslErr != nil {
		packages = append(packages, "openssl")
	}
	if _, err := m.lookPath("apk"); err == nil {
		if _, err := m.runCommand(ctx, "apk", append([]string{"add", "--no-cache"}, packages...)...); err == nil {
			return nil
		}
	} else if _, err := m.lookPath("apt-get"); err == nil {
		name, prefix := "apt-get", []string{}
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			if _, err := m.lookPath("systemd-run"); err == nil {
				name = "systemd-run"
				prefix = []string{"--quiet", "--wait", "--collect", "--setenv=DEBIAN_FRONTEND=noninteractive", "apt-get"}
			}
		}
		_, _ = m.runCommand(ctx, name, append(append([]string{}, prefix...), "update")...)
		if _, err := m.runCommand(ctx, name, append(append(append([]string{}, prefix...), "install", "-y"), packages...)...); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%w: install openssl and socat or python3 for standalone challenge", errManagedACME)
}

func runACMECommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output := &cappedBuffer{limit: 4 << 10}
	command.Stdout, command.Stderr = output, output
	if name == "apt-get" {
		command.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	}
	err := command.Run()
	return bytes.TrimSpace(output.Bytes()), err
}
