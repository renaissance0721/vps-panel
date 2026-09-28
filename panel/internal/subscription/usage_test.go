package subscription

import (
	"database/sql"
	"math"
	"testing"
	"time"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func TestSubscriberUsageSaturatesAcrossClientsAndArchive(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	insertSubscriptionTestServer(t, db, 2, "US", "203.0.113.20")
	sgProxy := createSubscriptionTestRealityProxy(t, db, 1, "SG", 443)
	usProxy := createSubscriptionTestRealityProxy(t, db, 2, "US", 443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	sgNode, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG", Mode: NodeModeDirect, TargetProxyID: sgProxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	usNode, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "US", Mode: NodeModeDirect, TargetProxyID: usProxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Unlimited", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{sgNode.ID, usNode.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID}); err != nil {
		t.Fatal(err)
	}
	for _, clientID := range subscriberClientIDsByServer(t, db, 100) {
		if _, err := db.Exec(`INSERT INTO client_metrics
			(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes, cycle_started_at, updated_at)
			VALUES (?, ?, 0, ?, 0, 1, 1)`, clientID, int64(math.MaxInt64), int64(math.MaxInt64)); err != nil {
			t.Fatal(err)
		}
	}
	value, err := service.GetSubscriber(t.Context(), 100)
	if err != nil || value.UsedBytes != math.MaxInt64 {
		t.Fatalf("saturated current usage = %+v, error = %v", value, err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	value, err = service.GetSubscriber(t.Context(), 100)
	if err != nil || value.UsedBytes != math.MaxInt64 || value.ClientCount != 0 {
		t.Fatalf("saturated archived usage = %+v, error = %v", value, err)
	}
}

func TestSubscriberGlobalTrafficAcrossServersAndManualReset(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	insertSubscriptionTestServer(t, db, 2, "US", "203.0.113.20")
	sgProxy := createSubscriptionTestRealityProxy(t, db, 1, "SG", 443)
	usProxy := createSubscriptionTestRealityProxy(t, db, 2, "US", 443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	sgNode, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG", Mode: NodeModeDirect, TargetProxyID: sgProxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	usNode, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "US", Mode: NodeModeDirect, TargetProxyID: usProxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(100)
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{
		Name: "100 bytes", Enabled: true, TrafficLimitBytes: &limit,
		TrafficResetMode: ResetModeMonthly, TrafficResetDay: 1, TrafficResetTime: "00:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{sgNode.ID, usNode.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID}); err != nil {
		t.Fatal(err)
	}
	clientIDs := subscriberClientIDsByServer(t, db, 100)

	mutations, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{
		{ClientID: clientIDs[1], UplinkBytes: 100, DownlinkBytes: 200},
	})
	if err != nil || len(mutations) != 0 {
		t.Fatalf("first SG baseline mutations = %+v, error = %v", mutations, err)
	}
	mutations, err = service.RecordClientTraffic(t.Context(), 2, []proxystore.ClientTrafficReport{
		{ClientID: clientIDs[2], UplinkBytes: 1000, DownlinkBytes: 2000},
	})
	if err != nil || len(mutations) != 0 {
		t.Fatalf("first US baseline mutations = %+v, error = %v", mutations, err)
	}
	value, err := service.GetSubscriber(t.Context(), 100)
	if err != nil || value.UsedBytes != 0 {
		t.Fatalf("subscriber after baselines = %+v, error = %v", value, err)
	}

	mutations, err = service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{
		{ClientID: clientIDs[1], UplinkBytes: 130, DownlinkBytes: 220},
	})
	if err != nil || len(mutations) != 0 {
		t.Fatalf("below-quota SG mutations = %+v, error = %v", mutations, err)
	}
	mutations, err = service.RecordClientTraffic(t.Context(), 2, []proxystore.ClientTrafficReport{
		{ClientID: clientIDs[2], UplinkBytes: 1040, DownlinkBytes: 2020},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 2 || mutations[0].ServerID != 1 || mutations[1].ServerID != 2 {
		t.Fatalf("quota transition mutations = %+v", mutations)
	}
	value, err = service.GetSubscriber(t.Context(), 100)
	if err != nil || value.UsedBytes != 110 || value.Active || value.Status != SubscriberStatusExhausted {
		t.Fatalf("exhausted subscriber = %+v, error = %v", value, err)
	}
	assertSubscriberClientShape(t, db, clientIDs[1], 100, false)
	assertSubscriberClientShape(t, db, clientIDs[2], 100, false)

	value, mutations, err = service.ResetSubscriberTraffic(t.Context(), 100)
	if err != nil || value.UsedBytes != 0 || !value.Active || len(mutations) != 2 {
		t.Fatalf("manual reset subscriber = %+v, mutations = %+v, error = %v", value, mutations, err)
	}
	assertSubscriberClientShape(t, db, clientIDs[1], 100, true)
	assertSubscriberClientShape(t, db, clientIDs[2], 100, true)
	assertRawSubscriberCounters(t, db, clientIDs[1], 130, 220)
	assertRawSubscriberCounters(t, db, clientIDs[2], 1040, 2020)
}

func TestSubscriberMonthlyResetAndExpiryReconcile(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	service.now = func() time.Time {
		return time.Date(2026, time.January, 15, 12, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60)).UTC()
	}
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	proxyValue := createSubscriptionTestRealityProxy(t, db, 1, "SG", 443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	node, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG", Mode: NodeModeDirect, TargetProxyID: proxyValue.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(10)
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{
		Name: "Monthly", Enabled: true, TrafficLimitBytes: &limit,
		TrafficResetMode: ResetModeMonthly, TrafficResetDay: 1, TrafficResetTime: "00:00",
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
	clientID := subscriberClientIDsByServer(t, db, 100)[1]
	if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{ClientID: clientID, UplinkBytes: 100, DownlinkBytes: 100}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{ClientID: clientID, UplinkBytes: 106, DownlinkBytes: 105}}); err != nil {
		t.Fatal(err)
	}
	assertSubscriberClientShape(t, db, clientID, 100, false)

	service.now = func() time.Time {
		return time.Date(2026, time.February, 2, 12, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60)).UTC()
	}
	mutations, err := service.ReconcileSubscriber(t.Context(), 100)
	if err != nil || len(mutations) != 1 {
		t.Fatalf("monthly reconcile mutations = %+v, error = %v", mutations, err)
	}
	value, err := service.GetSubscriber(t.Context(), 100)
	if err != nil || value.UsedBytes != 0 || !value.Active {
		t.Fatalf("monthly reset subscriber = %+v, error = %v", value, err)
	}
	assertSubscriberClientShape(t, db, clientID, 100, true)
	assertRawSubscriberCounters(t, db, clientID, 106, 105)

	expiresAt := service.now().Add(time.Hour)
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{ExpiresAtSet: true, ExpiresAt: &expiresAt}); err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return expiresAt.Add(time.Second) }
	mutations, err = service.ReconcileSubscriber(t.Context(), 100)
	if err != nil || len(mutations) != 1 {
		t.Fatalf("expiry reconcile mutations = %+v, error = %v", mutations, err)
	}
	value, err = service.GetSubscriber(t.Context(), 100)
	if err != nil || value.Active || value.Status != SubscriberStatusExpired {
		t.Fatalf("expired subscriber = %+v, error = %v", value, err)
	}
	assertSubscriberClientShape(t, db, clientID, 100, false)
}

func subscriberClientIDsByServer(t *testing.T, db interface {
	Query(string, ...any) (*sql.Rows, error)
}, userID int64) map[int64]int64 {
	t.Helper()
	rows, err := db.Query(`SELECT proxies.server_id, mapping.client_id FROM subscriber_clients AS mapping
		JOIN proxies ON proxies.id = mapping.proxy_id WHERE mapping.user_id = ?`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values := make(map[int64]int64)
	for rows.Next() {
		var serverID, clientID int64
		if err := rows.Scan(&serverID, &clientID); err != nil {
			t.Fatal(err)
		}
		values[serverID] = clientID
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func assertRawSubscriberCounters(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}, clientID, wantUplink, wantDownlink int64) {
	t.Helper()
	var uplink, downlink, cycleUplink, cycleDownlink int64
	if err := db.QueryRow(`SELECT xray_uplink_bytes, xray_downlink_bytes,
		cycle_uplink_bytes, cycle_downlink_bytes FROM client_metrics WHERE client_id = ?`, clientID).
		Scan(&uplink, &downlink, &cycleUplink, &cycleDownlink); err != nil {
		t.Fatal(err)
	}
	if uplink != wantUplink || downlink != wantDownlink || cycleUplink != 0 || cycleDownlink != 0 {
		t.Fatalf("raw/cycle counters = %d/%d %d/%d, want %d/%d 0/0",
			uplink, downlink, cycleUplink, cycleDownlink, wantUplink, wantDownlink)
	}
}
