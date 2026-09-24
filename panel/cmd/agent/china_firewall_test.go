package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testAPNICChinaData = `# delegated APNIC fixture
apnic|CN|ipv4|1.0.1.0|256|20260924|allocated
apnic|CN|ipv4|1.0.2.0|768|20260924|assigned
apnic|CN|ipv4|1.0.1.0|256|20260924|allocated
apnic|CN|ipv6|2400:3200::|32|20260924|assigned
apnic|HK|ipv4|2.0.0.0|256|20260924|allocated
apnic|MO|ipv6|2401:1000::|32|20260924|allocated
apnic|TW|ipv4|3.0.0.0|256|20260924|allocated
apnic|CN|ipv4|4.0.0.0|256|20260924|available
apnic|CN|ipv4|4.0.1.0|256|20260924|reserved
apnic|CN|ipv6|not-an-address|32|20260924|allocated
apnic|CN|ipv4|5.0.0.0|not-a-count|20260924|assigned
apnic|*|ipv4|*|1234|summary
`

func TestParseAPNICChinaPrefixesFiltersAndConvertsRanges(t *testing.T) {
	ipv4, ipv6, err := parseAPNICChinaPrefixes([]byte(testAPNICChinaData))
	if err != nil {
		t.Fatal(err)
	}
	wantIPv4 := []string{"1.0.1.0/24", "1.0.2.0/23", "1.0.4.0/24"}
	wantIPv6 := []string{"2400:3200::/32"}
	if !reflect.DeepEqual(ipv4, wantIPv4) || !reflect.DeepEqual(ipv6, wantIPv6) {
		t.Fatalf("parsed prefixes = (%v, %v), want (%v, %v)", ipv4, ipv6, wantIPv4, wantIPv6)
	}
}

func TestChinaPrefixesHTTPClientUsesIPv4ProxyAndHTTPSRedirects(t *testing.T) {
	dialErr := errors.New("dial stopped")
	var network, address string
	client := newChinaPrefixesHTTPClient(func(_ context.Context, value, destination string) (net.Conn, error) {
		network, address = value, destination
		return nil, dialErr
	})
	if client.Timeout != 30*time.Second {
		t.Fatalf("APNIC client timeout = %v, want 30s", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("APNIC client transport = %T, want *http.Transport", client.Transport)
	}
	if reflect.ValueOf(transport.Proxy).Pointer() != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Fatal("APNIC client does not preserve ProxyFromEnvironment")
	}
	if _, err := transport.DialContext(t.Context(), "tcp", "ftp.apnic.net:443"); !errors.Is(err, dialErr) {
		t.Fatalf("APNIC dial error = %v, want %v", err, dialErr)
	}
	if network != "tcp4" || address != "ftp.apnic.net:443" {
		t.Fatalf("APNIC dial = (%q, %q), want (tcp4, ftp.apnic.net:443)", network, address)
	}

	for _, test := range []struct {
		scheme  string
		wantErr bool
	}{
		{scheme: "https"},
		{scheme: "http", wantErr: true},
	} {
		request, err := http.NewRequest(http.MethodGet, test.scheme+"://ftp.apnic.net/delegated", nil)
		if err != nil {
			t.Fatal(err)
		}
		err = client.CheckRedirect(request, nil)
		if (err != nil) != test.wantErr {
			t.Fatalf("%s redirect error = %v, want error %t", test.scheme, err, test.wantErr)
		}
	}
}

func TestChinaInboundFirewallUsesOwnedNFTSetsAndManagedPorts(t *testing.T) {
	manager, now := newTestChinaInboundFirewall(t)
	manager.download = func(context.Context) ([]byte, error) {
		t.Fatal("fresh cache triggered a download")
		return nil, nil
	}
	if err := manager.writeCache(chinaPrefixes{
		FetchedAt: now.Add(-time.Hour), IPv4: []string{"1.0.1.0/24"}, IPv6: []string{"2400:3200::/32"},
	}); err != nil {
		t.Fatal(err)
	}
	var batches []string
	tableExists := false
	manager.runCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if reflect.DeepEqual(arguments, []string{"list", "tables"}) {
			if !tableExists {
				return nil, nil
			}
			return []byte("table inet vps_panel_cn_block\n"), nil
		}
		if len(arguments) >= 2 && arguments[0] == "list" && arguments[1] == "chain" {
			return []byte(`counter comment "vps-panel-cn-block-owner"`), nil
		}
		if len(arguments) == 2 && arguments[0] == "-f" {
			data, err := os.ReadFile(arguments[1])
			if err != nil {
				t.Fatal(err)
			}
			batches = append(batches, string(data))
			tableExists = true
			return nil, nil
		}
		t.Fatalf("unexpected nft command: %v", arguments)
		return nil, nil
	}
	state := desiredState{
		BlockChinaInbound: true,
		Xray: desiredXrayState{Enabled: true, Proxies: []desiredProxy{
			{Port: 443, Protocol: "vless"},
			{Port: 8388, Protocol: "shadowsocks"},
		}},
		Realm: desiredRealmState{Enabled: true, Relays: []desiredRelay{
			{ListenPort: 9000, Network: "tcp"},
			{ListenPort: 9001, Network: "udp"},
			{ListenPort: 9002, Network: "tcp,udp"},
		}},
	}
	if err := manager.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if err := manager.apply(t.Context(), state); err != nil {
		t.Fatalf("idempotent apply: %v", err)
	}
	if len(batches) != 2 {
		t.Fatalf("batch count = %d, want 2", len(batches))
	}
	first := batches[0]
	for _, text := range []string{
		"table inet vps_panel_cn_block", "set cn_ipv4", "type ipv4_addr", "flags interval",
		"set cn_ipv6", "type ipv6_addr", "set tcp_ports", "elements = { 443, 8388, 9000, 9002 }",
		"set udp_ports", "elements = { 8388, 9001, 9002 }", "priority -20", chinaFirewallOwnerMarker,
		"ip saddr @cn_ipv4 tcp dport @tcp_ports drop", "ip6 saddr @cn_ipv6 udp dport @udp_ports drop",
	} {
		if !strings.Contains(first, text) {
			t.Fatalf("nft batch missing %q:\n%s", text, first)
		}
	}
	if strings.Contains(first, "55222") || strings.Contains(first, "flush ruleset") ||
		strings.Contains(first, "table inet filter") || strings.Count(first, "tcp dport") != 2 || strings.Count(first, "udp dport") != 2 {
		t.Fatalf("nft batch crossed its managed boundary:\n%s", first)
	}
	if !strings.HasPrefix(batches[1], "delete table inet vps_panel_cn_block\n") {
		t.Fatalf("existing managed table was not atomically replaced:\n%s", batches[1])
	}
}

func TestChinaInboundFirewallOwnershipDisableAndMissingNFT(t *testing.T) {
	manager, now := newTestChinaInboundFirewall(t)
	if err := manager.writeCache(chinaPrefixes{
		FetchedAt: now, IPv4: []string{"1.0.1.0/24"}, IPv6: []string{"2400:3200::/32"},
	}); err != nil {
		t.Fatal(err)
	}
	manager.runCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if reflect.DeepEqual(arguments, []string{"list", "tables"}) {
			return []byte("table inet vps_panel_cn_block\n"), nil
		}
		return []byte("chain input {}"), nil
	}
	if err := manager.apply(t.Context(), desiredState{BlockChinaInbound: true}); !errors.Is(err, errManagedChinaInboundFirewall) {
		t.Fatalf("unmanaged table apply error = %v", err)
	}
	if err := manager.disable(t.Context()); !errors.Is(err, errManagedChinaInboundFirewall) {
		t.Fatalf("unmanaged table disable error = %v", err)
	}

	manager.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if err := manager.apply(t.Context(), desiredState{BlockChinaInbound: true}); !errors.Is(err, errManagedChinaInboundRequiresNFT) || err.Error() != "managed China inbound firewall requires nftables" {
		t.Fatalf("enabled missing nft error = %v", err)
	}
	if err := manager.disable(t.Context()); err != nil {
		t.Fatalf("disabled missing nft error = %v", err)
	}

	manager.lookPath = func(string) (string, error) { return "nft", nil }
	manager.runCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if reflect.DeepEqual(arguments, []string{"list", "tables"}) {
			return nil, nil
		}
		t.Fatalf("absent table triggered command: %v", arguments)
		return nil, nil
	}
	if err := manager.disable(t.Context()); err != nil {
		t.Fatalf("disabled absent table error = %v", err)
	}

	manager.runCommand = func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
		if reflect.DeepEqual(arguments, []string{"list", "tables"}) {
			return []byte("table inet vps_panel_cn_block\n"), nil
		}
		if len(arguments) >= 2 && arguments[0] == "list" && arguments[1] == "chain" {
			return []byte(`counter comment "vps-panel-cn-block-owner"`), nil
		}
		if arguments[0] == "-f" {
			data, err := os.ReadFile(arguments[1])
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "delete table inet vps_panel_cn_block\n" {
				t.Fatalf("disable batch = %q", data)
			}
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	}
	if err := manager.disable(t.Context()); err != nil {
		t.Fatalf("disable managed table: %v", err)
	}
}

func TestChinaPrefixCacheFreshStaleFallbackAndInvalid(t *testing.T) {
	manager, now := newTestChinaInboundFirewall(t)
	old := chinaPrefixes{
		FetchedAt: now.Add(-time.Hour), IPv4: []string{"1.0.1.0/24"}, IPv6: []string{"2400:3200::/32"},
	}
	if err := manager.writeCache(old); err != nil {
		t.Fatal(err)
	}
	downloads := 0
	manager.download = func(context.Context) ([]byte, error) {
		downloads++
		return []byte(testAPNICChinaData), nil
	}
	loaded, refreshed, err := manager.loadPrefixes(t.Context())
	if err != nil || refreshed || downloads != 0 || !reflect.DeepEqual(loaded, old) {
		t.Fatalf("fresh cache = (%+v, %t, downloads %d, %v)", loaded, refreshed, downloads, err)
	}

	old.FetchedAt = now.Add(-25 * time.Hour)
	if err := manager.writeCache(old); err != nil {
		t.Fatal(err)
	}
	loaded, refreshed, err = manager.loadPrefixes(t.Context())
	if err != nil || !refreshed || downloads != 1 || !loaded.FetchedAt.Equal(now) || len(loaded.IPv4) != 3 {
		t.Fatalf("stale cache refresh = (%+v, %t, downloads %d, %v)", loaded, refreshed, downloads, err)
	}
	info, err := os.Stat(manager.cachePath)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("cache mode = %v, error = %v", info.Mode().Perm(), err)
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(manager.cachePath), ".cn-prefixes-*.json"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("cache temporary files = %v, error = %v", temporary, err)
	}

	stale := loaded
	stale.FetchedAt = now.Add(-25 * time.Hour)
	if err := manager.writeCache(stale); err != nil {
		t.Fatal(err)
	}
	manager.download = func(context.Context) ([]byte, error) { return nil, errors.New("network unavailable") }
	loaded, refreshed, err = manager.loadPrefixes(t.Context())
	if err != nil || refreshed || !loaded.FetchedAt.Equal(stale.FetchedAt) {
		t.Fatalf("stale fallback = (%+v, %t, %v)", loaded, refreshed, err)
	}
	manager.runCommand = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("failed refresh changed nftables rules")
		return nil, nil
	}
	if err := manager.refresh(t.Context(), desiredState{BlockChinaInbound: true}); err != nil {
		t.Fatalf("stale refresh fallback = %v", err)
	}

	if err := os.Remove(manager.cachePath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.loadPrefixes(t.Context()); err == nil {
		t.Fatal("missing cache and failed download succeeded")
	}
	if err := os.WriteFile(manager.cachePath, []byte(`{"fetched_at":"bad","ipv4":[],"ipv6":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.loadPrefixes(t.Context()); err == nil {
		t.Fatal("invalid cache and failed download succeeded")
	}
}

func newTestChinaInboundFirewall(t *testing.T) (*chinaInboundFirewall, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 24, 1, 2, 3, 0, time.UTC)
	manager := newChinaInboundFirewall()
	manager.cachePath = filepath.Join(t.TempDir(), "firewall", "cn-prefixes.json")
	manager.now = func() time.Time { return now }
	manager.lookPath = func(name string) (string, error) {
		if name != "nft" {
			t.Fatalf("unexpected binary lookup: %s", name)
		}
		return "nft", nil
	}
	return manager, now
}
