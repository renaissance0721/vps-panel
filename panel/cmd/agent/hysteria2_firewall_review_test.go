package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHoppingNFTChainContextAndMatches(t *testing.T) {
	ranges := []hy2HoppingRule{{listen: "0.0.0.0", start: 20000, end: 20100}}
	match := func(right string) string {
		return `{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":` + right + `}}`
	}
	for _, tc := range []struct {
		name, family, chainType, hook, expr string
		conflict                            bool
	}{
		{"filter vmap", "inet", "filter", "prerouting", `{"vmap":{"key":1,"data":"@map"}}`, false},
		{"nat vmap", "inet", "nat", "prerouting", `{"vmap":{"key":1,"data":"@map"}}`, true},
		{"output nat", "ip", "nat", "output", `{"redirect":{}}`, false},
		{"overlap", "ip", "nat", "prerouting", match(`20001`) + `,{"dnat":{}}`, true},
		{"disjoint", "ip", "nat", "prerouting", match(`30001`) + `,{"dnat":{}}`, false},
		{"tcp only", "inet", "nat", "prerouting", `{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":20001}},{"redirect":{}}`, false},
		{"dynamic", "ip", "nat", "prerouting", match(`"@ports"`) + `,{"dnat":{}}`, true},
		{"dynamic plus disjoint", "ip", "nat", "prerouting", match(`30001`) + `,` + match(`"@ports"`) + `,{"dnat":{}}`, true},
		{"two disjoint matches", "ip", "nat", "prerouting", match(`{"range":[20000,20010]}`) + `,` + match(`{"range":[20020,20030]}`) + `,{"dnat":{}}`, false},
		{"ipv6 isolation", "ip6", "nat", "prerouting", `{"dnat":{}}`, false},
		{"unparseable", "ip", "nat", "prerouting", match(`{"map":"unknown"}`) + `,{"dnat":{}}`, true},
		{"unparseable nat with disjoint port", "ip", "nat", "prerouting", match(`30000`) + `,{"dnat":"invalid"}`, true},
		{"nat vmap with disjoint port", "ip", "nat", "prerouting", match(`30000`) + `,{"vmap":{"key":1,"data":"@dynamic"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := fmt.Sprintf(`{"nftables":[{"chain":{"family":%q,"table":"other","name":"c","type":%q,"hook":%q}},{"rule":{"family":%q,"table":"other","chain":"c","expr":[%s]}}]}`, tc.family, tc.chainType, tc.hook, tc.family, tc.expr)
			if !json.Valid([]byte(doc)) {
				t.Fatal(doc)
			}
			err := checkForeignNATRules([]byte(doc), ranges)
			if errors.Is(err, errHoppingConflict) != tc.conflict {
				t.Fatalf("conflict = %v", err)
			}
		})
	}
}

func TestHoppingNFTJumpedNATChain(t *testing.T) {
	doc := `{"nftables":[{"chain":{"family":"inet","table":"foreign","name":"pre","type":"nat","hook":"prerouting"}},{"chain":{"family":"inet","table":"foreign","name":"child"}},{"rule":{"family":"inet","table":"foreign","chain":"pre","expr":[{"jump":{"target":"child"}}]}},{"rule":{"family":"inet","table":"foreign","chain":"child","expr":[{"vmap":{"key":1,"data":"@map"}}]}}]}`
	err := checkForeignNATRules([]byte(doc), []hy2HoppingRule{{listen: "0.0.0.0", start: 20000, end: 20100}})
	if !errors.Is(err, errHoppingNATConflict) {
		t.Fatalf("indirect NAT map = %v", err)
	}
}

func TestLegacyConflictStopsBeforeNFTMutation(t *testing.T) {
	calls := 0
	m := &hy2HoppingManager{journalPath: filepath.Join(t.TempDir(), "state"), lookPath: func(string) (string, error) { return "nft", nil }, checkUDP: func([]hy2HoppingRule) error { return nil }, checkLegacy: func(context.Context, []hy2HoppingRule) error { return errHoppingLegacyConflict }, runCommand: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		switch strings.Join(args, " ") {
		case "list tables":
			return nil, nil
		case "-j list ruleset":
			return []byte(`{"nftables":[]}`), nil
		}
		t.Fatalf("unexpected mutation %v", args)
		return nil, nil
	}}
	err := m.apply(t.Context(), []hy2HoppingRule{{id: 1, listen: "0.0.0.0", port: 443, start: 20000, end: 20100}})
	if !errors.Is(err, errHoppingLegacyConflict) || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestLegacyNATRuleParsing(t *testing.T) {
	rules := []hy2HoppingRule{{listen: "0.0.0.0", start: 20000, end: 20100}}
	for _, tc := range []struct {
		name, line string
		conflict   bool
	}{
		{"udp overlap", `-A PREROUTING -p udp -m udp --dport 20050 -j DNAT --to-destination 127.0.0.1:443`, true},
		{"udp disjoint", `-A PREROUTING -p udp -m udp --dport 30000:30100 -j REDIRECT --to-ports 443`, false},
		{"tcp overlap", `-A PREROUTING -p tcp --dport 20050 -j REDIRECT --to-ports 443`, false},
		{"dynamic", `-A PREROUTING -p udp -m set --match-set ports dst -j REDIRECT --to-ports 443`, true},
		{"multiport", `-A PREROUTING -p udp -m multiport --dports 42,20020:20030 -j REDIRECT --to-ports 443`, true},
		{"dynamic with disjoint", `-A PREROUTING -p udp --dport 30000 -m set --match-set x dst -j REDIRECT`, true},
		{"negated tcp", `-A PREROUTING ! -p tcp -j DNAT --to-destination 127.0.0.1`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkLegacyNATRules([]byte("*nat\n:PREROUTING ACCEPT [0:0]\n"+tc.line+"\nCOMMIT\n"), rules)
			if errors.Is(err, errHoppingConflict) != tc.conflict {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if checkLegacyNATRules([]byte(`-A PREROUTING -p udp --dport "unterminated`), rules) == nil {
		t.Fatal("malformed rule accepted")
	}
}

func TestLegacyBackendDetection(t *testing.T) {
	rules := []hy2HoppingRule{{listen: "0.0.0.0", start: 20000, end: 20100}, {listen: "::", start: 30000, end: 30100}}
	for _, tc := range []struct {
		name    string
		tools   map[string]string
		outputs map[string]string
		readErr error
		proc    string
		wantErr bool
	}{
		{"nft only", map[string]string{"iptables": "iptables", "ip6tables": "ip6tables"}, map[string]string{"iptables --version": "iptables v1.8 (nf_tables)", "ip6tables --version": "ip6tables v1.8 (nf_tables)"}, os.ErrNotExist, "", false},
		{"legacy conflicts", map[string]string{"iptables-legacy-save": "iptables-legacy-save"}, map[string]string{"iptables-legacy-save -t nat": "*nat\n-A PREROUTING -p udp --dport 20001 -j REDIRECT\nCOMMIT\n"}, os.ErrNotExist, "", true},
		{"ipv6 legacy conflict", map[string]string{"ip6tables-legacy-save": "ip6tables-legacy-save"}, map[string]string{"ip6tables-legacy-save -t nat": "*nat\n-A PREROUTING -p udp --dport 30001 -j DNAT\nCOMMIT\n"}, os.ErrNotExist, "", true},
		{"selected legacy alternative", map[string]string{"iptables": "iptables", "iptables-save": "iptables-save"}, map[string]string{"iptables --version": "iptables v1.8 (legacy)", "iptables -t nat -S": "-P PREROUTING ACCEPT\n-A PREROUTING -p udp --dport 20001 -j REDIRECT\n"}, os.ErrNotExist, "", true},
		{"hidden legacy nat", map[string]string{}, nil, nil, "nat\n", true},
		{"no tools or tables", map[string]string{}, nil, os.ErrNotExist, "", false},
		{"cannot read legacy", map[string]string{"iptables-legacy-save": "iptables-legacy-save"}, nil, os.ErrNotExist, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(name string) (string, error) {
				if v, ok := tc.tools[name]; ok {
					return v, nil
				}
				return "", errors.New("missing")
			}
			run := func(_ context.Context, name string, args ...string) ([]byte, error) {
				if v, ok := tc.outputs[name+" "+strings.Join(args, " ")]; ok {
					return []byte(v), nil
				}
				return nil, errors.New("denied")
			}
			read := func(string) ([]byte, error) { return []byte(tc.proc), tc.readErr }
			err := inspectLegacyNAT(t.Context(), rules, lookup, run, read)
			if (err != nil) != tc.wantErr {
				t.Fatalf("detection = %v", err)
			}
		})
	}
}
