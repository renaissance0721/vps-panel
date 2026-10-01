package subscription

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRoutingPresetTemplateRenderingUsesStableNodeIDs(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	proxy := createSubscriptionTestRealityProxy(t, db, 1, "Internal", 443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	node, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG-01", Mode: NodeModeDirect, TargetProxyID: proxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{
		Name: "Streaming", Enabled: true,
		Groups: []RoutingGroup{{
			ID: "streaming", Name: "Streaming", Type: "select",
			Members: []RoutingGroupMember{{Type: "published_node", PublishedNodeID: node.ID}, {Type: "direct"}},
		}},
		Rules: []RoutingRule{{Type: "DOMAIN-SUFFIX", Value: "example.com", TargetGroupID: "streaming"},
			{Type: "MATCH", TargetGroupID: "streaming"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	template, err := service.CreateTemplate(t.Context(), CreateSubscriptionTemplateInput{
		Name: "Mihomo", Enabled: true, ConfigYAML: "dns:\n  enable: true\ntun:\n  enable: false",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{
		Name: "Plan", Enabled: true, RoutingPresetID: &preset.ID, TemplateID: &template.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{node.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID}); err != nil {
		t.Fatal(err)
	}

	data, _, err := service.GenerateSubscriptionData(t.Context(), "test-token")
	if err != nil {
		t.Fatal(err)
	}
	base64Before := RenderBase64Subscription(data)
	assertRenderedRoutingName(t, data, FormatNodeDisplayName("SG-01", 100))

	name := "SG Premium"
	multiplier := 250
	if _, _, err := service.UpdatePublishedNode(t.Context(), node.ID, UpdatePublishedNodeInput{
		Name: &name, TrafficMultiplierBP: &multiplier,
	}); err != nil {
		t.Fatal(err)
	}
	updated, _, err := service.GenerateSubscriptionData(t.Context(), "test-token")
	if err != nil {
		t.Fatal(err)
	}
	assertRenderedRoutingName(t, updated, FormatNodeDisplayName(name, multiplier))
	routingOnly := updated
	routingOnly.Template = nil
	routingOnlyBody, err := RenderMihomoSubscription(routingOnly)
	if err != nil {
		t.Fatal(err)
	}
	var routingOnlyConfig map[string]any
	if err := yaml.Unmarshal(routingOnlyBody, &routingOnlyConfig); err != nil {
		t.Fatal(err)
	}
	if routingOnlyConfig["mixed-port"] != 7890 ||
		!strings.Contains(string(routingOnlyBody), "DOMAIN-SUFFIX,example.com,Streaming") ||
		strings.Contains(string(routingOnlyBody), "MATCH,🚀 默认代理") {
		t.Fatalf("routing preset did not overlay built-in Mihomo template:\n%s", routingOnlyBody)
	}
	if base64Before == RenderBase64Subscription(updated) {
		t.Fatal("renaming a node should change its URI display name")
	}
	withoutRouting := updated
	withoutRouting.RoutingPreset = nil
	withoutRouting.Template = nil
	if RenderBase64Subscription(withoutRouting) != RenderBase64Subscription(updated) {
		t.Fatal("routing preset or template changed Base64 output")
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeletePublishedNode(t.Context(), node.ID); !errors.Is(err, ErrPublishedNodeReferenced) {
		t.Fatalf("delete referenced node error = %v", err)
	}
}

func assertRenderedRoutingName(t *testing.T, data SubscriptionData, want string) {
	t.Helper()
	body, err := RenderMihomoSubscription(data)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), want) || !strings.Contains(string(body), "DOMAIN-SUFFIX,example.com,Streaming") {
		t.Fatalf("rendered Mihomo config lacks dynamic node/rule:\n%s", body)
	}
	if _, exists := parsed["dns"]; !exists {
		t.Fatalf("template DNS settings missing:\n%s", body)
	}
}

func TestSubscriptionConfigurationRejectsUnsafeOrBrokenDefinitions(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	for _, config := range []string{
		"proxies: []",
		"defaults: &defaults\n  enable: true\ndns: *defaults",
		"dns: !custom value",
	} {
		if _, err := service.CreateTemplate(t.Context(), CreateSubscriptionTemplateInput{
			Name: "Unsafe", Enabled: true, ConfigYAML: config,
		}); !errors.Is(err, ErrInvalidTemplate) {
			t.Fatalf("template %q error = %v", config, err)
		}
	}
	if _, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{
		Name: "Cycle", Enabled: true,
		Groups: []RoutingGroup{
			{ID: "a", Name: "A", Type: "select", Members: []RoutingGroupMember{{Type: "group", GroupID: "b"}}},
			{ID: "b", Name: "B", Type: "select", Members: []RoutingGroupMember{{Type: "group", GroupID: "a"}}},
		},
		Rules: []RoutingRule{{Type: "MATCH", TargetGroupID: "a"}},
	}); !errors.Is(err, ErrInvalidRoutingPreset) {
		t.Fatalf("cyclic routing preset error = %v", err)
	}
}
