package subscription

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBuiltinMihomoConfigurationContainsOnlyClientBase(t *testing.T) {
	value, err := BuildMihomoConfiguration(nil)
	if err != nil {
		t.Fatal(err)
	}
	if value.YAML != builtinMihomoTemplate {
		t.Fatalf("built-in Mihomo YAML changed during parsing:\n%s", value.YAML)
	}
	var root map[string]any
	if err := yaml.Unmarshal([]byte(value.YAML), &root); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"proxies", "proxy-groups", "rule-providers", "rules"} {
		if _, exists := root[key]; exists {
			t.Fatalf("built-in Mihomo base contains %q:\n%s", key, value.YAML)
		}
	}
	for _, key := range []string{"mixed-port", "profile", "sniffer", "dns"} {
		if _, exists := root[key]; !exists {
			t.Fatalf("built-in Mihomo base is missing %q", key)
		}
	}
}

func TestBuildMihomoConfigurationAppliesOnlyBaseTemplateOverlay(t *testing.T) {
	value, err := BuildMihomoConfiguration(&SubscriptionTemplate{Type: TemplateTypeMihomo, Content: "dns:\n  enable: false\ntun:\n  enable: false"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value.YAML, "mixed-port: 7890") || !strings.Contains(value.YAML, "enable: false") {
		t.Fatalf("effective Mihomo base = %s", value.YAML)
	}
	for _, key := range []string{"proxies:", "proxy-groups:", "rule-providers:", "rules:"} {
		if strings.Contains(value.YAML, key) {
			t.Fatalf("effective Mihomo base contains routing key %q:\n%s", key, value.YAML)
		}
	}
}

func TestRoutingPresetIsLiveReferencedByPlan(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	preset, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{
		Name: "Streaming", Enabled: true,
		Groups: []RoutingGroup{{Name: "Streaming", Type: "select", Proxies: []string{"DIRECT"}}},
		Rules:  []string{"DOMAIN-SUFFIX,example.com,Streaming", "MATCH,Streaming"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{
		Name: "Plan", Enabled: true, RoutingPresetID: &preset.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	changedGroups := []RoutingGroup{{Name: "Changed", Type: "select", Proxies: []string{"REJECT"}}}
	changedRules := []string{"MATCH,Changed"}
	if _, err := service.UpdateRoutingPreset(t.Context(), preset.ID, UpdateRoutingPresetInput{
		Groups: &changedGroups, Rules: &changedRules,
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetPlan(t.Context(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RoutingPresetID == nil || *stored.RoutingPresetID != preset.ID {
		t.Fatalf("plan routing preset = %+v", stored.RoutingPresetID)
	}
	if err := service.DeleteRoutingPreset(t.Context(), preset.ID); !errors.Is(err, ErrRoutingPresetReferenced) {
		t.Fatalf("referenced routing preset deletion error = %v", err)
	}
}

func TestRoutingGroupKeysAreGeneratedAndStable(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	preset, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{
		Name: "Keys", Enabled: true,
		Groups: []RoutingGroup{{Key: "client-supplied", Name: "Old", Type: "select", Proxies: []string{"DIRECT"}}},
		Rules:  []string{"MATCH,Old"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(preset.Groups) != 1 || preset.Groups[0].Key == "" || preset.Groups[0].Key == "client-supplied" {
		t.Fatalf("generated routing group key = %+v", preset.Groups)
	}
	stableKey := preset.Groups[0].Key
	groups := []RoutingGroup{
		{Key: stableKey, Name: "Renamed", Type: "select", Proxies: []string{"DIRECT"}},
		{Name: "New", Type: "select", Proxies: []string{"Renamed"}},
	}
	rules := []string{"MATCH,Renamed"}
	preset, err = service.UpdateRoutingPreset(t.Context(), preset.ID, UpdateRoutingPresetInput{Groups: &groups, Rules: &rules})
	if err != nil {
		t.Fatal(err)
	}
	if preset.Groups[0].Key != stableKey || preset.Groups[1].Key == "" || preset.Groups[1].Key == stableKey {
		t.Fatalf("updated routing group keys = %+v", preset.Groups)
	}
}

func TestSubscriptionConfigurationRejectsUnsafeOrBrokenDefinitions(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	for _, config := range []string{
		"proxies: []",
		"proxy-groups: []",
		"rule-providers: {}",
		"rules: []",
		"defaults: &defaults\n  enable: true\ndns: *defaults",
		"dns: !custom value",
	} {
		if _, err := service.CreateTemplate(t.Context(), CreateSubscriptionTemplateInput{Type: TemplateTypeMihomo,
			Name: "Unsafe", Enabled: true, Content: config,
		}); !errors.Is(err, ErrInvalidTemplate) {
			t.Fatalf("template %q error = %v", config, err)
		}
	}
	if _, err := service.CreateTemplate(t.Context(), CreateSubscriptionTemplateInput{Type: TemplateTypeMihomo,
		Name: "Valid", Enabled: true, Content: "dns:\n  enable: false",
	}); err != nil {
		t.Fatalf("valid template error = %v", err)
	}

	validGroups := []RoutingGroup{{Name: "AI", Type: "select", Proxies: []string{"DIRECT"}}}
	for _, providersYAML := range []string{
		"defaults: &defaults\n  type: http\nOpenAI: *defaults",
		"OpenAI: !custom value",
		"- OpenAI",
		"OpenAI:\n  type: http\n  behavior: classical\n  format: yaml\n  interval: 86400\n  url: https://example.com/rules.yaml\n  unsupported: true",
	} {
		if _, err := parseRoutingRuleProvidersYAML(providersYAML); !errors.Is(err, ErrInvalidRoutingPreset) {
			t.Fatalf("rule providers %q error = %v", providersYAML, err)
		}
	}
	if _, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{
		Name: "Missing", Enabled: true, Groups: validGroups,
		Rules: []string{"RULE-SET,OpenAI,AI", "MATCH,AI"},
	}); !errors.Is(err, ErrInvalidRoutingPreset) {
		t.Fatalf("missing rule provider error = %v", err)
	}
	if _, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{
		Name: "Cycle", Enabled: true,
		Groups: []RoutingGroup{
			{Name: "A", Type: "select", Proxies: []string{"B"}},
			{Name: "B", Type: "select", Proxies: []string{"A"}},
		},
		Rules: []string{"MATCH,A"},
	}); !errors.Is(err, ErrInvalidRoutingPreset) {
		t.Fatalf("cyclic routing preset error = %v", err)
	}
}

func TestDefaultRoutingPresetCanBeEditedButNotDisabledOrDeleted(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	presets, err := service.ListRoutingPresets(t.Context())
	if err != nil || len(presets) == 0 || !presets[0].IsDefault || !presets[0].Enabled {
		t.Fatalf("default routing preset = %+v, %v", presets, err)
	}
	name := "默认分流（已编辑）"
	updated, err := service.UpdateRoutingPreset(t.Context(), presets[0].ID, UpdateRoutingPresetInput{Name: &name})
	if err != nil || updated.Name != name {
		t.Fatalf("edit default routing preset = %+v, %v", updated, err)
	}
	disabled := false
	if _, err := service.UpdateRoutingPreset(t.Context(), updated.ID, UpdateRoutingPresetInput{Enabled: &disabled}); !errors.Is(err, ErrDefaultRoutingPreset) {
		t.Fatalf("disable default routing preset error = %v", err)
	}
	if err := service.DeleteRoutingPreset(t.Context(), updated.ID); !errors.Is(err, ErrDefaultRoutingPreset) {
		t.Fatalf("delete default routing preset error = %v", err)
	}
}
