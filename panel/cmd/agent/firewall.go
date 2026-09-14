package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const (
	managedProxyFirewallTCPComment = "vps-panel-proxy-tcp"
	managedProxyFirewallUDPComment = "vps-panel-proxy-udp"
	managedRealmFirewallTCPComment = "vps-panel-realm-tcp"
	managedRealmFirewallUDPComment = "vps-panel-realm-udp"
	managedProxyFirewallOutputMax  = 64 << 10
)

var errManagedProxyFirewall = errors.New("managed proxy firewall synchronization failed")

type firewallRule struct {
	port     int
	protocol string
}

type proxyFirewall struct {
	owner      string
	lookPath   func(string) (string, error)
	runCommand func(context.Context, string, ...string) ([]byte, error)
}

func newProxyFirewall() *proxyFirewall {
	return &proxyFirewall{owner: "proxy", lookPath: exec.LookPath, runCommand: runFirewallCommand}
}

func newRealmFirewall() *proxyFirewall {
	return &proxyFirewall{owner: "realm", lookPath: exec.LookPath, runCommand: runFirewallCommand}
}

func runFirewallCommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output := &cappedBuffer{limit: managedProxyFirewallOutputMax}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	return output.Bytes(), err
}

func (f *proxyFirewall) reconcile(ctx context.Context, desiredPorts []int) error {
	rules := make([]firewallRule, 0, len(desiredPorts))
	for _, port := range desiredPorts {
		rules = append(rules, firewallRule{port: port, protocol: "tcp"})
	}
	return f.reconcileRules(ctx, rules)
}

func (f *proxyFirewall) reconcileRules(ctx context.Context, desiredRules []firewallRule) error {
	desired, err := normalizeFirewallRules(desiredRules)
	if err != nil {
		return err
	}
	if command, ok := f.activeCommand(ctx, "ufw", []string{"status"}, "status: active"); ok {
		return f.reconcileUFW(ctx, command, desired)
	}
	if command, ok := f.activeCommand(ctx, "firewall-cmd", []string{"--state"}, "running"); ok {
		return f.reconcileFirewalld(ctx, command, desired)
	}
	if command, ok := f.activeNFT(ctx); ok {
		return f.reconcileNFT(ctx, command, desired)
	}
	command, err := f.lookPath("iptables")
	if err != nil {
		return nil
	}
	output, err := f.runCommand(ctx, command, "-S", "INPUT")
	if err != nil || !iptablesIsActive(string(output)) {
		return nil
	}
	return f.reconcileIPTables(ctx, command, desired, string(output))
}

func (f *proxyFirewall) activeNFT(ctx context.Context) (string, bool) {
	command, err := f.lookPath("nft")
	if err != nil {
		return "", false
	}
	output, err := f.runCommand(ctx, command, "list", "ruleset")
	return command, err == nil && strings.Contains(strings.ToLower(string(output)), "table ")
}

func (f *proxyFirewall) reconcileNFT(ctx context.Context, command string, desired map[firewallRule]struct{}) error {
	table := "vps_panel_" + f.owner
	output, err := f.runCommand(ctx, command, "-a", "list", "chain", "inet", table, "input")
	if err != nil {
		if len(desired) == 0 {
			return nil
		}
		if _, tableErr := f.runCommand(ctx, command, "list", "table", "inet", table); tableErr == nil {
			return firewallError("refuse unmanaged nftables table "+table, nil)
		}
		if _, err := f.runCommand(ctx, command, "add", "table", "inet", table); err != nil {
			return firewallError("create nftables table", err)
		}
		if _, err := f.runCommand(ctx, command, "add", "chain", "inet", table, "input", "{", "type", "filter", "hook", "input", "priority", "-10", ";", "policy", "accept", ";", "}"); err != nil {
			_, _ = f.runCommand(ctx, command, "delete", "table", "inet", table)
			return firewallError("create nftables chain", err)
		}
		if _, err := f.runCommand(ctx, command, "add", "rule", "inet", table, "input", "counter", "comment", f.nftOwnerComment()); err != nil {
			_, _ = f.runCommand(ctx, command, "delete", "table", "inet", table)
			return firewallError("mark nftables ownership", err)
		}
		output = []byte("counter comment \"" + f.nftOwnerComment() + "\" # handle 1")
	}
	if !f.hasNFTOwnership(string(output)) {
		return firewallError("refuse unmanaged nftables chain "+table+" input", nil)
	}
	existing := f.parseNFTManagedRules(string(output))
	for _, rule := range missingFirewallRules(desired, nftRuleSet(existing)) {
		if _, err := f.runCommand(ctx, command, "add", "rule", "inet", table, "input", rule.protocol, "dport", strconv.Itoa(rule.port), "accept", "comment", f.firewallComment(rule.protocol)); err != nil {
			return firewallError("add nftables rule", err)
		}
	}
	for _, rule := range staleFirewallRules(desired, nftRuleSet(existing)) {
		if _, err := f.runCommand(ctx, command, "delete", "rule", "inet", table, "input", "handle", strconv.FormatUint(existing[rule], 10)); err != nil {
			return firewallError("remove stale nftables rule", err)
		}
	}
	return nil
}

func (f *proxyFirewall) nftOwnerComment() string {
	return "vps-panel-" + f.owner + "-owner"
}

func (f *proxyFirewall) hasNFTOwnership(value string) bool {
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(strings.ReplaceAll(line, `"`, ""))
		comment, ok := fieldAfter(fields, "comment")
		if ok && comment == f.nftOwnerComment() {
			return true
		}
	}
	return false
}

func (f *proxyFirewall) parseNFTManagedRules(value string) map[firewallRule]uint64 {
	rules := make(map[firewallRule]uint64)
	scanner := bufio.NewScanner(strings.NewReader(value))
	for scanner.Scan() {
		fields := strings.Fields(strings.ReplaceAll(scanner.Text(), `"`, ""))
		comment, commentOK := fieldAfter(fields, "comment")
		handleValue, handleOK := fieldAfter(fields, "handle")
		if !commentOK || !handleOK {
			continue
		}
		handle, err := strconv.ParseUint(handleValue, 10, 64)
		if err != nil {
			continue
		}
		for index := 0; index+2 < len(fields); index++ {
			protocol := fields[index]
			if (protocol != "tcp" && protocol != "udp") || fields[index+1] != "dport" || comment != f.firewallComment(protocol) {
				continue
			}
			port, err := strconv.Atoi(fields[index+2])
			if err == nil && port >= 1 && port <= 65535 {
				rules[firewallRule{port: port, protocol: protocol}] = handle
			}
			break
		}
	}
	return rules
}

func nftRuleSet(rules map[firewallRule]uint64) map[firewallRule]struct{} {
	result := make(map[firewallRule]struct{}, len(rules))
	for rule := range rules {
		result[rule] = struct{}{}
	}
	return result
}

func (f *proxyFirewall) activeCommand(ctx context.Context, name string, arguments []string, activeText string) (string, bool) {
	command, err := f.lookPath(name)
	if err != nil {
		return "", false
	}
	output, err := f.runCommand(ctx, command, arguments...)
	return command, err == nil && strings.Contains(strings.ToLower(string(output)), activeText)
}

func (f *proxyFirewall) reconcileUFW(ctx context.Context, command string, desired map[firewallRule]struct{}) error {
	output, err := f.runCommand(ctx, command, "status", "numbered")
	if err != nil {
		return firewallError("read ufw rules", err)
	}
	existing, numbered := f.parseUFWManagedRules(string(output))
	for _, rule := range missingFirewallRules(desired, existing) {
		if _, err := f.runCommand(ctx, command, "allow", strconv.Itoa(rule.port)+"/"+rule.protocol, "comment", f.firewallComment(rule.protocol)); err != nil {
			return firewallError("add ufw rule", err)
		}
	}
	numbers := make([]int, 0)
	for _, rule := range numbered {
		if _, keep := desired[rule.rule]; !keep {
			numbers = append(numbers, rule.number)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(numbers)))
	for _, number := range numbers {
		if _, err := f.runCommand(ctx, command, "--force", "delete", strconv.Itoa(number)); err != nil {
			return firewallError("remove stale ufw rule", err)
		}
	}
	return nil
}

func (f *proxyFirewall) reconcileFirewalld(ctx context.Context, command string, desired map[firewallRule]struct{}) error {
	output, err := f.runCommand(ctx, command, "--direct", "--get-rules", "ipv4", "filter", "INPUT")
	if err != nil {
		return firewallError("read firewalld rules", err)
	}
	existing := f.parseManagedFirewallRules(string(output))
	for _, rule := range missingFirewallRules(desired, existing) {
		arguments := append([]string{"--direct", "--add-rule", "ipv4", "filter", "INPUT", "0"}, f.managedIPTablesRule(rule)...)
		if _, err := f.runCommand(ctx, command, arguments...); err != nil {
			return firewallError("add firewalld rule", err)
		}
	}
	for _, rule := range staleFirewallRules(desired, existing) {
		arguments := append([]string{"--direct", "--remove-rule", "ipv4", "filter", "INPUT", "0"}, f.managedIPTablesRule(rule)...)
		if _, err := f.runCommand(ctx, command, arguments...); err != nil {
			return firewallError("remove stale firewalld rule", err)
		}
	}
	return nil
}

func (f *proxyFirewall) reconcileIPTables(ctx context.Context, command string, desired map[firewallRule]struct{}, rules string) error {
	existing := f.parseManagedFirewallRules(rules)
	for _, rule := range missingFirewallRules(desired, existing) {
		arguments := append([]string{"-I", "INPUT", "1"}, f.managedIPTablesRule(rule)...)
		if _, err := f.runCommand(ctx, command, arguments...); err != nil {
			return firewallError("add iptables rule", err)
		}
	}
	for _, rule := range staleFirewallRules(desired, existing) {
		arguments := append([]string{"-D", "INPUT"}, f.managedIPTablesRule(rule)...)
		if _, err := f.runCommand(ctx, command, arguments...); err != nil {
			return firewallError("remove stale iptables rule", err)
		}
	}
	return nil
}

func normalizeFirewallRules(rules []firewallRule) (map[firewallRule]struct{}, error) {
	result := make(map[firewallRule]struct{}, len(rules))
	for _, rule := range rules {
		if rule.port < 1 || rule.port > 65535 || (rule.protocol != "tcp" && rule.protocol != "udp") {
			return nil, firewallError("invalid proxy firewall rule", nil)
		}
		result[rule] = struct{}{}
	}
	return result, nil
}

func firewallComment(protocol string) string {
	if protocol == "udp" {
		return managedProxyFirewallUDPComment
	}
	return managedProxyFirewallTCPComment
}

func (f *proxyFirewall) firewallComment(protocol string) string {
	if f.owner == "realm" {
		if protocol == "udp" {
			return managedRealmFirewallUDPComment
		}
		return managedRealmFirewallTCPComment
	}
	return firewallComment(protocol)
}

func managedIPTablesRule(rule firewallRule) []string {
	return []string{"-p", rule.protocol, "--dport", strconv.Itoa(rule.port), "-m", "comment", "--comment", firewallComment(rule.protocol), "-j", "ACCEPT"}
}

func (f *proxyFirewall) managedIPTablesRule(rule firewallRule) []string {
	return []string{"-p", rule.protocol, "--dport", strconv.Itoa(rule.port), "-m", "comment", "--comment", f.firewallComment(rule.protocol), "-j", "ACCEPT"}
}

func parseManagedFirewallRules(value string) map[firewallRule]struct{} {
	return (&proxyFirewall{owner: "proxy"}).parseManagedFirewallRules(value)
}

func (f *proxyFirewall) parseManagedFirewallRules(value string) map[firewallRule]struct{} {
	rules := make(map[firewallRule]struct{})
	scanner := bufio.NewScanner(strings.NewReader(value))
	for scanner.Scan() {
		fields := strings.Fields(strings.ReplaceAll(scanner.Text(), `"`, ""))
		protocol, protocolOK := fieldAfter(fields, "-p")
		comment, commentOK := fieldAfter(fields, "--comment")
		if !protocolOK || !commentOK || comment != f.firewallComment(protocol) || !containsFields(fields, "-j", "ACCEPT") {
			continue
		}
		portValue, ok := fieldAfter(fields, "--dport")
		port, err := strconv.Atoi(portValue)
		if ok && err == nil && port >= 1 && port <= 65535 {
			rules[firewallRule{port: port, protocol: protocol}] = struct{}{}
		}
	}
	return rules
}

type numberedFirewallRule struct {
	number int
	rule   firewallRule
}

func parseUFWManagedRules(value string) (map[firewallRule]struct{}, []numberedFirewallRule) {
	return (&proxyFirewall{owner: "proxy"}).parseUFWManagedRules(value)
}

func (f *proxyFirewall) parseUFWManagedRules(value string) (map[firewallRule]struct{}, []numberedFirewallRule) {
	rules := make(map[firewallRule]struct{})
	numbered := make([]numberedFirewallRule, 0)
	scanner := bufio.NewScanner(strings.NewReader(value))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "[") {
			continue
		}
		closing := strings.IndexByte(line, ']')
		if closing < 2 {
			continue
		}
		number, numberErr := strconv.Atoi(strings.TrimSpace(line[1:closing]))
		fields := strings.Fields(strings.TrimSpace(line[closing+1:]))
		if numberErr != nil || len(fields) == 0 {
			continue
		}
		portProtocol := strings.Split(fields[0], "/")
		if len(portProtocol) != 2 {
			continue
		}
		port, portErr := strconv.Atoi(portProtocol[0])
		rule := firewallRule{port: port, protocol: portProtocol[1]}
		if portErr != nil || port < 1 || port > 65535 || !strings.Contains(line, "# "+f.firewallComment(rule.protocol)) {
			continue
		}
		rules[rule] = struct{}{}
		numbered = append(numbered, numberedFirewallRule{number: number, rule: rule})
	}
	return rules, numbered
}

func iptablesIsActive(value string) bool {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "-A INPUT ") || strings.HasPrefix(line, "-P INPUT DROP") || strings.HasPrefix(line, "-P INPUT REJECT") {
			return true
		}
	}
	return false
}

func missingFirewallRules(desired, existing map[firewallRule]struct{}) []firewallRule {
	rules := make([]firewallRule, 0)
	for rule := range desired {
		if _, exists := existing[rule]; !exists {
			rules = append(rules, rule)
		}
	}
	sortFirewallRules(rules)
	return rules
}

func staleFirewallRules(desired, existing map[firewallRule]struct{}) []firewallRule {
	rules := make([]firewallRule, 0)
	for rule := range existing {
		if _, desired := desired[rule]; !desired {
			rules = append(rules, rule)
		}
	}
	sortFirewallRules(rules)
	return rules
}

func sortFirewallRules(rules []firewallRule) {
	sort.Slice(rules, func(left, right int) bool {
		if rules[left].port == rules[right].port {
			return rules[left].protocol < rules[right].protocol
		}
		return rules[left].port < rules[right].port
	})
}

func containsFields(fields []string, key, value string) bool {
	got, ok := fieldAfter(fields, key)
	return ok && got == value
}

func fieldAfter(fields []string, key string) (string, bool) {
	for index := 0; index+1 < len(fields); index++ {
		if fields[index] == key {
			return fields[index+1], true
		}
	}
	return "", false
}

func firewallError(action string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", errManagedProxyFirewall, action)
	}
	return fmt.Errorf("%w: %s: %v", errManagedProxyFirewall, action, err)
}
