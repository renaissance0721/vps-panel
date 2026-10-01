package subscription

import (
	"errors"
	"slices"
	"testing"
)

func TestRoutingPresetIsCopiedIntoPlan(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	proxy := createSubscriptionTestRealityProxy(t, db, 1, "Internal", 443)
	node, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG-01", Mode: NodeModeDirect, TargetProxyID: proxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{
		Name: "Streaming", Enabled: true,
		Groups: []RoutingGroup{{Name: "Streaming", Type: "select", Proxies: []string{"DIRECT"}, NodeIDs: []int64{node.ID}}},
		Rules:  []string{"DOMAIN-SUFFIX,example.com,Streaming", "MATCH,Streaming"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{
		Name: "Plan", Enabled: true, RoutingGroups: preset.Groups, RoutingRules: preset.Rules,
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
	if err := service.DeleteRoutingPreset(t.Context(), preset.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetPlan(t.Context(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.RoutingGroups) != 1 || stored.RoutingGroups[0].Name != "Streaming" ||
		!slices.Equal(stored.RoutingRules, []string{"DOMAIN-SUFFIX,example.com,Streaming", "MATCH,Streaming"}) {
		t.Fatalf("plan routing changed with preset = %+v / %+v", stored.RoutingGroups, stored.RoutingRules)
	}
}

func TestSubscriptionConfigurationRejectsUnsafeOrBrokenDefinitions(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	for _, config := range []string{
		"proxies: []",
		"proxy-groups: []",
		"rules: []",
		"defaults: &defaults\n  enable: true\ndns: *defaults",
		"dns: !custom value",
	} {
		if _, err := service.CreateTemplate(t.Context(), CreateSubscriptionTemplateInput{
			Name: "Unsafe", Enabled: true, ConfigYAML: config,
		}); !errors.Is(err, ErrInvalidTemplate) {
			t.Fatalf("template %q error = %v", config, err)
		}
	}
	for _, config := range []string{
		"dns:\n  enable: false",
		"proxy-groups:\n  - name: Custom\n    type: select\n    proxies: [DIRECT]\nrules:\n  - MATCH,Custom",
	} {
		if _, err := service.CreateTemplate(t.Context(), CreateSubscriptionTemplateInput{
			Name: "Valid", Enabled: true, ConfigYAML: config,
		}); err != nil {
			t.Fatalf("valid template %q error = %v", config, err)
		}
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

func TestUpdatePlanRoutingMustBePaired(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Plan", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdatePlan(t.Context(), plan.ID, UpdatePlanInput{
		RoutingGroupsSet: true,
		RoutingGroups:    []RoutingGroup{{Name: "Default", Type: "select", Proxies: []string{"DIRECT"}}},
	}); !errors.Is(err, ErrInvalidPlanRouting) {
		t.Fatalf("unpaired plan routing error = %v", err)
	}
}
