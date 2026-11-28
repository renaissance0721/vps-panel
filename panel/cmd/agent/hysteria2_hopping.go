package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	hy2HoppingTable  = "vps_panel_hy2_hopping"
	hy2HoppingMarker = "vps-panel-hy2-hopping-owner"
)

var (
	errHoppingUnsupported = errors.New("Hysteria2 port hopping requires nftables and CAP_NET_ADMIN with inet NAT support")
	errHoppingConflict    = errors.New("Hysteria2 port hopping conflicts with an existing UDP listener or unmanaged nftables table")
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
	journalPath string
}

func newHY2HoppingManager() *hy2HoppingManager {
	return &hy2HoppingManager{lookPath: exec.LookPath, runCommand: runFirewallCommand, checkUDP: checkHY2HoppingUDPListeners, journalPath: "/var/lib/vps-panel/agent/hy2-hopping-owned"}
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
			settings.PortHoppingStart < 1 || settings.PortHoppingEnd > 65535 ||
			settings.PortHoppingStart > settings.PortHoppingEnd ||
			(proxy.Port >= settings.PortHoppingStart && proxy.Port <= settings.PortHoppingEnd) {
			return nil, errUnsupportedManagedConfig
		}
		rules = append(rules, hy2HoppingRule{id: proxy.ID, listen: proxy.Listen, port: proxy.Port, start: settings.PortHoppingStart, end: settings.PortHoppingEnd})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].start < rules[j].start })
	for i, rule := range rules {
		for _, proxy := range proxies {
			if proxy.Port >= rule.start && proxy.Port <= rule.end {
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
