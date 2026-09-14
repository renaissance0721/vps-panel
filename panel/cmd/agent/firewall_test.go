package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestProxyFirewallIPTablesReconcileIsOwnedAndIdempotent(t *testing.T) {
	managed := map[int]struct{}{80: {}, 443: {}}
	mutations := make([][]string, 0)
	firewall := &proxyFirewall{
		lookPath: func(name string) (string, error) {
			if name == "iptables" {
				return "/sbin/iptables", nil
			}
			return "", errors.New("not installed")
		},
		runCommand: func(_ context.Context, name string, arguments ...string) ([]byte, error) {
			if name != "/sbin/iptables" {
				return nil, fmt.Errorf("unexpected command %s", name)
			}
			if len(arguments) == 2 && arguments[0] == "-S" {
				lines := []string{"-P INPUT DROP", "-A INPUT -p tcp --dport 22 -j ACCEPT"}
				ports := make([]int, 0, len(managed))
				for port := range managed {
					ports = append(ports, port)
				}
				sort.Ints(ports)
				for _, port := range ports {
					lines = append(lines, "-A INPUT "+strings.Join(managedIPTablesRule(firewallRule{port: port, protocol: "tcp"}), " "))
				}
				return []byte(strings.Join(lines, "\n")), nil
			}
			mutations = append(mutations, append([]string(nil), arguments...))
			portValue, ok := fieldAfter(arguments, "--dport")
			if !ok || !containsFields(arguments, "--comment", managedProxyFirewallTCPComment) {
				return nil, errors.New("attempted to change an unowned rule")
			}
			port, _ := strconv.Atoi(portValue)
			switch arguments[0] {
			case "-I":
				managed[port] = struct{}{}
			case "-D":
				delete(managed, port)
			default:
				return nil, fmt.Errorf("unexpected iptables operation %q", arguments[0])
			}
			return nil, nil
		},
	}

	if err := firewall.reconcile(t.Context(), []int{443, 8443}); err != nil {
		t.Fatal(err)
	}
	if _, exists := managed[80]; exists {
		t.Fatal("stale managed rule was not removed")
	}
	if _, exists := managed[443]; !exists {
		t.Fatal("existing desired managed rule was removed")
	}
	if _, exists := managed[8443]; !exists {
		t.Fatal("missing desired managed rule was not added")
	}
	if len(mutations) != 2 || mutations[0][0] != "-I" || mutations[1][0] != "-D" {
		t.Fatalf("iptables mutations = %v", mutations)
	}
	if err := firewall.reconcile(t.Context(), []int{8443, 443, 443}); err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 2 {
		t.Fatalf("idempotent reconcile added mutations: %v", mutations)
	}
	if err := firewall.reconcile(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if len(managed) != 0 || len(mutations) != 4 || mutations[2][0] != "-D" || mutations[3][0] != "-D" {
		t.Fatalf("clear managed rules = %v, mutations = %v", managed, mutations)
	}
}

func TestProxyFirewallNoActiveFirewallDoesNothing(t *testing.T) {
	mutated := false
	firewall := &proxyFirewall{
		lookPath: func(name string) (string, error) {
			if name == "iptables" {
				return name, nil
			}
			return "", errors.New("not installed")
		},
		runCommand: func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			if len(arguments) == 2 && arguments[0] == "-S" {
				return []byte("-P INPUT ACCEPT\n"), nil
			}
			mutated = true
			return nil, nil
		},
	}
	if err := firewall.reconcile(t.Context(), []int{443}); err != nil {
		t.Fatal(err)
	}
	if mutated {
		t.Fatal("inactive firewall was modified")
	}
}

func TestNFTablesReconcileUsesOwnedTableCommentsAndProtocols(t *testing.T) {
	mutations := make([]string, 0)
	firewall := &proxyFirewall{
		owner: "realm",
		lookPath: func(name string) (string, error) {
			if name == "nft" {
				return "/sbin/nft", nil
			}
			return "", errors.New("not installed")
		},
		runCommand: func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			command := strings.Join(arguments, " ")
			switch command {
			case "list ruleset":
				return []byte("table inet filter {\n}\n"), nil
			case "-a list chain inet vps_panel_realm input":
				return []byte(`counter comment "vps-panel-realm-owner" # handle 1
tcp dport 9502 accept comment "vps-panel-realm-tcp" # handle 7
udp dport 9502 accept comment "vps-panel-realm-udp" # handle 8
tcp dport 443 accept comment "vps-panel-proxy-tcp" # handle 9
tcp dport 22 accept # handle 10`), nil
			default:
				mutations = append(mutations, command)
				return nil, nil
			}
		},
	}
	desired := []firewallRule{{port: 9600, protocol: "tcp"}, {port: 9600, protocol: "udp"}}
	if err := firewall.reconcileRules(t.Context(), desired); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"add rule inet vps_panel_realm input tcp dport 9600 accept comment vps-panel-realm-tcp",
		"add rule inet vps_panel_realm input udp dport 9600 accept comment vps-panel-realm-udp",
		"delete rule inet vps_panel_realm input handle 7",
		"delete rule inet vps_panel_realm input handle 8",
	}
	if strings.Join(mutations, "|") != strings.Join(want, "|") {
		t.Fatalf("nftables mutations = %v, want %v", mutations, want)
	}
}

func TestNFTablesCreatesOnlyItsDedicatedTableAndChain(t *testing.T) {
	mutations := make([]string, 0)
	firewall := &proxyFirewall{
		owner: "proxy",
		lookPath: func(name string) (string, error) {
			if name == "nft" {
				return "nft", nil
			}
			return "", errors.New("not installed")
		},
		runCommand: func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			command := strings.Join(arguments, " ")
			switch command {
			case "list ruleset":
				return []byte("table inet user_firewall {\n}\n"), nil
			case "-a list chain inet vps_panel_proxy input", "list table inet vps_panel_proxy":
				return nil, errors.New("not found")
			default:
				mutations = append(mutations, command)
				return nil, nil
			}
		},
	}
	if err := firewall.reconcileRules(t.Context(), []firewallRule{{port: 443, protocol: "tcp"}}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(mutations, "|")
	for _, expected := range []string{
		"add table inet vps_panel_proxy",
		"add chain inet vps_panel_proxy input { type filter hook input priority -10 ; policy accept ; }",
		"add rule inet vps_panel_proxy input counter comment vps-panel-proxy-owner",
		"add rule inet vps_panel_proxy input tcp dport 443 accept comment vps-panel-proxy-tcp",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("nftables setup missing %q: %v", expected, mutations)
		}
	}
	if strings.Contains(joined, "user_firewall") {
		t.Fatalf("nftables setup changed a user table: %v", mutations)
	}
}

func TestNFTablesRefusesUnmanagedDedicatedTable(t *testing.T) {
	mutated := false
	firewall := &proxyFirewall{
		owner: "proxy",
		lookPath: func(name string) (string, error) {
			if name == "nft" {
				return "nft", nil
			}
			return "", errors.New("not installed")
		},
		runCommand: func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			switch strings.Join(arguments, " ") {
			case "list ruleset":
				return []byte("table inet vps_panel_proxy {\n}\n"), nil
			case "-a list chain inet vps_panel_proxy input":
				return []byte(`tcp dport 22 accept comment "user-rule" # handle 9`), nil
			default:
				mutated = true
				return nil, nil
			}
		},
	}
	err := firewall.reconcileRules(t.Context(), []firewallRule{{port: 443, protocol: "tcp"}})
	if !errors.Is(err, errManagedProxyFirewall) || mutated {
		t.Fatalf("unmanaged nftables table = %v, mutated = %v", err, mutated)
	}
}

func TestProxyFirewallAddFailureDoesNotRemoveStaleManagedRules(t *testing.T) {
	removed := false
	firewall := &proxyFirewall{
		lookPath: func(name string) (string, error) {
			if name == "iptables" {
				return name, nil
			}
			return "", errors.New("not installed")
		},
		runCommand: func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			if arguments[0] == "-S" {
				return []byte("-P INPUT DROP\n-A INPUT -p tcp --dport 80 -m comment --comment vps-panel-proxy-tcp -j ACCEPT\n"), nil
			}
			if arguments[0] == "-I" {
				return nil, errors.New("add failed")
			}
			if arguments[0] == "-D" {
				removed = true
			}
			return nil, nil
		},
	}

	err := firewall.reconcile(t.Context(), []int{443})
	if !errors.Is(err, errManagedProxyFirewall) || removed {
		t.Fatalf("add failure = %v, removed stale = %v", err, removed)
	}
}

func TestProxyFirewallParsesOwnedUFWAndFirewalldRules(t *testing.T) {
	ufwPorts, ufwRules := parseUFWManagedRules(`Status: active
[ 1] 22/tcp ALLOW IN Anywhere
[ 2] 443/tcp ALLOW IN Anywhere # vps-panel-proxy-tcp
[ 3] 8443/tcp (v6) ALLOW IN Anywhere (v6) # vps-panel-proxy-tcp
[ 4] 8388/udp ALLOW IN Anywhere # vps-panel-proxy-udp`)
	if len(ufwPorts) != 3 || len(ufwRules) != 3 {
		t.Fatalf("UFW managed rules = %v, %+v", ufwPorts, ufwRules)
	}
	firewalldPorts := parseManagedFirewallRules(`0 -p tcp --dport 22 -j ACCEPT
0 -p tcp --dport 443 -m comment --comment "vps-panel-proxy-tcp" -j ACCEPT
0 -p udp --dport 8388 -m comment --comment "vps-panel-proxy-udp" -j ACCEPT`)
	if len(firewalldPorts) != 2 {
		t.Fatalf("firewalld managed rules = %v", firewalldPorts)
	}
	if _, exists := firewalldPorts[firewallRule{port: 443, protocol: "tcp"}]; !exists {
		t.Fatal("managed firewalld port was not recognized")
	}
	if _, exists := firewalldPorts[firewallRule{port: 8388, protocol: "udp"}]; !exists {
		t.Fatal("managed firewalld UDP port was not recognized")
	}
}

func TestProxyFirewallReconcilesActiveUFW(t *testing.T) {
	var mutations []string
	firewall := &proxyFirewall{
		lookPath: func(name string) (string, error) {
			if name == "ufw" {
				return name, nil
			}
			return "", errors.New("not installed")
		},
		runCommand: func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			command := strings.Join(arguments, " ")
			switch command {
			case "status":
				return []byte("Status: active\n"), nil
			case "status numbered":
				return []byte("[ 1] 80/tcp ALLOW IN Anywhere # vps-panel-proxy-tcp\n"), nil
			default:
				mutations = append(mutations, command)
				return nil, nil
			}
		},
	}
	if err := firewall.reconcile(t.Context(), []int{443}); err != nil {
		t.Fatal(err)
	}
	want := []string{"allow 443/tcp comment vps-panel-proxy-tcp", "--force delete 1"}
	if strings.Join(mutations, "|") != strings.Join(want, "|") {
		t.Fatalf("UFW mutations = %v, want %v", mutations, want)
	}
}

func TestProxyFirewallReconcilesActiveFirewalld(t *testing.T) {
	var mutations []string
	firewall := &proxyFirewall{
		lookPath: func(name string) (string, error) {
			if name == "firewall-cmd" {
				return name, nil
			}
			return "", errors.New("not installed")
		},
		runCommand: func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			command := strings.Join(arguments, " ")
			switch command {
			case "--state":
				return []byte("running\n"), nil
			case "--direct --get-rules ipv4 filter INPUT":
				return []byte("0 -p tcp --dport 80 -m comment --comment vps-panel-proxy-tcp -j ACCEPT\n"), nil
			default:
				mutations = append(mutations, command)
				return nil, nil
			}
		},
	}
	if err := firewall.reconcile(t.Context(), []int{443}); err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 2 || !strings.Contains(mutations[0], "--add-rule") || !strings.Contains(mutations[0], "--dport 443") ||
		!strings.Contains(mutations[1], "--remove-rule") || !strings.Contains(mutations[1], "--dport 80") {
		t.Fatalf("firewalld mutations = %v", mutations)
	}
}

func TestProxyFirewallShadowsocksReconcilesTCPAndUDPWithoutTouchingUserRules(t *testing.T) {
	managed := map[firewallRule]struct{}{
		{port: 8388, protocol: "tcp"}: {},
		{port: 8388, protocol: "udp"}: {},
	}
	mutations := make([][]string, 0)
	firewall := &proxyFirewall{
		lookPath: func(name string) (string, error) {
			if name == "iptables" {
				return name, nil
			}
			return "", errors.New("not installed")
		},
		runCommand: func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			if arguments[0] == "-S" {
				lines := []string{"-P INPUT DROP", "-A INPUT -p udp --dport 5353 -j ACCEPT"}
				for rule := range managed {
					lines = append(lines, "-A INPUT "+strings.Join(managedIPTablesRule(rule), " "))
				}
				return []byte(strings.Join(lines, "\n")), nil
			}
			mutations = append(mutations, append([]string(nil), arguments...))
			portValue, _ := fieldAfter(arguments, "--dport")
			port, _ := strconv.Atoi(portValue)
			protocol, _ := fieldAfter(arguments, "-p")
			rule := firewallRule{port: port, protocol: protocol}
			if !containsFields(arguments, "--comment", firewallComment(protocol)) {
				return nil, errors.New("attempted to change an unowned rule")
			}
			if arguments[0] == "-I" {
				managed[rule] = struct{}{}
			} else {
				delete(managed, rule)
			}
			return nil, nil
		},
	}
	desired := []firewallRule{{port: 443, protocol: "tcp"}, {port: 8389, protocol: "tcp"}, {port: 8389, protocol: "udp"}}
	if err := firewall.reconcileRules(t.Context(), desired); err != nil {
		t.Fatal(err)
	}
	for _, rule := range desired {
		if _, exists := managed[rule]; !exists {
			t.Fatalf("missing desired rule %+v; managed = %+v", rule, managed)
		}
	}
	if _, exists := managed[firewallRule{port: 8388, protocol: "tcp"}]; exists {
		t.Fatal("stale Shadowsocks TCP rule was not removed")
	}
	if _, exists := managed[firewallRule{port: 8388, protocol: "udp"}]; exists {
		t.Fatal("stale Shadowsocks UDP rule was not removed")
	}
	if len(mutations) != 5 {
		t.Fatalf("mixed firewall mutations = %v", mutations)
	}
	if err := firewall.reconcileRules(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if len(managed) != 0 {
		t.Fatalf("disable/delete retained managed rules: %+v", managed)
	}
}

func TestExpectedProxyFirewallRulesAreProtocolSpecific(t *testing.T) {
	vless := testDesiredTLSProxy()
	shadowsocks := testDesiredShadowsocksProxy(2, 8388, "2022-blake3-aes-128-gcm", 16)
	empty := testDesiredShadowsocksProxy(3, 8389, "2022-blake3-aes-128-gcm", 16)
	empty.Clients = nil
	rules := expectedProxyFirewallRules([]desiredProxy{vless, shadowsocks, empty})
	want := []firewallRule{{port: 443, protocol: "tcp"}, {port: 8388, protocol: "tcp"}, {port: 8388, protocol: "udp"}}
	if fmt.Sprint(rules) != fmt.Sprint(want) {
		t.Fatalf("expected firewall rules = %+v, want %+v", rules, want)
	}
}

func TestRealmFirewallPortChangeAndCleanupDoNotTouchProxyOrUserRules(t *testing.T) {
	managed := map[firewallRule]struct{}{
		{port: 9502, protocol: "tcp"}: {},
		{port: 9502, protocol: "udp"}: {},
	}
	proxyRule := "-A INPUT -p tcp --dport 443 -m comment --comment vps-panel-proxy-tcp -j ACCEPT"
	userRule := "-A INPUT -p tcp --dport 22 -j ACCEPT"
	firewall := &proxyFirewall{
		owner: "realm",
		lookPath: func(name string) (string, error) {
			if name == "iptables" {
				return name, nil
			}
			return "", errors.New("not installed")
		},
		runCommand: func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			if arguments[0] == "-S" {
				lines := []string{"-P INPUT DROP", proxyRule, userRule}
				for rule := range managed {
					lines = append(lines, "-A INPUT "+strings.Join((&proxyFirewall{owner: "realm"}).managedIPTablesRule(rule), " "))
				}
				return []byte(strings.Join(lines, "\n")), nil
			}
			portValue, _ := fieldAfter(arguments, "--dport")
			port, _ := strconv.Atoi(portValue)
			protocol, _ := fieldAfter(arguments, "-p")
			rule := firewallRule{port: port, protocol: protocol}
			if !containsFields(arguments, "--comment", (&proxyFirewall{owner: "realm"}).firewallComment(protocol)) {
				return nil, errors.New("attempted to change another owner's rule")
			}
			if arguments[0] == "-I" {
				managed[rule] = struct{}{}
			} else {
				delete(managed, rule)
			}
			return nil, nil
		},
	}
	desired := []firewallRule{{port: 9600, protocol: "tcp"}, {port: 9600, protocol: "udp"}}
	if err := firewall.reconcileRules(t.Context(), desired); err != nil {
		t.Fatal(err)
	}
	if len(managed) != 2 {
		t.Fatalf("Realm port change = %+v", managed)
	}
	for _, rule := range desired {
		if _, ok := managed[rule]; !ok {
			t.Fatalf("Realm rule missing after port change: %+v", rule)
		}
	}
	if err := firewall.reconcileRules(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if len(managed) != 0 {
		t.Fatalf("Realm disable/delete retained rules: %+v", managed)
	}
}
