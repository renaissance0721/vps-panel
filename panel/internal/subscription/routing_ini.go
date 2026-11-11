package subscription

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// RoutingINIFormat documents the bounded, explicitly supported subset. The
// provider and raw rule extensions preserve the existing structured model.
const RoutingINIFormat = "vps-panel-routing-ini-v1"

// Allow for six-byte JSON escapes of ASCII characters while staying below the
// existing 64 KiB MCP request limit, including the request envelope.
const maxRoutingINIBytes = 10 << 10
const maxRoutingINIGroups = 128
const maxRoutingINIProviders = 128

// This Panel-specific marker represents a group populated solely by per-
// subscription bindings; it does not add a fallback or select any nodes.
const routingINIManualMembers = "!!VPS_MANUAL"

type RoutingINIDiagnostic struct {
	Line       int    `json:"line"`
	Directive  string `json:"directive"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}
type RoutingINIResult struct {
	Format        string                 `json:"format"`
	Groups        []RoutingGroup         `json:"groups"`
	RuleProviders []RoutingRuleProvider  `json:"rule_providers"`
	Rules         []string               `json:"rules"`
	Diagnostics   []RoutingINIDiagnostic `json:"diagnostics"`
	Warnings      []RoutingINIDiagnostic `json:"warnings"`
}

// ParseRoutingINI never fetches remote content. A remote URL alone does not
// identify the ruleset format, so it is rejected rather than guessed.
func ParseRoutingINI(source string) RoutingINIResult {
	out := RoutingINIResult{Format: RoutingINIFormat, Groups: []RoutingGroup{}, RuleProviders: []RoutingRuleProvider{}, Rules: []string{}, Diagnostics: []RoutingINIDiagnostic{}, Warnings: []RoutingINIDiagnostic{}}
	add := func(line int, directive, message, suggestion string) {
		out.Diagnostics = append(out.Diagnostics, RoutingINIDiagnostic{line, directive, message, suggestion})
	}
	if len(source) > maxRoutingINIBytes || !utf8.ValidString(source) {
		add(0, "file", "文件过大或不是有效 UTF-8", "文件上限为 10 KiB")
		return out
	}
	for _, r := range source {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			add(0, "file", "文件包含不受支持的控制字符", "")
			return out
		}
	}
	section := false
	groupLines := map[string]int{}
	ruleLines := []int{}
	ruleDirectives := []string{}
	providerLines := map[string]int{}
	for i, raw := range strings.Split(strings.TrimPrefix(source, "\ufeff"), "\n") {
		line := i + 1
		text := strings.TrimSpace(raw)
		if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
			continue
		}
		if strings.HasPrefix(text, "[") {
			if text != "[custom]" || section {
				add(line, "section", "只支持一个 [custom] 区段", "")
			} else {
				section = true
			}
			continue
		}
		if !section {
			add(line, "section", "指令必须位于 [custom] 之后", "")
			continue
		}
		directive, value, ok := strings.Cut(text, "=")
		directive = strings.TrimSpace(directive)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			add(line, directive, "缺少指令值", "")
			continue
		}
		switch directive {
		case "custom_proxy_group":
			if lineText := strings.TrimSuffix(raw, "\r"); lineText != strings.TrimRight(lineText, " \t") {
				add(line, directive, "策略组行末不能包含空白", "如需在正则末尾匹配空白，请使用转义")
				continue
			}
			parts := strings.Split(value, "`")
			if len(parts) < 3 || strings.TrimSpace(parts[1]) != "select" {
				add(line, directive, "仅支持包含成员的 select 策略组", "使用 名称`select`[]DIRECT")
				continue
			}
			group := RoutingGroup{Name: strings.TrimSpace(parts[0]), Type: "select", Proxies: []string{}}
			if group.Name == "" || utf8.RuneCountInString(group.Name) > maxNameRunes || group.Name == "DIRECT" || group.Name == "REJECT" {
				add(line, directive, "策略组名称无效", "")
				continue
			}
			if _, exists := groupLines[group.Name]; exists {
				add(line, directive, "策略组名称重复", "请使用唯一名称")
				continue
			}
			valid := true
			for _, member := range parts[2:] {
				trimmed := strings.TrimSpace(member)
				if member != trimmed && !strings.HasPrefix(trimmed, "[]") && trimmed != ".*" {
					add(line, directive, "节点正则不能有首尾空白", "如需匹配空白，请使用正则转义")
					valid = false
					continue
				}
				member = trimmed
				switch {
				case member == routingINIManualMembers:
					if len(parts) != 3 {
						add(line, directive, "手工绑定标记不能与其他成员混用", "移除该标记或其他成员")
						valid = false
					}
				case strings.HasPrefix(member, "[]") && len(member) > 2:
					if group.IncludeAll || len(group.NodePatterns) > 0 {
						add(line, directive, "显式策略组必须位于节点选择器之前", "先列出 []成员")
						valid = false
						continue
					}
					group.Proxies = append(group.Proxies, strings.TrimPrefix(member, "[]"))
				case member == ".*":
					if group.IncludeAll {
						add(line, directive, "全部节点选择器重复", "只保留一个 .*")
						valid = false
					}
					group.IncludeAll = true
				default:
					if member == "" || len(member) > 256 || strings.HasPrefix(member, "!!") || group.IncludeAll {
						add(line, directive, "节点正则无效或位置不受支持", "正则必须位于 .* 之前且不超过 256 字节")
						valid = false
						continue
					}
					if _, err := regexp.Compile(member); err != nil {
						add(line, directive, "节点正则表达式无效（使用 RE2）", "检查正则语法")
						valid = false
						continue
					}
					if len(group.NodePatterns) >= 16 {
						add(line, directive, "策略组的节点正则超过上限", "每组最多 16 条")
						valid = false
						continue
					}
					group.NodePatterns = append(group.NodePatterns, member)
				}
			}
			if valid {
				out.Groups = append(out.Groups, group)
				groupLines[group.Name] = line
			}
		case "rule_provider":
			var p RoutingRuleProvider
			decoder := json.NewDecoder(strings.NewReader(value))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&p); err != nil {
				add(line, directive, "规则源必须是完整的 JSON 对象", "")
				continue
			}
			var trailing any
			if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
				add(line, directive, "规则源包含多余内容", "")
				continue
			}
			if routingINIURLHasCredentials(p.URL) {
				add(line, directive, "规则源 URL 不支持用户信息、查询参数或片段", "")
				continue
			}
			if !routingINIProviderSupported(p) {
				add(line, directive, "不支持该规则源类型、行为或格式", "支持 http 与 classical/domain/ipcidr、yaml/text/mrs 的有效组合")
				continue
			}
			normalized, _, err := normalizeRoutingRuleProviders([]RoutingRuleProvider{p})
			if err != nil {
				add(line, directive, "规则源 URL 或元数据无效", "请明确提供 HTTP URL、类型、格式、行为和更新间隔")
				continue
			}
			p = normalized[0]
			if _, exists := providerLines[p.Name]; exists {
				add(line, directive, "规则源名称重复", "")
				continue
			}
			providerLines[p.Name] = line
			out.RuleProviders = append(out.RuleProviders, p)
		case "routing_rule":
			var rule string
			if err := json.Unmarshal([]byte(value), &rule); err != nil {
				add(line, directive, "规则必须是 JSON 字符串", "")
				continue
			}
			out.Rules = append(out.Rules, rule)
			ruleLines = append(ruleLines, line)
			ruleDirectives = append(ruleDirectives, directive)
		case "ruleset":
			policy, rule, ok := strings.Cut(value, ",")
			policy = strings.TrimSpace(policy)
			rule = strings.TrimSpace(rule)
			if !ok || policy == "" || !strings.HasPrefix(rule, "[]") {
				add(line, directive, "远程规则源格式无法仅凭 URL 确定", "使用 rule_provider 明确声明格式，再使用 []RULE-SET,名称")
				continue
			}
			rule = strings.TrimPrefix(rule, "[]")
			if rule == "FINAL" {
				out.Rules = append(out.Rules, "MATCH,"+policy)
				ruleLines = append(ruleLines, line)
				ruleDirectives = append(ruleDirectives, directive)
				continue
			}
			if rule == "" {
				add(line, directive, "规则为空", "")
				continue
			}
			kind, _, _ := strings.Cut(rule, ",")
			switch kind {
			case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "IP-CIDR", "IP-CIDR6", "GEOIP", "RULE-SET":
			default:
				add(line, directive, "不支持的内联规则类型", "使用 routing_rule 保留已有规则或使用支持的类型")
				continue
			}
			fields := strings.Split(rule, ",")
			if len(fields) != 2 && !(len(fields) == 3 && fields[2] == "no-resolve" && (kind == "IP-CIDR" || kind == "IP-CIDR6" || kind == "GEOIP" || kind == "RULE-SET")) {
				add(line, directive, "内联规则参数无效", "仅 IP、GEOIP 和 RULE-SET 支持 no-resolve")
				continue
			}
			if strings.HasSuffix(rule, ",no-resolve") {
				rule = strings.TrimSuffix(rule, ",no-resolve") + "," + policy + ",no-resolve"
			} else {
				rule += "," + policy
			}
			out.Rules = append(out.Rules, rule)
			ruleLines = append(ruleLines, line)
			ruleDirectives = append(ruleDirectives, directive)
		default:
			add(line, directive, "不支持的指令", "删除该指令或使用支持的格式")
		}
	}
	if !section {
		add(0, "section", "缺少 [custom] 区段", "")
	}
	if len(out.Groups) > maxRoutingINIGroups || len(out.RuleProviders) > maxRoutingINIProviders || len(out.Rules) > maxRoutingRules {
		add(0, "file", "配置项数量超过限制", "")
	}
	if len(out.Diagnostics) > 0 {
		return out
	}
	groupsByName := map[string]RoutingGroup{}
	for _, group := range out.Groups {
		groupsByName[group.Name] = group
	}
	for _, group := range out.Groups {
		for _, member := range group.Proxies {
			if member != "DIRECT" && member != "REJECT" {
				if _, ok := groupsByName[member]; !ok {
					add(groupLines[group.Name], "custom_proxy_group", "引用了不存在的策略组", member)
				}
			}
		}
	}
	if routingGroupsCyclic(groupsByName) {
		line := 0
		if len(out.Groups) > 0 {
			line = groupLines[out.Groups[0].Name]
		}
		add(line, "custom_proxy_group", "策略组存在循环引用", "")
	}
	providerNames := map[string]struct{}{}
	for _, provider := range out.RuleProviders {
		providerNames[provider.Name] = struct{}{}
	}
	for i, rule := range out.Rules {
		if err := validateRoutingINIRule(rule); err != nil {
			add(ruleLines[i], ruleDirectives[i], err.Error(), "请使用文档中支持的规则类型和参数")
			continue
		}
		policy, provider, err := parseRoutingRule(rule)
		if err != nil {
			add(ruleLines[i], ruleDirectives[i], "规则格式无效", "")
			continue
		}
		if policy != "DIRECT" && policy != "REJECT" {
			if _, ok := groupsByName[policy]; !ok {
				add(ruleLines[i], ruleDirectives[i], "规则引用了不存在的策略组", policy)
			}
		}
		if provider != "" {
			if _, ok := providerNames[provider]; !ok {
				add(ruleLines[i], ruleDirectives[i], "规则引用了不存在的规则源", provider)
			}
		}
	}
	if len(out.Diagnostics) > 0 {
		return out
	}
	value := RoutingPreset{Name: "INI validation", Enabled: true, Groups: out.Groups, RuleProviders: out.RuleProviders, Rules: out.Rules}
	if err := normalizeRoutingPreset(&value); err != nil {
		add(0, "validation", err.Error(), "检查策略组、规则和规则源引用")
		return out
	}
	// Keys are assigned by the service, not by validation.
	for i := range value.Groups {
		value.Groups[i].Key = ""
	}
	out.Groups = value.Groups
	out.RuleProviders = value.RuleProviders
	out.Rules = value.Rules
	for _, g := range out.Groups {
		if len(g.NodePatterns) > 0 {
			out.Warnings = append(out.Warnings, RoutingINIDiagnostic{groupLines[g.Name], "custom_proxy_group", "未指定订阅上下文，无法判断正则是否匹配节点", "保存后检查实际订阅预览"})
		}
	}
	providers := map[string]RoutingRuleProvider{}
	for _, p := range out.RuleProviders {
		providers[p.Name] = p
	}
	for i, rule := range out.Rules {
		if i < len(out.Rules)-1 && strings.TrimSpace(strings.SplitN(rule, ",", 2)[0]) == "MATCH" {
			out.Warnings = append(out.Warnings, RoutingINIDiagnostic{ruleLines[i], ruleDirectives[i], "MATCH/FINAL 后的规则不会被执行", "将最终规则移到末尾"})
		}
		if _, err := renderShadowrocketRule(rule, providers); err != nil {
			out.Warnings = append(out.Warnings, RoutingINIDiagnostic{ruleLines[i], ruleDirectives[i], fmt.Sprintf("第 %d 条规则与 Shadowrocket 不兼容：%v", i+1, err), ""})
		}
	}
	return out
}

// The INI subset does not accept arbitrary Mihomo expressions merely because
// they contain commas. More advanced existing rules remain in the preset, but
// cannot be exported until their semantics can be validated losslessly.
func validateRoutingINIRule(rule string) error {
	if _, _, err := parseRoutingRule(rule); err != nil {
		return errors.New("规则格式无效")
	}
	parts := strings.Split(rule, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	kind := parts[0]
	switch kind {
	case "MATCH":
		if len(parts) != 2 {
			return errors.New("MATCH 规则参数无效")
		}
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-WILDCARD", "DOMAIN-REGEX", "GEOSITE", "PROCESS-NAME", "PROCESS-PATH", "NETWORK", "DST-PORT", "SRC-PORT":
		if len(parts) != 3 {
			return errors.New("规则参数无效")
		}
	case "IP-CIDR", "IP-CIDR6", "GEOIP", "RULE-SET":
		if len(parts) != 3 && !(len(parts) == 4 && parts[3] == "no-resolve") {
			return errors.New("规则参数无效")
		}
		if kind == "IP-CIDR" || kind == "IP-CIDR6" {
			prefix, err := netip.ParsePrefix(parts[1])
			if err != nil || (kind == "IP-CIDR" && !prefix.Addr().Is4()) || (kind == "IP-CIDR6" && !prefix.Addr().Is6()) {
				return errors.New("IP 网段无效")
			}
		}
	default:
		return errors.New("不支持的规则类型")
	}
	return nil
}

// ExportRoutingINI emits only representations that can be read back without
// altering policy order or provider metadata.
func ExportRoutingINI(preset RoutingPreset) (string, error) {
	var b strings.Builder
	b.WriteString("[custom]\n")
	for _, g := range preset.Groups {
		if strings.ContainsAny(g.Name, "`\r\n") || g.Type != "select" {
			return "", fmt.Errorf("策略组无法无损导出")
		}
		b.WriteString("custom_proxy_group=" + g.Name + "`select")
		for _, p := range g.Proxies {
			if strings.ContainsAny(p, "`\r\n") {
				return "", fmt.Errorf("策略组成员无法无损导出")
			}
			b.WriteString("`[]" + p)
		}
		for _, pattern := range g.NodePatterns {
			if strings.ContainsAny(pattern, "`\r\n") || pattern == ".*" || strings.HasPrefix(pattern, "[]") {
				return "", fmt.Errorf("节点正则无法无损导出")
			}
			b.WriteString("`" + pattern)
		}
		if g.IncludeAll {
			b.WriteString("`.*")
		}
		if len(g.Proxies) == 0 && len(g.NodePatterns) == 0 && !g.IncludeAll {
			b.WriteString("`" + routingINIManualMembers)
		}
		b.WriteByte('\n')
	}
	for _, p := range preset.RuleProviders {
		if routingINIURLHasCredentials(p.URL) {
			return "", fmt.Errorf("规则源 URL 包含用户信息、查询参数或片段，不能导出")
		}
		if !routingINIProviderSupported(p) {
			return "", fmt.Errorf("规则源格式无法无损导出")
		}
		v, err := json.Marshal(p)
		if err != nil {
			return "", err
		}
		b.WriteString("rule_provider=")
		b.Write(v)
		b.WriteByte('\n')
	}
	for _, rule := range preset.Rules {
		v, err := json.Marshal(rule)
		if err != nil {
			return "", err
		}
		b.WriteString("routing_rule=")
		b.Write(v)
		b.WriteByte('\n')
	}
	if b.Len() > maxRoutingINIBytes {
		return "", fmt.Errorf("配置过大，无法安全导出")
	}
	result := b.String()
	parsed := ParseRoutingINI(result)
	if len(parsed.Diagnostics) > 0 || len(parsed.Groups) != len(preset.Groups) || !slices.Equal(parsed.RuleProviders, preset.RuleProviders) || !slices.Equal(parsed.Rules, preset.Rules) {
		return "", fmt.Errorf("配置无法无损导出为受支持的 INI")
	}
	for i, group := range preset.Groups {
		other := parsed.Groups[i]
		if group.Name != other.Name || group.Type != other.Type || group.IncludeAll != other.IncludeAll || !slices.Equal(group.Proxies, other.Proxies) || !slices.Equal(group.NodePatterns, other.NodePatterns) {
			return "", fmt.Errorf("策略组无法无损导出为受支持的 INI")
		}
	}
	return result, nil
}

func routingINIURLHasCredentials(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.User != nil {
		return true
	}
	// Query strings and fragments may contain credentials under arbitrary names.
	// Without a trusted schema they cannot be safely exposed in INI exports.
	return u.RawQuery != "" || u.Fragment != ""
}

func routingINIProviderSupported(p RoutingRuleProvider) bool {
	if p.Type != "http" {
		return false
	}
	switch p.Behavior {
	case "classical", "domain", "ipcidr":
	default:
		return false
	}
	switch p.Format {
	case "yaml", "text":
		return true
	case "mrs":
		return p.Behavior != "classical"
	default:
		return false
	}
}
