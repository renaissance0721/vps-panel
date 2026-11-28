package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

func TestHoppingFailureRestoresPreviousSingboxConfiguration(t *testing.T) {
	manager, service := newTestSingboxManager(t)
	proxy := testSingboxProxy(t, 1, 443)
	previousState := desiredSingboxState{Enabled: true, Proxies: []desiredProxy{proxy}}
	if err := manager.apply(t.Context(), previousState); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(manager.configPath)
	if err != nil {
		t.Fatal(err)
	}
	proxy.Port = 8443
	state := desiredState{Hysteria2HoppingControl: true, Singbox: &desiredSingboxState{Enabled: true, Proxies: []desiredProxy{proxy}}}
	components := map[string]string{}
	err = applySingboxAndHopping(t.Context(), state, manager, func(context.Context, desiredState) error { return errHoppingApply }, components)
	current, readErr := os.ReadFile(manager.configPath)
	if !errors.Is(err, errHoppingApply) || readErr != nil || !bytes.Equal(previous, current) || !service.active || components["hy2_port_hopping"] != "failed" {
		t.Fatalf("hopping failure did not preserve previous service: %v, %v, %+v", err, readErr, components)
	}
	state.Singbox = &previousState
	components = map[string]string{}
	err = applySingboxAndHopping(t.Context(), state, manager, func(context.Context, desiredState) error { return errHoppingUnsupported }, components)
	if !errors.Is(err, errHoppingUnsupported) || components["singbox"] != "success" || components["hy2_port_hopping"] != "unsupported" {
		t.Fatalf("independent component states = %+v, %v", components, err)
	}
}

func TestHY2HoppingRulesRejectInvalidRanges(t *testing.T) {
	proxy := desiredProxy{ID: 1, Listen: "0.0.0.0", Port: 443, Hysteria2: &desiredHysteria2{PortHoppingEnabled: true, PortHoppingStart: 20000, PortHoppingEnd: 20100}}
	rules, err := hy2HoppingRules([]desiredProxy{proxy})
	if err != nil || len(rules) != 1 {
		t.Fatalf("valid hopping rules = %v, %v", rules, err)
	}
	other := proxy
	other.ID = 2
	other.Port = 444
	other.Hysteria2 = &desiredHysteria2{PortHoppingEnabled: true, PortHoppingStart: 20050, PortHoppingEnd: 20200}
	if _, err := hy2HoppingRules([]desiredProxy{proxy, other}); !errors.Is(err, errHoppingConflict) {
		t.Fatalf("overlapping hopping rules = %v", err)
	}
	other.Hysteria2.PortHoppingEnabled = false
	other.Port = 20050
	if _, err := hy2HoppingRules([]desiredProxy{proxy, other}); !errors.Is(err, errHoppingConflict) {
		t.Fatalf("hopping range with listener = %v", err)
	}
}

func TestHY2HoppingAtomicBatchAndOwnership(t *testing.T) {
	root := t.TempDir()
	exists, owned, failCheck, failApply := false, true, false, false
	var applied []string
	manager := &hy2HoppingManager{journalPath: filepath.Join(root, "state", "hopping"),
		lookPath: func(string) (string, error) { return "nft", nil },
		checkUDP: func([]hy2HoppingRule) error { return nil },
		runCommand: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			switch strings.Join(args, " ") {
			case "list tables":
				if exists {
					return []byte("table inet unrelated\ntable inet " + hy2HoppingTable + "\n"), nil
				}
				return nil, nil
			case "-j list ruleset":
				return []byte(`{"nftables":[]}`), nil
			case "list chain inet " + hy2HoppingTable + " prerouting":
				if owned {
					return []byte(`counter comment "` + hy2HoppingMarker + `"`), nil
				}
				return []byte("foreign"), nil
			}
			if len(args) < 2 {
				t.Fatalf("unexpected nft args: %v", args)
			}
			data, err := os.ReadFile(args[len(args)-1])
			if err != nil {
				t.Fatal(err)
			}
			if args[0] == "-c" {
				if failCheck {
					return nil, errors.New("invalid candidate")
				}
				return nil, nil
			}
			if failApply {
				return nil, errors.New("atomic update rejected")
			}
			applied = append(applied, string(data))
			exists = !strings.HasPrefix(string(data), "delete table")
			return nil, nil
		}}
	rules := []hy2HoppingRule{{id: 1, listen: "0.0.0.0", port: 443, start: 20000, end: 20100}, {id: 2, listen: "::", port: 8443, start: 30000, end: 30100}}
	if err := manager.apply(t.Context(), rules); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || !strings.Contains(applied[0], "meta nfproto ipv4 fib daddr type local udp dport 20000-20100 redirect to :443") || !strings.Contains(applied[0], "meta nfproto ipv6 fib daddr type local udp dport 30000-30100 redirect to :8443") {
		t.Fatalf("batch = %v", applied)
	}
	failCheck = true
	if err := manager.apply(t.Context(), rules[:1]); !errors.Is(err, errHoppingApply) || len(applied) != 1 {
		t.Fatalf("failed validation changed rules: %v", err)
	}
	failCheck = false
	failApply = true
	if err := manager.apply(t.Context(), rules[:1]); !errors.Is(err, errHoppingApply) || len(applied) != 1 {
		t.Fatalf("failed apply changed existing rules: %v", err)
	}
	failApply = false
	if err := manager.apply(t.Context(), rules[:1]); err != nil || len(applied) != 2 || strings.Contains(applied[1], "vps-panel-hy2-2") || !strings.Contains(applied[1], "vps-panel-hy2-1") {
		t.Fatalf("remove one hopping rule: %v, %v", err, applied)
	}
	owned = false
	if err := manager.apply(t.Context(), nil); !errors.Is(err, errHoppingConflict) || len(applied) != 2 {
		t.Fatalf("unmanaged table changed: %v", err)
	}
	owned = true
	if err := manager.apply(t.Context(), nil); err != nil || exists || len(applied) != 3 {
		t.Fatalf("cleanup = %v, exists %v", err, exists)
	}
}

func TestHoppingManagedNetworkAndRanges(t *testing.T) {
	proxy := desiredProxy{ID: 1, Listen: "0.0.0.0", Port: 443, Protocol: "hysteria2", Hysteria2: &desiredHysteria2{PortHoppingEnabled: true, PortHoppingStart: 20000, PortHoppingEnd: 20100}}
	rules, err := hy2HoppingRules([]desiredProxy{proxy})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		state    desiredState
		conflict bool
	}{
		{"vless tcp", desiredState{Xray: desiredXrayState{Proxies: []desiredProxy{{Protocol: "vless", Port: 20001}}}}, false},
		{"shadowsocks both", desiredState{Xray: desiredXrayState{Proxies: []desiredProxy{{Protocol: "shadowsocks", Port: 20001}}}}, true},
		{"realm tcp", desiredState{Realm: desiredRealmState{Relays: []desiredRelay{{Network: "tcp", ListenPort: 20001}}}}, false},
		{"realm udp", desiredState{Realm: desiredRealmState{Relays: []desiredRelay{{Network: "udp", ListenPort: 20001}}}}, true},
		{"realm both", desiredState{Realm: desiredRealmState{Relays: []desiredRelay{{Network: "tcp,udp", ListenPort: 20001}}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkHoppingManagedListeners(rules, tc.state); errors.Is(got, errHoppingConflict) != tc.conflict {
				t.Fatalf("conflict = %v", got)
			}
		})
	}
	for _, bounds := range [][2]int{{1, 1024}, {1023, 1023}, {20000, 30000}, {21000, 20000}} {
		proxy.Hysteria2.PortHoppingStart, proxy.Hysteria2.PortHoppingEnd = bounds[0], bounds[1]
		if _, err := hy2HoppingRules([]desiredProxy{proxy}); err == nil {
			t.Fatalf("accepted %v", bounds)
		}
	}
}

func TestHoppingNATProbeCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		missing, denied, v4, v6 bool
	}{{"missing", true, false, false, false}, {"no permissions", false, true, false, false}, {"ipv4 only", false, false, true, false}, {"ipv6 only", false, false, false, true}, {"both", false, false, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			m := &hy2HoppingManager{lookPath: func(string) (string, error) {
				if tc.missing {
					return "", errors.New("missing")
				}
				return "nft", nil
			}, runCommand: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				data, err := os.ReadFile(args[len(args)-1])
				if err != nil {
					return nil, err
				}
				if tc.denied || (strings.Contains(string(data), "nfproto ipv4") && !tc.v4) || (strings.Contains(string(data), "nfproto ipv6") && !tc.v6) {
					return nil, errors.New("denied")
				}
				return nil, nil
			}}
			got := m.probe(t.Context())
			if got != [2]bool{tc.v4, tc.v6} {
				t.Fatalf("probe = %v", got)
			}
			caps := capabilitiesWithHopping(nil, got)
			has := func(s string) bool {
				for _, c := range caps {
					if c == s {
						return true
					}
				}
				return false
			}
			if has(agentcontrol.CapabilityHysteria2PortHopping) != (tc.v4 || tc.v6) || has(agentcontrol.CapabilityHysteria2HoppingIPv4) != tc.v4 || has(agentcontrol.CapabilityHysteria2HoppingIPv6) != tc.v6 {
				t.Fatalf("capabilities = %v", caps)
			}
		})
	}
}

func TestForeignNATConflictProtection(t *testing.T) {
	ranges := []hy2HoppingRule{{start: 20000, end: 20100}}
	for _, tc := range []struct {
		name, expr string
		conflict   bool
	}{
		{"udp overlap", `[{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":20050}},{"dnat":{}}]`, true},
		{"udp disjoint", `[{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":{"range":[30000,30100]}}},{"redirect":{}}]`, false},
		{"tcp same port", `[{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":20050}},{"redirect":{}}]`, false},
		{"dynamic set", `[{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":"@ports"}},{"dnat":{}}]`, true},
		{"unrestricted", `[{"redirect":{}}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := `{"nftables":[{"rule":{"family":"inet","table":"other","expr":` + tc.expr + `}}]}`
			err := checkForeignNATRules([]byte(doc), ranges)
			if errors.Is(err, errHoppingConflict) != tc.conflict {
				t.Fatalf("result = %v", err)
			}
		})
	}
}

func TestHoppingDoesNotModifyForeignNAT(t *testing.T) {
	calls := 0
	manager := &hy2HoppingManager{journalPath: filepath.Join(t.TempDir(), "state"), lookPath: func(string) (string, error) { return "nft", nil }, checkUDP: func([]hy2HoppingRule) error { return nil }, runCommand: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		switch strings.Join(args, " ") {
		case "list tables":
			return []byte("table inet other\n"), nil
		case "-j list ruleset":
			return []byte(`{"nftables":[{"rule":{"family":"inet","table":"other","expr":[{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":20050}},{"dnat":{}}]}}]}`), nil
		}
		t.Fatalf("unexpected mutation: %v", args)
		return nil, nil
	}}
	err := manager.apply(t.Context(), []hy2HoppingRule{{id: 1, listen: "0.0.0.0", port: 443, start: 20000, end: 20100}})
	if !errors.Is(err, errHoppingNATConflict) || calls != 2 {
		t.Fatalf("foreign NAT protection = %v, calls %d", err, calls)
	}
}
