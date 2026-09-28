package subscription

import (
	"database/sql"
	"errors"
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
		if _, err := db.Exec(`UPDATE subscriber_clients SET charged_uplink_bytes = ?,
			charged_downlink_bytes = ? WHERE client_id = ?`, int64(math.MaxInt64), int64(math.MaxInt64), clientID); err != nil {
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
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{sgNode.ID, usNode.ID}); err != nil {
		t.Fatal(err)
	}
	resetMode, resetDay, resetTime := ResetModeMonthly, 1, "00:00"
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{
		PlanIDSet: true, PlanID: &plan.ID, TrafficResetMode: &resetMode,
		TrafficResetDay: &resetDay, TrafficResetTime: &resetTime,
	}); err != nil {
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
	if _, err := db.Exec(`UPDATE subscriber_clients SET charge_uplink_remainder = 75,
		charge_downlink_remainder = 25 WHERE user_id = 100`); err != nil {
		t.Fatal(err)
	}

	value, mutations, err = service.ResetSubscriberTraffic(t.Context(), 100)
	if err != nil || value.UsedBytes != 0 || !value.Active || len(mutations) != 2 {
		t.Fatalf("manual reset subscriber = %+v, mutations = %+v, error = %v", value, mutations, err)
	}
	assertSubscriberClientShape(t, db, clientIDs[1], 100, true)
	assertSubscriberClientShape(t, db, clientIDs[2], 100, true)
	assertRawSubscriberCounters(t, db, clientIDs[1], 130, 220)
	assertRawSubscriberCounters(t, db, clientIDs[2], 1040, 2020)
	assertSubscriberCharge(t, db, clientIDs[1], 0, 0, 0, 0)
	assertSubscriberCharge(t, db, clientIDs[2], 0, 0, 0, 0)
	if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{
		ClientID: clientIDs[1], UplinkBytes: 137, DownlinkBytes: 231,
	}}); err != nil {
		t.Fatal(err)
	}
	assertSubscriberCharge(t, db, clientIDs[1], 7, 11, 0, 0)
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
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{node.ID}); err != nil {
		t.Fatal(err)
	}
	resetMode, resetDay, resetTime := ResetModeMonthly, 1, "00:00"
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{
		PlanIDSet: true, PlanID: &plan.ID, TrafficResetMode: &resetMode,
		TrafficResetDay: &resetDay, TrafficResetTime: &resetTime,
	}); err != nil {
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
	if _, err := db.Exec(`UPDATE subscriber_clients SET charge_uplink_remainder = 75,
		charge_downlink_remainder = 25 WHERE client_id = ?`, clientID); err != nil {
		t.Fatal(err)
	}

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
	assertSubscriberCharge(t, db, clientID, 0, 0, 0, 0)
	if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{
		ClientID: clientID, UplinkBytes: 108, DownlinkBytes: 108,
	}}); err != nil {
		t.Fatal(err)
	}
	assertSubscriberCharge(t, db, clientID, 2, 3, 0, 0)

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

func TestSubscriberTrafficMultiplierChargesRawDeltas(t *testing.T) {
	for _, test := range []struct {
		name         string
		multiplierBP int
		wantUplink   int64
		wantDownlink int64
	}{
		{"point one", 10, 10, 4},
		{"half", 50, 50, 20},
		{"one", 100, 100, 40},
		{"two", 200, 200, 80},
		{"five", 500, 500, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, service := newSubscriptionTestService(t)
			insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
			proxyValue := createSubscriptionTestRealityProxy(t, db, 1, "SG", 443)
			insertSubscriptionTestSubscriber(t, db, 100, "alice")
			node, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
				Name: "SG", Mode: NodeModeDirect, TargetProxyID: proxyValue.ID,
				TrafficMultiplierBP: test.multiplierBP, Enabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Plan", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{node.ID}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{
				PlanIDSet: true, PlanID: &plan.ID,
			}); err != nil {
				t.Fatal(err)
			}
			clientID := subscriberClientIDsByServer(t, db, 100)[1]
			if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{
				ClientID: clientID, UplinkBytes: 1_000, DownlinkBytes: 2_000,
			}}); err != nil {
				t.Fatal(err)
			}
			assertSubscriberCharge(t, db, clientID, 0, 0, 0, 0)
			if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{
				ClientID: clientID, UplinkBytes: 1_100, DownlinkBytes: 2_040,
			}}); err != nil {
				t.Fatal(err)
			}
			assertSubscriberCharge(t, db, clientID, test.wantUplink, test.wantDownlink, 0, 0)
			var rawUplink, rawDownlink int64
			if err := db.QueryRow(`SELECT cycle_uplink_bytes, cycle_downlink_bytes
				FROM client_metrics WHERE client_id = ?`, clientID).Scan(&rawUplink, &rawDownlink); err != nil {
				t.Fatal(err)
			}
			if rawUplink != 100 || rawDownlink != 40 {
				t.Fatalf("raw traffic changed by multiplier = %d/%d", rawUplink, rawDownlink)
			}
			if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{
				ClientID: clientID, UplinkBytes: 1_100, DownlinkBytes: 2_040,
			}}); err != nil {
				t.Fatal(err)
			}
			assertSubscriberCharge(t, db, clientID, test.wantUplink, test.wantDownlink, 0, 0)
			if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{
				ClientID: clientID, UplinkBytes: 10, DownlinkBytes: 20,
			}}); err != nil {
				t.Fatal(err)
			}
			assertSubscriberCharge(t, db, clientID, test.wantUplink, test.wantDownlink, 0, 0)
		})
	}
}

func TestSubscriberTrafficMultiplierRemainder(t *testing.T) {
	for _, test := range []struct {
		multiplierBP int
		reports      int
		wantCharge   int64
	}{
		{125, 4, 5},
		{10, 10, 1},
	} {
		var charged, remainder int64
		for range test.reports {
			delta, nextRemainder, err := applyTrafficMultiplierDelta(1, test.multiplierBP, remainder)
			if err != nil {
				t.Fatal(err)
			}
			charged += delta
			remainder = nextRemainder
		}
		if charged != test.wantCharge || remainder != 0 {
			t.Fatalf("multiplier %d small deltas = charged %d remainder %d", test.multiplierBP, charged, remainder)
		}
	}
	if _, _, err := applyTrafficMultiplierDelta(math.MaxInt64, 500, 0); err == nil {
		t.Fatal("expected overflow-safe multiplier error")
	}

	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	proxyValue := createSubscriptionTestRealityProxy(t, db, 1, "SG", 443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	node, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG", Mode: NodeModeDirect, TargetProxyID: proxyValue.ID,
		TrafficMultiplierBP: 125, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Plan", Enabled: true})
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
	if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{ClientID: clientID, UplinkBytes: 100}}); err != nil {
		t.Fatal(err)
	}
	for counter := int64(101); counter <= 104; counter++ {
		if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{ClientID: clientID, UplinkBytes: counter}}); err != nil {
			t.Fatal(err)
		}
	}
	assertSubscriberCharge(t, db, clientID, 5, 0, 0, 0)
}

func TestSubscriberTrafficMultiplierChangeDoesNotRepriceHistory(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	proxyValue := createSubscriptionTestRealityProxy(t, db, 1, "SG", 443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	node, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "SG", Mode: NodeModeDirect, TargetProxyID: proxyValue.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Plan", Enabled: true})
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
	for _, report := range []proxystore.ClientTrafficReport{
		{ClientID: clientID, UplinkBytes: 100},
		{ClientID: clientID, UplinkBytes: 110},
	} {
		if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{report}); err != nil {
			t.Fatal(err)
		}
	}
	assertSubscriberCharge(t, db, clientID, 10, 0, 0, 0)
	multiplier := 200
	if _, mutations, err := service.UpdatePublishedNode(t.Context(), node.ID, UpdatePublishedNodeInput{
		TrafficMultiplierBP: &multiplier,
	}); err != nil || len(mutations) != 0 {
		t.Fatalf("update multiplier mutations = %+v, error = %v", mutations, err)
	}
	assertSubscriberCharge(t, db, clientID, 10, 0, 0, 0)
	if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{
		ClientID: clientID, UplinkBytes: 115,
	}}); err != nil {
		t.Fatal(err)
	}
	assertSubscriberCharge(t, db, clientID, 20, 0, 0, 0)
}

func TestSubscriberQuotaUsesChargedTraffic(t *testing.T) {
	for _, test := range []struct {
		name         string
		multiplierBP int
		rawDelta     int64
		wantUsed     int64
		wantActive   bool
	}{
		{"double exhausts at half raw", 200, 50, 100, false},
		{"half charges half raw", 50, 100, 50, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, service := newSubscriptionTestService(t)
			insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
			proxyValue := createSubscriptionTestRealityProxy(t, db, 1, "SG", 443)
			insertSubscriptionTestSubscriber(t, db, 100, "alice")
			node, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
				Name: "SG", Mode: NodeModeDirect, TargetProxyID: proxyValue.ID,
				TrafficMultiplierBP: test.multiplierBP, Enabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			limit := int64(100)
			plan, err := service.CreatePlan(t.Context(), CreatePlanInput{
				Name: "100 bytes", Enabled: true, TrafficLimitBytes: &limit,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{node.ID}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{
				PlanIDSet: true, PlanID: &plan.ID,
			}); err != nil {
				t.Fatal(err)
			}
			clientID := subscriberClientIDsByServer(t, db, 100)[1]
			if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{
				ClientID: clientID, UplinkBytes: 1_000,
			}}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{
				ClientID: clientID, UplinkBytes: 1_000 + test.rawDelta,
			}}); err != nil {
				t.Fatal(err)
			}
			value, err := service.GetSubscriber(t.Context(), 100)
			if err != nil || value.UsedBytes != test.wantUsed || value.Active != test.wantActive {
				t.Fatalf("charged quota subscriber = %+v, error = %v", value, err)
			}
		})
	}
}

func TestSubscriberTrafficMultiplierLookupRequiresExactlyOneCurrentPlanNode(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	proxyValue := createSubscriptionTestRealityProxy(t, db, 1, "SG", 443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	first, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "First", Mode: NodeModeDirect, TargetProxyID: proxyValue.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "Second", Mode: NodeModeDirect, TargetProxyID: proxyValue.ID, TrafficMultiplierBP: 200, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "Plan", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{first.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID}); err != nil {
		t.Fatal(err)
	}
	clientID := subscriberClientIDsByServer(t, db, 100)[1]
	if _, err := db.Exec(`DELETE FROM subscription_plan_nodes WHERE plan_id = ?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{ClientID: clientID}}); !errors.Is(err, ErrInvalidPlanNodes) {
		t.Fatalf("zero multiplier matches error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO subscription_plan_nodes (plan_id, published_node_id, position)
		VALUES (?, ?, 1), (?, ?, 2)`, plan.ID, first.ID, plan.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{ClientID: clientID}}); !errors.Is(err, ErrInvalidPlanNodes) {
		t.Fatalf("multiple multiplier matches error = %v", err)
	}
}

func TestSubscriberPlanSwitchArchivesChargedUsage(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	firstProxy := createSubscriptionTestRealityProxy(t, db, 1, "First", 443)
	secondProxy := createSubscriptionTestRealityProxy(t, db, 1, "Second", 8443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	firstNode, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "First", Mode: NodeModeDirect, TargetProxyID: firstProxy.ID, TrafficMultiplierBP: 200, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondNode, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "Second", Mode: NodeModeDirect, TargetProxyID: secondProxy.ID, Enabled: true,
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
	if _, _, err := service.SetPlanNodes(t.Context(), planA.ID, []int64{firstNode.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), planB.ID, []int64{secondNode.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{PlanIDSet: true, PlanID: &planA.ID}); err != nil {
		t.Fatal(err)
	}
	clientID := subscriberClientIDsByServer(t, db, 100)[1]
	if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{ClientID: clientID, UplinkBytes: 100}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordClientTraffic(t.Context(), 1, []proxystore.ClientTrafficReport{{ClientID: clientID, UplinkBytes: 110}}); err != nil {
		t.Fatal(err)
	}
	before, err := service.GetSubscriber(t.Context(), 100)
	if err != nil || before.UsedBytes != 20 {
		t.Fatalf("usage before plan switch = %+v, error = %v", before, err)
	}
	after, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{PlanIDSet: true, PlanID: &planB.ID})
	if err != nil || after.UsedBytes != 20 {
		t.Fatalf("usage after plan switch = %+v, error = %v", after, err)
	}
	var archivedUplink int64
	if err := db.QueryRow(`SELECT archived_uplink_bytes FROM subscriber_usage WHERE user_id = 100`).Scan(&archivedUplink); err != nil {
		t.Fatal(err)
	}
	if archivedUplink != 20 {
		t.Fatalf("archived charged usage after plan switch = %d, want 20", archivedUplink)
	}
}

func assertSubscriberCharge(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}, clientID, wantUplink, wantDownlink, wantUplinkRemainder, wantDownlinkRemainder int64) {
	t.Helper()
	var uplink, downlink, uplinkRemainder, downlinkRemainder int64
	if err := db.QueryRow(`SELECT charged_uplink_bytes, charged_downlink_bytes,
		charge_uplink_remainder, charge_downlink_remainder
		FROM subscriber_clients WHERE client_id = ?`, clientID).
		Scan(&uplink, &downlink, &uplinkRemainder, &downlinkRemainder); err != nil {
		t.Fatal(err)
	}
	if uplink != wantUplink || downlink != wantDownlink ||
		uplinkRemainder != wantUplinkRemainder || downlinkRemainder != wantDownlinkRemainder {
		t.Fatalf("charged traffic = %d/%d remainder %d/%d, want %d/%d remainder %d/%d",
			uplink, downlink, uplinkRemainder, downlinkRemainder,
			wantUplink, wantDownlink, wantUplinkRemainder, wantDownlinkRemainder)
	}
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
