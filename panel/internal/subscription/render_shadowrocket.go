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
	if err := normalizeRoutingPreset(&routing); err != nil {
		return nil, err
	}
	// A single namespace is used for declarations and references. Reject
	// collisions instead of silently binding a group to a different node.
	names := map[string]bool{"DIRECT": true, "REJECT": true}
	for _, group := range routing.Groups {
		names[group.Name] = true
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
	providers := make(map[string]RoutingRuleProvider, len(routing.RuleProviders))
	for _, provider := range routing.RuleProviders {
		providers[provider.Name] = provider
	}
	ruleLines := make([]string, 0, len(routing.Rules))
	for index, rule := range routing.Rules {
		line, err := renderShadowrocketRule(rule, providers)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条规则: %w", index+1, err)
		}
		ruleLines = append(ruleLines, line)
	}
	return []byte(injectShadowrocketSections(source, proxyLines, groupLines, ruleLines)), nil
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
	fields := []string{"", shadowrocketQuote(node.Address), strconv.Itoa(node.Port)}
	switch node.Protocol {
	case proxystore.ProtocolVLESS:
		if node.UUID == "" || (node.Network != "" && node.Network != proxystore.TransportTCP) {
			return "", ErrUnsupportedShadowrocketProtocol
		}
		fields[0] = "vless"
		fields = append(fields, "encrypt-method=none", "password="+shadowrocketQuote(node.UUID), "obfs=none", "udp-relay=true")
		if node.Security != "" && node.Security != "none" && node.Security != proxystore.SecurityTLS && node.Security != proxystore.SecurityReality {
			return "", ErrUnsupportedShadowrocketProtocol
		}
		if node.TLS || node.Security == proxystore.SecurityTLS || node.Security == proxystore.SecurityReality {
			fields = append(fields, "tls=true")
		}
		if node.ServerName != "" {
			fields = append(fields, "peer="+shadowrocketQuote(node.ServerName))
		}
		if node.Security == proxystore.SecurityReality {
			if node.RealityPublicKey == "" || node.ServerName == "" {
				return "", ErrUnsupportedShadowrocketProtocol
			}
			fields = append(fields, "reality=true", "pbk="+shadowrocketQuote(node.RealityPublicKey), "sid="+shadowrocketQuote(node.RealityShortID))
		}
		if flow := subscriptionVLESSFlow(node.Flow); flow != "" {
			fields = append(fields, "flow="+shadowrocketQuote(flow))
		}
		if node.Fingerprint != "" {
			fields = append(fields, "fp="+shadowrocketQuote(node.Fingerprint))
		}
	case proxystore.ProtocolShadowsocks:
		if node.Method == "" || node.ShadowsocksPassword == "" {
			return "", ErrUnsupportedShadowrocketProtocol
		}
		fields[0] = "ss"
		// SS2022's server:user key pair is already resolved by the source.
		fields = append(fields, "encrypt-method="+shadowrocketQuote(node.Method), "password="+shadowrocketQuote(node.ShadowsocksPassword), "udp-relay=true")
	default:
		return "", ErrUnsupportedShadowrocketProtocol
	}
	return shadowrocketQuote(node.Name) + " = " + strings.Join(fields, ","), nil
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
		fields = append(fields, shadowrocketQuote(member))
	}
	return shadowrocketQuote(group.Name) + " = " + strings.Join(fields, ","), nil
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
		return "FINAL," + shadowrocketQuote(parts[1]), nil
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
			return "", ErrUnsupportedShadowrocketRule
		}
		address, err := shadowrocketProviderURL(provider)
		if err != nil {
			return "", err
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
	parts[1] = shadowrocketQuote(parts[1])
	parts[2] = shadowrocketQuote(parts[2])
	return strings.Join(parts, ","), nil
}

func shadowrocketProviderURL(provider RoutingRuleProvider) (string, error) {
	if provider.Type != "http" || provider.Behavior != "classical" || !safeShadowrocketValue(provider.URL) {
		return "", ErrUnsupportedShadowrocketRule
	}
	parsed, err := url.Parse(provider.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", ErrUnsupportedShadowrocketRule
	}
	if provider.Format == "text" && !strings.HasSuffix(strings.ToLower(parsed.Path), ".yaml") && !strings.HasSuffix(strings.ToLower(parsed.Path), ".yml") {
		return provider.URL, nil
	}
	// These exact URLs ship in Panel's default preset. The upstream publishes
	// equivalent Shadowrocket lists. Keep stored URLs and Mihomo output intact;
	// never rewrite an arbitrary YAML URL, mirror, branch or query string.
	const base = "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/"
	if provider.Format == "yaml" {
		for _, name := range []string{"OpenAI", "Claude", "Gemini", "YouTube", "Netflix", "Telegram", "TikTok", "Apple", "Copilot", "Microsoft"} {
			file := name
			if name == "Netflix" || name == "Apple" {
				file += "_Classical"
			}
			if provider.URL == base+"Clash/"+name+"/"+file+".yaml" {
				return base + "Shadowrocket/" + name + "/" + name + ".list", nil
			}
		}
	}
	return "", ErrUnsupportedShadowrocketRule
}

func safeShadowrocketValue(value string) bool {
	return utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
	}) < 0
}

func shadowrocketQuote(value string) string {
	if value == "" || strings.ContainsAny(value, ",=\"\\#;[]") || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
	}
	return value
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
