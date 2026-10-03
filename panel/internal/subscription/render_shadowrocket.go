package subscription

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func RenderShadowrocketSubscription(data SubscriptionData) ([]byte, error) {
	nodes := make([]ResolvedSubscriptionNode, 0, len(data.Nodes))
	for _, node := range data.Nodes {
		nodes = append(nodes, resolvedNodeFromClientShare(node))
	}
	return renderShadowrocketResolvedSubscription(nodes, data.NodeNames, data.RoutingBindings, data.RoutingPreset, data.ShadowrocketTemplate)
}

func RenderPersonalShadowrocketSubscription(data PersonalSubscriptionData) ([]byte, error) {
	return renderShadowrocketResolvedSubscription(data.Nodes, data.NodeNames, data.RoutingBindings, data.RoutingPreset, data.ShadowrocketTemplate)
}

func renderShadowrocketResolvedSubscription(nodes []ResolvedSubscriptionNode, nodeNames map[int64]string,
	bindings RoutingBindings, preset *RoutingPreset, template *SubscriptionTemplate,
) ([]byte, error) {
	source, err := BuildShadowrocketConfiguration(template)
	if err != nil {
		return nil, err
	}
	if preset == nil {
		return nil, ErrInvalidRoutingPreset
	}
	routing := *preset
	// Resolve rules before generic preset validation so missing providers and
	// incompatible attributes retain the rule's position and original text.
	providers := make(map[string]RoutingRuleProvider, len(routing.RuleProviders))
	for _, provider := range routing.RuleProviders {
		providers[strings.TrimSpace(provider.Name)] = provider
	}
	ruleLines := make([]string, 0, len(routing.Rules))
	for index, rule := range routing.Rules {
		line, err := renderShadowrocketRule(rule, providers)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条规则 %q：%w", index+1, shadowrocketErrorContext(rule), err)
		}
		ruleLines = append(ruleLines, line)
	}
	if err := normalizeRoutingPreset(&routing); err != nil {
		return nil, err
	}
	// A single namespace is used for declarations and references. Reject
	// collisions instead of silently binding a group to a different node.
	names := map[string]bool{"DIRECT": true, "REJECT": true}
	for _, group := range routing.Groups {
		names[group.Name] = true
	}
	nodes, nodeNames, err = shadowrocketNodeNames(nodes, nodeNames, names)
	if err != nil {
		return nil, err
	}
	proxyLines := make([]string, 0, len(nodes))
	allNames := make([]string, 0, len(nodes))
	for index, node := range nodes {
		if names[node.Name] {
			return nil, fmt.Errorf("第 %d 个节点名称与节点或策略组重复: %w", index+1, ErrUnsupportedShadowrocketProtocol)
		}
		line, err := renderShadowrocketProxy(node)
		if err != nil {
			return nil, fmt.Errorf("第 %d 个节点: %w", index+1, err)
		}
		names[node.Name] = true
		allNames = append(allNames, node.Name)
		proxyLines = append(proxyLines, line)
	}
	groups, err := resolveRoutingGroups(routing.Groups, bindings, nodeNames, allNames)
	if err != nil {
		return nil, err
	}
	groupLines := make([]string, 0, len(groups))
	for _, group := range groups {
		for _, member := range group.Proxies {
			if !names[member] {
				return nil, ErrInvalidRoutingBindings
			}
		}
		line, err := renderShadowrocketProxyGroup(group)
		if err != nil {
			return nil, err
		}
		groupLines = append(groupLines, line)
	}
	return []byte(injectShadowrocketSections(source, proxyLines, groupLines, ruleLines)), nil
}

func shadowrocketNodeNames(nodes []ResolvedSubscriptionNode, nodeNames map[int64]string, reserved map[string]bool) ([]ResolvedSubscriptionNode, map[int64]string, error) {
	type namePart struct {
		base, country string
	}
	parts := make([]namePart, len(nodes))
	baseCount := make(map[string]int, len(nodes))
	used := make(map[string]bool, len(reserved)+len(nodes))
	for name := range reserved {
		used[name] = true
	}
	originals := make(map[string]bool, len(nodes))
	unflagged := make(map[string]bool, len(nodes))
	for index, node := range nodes {
		if !safeShadowrocketValue(node.Name) {
			return nil, nil, fmt.Errorf("第 %d 个节点: %w", index+1, ErrUnsupportedShadowrocketProtocol)
		}
		if reserved[node.Name] || originals[node.Name] {
			return nil, nil, fmt.Errorf("第 %d 个节点名称与节点或策略组重复: %w", index+1, ErrUnsupportedShadowrocketProtocol)
		}
		originals[node.Name] = true
		base, country := splitShadowrocketLeadingFlag(node.Name)
		parts[index] = namePart{base, country}
		baseCount[base]++
		if country == "" {
			used[base] = true
			unflagged[base] = true
		}
	}
	// Keep unique stripped names before assigning suffixes, so earlier nodes
	// cannot take the name that a later node would otherwise keep unchanged.
	for _, part := range parts {
		if part.country != "" && baseCount[part.base] == 1 && !used[part.base] {
			used[part.base] = true
		}
	}
	resolved := append([]ResolvedSubscriptionNode(nil), nodes...)
	byOriginal := make(map[string]string, len(nodes))
	for index, part := range parts {
		if part.country == "" {
			continue
		}
		name := part.base
		if baseCount[name] > 1 || reserved[name] || unflagged[name] {
			if name == part.country {
				for suffix := 2; ; suffix++ {
					name = part.country + " " + strconv.Itoa(suffix)
					if !used[name] {
						break
					}
				}
			} else {
				name = part.base + " [" + part.country + "]"
				for suffix := 2; used[name]; suffix++ {
					name = part.base + " [" + part.country + " " + strconv.Itoa(suffix) + "]"
				}
			}
		}
		used[name] = true
		resolved[index].Name = name
		byOriginal[nodes[index].Name] = name
	}
	resolvedNames := make(map[int64]string, len(nodeNames))
	for id, name := range nodeNames {
		if updated, exists := byOriginal[name]; exists {
			name = updated
		}
		resolvedNames[id] = name
	}
	return resolved, resolvedNames, nil
}

func splitShadowrocketLeadingFlag(name string) (string, string) {
	runes := []rune(name)
	if len(runes) < 2 || runes[0] < 0x1F1E6 || runes[0] > 0x1F1FF || runes[1] < 0x1F1E6 || runes[1] > 0x1F1FF {
		return name, ""
	}
	code := string([]rune{'A' + runes[0] - 0x1F1E6, 'A' + runes[1] - 0x1F1E6})
	clean := strings.TrimLeft(string(runes[2:]), " ")
	if clean == "" {
		clean = code
	}
	return clean, code
}

func renderShadowrocketProxy(node ResolvedSubscriptionNode) (string, error) {
	// Do not put credentials or raw node data in errors.
	for _, value := range []string{node.Name, node.Address, node.UUID, node.ServerName, node.Flow,
		node.Fingerprint, node.RealityPublicKey, node.RealityShortID, node.Method, node.ShadowsocksPassword} {
		if !safeShadowrocketValue(value) {
			return "", ErrUnsupportedShadowrocketProtocol
		}
	}
	if strings.TrimSpace(node.Name) == "" || node.Address == "" || node.Port < 1 || node.Port > 65535 {
		return "", ErrUnsupportedShadowrocketProtocol
	}
	fields := []string{"", shadowrocketValue(node.Address), strconv.Itoa(node.Port)}
	switch node.Protocol {
	case proxystore.ProtocolVLESS:
		if node.UUID == "" || (node.Network != "" && node.Network != proxystore.TransportTCP) {
			return "", ErrUnsupportedShadowrocketProtocol
		}
		fields[0] = "vless"
		fields = append(fields, "encrypt-method=none", "password="+shadowrocketValue(node.UUID), "obfs=none", "udp-relay=true")
		if node.Security != "" && node.Security != "none" && node.Security != proxystore.SecurityTLS && node.Security != proxystore.SecurityReality {
			return "", ErrUnsupportedShadowrocketProtocol
		}
		if node.TLS || node.Security == proxystore.SecurityTLS || node.Security == proxystore.SecurityReality {
			fields = append(fields, "tls=true")
		}
		if node.ServerName != "" {
			fields = append(fields, "peer="+shadowrocketValue(node.ServerName))
		}
		if node.Security == proxystore.SecurityReality {
			if node.RealityPublicKey == "" || node.ServerName == "" {
				return "", ErrUnsupportedShadowrocketProtocol
			}
			fields = append(fields, "reality=true", "pbk="+shadowrocketValue(node.RealityPublicKey), "sid="+shadowrocketValue(node.RealityShortID))
		}
		if flow := subscriptionVLESSFlow(node.Flow); flow != "" {
			fields = append(fields, "flow="+shadowrocketValue(flow))
		}
		if node.Fingerprint != "" {
			fields = append(fields, "fp="+shadowrocketValue(node.Fingerprint))
		}
	case proxystore.ProtocolShadowsocks:
		if node.Method == "" || node.ShadowsocksPassword == "" {
			return "", ErrUnsupportedShadowrocketProtocol
		}
		fields[0] = "ss"
		// SS2022's server:user key pair is already resolved by the source.
		fields = append(fields, "encrypt-method="+shadowrocketValue(node.Method), "password="+shadowrocketValue(node.ShadowsocksPassword), "udp-relay=true")
	default:
		return "", ErrUnsupportedShadowrocketProtocol
	}
	return shadowrocketIdent(node.Name) + " = " + strings.Join(fields, ","), nil
}

func renderShadowrocketProxyGroup(group resolvedRoutingGroup) (string, error) {
	// RoutingPreset currently permits only select; never guess a mapping for
	// future Mihomo group types that require additional client settings.
	if group.Type != "select" || !safeShadowrocketValue(group.Name) {
		return "", ErrInvalidRoutingPreset
	}
	fields := []string{"select"}
	for _, member := range group.Proxies {
		if !safeShadowrocketValue(member) {
			return "", ErrInvalidRoutingBindings
		}
		fields = append(fields, shadowrocketIdent(member))
	}
	return shadowrocketIdent(group.Name) + " = " + strings.Join(fields, ","), nil
}

func renderShadowrocketRule(rule string, providers map[string]RoutingRuleProvider) (string, error) {
	if _, _, err := parseRoutingRule(rule); err != nil {
		return "", ErrUnsupportedShadowrocketRule
	}
	parts := strings.Split(rule, ",")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if !safeShadowrocketValue(parts[index]) {
			return "", ErrUnsupportedShadowrocketRule
		}
	}
	if parts[0] == "MATCH" {
		return "FINAL," + shadowrocketIdent(parts[1]), nil
	}
	supportsNoResolve := false
	switch parts[0] {
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD":
	case "IP-CIDR", "IP-CIDR6", "GEOIP":
		supportsNoResolve = true
	case "RULE-SET":
		supportsNoResolve = true
		provider, exists := providers[parts[1]]
		if !exists {
			return "", fmt.Errorf("未找到规则源 %q: %w", shadowrocketErrorContext(parts[1]), ErrUnsupportedShadowrocketRule)
		}
		address, err := shadowrocketProviderURL(provider)
		if err != nil {
			return "", fmt.Errorf("规则源 %q：%w", shadowrocketErrorContext(parts[1]), err)
		}
		parts[1] = address
	default:
		// GEOSITE, logical expressions and other Mihomo-only rules are not
		// equivalent. Failing keeps their intended routing from being lost.
		return "", ErrUnsupportedShadowrocketRule
	}
	if len(parts) != 3 && !(len(parts) == 4 && supportsNoResolve && parts[3] == "no-resolve") {
		return "", ErrUnsupportedShadowrocketRule
	}
	parts[1] = shadowrocketValue(parts[1])
	parts[2] = shadowrocketIdent(parts[2])
	return strings.Join(parts, ","), nil
}

func shadowrocketProviderURL(provider RoutingRuleProvider) (string, error) {
	for _, attribute := range []struct{ name, value, required string }{
		{"type", provider.Type, "http"},
		{"behavior", provider.Behavior, "classical"},
		{"format", provider.Format, "text"},
	} {
		if attribute.value != attribute.required {
			return "", fmt.Errorf("使用 %s=%q，Shadowrocket 仅支持 http / classical / text Rule Provider: %w",
				attribute.name, shadowrocketErrorContext(attribute.value), ErrUnsupportedShadowrocketRule)
		}
	}
	parsed, err := url.Parse(provider.URL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || !safeShadowrocketValue(provider.URL) {
		return "", fmt.Errorf("URL 无效，须为不含控制字符的 http/https URL: %w", ErrUnsupportedShadowrocketRule)
	}
	// Compatibility is declared by the provider metadata, not its name, host
	// or URL suffix. The client downloads the text; Panel never rewrites it.
	return provider.URL, nil
}

func shadowrocketErrorContext(value string) string {
	runes := []rune(value)
	if len(runes) > 256 {
		return string(runes[:256]) + "…"
	}
	return value
}

func safeShadowrocketValue(value string) bool {
	return utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
	}) < 0
}

func shadowrocketIdent(value string) string {
	if value == "" || strings.ContainsAny(value, ",=\"\\#;") || strings.HasPrefix(strings.TrimLeftFunc(value, unicode.IsSpace), "[") {
		return shadowrocketEscaped(value)
	}
	return value
}

func shadowrocketValue(value string) string {
	if value == "" || strings.ContainsAny(value, ",=\"\\#;[]") || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return shadowrocketEscaped(value)
	}
	return value
}

func shadowrocketEscaped(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

var shadowrocketMarkers = map[string]string{
	"proxy": "{{PROXIES}}", "proxy group": "{{PROXY_GROUPS}}", "rule": "{{RULES}}",
}

func validateShadowrocketTemplate(source string) error {
	if strings.TrimSpace(source) == "" || len(source) > maxSubscriptionTemplateBytes || !utf8.ValidString(source) {
		return ErrInvalidShadowrocketTemplate
	}
	sections, markers := map[string]bool{}, map[string]bool{}
	section := ""
	for _, raw := range strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsControl(r) && r != '\t' }) >= 0 || strings.HasPrefix(line, "#!") {
			return ErrInvalidShadowrocketTemplate
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			if strings.Contains(line, "{{") || strings.Contains(line, "}}") {
				return ErrInvalidShadowrocketTemplate
			}
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return ErrInvalidShadowrocketTemplate
			}
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			switch section {
			case "general", "proxy", "proxy group", "rule", "host", "url rewrite":
			default:
				return ErrInvalidShadowrocketTemplate
			}
			if sections[section] {
				return ErrInvalidShadowrocketTemplate
			}
			sections[section] = true
			continue
		}
		if marker, dynamic := shadowrocketMarkers[section]; dynamic {
			if line != marker || markers[section] {
				return ErrInvalidShadowrocketTemplate
			}
			markers[section] = true
		} else if section == "" || strings.Contains(line, "{{") || strings.Contains(line, "}}") {
			return ErrInvalidShadowrocketTemplate
		}
	}
	if !sections["general"] || len(markers) != len(shadowrocketMarkers) {
		return ErrInvalidShadowrocketTemplate
	}
	return nil
}

func injectShadowrocketSections(source string, proxies, groups, rules []string) string {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	// Replace complete template lines once. User data containing marker text
	// must never be interpreted as a second template expansion.
	for index, line := range lines {
		switch strings.TrimSpace(line) {
		case "{{PROXIES}}":
			lines[index] = strings.Join(proxies, "\n")
		case "{{PROXY_GROUPS}}":
			lines[index] = strings.Join(groups, "\n")
		case "{{RULES}}":
			lines[index] = strings.Join(rules, "\n")
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}
