package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
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

// fakeChinaNFT models atomic nft -f transactions and retains state across Agent restarts.
type fakeChinaNFT struct {
	t           *testing.T
	exists      bool
	chain       string
	sets        map[string]string
	elements    map[string][]string
	batches     []string
	fail        string
	maxElements int
}

func newFakeChinaNFT(t *testing.T) *fakeChinaNFT {
	return &fakeChinaNFT{t: t, sets: map[string]string{}, elements: map[string][]string{}}
}
func (n *fakeChinaNFT) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	switch {
	case reflect.DeepEqual(args, []string{"list", "tables"}):
		if n.exists {
			return []byte("table inet vps_panel_cn_block\n"), nil
		}
		return nil, nil
	case len(args) == 5 && args[0] == "list" && args[1] == "chain":
		return []byte(n.chain), nil
	case len(args) == 5 && args[0] == "-t" && args[1] == "list" && args[2] == "sets":
		var out strings.Builder
		for name := range n.sets {
			fmt.Fprintf(&out, "set %s {\n}\n", name)
		}
		return []byte(out.String()), nil
	case len(args) == 6 && args[0] == "-t" && args[1] == "list" && args[2] == "set":
		return []byte(n.sets[args[5]]), nil
	case len(args) == 2 && args[0] == "-f":
		data, err := os.ReadFile(args[1])
		if err != nil {
			n.t.Fatal(err)
		}
		batch := string(data)
		n.batches = append(n.batches, batch)
		if n.fail != "" && strings.Contains(batch, n.fail) {
			n.fail = ""
			return []byte("synthetic failure"), errors.New("exit status")
		}
		if strings.HasPrefix(batch, "create table") {
			n.exists = true
			n.chain = `counter comment "` + chinaFirewallOwnerMarker + `"`
			return nil, nil
		}
		if strings.HasPrefix(batch, "delete table") {
			n.exists = false
			n.chain = ""
			n.sets = map[string]string{}
			return nil, nil
		}
		if strings.HasPrefix(batch, "create set") {
			fields := strings.Fields(batch)
			name := fields[4]
			if _, ok := n.sets[name]; ok {
				return nil, errors.New("set exists")
			}
			n.sets[name] = batch
			return nil, nil
		}
		if strings.HasPrefix(batch, "add element") {
			fields := strings.Fields(batch)
			name := fields[4]
			vals := strings.Split(strings.TrimSpace(strings.Split(strings.Split(batch, "{")[1], "}")[0]), ", ")
			if n.maxElements > 0 && len(vals) > n.maxElements {
				return []byte("netlink: Message too large"), errors.New("exit status")
			}
			n.elements[name] = append(n.elements[name], vals...)
			return nil, nil
		}
		if strings.HasPrefix(batch, "flush chain") {
			n.chain = batch
			return nil, nil
		}
		if strings.HasPrefix(batch, "delete set") {
			fields := strings.Fields(batch)
			name := fields[4]
			if strings.Contains(n.chain, "@"+name) {
				n.t.Fatal("attempted to delete active set")
			}
			delete(n.sets, name)
			delete(n.elements, name)
			return nil, nil
		}
		n.t.Fatalf("unexpected nft batch %q", batch)
	}
	n.t.Fatalf("unexpected nft arguments %v", args)
	return nil, nil
}

func TestChinaInboundFirewallAtomicUpdatesAndRecovery(t *testing.T) {
	manager, now := newTestChinaInboundFirewall(t)
	nft := newFakeChinaNFT(t)
	manager.runCommand = nft.run
	if err := manager.writeCache(chinaPrefixes{FetchedAt: now, IPv4: []string{"1.0.1.0/24"}, IPv6: []string{"2400:3200::/32"}}); err != nil {
		t.Fatal(err)
	}
	state := desiredState{BlockChinaInbound: true, Xray: desiredXrayState{Enabled: true, Proxies: []desiredProxy{{Port: 443, Protocol: "vless"}, {Port: 8388, Protocol: "shadowsocks"}}}, Realm: desiredRealmState{Enabled: true, Relays: []desiredRelay{{ListenPort: 9000, Network: "tcp"}, {ListenPort: 9001, Network: "udp"}, {ListenPort: 9002, Network: "tcp,udp"}}}}
	if err := manager.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nft.chain, "tcp dport") || !strings.Contains(nft.chain, "ip6 saddr") || strings.Count(nft.chain, " drop") != 4 {
		t.Fatalf("activation: %s", nft.chain)
	}
	var ports [][]string
	for name, values := range nft.elements {
		if strings.HasPrefix(name, "tp_") || strings.HasPrefix(name, "up_") {
			ports = append(ports, values)
		}
	}
	sort.Slice(ports, func(i, j int) bool { return len(ports[i]) > len(ports[j]) })
	if !reflect.DeepEqual(ports, [][]string{{"443", "8388", "9000", "9002"}, {"8388", "9001", "9002"}}) {
		t.Fatalf("ports: %v", ports)
	}
	old := nft.chain
	for _, fail := range []string{"add element", "flush chain"} {
		nft.fail = fail
		if err := manager.apply(t.Context(), state); err == nil {
			t.Fatalf("%s succeeded", fail)
		}
		if nft.chain != old {
			t.Fatalf("%s changed active chain", fail)
		}
	}
	// Simulate a crash after creating and partially filling an inactive set.
	nft.sets["c4_0123456789abcdef"] = "type ipv4_addr; flags interval"
	nft.elements["c4_0123456789abcdef"] = []string{"9.0.0.0/8"}
	// A new manager sees abandoned sets and removes them without touching the old generation.
	manager2, _ := newTestChinaInboundFirewall(t)
	manager2.cachePath = manager.cachePath
	manager2.runCommand = nft.run
	if err := manager2.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if nft.chain == old || len(nft.sets) != 4 {
		t.Fatalf("recovery did not replace and clean sets: %d", len(nft.sets))
	}
	if err := manager2.apply(t.Context(), state); err != nil || len(nft.sets) != 4 {
		t.Fatalf("repeat apply: %v, sets %d", err, len(nft.sets))
	}
	if err := manager2.writeCache(chinaPrefixes{FetchedAt: now, IPv4: []string{"2.0.0.0/8"}, IPv6: []string{"2401::/32"}}); err != nil {
		t.Fatal(err)
	}
	state.Xray.Proxies = []desiredProxy{{Port: 12345, Protocol: "vless"}}
	state.Realm.Enabled = false
	if err := manager2.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	for name, values := range nft.elements {
		if strings.HasPrefix(name, "c4_") && !reflect.DeepEqual(values, []string{"2.0.0.0/8"}) {
			t.Fatalf("IPv4 update: %v", values)
		}
		if strings.HasPrefix(name, "c6_") && !reflect.DeepEqual(values, []string{"2401::/32"}) {
			t.Fatalf("IPv6 update: %v", values)
		}
		if strings.HasPrefix(name, "tp_") && !reflect.DeepEqual(values, []string{"12345"}) {
			t.Fatalf("port update: %v", values)
		}
	}
	if err := manager2.purge(t.Context()); err != nil || nft.exists {
		t.Fatalf("purge: %v", err)
	}
}

func TestChinaInboundFirewallLegacyMigration(t *testing.T) {
	manager, now := newTestChinaInboundFirewall(t)
	nft := newFakeChinaNFT(t)
	manager.runCommand = nft.run
	if err := manager.writeCache(chinaPrefixes{FetchedAt: now, IPv4: []string{"1.0.1.0/24"}, IPv6: []string{"2400:3200::/32"}}); err != nil {
		t.Fatal(err)
	}
	nft.exists = true
	nft.chain = `counter comment "` + chinaFirewallOwnerMarker + `"` + "\n @cn_ipv4 @cn_ipv6 @tcp_ports @udp_ports"
	for _, name := range []string{"cn_ipv4", "cn_ipv6", "tcp_ports", "udp_ports"} {
		nft.sets[name] = "legacy"
	}
	nft.fail = "flush chain"
	old := nft.chain
	if err := manager.apply(t.Context(), desiredState{BlockChinaInbound: true}); err == nil || nft.chain != old {
		t.Fatalf("legacy activation failure: %v", err)
	}
	if err := manager.apply(t.Context(), desiredState{BlockChinaInbound: true}); err != nil {
		t.Fatal(err)
	}
	if len(nft.sets) != 4 {
		t.Fatalf("legacy sets not cleaned: %v", nft.sets)
	}
}

func TestOptimizeChinaPrefixes(t *testing.T) {
	for _, tc := range []struct {
		values []string
		ipv4   bool
		want   []string
	}{
		{[]string{"10.0.0.1/24", "10.0.0.0/24", "10.0.1.0/24", "10.0.0.0/25", "10.0.3.0/24"}, true, []string{"10.0.0.0/23", "10.0.3.0/24"}},
		{[]string{"2001:db8::/33", "2001:db8:8000::/33", "2001:db8::1/128"}, false, []string{"2001:db8::/32"}},
	} {
		got, err := optimizeChinaPrefixes(tc.values, tc.ipv4)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("optimize %v: %v %v", tc.values, got, err)
		}
	}
	for _, value := range []string{"bad", "::ffff:1.2.3.4/120", "2001:db8::/32"} {
		if _, err := optimizeChinaPrefixes([]string{value}, true); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestChinaNFTLargeBatchesAndTooLargeRetry(t *testing.T) {
	manager, _ := newTestChinaInboundFirewall(t)
	nft := newFakeChinaNFT(t)
	nft.maxElements = 16
	manager.runCommand = nft.run
	values := make([]string, 4096)
	for i := range values {
		values[i] = fmt.Sprintf("10.%d.%d.0/24", i/128, (i%128)*2)
	}
	optimized, err := optimizeChinaPrefixes(values, true)
	if err != nil || len(optimized) != len(values) {
		t.Fatalf("large optimization: %d %v", len(optimized), err)
	}
	if err := manager.loadNFTElements(t.Context(), "nft", "c4_0123456789abcdef", optimized); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(nft.elements["c4_0123456789abcdef"], values) {
		t.Fatal("batch loading changed prefixes")
	}
	for _, batch := range nft.batches {
		if len(batch) > chinaNFTBatchBytes || strings.Count(batch, ",")+1 > chinaNFTBatchElements {
			t.Fatalf("oversized batch: %d", len(batch))
		}
	}
	nft.maxElements = 0
	nft.fail = "add element"
	if err := manager.loadNFTElements(t.Context(), "nft", "test", []string{"1.1.1.0/24"}); err == nil {
		t.Fatal("non-size error retried")
	}
	if !errors.Is(chinaNFTCommandError("apply nftables batch", []byte("Netlink: Message too large"), errors.New("failed")), errChinaNFTTooLarge) {
		t.Fatal("message too large not classified")
	}
}

func TestChinaSyntheticAPNICLargeIPv4AndIPv6(t *testing.T) {
	var input strings.Builder
	for i := 0; i < 2048; i++ {
		fmt.Fprintf(&input, "apnic|CN|ipv4|10.%d.%d.0|256|20260924|allocated\n", i/128, (i%128)*2)
		fmt.Fprintf(&input, "apnic|CN|ipv6|2001:db8:%x::|48|20260924|assigned\n", i*2)
	}
	ipv4, ipv6, err := parseAPNICChinaPrefixes([]byte(input.String()))
	if err != nil || len(ipv4) != 2048 || len(ipv6) != 2048 {
		t.Fatalf("parse: %d %d %v", len(ipv4), len(ipv6), err)
	}
	manager, _ := newTestChinaInboundFirewall(t)
	nft := newFakeChinaNFT(t)
	manager.runCommand = nft.run
	for _, test := range []struct {
		name   string
		values []string
		ipv4   bool
	}{{"v4", ipv4, true}, {"v6", ipv6, false}} {
		optimized, err := optimizeChinaPrefixes(test.values, test.ipv4)
		if err != nil || len(optimized) != 2048 {
			t.Fatalf("%s optimize %d: %v", test.name, len(optimized), err)
		}
		if err := manager.loadNFTElements(t.Context(), "nft", test.name, optimized); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(optimized, nft.elements[test.name]) {
			t.Fatalf("%s batches changed data", test.name)
		}
	}
}

func TestChinaInboundInitialFailureAndCleanupFailure(t *testing.T) {
	manager, now := newTestChinaInboundFirewall(t)
	nft := newFakeChinaNFT(t)
	manager.runCommand = nft.run
	if err := manager.writeCache(chinaPrefixes{FetchedAt: now, IPv4: []string{"1.0.1.0/24"}, IPv6: []string{"2400:3200::/32"}}); err != nil {
		t.Fatal(err)
	}
	state := desiredState{BlockChinaInbound: true}
	nft.fail = "add element"
	if err := manager.apply(t.Context(), state); err == nil || strings.Contains(nft.chain, " drop") || len(nft.sets) != 0 {
		t.Fatalf("initial failure: %v, chain %s, sets %v", err, nft.chain, nft.sets)
	}
	if err := manager.apply(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	nft.fail = "delete set"
	err := manager.apply(t.Context(), state)
	if !errors.Is(err, errChinaNFTCleanup) || !strings.Contains(nft.chain, " drop") {
		t.Fatalf("cleanup failure: %v; chain %s", err, nft.chain)
	}
	if err := manager.apply(t.Context(), state); err != nil || len(nft.sets) != 4 {
		t.Fatalf("cleanup recovery: %v, %d sets", err, len(nft.sets))
	}
}

func TestOptimizeChinaPrefixesPreservesCoverage(t *testing.T) {
	inputs := []string{"192.0.2.0/27", "192.0.2.0/28", "192.0.2.32/27", "192.0.2.96/27", "192.0.2.128/26", "192.0.2.192/27", "192.0.2.224/28"}
	optimized, err := optimizeChinaPrefixes(inputs, true)
	if err != nil {
		t.Fatal(err)
	}
	contains := func(list []string, address netip.Addr) bool {
		for _, raw := range list {
			if netip.MustParsePrefix(raw).Contains(address) {
				return true
			}
		}
		return false
	}
	for i := 0; i < 256; i++ {
		addr := netip.AddrFrom4([4]byte{192, 0, 2, byte(i)})
		if contains(inputs, addr) != contains(optimized, addr) {
			t.Fatalf("coverage changed at %s", addr)
		}
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
	if err := manager.apply(t.Context(), desiredState{BlockChinaInbound: true}); !errors.Is(err, errManagedChinaInboundRequiresNFT) || !strings.Contains(err.Error(), "apk add --no-cache nftables") {
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

func TestChinaFirewallReportsNFTFailuresWithoutLeakingOutput(t *testing.T) {
	for _, tc := range []struct{ name, output, expected string }{
		{"permission", "Operation not permitted synthetic-secret", "insufficient firewall privileges"},
		{"kernel", "Protocol not supported synthetic-secret", "kernel or container support"},
		{"other", "syntax error synthetic-secret", "nftables inspection failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, _ := newTestChinaInboundFirewall(t)
			manager.runCommand = func(context.Context, string, ...string) ([]byte, error) {
				return []byte(tc.output), errors.New("exit status 1")
			}
			err := manager.apply(t.Context(), desiredState{BlockChinaInbound: true})
			if !errors.Is(err, errManagedChinaInboundFirewall) || !strings.Contains(err.Error(), tc.expected) || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("diagnostic: %v", err)
			}
		})
	}
	manager, now := newTestChinaInboundFirewall(t)
	if err := manager.writeCache(chinaPrefixes{FetchedAt: now, IPv4: []string{"1.0.1.0/24"}, IPv6: []string{"2400:3200::/32"}}); err != nil {
		t.Fatal(err)
	}
	manager.runCommand = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "list" {
			return nil, nil
		}
		return []byte("syntax error synthetic-secret"), errors.New("exit status 1")
	}
	err := manager.apply(t.Context(), desiredState{BlockChinaInbound: true})
	if !errors.Is(err, errManagedChinaInboundFirewall) || !strings.Contains(err.Error(), "apply nftables batch") || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("apply diagnostic: %v", err)
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
