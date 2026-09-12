package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	maxReportedIPAddresses   = 16
	publicIPv4Endpoint       = "https://api.ipify.org"
	publicIPv4ResponseLimit  = 64
	publicIPv4RequestTimeout = 5 * time.Second
)

type systemInfoMessage struct {
	Type       string   `json:"type"`
	Hostname   string   `json:"hostname"`
	OSName     string   `json:"os_name"`
	OSVersion  string   `json:"os_version"`
	Kernel     string   `json:"kernel"`
	Arch       string   `json:"arch"`
	IPv4       []string `json:"ipv4"`
	IPv6       []string `json:"ipv6"`
	PublicIPv4 string   `json:"public_ipv4"`
}

func collectSystemInfo() systemInfoMessage {
	return collectSystemInfoWith(os.Hostname, os.ReadFile, net.InterfaceAddrs)
}

func collectSystemInfoWith(
	hostname func() (string, error),
	readFile func(string) ([]byte, error),
	interfaceAddrs func() ([]net.Addr, error),
) systemInfoMessage {
	message := systemInfoMessage{
		Type:   "system_info",
		OSName: "Linux",
		Arch:   runtime.GOARCH,
		IPv4:   []string{},
		IPv6:   []string{},
	}
	if value, err := hostname(); err == nil {
		message.Hostname = strings.TrimSpace(value)
	}
	if data, err := readFile("/etc/os-release"); err == nil {
		if name, version := parseOSRelease(data); name != "" || version != "" {
			if name != "" {
				message.OSName = name
			}
			message.OSVersion = version
		}
	}
	if data, err := readFile("/proc/sys/kernel/osrelease"); err == nil {
		message.Kernel = strings.TrimSpace(string(data))
	}
	if addresses, err := interfaceAddrs(); err == nil {
		message.IPv4, message.IPv6 = collectIPAddresses(addresses)
	}
	return message
}

func parseOSRelease(data []byte) (string, string) {
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "NAME" && key != "PRETTY_NAME" && key != "VERSION_ID" && key != "VERSION" {
			continue
		}
		values[key] = parseOSReleaseValue(strings.TrimSpace(value))
	}
	name := values["NAME"]
	if name == "" {
		name = values["PRETTY_NAME"]
	}
	version := values["VERSION_ID"]
	if version == "" {
		version = values["VERSION"]
	}
	return name, version
}

func parseOSReleaseValue(value string) string {
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return value
}

func collectIPAddresses(addresses []net.Addr) ([]string, []string) {
	ipv4Set := make(map[string]struct{})
	ipv6Set := make(map[string]struct{})
	for _, address := range addresses {
		value := address.String()
		if ip, _, err := net.ParseCIDR(value); err == nil {
			addReportedIP(ip, ipv4Set, ipv6Set)
			continue
		}
		addReportedIP(net.ParseIP(value), ipv4Set, ipv6Set)
	}
	ipv4 := sortedReportedIPs(ipv4Set)
	ipv6 := sortedReportedIPs(ipv6Set)
	return ipv4, ipv6
}

func addReportedIP(ip net.IP, ipv4, ipv6 map[string]struct{}) {
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return
	}
	if value := ip.To4(); value != nil {
		ipv4[value.String()] = struct{}{}
		return
	}
	ipv6[ip.String()] = struct{}{}
}

func sortedReportedIPs(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	if len(result) > maxReportedIPAddresses {
		result = result[:maxReportedIPAddresses]
	}
	return result
}

func newPublicIPv4HTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: publicIPv4RequestTimeout}
	transport.DialContext = func(ctx context.Context, _ string, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", address)
	}
	return &http.Client{Transport: transport, Timeout: publicIPv4RequestTimeout}
}

func detectPublicIPv4(ctx context.Context, client *http.Client, endpoint string) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, publicIPv4ResponseLimit+1))
	if err != nil || len(data) > publicIPv4ResponseLimit {
		return ""
	}
	ip := net.ParseIP(strings.TrimSpace(string(data)))
	if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return ""
	}
	return ip.To4().String()
}

func sendSystemInfo(ctx context.Context, connection *websocket.Conn, message systemInfoMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, payload)
}
