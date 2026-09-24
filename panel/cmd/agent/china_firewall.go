package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/bits"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	chinaDelegatedURL          = "https://ftp.apnic.net/stats/apnic/delegated-apnic-latest"
	chinaPrefixesCachePath     = "/var/lib/vps-panel/agent/firewall/cn-prefixes.json"
	chinaPrefixesCacheTTL      = 24 * time.Hour
	chinaPrefixesDownloadMax   = 32 << 20
	chinaFirewallTable         = "vps_panel_cn_block"
	chinaFirewallOwnerMarker   = "vps-panel-cn-block-owner"
	chinaFirewallInputPriority = -20
)

var (
	errManagedChinaInboundFirewall    = errors.New("managed China inbound firewall synchronization failed")
	errManagedChinaInboundRequiresNFT = errors.New("managed China inbound firewall requires nftables")
)

type chinaPrefixes struct {
	FetchedAt time.Time `json:"fetched_at"`
	IPv4      []string  `json:"ipv4"`
	IPv6      []string  `json:"ipv6"`
}

type chinaInboundFirewall struct {
	cachePath  string
	now        func() time.Time
	download   func(context.Context) ([]byte, error)
	lookPath   func(string) (string, error)
	runCommand func(context.Context, string, ...string) ([]byte, error)
}

func newChinaInboundFirewall() *chinaInboundFirewall {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" {
				return errors.New("APNIC redirect must use HTTPS")
			}
			return nil
		},
	}
	manager := &chinaInboundFirewall{
		cachePath:  chinaPrefixesCachePath,
		now:        time.Now,
		lookPath:   exec.LookPath,
		runCommand: runFirewallCommand,
	}
	manager.download = func(ctx context.Context) ([]byte, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, chinaDelegatedURL, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("APNIC returned %s", response.Status)
		}
		value, err := io.ReadAll(io.LimitReader(response.Body, chinaPrefixesDownloadMax+1))
		if err != nil {
			return nil, err
		}
		if len(value) > chinaPrefixesDownloadMax {
			return nil, errors.New("APNIC response exceeds size limit")
		}
		return value, nil
	}
	return manager
}

func (f *chinaInboundFirewall) apply(ctx context.Context, state desiredState) error {
	if !state.BlockChinaInbound {
		return f.disable(ctx)
	}
	command, err := f.lookPath("nft")
	if err != nil {
		return errManagedChinaInboundRequiresNFT
	}
	exists, err := f.inspectOwnedTable(ctx, command)
	if err != nil {
		return err
	}
	prefixes, _, err := f.loadPrefixes(ctx)
	if err != nil {
		return chinaInboundFirewallError("load China prefixes", err)
	}
	return f.install(ctx, command, exists, prefixes, state)
}

func (f *chinaInboundFirewall) refresh(ctx context.Context, state desiredState) error {
	if !state.BlockChinaInbound {
		return nil
	}
	prefixes, refreshed, err := f.loadPrefixes(ctx)
	if err != nil {
		return chinaInboundFirewallError("refresh China prefixes", err)
	}
	if !refreshed {
		return nil
	}
	command, err := f.lookPath("nft")
	if err != nil {
		return errManagedChinaInboundRequiresNFT
	}
	exists, err := f.inspectOwnedTable(ctx, command)
	if err != nil {
		return err
	}
	return f.install(ctx, command, exists, prefixes, state)
}

func (f *chinaInboundFirewall) disable(ctx context.Context) error {
	command, err := f.lookPath("nft")
	if err != nil {
		return nil
	}
	exists, err := f.inspectOwnedTable(ctx, command)
	if err != nil || !exists {
		return err
	}
	return f.runBatch(ctx, command, []byte("delete table inet "+chinaFirewallTable+"\n"))
}

func (f *chinaInboundFirewall) inspectOwnedTable(ctx context.Context, command string) (bool, error) {
	output, err := f.runCommand(ctx, command, "list", "tables")
	if err != nil {
		return false, chinaInboundFirewallError("list nftables tables", err)
	}
	if !nftTablesContain(string(output), "inet", chinaFirewallTable) {
		return false, nil
	}
	output, err = f.runCommand(ctx, command, "list", "chain", "inet", chinaFirewallTable, "input")
	if err != nil || !strings.Contains(string(output), chinaFirewallOwnerMarker) {
		return false, chinaInboundFirewallError("refuse unmanaged nftables table", nil)
	}
	return true, nil
}

func nftTablesContain(output, family, name string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "table" && fields[1] == family && fields[2] == name {
			return true
		}
	}
	return false
}

func (f *chinaInboundFirewall) install(
	ctx context.Context,
	command string,
	exists bool,
	prefixes chinaPrefixes,
	state desiredState,
) error {
	tcpPorts, udpPorts := managedChinaInboundPorts(state)
	return f.runBatch(ctx, command, renderChinaInboundNFT(exists, prefixes, tcpPorts, udpPorts))
}

func (f *chinaInboundFirewall) runBatch(ctx context.Context, command string, batch []byte) error {
	path, err := writeTemporaryFile(os.TempDir(), ".vps-panel-cn-block-*.nft", batch, 0o600)
	if err != nil {
		return chinaInboundFirewallError("write nftables batch", err)
	}
	defer os.Remove(path)
	if _, err := f.runCommand(ctx, command, "-f", path); err != nil {
		return chinaInboundFirewallError("apply nftables batch", err)
	}
	return nil
}

func renderChinaInboundNFT(exists bool, prefixes chinaPrefixes, tcpPorts, udpPorts []int) []byte {
	var value strings.Builder
	if exists {
		value.WriteString("delete table inet " + chinaFirewallTable + "\n")
	}
	value.WriteString("table inet " + chinaFirewallTable + " {\n")
	writeNFTAddressSet(&value, "cn_ipv4", "ipv4_addr", prefixes.IPv4)
	writeNFTAddressSet(&value, "cn_ipv6", "ipv6_addr", prefixes.IPv6)
	writeNFTPortSet(&value, "tcp_ports", tcpPorts)
	writeNFTPortSet(&value, "udp_ports", udpPorts)
	value.WriteString("  chain input {\n")
	value.WriteString(fmt.Sprintf("    type filter hook input priority %d; policy accept;\n", chinaFirewallInputPriority))
	value.WriteString("    counter comment \"" + chinaFirewallOwnerMarker + "\"\n")
	value.WriteString("    ip saddr @cn_ipv4 tcp dport @tcp_ports drop\n")
	value.WriteString("    ip6 saddr @cn_ipv6 tcp dport @tcp_ports drop\n")
	value.WriteString("    ip saddr @cn_ipv4 udp dport @udp_ports drop\n")
	value.WriteString("    ip6 saddr @cn_ipv6 udp dport @udp_ports drop\n")
	value.WriteString("  }\n}\n")
	return []byte(value.String())
}

func writeNFTAddressSet(value *strings.Builder, name, addressType string, elements []string) {
	value.WriteString("  set " + name + " {\n")
	value.WriteString("    type " + addressType + "\n")
	value.WriteString("    flags interval\n")
	if len(elements) != 0 {
		value.WriteString("    elements = { " + strings.Join(elements, ", ") + " }\n")
	}
	value.WriteString("  }\n")
}

func writeNFTPortSet(value *strings.Builder, name string, ports []int) {
	value.WriteString("  set " + name + " {\n")
	value.WriteString("    type inet_service\n")
	if len(ports) != 0 {
		values := make([]string, len(ports))
		for index, port := range ports {
			values[index] = strconv.Itoa(port)
		}
		value.WriteString("    elements = { " + strings.Join(values, ", ") + " }\n")
	}
	value.WriteString("  }\n")
}

func managedChinaInboundPorts(state desiredState) ([]int, []int) {
	tcp := make(map[int]bool)
	udp := make(map[int]bool)
	if state.Xray.Enabled {
		for _, proxy := range state.Xray.Proxies {
			if proxy.Port < 1 || proxy.Port > 65535 {
				continue
			}
			switch proxy.Protocol {
			case "vless":
				tcp[proxy.Port] = true
			case "shadowsocks":
				tcp[proxy.Port] = true
				udp[proxy.Port] = true
			}
		}
	}
	if state.Realm.Enabled {
		for _, relay := range state.Realm.Relays {
			if relay.ListenPort < 1 || relay.ListenPort > 65535 {
				continue
			}
			switch relay.Network {
			case "tcp":
				tcp[relay.ListenPort] = true
			case "udp":
				udp[relay.ListenPort] = true
			case "tcp,udp":
				tcp[relay.ListenPort] = true
				udp[relay.ListenPort] = true
			}
		}
	}
	return sortedPorts(tcp), sortedPorts(udp)
}

func sortedPorts(values map[int]bool) []int {
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}

func (f *chinaInboundFirewall) loadPrefixes(ctx context.Context) (chinaPrefixes, bool, error) {
	cached, cacheErr := f.readCache()
	now := f.now().UTC()
	if cacheErr == nil && now.Sub(cached.FetchedAt) < chinaPrefixesCacheTTL && !now.Before(cached.FetchedAt) {
		return cached, false, nil
	}
	data, err := f.download(ctx)
	if err != nil {
		if cacheErr == nil {
			log.Printf("refresh China inbound prefixes: %v; using stale cache", err)
			return cached, false, nil
		}
		return chinaPrefixes{}, false, err
	}
	ipv4, ipv6, err := parseAPNICChinaPrefixes(data)
	if err != nil {
		if cacheErr == nil {
			log.Printf("refresh China inbound prefixes: %v; using stale cache", err)
			return cached, false, nil
		}
		return chinaPrefixes{}, false, err
	}
	updated := chinaPrefixes{FetchedAt: now, IPv4: ipv4, IPv6: ipv6}
	if err := f.writeCache(updated); err != nil {
		return chinaPrefixes{}, false, err
	}
	return updated, true, nil
}

func (f *chinaInboundFirewall) readCache() (chinaPrefixes, error) {
	data, exists, err := readOptionalFile(f.cachePath)
	if err != nil {
		return chinaPrefixes{}, err
	}
	if !exists || len(data) > chinaPrefixesDownloadMax {
		return chinaPrefixes{}, errors.New("China prefix cache is unavailable")
	}
	var value chinaPrefixes
	if err := json.Unmarshal(data, &value); err != nil {
		return chinaPrefixes{}, err
	}
	if err := validateChinaPrefixes(value); err != nil {
		return chinaPrefixes{}, err
	}
	return value, nil
}

func (f *chinaInboundFirewall) writeCache(value chinaPrefixes) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	directory := filepath.Dir(f.cachePath)
	if err := ensureSecureDirectory(directory, 0o700); err != nil {
		return err
	}
	temporary, err := writeTemporaryFile(directory, ".cn-prefixes-*.json", data, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	return replaceFile(temporary, f.cachePath, 0o600)
}

func validateChinaPrefixes(value chinaPrefixes) error {
	if value.FetchedAt.IsZero() || len(value.IPv4) == 0 || len(value.IPv6) == 0 {
		return errors.New("invalid China prefix cache")
	}
	for _, group := range []struct {
		values []string
		ipv4   bool
	}{{value.IPv4, true}, {value.IPv6, false}} {
		seen := make(map[string]bool, len(group.values))
		for _, raw := range group.values {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4() != group.ipv4 || seen[raw] {
				return errors.New("invalid China prefix cache")
			}
			seen[raw] = true
		}
	}
	return nil
}

func parseAPNICChinaPrefixes(data []byte) ([]string, []string, error) {
	ipv4 := make(map[string]bool)
	ipv6 := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 7 || fields[0] != "apnic" || fields[1] != "CN" ||
			(fields[6] != "allocated" && fields[6] != "assigned") {
			continue
		}
		switch fields[2] {
		case "ipv4":
			start, err := netip.ParseAddr(fields[3])
			count, countErr := strconv.ParseUint(fields[4], 10, 64)
			if err != nil || countErr != nil || !start.Is4() || count == 0 {
				continue
			}
			for _, prefix := range ipv4RangePrefixes(start, count) {
				ipv4[prefix.String()] = true
			}
		case "ipv6":
			start, err := netip.ParseAddr(fields[3])
			prefixBits, bitsErr := strconv.Atoi(fields[4])
			if err != nil || bitsErr != nil || !start.Is6() || start.Is4In6() || prefixBits < 0 || prefixBits > 128 {
				continue
			}
			prefix := netip.PrefixFrom(start, prefixBits)
			if prefix == prefix.Masked() {
				ipv6[prefix.String()] = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	if len(ipv4) == 0 || len(ipv6) == 0 {
		return nil, nil, errors.New("APNIC response contains no usable China prefixes")
	}
	return sortedPrefixStrings(ipv4), sortedPrefixStrings(ipv6), nil
}

func ipv4RangePrefixes(start netip.Addr, count uint64) []netip.Prefix {
	bytes := start.As4()
	current := uint64(binary.BigEndian.Uint32(bytes[:]))
	if count == 0 || count > (uint64(1)<<32)-current {
		return nil
	}
	result := make([]netip.Prefix, 0)
	remaining := count
	for remaining > 0 {
		alignmentBits := bits.TrailingZeros32(uint32(current))
		if current == 0 {
			alignmentBits = 32
		}
		remainingBits := bits.Len64(remaining) - 1
		blockBits := alignmentBits
		if remainingBits < blockBits {
			blockBits = remainingBits
		}
		var address [4]byte
		binary.BigEndian.PutUint32(address[:], uint32(current))
		result = append(result, netip.PrefixFrom(netip.AddrFrom4(address), 32-blockBits))
		blockSize := uint64(1) << blockBits
		current += blockSize
		remaining -= blockSize
	}
	return result
}

func sortedPrefixStrings(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		leftPrefix, _ := netip.ParsePrefix(result[left])
		rightPrefix, _ := netip.ParsePrefix(result[right])
		if comparison := leftPrefix.Addr().Compare(rightPrefix.Addr()); comparison != 0 {
			return comparison < 0
		}
		return leftPrefix.Bits() < rightPrefix.Bits()
	})
	return result
}

func chinaInboundFirewallError(action string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", errManagedChinaInboundFirewall, action)
	}
	return fmt.Errorf("%w: %s: %v", errManagedChinaInboundFirewall, action, err)
}
