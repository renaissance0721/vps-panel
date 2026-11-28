package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	hy2HoppingTable  = "vps_panel_hy2_hopping"
	hy2HoppingMarker = "vps-panel-hy2-hopping-owner"
)

var (
	errHoppingUnsupported = errors.New("Hysteria2 port hopping requires nftables and CAP_NET_ADMIN with inet NAT support")
	errHoppingConflict    = errors.New("Hysteria2 port hopping conflicts with an existing UDP listener or unmanaged nftables table")
	errHoppingNATInspect  = fmt.Errorf("%w: cannot inspect existing NAT rules safely", errHoppingConflict)
	errHoppingNATConflict = fmt.Errorf("%w: an existing NAT rule may redirect UDP in the hopping range", errHoppingConflict)
	errHoppingApply       = errors.New("Hysteria2 port hopping nftables synchronization failed; existing rules were retained")
)

type hy2HoppingRule struct {
	id               int64
	listen           string
	port, start, end int
}

type hy2HoppingManager struct {
	lookPath    func(string) (string, error)
	runCommand  func(context.Context, string, ...string) ([]byte, error)
	checkUDP    func([]hy2HoppingRule) error
	checkLegacy func(context.Context, []hy2HoppingRule) error
	journalPath string
}

func newHY2HoppingManager() *hy2HoppingManager {
	m := &hy2HoppingManager{lookPath: exec.LookPath, runCommand: runFirewallCommand, checkUDP: checkHY2HoppingUDPListeners, journalPath: "/var/lib/vps-panel/agent/hy2-hopping-owned"}
	m.checkLegacy = func(ctx context.Context, rules []hy2HoppingRule) error {
		return inspectLegacyNAT(ctx, rules, m.lookPath, m.runCommand, os.ReadFile)
	}
	return m
}

// The disposable table is created and deleted in one nft transaction, so a
// failed probe cannot leave test rules in the host firewall.
func (m *hy2HoppingManager) probe(ctx context.Context) [2]bool {
	var supported [2]bool
	command, err := m.lookPath("nft")
	if err != nil {
		return supported
	}
	for i, family := range []string{"ipv4", "ipv6"} {
		table := fmt.Sprintf("vps_panel_hy2_probe_%d_%d", os.Getpid(), time.Now().UnixNano())
		batch := fmt.Sprintf("add table inet %s\nadd chain inet %s prerouting { type nat hook prerouting priority dstnat; policy accept; }\nadd rule inet %s prerouting meta nfproto %s fib daddr type local udp dport 1024 redirect to :1025\ndelete table inet %s\n", table, table, table, family, table)
		path, err := writeTemporaryFile(os.TempDir(), ".vps-panel-hy2-probe-*.nft", []byte(batch), 0o600)
		if err != nil {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = m.runCommand(probeCtx, command, "-c", "-f", path)
		if err == nil {
			_, err = m.runCommand(probeCtx, command, "-f", path)
		}
		cancel()
		os.Remove(path)
		supported[i] = err == nil
	}
	return supported
}

func checkHY2HoppingUDPListeners(rules []hy2HoppingRule) error {
	if len(rules) == 0 {
		return nil
	}
	readable := false
	for _, path := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		readable = true
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			_, portText, found := strings.Cut(fields[1], ":")
			port, err := strconv.ParseUint(portText, 16, 16)
			if !found || err != nil {
				continue
			}
			for _, rule := range rules {
				if int(port) >= rule.start && int(port) <= rule.end {
					return errHoppingConflict
				}
			}
		}
	}
	if !readable {
		return errHoppingUnsupported
	}
	return nil
}

func hy2HoppingRules(proxies []desiredProxy) ([]hy2HoppingRule, error) {
	var rules []hy2HoppingRule
	for _, proxy := range proxies {
		settings := proxy.Hysteria2
		if settings == nil || !settings.PortHoppingEnabled {
			continue
		}
		if proxy.ID <= 0 || proxy.Port < 1 || proxy.Port > 65535 ||
			(proxy.Listen != "0.0.0.0" && proxy.Listen != "::") ||
			settings.PortHoppingStart < 1024 || settings.PortHoppingEnd > 65535 ||
			settings.PortHoppingStart > settings.PortHoppingEnd ||
			settings.PortHoppingEnd-settings.PortHoppingStart+1 > 10000 ||
			(proxy.Port >= settings.PortHoppingStart && proxy.Port <= settings.PortHoppingEnd) {
			return nil, errUnsupportedManagedConfig
		}
		rules = append(rules, hy2HoppingRule{id: proxy.ID, listen: proxy.Listen, port: proxy.Port, start: settings.PortHoppingStart, end: settings.PortHoppingEnd})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].start < rules[j].start })
	for i, rule := range rules {
		for _, proxy := range proxies {
			if proxy.Protocol != "vless" && proxy.Port >= rule.start && proxy.Port <= rule.end {
				return nil, errHoppingConflict
			}
		}
		if i > 0 && rule.start <= rules[i-1].end {
			return nil, errHoppingConflict
		}
	}
	return rules, nil
}

// Each batch changes only the owned table and is checked before its atomic
// application. The marker prevents adopting a table created by other software.
func (m *hy2HoppingManager) apply(ctx context.Context, rules []hy2HoppingRule) error {
	if err := m.checkUDP(rules); err != nil {
		return err
	}
	_, journalErr := os.Stat(m.journalPath)
	if journalErr != nil && !errors.Is(journalErr, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect managed state", errHoppingApply)
	}
	ownedBefore := journalErr == nil
	command, err := m.lookPath("nft")
	if err != nil {
		if len(rules) == 0 && !ownedBefore {
			return nil
		}
		return errHoppingUnsupported
	}
	output, err := m.runCommand(ctx, command, "list", "tables")
	if err != nil {
		if len(rules) == 0 && !ownedBefore {
			return nil
		}
		return errHoppingUnsupported
	}
	exists := nftTablesContain(string(output), "inet", hy2HoppingTable)
	if exists {
		output, err = m.runCommand(ctx, command, "list", "chain", "inet", hy2HoppingTable, "prerouting")
		if err != nil {
			return errHoppingUnsupported
		}
		if !strings.Contains(string(output), `comment "`+hy2HoppingMarker+`"`) {
			return errHoppingConflict
		}
	}
	if len(rules) > 0 {
		output, err = m.runCommand(ctx, command, "-j", "list", "ruleset")
		if err != nil {
			return errHoppingNATInspect
		}
		if err := checkForeignNATRules(output, rules); err != nil {
			return err
		}
		if m.checkLegacy != nil {
			if err := m.checkLegacy(ctx, rules); err != nil {
				return err
			}
		}
	}
	if !exists && len(rules) == 0 {
		if ownedBefore {
			if err := os.Remove(m.journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return errHoppingApply
			}
		}
		return nil
	}
	if len(rules) != 0 {
		if err := ensureSecureDirectory(filepath.Dir(m.journalPath), 0o700); err != nil {
			return errHoppingApply
		}
		if err := writeFileAtomically(m.journalPath, []byte("managed by vps-panel\n"), 0o600); err != nil {
			return errHoppingApply
		}
	}
	var batch strings.Builder
	if len(rules) == 0 {
		fmt.Fprintf(&batch, "delete table inet %s\n", hy2HoppingTable)
	} else if !exists {
		fmt.Fprintf(&batch, "add table inet %s\nadd chain inet %s prerouting { type nat hook prerouting priority dstnat; policy accept; }\n", hy2HoppingTable, hy2HoppingTable)
	} else {
		fmt.Fprintf(&batch, "flush chain inet %s prerouting\n", hy2HoppingTable)
	}
	if len(rules) != 0 {
		fmt.Fprintf(&batch, "add rule inet %s prerouting counter comment \"%s\"\n", hy2HoppingTable, hy2HoppingMarker)
	}
	for _, rule := range rules {
		family := "ipv4"
		if rule.listen == "::" {
			family = "ipv6"
		}
		ports := strconv.Itoa(rule.start)
		if rule.start != rule.end {
			ports = fmt.Sprintf("%d-%d", rule.start, rule.end)
		}
		fmt.Fprintf(&batch, "add rule inet %s prerouting meta nfproto %s fib daddr type local udp dport %s redirect to :%d comment \"vps-panel-hy2-%d\"\n", hy2HoppingTable, family, ports, rule.port, rule.id)
	}
	path, err := writeTemporaryFile(os.TempDir(), ".vps-panel-hy2-*.nft", []byte(batch.String()), 0o600)
	if err != nil {
		return fmt.Errorf("%w: %v", errHoppingApply, err)
	}
	defer os.Remove(path)
	if _, err := m.runCommand(ctx, command, "-c", "-f", path); err != nil {
		if !exists && !ownedBefore {
			_ = os.Remove(m.journalPath)
		}
		return errHoppingApply
	}
	if _, err := m.runCommand(ctx, command, "-f", path); err != nil {
		return errHoppingApply
	}
	if len(rules) == 0 {
		if err := os.Remove(m.journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errHoppingApply
		}
	}
	return nil
}

// Inspect nft's structured output. Unsupported dynamic NAT expressions are
// rejected because their effective UDP destination ports cannot be proven safe.
func checkForeignNATRules(data []byte, ranges []hy2HoppingRule) error {
	var document struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(data, &document); err != nil || document.Nftables == nil {
		return errHoppingNATInspect
	}
	// Propagate the base-chain context through static jumps. Regular chains do
	// not carry a type or hook of their own in nft's JSON representation.
	type chainKey struct{ family, table, chain string }
	type chainInfo struct{ kind, hook string }
	chains := map[chainKey]chainInfo{}
	edges := map[chainKey][]chainKey{}
	natReachable := map[chainKey]bool{}
	filterReachable := map[chainKey]bool{}
	otherReachable := map[chainKey]bool{}
	for _, item := range document.Nftables {
		if raw, ok := item["chain"]; ok {
			var c struct{ Family, Table, Name, Type, Hook string }
			if json.Unmarshal(raw, &c) != nil || c.Family == "" || c.Table == "" || c.Name == "" {
				return errHoppingNATInspect
			}
			key := chainKey{c.Family, c.Table, c.Name}
			chains[key] = chainInfo{c.Type, c.Hook}
			if c.Hook == "prerouting" && c.Type == "nat" {
				natReachable[key] = true
			}
			if c.Hook == "prerouting" && c.Type == "filter" {
				filterReachable[key] = true
			}
			if c.Hook != "" && c.Hook != "prerouting" {
				otherReachable[key] = true
			}
		}
		if raw, ok := item["rule"]; ok {
			var rule struct {
				Family, Table, Chain string
				Expr                 []map[string]json.RawMessage `json:"expr"`
			}
			if json.Unmarshal(raw, &rule) != nil {
				return errHoppingNATInspect
			}
			key := chainKey{rule.Family, rule.Table, rule.Chain}
			for _, expr := range rule.Expr {
				for _, name := range []string{"jump", "goto"} {
					if target, ok := expr[name]; ok {
						var jump struct{ Target string }
						if json.Unmarshal(target, &jump) != nil || jump.Target == "" {
							return errHoppingNATInspect
						}
						edges[key] = append(edges[key], chainKey{rule.Family, rule.Table, jump.Target})
					}
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for from, targets := range edges {
			for _, to := range targets {
				if natReachable[from] && !natReachable[to] {
					natReachable[to] = true
					changed = true
				}
				if filterReachable[from] && !filterReachable[to] {
					filterReachable[to] = true
					changed = true
				}
				if otherReachable[from] && !otherReachable[to] {
					otherReachable[to] = true
					changed = true
				}
			}
		}
	}
	for _, item := range document.Nftables {
		raw, ok := item["rule"]
		if !ok {
			continue
		}
		var rule struct {
			Family, Table, Chain string
			Expr                 []map[string]json.RawMessage `json:"expr"`
		}
		if json.Unmarshal(raw, &rule) != nil || rule.Expr == nil {
			return errHoppingNATInspect
		}
		if rule.Family == "inet" && rule.Table == hy2HoppingTable {
			continue
		}
		var applicable []hy2HoppingRule
		familyMatch := ""
		for _, expr := range rule.Expr {
			if rawMatch, ok := expr["match"]; ok {
				var match struct {
					Op   string `json:"op"`
					Left struct {
						Meta struct{ Key string } `json:"meta"`
					} `json:"left"`
					Right string `json:"right"`
				}
				if json.Unmarshal(rawMatch, &match) == nil && match.Op == "==" && match.Left.Meta.Key == "nfproto" && (match.Right == "ipv4" || match.Right == "ipv6") {
					familyMatch = match.Right
				}
			}
		}
		for _, candidate := range ranges {
			if (rule.Family == "ip" || familyMatch == "ipv4") && candidate.listen == "::" || (rule.Family == "ip6" || familyMatch == "ipv6") && candidate.listen != "::" {
				continue
			}
			applicable = append(applicable, candidate)
		}
		if len(applicable) == 0 {
			continue
		}
		key := chainKey{rule.Family, rule.Table, rule.Chain}
		info, knownChain := chains[key]
		if knownChain && info.hook != "" && info.hook != "prerouting" {
			continue
		}
		if otherReachable[key] && !natReachable[key] && !filterReachable[key] {
			continue
		}
		nat, tcpOnly, unknownPort := false, false, false
		possible := make([][2]int, 0, len(applicable))
		for _, r := range applicable {
			possible = append(possible, [2]int{r.start, r.end})
		}
		for _, expr := range rule.Expr {
			if raw, ok := expr["dnat"]; ok {
				nat = true
				var value map[string]json.RawMessage
				if json.Unmarshal(raw, &value) != nil {
					unknownPort = true
				}
			}
			if raw, ok := expr["redirect"]; ok {
				nat = true
				var value map[string]json.RawMessage
				if json.Unmarshal(raw, &value) != nil {
					unknownPort = true
				}
			}
			if _, ok := expr["vmap"]; ok {
				// A vmap in a filter-only path is an ordinary verdict map.
				// A NAT-reachable or unclassified map may jump to NAT rules.
				if natReachable[key] || (!filterReachable[key] && (!knownChain || info.kind != "filter")) {
					nat = true
					unknownPort = true
				}
			}
			// Compatibility targets and transparent proxying may redirect
			// packets without exposing a directly inspectable NAT statement.
			if _, ok := expr["xt"]; ok {
				nat = true
				unknownPort = true
			}
			if _, ok := expr["tproxy"]; ok {
				nat = true
			}
			if rawMatch, ok := expr["match"]; ok {
				var match struct {
					Op   string `json:"op"`
					Left struct {
						Payload struct{ Protocol, Field string } `json:"payload"`
						Meta    struct{ Key string }             `json:"meta"`
					} `json:"left"`
					Right json.RawMessage `json:"right"`
				}
				if json.Unmarshal(rawMatch, &match) != nil {
					unknownPort = true
					continue
				}
				if match.Left.Payload.Field == "dport" && match.Left.Payload.Protocol != "udp" && match.Left.Payload.Protocol != "tcp" {
					unknownPort = true
				}
				if match.Op != "==" {
					if match.Left.Payload.Protocol == "udp" && match.Left.Payload.Field == "dport" {
						unknownPort = true
					}
					continue
				}
				if match.Left.Payload.Protocol == "tcp" {
					tcpOnly = true
				}
				if match.Left.Meta.Key == "l4proto" && string(match.Right) == `"tcp"` {
					tcpOnly = true
				}
				if match.Left.Payload.Protocol == "udp" && match.Left.Payload.Field == "dport" {
					if ports, ok := nftPortIntervals(match.Right); ok {
						var intersection [][2]int
						for _, p := range ports {
							for _, r := range possible {
								lo, hi := max(p[0], r[0]), min(p[1], r[1])
								if lo <= hi {
									intersection = append(intersection, [2]int{lo, hi})
								}
							}
						}
						possible = intersection
					} else {
						unknownPort = true
					}
				}
			}
		}
		if nat && !tcpOnly && (unknownPort || len(possible) > 0) {
			return errHoppingNATConflict
		}
	}
	return nil
}

func nftPortIntervals(raw json.RawMessage) ([][2]int, bool) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, false
	}
	var parse func(any) ([][2]int, bool)
	parse = func(v any) ([][2]int, bool) {
		switch x := v.(type) {
		case float64:
			if x < 1 || x > 65535 || x != float64(int(x)) {
				return nil, false
			}
			return [][2]int{{int(x), int(x)}}, true
		case map[string]any:
			if seq, ok := x["range"].([]any); ok && len(seq) == 2 {
				a, aok := seq[0].(float64)
				b, bok := seq[1].(float64)
				if aok && bok && a >= 1 && b <= 65535 && a <= b && a == float64(int(a)) && b == float64(int(b)) {
					return [][2]int{{int(a), int(b)}}, true
				}
				return nil, false
			}
			if seq, ok := x["set"].([]any); ok {
				var result [][2]int
				for _, item := range seq {
					values, valid := parse(item)
					if !valid {
						return nil, false
					}
					result = append(result, values...)
				}
				return result, true
			}
		}
		return nil, false
	}
	return parse(value)
}
