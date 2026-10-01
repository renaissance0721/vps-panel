package subscription

import (
	"errors"
	"slices"
	"strings"
	"testing"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"gopkg.in/yaml.v3"
)

func defaultRoutingPresetForTest(t *testing.T) *RoutingPreset {
	t.Helper()
	_, service := newSubscriptionTestService(t)
	values, err := service.ListRoutingPresets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for index := range values {
		if values[index].IsDefault {
			return &values[index]
		}
	}
	t.Fatal("default routing preset not found")
	return nil
}

func TestRenderMihomoSubscriptionUsesStructuredShares(t *testing.T) {
	data := SubscriptionData{
		Title:         "我的机场",
		RoutingPreset: defaultRoutingPresetForTest(t),
		Nodes: []proxystore.ClientShare{
			{
				Client:      proxystore.Client{UUID: "11111111-1111-1111-1111-111111111111"},
				DisplayName: "韩国: 联通 #1 🚀", Protocol: proxystore.ProtocolVLESS,
				Address: "203.0.113.10", Port: 443, Security: proxystore.SecurityReality,
				ServerName: "www.example.com", Fingerprint: proxystore.Fingerprint,
				Flow: proxystore.ServerFlow + "-udp443", RealityPublicKey: "public-key",
				RealityShortID: "short-id",
			},
			{
				DisplayName: "日本 SS", Protocol: proxystore.ProtocolShadowsocks,
				Address: "198.51.100.20", Port: 8388,
				Method:              proxystore.ShadowsocksMethodAES128GCM,
				ShadowsocksPassword: "master-password:client-password",
			},
		},
	}
	body, err := RenderMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	var parsed mihomoConfig
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse Mihomo YAML: %v\n%s", err, body)
	}
	if parsed.Mode != "rule" || len(parsed.Proxies) != 2 || len(parsed.ProxyGroups) != 8 {
		t.Fatalf("Mihomo config = %+v", parsed)
	}
	reality := parsed.Proxies[0]
	if reality.Name != data.Nodes[0].DisplayName || reality.Type != "vless" ||
		reality.Server != "203.0.113.10" || reality.Port != 443 ||
		reality.UUID != data.Nodes[0].UUID || reality.Network != proxystore.TransportTCP ||
		!reality.UDP || !reality.TLS || reality.ServerName != "www.example.com" ||
		reality.Flow != proxystore.ServerFlow || reality.ClientFingerprint != proxystore.Fingerprint ||
		reality.RealityOptions == nil || reality.RealityOptions.PublicKey != "public-key" ||
		reality.RealityOptions.ShortID != "short-id" {
		t.Fatalf("Mihomo Reality proxy = %+v", reality)
	}
	shadowsocks := parsed.Proxies[1]
	if shadowsocks.Name != data.Nodes[1].DisplayName || shadowsocks.Type != "ss" ||
		shadowsocks.Cipher != proxystore.ShadowsocksMethodAES128GCM ||
		shadowsocks.Password != "master-password:client-password" || !shadowsocks.UDP {
		t.Fatalf("Mihomo Shadowsocks proxy = %+v", shadowsocks)
	}
	wantGroups := []struct {
		name    string
		proxies []string
	}{
		{name: "🚀 默认代理", proxies: []string{data.Nodes[0].DisplayName, data.Nodes[1].DisplayName, "DIRECT"}},
		{name: "🤖 AI", proxies: []string{"🚀 默认代理", data.Nodes[0].DisplayName, data.Nodes[1].DisplayName}},
		{name: "▶️ YouTube", proxies: []string{"🚀 默认代理", data.Nodes[0].DisplayName, data.Nodes[1].DisplayName}},
		{name: "🎬 Netflix", proxies: []string{"🚀 默认代理", data.Nodes[0].DisplayName, data.Nodes[1].DisplayName}},
		{name: "✈️ Telegram", proxies: []string{"🚀 默认代理", data.Nodes[0].DisplayName, data.Nodes[1].DisplayName}},
		{name: "🎵 TikTok", proxies: []string{"🚀 默认代理", data.Nodes[0].DisplayName, data.Nodes[1].DisplayName}},
		{name: "🍎 Apple", proxies: []string{"DIRECT", "🚀 默认代理", data.Nodes[0].DisplayName, data.Nodes[1].DisplayName}},
		{name: "Ⓜ️ Microsoft", proxies: []string{"DIRECT", "🚀 默认代理", data.Nodes[0].DisplayName, data.Nodes[1].DisplayName}},
	}
	for index, want := range wantGroups {
		group := parsed.ProxyGroups[index]
		if group.Name != want.name || group.Type != "select" || !slices.Equal(group.Proxies, want.proxies) {
			t.Fatalf("Mihomo proxy group %d = %+v, want %+v", index, group, want)
		}
	}
	wantRules := []string{
		"RULE-SET,OpenAI,🤖 AI",
		"RULE-SET,Claude,🤖 AI",
		"RULE-SET,Gemini,🤖 AI",
		"RULE-SET,YouTube,▶️ YouTube",
		"RULE-SET,Netflix,🎬 Netflix",
		"RULE-SET,Telegram,✈️ Telegram",
		"RULE-SET,TikTok,🎵 TikTok",
		"RULE-SET,Apple,🍎 Apple",
		"RULE-SET,Copilot,Ⓜ️ Microsoft",
		"RULE-SET,Microsoft,Ⓜ️ Microsoft",
		"GEOIP,CN,DIRECT,no-resolve",
		"MATCH,🚀 默认代理",
	}
	if !slices.Equal(parsed.Rules, wantRules) || strings.Contains(string(body), "{{all}}") {
		t.Fatalf("Mihomo rules or placeholders = %+v\n%s", parsed.Rules, body)
	}
	assertBuiltinMihomoSettings(t, body)
}

func assertBuiltinMihomoSettings(t *testing.T, body []byte) {
	t.Helper()
	var raw map[string]any
	if err := yaml.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mixed-port", "allow-lan", "mode", "log-level", "ipv6", "unified-delay", "tcp-concurrent", "profile", "sniffer", "dns"} {
		if _, exists := raw[key]; !exists {
			t.Fatalf("built-in Mihomo setting %q missing:\n%s", key, body)
		}
	}
	var settings struct {
		MixedPort     int    `yaml:"mixed-port"`
		AllowLAN      bool   `yaml:"allow-lan"`
		Mode          string `yaml:"mode"`
		LogLevel      string `yaml:"log-level"`
		IPv6          bool   `yaml:"ipv6"`
		UnifiedDelay  bool   `yaml:"unified-delay"`
		TCPConcurrent bool   `yaml:"tcp-concurrent"`
		Profile       struct {
			StoreSelected bool `yaml:"store-selected"`
			StoreFakeIP   bool `yaml:"store-fake-ip"`
		} `yaml:"profile"`
		Sniffer struct {
			Enable bool `yaml:"enable"`
			Sniff  map[string]struct {
				Ports               []any `yaml:"ports"`
				OverrideDestination bool  `yaml:"override-destination"`
			} `yaml:"sniff"`
		} `yaml:"sniffer"`
		DNS struct {
			Enable                bool     `yaml:"enable"`
			IPv6                  bool     `yaml:"ipv6"`
			EnhancedMode          string   `yaml:"enhanced-mode"`
			FakeIPRange           string   `yaml:"fake-ip-range"`
			FakeIPFilter          []string `yaml:"fake-ip-filter"`
			DefaultNameserver     []string `yaml:"default-nameserver"`
			Nameserver            []string `yaml:"nameserver"`
			ProxyServerNameserver []string `yaml:"proxy-server-nameserver"`
		} `yaml:"dns"`
	}
	if err := yaml.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.MixedPort != 7890 || settings.AllowLAN || settings.Mode != "rule" || settings.LogLevel != "info" ||
		!settings.IPv6 || !settings.UnifiedDelay || !settings.TCPConcurrent ||
		!settings.Profile.StoreSelected || !settings.Profile.StoreFakeIP || !settings.Sniffer.Enable ||
		len(settings.Sniffer.Sniff["HTTP"].Ports) != 2 || !settings.Sniffer.Sniff["HTTP"].OverrideDestination ||
		len(settings.Sniffer.Sniff["TLS"].Ports) != 2 || len(settings.Sniffer.Sniff["QUIC"].Ports) != 2 ||
		!settings.DNS.Enable || !settings.DNS.IPv6 || settings.DNS.EnhancedMode != "fake-ip" ||
		settings.DNS.FakeIPRange != "198.18.0.1/16" ||
		!slices.Equal(settings.DNS.FakeIPFilter, []string{"*.lan", "*.local", "geosite:cn", "geosite:private"}) ||
		!slices.Equal(settings.DNS.DefaultNameserver, []string{"223.5.5.5", "119.29.29.29"}) ||
		!slices.Equal(settings.DNS.Nameserver, []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"}) ||
		!slices.Equal(settings.DNS.ProxyServerNameserver, []string{"https://223.5.5.5/dns-query"}) {
		t.Fatalf("built-in Mihomo settings = %+v", settings)
	}
}

func TestRenderMihomoTLSOmitsRealityOptions(t *testing.T) {
	body, err := RenderMihomoSubscription(SubscriptionData{RoutingPreset: defaultRoutingPresetForTest(t), Nodes: []proxystore.ClientShare{{
		Client: proxystore.Client{UUID: "uuid"}, DisplayName: "TLS 节点", Protocol: proxystore.ProtocolVLESS,
		Address: "tls.example.com", Port: 443, Security: proxystore.SecurityTLS,
		ServerName: "www.example.com", Fingerprint: proxystore.Fingerprint,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var parsed mihomoConfig
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Proxies) != 1 {
		t.Fatalf("Mihomo TLS proxy = %+v", parsed.Proxies)
	}
	proxy := parsed.Proxies[0]
	if proxy.Name != "TLS 节点" || proxy.Type != "vless" || proxy.Server != "tls.example.com" ||
		proxy.Port != 443 || proxy.UUID != "uuid" || proxy.Network != proxystore.TransportTCP ||
		!proxy.UDP || !proxy.TLS || proxy.ServerName != "www.example.com" ||
		proxy.Flow != proxystore.ServerFlow || proxy.ClientFingerprint != proxystore.Fingerprint ||
		proxy.RealityOptions != nil {
		t.Fatalf("Mihomo TLS proxy = %+v", proxy)
	}
}

func TestRenderMihomoCustomTemplateKeepsExistingSkeletonSemantics(t *testing.T) {
	body, err := RenderMihomoSubscription(SubscriptionData{
		RoutingPreset: defaultRoutingPresetForTest(t),
		Nodes: []proxystore.ClientShare{{
			Client: proxystore.Client{UUID: "uuid"}, DisplayName: "Custom", Protocol: proxystore.ProtocolVLESS,
			Address: "custom.example.com", Port: 443, Security: proxystore.SecurityTLS,
			ServerName: "custom.example.com", Fingerprint: proxystore.Fingerprint,
		}},
		Template: &SubscriptionTemplate{ConfigYAML: "dns:\n  enable: false\ntun:\n  enable: false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	var parsed mihomoConfig
	if err := yaml.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	dns := raw["dns"].(map[string]any)
	if raw["mixed-port"] != 7890 || dns["enable"] != false || len(parsed.ProxyGroups) != 8 ||
		parsed.ProxyGroups[0].Name != "🚀 默认代理" ||
		!slices.Equal(parsed.ProxyGroups[0].Proxies, []string{"Custom", "DIRECT"}) ||
		!slices.Equal(parsed.Rules, []string{
			"RULE-SET,OpenAI,🤖 AI", "RULE-SET,Claude,🤖 AI", "RULE-SET,Gemini,🤖 AI",
			"RULE-SET,YouTube,▶️ YouTube", "RULE-SET,Netflix,🎬 Netflix", "RULE-SET,Telegram,✈️ Telegram",
			"RULE-SET,TikTok,🎵 TikTok", "RULE-SET,Apple,🍎 Apple", "RULE-SET,Copilot,Ⓜ️ Microsoft",
			"RULE-SET,Microsoft,Ⓜ️ Microsoft", "GEOIP,CN,DIRECT,no-resolve", "MATCH,🚀 默认代理",
		}) {
		t.Fatalf("custom Mihomo template behavior changed:\n%s", body)
	}
}

func TestRenderMihomoCombinesTemplateProxiesAndRoutingPreset(t *testing.T) {
	node := proxystore.ClientShare{
		Client: proxystore.Client{UUID: "uuid"}, DisplayName: "Node A", Protocol: proxystore.ProtocolVLESS,
		Address: "node.example.com", Port: 443, Security: proxystore.SecurityTLS,
		ServerName: "node.example.com", Fingerprint: proxystore.Fingerprint,
	}
	custom := &SubscriptionTemplate{ConfigYAML: "mixed-port: 9999\ndns:\n  enable: false"}
	routing := &RoutingPreset{
		Name: "Plan", Enabled: true,
		Groups: []RoutingGroup{
			{Name: "Other", Type: "select", Proxies: []string{"DIRECT"}},
			{Name: "Plan", Type: "select", Proxies: []string{"Other", "DIRECT"}, NodeIDs: []int64{7, 999}, IncludeAll: true},
			{Name: "Empty", Type: "select"},
		},
		Rules: []string{"DOMAIN-SUFFIX,example.com,Plan", "MATCH,Plan"},
	}
	body, err := RenderMihomoSubscription(SubscriptionData{
		Nodes: []proxystore.ClientShare{node}, PublishedNodeNames: map[int64]string{7: "Node A"}, Template: custom,
		RoutingPreset: routing,
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed mihomoConfig
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Mode != "rule" || len(parsed.ProxyGroups) != 3 ||
		!slices.Equal(parsed.ProxyGroups[1].Proxies, []string{"Other", "DIRECT", "Node A"}) ||
		!slices.Equal(parsed.ProxyGroups[2].Proxies, []string{"DIRECT"}) ||
		!slices.Equal(parsed.Rules, []string{"DOMAIN-SUFFIX,example.com,Plan", "MATCH,Plan"}) {
		t.Fatalf("plan routing override = %+v\n%s", parsed, body)
	}
}

func TestRenderMihomoRuleSetRequiresProvider(t *testing.T) {
	routing := &RoutingPreset{Name: "Custom", Enabled: true,
		Groups: []RoutingGroup{{Name: "Custom", Type: "select", Proxies: []string{"DIRECT"}}},
		Rules:  []string{"RULE-SET,Missing,Custom", "MATCH,Custom"}}
	if _, err := RenderMihomoSubscription(SubscriptionData{RoutingPreset: routing}); !errors.Is(err, ErrInvalidRoutingPreset) {
		t.Fatalf("missing provider error = %v", err)
	}
	routing.RuleProviders = []RoutingRuleProvider{{
		Name: "Missing", Type: "http", Behavior: "classical", Format: "yaml", Interval: 86400,
		URL: "https://example.com/rules.yaml",
	}}
	if _, err := RenderMihomoSubscription(SubscriptionData{RoutingPreset: routing}); err != nil {
		t.Fatalf("valid RULE-SET render error = %v", err)
	}
}

func TestBuiltinMihomoProviders(t *testing.T) {
	body, err := RenderMihomoSubscription(SubscriptionData{RoutingPreset: defaultRoutingPresetForTest(t)})
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Providers map[string]struct {
			Type, Behavior, Format, URL string
			Interval                    int
		} `yaml:"rule-providers"`
	}
	if err := yaml.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"OpenAI", "Claude", "Gemini", "YouTube", "Netflix", "Telegram", "TikTok", "Apple", "Copilot", "Microsoft"} {
		provider, exists := value.Providers[name]
		if !exists || provider.Type != "http" || provider.Behavior != "classical" || provider.Format != "yaml" ||
			provider.Interval != 86400 || !strings.Contains(provider.URL, "/"+name+"/") {
			t.Fatalf("provider %q = %+v", name, provider)
		}
	}
}
