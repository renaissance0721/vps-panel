package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderManagedRealmConfigSupportsNetworksAndStableOrder(t *testing.T) {
	relays := []desiredRelay{
		{ID: 3, ListenAddress: "::", ListenPort: 9503, TargetHost: "2001:db8::1", TargetPort: 443, Network: "tcp,udp"},
		{ID: 1, ListenAddress: "0.0.0.0", ListenPort: 9501, TargetHost: "example.com", TargetPort: 80, Network: "tcp"},
		{ID: 2, ListenAddress: "127.0.0.1", ListenPort: 9502, TargetHost: "192.0.2.10", TargetPort: 53, Network: "udp"},
	}
	first, err := renderManagedRealmConfig(relays)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderManagedRealmConfig([]desiredRelay{relays[1], relays[2], relays[0]})
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("Realm config is not stable: %v\n%s\n%s", err, first, second)
	}
	text := string(first)
	if strings.Index(text, "relay_id = 1") > strings.Index(text, "relay_id = 2") ||
		strings.Index(text, "relay_id = 2") > strings.Index(text, "relay_id = 3") {
		t.Fatalf("Realm relays are not sorted: %s", text)
	}
	for _, expected := range []string{
		`listen = "0.0.0.0:9501"`, `remote = "example.com:80"`,
		`listen = "127.0.0.1:9502"`, `remote = "192.0.2.10:53"`,
		`listen = "[::]:9503"`, `remote = "[2001:db8::1]:443"`,
		"no_tcp = false\nuse_udp = false", "no_tcp = true\nuse_udp = true",
		"no_tcp = false\nuse_udp = true",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Realm config missing %q:\n%s", expected, text)
		}
	}
}

func TestRenderManagedRealmConfigRejectsInjection(t *testing.T) {
	for _, relay := range []desiredRelay{
		{ID: 1, ListenAddress: "0.0.0.0", ListenPort: 9501, TargetHost: "bad.example\n[[endpoints]]", TargetPort: 443, Network: "tcp"},
		{ID: 1, ListenAddress: "0.0.0.0", ListenPort: 9501, TargetHost: "example.com", TargetPort: 443, Network: "other"},
	} {
		if _, err := renderManagedRealmConfig([]desiredRelay{relay}); err == nil {
			t.Fatalf("unsafe Relay rendered: %+v", relay)
		}
	}
}

func TestRenderedRealmStateRestoresProtocolAwareRules(t *testing.T) {
	config, err := renderManagedRealmConfig([]desiredRelay{
		{ID: 1, ListenAddress: "0.0.0.0", ListenPort: 9501, TargetHost: "example.com", TargetPort: 443, Network: "tcp"},
		{ID: 2, ListenAddress: "::", ListenPort: 9502, TargetHost: "192.0.2.1", TargetPort: 53, Network: "tcp,udp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	listeners, rules, ok := parseRenderedRealmState(config)
	if !ok || len(listeners) != 2 || len(rules) != 3 || !listeners[1].tcp || !listeners[1].udp {
		t.Fatalf("parsed Realm state = %+v, %+v, %v", listeners, rules, ok)
	}
}

func TestRenderedRealmConfigWithOfficialBinary(t *testing.T) {
	binaryPath := os.Getenv("VPS_PANEL_REALM_TEST_BINARY")
	if binaryPath == "" {
		t.Skip("VPS_PANEL_REALM_TEST_BINARY is not set")
	}
	config, err := renderManagedRealmValidationConfig([]desiredRelay{
		{ID: 1, ListenAddress: "0.0.0.0", ListenPort: 9502, TargetHost: "example.com", TargetPort: 443, Network: "tcp"},
		{ID: 2, ListenAddress: "::", ListenPort: 9503, TargetHost: "2001:db8::1", TargetPort: 53, Network: "udp"},
		{ID: 3, ListenAddress: "127.0.0.1", ListenPort: 9504, TargetHost: "203.0.113.10", TargetPort: 8443, Network: "tcp,udp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := validateRealmConfig(ctx, binaryPath, configPath); err != nil {
		t.Fatalf("official Realm rejected rendered config: %v\n%s", err, config)
	}
}
