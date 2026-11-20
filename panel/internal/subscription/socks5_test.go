package subscription

import (
	"errors"
	"strings"
	"testing"

	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	"gopkg.in/yaml.v3"
)

func TestSOCKS5PersonalImportToProviderAndFullConfig(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertPersonalTestUser(t, db, 100, "owner", "admin")
	insertPersonalTestUser(t, db, 101, "other", "vip")
	actor := PersonalSubscriptionActor{UserID: 100, Role: "admin"}
	node, err := landingstore.NewService(db).Create(t.Context(), actor.UserID, landingstore.CreateInput{NodeRole: "landing", URI: "socks5://example%40user:p%3A%23%25%22@203.0.113.10:9492#Webshare-Japan"})
	if err != nil {
		t.Fatal(err)
	}
	group, err := service.CreatePersonalSubscription(t.Context(), actor, CreatePersonalSubscriptionInput{Name: "Personal", ClientName: "owner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	upstream := "机场入口"
	group, err = service.SetPersonalSubscriptionNodes(t.Context(), actor, group.ID, []SetPersonalSubscriptionNodeInput{{SourceType: PersonalSourceLanding, SourceID: node.ID, DisplayName: "Webshare-Japan", Enabled: true, DialerProxy: &upstream}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := service.GeneratePersonalMihomoProviderData(t.Context(), group.Token)
	if err != nil || len(data.Nodes) != 1 {
		t.Fatalf("resolve: %v", err)
	}
	body, err := RenderPersonalMihomoProvider(data)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Proxies []struct {
			Name     string `yaml:"name"`
			Type     string `yaml:"type"`
			Server   string `yaml:"server"`
			Port     int    `yaml:"port"`
			Username string `yaml:"username"`
			Password string `yaml:"password"`
			UDP      *bool  `yaml:"udp"`
			Dialer   string `yaml:"dialer-proxy"`
			Cipher   string `yaml:"cipher"`
		} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(body, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Proxies) != 1 {
		t.Fatal("missing node")
	}
	proxy := output.Proxies[0]
	if proxy.Name != "Webshare-Japan" || proxy.Type != "socks5" || proxy.Server != "203.0.113.10" || proxy.Port != 9492 || proxy.Username != "example@user" || proxy.Password != "p:#%\"" || proxy.UDP == nil || *proxy.UDP || proxy.Dialer != upstream || proxy.Cipher != "" {
		t.Fatal("provider node did not preserve SOCKS5 fields")
	}
	data, err = service.GeneratePersonalSubscriptionData(t.Context(), group.Token)
	if err != nil {
		t.Fatal(err)
	}
	data.RoutingPreset = &RoutingPreset{Name: "internal", Enabled: true, Groups: []RoutingGroup{{Key: "grp_airport", Name: upstream, Type: "select", Proxies: []string{"DIRECT"}}}, Rules: []string{"MATCH,机场入口"}}
	full, err := RenderPersonalMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	var fullOutput struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(full, &fullOutput); err != nil || len(fullOutput.Proxies) != 1 || fullOutput.Proxies[0]["password"] != "p:#%\"" || fullOutput.Proxies[0]["dialer-proxy"] != upstream {
		t.Fatal("full config lost SOCKS5 authentication or chain")
	}
	if _, err := RenderPersonalBase64Subscription(data); !errors.Is(err, ErrSOCKS5Export) {
		t.Fatalf("Base64: %v", err)
	}
	rocket, err := RenderPersonalShadowrocketSubscription(data)
	if err != nil || !strings.Contains(string(rocket), `Webshare-Japan = socks5,203.0.113.10,9492,example@user,"p:#%\""`) ||
		!strings.Contains(string(rocket), "# 第 1 个节点的前置代理未写入 Shadowrocket 配置") {
		t.Fatalf("SOCKS5 conversion or chain warning failed: %v", err)
	}
	other := PersonalSubscriptionActor{UserID: 101, Role: "vip"}
	otherGroup, err := service.CreatePersonalSubscription(t.Context(), other, CreatePersonalSubscriptionInput{Name: "Other", ClientName: "other", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetPersonalSubscriptionNodes(t.Context(), other, otherGroup.ID, []SetPersonalSubscriptionNodeInput{{SourceType: PersonalSourceLanding, SourceID: node.ID, DisplayName: "Denied", Enabled: true}}); err == nil {
		t.Fatal("private node accessible to another user")
	}
}

func TestShadowrocketSOCKS5AddressAndEscaping(t *testing.T) {
	udpDisabled := false
	for _, tc := range []struct{ address, want string }{
		{"192.0.2.11", "192.0.2.11"},
		{"[2001:db8::1]", "2001:db8::1"},
		{"2001:db8::2", "2001:db8::2"},
		{"Socks.Example", "socks.example"},
	} {
		t.Run(tc.address, func(t *testing.T) {
			node := ResolvedSubscriptionNode{
				Name: `SOCKS, "备用"`, Protocol: landingstore.ProtocolSOCKS5, Address: tc.address, Port: 1080,
				SOCKSUsername: "test user", SOCKSPassword: `p,=;"\#%`, Network: "tcp", RemoteUDP: &udpDisabled,
			}
			line, err := renderShadowrocketProxy(node)
			want := `"SOCKS, \"备用\"" = socks5,` + tc.want + `,1080,"test user","p,=;\"\\#%"`
			if err != nil || line != want {
				t.Fatalf("unexpected export for fixture: %q, %v", line, err)
			}
		})
	}
}

func TestShadowrocketSOCKS5InvalidParameters(t *testing.T) {
	udpEnabled := true
	for _, tc := range []struct {
		name, message string
		change        func(*ResolvedSubscriptionNode)
	}{
		{"address", "地址或端口", func(n *ResolvedSubscriptionNode) { n.Address = "socks5://private-user:private-password@example.test" }},
		{"port", "地址或端口", func(n *ResolvedSubscriptionNode) { n.Port = 65536 }},
		{"missing password", "用户名和密码", func(n *ResolvedSubscriptionNode) { n.SOCKSPassword = "" }},
		{"invalid username", "用户名和密码", func(n *ResolvedSubscriptionNode) { n.SOCKSUsername += "\u2028" }},
		{"long password", "用户名和密码", func(n *ResolvedSubscriptionNode) { n.SOCKSPassword = strings.Repeat("密", 86) }},
		{"TLS", "普通 TCP", func(n *ResolvedSubscriptionNode) { n.TLS = true }},
		{"transport", "普通 TCP", func(n *ResolvedSubscriptionNode) { n.Network = "ws" }},
		{"UDP", "无法保留 UDP 转发", func(n *ResolvedSubscriptionNode) { n.RemoteUDP = &udpEnabled }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := ResolvedSubscriptionNode{Name: "Landing", Protocol: landingstore.ProtocolSOCKS5,
				Address: "socks.example", Port: 1080, SOCKSUsername: "private-user", SOCKSPassword: "private-password"}
			tc.change(&node)
			body, err := RenderPersonalShadowrocketSubscription(PersonalSubscriptionData{
				Nodes: []ResolvedSubscriptionNode{node}, RoutingPreset: defaultRoutingPresetForTest(t),
			})
			if body != nil || !errors.Is(err, ErrUnsupportedShadowrocketParams) ||
				!strings.Contains(err.Error(), "第 1 个节点") || !strings.Contains(err.Error(), tc.message) || strings.Contains(err.Error(), "private-") {
				t.Fatalf("expected a redacted node parameter error, got %v", err)
			}
		})
	}
}

func TestShadowrocketMixedSubscriptionPreservesRoutingINI(t *testing.T) {
	ini := ParseRoutingINI(`[custom]
custom_proxy_group=机场香港` + "`select`^🇭🇰" + `
custom_proxy_group=机场入口` + "`select`[]机场香港" + `
custom_proxy_group=主用` + "`select`!!VPS_MANUAL" + `
custom_proxy_group=备用` + "`select`!!VPS_MANUAL" + `
custom_proxy_group=默认出口` + "`select`[]主用`[]备用`[]机场入口`.*" + `
custom_proxy_group=AI` + "`select`[]主用`[]默认出口" + `
rule_provider={"name":"AI","url":"https://example.test/ai.list","type":"http","behavior":"classical","format":"text","interval":86400}
ruleset=AI,[]RULE-SET,AI
ruleset=默认出口,[]FINAL
`)
	if len(ini.Diagnostics) != 0 || len(ini.Groups) != 6 {
		t.Fatalf("invalid preset fixture: %+v", ini.Diagnostics)
	}
	ini.Groups[2].Key = "grp_primary"
	ini.Groups[3].Key = "grp_backup"
	nodes := []ResolvedSubscriptionNode{
		{Name: "🇭🇰 Airport", Protocol: "trojan", RemoteFormat: "uri", RemotePayload: "trojan://fixture@airport.example:443"},
		{Name: "Landing", Protocol: landingstore.ProtocolSOCKS5, Address: "socks.example", Port: 1080,
			SOCKSUsername: "fixture", SOCKSPassword: "test-password", DialerProxy: "机场入口"},
	}
	data := PersonalSubscriptionData{
		Nodes: nodes, NodeNames: map[int64]string{1: nodes[0].Name, 2: nodes[1].Name},
		RoutingBindings: RoutingBindings{ini.Groups[2].Key: {2}, ini.Groups[3].Key: {1}},
		RoutingPreset:   &RoutingPreset{Name: "Airport chain", Groups: ini.Groups, RuleProviders: ini.RuleProviders, Rules: ini.Rules},
	}
	rocket, err := RenderPersonalShadowrocketSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# 第 2 个节点的前置代理未写入 Shadowrocket 配置", "Airport = trojan,airport.example,443,",
		"Landing = socks5,socks.example,1080,fixture,test-password", "机场香港 = select,Airport", "机场入口 = select,机场香港",
		"主用 = select,Landing", "备用 = select,Airport", "默认出口 = select,主用,备用,机场入口,Airport,Landing", "AI = select,主用,默认出口",
		"RULE-SET,https://example.test/ai.list,AI", "FINAL,默认出口",
	} {
		if !strings.Contains(string(rocket), want) {
			t.Errorf("missing expected configuration %q", want)
		}
	}
	if strings.Contains(string(rocket), "dialer-proxy") || strings.Contains(string(rocket), "!!VPS_MANUAL") {
		t.Fatal("unconverted parameters or members in output")
	}
	mihomo, err := RenderPersonalMihomoSubscription(data)
	if err != nil || !strings.Contains(string(mihomo), "dialer-proxy: 机场入口") {
		t.Fatalf("Mihomo chain changed: %v", err)
	}
}
