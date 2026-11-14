package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/bits"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	chinaNFTBatchElements      = 64
	chinaNFTBatchBytes         = 4096
)

var (
	errManagedChinaInboundFirewall    = errors.New("managed China inbound firewall synchronization failed")
	errManagedChinaInboundRequiresNFT = errors.New("managed China inbound firewall requires nftables; install nftables (Alpine: apk add --no-cache nftables; Debian/Ubuntu: apt-get update && apt-get install -y nftables) or run the Agent upgrade installer")
	errChinaNFTPrivileges             = errors.New("managed China inbound firewall: insufficient firewall privileges; check CAP_NET_ADMIN and container permissions")
	errChinaNFTKernel                 = errors.New("managed China inbound firewall: nftables kernel or container support is unavailable")
	errChinaNFTInspect                = errors.New("managed China inbound firewall: nftables inspection failed; check kernel, container and rule compatibility")
	errChinaNFTLoad                   = errors.New("managed China inbound firewall: failed to prepare or load candidate sets; existing rules remain unchanged")
	errChinaNFTActivation             = errors.New("managed China inbound firewall: failed to activate candidate rules; inspect the Agent log and retry")
	errChinaNFTTooLarge               = errors.New("managed China inbound firewall: nftables message too large even for a single element; check container netlink limits")
	errChinaNFTInterval               = errors.New("managed China inbound firewall: invalid or overlapping nftables address interval")
	errChinaNFTCleanup                = errors.New("managed China inbound firewall: filtering is active, but inactive set cleanup failed; retry synchronization")
	errChinaNFTApply                  = errors.New("managed China inbound firewall: applying nftables rules failed; check kernel, container and rule compatibility")
)

type chinaPrefixes struct {
	FetchedAt time.Time `json:"fetched_at"`
	IPv4      []string  `json:"ipv4"`
	IPv6      []string  `json:"ipv6"`
}

type chinaInboundFirewall struct {
	cachePath      string
	now            func() time.Time
	download       func(context.Context) ([]byte, error)
	lookPath       func(string) (string, error)
	runCommand     func(context.Context, string, ...string) ([]byte, error)
	refreshPending bool
}

func newChinaInboundFirewall() *chinaInboundFirewall {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	client := newChinaPrefixesHTTPClient(dialer.DialContext)
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

func newChinaPrefixesHTTPClient(dialContext func(context.Context, string, string) (net.Conn, error)) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = func(ctx context.Context, _ string, address string) (net.Conn, error) {
		return dialContext(ctx, "tcp4", address)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" {
				return errors.New("APNIC redirect must use HTTPS")
			}
			return nil
		},
	}
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
	if !refreshed && !f.refreshPending {
		return nil
	}
	f.refreshPending = true
	command, err := f.lookPath("nft")
	if err != nil {
		return errManagedChinaInboundRequiresNFT
	}
	exists, err := f.inspectOwnedTable(ctx, command)
	if err != nil {
		return err
	}
	err = f.install(ctx, command, exists, prefixes, state)
	if err == nil {
		f.refreshPending = false
	}
	return err
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

func (f *chinaInboundFirewall) purge(ctx context.Context) error {
	return f.disable(ctx)
}

func (f *chinaInboundFirewall) inspectOwnedTable(ctx context.Context, command string) (bool, error) {
	output, err := f.runCommand(ctx, command, "list", "tables")
	if err != nil {
		return false, chinaNFTCommandError("list nftables tables", output, err)
	}
	if !nftTablesContain(string(output), "inet", chinaFirewallTable) {
		return false, nil
	}
	output, err = f.runCommand(ctx, command, "list", "chain", "inet", chinaFirewallTable, "input")
	if err != nil {
		return false, chinaNFTCommandError("inspect managed nftables table", output, err)
	}
	if !chinaNFTChainOwned(string(output)) {
		return false, chinaInboundFirewallError("refuse unmanaged nftables table", nil)
	}
	return true, nil
}

func chinaNFTChainOwned(chain string) bool {
	for _, line := range strings.Split(chain, "\n") {
		fields := strings.Fields(strings.ReplaceAll(line, `"`, ""))
		if comment, ok := fieldAfter(fields, "comment"); ok && comment == chinaFirewallOwnerMarker {
			return true
		}
	}
	return false
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

// Each generation has fresh set names. Never mutate a set referenced by live rules.
func (f *chinaInboundFirewall) install(ctx context.Context, command string, exists bool, prefixes chinaPrefixes, state desiredState) (result error) {
	ipv4, err := optimizeChinaPrefixes(prefixes.IPv4, true)
	if err != nil {
		return chinaInboundFirewallError("invalid IPv4 prefixes", err)
	}
	ipv6, err := optimizeChinaPrefixes(prefixes.IPv6, false)
	if err != nil {
		return chinaInboundFirewallError("invalid IPv6 prefixes", err)
	}
	if len(ipv4) == 0 || len(ipv6) == 0 {
		return chinaInboundFirewallError("empty address prefixes", nil)
	}
	if len(state.ChinaInboundWhitelist) > 128 {
		return chinaInboundFirewallError("too many whitelist entries", nil)
	}
	var allow4, allow6 []string
	for _, raw := range state.ChinaInboundWhitelist {
		p, err := netip.ParsePrefix(raw)
		if err != nil || p.Addr().Is4In6() || p.Bits() == 0 {
			return chinaInboundFirewallError("invalid whitelist prefix", nil)
		}
		if p.Addr().Is4() {
			allow4 = append(allow4, p.Masked().String())
		} else {
			allow6 = append(allow6, p.Masked().String())
		}
	}
	allow4, err = optimizeChinaPrefixes(allow4, true)
	if err != nil {
		return chinaInboundFirewallError("invalid IPv4 whitelist", err)
	}
	allow6, err = optimizeChinaPrefixes(allow6, false)
	if err != nil {
		return chinaInboundFirewallError("invalid IPv6 whitelist", err)
	}
	if !exists {
		bootstrap := fmt.Sprintf("create table inet %s\nadd chain inet %s input { type filter hook input priority %d; policy accept; }\nadd rule inet %s input counter comment \"%s\"\n", chinaFirewallTable, chinaFirewallTable, chinaFirewallInputPriority, chinaFirewallTable, chinaFirewallOwnerMarker)
		if err := f.runBatch(ctx, command, []byte(bootstrap)); err != nil {
			return fmt.Errorf("initialize managed table: %w", err)
		}
	}
	// Remove abandoned generations only after inspecting the currently active chain.
	if err := f.cleanupInactiveSets(ctx, command); err != nil {
		return fmt.Errorf("recover inactive sets: %w", err)
	}
	// On failure, inspect the chain again before deleting any candidate. If the
	// activation outcome is uncertain this also protects a committed generation.
	defer func() {
		if result != nil && ctx.Err() == nil {
			if cleanupErr := f.cleanupInactiveSets(ctx, command); cleanupErr != nil {
				result = errors.Join(result, fmt.Errorf("cleanup inactive candidates: %w", cleanupErr))
			}
		}
	}()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return chinaInboundFirewallError("generate set names", err)
	}
	suffix := hex.EncodeToString(random[:])
	tcp, udp := managedChinaInboundPorts(state)
	portStrings := func(ports []int) []string {
		out := make([]string, len(ports))
		for i, p := range ports {
			out[i] = strconv.Itoa(p)
		}
		return out
	}
	sets := []struct {
		name, typ, flags string
		elements         []string
	}{
		{"c4_" + suffix, "ipv4_addr", "flags interval;", ipv4},
		{"c6_" + suffix, "ipv6_addr", "flags interval;", ipv6},
		{"tp_" + suffix, "inet_service", "", portStrings(tcp)},
		{"up_" + suffix, "inet_service", "", portStrings(udp)},
		{"a4_" + suffix, "ipv4_addr", "flags interval;", allow4},
		{"a6_" + suffix, "ipv6_addr", "flags interval;", allow6},
	}
	for _, set := range sets {
		create := fmt.Sprintf("create set inet %s %s { type %s; %s }\n", chinaFirewallTable, set.name, set.typ, set.flags)
		if err := f.runBatch(ctx, command, []byte(create)); err != nil {
			return fmt.Errorf("%w: %w", errChinaNFTLoad, err)
		}
		if err := f.loadNFTElements(ctx, command, set.name, set.elements); err != nil {
			return fmt.Errorf("%w: %w", errChinaNFTLoad, err)
		}
		output, err := f.runCommand(ctx, command, "-t", "list", "set", "inet", chinaFirewallTable, set.name)
		if err != nil {
			return chinaNFTCommandError("validate candidate set", output, err)
		}
		if !strings.Contains(string(output), "type "+set.typ) || (set.flags != "" && !strings.Contains(string(output), "flags interval")) {
			return chinaInboundFirewallError("candidate set schema mismatch", nil)
		}
	}
	var activation strings.Builder
	fmt.Fprintf(&activation, "flush chain inet %s input\nadd rule inet %s input counter comment \"%s\"\n", chinaFirewallTable, chinaFirewallTable, chinaFirewallOwnerMarker)
	for _, rule := range []struct{ family, addr, allow, proto, port string }{
		{"ip", sets[0].name, sets[4].name, "tcp", sets[2].name}, {"ip6", sets[1].name, sets[5].name, "tcp", sets[2].name},
		{"ip", sets[0].name, sets[4].name, "udp", sets[3].name}, {"ip6", sets[1].name, sets[5].name, "udp", sets[3].name},
	} {
		fmt.Fprintf(&activation, "add rule inet %s input %s saddr != @%s %s saddr @%s %s dport @%s drop\n", chinaFirewallTable, rule.family, rule.allow, rule.family, rule.addr, rule.proto, rule.port)
	}
	if err := f.runBatch(ctx, command, []byte(activation.String())); err != nil {
		return fmt.Errorf("%w: %w", errChinaNFTActivation, err)
	}
	if err := f.cleanupInactiveSets(ctx, command); err != nil {
		return fmt.Errorf("%w: %v", errChinaNFTCleanup, err)
	}
	return nil
}

func (f *chinaInboundFirewall) runBatch(ctx context.Context, command string, batch []byte) error {
	path, err := writeTemporaryFile(os.TempDir(), ".vps-panel-cn-block-*.nft", batch, 0o600)
	if err != nil {
		return chinaInboundFirewallError("write nftables batch", err)
	}
	defer os.Remove(path)
	if output, err := f.runCommand(ctx, command, "-f", path); err != nil {
		return chinaNFTCommandError("apply nftables batch", output, err)
	}
	return nil
}

func (f *chinaInboundFirewall) loadNFTElements(ctx context.Context, command, name string, values []string) error {
	for start := 0; start < len(values); {
		end := start
		size := len(name) + len(chinaFirewallTable) + 40
		for end < len(values) && end-start < chinaNFTBatchElements && size+len(values[end])+2 <= chinaNFTBatchBytes {
			size += len(values[end]) + 2
			end++
		}
		if end == start {
			return chinaInboundFirewallError("element exceeds batch size limit", nil)
		}
		for {
			batch := fmt.Sprintf("add element inet %s %s { %s }\n", chinaFirewallTable, name, strings.Join(values[start:end], ", "))
			err := f.runBatch(ctx, command, []byte(batch))
			if err == nil {
				start = end
				break
			}
			if !errors.Is(err, errChinaNFTTooLarge) || end-start == 1 {
				return err
			}
			end = start + (end-start)/2
		}
	}
	return nil
}

var chinaManagedSetName = regexp.MustCompile(`^(?:c4|c6|a4|a6|tp|up)_[0-9a-f]{16}$`)
var chinaSetDefinition = regexp.MustCompile(`(?m)^\s*set\s+([A-Za-z0-9_]+)\s*\{`)

func (f *chinaInboundFirewall) cleanupInactiveSets(ctx context.Context, command string) error {
	chain, err := f.runCommand(ctx, command, "list", "chain", "inet", chinaFirewallTable, "input")
	if err != nil {
		return chinaNFTCommandError("inspect active rules", chain, err)
	}
	if !chinaNFTChainOwned(string(chain)) {
		return chinaInboundFirewallError("refuse unmanaged nftables table", nil)
	}
	output, err := f.runCommand(ctx, command, "-t", "list", "table", "inet", chinaFirewallTable)
	if err != nil {
		return chinaNFTCommandError("inspect managed sets", output, err)
	}
	for _, match := range chinaSetDefinition.FindAllStringSubmatch(string(output), -1) {
		name := match[1]
		if !chinaManagedSetName.MatchString(name) && name != "cn_ipv4" && name != "cn_ipv6" && name != "tcp_ports" && name != "udp_ports" {
			continue
		}
		// Match whole identifiers so one set name cannot hide another.
		if regexp.MustCompile(`@` + regexp.QuoteMeta(name) + `\b`).Match(chain) {
			continue
		}
		if err := f.runBatch(ctx, command, []byte("delete set inet "+chinaFirewallTable+" "+name+"\n")); err != nil {
			return err
		}
	}
	return nil
}

// Normalize, sort, remove contained prefixes and recursively merge sibling CIDRs.
func optimizeChinaPrefixes(values []string, ipv4 bool) ([]string, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		p, err := netip.ParsePrefix(value)
		if err != nil || p.Addr().Is4() != ipv4 || p.Addr().Is4In6() {
			return nil, errors.New("invalid address prefix")
		}
		prefixes = append(prefixes, p.Masked())
	}
	sort.Slice(prefixes, func(i, j int) bool {
		c := prefixes[i].Addr().Compare(prefixes[j].Addr())
		if c != 0 {
			return c < 0
		}
		return prefixes[i].Bits() < prefixes[j].Bits()
	})
	stack := make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		if len(stack) > 0 && stack[len(stack)-1].Contains(p.Addr()) {
			continue
		}
		stack = append(stack, p)
		for len(stack) >= 2 {
			a, b := stack[len(stack)-2], stack[len(stack)-1]
			if a.Bits() != b.Bits() || a.Bits() == 0 {
				break
			}
			parent := netip.PrefixFrom(a.Addr(), a.Bits()-1).Masked()
			if !parent.Contains(b.Addr()) {
				break
			}
			stack = append(stack[:len(stack)-2], parent)
		}
	}
	result := make([]string, len(stack))
	for i, p := range stack {
		result[i] = p.String()
	}
	return result, nil
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
		for _, raw := range group.values {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil || prefix.Addr().Is4() != group.ipv4 || prefix.Addr().Is4In6() {
				return errors.New("invalid China prefix cache")
			}
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

// Do not return nft's raw output: failed commands may echo configuration.
func chinaNFTCommandError(action string, output []byte, err error) error {
	diagnostic := strings.ToLower(string(output) + " " + err.Error())
	var reason error
	switch {
	case strings.Contains(diagnostic, "message too large"):
		reason = errChinaNFTTooLarge
	case strings.Contains(diagnostic, "conflicting intervals"), strings.Contains(diagnostic, "overlapping"), strings.Contains(diagnostic, "invalid interval"):
		reason = errChinaNFTInterval
	case strings.Contains(diagnostic, "operation not permitted"), strings.Contains(diagnostic, "permission denied"), strings.Contains(diagnostic, "must be root"):
		reason = errChinaNFTPrivileges
	case strings.Contains(diagnostic, "operation not supported"), strings.Contains(diagnostic, "protocol not supported"), strings.Contains(diagnostic, "address family not supported"), strings.Contains(diagnostic, "not supported by kernel"):
		reason = errChinaNFTKernel
	case action == "apply nftables batch":
		reason = errChinaNFTApply
	default:
		reason = errChinaNFTInspect
	}
	return fmt.Errorf("%w: %s: %w", errManagedChinaInboundFirewall, action, reason)
}
