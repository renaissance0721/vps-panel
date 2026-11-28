package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
