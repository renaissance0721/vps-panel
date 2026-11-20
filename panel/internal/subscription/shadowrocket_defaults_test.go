package subscription

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestShadowrocketPolicyDefaultsUseResolvedNamesAndKeepOtherFormats(t *testing.T) {
	groups := []RoutingGroup{
		{Key: "grp_default", Name: "Outbound", Type: "select", Proxies: []string{"DIRECT"}, IncludeAll: true},
		{Key: "grp_ai1", Name: "AI", Type: "select", Proxies: []string{"Outbound", "DIRECT"}, IncludeAll: true},
		{Key: "grp_youtube", Name: "YouTube", Type: "select", Proxies: []string{"AI", "REJECT"}, NodePatterns: []string{"🇯🇵"}},
		{Key: "grp_google", Name: "Google", Type: "select", Proxies: []string{"YouTube"}},
		{Key: "grp_apple", Name: "Apple", Type: "select", Proxies: []string{"DIRECT"}},
	}
	data := PersonalSubscriptionData{
		RoutingPreset: &RoutingPreset{Name: "Example", Groups: groups, Rules: []string{"DOMAIN,example.net,AI", "MATCH,Outbound"}},
		Nodes: []ResolvedSubscriptionNode{
			{Name: "🇯🇵 City", Protocol: "trojan", RemoteFormat: "uri", RemotePayload: "trojan://fixture@example.net:443"},
			{Name: "City", Protocol: "trojan", RemoteFormat: "uri", RemotePayload: "trojan://fixture@example.org:443"},
			{Name: "Whole source", Protocol: "trojan", RemoteFormat: "uri", RemotePayload: "trojan://fixture@example.com:443"},
		},
		NodeNames: map[int64]string{1: "🇯🇵 City", 2: "City"}, NodeGroupNames: map[int64][]string{3: {"Whole source"}},
		RoutingBindings: RoutingBindings{"grp_youtube": {3}},
	}
	baseline, err := RenderPersonalShadowrocketSubscription(data)
	if err != nil || !strings.Contains(string(baseline), "FINAL,PROXY") ||
		strings.Contains(string(baseline), "Outbound =") || strings.Contains(string(baseline), "policy-select-name") {
		t.Fatalf("native PROXY baseline: %v", err)
	}
	policies, err := resolveShadowrocketPolicies(data, *data.RoutingPreset, true)
	wantVideoOptions := []ShadowrocketPolicyOption{
		{Target: ShadowrocketPolicyTarget{Type: "group", GroupKey: "grp_ai1"}, Name: "AI"},
		{Target: ShadowrocketPolicyTarget{Type: "builtin", Policy: "REJECT"}, Name: "REJECT"},
		{Target: ShadowrocketPolicyTarget{Type: "node", NodeID: 1}, Name: "City [JP]"},
	}
	if err != nil || !slices.Equal(policies.options["grp_youtube"], wantVideoOptions) {
		t.Fatalf("options should exclude whole-source nodes and nonmembers: %+v %v", policies.options["grp_youtube"], err)
	}
	mihomoBefore, err := RenderPersonalMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	base64Before, err := RenderPersonalBase64Subscription(data)
	if err != nil {
		t.Fatal(err)
	}
	data.ShadowrocketDefaults = ShadowrocketDefaults{
		"grp_default": {Type: "node", NodeID: 2},
		"grp_ai1":     {Type: "node", NodeID: 1},
		"grp_youtube": {Type: "builtin", Policy: "REJECT"},
		"grp_google":  {Type: "group", GroupKey: "grp_youtube"},
		"grp_apple":   {Type: "builtin", Policy: "DIRECT"},
	}
	body, warning, err := RenderPersonalShadowrocketPreview(data)
	if err != nil || warning != "" {
		t.Fatalf("configured defaults: %v %s", err, warning)
	}
	for _, want := range []string{
		"Outbound = select,DIRECT,City [JP],City,Whole source,policy-select-name=City",
		"AI = select,Outbound,DIRECT,City [JP],City,Whole source,policy-select-name=City [JP]",
		"YouTube = select,AI,REJECT,City [JP],Whole source,policy-select-name=REJECT",
		"Google = select,YouTube,policy-select-name=YouTube", "Apple = select,DIRECT,policy-select-name=DIRECT", "FINAL,Outbound",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q", want)
		}
	}
	mihomoAfter, err := RenderPersonalMihomoSubscription(data)
	if err != nil || !bytes.Equal(mihomoBefore, mihomoAfter) {
		t.Fatalf("defaults changed Mihomo: %v", err)
	}
	base64After, err := RenderPersonalBase64Subscription(data)
	if err != nil || base64After != base64Before {
		t.Fatalf("defaults changed Base64: %v", err)
	}

	groups[2].Name = "Video"
	groups[3].Proxies = []string{"Video"}
	data.Nodes[0].Name, data.NodeNames[1] = `🇯🇵 New,"name"\=`, `🇯🇵 New,"name"\=`
	body, warning, err = RenderPersonalShadowrocketPreview(data)
	if err != nil || warning != "" ||
		!strings.Contains(string(body), `policy-select-name="New,\"name\"\\="`) ||
		!strings.Contains(string(body), "Google = select,Video,policy-select-name=Video") {
		t.Fatalf("stable identities or escaping failed after rename: %v %s", err, warning)
	}

	data.ShadowrocketDefaults["grp_default"] = ShadowrocketPolicyTarget{Type: "node", NodeID: 300}
	data.ShadowrocketDefaults["grp_ai1"] = ShadowrocketPolicyTarget{Type: "group", GroupKey: "grp_default"}
	data.ShadowrocketDefaults["grp_youtube"] = ShadowrocketPolicyTarget{Type: "node", NodeID: 3}
	body, warning, err = RenderPersonalShadowrocketPreview(data)
	if err != nil || !strings.Contains(warning, "2 项") ||
		!strings.Contains(string(body), "FINAL,PROXY") || !strings.Contains(string(body), "policy-select-name=PROXY") ||
		strings.Contains(string(body), "Outbound =") || strings.Contains(string(body), "policy-select-name=Whole source") {
		t.Fatalf("unavailable default fallback: %v %s", err, warning)
	}

	// A subscription literally named PROXY can keep the previous projection,
	// but cannot declare a second policy with the reserved name.
	groups[0].Name = "PROXY"
	groups[1].Proxies[0] = "PROXY"
	data.RoutingPreset.Rules[1] = "MATCH,PROXY"
	data.ShadowrocketDefaults["grp_default"] = ShadowrocketPolicyTarget{Type: "node", NodeID: 2}
	body, warning, err = RenderPersonalShadowrocketPreview(data)
	if err != nil || warning == "" || strings.Contains(string(body), "\nPROXY =") || !strings.Contains(string(body), "FINAL,PROXY") {
		t.Fatalf("reserved catch-all lost native projection: %v %s", err, warning)
	}
}
