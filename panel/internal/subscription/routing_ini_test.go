package subscription

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"gopkg.in/yaml.v3"
)

func TestRoutingINIParseAndRoundTrip(t *testing.T) {
	source := "[custom]\n# test\ncustom_proxy_group=🌏 代理`select`[]DIRECT`[]🇯🇵 日本\ncustom_proxy_group=🇯🇵 日本`select`.*\nruleset=🇯🇵 日本,[]DOMAIN-SUFFIX,example.com\nruleset=DIRECT,[]GEOIP,CN\nruleset=🌏 代理,[]FINAL\n"
	parsed := ParseRoutingINI(source)
	if len(parsed.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", parsed.Diagnostics)
	}
	if !reflect.DeepEqual(parsed.Rules, []string{"DOMAIN-SUFFIX,example.com,🇯🇵 日本", "GEOIP,CN,DIRECT", "MATCH,🌏 代理"}) {
		t.Fatalf("rules lost order: %+v", parsed.Rules)
	}
	preset := RoutingPreset{Groups: parsed.Groups, RuleProviders: parsed.RuleProviders, Rules: parsed.Rules}
	ini, err := ExportRoutingINI(preset)
	if err != nil {
		t.Fatal(err)
	}
	round := ParseRoutingINI(ini)
	if len(round.Diagnostics) != 0 || !reflect.DeepEqual(parsed.Groups, round.Groups) || !reflect.DeepEqual(parsed.Rules, round.Rules) {
		t.Fatalf("round trip: %+v", round)
	}
	again, err := ExportRoutingINI(RoutingPreset{Groups: round.Groups, RuleProviders: round.RuleProviders, Rules: round.Rules})
	if err != nil || ini != again {
		t.Fatal("export not deterministic")
	}
}

func TestRoutingINIDefaultPresetRoundTrip(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	values, err := service.ListRoutingPresets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range values {
		if !p.IsDefault {
			continue
		}
		text, err := ExportRoutingINI(p)
		if err != nil {
			t.Fatal(err)
		}
		preview, err := service.PreviewRoutingINI(t.Context(), text, &p.ID)
		if err != nil || len(preview.Result.Diagnostics) > 0 {
			t.Fatalf("default preview: %+v %v", preview.Result.Diagnostics, err)
		}
		updated, _, err := service.UpdateRoutingFromINI(t.Context(), p.ID, preview.Revision, text)
		if err != nil {
			t.Fatal(err)
		}
		if RoutingINIRevision(updated) != RoutingINIRevision(p) {
			t.Fatal("default preset changed during round trip")
		}
		return
	}
	t.Fatal("default preset missing")
}

func TestRoutingINIRegexSelectionAndRendering(t *testing.T) {
	source := "[custom]\ncustom_proxy_group=入口`select`[]DIRECT`(Japan|JP)\ncustom_proxy_group=全部`select`[]入口`.*\nruleset=入口,[]DOMAIN-SUFFIX,example.test\nruleset=全部,[]FINAL\n"
	parsed := ParseRoutingINI(source)
	if len(parsed.Diagnostics) > 0 {
		t.Fatalf("regex INI: %+v", parsed.Diagnostics)
	}
	parsed.Groups[0].Key = "grp_entry"
	parsed.Groups[1].Key = "grp_all"
	preset := &RoutingPreset{Name: "Regex", Enabled: true, Groups: parsed.Groups, Rules: parsed.Rules}
	nodes := []ResolvedSubscriptionNode{{Name: "JP Tokyo", Protocol: proxystore.ProtocolShadowsocks, Address: "jp.example.test", Port: 443, Method: "aes-256-gcm", ShadowsocksPassword: "fixture"}, {Name: "Japan Osaka", Protocol: proxystore.ProtocolShadowsocks, Address: "osaka.example.test", Port: 443, Method: "aes-256-gcm", ShadowsocksPassword: "fixture"}, {Name: "jp lowercase", Protocol: proxystore.ProtocolShadowsocks, Address: "lower.example.test", Port: 443, Method: "aes-256-gcm", ShadowsocksPassword: "fixture"}}
	data := PersonalSubscriptionData{Nodes: nodes, NodeNames: map[int64]string{1: "JP Tokyo", 2: "jp lowercase"}, RoutingBindings: RoutingBindings{"grp_entry": []int64{1, 2}}, RoutingPreset: preset}
	body, err := RenderPersonalMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	var rendered mihomoConfig
	if err := yaml.Unmarshal(body, &rendered); err != nil {
		t.Fatal(err)
	}
	want := []string{"DIRECT", "JP Tokyo", "Japan Osaka", "jp lowercase"}
	if !reflect.DeepEqual(rendered.ProxyGroups[0].Proxies, want) {
		t.Fatalf("matched members: %+v", rendered.ProxyGroups[0].Proxies)
	}
	rocket, err := RenderPersonalShadowrocketSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rocket), "入口 = select,DIRECT,JP Tokyo,Japan Osaka,jp lowercase") {
		t.Fatal("Shadowrocket members differ")
	}
	// Removing or renaming a node changes automatic selection without rewriting bindings.
	data.Nodes = data.Nodes[1:]
	data.NodeNames = map[int64]string{2: "jp lowercase"}
	body, err = RenderPersonalMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	if err = yaml.Unmarshal(body, &rendered); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(rendered.ProxyGroups[0].Proxies, ","), "JP Tokyo") {
		t.Fatal("removed node remained selected")
	}
	shared := SubscriptionData{RoutingPreset: preset, Nodes: []proxystore.ClientShare{{DisplayName: "JP shared", Protocol: proxystore.ProtocolShadowsocks, Address: "shared.example.test", Port: 443, Method: "aes-256-gcm", ShadowsocksPassword: "fixture"}}}
	sharedMihomo, err := RenderMihomoSubscription(shared)
	if err != nil {
		t.Fatal(err)
	}
	var sharedRendered mihomoConfig
	if err = yaml.Unmarshal(sharedMihomo, &sharedRendered); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sharedRendered.ProxyGroups[0].Proxies, []string{"DIRECT", "JP shared"}) {
		t.Fatalf("shared matches: %+v", sharedRendered.ProxyGroups[0].Proxies)
	}
	sharedRocket, err := RenderShadowrocketSubscription(shared)
	if err != nil || !strings.Contains(string(sharedRocket), "入口 = select,DIRECT,JP shared") {
		t.Fatalf("shared Shadowrocket rendering: %v", err)
	}
	if _, err := ExportRoutingINI(*preset); err != nil {
		t.Fatal(err)
	}
	ini, err := ExportRoutingINI(*preset)
	if err != nil {
		t.Fatal(err)
	}
	again := ParseRoutingINI(ini)
	if len(again.Diagnostics) > 0 || !reflect.DeepEqual(again.Groups[0].NodePatterns, preset.Groups[0].NodePatterns) {
		t.Fatal("regex patterns did not survive INI round-trip")
	}
	if bad := ParseRoutingINI("[custom]\ncustom_proxy_group=A`select`(?!JP)\n"); len(bad.Diagnostics) == 0 {
		t.Fatal("unsupported backtracking regex accepted")
	}
}

func TestRoutingINIPatternPersistenceAndEmptyGroup(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	source := "[custom]\ncustom_proxy_group=Regions`select`(?i)(jp|us)\nruleset=Regions,[]FINAL\n"
	p, _, err := service.CreateRoutingFromINI(t.Context(), "Patterns", true, source)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := service.GetRoutingPreset(t.Context(), p.ID)
	if err != nil || !reflect.DeepEqual(loaded.Groups[0].NodePatterns, []string{"(?i)(jp|us)"}) {
		t.Fatalf("stored patterns: %+v %v", loaded.Groups, err)
	}
	groups, err := resolveRoutingGroups(loaded.Groups, nil, nil, []string{"jp north", "US west", "other"}, []string{"jp north", "US west", "other"})
	if err != nil || !reflect.DeepEqual(groups[0].Proxies, []string{"jp north", "US west"}) {
		t.Fatalf("case insensitive matching: %+v %v", groups, err)
	}
	ordered := []RoutingGroup{{Name: "Regions", Type: "select", NodePatterns: []string{"US", "jp"}}}
	groups, err = resolveRoutingGroups(ordered, nil, nil, []string{"jp north", "US west"}, []string{"jp north", "US west"})
	if err != nil || !reflect.DeepEqual(groups[0].Proxies, []string{"jp north", "US west"}) {
		t.Fatal("patterns changed subscription order")
	}
	_, err = resolveRoutingGroups(loaded.Groups, nil, nil, []string{"other"}, []string{"other"})
	if !errors.Is(err, ErrRoutingGroupEmpty) {
		t.Fatalf("empty group was accepted: %v", err)
	}
	bad := []RoutingGroup{{Key: loaded.Groups[0].Key, Name: "Regions", Type: "select", NodePatterns: []string{"(?=invalid)"}}}
	if _, err = service.UpdateRoutingPreset(t.Context(), p.ID, UpdateRoutingPresetInput{Groups: &bad}); err == nil {
		t.Fatal("invalid RE2 pattern accepted by visual update")
	}
}

func TestRoutingINIPatternCompatibilityWithOlderEditors(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	source := "[custom]\ncustom_proxy_group=A`select`[]DIRECT`JP\nruleset=A,[]FINAL\n"
	p, _, err := service.CreateRoutingFromINI(t.Context(), "Legacy", true, source)
	if err != nil {
		t.Fatal(err)
	}
	legacy := []RoutingGroup{{Key: p.Groups[0].Key, Name: "A", Type: "select", Proxies: []string{"REJECT"}}}
	p, err = service.UpdateRoutingPreset(t.Context(), p.ID, UpdateRoutingPresetInput{Groups: &legacy})
	if err != nil || !reflect.DeepEqual(p.Groups[0].NodePatterns, []string{"JP"}) {
		t.Fatal("older editor discarded regex")
	}
	cleared := "[custom]\ncustom_proxy_group=A`select`[]REJECT\nruleset=A,[]FINAL\n"
	p, _, err = service.UpdateRoutingFromINI(t.Context(), p.ID, RoutingINIRevision(p), cleared)
	if err != nil || len(p.Groups[0].NodePatterns) != 0 {
		t.Fatal("INI could not explicitly clear regex")
	}
}

func TestRoutingINIShadowrocketMatchesOriginalDisplayName(t *testing.T) {
	preset := &RoutingPreset{Name: "Names", Groups: []RoutingGroup{{Key: "grp_japan", Name: "Group", Type: "select", NodePatterns: []string{"🇯🇵"}}}, Rules: []string{"MATCH,DIRECT"}}
	data := PersonalSubscriptionData{RoutingPreset: preset, Nodes: []ResolvedSubscriptionNode{{Name: "🇯🇵 Tokyo", Protocol: proxystore.ProtocolShadowsocks, Address: "example.test", Port: 443, Method: "aes-256-gcm", ShadowsocksPassword: "fixture"}}}
	body, err := RenderPersonalShadowrocketSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Group = select,Tokyo") {
		t.Fatal("regex did not use original display name")
	}
}

func TestRoutingINIRevisionAndBindings(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	preset, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{Name: "INI", Enabled: true, Groups: []RoutingGroup{{Name: "Group", Type: "select", Proxies: []string{"DIRECT"}}}, Rules: []string{"MATCH,Group"}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Plan", Enabled: true, RoutingPresetID: &preset.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE subscription_routing_presets SET updated_at=1 WHERE id=?`, preset.ID); err != nil {
		t.Fatal(err)
	}
	bindings, _ := json.Marshal(RoutingBindings{preset.Groups[0].Key: []int64{77}})
	if _, err := db.Exec(`UPDATE subscription_plans SET routing_bindings_json=? WHERE id=?`, string(bindings), plan.ID); err != nil {
		t.Fatal(err)
	}
	ini, err := ExportRoutingINI(preset)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewRoutingINI(t.Context(), ini, &preset.ID)
	if err != nil || len(preview.Changes.InvalidBindingGroups) != 0 || len(preview.Changes.GroupsChanged) != 0 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	updated, _, err := service.UpdateRoutingFromINI(t.Context(), preset.ID, preview.Revision, ini)
	if err != nil || updated.Groups[0].Key != preset.Groups[0].Key {
		t.Fatalf("round-trip key: %+v %v", updated, err)
	}
	if RoutingINIRevision(updated) != RoutingINIRevision(preset) {
		t.Fatal("no-op round trip changed effective configuration")
	}
	var updatedAt int64
	if err := db.QueryRow(`SELECT updated_at FROM subscription_routing_presets WHERE id=?`, preset.ID).Scan(&updatedAt); err != nil || updatedAt != 1 {
		t.Fatal("no-op round trip wrote the preset")
	}
	var storedBindings string
	if err := db.QueryRow(`SELECT routing_bindings_json FROM subscription_plans WHERE id=?`, plan.ID).Scan(&storedBindings); err != nil || storedBindings != string(bindings) {
		t.Fatal("round trip changed bindings")
	}
	bad := "[custom]\ncustom_proxy_group=Other`select`[]DIRECT\nruleset=Other,[]FINAL\n"
	preview, err = service.PreviewRoutingINI(t.Context(), bad, &preset.ID)
	if err != nil || len(preview.Changes.InvalidBindingGroups) != 1 {
		t.Fatalf("missing binding warning: %+v %v", preview, err)
	}
	if _, _, err = service.UpdateRoutingFromINI(t.Context(), preset.ID, preview.Revision, bad); !errors.Is(err, ErrRoutingINIBindings) {
		t.Fatalf("lost binding was not rejected: %v", err)
	}
	name := "Changed"
	if _, err = service.UpdateRoutingPreset(t.Context(), preset.ID, UpdateRoutingPresetInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.UpdateRoutingFromINI(t.Context(), preset.ID, preview.Revision, ini); !errors.Is(err, ErrRoutingINIConflict) {
		t.Fatalf("stale revision: %v", err)
	}
}

func TestRoutingINIManualOnlyGroupRoundTrip(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	preset, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{
		Name: "Manual", Enabled: true,
		Groups: []RoutingGroup{{Name: "Bound", Type: "select", Proxies: []string{}}},
		Rules:  []string{"MATCH,Bound"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Plan", Enabled: true, RoutingPresetID: &preset.ID})
	if err != nil {
		t.Fatal(err)
	}
	bindings, _ := json.Marshal(RoutingBindings{preset.Groups[0].Key: []int64{77}})
	if _, err := db.Exec(`UPDATE subscription_plans SET routing_bindings_json=? WHERE id=?`, string(bindings), plan.ID); err != nil {
		t.Fatal(err)
	}
	text, err := ExportRoutingINI(preset)
	if err != nil || !strings.Contains(text, "`!!VPS_MANUAL") {
		t.Fatalf("manual export failed: %v", err)
	}
	preview, err := service.PreviewRoutingINI(t.Context(), text, &preset.ID)
	if err != nil || len(preview.Result.Diagnostics) != 0 || len(preview.Changes.GroupsChanged) != 0 {
		t.Fatalf("manual preview: %+v %v", preview, err)
	}
	updated, _, err := service.UpdateRoutingFromINI(t.Context(), preset.ID, preview.Revision, text)
	if err != nil || RoutingINIRevision(updated) != RoutingINIRevision(preset) || updated.Groups[0].Key != preset.Groups[0].Key {
		t.Fatalf("manual round trip failed: %v", err)
	}
	var after string
	if err := db.QueryRow(`SELECT routing_bindings_json FROM subscription_plans WHERE id=?`, plan.ID).Scan(&after); err != nil || after != string(bindings) {
		t.Fatal("manual binding changed")
	}
	groups, err := resolveRoutingGroups(updated.Groups, RoutingBindings{updated.Groups[0].Key: []int64{77}}, map[int64]string{77: "Bound node"}, []string{"Bound node"}, []string{"Bound node"})
	if err != nil || len(groups) != 1 || !reflect.DeepEqual(groups[0].Proxies, []string{"Bound node"}) {
		t.Fatalf("manual binding no longer renders: %+v, %v", groups, err)
	}
	if got := ParseRoutingINI("[custom]\ncustom_proxy_group=Bound`select`!!VPS_MANUAL`[]DIRECT\n"); len(got.Diagnostics) == 0 {
		t.Fatal("accepted mixed manual marker")
	}
}

func TestRoutingINIConcurrentUpdatesDoNotOverwrite(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	first := "[custom]\ncustom_proxy_group=A`select`[]DIRECT\nruleset=A,[]FINAL\n"
	second := "[custom]\ncustom_proxy_group=A`select`[]REJECT\nruleset=A,[]FINAL\n"
	third := "[custom]\ncustom_proxy_group=A`select`[]DIRECT\nruleset=DIRECT,[]GEOIP,CN\nruleset=A,[]FINAL\n"
	p, _, err := service.CreateRoutingFromINI(t.Context(), "Concurrent", true, first)
	if err != nil {
		t.Fatal(err)
	}
	revision := RoutingINIRevision(p)
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, source := range []string{second, third} {
		go func(s string) {
			<-start
			_, _, err := service.UpdateRoutingFromINI(t.Context(), p.ID, revision, s)
			results <- err
		}(source)
	}
	close(start)
	a, b := <-results, <-results
	if (a == nil) == (b == nil) {
		t.Fatalf("expected exactly one successful update: %v / %v", a, b)
	}
	stored, err := service.GetRoutingPreset(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Groups) != 1 || (stored.Groups[0].Proxies[0] != "DIRECT" && stored.Groups[0].Proxies[0] != "REJECT") {
		t.Fatal("invalid resulting preset")
	}
}

func TestRoutingINIRejectsUnsupportedAndInvalid(t *testing.T) {
	for _, source := range []string{
		"[custom]\ncustom_proxy_group=A`url-test`.*",
		"[custom]\nunknown=anything",
		"[custom]\ncustom_proxy_group=A`select`[]B",
		"[custom]\ncustom_proxy_group=A`select`[]B\ncustom_proxy_group=B`select`[]A",
		"[custom]\ncustom_proxy_group=A`select`[]DIRECT\ncustom_proxy_group=A`select`[]REJECT",
		"[custom]\ncustom_proxy_group=A`select`[]DIRECT\nruleset=A,https://example.com/rules.list",
		"[custom]\ncustom_proxy_group=A`select`[]DIRECT\nruleset=A,[]UNSUPPORTED,example",
		"[custom]\ncustom_proxy_group=A`select`[]DIRECT\nruleset=A,[]DOMAIN,example,no-resolve",
		strings.Repeat("x", maxRoutingINIBytes+1),
	} {
		if got := ParseRoutingINI(source); len(got.Diagnostics) == 0 {
			t.Fatalf("accepted invalid input %q", source[:min(len(source), 100)])
		}
	}
}

func TestRoutingINIRulesAndDiagnostics(t *testing.T) {
	source := "[custom]\ncustom_proxy_group=A`select`[]DIRECT\nruleset=A,[]GEOIP,CN,no-resolve\nruleset=DIRECT,[]FINAL\n"
	parsed := ParseRoutingINI(source)
	if len(parsed.Diagnostics) != 0 || !reflect.DeepEqual(parsed.Rules, []string{"GEOIP,CN,A,no-resolve", "MATCH,DIRECT"}) {
		t.Fatalf("rules: %+v", parsed)
	}
	invalid := ParseRoutingINI("[custom]\ncustom_proxy_group=A`select`[]Missing\nruleset=Unknown,[]FINAL\n")
	if len(invalid.Diagnostics) != 2 || invalid.Diagnostics[0].Line != 2 || invalid.Diagnostics[1].Line != 3 {
		t.Fatalf("line diagnostics: %+v", invalid.Diagnostics)
	}
	provider := RoutingRuleProvider{Name: "rules", URL: "https://example.test/data", Type: "http", Behavior: "classical", Format: "text", Interval: 86400}
	preset := RoutingPreset{Groups: parsed.Groups, Rules: parsed.Rules, RuleProviders: []RoutingRuleProvider{provider}}
	text, err := ExportRoutingINI(preset)
	if err != nil {
		t.Fatal(err)
	}
	round := ParseRoutingINI(text)
	if len(round.Diagnostics) > 0 || !reflect.DeepEqual(round.RuleProviders, preset.RuleProviders) {
		t.Fatalf("provider lost: %+v", round)
	}
	if len(ParseRoutingINI("[custom]\ncustom_proxy_group=A`select`[]DIRECT\nrule_provider={} {}\n").Diagnostics) == 0 {
		t.Fatal("accepted trailing JSON")
	}
	unsafe := `[custom]
custom_proxy_group=A` + "`select`[]DIRECT" + `
rule_provider={"name":"unsafe","url":"https://user:secret@example.test/rules","type":"http","behavior":"classical","format":"text","interval":60}
`
	if len(ParseRoutingINI(unsafe).Diagnostics) == 0 {
		t.Fatal("accepted provider credentials")
	}
	unsupported := `[custom]
custom_proxy_group=A` + "`select`[]DIRECT" + `
rule_provider={"name":"bad","url":"https://example.test/rules","type":"http","behavior":"classical","format":"mrs","interval":60}
`
	if d := ParseRoutingINI(unsupported).Diagnostics; len(d) == 0 || d[0].Line != 3 {
		t.Fatal("unsupported provider format was accepted or misplaced")
	}
	provider.URL = "https://example.test/rules?token=secret"
	preset.RuleProviders = []RoutingRuleProvider{provider}
	if _, err := ExportRoutingINI(preset); err == nil {
		t.Fatal("exported provider credential")
	}
	provider.URL = "https://example.test/rules?opaque=value"
	preset.RuleProviders = []RoutingRuleProvider{provider}
	if _, err := ExportRoutingINI(preset); err == nil {
		t.Fatal("exported an opaque query string")
	}
	preset.RuleProviders = nil
	preset.Groups[0].NodePatterns = []string{" JP "}
	if _, err := ExportRoutingINI(preset); err == nil {
		t.Fatal("export changed meaningful regex whitespace")
	}
}

func TestRoutingINIRejectsUnknownOrInvalidRules(t *testing.T) {
	prefix := "[custom]\ncustom_proxy_group=A`select`[]DIRECT\n"
	for _, input := range []string{
		`routing_rule="UNKNOWN,example,A"`,
		`routing_rule="IP-CIDR,not-a-network,A"`,
		`routing_rule="DOMAIN,example,A,no-resolve"`,
		`routing_rule="AND,((DOMAIN,example)),A"`,
	} {
		got := ParseRoutingINI(prefix + input + "\n")
		if len(got.Diagnostics) == 0 || got.Diagnostics[0].Line != 3 {
			t.Fatalf("invalid rule accepted or wrong line: %q, %+v", input, got.Diagnostics)
		}
		if got.Diagnostics[0].Directive != "routing_rule" {
			t.Fatalf("wrong diagnostic directive: %+v", got.Diagnostics[0])
		}
	}
	badInline := ParseRoutingINI(prefix + "ruleset=A,[]IP-CIDR,not-a-network\n")
	if len(badInline.Diagnostics) == 0 || badInline.Diagnostics[0].Line != 3 || badInline.Diagnostics[0].Directive != "ruleset" {
		t.Fatalf("wrong inline diagnostic: %+v", badInline.Diagnostics)
	}
	valid := ParseRoutingINI(prefix + "routing_rule=\"GEOSITE,cn,A\"\nruleset=A,[]FINAL\n")
	if len(valid.Diagnostics) != 0 || len(valid.Warnings) == 0 {
		t.Fatalf("Mihomo-only rule was not warned: %+v", valid)
	}
	afterFinal := ParseRoutingINI(prefix + "ruleset=A,[]FINAL\nruleset=DIRECT,[]GEOIP,CN\n")
	if len(afterFinal.Diagnostics) != 0 || len(afterFinal.Warnings) == 0 || afterFinal.Warnings[0].Line != 3 {
		t.Fatal("missing unreachable-rule warning")
	}
}

func TestRoutingINIValidationDoesNotFetchRulesets(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	source := fmt.Sprintf("[custom]\ncustom_proxy_group=A`select`[]DIRECT\nrule_provider={\"name\":\"test\",\"url\":%q,\"type\":\"http\",\"behavior\":\"classical\",\"format\":\"text\",\"interval\":60}\nruleset=A,[]RULE-SET,test\n", server.URL)
	if result := ParseRoutingINI(source); len(result.Diagnostics) != 0 {
		t.Fatalf("valid provider: %+v", result.Diagnostics)
	}
	if called {
		t.Fatal("validation fetched the provider")
	}
}

func TestRoutingINIMaxSizeFitsEscapedMCPRequest(t *testing.T) {
	prefix := "[custom]\ncustom_proxy_group=A`select`[]DIRECT\n# "
	source := prefix + strings.Repeat("<", maxRoutingINIBytes-len(prefix))
	if result := ParseRoutingINI(source); len(result.Diagnostics) != 0 {
		t.Fatalf("max-size INI rejected: %+v", result.Diagnostics)
	}
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "update_routing_from_ini", "arguments": map[string]any{"id": 1, "revision": strings.Repeat("a", 64), "source": source}}})
	if err != nil || len(request) >= 64<<10 {
		t.Fatalf("escaped MCP request too large: %d, %v", len(request), err)
	}
}
