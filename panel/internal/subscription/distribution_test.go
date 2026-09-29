package subscription

import (
	"errors"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/relay"
)

func TestPublishedNodeCreationRequiresAdminCreatedServers(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "Admin Private", "203.0.113.10")
	insertSubscriptionTestServer(t, db, 2, "VIP Public", "203.0.113.20")
	if _, err := db.Exec(`UPDATE servers SET visibility = 'private' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE servers SET created_by_role = 'vip', visibility = 'public' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	insertSubscriptionTestProxy(t, db, 10, 1, "Admin", 443, relay.EntryHostAuto, "")
	insertSubscriptionTestProxy(t, db, 20, 2, "VIP", 8443, relay.EntryHostAuto, "")

	created, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "Admin Private", Mode: NodeModeDirect, TargetProxyID: 10, Enabled: true,
	})
	if err != nil || !created.Distributable {
		t.Fatalf("admin private node = %+v, %v", created, err)
	}
	if _, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "VIP Direct", Mode: NodeModeDirect, TargetProxyID: 20, Enabled: true,
	}); !errors.Is(err, ErrServerNotDistributable) {
		t.Fatalf("VIP direct error = %v", err)
	}
	adminSource := int64(10)
	vipSource := int64(20)
	for name, topology := range map[string]struct {
		source int64
		target int64
	}{
		"VIP source": {source: vipSource, target: 10},
		"VIP target": {source: adminSource, target: 20},
	} {
		sourceID := topology.source
		if _, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
			Name: name, Mode: NodeModeRelay, SourceProxyID: &sourceID, TargetProxyID: topology.target, Enabled: true,
		}); !errors.Is(err, ErrServerNotDistributable) {
			t.Fatalf("%s relay error = %v", name, err)
		}
	}
}

func TestLegacyPublishedNodeCanBeMaintainedButNotNewlyDistributed(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "VIP Legacy", "203.0.113.20")
	if _, err := db.Exec(`UPDATE servers SET created_by_role = 'vip' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	proxy := createSubscriptionTestRealityProxy(t, db, 1, "VIP Legacy", 443)
	if _, err := db.Exec(`INSERT INTO subscription_published_nodes
		(id, name, mode, target_proxy_id, traffic_multiplier_bp, enabled, created_at, updated_at)
		VALUES (100, 'Legacy', 'direct', ?, 100, 1, 1, 1)`, proxy.ID); err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Legacy Plan", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscription_plan_nodes (plan_id, published_node_id, position) VALUES (?, 100, 1)`, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{100}); err != nil {
		t.Fatalf("retain legacy plan node error = %v", err)
	}
	listed, err := service.ListPublishedNodes(t.Context())
	if err != nil || len(listed) != 1 || listed[0].Distributable {
		t.Fatalf("legacy published nodes = %+v, %v", listed, err)
	}
	name := "Legacy Renamed"
	if updated, _, err := service.UpdatePublishedNode(t.Context(), 100, UpdatePublishedNodeInput{Name: &name}); err != nil || updated.Name != name {
		t.Fatalf("update legacy node = %+v, %v", updated, err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, nil); err != nil {
		t.Fatalf("remove legacy plan node error = %v", err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{100}); !errors.Is(err, ErrServerNotDistributable) {
		t.Fatalf("re-add legacy plan node error = %v", err)
	}
	if _, err := service.DeletePublishedNode(t.Context(), 100); err != nil {
		t.Fatalf("delete legacy node error = %v", err)
	}
}

func TestSubscriberReconcilePreservesLegacyClientAndNeverCreatesAnother(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "VIP Legacy", "203.0.113.20")
	if _, err := db.Exec(`UPDATE servers SET created_by_role = 'vip' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	first := createSubscriptionTestRealityProxy(t, db, 1, "Legacy One", 443)
	second := createSubscriptionTestRealityProxy(t, db, 1, "Legacy Two", 8443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Legacy Plan", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for id, proxyID := range map[int64]int64{101: first.ID, 102: second.ID} {
		if _, err := db.Exec(`INSERT INTO subscription_published_nodes
			(id, name, mode, target_proxy_id, traffic_multiplier_bp, enabled, created_at, updated_at)
			VALUES (?, 'Legacy', 'direct', ?, 100, 1, 1, 1)`, id, proxyID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO subscription_plan_nodes (plan_id, published_node_id, position) VALUES (?, ?, ?)`, plan.ID, id, id-100); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE subscriber_profiles SET plan_id = ? WHERE user_id = 100`, plan.ID); err != nil {
		t.Fatal(err)
	}
	legacyClientID := first.Clients[0].ID
	if _, err := db.Exec(`UPDATE clients SET assigned_user_id = 100 WHERE id = ?`, legacyClientID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscriber_clients (user_id, proxy_id, client_id, created_at)
		VALUES (100, ?, ?, 1)`, first.ID, legacyClientID); err != nil {
		t.Fatal(err)
	}

	if _, err := service.ReconcileSubscriber(t.Context(), 100); !errors.Is(err, ErrServerNotDistributable) {
		t.Fatalf("legacy reconcile error = %v", err)
	}
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM subscriber_clients WHERE user_id = 100 AND proxy_id = ?`, 1, first.ID)
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM subscriber_clients WHERE user_id = 100 AND proxy_id = ?`, 0, second.ID)
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM clients WHERE id = ?`, 1, legacyClientID)
}
