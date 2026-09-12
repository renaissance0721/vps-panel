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
					lines = append(lines, "-A INPUT "+strings.Join(managedIPTablesRule(port), " "))
				}
				return []byte(strings.Join(lines, "\n")), nil
			}
			mutations = append(mutations, append([]string(nil), arguments...))
			portValue, ok := fieldAfter(arguments, "--dport")
			if !ok || !containsFields(arguments, "--comment", managedProxyFirewallComment) {
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
[ 3] 8443/tcp (v6) ALLOW IN Anywhere (v6) # vps-panel-proxy-tcp`)
	if len(ufwPorts) != 2 || len(ufwRules) != 2 {
		t.Fatalf("UFW managed rules = %v, %+v", ufwPorts, ufwRules)
	}
	firewalldPorts := parseManagedRulePorts(`0 -p tcp --dport 22 -j ACCEPT
0 -p tcp --dport 443 -m comment --comment "vps-panel-proxy-tcp" -j ACCEPT`)
	if len(firewalldPorts) != 1 {
		t.Fatalf("firewalld managed rules = %v", firewalldPorts)
	}
	if _, exists := firewalldPorts[443]; !exists {
		t.Fatal("managed firewalld port was not recognized")
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
