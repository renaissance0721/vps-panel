package subscription

import (
	"database/sql"
	"errors"
	"testing"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func TestSubscriberReconcileCreatesReusesAndRemovesClients(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	insertSubscriptionTestServer(t, db, 2, "US", "203.0.113.20")
	sgProxy := createSubscriptionTestRealityProxy(t, db, 1, "SG Native", 443)
	usProxy := createSubscriptionTestRealityProxy(t, db, 2, "US Native", 443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")

	sgDirect, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG Direct", Mode: NodeModeDirect, TargetProxyID: sgProxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sgAlternate, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG Alternate", Mode: NodeModeDirect, TargetProxyID: sgProxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	usDirect, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "US Direct", Mode: NodeModeDirect, TargetProxyID: usProxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	validityDays := 30
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{
		Name: "Premium", Enabled: true, DefaultValidityDays: &validityDays,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, mutations, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{sgDirect.ID, sgAlternate.ID}); err != nil || len(mutations) != 0 {
		t.Fatalf("initial plan nodes mutations = %+v, error = %v", mutations, err)
	}

	updated, mutations, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 1 || mutations[0].ServerID != 1 || updated.ClientCount != 1 || updated.ExpiresAt == nil ||
		!updated.ExpiresAt.Equal(service.now().UTC().AddDate(0, 0, 30)) {
		t.Fatalf("assigned subscriber = %+v, mutations = %+v", updated, mutations)
	}
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM subscriber_clients WHERE user_id = 100`, 1)
	var sgClientID int64
	if err := db.QueryRow(`SELECT client_id FROM subscriber_clients WHERE user_id = 100 AND proxy_id = ?`, sgProxy.ID).Scan(&sgClientID); err != nil {
		t.Fatal(err)
	}
	assertSubscriberClientShape(t, db, sgClientID, 100, true)

	plan, mutations, err = service.SetPlanNodes(t.Context(), plan.ID, []int64{sgDirect.ID, sgAlternate.ID, usDirect.ID})
	if err != nil || len(mutations) != 1 || mutations[0].ServerID != 2 {
		t.Fatalf("expanded plan = %+v, mutations = %+v, error = %v", plan, mutations, err)
	}
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM subscriber_clients WHERE user_id = 100`, 2)
	var usClientID int64
	if err := db.QueryRow(`SELECT client_id FROM subscriber_clients WHERE user_id = 100 AND proxy_id = ?`, usProxy.ID).Scan(&usClientID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO client_metrics
		(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes, cycle_started_at, updated_at)
		VALUES (?, 100, 200, 12, 34, 1, 1)`, usClientID); err != nil {
		t.Fatal(err)
	}

	plan, mutations, err = service.SetPlanNodes(t.Context(), plan.ID, []int64{sgAlternate.ID})
	if err != nil || len(mutations) != 1 || mutations[0].ServerID != 2 {
		t.Fatalf("reduced plan = %+v, mutations = %+v, error = %v", plan, mutations, err)
	}
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM subscriber_clients WHERE user_id = 100`, 1)
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM clients WHERE id = ?`, 0, usClientID)
	var archivedUplink, archivedDownlink int64
	if err := db.QueryRow(`SELECT archived_uplink_bytes, archived_downlink_bytes FROM subscriber_usage WHERE user_id = 100`).
		Scan(&archivedUplink, &archivedDownlink); err != nil {
		t.Fatal(err)
	}
	if archivedUplink != 12 || archivedDownlink != 34 {
		t.Fatalf("archived usage = %d/%d", archivedUplink, archivedDownlink)
	}
	if _, mutations, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{sgDirect.ID}); err != nil || len(mutations) != 0 {
		t.Fatalf("same-target plan replacement mutations = %+v, error = %v", mutations, err)
	}
	var remainingClientID int64
	if err := db.QueryRow(`SELECT client_id FROM subscriber_clients WHERE user_id = 100`).Scan(&remainingClientID); err != nil {
		t.Fatal(err)
	}
	if remainingClientID != sgClientID {
		t.Fatalf("same target client changed from %d to %d", sgClientID, remainingClientID)
	}
}

func TestSubscriberLifecycleAndManagedClientProtection(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	sgProxy := createSubscriptionTestRealityProxy(t, db, 1, "SG", 443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	node, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG", Mode: NodeModeDirect, TargetProxyID: sgProxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Basic", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{node.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID}); err != nil {
		t.Fatal(err)
	}
	var clientID int64
	if err := db.QueryRow(`SELECT client_id FROM subscriber_clients WHERE user_id = 100`).Scan(&clientID); err != nil {
		t.Fatal(err)
	}

	proxyService := proxystore.NewService(db)
	client, err := proxyService.GetClient(t.Context(), clientID)
	if err != nil || !client.SubscriptionManaged {
		t.Fatalf("managed client = %+v, error = %v", client, err)
	}
	name := "manual edit"
	if _, _, err := proxyService.UpdateClient(t.Context(), clientID, proxystore.ClientUpdateInput{Name: &name}); !errors.Is(err, proxystore.ErrSubscriptionManagedClient) {
		t.Fatalf("generic update error = %v", err)
	}
	if _, err := proxyService.DeleteClient(t.Context(), clientID); !errors.Is(err, proxystore.ErrSubscriptionManagedClient) {
		t.Fatalf("generic delete error = %v", err)
	}
	if _, err := proxyService.AssignClient(t.Context(), clientID, nil, nil); !errors.Is(err, proxystore.ErrSubscriptionManagedClient) {
		t.Fatalf("generic assignment error = %v", err)
	}
	if _, _, err := proxyService.ResetClientTrafficWithMutation(t.Context(), clientID); !errors.Is(err, proxystore.ErrSubscriptionManagedClient) {
		t.Fatalf("generic traffic reset error = %v", err)
	}
	if _, err := proxyService.UpdateClientRelayPortCount(t.Context(), clientID, 1); !errors.Is(err, proxystore.ErrSubscriptionManagedClient) {
		t.Fatalf("generic relay port update error = %v", err)
	}

	disabled := false
	plan, mutations, err := service.UpdatePlan(t.Context(), plan.ID, UpdatePlanInput{Enabled: &disabled})
	if err != nil || plan.Enabled || len(mutations) != 1 {
		t.Fatalf("disabled plan = %+v, mutations = %+v, error = %v", plan, mutations, err)
	}
	assertSubscriberClientShape(t, db, clientID, 100, false)
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM subscriber_clients WHERE user_id = 100`, 1)

	enabled := true
	if _, _, err := service.UpdatePlan(t.Context(), plan.ID, UpdatePlanInput{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	profileDisabled := false
	if _, mutations, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{Enabled: &profileDisabled}); err != nil || len(mutations) != 1 {
		t.Fatalf("disabled profile mutations = %+v, error = %v", mutations, err)
	}
	assertSubscriberClientShape(t, db, clientID, 100, false)
	if err := service.DeletePlan(t.Context(), plan.ID); !errors.Is(err, ErrPlanReferenced) {
		t.Fatalf("referenced plan deletion error = %v", err)
	}

	oldToken := "test-token"
	newToken, err := service.RegenerateSubscriptionToken(t.Context(), 100)
	if err != nil || newToken == oldToken || len(newToken) < 43 {
		t.Fatalf("regenerated token length = %d, error = %v", len(newToken), err)
	}
	var storedToken string
	if err := db.QueryRow(`SELECT subscription_token FROM subscriber_profiles WHERE user_id = 100`).Scan(&storedToken); err != nil {
		t.Fatal(err)
	}
	if storedToken != newToken {
		t.Fatalf("stored token = %q, want regenerated token", storedToken)
	}
}

func insertSubscriptionTestSubscriber(t *testing.T, db *sql.DB, id int64, username string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (?, ?, 'hash', 'subscriber', 1, 1)`, id, username); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscriber_profiles
		(user_id, enabled, subscription_token, created_at, updated_at) VALUES (?, 1, 'test-token', 1, 1)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscriber_usage (user_id, cycle_started_at, updated_at) VALUES (?, 1, 1)`, id); err != nil {
		t.Fatal(err)
	}
}

func assertSubscriberClientShape(t *testing.T, db *sql.DB, clientID, userID int64, effective bool) {
	t.Helper()
	var assignedUserID int64
	var enabled, effectiveEnabled int
	var expiresAt, trafficLimit, billingPeriod sql.NullInt64
	var resetMode string
	if err := db.QueryRow(`SELECT assigned_user_id, enabled, effective_enabled_snapshot, expires_at,
		traffic_limit_bytes, billing_period_months, traffic_reset_mode FROM clients WHERE id = ?`, clientID).
		Scan(&assignedUserID, &enabled, &effectiveEnabled, &expiresAt, &trafficLimit, &billingPeriod, &resetMode); err != nil {
		t.Fatal(err)
	}
	if assignedUserID != userID || enabled != 1 || (effectiveEnabled != 0) != effective || expiresAt.Valid ||
		trafficLimit.Valid || billingPeriod.Valid || resetMode != proxystore.TrafficResetNever {
		t.Fatalf("subscriber client shape = assigned %d enabled %d effective %d expires %v traffic %v billing %v reset %q",
			assignedUserID, enabled, effectiveEnabled, expiresAt, trafficLimit, billingPeriod, resetMode)
	}
	assertSubscriptionCount(t, db, `SELECT COUNT(*) FROM client_relay_ports WHERE client_id = ?`, 0, clientID)
}

func createSubscriptionTestRealityProxy(t *testing.T, db *sql.DB, serverID int64, name string, port int) proxystore.Proxy {
	t.Helper()
	value, _, err := proxystore.NewService(db).Create(t.Context(), proxystore.CreateInput{
		ServerID: serverID, Name: name, ListenPort: port, EntryHostMode: proxystore.EntryHostAuto,
		Enabled: true, Security: proxystore.SecurityReality, ServerName: "www.example.com",
		RealityTarget: "www.example.com:443", FirstClientName: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
