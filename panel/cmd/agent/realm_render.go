package main

import (
	"bytes"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

func renderManagedRealmConfig(relays []desiredRelay) ([]byte, error) {
	return renderRealmConfig(relays, false)
}

func renderManagedRealmValidationConfig(relays []desiredRelay) ([]byte, error) {
	return renderRealmConfig(relays, true)
}

func renderRealmConfig(relays []desiredRelay, validation bool) ([]byte, error) {
	values := append([]desiredRelay(nil), relays...)
	sort.Slice(values, func(left, right int) bool { return values[left].ID < values[right].ID })
	var output bytes.Buffer
	output.WriteString("[log]\nlevel = \"warn\"\noutput = \"stdout\"\n")
	for _, relay := range values {
		if relay.ID <= 0 || net.ParseIP(relay.ListenAddress) == nil || relay.ListenPort < 1 || relay.ListenPort > 65535 ||
			relay.TargetPort < 1 || relay.TargetPort > 65535 || !validRealmHost(relay.TargetHost) {
			return nil, errUnsupportedManagedConfig
		}
		noTCP, useUDP, err := realmNetworkOptions(relay.Network)
		if err != nil {
			return nil, err
		}
		listenHost, listenPort := relay.ListenAddress, relay.ListenPort
		if validation {
			listenHost, listenPort = "127.0.0.1", 0
		}
		output.WriteString("\n[[endpoints]]\n")
		output.WriteString("# relay_id = " + strconv.FormatInt(relay.ID, 10) + "\n")
		output.WriteString("listen = " + strconv.Quote(net.JoinHostPort(listenHost, strconv.Itoa(listenPort))) + "\n")
		output.WriteString("remote = " + strconv.Quote(net.JoinHostPort(relay.TargetHost, strconv.Itoa(relay.TargetPort))) + "\n")
		output.WriteString("[endpoints.network]\n")
		output.WriteString(fmt.Sprintf("no_tcp = %t\nuse_udp = %t\n", noTCP, useUDP))
	}
	return output.Bytes(), nil
}

func realmNetworkOptions(network string) (bool, bool, error) {
	switch network {
	case "tcp":
		return false, false, nil
	case "udp":
		return true, true, nil
	case "tcp,udp":
		return false, true, nil
	default:
		return false, false, errUnsupportedManagedConfig
	}
}

func validRealmHost(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/?#@") || strings.Contains(value, "://") {
		return false
	}
	if net.ParseIP(strings.Trim(value, "[]")) != nil {
		return true
	}
	if strings.Contains(value, ":") || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func parseRenderedRealmState(value []byte) ([]realmListener, []firewallRule, bool) {
	if len(value) == 0 {
		return nil, nil, false
	}
	lines := strings.Split(string(value), "\n")
	listeners := make([]realmListener, 0)
	var current *realmListener
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "[[endpoints]]":
			listeners = append(listeners, realmListener{})
			current = &listeners[len(listeners)-1]
		case current != nil && strings.HasPrefix(line, "listen = "):
			quoted := strings.TrimSpace(strings.TrimPrefix(line, "listen = "))
			address, err := strconv.Unquote(quoted)
			if err != nil {
				return nil, nil, false
			}
			host, portValue, err := net.SplitHostPort(address)
			port, portErr := strconv.Atoi(portValue)
			if err != nil || portErr != nil || net.ParseIP(host) == nil || port < 1 || port > 65535 {
				return nil, nil, false
			}
			current.address, current.port = host, port
		case current != nil && line == "no_tcp = true":
			current.tcp = false
		case current != nil && line == "no_tcp = false":
			current.tcp = true
		case current != nil && line == "use_udp = true":
			current.udp = true
		case current != nil && line == "use_udp = false":
			current.udp = false
		}
	}
	if len(listeners) == 0 {
		return []realmListener{}, []firewallRule{}, true
	}
	rules := make([]firewallRule, 0, len(listeners)*2)
	for _, listener := range listeners {
		if listener.address == "" || listener.port == 0 || (!listener.tcp && !listener.udp) {
			return nil, nil, false
		}
		if listener.tcp {
			rules = append(rules, firewallRule{port: listener.port, protocol: "tcp"})
		}
		if listener.udp {
			rules = append(rules, firewallRule{port: listener.port, protocol: "udp"})
		}
	}
	return listeners, rules, true
}
