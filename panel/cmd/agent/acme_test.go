package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func testACMECertificate(t *testing.T, domain string, expires time.Time) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: now.Add(-time.Hour), NotAfter: expires, KeyUsage: x509.KeyUsageDigitalSignature,
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: now.Add(-time.Hour), NotAfter: expires, KeyUsage: x509.KeyUsageDigitalSignature,
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateDER})
}

func newTestACMEManager(t *testing.T) *acmeManager {
	t.Helper()
	root := t.TempDir()
	m := newACMEManager()
	m.installDir = filepath.Join(root, "opt", "vps-panel", "acme")
	m.homeDir = filepath.Join(root, "var", "lib", "vps-panel", "acme")
	m.certDir = filepath.Join(root, "etc", "vps-panel", "xray", "certs")
	m.lookPath = func(name string) (string, error) {
		if name == "socat" || name == "openssl" {
			return name, nil
		}
		return "", errors.New("not found")
	}
	m.reconcileFirewall = func(context.Context, []firewallRule) error { return nil }
	data := []byte("verified test acme.sh\n")
	sum := sha256.Sum256(data)
	m.scriptSHA256 = hex.EncodeToString(sum[:])
	if err := os.MkdirAll(m.installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.installDir, "acme.sh"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.installDir, ".managed-by-vps-panel"), []byte("managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return m
}

func writeTestACMECertificate(t *testing.T, paths certificatePaths, domain string, expires time.Time) {
	t.Helper()
	cert, key := testACMECertificate(t, domain, expires)
	if err := os.MkdirAll(filepath.Dir(paths.Fullchain), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(paths.Fullchain), ".managed-by-vps-panel"), []byte("managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Fullchain, cert, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PrivateKey, key, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestACMEValidCertificateDoesNotIssue(t *testing.T) {
	m := newTestACMEManager(t)
	paths := managedACMECertificatePaths(m.certDir, "jp.example.com")
	writeTestACMECertificate(t, paths, "jp.example.com", time.Now().Add(60*24*time.Hour))
	m.runCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("valid certificate triggered ACME command")
		return nil, nil
	}
	if got, err := m.ensureCertificate(t.Context(), "jp.example.com"); err != nil || got != paths {
		t.Fatalf("ensure existing certificate = %+v, %v", got, err)
	}
}

func TestACMEIssueRenewAndTemporaryFirewall(t *testing.T) {
	for _, scenario := range []struct {
		name, action string
		existing     bool
	}{
		{"issue", "--issue", false}, {"renew", "--renew", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			m := newTestACMEManager(t)
			paths := managedACMECertificatePaths(m.certDir, "jp.example.com")
			if scenario.existing {
				writeTestACMECertificate(t, paths, "jp.example.com", time.Now().Add(10*24*time.Hour))
			}
			cert, key := testACMECertificate(t, "jp.example.com", time.Now().Add(90*24*time.Hour))
			var calls [][]string
			var firewall []int
			m.reconcileFirewall = func(_ context.Context, rules []firewallRule) error {
				firewall = append(firewall, len(rules))
				return nil
			}
			m.runCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "sh" || len(args) == 0 || args[0] != filepath.Join(m.installDir, "acme.sh") {
					t.Fatalf("unexpected command: %s %v", name, args)
				}
				calls = append(calls, append([]string(nil), args...))
				if strings.Contains(strings.Join(args, " "), "--install-cert") {
					for index, arg := range args {
						if arg == "--fullchain-file" {
							if err := os.WriteFile(args[index+1], cert, 0o644); err != nil {
								t.Fatal(err)
							}
						}
						if arg == "--key-file" {
							if err := os.WriteFile(args[index+1], key, 0o600); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				return nil, nil
			}
			if _, err := m.ensureCertificate(t.Context(), "jp.example.com"); err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 || !strings.Contains(strings.Join(calls[0], " "), scenario.action) ||
				!strings.Contains(strings.Join(calls[0], " "), "--server letsencrypt") ||
				!strings.Contains(strings.Join(calls[0], " "), "-d jp.example.com") ||
				!strings.Contains(strings.Join(calls[1], " "), "--install-cert") ||
				len(firewall) != 2 || firewall[0] != 1 || firewall[1] != 0 {
				t.Fatalf("commands = %v, firewall = %v", calls, firewall)
			}
			if scenario.action == "--issue" && !strings.Contains(strings.Join(calls[0], " "), "--keylength ec-256") {
				t.Fatalf("issue did not use ECC P-256: %v", calls[0])
			}
			if valid, err := certificateValidForDomain(paths, "jp.example.com", time.Now(), 30*24*time.Hour); err != nil || !valid {
				t.Fatalf("installed certificate = %v, %v", valid, err)
			}
			if runtime.GOOS != "windows" {
				if info, err := os.Stat(paths.PrivateKey); err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("private key permissions = %v, %v", info, err)
				}
			}
		})
	}
}

func TestACMEFailurePreservesExistingCertificate(t *testing.T) {
	for _, failureAt := range []int{1, 2} {
		t.Run(fmt.Sprint(failureAt), func(t *testing.T) {
			m := newTestACMEManager(t)
			paths := managedACMECertificatePaths(m.certDir, "jp.example.com")
			writeTestACMECertificate(t, paths, "jp.example.com", time.Now().Add(5*24*time.Hour))
			beforeCert, _ := os.ReadFile(paths.Fullchain)
			beforeKey, _ := os.ReadFile(paths.PrivateKey)
			cleaned := false
			m.reconcileFirewall = func(_ context.Context, rules []firewallRule) error {
				if len(rules) == 0 {
					cleaned = true
				}
				return nil
			}
			calls := 0
			m.runCommand = func(context.Context, string, ...string) ([]byte, error) {
				calls++
				if calls == failureAt {
					return nil, errors.New("ACME operation failed")
				}
				return nil, nil
			}
			if _, err := m.ensureCertificate(t.Context(), "jp.example.com"); !errors.Is(err, errManagedACME) || !cleaned {
				t.Fatalf("failed issuance = %v, cleaned = %v", err, cleaned)
			}
			afterCert, _ := os.ReadFile(paths.Fullchain)
			afterKey, _ := os.ReadFile(paths.PrivateKey)
			if string(beforeCert) != string(afterCert) || string(beforeKey) != string(afterKey) {
				t.Fatal("failed renewal changed existing certificate")
			}
		})
	}
}

func TestACMERejectsUnsafeDomainAndDeduplicatesConcurrentRequest(t *testing.T) {
	m := newTestACMEManager(t)
	for _, domain := range []string{"../bad.example", "example.com/../../etc", "localhost", "1.2.3.4", "UPPER.example.com"} {
		if _, err := m.ensureCertificate(t.Context(), domain); !errors.Is(err, errUnsupportedManagedConfig) {
			t.Fatalf("unsafe domain %q error = %v", domain, err)
		}
	}
	cert, key := testACMECertificate(t, "jp.example.com", time.Now().Add(90*24*time.Hour))
	var mu sync.Mutex
	issues := 0
	m.runCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--issue") {
			mu.Lock()
			issues++
			mu.Unlock()
		}
		if strings.Contains(joined, "--install-cert") {
			for i, arg := range args {
				if arg == "--fullchain-file" {
					_ = os.WriteFile(args[i+1], cert, 0o644)
				}
				if arg == "--key-file" {
					_ = os.WriteFile(args[i+1], key, 0o600)
				}
			}
		}
		return nil, nil
	}
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := m.ensureCertificate(t.Context(), "jp.example.com"); err != nil {
				t.Errorf("ensure shared certificate: %v", err)
			}
		}()
	}
	wait.Wait()
	if issues != 1 {
		t.Fatalf("concurrent issuance count = %d", issues)
	}
}

func TestACMEToolDownloadRequiresPinnedChecksumAndOwnership(t *testing.T) {
	m := newACMEManager()
	root := t.TempDir()
	m.installDir = filepath.Join(root, "acme")
	script := []byte("official pinned script\n")
	sum := sha256.Sum256(script)
	m.scriptSHA256 = hex.EncodeToString(sum[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(script)
	}))
	defer server.Close()
	m.scriptURL, m.client = server.URL, server.Client()
	if err := m.ensureTool(t.Context()); err != nil {
		t.Fatal(err)
	}
	if stored, err := os.ReadFile(filepath.Join(m.installDir, "acme.sh")); err != nil || string(stored) != string(script) {
		t.Fatalf("verified script = %q, %v", stored, err)
	}
	if err := os.Remove(filepath.Join(m.installDir, ".managed-by-vps-panel")); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureTool(t.Context()); !errors.Is(err, errManagedACME) {
		t.Fatalf("unmanaged ACME directory error = %v", err)
	}
	if err := os.RemoveAll(m.installDir); err != nil {
		t.Fatal(err)
	}
	m.scriptSHA256 = strings.Repeat("0", 64)
	if err := m.ensureTool(t.Context()); !errors.Is(err, errManagedACME) {
		t.Fatalf("invalid checksum error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.installDir, "acme.sh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unverified script was installed: %v", err)
	}
}

func TestACMERefusesUnmanagedCertificateDirectory(t *testing.T) {
	m := newTestACMEManager(t)
	paths := managedACMECertificatePaths(m.certDir, "jp.example.com")
	if err := os.MkdirAll(filepath.Dir(paths.Fullchain), 0o700); err != nil {
		t.Fatal(err)
	}
	userCertificate := []byte("user-owned certificate")
	if err := os.WriteFile(paths.Fullchain, userCertificate, 0o644); err != nil {
		t.Fatal(err)
	}
	m.runCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unmanaged certificate directory triggered acme.sh")
		return nil, nil
	}
	if _, err := m.ensureCertificate(t.Context(), "jp.example.com"); !errors.Is(err, errManagedACME) {
		t.Fatalf("unmanaged certificate directory error = %v", err)
	}
	if contents, err := os.ReadFile(paths.Fullchain); err != nil || string(contents) != string(userCertificate) {
		t.Fatalf("user-owned certificate changed: %q, %v", contents, err)
	}
}

func TestACMEPurgeRemovesManagedStateAndPreservesUnmanagedCertificate(t *testing.T) {
	m := newTestACMEManager(t)
	managed := managedACMECertificatePaths(m.certDir, "managed.example.com")
	writeTestACMECertificate(t, managed, "managed.example.com", time.Now().Add(60*24*time.Hour))
	unmanagedDir := filepath.Join(m.certDir, "user.example.com")
	writeTestFile(t, filepath.Join(unmanagedDir, "fullchain.pem"), []byte("user certificate"), 0o644)
	writeTestFile(t, filepath.Join(m.homeDir, "account.conf"), []byte("state"), 0o600)
	firewallPurged := false
	m.reconcileFirewall = func(_ context.Context, rules []firewallRule) error {
		firewallPurged = len(rules) == 0
		return nil
	}
	if err := m.purge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !firewallPurged {
		t.Fatal("ACME purge did not clear managed firewall")
	}
	for _, path := range []string{m.installDir, m.homeDir, filepath.Dir(managed.Fullchain)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("managed ACME path remained: %s (%v)", path, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(unmanagedDir, "fullchain.pem")); err != nil || string(got) != "user certificate" {
		t.Fatalf("unmanaged certificate changed: %q, %v", got, err)
	}
}
