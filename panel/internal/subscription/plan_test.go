package subscription

import (
	"errors"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/relay"
)

func TestPlanCRUDAndOrderedNodes(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	insertSubscriptionTestProxy(t, db, 10, 1, "SG-1", 443, relay.EntryHostAuto, "")
	insertSubscriptionTestProxy(t, db, 11, 1, "SG-2", 8443, relay.EntryHostAuto, "")
	first, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG-01", Mode: NodeModeDirect, TargetProxyID: 10, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG-02", Mode: NodeModeDirect, TargetProxyID: 11, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	limit := int64(100 * 1024 * 1024 * 1024)
	created, err := service.CreatePlan(t.Context(), CreatePlanInput{
		Name: " Basic ", SubscriptionTitle: " Refrain Cloud ", Enabled: true, TrafficLimitBytes: &limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Basic" || created.SubscriptionTitle != "Refrain Cloud" || !created.Enabled ||
		created.TrafficLimitBytes == nil || *created.TrafficLimitBytes != limit || len(created.Nodes) != 0 {
		t.Fatalf("created subscription plan = %+v", created)
	}

	updated, mutations, err := service.SetPlanNodes(t.Context(), created.ID, []int64{second.ID, first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 0 {
		t.Fatalf("unexpected plan node mutations = %+v", mutations)
	}
	if len(updated.Nodes) != 2 || updated.Nodes[0].ID != second.ID || updated.Nodes[0].Position != 1 ||
		updated.Nodes[1].ID != first.ID || updated.Nodes[1].Position != 2 {
		t.Fatalf("ordered plan nodes = %+v", updated.Nodes)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), created.ID, []int64{first.ID, first.ID}); !errors.Is(err, ErrInvalidPlanNodes) {
		t.Fatalf("duplicate plan nodes error = %v", err)
	}
	if _, err := service.DeletePublishedNode(t.Context(), first.ID); !errors.Is(err, ErrPublishedNodeReferenced) {
		t.Fatalf("referenced published node deletion error = %v", err)
	}

	disabled := false
	emptyTitle := "  "
	updated, mutations, err = service.UpdatePlan(t.Context(), created.ID, UpdatePlanInput{
		SubscriptionTitle: &emptyTitle, Enabled: &disabled, TrafficLimitBytesSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 0 {
		t.Fatalf("unexpected plan update mutations = %+v", mutations)
	}
	if updated.SubscriptionTitle != "" || updated.Enabled || updated.TrafficLimitBytes != nil {
		t.Fatalf("updated subscription plan = %+v", updated)
	}

	listed, err := service.ListPlans(t.Context())
	if err != nil || len(listed) != 1 || len(listed[0].Nodes) != 2 {
		t.Fatalf("listed subscription plans = %+v, error = %v", listed, err)
	}
	if err := service.DeletePlan(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM subscription_plan_nodes`, 0)
	if _, err := service.DeletePublishedNode(t.Context(), first.ID); err != nil {
		t.Fatalf("delete published node after plan deletion: %v", err)
	}
}

func TestPlanValidation(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	negative := int64(-1)
	if _, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "invalid", Enabled: true, TrafficLimitBytes: &negative}); !errors.Is(err, ErrInvalidTrafficLimit) {
		t.Fatalf("negative traffic limit error = %v", err)
	}
	if _, err := service.CreatePlan(t.Context(), CreatePlanInput{
		Name: "invalid title", SubscriptionTitle: strings.Repeat("长", 101), Enabled: true,
	}); !errors.Is(err, ErrInvalidSubscriptionTitle) {
		t.Fatalf("invalid subscription title error = %v", err)
	}
}

func TestDifferentPlansMayUseDifferentNodesForSameTargetProxy(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	insertSubscriptionTestProxy(t, db, 10, 1, "SG", 443, relay.EntryHostAuto, "")
	first, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "Half", Mode: NodeModeDirect, TargetProxyID: 10, TrafficMultiplierBP: 50, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "Double", Mode: NodeModeDirect, TargetProxyID: 10, TrafficMultiplierBP: 200, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	planA, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "A", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	planB, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "B", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), planA.ID, []int64{first.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), planB.ID, []int64{second.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), planA.ID, []int64{first.ID, second.ID}); !errors.Is(err, ErrInvalidPlanNodes) {
		t.Fatalf("same-plan duplicate target error = %v", err)
	}
}

func TestPlanDefaultsToDefaultRoutingPresetAndCanSwitch(t *testing.T) {
	_, service := newSubscriptionTestService(t)
	created, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.RoutingPresetID == nil {
		t.Fatal("new plan has no default routing preset")
	}
	defaultPreset, err := service.GetRoutingPreset(t.Context(), *created.RoutingPresetID)
	if err != nil || !defaultPreset.IsDefault {
		t.Fatalf("new plan routing preset = %+v, %v", defaultPreset, err)
	}
	custom, err := service.CreateRoutingPreset(t.Context(), CreateRoutingPresetInput{
		Name: "Custom", Enabled: true,
		Groups: []RoutingGroup{{Name: "Custom", Type: "select", Proxies: []string{"DIRECT"}}},
		Rules:  []string{"MATCH,Custom"},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err := service.UpdatePlan(t.Context(), created.ID, UpdatePlanInput{
		RoutingPresetIDSet: true, RoutingPresetID: &custom.ID,
	})
	if err != nil || updated.RoutingPresetID == nil || *updated.RoutingPresetID != custom.ID {
		t.Fatalf("switched plan routing preset = %+v, %v", updated.RoutingPresetID, err)
	}
	disabled := false
	if _, err := service.UpdateRoutingPreset(t.Context(), custom.ID, UpdateRoutingPresetInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	name := "Still works"
	if _, _, err := service.UpdatePlan(t.Context(), created.ID, UpdatePlanInput{Name: &name}); err != nil {
		t.Fatalf("update plan retaining disabled routing preset: %v", err)
	}
	other, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Other", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdatePlan(t.Context(), other.ID, UpdatePlanInput{
		RoutingPresetIDSet: true, RoutingPresetID: &custom.ID,
	}); !errors.Is(err, ErrInvalidPlanRouting) {
		t.Fatalf("new disabled routing preset selection error = %v", err)
	}
}
