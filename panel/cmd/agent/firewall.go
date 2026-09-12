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

const managedProxyFirewallComment = "vps-panel-proxy-tcp"

const managedProxyFirewallOutputMax = 64 << 10

var errManagedProxyFirewall = errors.New("managed proxy firewall synchronization failed")

type proxyFirewall struct {
	lookPath   func(string) (string, error)
	runCommand func(context.Context, string, ...string) ([]byte, error)
}

func newProxyFirewall() *proxyFirewall {
	return &proxyFirewall{lookPath: exec.LookPath, runCommand: runFirewallCommand}
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
	desired, err := normalizeFirewallPorts(desiredPorts)
	if err != nil {
		return err
	}
	if command, ok := f.activeCommand(ctx, "ufw", []string{"status"}, "status: active"); ok {
		return f.reconcileUFW(ctx, command, desired)
	}
	if command, ok := f.activeCommand(ctx, "firewall-cmd", []string{"--state"}, "running"); ok {
		return f.reconcileFirewalld(ctx, command, desired)
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

func (f *proxyFirewall) activeCommand(ctx context.Context, name string, arguments []string, activeText string) (string, bool) {
	command, err := f.lookPath(name)
	if err != nil {
		return "", false
	}
	output, err := f.runCommand(ctx, command, arguments...)
	return command, err == nil && strings.Contains(strings.ToLower(string(output)), activeText)
}

func (f *proxyFirewall) reconcileUFW(ctx context.Context, command string, desired map[int]struct{}) error {
	output, err := f.runCommand(ctx, command, "status", "numbered")
	if err != nil {
		return firewallError("read ufw rules", err)
	}
	existing, numbered := parseUFWManagedRules(string(output))
	for _, port := range missingFirewallPorts(desired, existing) {
		if _, err := f.runCommand(ctx, command, "allow", strconv.Itoa(port)+"/tcp", "comment", managedProxyFirewallComment); err != nil {
			return firewallError("add ufw rule", err)
		}
	}
	numbers := make([]int, 0)
	for _, rule := range numbered {
		if _, keep := desired[rule.port]; !keep {
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

func (f *proxyFirewall) reconcileFirewalld(ctx context.Context, command string, desired map[int]struct{}) error {
	output, err := f.runCommand(ctx, command, "--direct", "--get-rules", "ipv4", "filter", "INPUT")
	if err != nil {
		return firewallError("read firewalld rules", err)
	}
	existing := parseManagedRulePorts(string(output))
	for _, port := range missingFirewallPorts(desired, existing) {
		arguments := append([]string{"--direct", "--add-rule", "ipv4", "filter", "INPUT", "0"}, managedIPTablesRule(port)...)
		if _, err := f.runCommand(ctx, command, arguments...); err != nil {
			return firewallError("add firewalld rule", err)
		}
	}
	for _, port := range staleFirewallPorts(desired, existing) {
		arguments := append([]string{"--direct", "--remove-rule", "ipv4", "filter", "INPUT", "0"}, managedIPTablesRule(port)...)
		if _, err := f.runCommand(ctx, command, arguments...); err != nil {
			return firewallError("remove stale firewalld rule", err)
		}
	}
	return nil
}

func (f *proxyFirewall) reconcileIPTables(ctx context.Context, command string, desired map[int]struct{}, rules string) error {
	existing := parseManagedRulePorts(rules)
	for _, port := range missingFirewallPorts(desired, existing) {
		arguments := append([]string{"-I", "INPUT", "1"}, managedIPTablesRule(port)...)
		if _, err := f.runCommand(ctx, command, arguments...); err != nil {
			return firewallError("add iptables rule", err)
		}
	}
	for _, port := range staleFirewallPorts(desired, existing) {
		arguments := append([]string{"-D", "INPUT"}, managedIPTablesRule(port)...)
		if _, err := f.runCommand(ctx, command, arguments...); err != nil {
			return firewallError("remove stale iptables rule", err)
		}
	}
	return nil
}

func normalizeFirewallPorts(ports []int) (map[int]struct{}, error) {
	result := make(map[int]struct{}, len(ports))
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, firewallError("invalid TCP port", nil)
		}
		result[port] = struct{}{}
	}
	return result, nil
}

func managedIPTablesRule(port int) []string {
	return []string{"-p", "tcp", "--dport", strconv.Itoa(port), "-m", "comment", "--comment", managedProxyFirewallComment, "-j", "ACCEPT"}
}

func parseManagedRulePorts(value string) map[int]struct{} {
	ports := make(map[int]struct{})
	scanner := bufio.NewScanner(strings.NewReader(value))
	for scanner.Scan() {
		fields := strings.Fields(strings.ReplaceAll(scanner.Text(), `"`, ""))
		if !containsFields(fields, "-p", "tcp") || !containsFields(fields, "--comment", managedProxyFirewallComment) || !containsFields(fields, "-j", "ACCEPT") {
			continue
		}
		if port, ok := fieldAfter(fields, "--dport"); ok {
			value, err := strconv.Atoi(port)
			if err == nil && value >= 1 && value <= 65535 {
				ports[value] = struct{}{}
			}
		}
	}
	return ports
}

type numberedFirewallRule struct {
	number int
	port   int
}

func parseUFWManagedRules(value string) (map[int]struct{}, []numberedFirewallRule) {
	ports := make(map[int]struct{})
	rules := make([]numberedFirewallRule, 0)
	scanner := bufio.NewScanner(strings.NewReader(value))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, "# "+managedProxyFirewallComment) || !strings.HasPrefix(line, "[") {
			continue
		}
		closing := strings.IndexByte(line, ']')
		if closing < 2 {
			continue
		}
		number, numberErr := strconv.Atoi(strings.TrimSpace(line[1:closing]))
		fields := strings.Fields(strings.TrimSpace(line[closing+1:]))
		if numberErr != nil || len(fields) == 0 || !strings.HasSuffix(fields[0], "/tcp") {
			continue
		}
		port, portErr := strconv.Atoi(strings.TrimSuffix(fields[0], "/tcp"))
		if portErr != nil || port < 1 || port > 65535 {
			continue
		}
		ports[port] = struct{}{}
		rules = append(rules, numberedFirewallRule{number: number, port: port})
	}
	return ports, rules
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

func missingFirewallPorts(desired, existing map[int]struct{}) []int {
	ports := make([]int, 0)
	for port := range desired {
		if _, exists := existing[port]; !exists {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports
}

func staleFirewallPorts(desired, existing map[int]struct{}) []int {
	ports := make([]int, 0)
	for port := range existing {
		if _, desired := desired[port]; !desired {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports
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
