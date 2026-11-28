package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var errHoppingLegacyInspect = fmt.Errorf("%w: cannot inspect iptables-legacy NAT rules safely", errHoppingConflict)
var errHoppingLegacyConflict = fmt.Errorf("%w: iptables-legacy NAT rule may redirect UDP in the hopping range", errHoppingConflict)

// Inspect both explicit legacy tools and the selected alternative. We never
// invoke mutating iptables operations. Missing tools are safe only when the
// legacy kernel table list is absent or does not contain a NAT table.
func inspectLegacyNAT(ctx context.Context, rules []hy2HoppingRule, lookup func(string) (string, error), run func(context.Context, string, ...string) ([]byte, error), read func(string) ([]byte, error)) error {
	for _, family := range []struct {
		v6           bool
		prefix, proc string
	}{{false, "iptables", "/proc/net/ip_tables_names"}, {true, "ip6tables", "/proc/net/ip6_tables_names"}} {
		var relevant []hy2HoppingRule
		for _, r := range rules {
			if (r.listen == "::") == family.v6 {
				relevant = append(relevant, r)
			}
		}
		if len(relevant) == 0 {
			continue
		}
		save, saveErr := lookup(family.prefix + "-legacy-save")
		legacy, legacyErr := lookup(family.prefix + "-legacy")
		var command string
		var args []string
		if saveErr == nil {
			command, args = save, []string{"-t", "nat"}
		} else if legacyErr == nil {
			command, args = legacy, []string{"-t", "nat", "-S"}
		}
		if command == "" {
			if generic, err := lookup(family.prefix); err == nil {
				probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				version, err := run(probeCtx, generic, "--version")
				cancel()
				if err != nil {
					return errHoppingLegacyInspect
				}
				versionText := strings.ToLower(string(version))
				if strings.Contains(versionText, "legacy") {
					// The save alternative may point to a different backend.
					command, args = generic, []string{"-t", "nat", "-S"}
				} else if !strings.Contains(versionText, "nf_tables") {
					return errHoppingLegacyInspect
				}
			}
		}
		if command == "" {
			data, err := read(family.proc)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return errHoppingLegacyInspect
			}
			for _, name := range strings.Fields(string(data)) {
				if name == "nat" {
					return errHoppingLegacyInspect
				}
			}
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		data, err := run(probeCtx, command, args...)
		cancel()
		if err != nil || len(data) == 0 || len(data) >= managedProxyFirewallOutputMax {
			return errHoppingLegacyInspect
		}
		if len(args) == 2 && (!strings.Contains(string(data), "*nat\n") || !strings.Contains(string(data), "\nCOMMIT")) {
			return errHoppingLegacyInspect
		}
		if err := checkLegacyNATRules(data, relevant); err != nil {
			return err
		}
	}
	return nil
}

func checkLegacyNATRules(data []byte, rules []hy2HoppingRule) error {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, ":") || line == "COMMIT" || strings.HasPrefix(line, "-N ") || strings.HasPrefix(line, "-P ") {
			continue
		}
		fields, ok := legacyFields(line)
		if !ok || len(fields) < 2 || fields[0] != "-A" {
			return errHoppingLegacyInspect
		}
		var target string
		tcpOnly, unknown := false, false
		possible := make([][2]int, 0, len(rules))
		for _, r := range rules {
			possible = append(possible, [2]int{r.start, r.end})
		}
		for i := 2; i < len(fields); i++ {
			token := fields[i]
			if token == "!" {
				unknown = true
				continue
			}
			if token == "-j" || token == "--jump" || token == "-g" || token == "--goto" {
				if i+1 >= len(fields) {
					return errHoppingLegacyInspect
				}
				i++
				target = fields[i]
				continue
			}
			if (token == "-p" || token == "--protocol") && i+1 < len(fields) {
				i++
				if fields[i] == "tcp" && (i < 2 || fields[i-2] != "!") {
					tcpOnly = true
				}
				continue
			}
			if token == "--match-set" || token == "--set" {
				unknown = true
			}
			if token == "-m" || token == "--match" {
				if i+1 >= len(fields) {
					return errHoppingLegacyInspect
				}
				switch fields[i+1] {
				case "tcp", "udp", "multiport", "comment", "conntrack", "state":
				default:
					unknown = true
				}
			}
			if token == "--dport" || token == "--destination-port" || token == "--dports" || token == "--destination-ports" {
				if i+1 >= len(fields) {
					return errHoppingLegacyInspect
				}
				i++
				var ports [][2]int
				for _, part := range strings.Split(fields[i], ",") {
					loText, hiText, hasRange := strings.Cut(part, ":")
					if !hasRange {
						hiText = loText
					}
					lo, e1 := strconv.Atoi(loText)
					hi, e2 := strconv.Atoi(hiText)
					if e1 != nil || e2 != nil || lo < 1 || hi > 65535 || lo > hi {
						unknown = true
						continue
					}
					ports = append(ports, [2]int{lo, hi})
				}
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
			}
		}
		// User-defined chains are inspected separately. Unknown extension
		// targets are conservatively treated as potential NAT operations.
		switch strings.ToUpper(target) {
		case "DNAT", "REDIRECT", "NETMAP", "TPROXY":
			if !tcpOnly && (unknown || len(possible) > 0) {
				return errHoppingLegacyConflict
			}
		case "", "ACCEPT", "DROP", "RETURN", "SNAT", "MASQUERADE", "LOG", "MARK", "CONNMARK":
		default:
			// Chain names occur in save output as declarations; unresolved
			// extension targets cannot safely be assumed harmless.
			declared := strings.Contains("\n"+string(data), "\n:"+target+" ") || strings.Contains("\n"+string(data), "\n-N "+target+"\n")
			if !declared && !tcpOnly && (unknown || len(possible) > 0) {
				return errHoppingLegacyInspect
			}
		}
	}
	return nil
}

func legacyFields(line string) ([]string, bool) {
	var out []string
	var word strings.Builder
	var quote rune
	escaped, active := false, false
	for _, ch := range line {
		if escaped {
			word.WriteRune(ch)
			escaped = false
			active = true
			continue
		}
		if ch == '\\' {
			escaped = true
			active = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				word.WriteRune(ch)
			}
			active = true
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			active = true
			continue
		}
		if ch == ' ' || ch == '\t' {
			if active {
				out = append(out, word.String())
				word.Reset()
				active = false
			}
			continue
		}
		word.WriteRune(ch)
		active = true
	}
	if escaped || quote != 0 {
		return nil, false
	}
	if active {
		out = append(out, word.String())
	}
	return out, true
}
