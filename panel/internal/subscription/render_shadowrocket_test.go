package subscription

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"gopkg.in/yaml.v3"
)

func shadowrocketTestRouting() *RoutingPreset {
	return &RoutingPreset{Name: "Shared routing", Groups: []RoutingGroup{
		{Key: "grp_ai1", Name: "AI", Type: "select"},
		{Key: "grp_proxy", Name: "Proxy", Type: "select", Proxies: []string{"AI", "DIRECT", "REJECT"}},
	}, RuleProviders: []RoutingRuleProvider{{Name: "OpenAI", URL: "https://example.com/OpenAI.list",
		Type: "http", Behavior: "classical", Format: "text", Interval: 86400}},
		Rules: []string{"RULE-SET,OpenAI,AI", "GEOIP,CN,DIRECT,no-resolve", "MATCH,Proxy"}}
}

func TestShadowrocketRealityUsesNativeFieldsAndManagedFlow(t *testing.T) {
	data := SubscriptionData{RoutingPreset: defaultRoutingPresetForTest(t), Nodes: []proxystore.ClientShare{{
		Client: proxystore.Client{UUID: "test-uuid"}, DisplayName: "US1", Protocol: proxystore.ProtocolVLESS,
		Address: "203.0.113.1", Port: 443, Security: proxystore.SecurityReality,
		ServerName: "example.com", RealityPublicKey: "public-test-key", RealityShortID: "abcd",
		Fingerprint: "chrome", Flow: proxystore.ServerFlow + "-udp443",
	}}}
	body, err := RenderShadowrocketSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"[General]", "[Proxy]", "[Proxy Group]", "[Rule]",
		"US1 = vless,203.0.113.1,443,", "password=test-uuid", "tls=true", "reality=true",
		"pbk=public-test-key", "sid=abcd", "fp=chrome", "peer=example.com", "flow=xtls-rprx-vision", "udp-relay=true",
		"FINAL,🚀 默认代理", "/Surge/OpenAI/OpenAI.list"} {
		if !strings.Contains(string(body), expected) {
			t.Errorf("missing %q", expected)
		}
	}
	for _, forbidden := range []string{"public-key=", "short-id=", "client-fingerprint=", "udp443", "MATCH,", ".yaml"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("unexpected %q", forbidden)
		}
	}
	// The same resolved flow is used by Mihomo and Shadowrocket.
	resolved := resolvedNodeFromClientShare(data.Nodes[0])
	mihomo, err := renderMihomoProxy(resolved)
	if err != nil || !strings.Contains(string(body), "flow="+mihomo.Flow) {
		t.Fatal("client flows differ", err)
	}
}

func TestShadowrocketShadowsocksAndSS2022(t *testing.T) {
	for _, method := range []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"} {
		t.Run(method, func(t *testing.T) {
			line, err := renderShadowrocketProxy(ResolvedSubscriptionNode{Name: "SG-A", Protocol: proxystore.ProtocolShadowsocks,
				Address: "ss.example.com", Port: 8388, Method: method, ShadowsocksPassword: "server-key:user-key"})
			want := "SG-A = ss,ss.example.com,8388,encrypt-method=" + method + ",password=server-key:user-key,udp-relay=true"
			if err != nil || line != want {
				t.Fatalf("SS render mismatch: %v", err)
			}
		})
	}
}

func TestShadowrocketBindingsReferencesAndCustomSections(t *testing.T) {
	nodes := []ResolvedSubscriptionNode{
		{Name: "US1", Protocol: proxystore.ProtocolShadowsocks, Address: "us1.example.com", Port: 8388, Method: "aes-256-gcm", ShadowsocksPassword: "test-password"},
		{Name: "US2", Protocol: proxystore.ProtocolShadowsocks, Address: "us2.example.com", Port: 8388, Method: "aes-256-gcm", ShadowsocksPassword: "test-password"},
	}
	custom := strings.Replace(builtinShadowrocketTemplate, "ipv6 = true", "ipv6 = false", 1) + "\n[Host]\nexample.com = 127.0.0.1\n\n[URL Rewrite]\n^https://example.com/ - reject\n"
	data := PersonalSubscriptionData{Nodes: nodes, NodeNames: map[int64]string{1: "US1", 2: "US2"},
		RoutingPreset: shadowrocketTestRouting(), RoutingBindings: RoutingBindings{"grp_ai1": {1, 2, 999}},
		ShadowrocketTemplate: &SubscriptionTemplate{Type: TemplateTypeShadowrocket, Content: custom}}
	body, err := RenderPersonalShadowrocketSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AI = select,US1,US2", "Proxy = select,AI,DIRECT,REJECT",
		"RULE-SET,https://example.com/OpenAI.list,AI", "GEOIP,CN,DIRECT,no-resolve", "FINAL,Proxy",
		"ipv6 = false", "[Host]\nexample.com = 127.0.0.1", "[URL Rewrite]\n^https://example.com/ - reject"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, section := range []string{"[Proxy]", "[Proxy Group]", "[Rule]"} {
		if strings.Count(string(body), section) != 1 {
			t.Errorf("duplicated section %s", section)
		}
	}
	if strings.Contains(string(body), "{{") {
		t.Fatal("unexpanded marker")
	}
	// Rendering is pure and leaves the common logical rules/provider URL intact.
	if data.RoutingPreset.Rules[2] != "MATCH,Proxy" || data.RoutingPreset.RuleProviders[0].URL != "https://example.com/OpenAI.list" {
		t.Fatal("routing mutated")
	}
	data.RoutingBindings = nil
	if _, err := RenderPersonalShadowrocketSubscription(data); !errors.Is(err, ErrRoutingGroupEmpty) {
		t.Fatalf("empty group: %v", err)
	}
}

func TestShadowrocketNamesAndBindingsUseOneNormalization(t *testing.T) {
	originals := []string{"🇺🇸   Los Angeles Home", "🇯🇵 Tokyo", "🇸🇬 Singapore", "🇭🇰 Hong Kong", "⚡ US Premium", "🚀 🇺🇸 LA"}
	wants := []string{"Los Angeles Home", "Tokyo", "Singapore", "Hong Kong", "⚡ US Premium", "🚀 🇺🇸 LA"}
	nodes := make([]ResolvedSubscriptionNode, len(originals))
	names := make(map[int64]string, len(originals))
	for index, name := range originals {
		nodes[index] = ResolvedSubscriptionNode{Name: name, Protocol: proxystore.ProtocolShadowsocks,
			Address: "example.com", Port: 8388, Method: "aes-256-gcm", ShadowsocksPassword: "secret"}
		names[int64(index+1)] = name
	}
	routing := &RoutingPreset{Name: "Names", Groups: []RoutingGroup{
		{Key: "grp_ai01", Name: "🤖 AI", Type: "select"},
		{Key: "grp_us01", Name: "🇺🇸 美国", Type: "select", Proxies: []string{"DIRECT"}},
		{Key: "grp_proxy", Name: "🚀 默认代理", Type: "select", Proxies: []string{"🤖 AI", "🇺🇸 美国"}, IncludeAll: true},
	}, RuleProviders: shadowrocketTestRouting().RuleProviders,
		Rules: []string{"RULE-SET,OpenAI,🤖 AI", "MATCH,🚀 默认代理"}}
	data := PersonalSubscriptionData{Nodes: nodes, NodeNames: names, RoutingPreset: routing,
		RoutingBindings: RoutingBindings{"grp_ai01": {1, 2}}}
	body, err := RenderPersonalShadowrocketSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	output := string(body)
	for _, name := range wants {
		if !strings.Contains(output, "\n"+name+" = ss,example.com,8388,") {
			t.Errorf("missing unquoted proxy %q", name)
		}
	}
	for _, line := range []string{
		"🤖 AI = select,Los Angeles Home,Tokyo",
		"🇺🇸 美国 = select,DIRECT",
		"🚀 默认代理 = select,🤖 AI,🇺🇸 美国,Los Angeles Home,Tokyo,Singapore,Hong Kong,⚡ US Premium,🚀 🇺🇸 LA",
		"RULE-SET,https://example.com/OpenAI.list,🤖 AI",
		"FINAL,🚀 默认代理",
	} {
		if !strings.Contains(output, "\n"+line+"\n") {
			t.Errorf("missing unquoted line %q", line)
		}
	}
	for _, old := range originals[:4] {
		if strings.Contains(output, old) {
			t.Errorf("old flagged node name remained: %q", old)
		}
	}
	if !slices.EqualFunc(data.Nodes, originals, func(node ResolvedSubscriptionNode, original string) bool { return node.Name == original }) ||
		data.NodeNames[1] != originals[0] {
		t.Fatal("original personal names were mutated")
	}
	mihomo, err := RenderPersonalMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	var config mihomoConfig
	if err := yaml.Unmarshal(mihomo, &config); err != nil || config.Proxies[0].Name != originals[0] || config.ProxyGroups[0].Proxies[0] != originals[0] {
		t.Fatalf("Mihomo names changed: %v", err)
	}
}

func TestShadowrocketNormalizedNameCollisions(t *testing.T) {
	for _, check := range []struct {
		name, group      string
		originals, wants []string
	}{
		{"different flags", "Proxy", []string{"🇺🇸 Home", "🇯🇵 Home"}, []string{"Home [US]", "Home [JP]"}},
		{"unflagged priority", "Proxy", []string{"🇺🇸 Home", "Home"}, []string{"Home [US]", "Home"}},
		{"same country", "Proxy", []string{"🇺🇸 Home", "🇺🇸   Home"}, []string{"Home [US]", "Home [US 2]"}},
		{"generated name reserved", "Proxy", []string{"🇺🇸 Home", "🇯🇵 Home", "Home [US]"}, []string{"Home [US 2]", "Home [JP]", "Home [US]"}},
		{"later unique base", "Proxy", []string{"🇺🇸 Home", "🇯🇵 Home", "🇸🇬 Home [US]"}, []string{"Home [US 2]", "Home [JP]", "Home [US]"}},
		{"flag only", "Proxy", []string{"🇺🇸", "🇯🇵"}, []string{"US", "JP"}},
		{"flag only collision", "Proxy", []string{"🇺🇸", "US"}, []string{"US 2", "US"}},
		{"reserved", "🤖 AI", []string{"🇺🇸 DIRECT", "🇯🇵 REJECT", "🇺🇸 🤖 AI"}, []string{"DIRECT [US]", "REJECT [JP]", "🤖 AI [US]"}},
	} {
		t.Run(check.name, func(t *testing.T) {
			nodes := make([]ResolvedSubscriptionNode, len(check.originals))
			names := make(map[int64]string, len(nodes))
			for index, name := range check.originals {
				nodes[index] = ResolvedSubscriptionNode{Name: name, Protocol: proxystore.ProtocolShadowsocks,
					Address: "example.com", Port: 8388, Method: "aes-256-gcm", ShadowsocksPassword: "secret"}
				names[int64(index+1)] = name
			}
			data := PersonalSubscriptionData{Nodes: nodes, NodeNames: names, RoutingPreset: &RoutingPreset{Name: "Collision",
				Groups: []RoutingGroup{{Key: "grp_proxy", Name: check.group, Type: "select", IncludeAll: true}},
				Rules:  []string{"MATCH," + check.group}}}
			body, err := RenderPersonalShadowrocketSubscription(data)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range check.wants {
				if !strings.Contains(string(body), "\n"+want+" = ss,") {
					t.Errorf("missing proxy %q", want)
				}
			}
			group := check.group + " = select," + strings.Join(check.wants, ",")
			if !strings.Contains(string(body), "\n"+group+"\n") {
				t.Errorf("group references differ: want %q", group)
			}
			if body2, err := RenderPersonalShadowrocketSubscription(data); err != nil || string(body2) != string(body) {
				t.Fatal("non-deterministic names", err)
			}
		})
	}
}

func TestShadowrocketOriginalNameCollisionsStillFail(t *testing.T) {
	for _, check := range [][]string{{"🇺🇸 Home", "🇺🇸 Home"}, {"Proxy"}, {"DIRECT"}} {
		nodes := make([]ResolvedSubscriptionNode, len(check))
		for index, name := range check {
			nodes[index] = ResolvedSubscriptionNode{Name: name, Protocol: proxystore.ProtocolShadowsocks,
				Address: "example.com", Port: 8388, Method: "aes-256-gcm", ShadowsocksPassword: "secret"}
		}
		_, err := RenderPersonalShadowrocketSubscription(PersonalSubscriptionData{Nodes: nodes, RoutingPreset: &RoutingPreset{
			Name: "Names", Groups: []RoutingGroup{{Key: "grp_proxy", Name: "Proxy", Type: "select", IncludeAll: true}}, Rules: []string{"MATCH,Proxy"}}})
		if !errors.Is(err, ErrUnsupportedShadowrocketProtocol) {
			t.Errorf("original collision %q: %v", check, err)
		}
	}
}

func TestShadowrocketFlagNormalizationKeepsNameValidation(t *testing.T) {
	for _, name := range []string{"🇺🇸 \nBad", "🇺🇸 " + string([]byte{0xff})} {
		_, err := RenderPersonalShadowrocketSubscription(PersonalSubscriptionData{
			Nodes: []ResolvedSubscriptionNode{{Name: name, Protocol: proxystore.ProtocolShadowsocks,
				Address: "example.com", Port: 8388, Method: "aes-256-gcm", ShadowsocksPassword: "secret"}},
			RoutingPreset: &RoutingPreset{Name: "Names", Groups: []RoutingGroup{
				{Key: "grp_proxy", Name: "Proxy", Type: "select", IncludeAll: true}}, Rules: []string{"MATCH,Proxy"}},
		})
		if !errors.Is(err, ErrUnsupportedShadowrocketProtocol) || strings.Contains(err.Error(), name) {
			t.Errorf("unsafe name accepted or exposed: %v", err)
		}
	}
}

func TestShadowrocketSharedNamesDoNotChangeOtherFormats(t *testing.T) {
	const original = "🇺🇸 Los Angeles Home"
	uri := "ss://example#" + original
	data := SubscriptionData{Nodes: []proxystore.ClientShare{{DisplayName: original, URI: uri,
		Protocol: proxystore.ProtocolShadowsocks, Address: "example.com", Port: 8388,
		Method: "aes-256-gcm", ShadowsocksPassword: "secret"}},
		NodeNames:     map[int64]string{1: original},
		RoutingPreset: &RoutingPreset{Name: "Shared", Groups: []RoutingGroup{{Key: "grp_proxy", Name: "Proxy", Type: "select", IncludeAll: true}}, Rules: []string{"MATCH,Proxy"}}}
	shadowrocket, err := RenderShadowrocketSubscription(data)
	if err != nil || !strings.Contains(string(shadowrocket), "\nLos Angeles Home = ss,") ||
		!strings.Contains(string(shadowrocket), "\nProxy = select,Los Angeles Home\n") {
		t.Fatalf("shared names not normalized: %v", err)
	}
	if data.Nodes[0].DisplayName != original || data.NodeNames[1] != original {
		t.Fatal("shared source names mutated")
	}
	mihomo, err := RenderMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	var config mihomoConfig
	if err := yaml.Unmarshal(mihomo, &config); err != nil || config.Proxies[0].Name != original {
		t.Fatalf("shared Mihomo name changed: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(RenderBase64Subscription(data))
	if err != nil || string(decoded) != uri {
		t.Fatalf("Base64 URI changed: %v", err)
	}
}

func TestShadowrocketEscapingAndInjectionRejection(t *testing.T) {
	name := "🇺🇸 US, \"A\" \\ B"
	node := ResolvedSubscriptionNode{Name: name, Protocol: proxystore.ProtocolShadowsocks,
		Address: "2001:db8::1", Port: 8388, Method: "aes-256-gcm", ShadowsocksPassword: "test,\"quoted\"\\password"}
	line, err := renderShadowrocketProxy(node)
	if err != nil {
		t.Fatal(err)
	}
	quotedName := `"🇺🇸 US, \"A\" \\ B"`
	if !strings.HasPrefix(line, quotedName+" = ss,2001:db8::1,8388,") || !strings.Contains(line, `password="test,\"quoted\"\\password"`) {
		t.Fatal("unsafe quoting")
	}
	group, err := renderShadowrocketProxyGroup(resolvedRoutingGroup{Name: "AI", Type: "select", Proxies: []string{name}})
	if err != nil || group != "AI = select,"+quotedName {
		t.Fatal("declaration and reference escaping differ")
	}
	for _, bad := range []string{"line\n[Rule]\nFINAL,DIRECT", "line\rbreak", "nul\x00", "line\u2028break"} {
		node.ShadowsocksPassword = bad
		if _, err := renderShadowrocketProxy(node); !errors.Is(err, ErrUnsupportedShadowrocketProtocol) || strings.Contains(err.Error(), bad) {
			t.Fatal("unsafe parameter or secret in error")
		}
	}
	if got := injectShadowrocketSections(builtinShadowrocketTemplate, []string{"{{RULES}} = ss,example.com,1"}, nil, []string{"FINAL,DIRECT"}); !strings.Contains(got, "{{RULES}} = ss") {
		t.Fatal("node interpreted as a template")
	}
}

func TestShadowrocketIdentifiersAndValuesEscapeSeparately(t *testing.T) {
	node := ResolvedSubscriptionNode{Name: "US Los Angeles", Protocol: proxystore.ProtocolVLESS,
		Address: "edge host", Port: 443, UUID: "id, with \"quote\"\\", Security: proxystore.SecurityReality,
		ServerName: "peer with space", RealityPublicKey: "key,part", RealityShortID: "sid\"part",
		Fingerprint: "chrome profile", Flow: proxystore.ServerFlow}
	line, err := renderShadowrocketProxy(node)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`US Los Angeles = vless,"edge host",443,`, `password="id, with \"quote\"\\"`,
		`peer="peer with space"`, `pbk="key,part"`, `sid="sid\"part"`,
		`fp="chrome profile"`, `flow=xtls-rprx-vision`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("missing escaped value %q from %q", want, line)
		}
	}
	if got, err := renderShadowrocketRule("DOMAIN,example.com,🚀 默认代理", nil); err != nil || got != "DOMAIN,example.com,🚀 默认代理" {
		t.Fatalf("rule policy was quoted: %q, %v", got, err)
	}
	if shadowrocketIdent("Home [US]") != "Home [US]" || shadowrocketValue("home with space") != `"home with space"` {
		t.Fatal("identifier and value quoting diverged")
	}
}

func TestShadowrocketRulesAndProviderCompatibility(t *testing.T) {
	providers := map[string]RoutingRuleProvider{"OpenAI": shadowrocketTestRouting().RuleProviders[0]}
	for _, rule := range []string{"DOMAIN,example.com,DIRECT", "DOMAIN-SUFFIX,example.com,REJECT", "DOMAIN-KEYWORD,example,DIRECT",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve", "IP-CIDR6,2001:db8::/32,DIRECT,no-resolve", "GEOIP,CN,DIRECT,no-resolve"} {
		got, err := renderShadowrocketRule(rule, providers)
		if err != nil || got != rule {
			t.Errorf("rule %s: %v", rule, err)
		}
	}
	if got, err := renderShadowrocketRule("RULE-SET,OpenAI,AI,no-resolve", providers); err != nil || got != "RULE-SET,https://example.com/OpenAI.list,AI,no-resolve" {
		t.Fatal(got, err)
	}
	for _, rule := range []string{"GEOSITE,cn,DIRECT", "PROCESS-NAME,app,DIRECT", "DOMAIN,example.com,DIRECT,no-resolve", "RULE-SET,missing,AI", "MATCH,DIRECT,extra", "IP-CIDR,a,b,DIRECT"} {
		if _, err := renderShadowrocketRule(rule, providers); !errors.Is(err, ErrUnsupportedShadowrocketRule) {
			t.Errorf("unsupported rule %q: %v", rule, err)
		}
	}
	for _, provider := range defaultRoutingPresetForTest(t).RuleProviders {
		got, err := shadowrocketProviderURL(provider)
		want := "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Surge/" + provider.Name + "/" + provider.Name + ".list"
		if err != nil || got != want {
			t.Errorf("built-in provider %s: %v", provider.Name, err)
		}
	}
}

func TestShadowrocketTemplateValidationAndTypeIsolation(t *testing.T) {
	for _, source := range []string{"", "no section", "[General]\nfoo=bar", builtinShadowrocketTemplate + "\n[Proxy]\n{{PROXIES}}",
		strings.Replace(builtinShadowrocketTemplate, "{{PROXIES}}", "old = ss,example.com,443\n{{PROXIES}}", 1),
		strings.Replace(builtinShadowrocketTemplate, "{{PROXIES}}", "{{PROXIES}", 1),
		strings.Replace(builtinShadowrocketTemplate, "{{RULES}}", "{{PROXIES}}", 1),
		strings.Replace(builtinShadowrocketTemplate, "{{RULES}}", "{{RULES}}\n{{RULES}}", 1),
		builtinShadowrocketTemplate + "\n[Script]\nx=y", builtinShadowrocketTemplate + "\n[MITM]\nenable=true",
		"#!include https://example.com/conf\n" + builtinShadowrocketTemplate,
	} {
		if err := validateShadowrocketTemplate(source); !errors.Is(err, ErrInvalidShadowrocketTemplate) {
			t.Errorf("invalid template accepted: %v", err)
		}
	}
	if err := validateShadowrocketTemplate(strings.ReplaceAll(builtinShadowrocketTemplate, "\n", "\r\n")); err != nil {
		t.Fatal("CRLF rejected", err)
	}
	mihomo := &SubscriptionTemplate{Type: TemplateTypeMihomo, Content: "dns:\n  enable: false"}
	shadowrocket := &SubscriptionTemplate{Type: TemplateTypeShadowrocket, Content: builtinShadowrocketTemplate}
	if _, err := RenderPersonalShadowrocketSubscription(PersonalSubscriptionData{ShadowrocketTemplate: mihomo}); !errors.Is(err, ErrTemplateTypeMismatch) {
		t.Fatal(err)
	}
	if _, err := RenderPersonalMihomoSubscription(PersonalSubscriptionData{MihomoTemplate: shadowrocket}); !errors.Is(err, ErrTemplateTypeMismatch) {
		t.Fatal(err)
	}
	if _, err := renderShadowrocketProxy(ResolvedSubscriptionNode{Name: "Unknown", Address: "example.com", Port: 443, Protocol: "unsupported"}); !errors.Is(err, ErrUnsupportedShadowrocketProtocol) {
		t.Fatal(err)
	}
}

func TestShadowrocketTextProvidersUseOriginalURLWithAnyName(t *testing.T) {
	for _, name := range []string{"OpenAI", "Muse", "MetaAI", "MyCustomProvider"} {
		for _, address := range []string{"https://example.com/OpenAI.list", "http://mirror.example.com/rules", "https://example.com/rules.yaml",
			"https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/refs/heads/master/rule/Surge/Muse/Muse.list"} {
			provider := RoutingRuleProvider{Name: name, Type: "http", Behavior: "classical", Format: "text", URL: address, Interval: 86400}
			got, err := renderShadowrocketRule("RULE-SET,"+name+",AI", map[string]RoutingRuleProvider{name: provider})
			if err != nil || got != "RULE-SET,"+address+",AI" {
				t.Fatalf("%s: original URL was not used: %q, %v", name, got, err)
			}
		}
	}
}

func TestShadowrocketProviderErrorsIncludeRuleAndReason(t *testing.T) {
	for _, check := range []struct{ field, value, reason string }{
		{"type", "file", `type="file"`},
		{"format", "yaml", `format="yaml"`},
		{"legacy", "", `format="yaml"`},
		{"format", "mrs", `format="mrs"`},
		{"behavior", "domain", `behavior="domain"`},
		{"url", "ftp://example.com/rules.list", "URL 无效"},
		{"url", "https:///rules.list", "URL 无效"},
		{"url", "https://example.com/%zz", "URL 无效"},
		{"url", "https://example.com/line\nbreak", "URL 无效"},
		{"url", "https://example.com/line\u2028break", "URL 无效"},
		{"missing", "", "未找到规则源"},
	} {
		t.Run(check.field+check.value, func(t *testing.T) {
			routing := shadowrocketTestRouting()
			routing.RuleProviders[0].Name = "Muse"
			routing.Rules = []string{"DOMAIN,example.com,DIRECT", "GEOIP,CN,DIRECT", "RULE-SET,Muse,AI", "MATCH,Proxy"}
			provider := &routing.RuleProviders[0]
			switch check.field {
			case "type":
				provider.Type = check.value
			case "format":
				provider.Format = check.value
			case "legacy":
				provider.Format = "yaml"
				provider.URL = "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/OpenAI/OpenAI.yaml"
			case "behavior":
				provider.Behavior = check.value
			case "url":
				provider.URL = check.value
			case "missing":
				routing.RuleProviders = nil
			}
			_, err := RenderPersonalShadowrocketSubscription(PersonalSubscriptionData{RoutingPreset: routing,
				Nodes: []ResolvedSubscriptionNode{{Name: "US1", Protocol: proxystore.ProtocolShadowsocks, Address: "example.com", Port: 443,
					Method: "aes-256-gcm", ShadowsocksPassword: "secret-not-for-errors"}}})
			if !errors.Is(err, ErrUnsupportedShadowrocketRule) {
				t.Fatalf("missing typed rule error: %v", err)
			}
			for _, want := range []string{"第 3 条规则", `"RULE-SET,Muse,AI"`, `规则源 "Muse"`, check.reason} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing %q in %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "secret-not-for-errors") {
				t.Fatal("error exposed node credentials")
			}
		})
	}
	// Bound and escape administrator-supplied context, including malformed rules.
	routing := shadowrocketTestRouting()
	routing.Rules = []string{"GEOSITE," + strings.Repeat("x", 4096) + "\n,DIRECT"}
	_, err := RenderPersonalShadowrocketSubscription(PersonalSubscriptionData{RoutingPreset: routing})
	if !errors.Is(err, ErrUnsupportedShadowrocketRule) || len(err.Error()) > 1200 || strings.Contains(err.Error(), "\n") {
		t.Fatal("unbounded or unsafe error context", err)
	}
}
