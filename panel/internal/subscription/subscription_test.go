package subscription

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGenerateSubscriptionDirectAndRelayInPlanOrder(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "HK", "198.51.100.10")
	insertSubscriptionTestServer(t, db, 2, "SG", "203.0.113.20")
	sourceProxy := createSubscriptionTestRealityProxy(t, db, 1, "internal source", 8443)
	targetProxy := createSubscriptionTestRealityProxy(t, db, 2, "internal target", 443)
	directProxy := createSubscriptionTestRealityProxy(t, db, 2, "internal direct", 9443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")

	direct, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "🇸🇬 SG-01", Mode: NodeModeDirect, TargetProxyID: directProxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceID := sourceProxy.ID
	relayNode, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "🇭🇰 HK-01", Mode: NodeModeRelay, SourceProxyID: &sourceID,
		TargetProxyID: targetProxy.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	trafficLimit := int64(1_000_000)
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{
		Name: "Premium", Enabled: true, TrafficLimitBytes: &trafficLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{relayNode.ID, direct.ID}); err != nil {
		t.Fatal(err)
	}
	expiresAt := service.now().Add(24 * time.Hour)
	billingMonths := 1
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{
		PlanIDSet: true, PlanID: &plan.ID, ExpiresAtSet: true, ExpiresAt: &expiresAt,
		BillingPeriodMonthsSet: true, BillingPeriodMonths: &billingMonths,
	}); err != nil {
		t.Fatal(err)
	}
	var clientID int64
	if err := db.QueryRow(`SELECT client_id FROM subscriber_clients WHERE user_id = 100 ORDER BY client_id LIMIT 1`).Scan(&clientID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE subscriber_clients SET charged_uplink_bytes = 11,
		charged_downlink_bytes = 13 WHERE client_id = ?`, clientID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE subscriber_usage SET archived_uplink_bytes = 5, archived_downlink_bytes = 7 WHERE user_id = 100`); err != nil {
		t.Fatal(err)
	}

	generated, mutations, err := service.GenerateSubscription(t.Context(), "test-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 0 || generated.Title != "Premium" || generated.Upload != 16 || generated.Download != 20 ||
		generated.Total != trafficLimit || generated.Expire != expiresAt.Unix() {
		t.Fatalf("generated metadata = %+v, mutations = %+v", generated, mutations)
	}
	decoded, err := base64.StdEncoding.DecodeString(generated.Body)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(decoded), "\n")
	if len(lines) != 2 {
		t.Fatalf("subscription lines = %q", decoded)
	}
	relayURI, err := url.Parse(lines[0])
	if err != nil {
		t.Fatal(err)
	}
	directURI, err := url.Parse(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	if relayURI.Fragment != relayNode.Name || relayURI.Host != "198.51.100.10:"+strconv.Itoa(relayNode.EntryPort) ||
		directURI.Fragment != direct.Name || directURI.Host != "203.0.113.20:9443" {
		t.Fatalf("subscription URIs = relay %q, direct %q", lines[0], lines[1])
	}
	if strings.Contains(string(decoded), "internal source") || strings.Contains(string(decoded), "internal target") ||
		strings.Contains(string(decoded), "subscriber-100") {
		t.Fatalf("subscription leaked internal names: %q", decoded)
	}
}

func TestGenerateSubscriptionTitleFallbackAndOverride(t *testing.T) {
	db, service := newSubscriptionTestService(t)
	insertSubscriptionTestServer(t, db, 1, "SG", "203.0.113.10")
	proxyValue := createSubscriptionTestRealityProxy(t, db, 1, "internal", 443)
	insertSubscriptionTestSubscriber(t, db, 100, "alice")
	node, _, err := service.CreatePublishedNode(t.Context(), CreatePublishedNodeInput{
		Name: "公开节点名称", Mode: NodeModeDirect, TargetProxyID: proxyValue.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), CreatePlanInput{Name: "50G 月付套餐", Enabled: true})
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
	if data.Title != plan.Name || len(data.Nodes) != 1 || data.Nodes[0].DisplayName != node.Name {
		t.Fatalf("fallback subscription data = %+v", data)
	}
	title := "我的机场"
	if _, _, err := service.UpdatePlan(t.Context(), plan.ID, UpdatePlanInput{SubscriptionTitle: &title}); err != nil {
		t.Fatal(err)
	}
	data, _, err = service.GenerateSubscriptionData(t.Context(), "test-token")
	if err != nil || data.Title != title {
		t.Fatalf("custom subscription title = %q, error = %v", data.Title, err)
	}
}

func TestGenerateSubscriptionTokenAndAvailability(t *testing.T) {
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
	newToken, err := service.RegenerateSubscriptionToken(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.GenerateSubscription(t.Context(), "test-token"); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("old token error = %v", err)
	}
	if _, _, err := service.GenerateSubscription(t.Context(), newToken); err != nil {
		t.Fatalf("new token error = %v", err)
	}
	disabled := false
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.GenerateSubscription(t.Context(), newToken); !errors.Is(err, ErrSubscriptionUnavailable) {
		t.Fatalf("disabled subscription error = %v", err)
	}

	enabled := true
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	disabled = false
	if _, _, err := service.UpdatePlan(t.Context(), plan.ID, UpdatePlanInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.GenerateSubscription(t.Context(), newToken); !errors.Is(err, ErrSubscriptionUnavailable) {
		t.Fatalf("disabled plan subscription error = %v", err)
	}
	if _, _, err := service.UpdatePlan(t.Context(), plan.ID, UpdatePlanInput{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	past := service.now().Add(-time.Second)
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{ExpiresAtSet: true, ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.GenerateSubscription(t.Context(), newToken); !errors.Is(err, ErrSubscriptionUnavailable) {
		t.Fatalf("expired subscription error = %v", err)
	}
	if _, _, err := service.UpdateSubscriber(t.Context(), 100, UpdateSubscriberInput{ExpiresAtSet: true}); err != nil {
		t.Fatal(err)
	}
	limit := int64(1)
	if _, _, err := service.UpdatePlan(t.Context(), plan.ID, UpdatePlanInput{
		TrafficLimitBytesSet: true, TrafficLimitBytes: &limit,
	}); err != nil {
		t.Fatal(err)
	}
	var clientID int64
	if err := db.QueryRow(`SELECT client_id FROM subscriber_clients WHERE user_id = 100`).Scan(&clientID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO client_metrics
		(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes, cycle_started_at, updated_at)
		VALUES (?, 1, 0, 1, 0, 1, 1)`, clientID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE subscriber_clients SET charged_uplink_bytes = 1 WHERE client_id = ?`, clientID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.GenerateSubscription(t.Context(), newToken); !errors.Is(err, ErrSubscriptionUnavailable) {
		t.Fatalf("exhausted subscription error = %v", err)
	}
}
