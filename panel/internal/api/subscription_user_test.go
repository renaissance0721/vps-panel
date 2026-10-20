package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestSubscriptionUserDetailNodeUsageAndPermissions(t *testing.T) {
	fixture := newProxyDeleteFixture(t)
	subscriber, userID := registerAccount(t, fixture.db, fixture.handler, fixture.cookie, "subscriber", "usage-alice")
	vip, _ := registerAccount(t, fixture.db, fixture.handler, fixture.cookie, "vip", "usage-vip")
	carpool, _ := registerAccount(t, fixture.db, fixture.handler, fixture.cookie, "carpool", "usage-carpool")
	serverID := fixture.createServer(t, "Usage server")
	proxyValue := fixture.createProxy(t, serverID, 443, "Usage proxy")
	service := subscriptionstore.NewService(fixture.db, relaystore.NewService(fixture.db))
	node, _, err := service.CreatePublishedNode(t.Context(), subscriptionstore.CreatePublishedNodeInput{
		Name: "Double", Mode: subscriptionstore.NodeModeDirect, TargetProxyID: proxyValue.ID,
		TrafficMultiplierBP: 200, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.CreatePlan(t.Context(), subscriptionstore.CreatePlanInput{Name: "Usage", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, []int64{node.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.UpdateSubscriber(t.Context(), userID, subscriptionstore.UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Exec(`UPDATE subscriber_clients SET charged_uplink_bytes = 12,
		charged_downlink_bytes = 8 WHERE user_id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/subscription/users/" + strconv.FormatInt(userID, 10)
	for name, cookie := range map[string]*http.Cookie{"subscriber": subscriber, "vip": vip, "carpool": carpool, "anonymous": nil} {
		response := performRequest(t, fixture.handler, http.MethodGet, path, nil, cookie)
		if response.Code != http.StatusForbidden && response.Code != http.StatusUnauthorized {
			t.Fatalf("%s detail status = %d", name, response.Code)
		}
	}
	response := performRequest(t, fixture.handler, http.MethodGet, path, nil, fixture.cookie)
	var detail struct {
		User subscriptionUserResponse `json:"user"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil {
		t.Fatalf("detail status = %d", response.Code)
	}
	if detail.User.UsedBytes != 20 || len(detail.User.NodeUsage) != 1 {
		t.Fatalf("detail total/count = %d/%d", detail.User.UsedBytes, len(detail.User.NodeUsage))
	}
	usage := detail.User.NodeUsage[0]
	if usage.PublishedNodeID != node.ID || usage.Name != "Double" || usage.Mode != "direct" || usage.TrafficMultiplier != 2 ||
		usage.UsedBytes != 20 || usage.ChargedUplinkBytes != 12 || usage.ChargedDownlinkBytes != 8 {
		t.Fatalf("detail node = %+v", usage)
	}
	urls := buildSubscriptionURLs("http://example.com", detail.User.SubscriptionToken)
	if detail.User.SubscriptionURL != urls.Base64 || detail.User.SubscriptionBase64URL != urls.Base64 ||
		detail.User.SubscriptionMihomoURL != urls.Mihomo || detail.User.SubscriptionShadowrocketURL != urls.Shadowrocket || detail.User.SubscriptionAutoURL != urls.Auto {
		t.Fatal("detail subscription format URLs do not follow existing public paths")
	}
	for _, forbidden := range []string{`"client_id"`, `"proxy_id"`, `"credential"`, `"traffic_multiplier_bp"`} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("detail exposes unexpected field %s", forbidden)
		}
	}
	listed := performRequest(t, fixture.handler, http.MethodGet, "/api/admin/subscription/users", nil, fixture.cookie)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), `"subscription_token"`) || strings.Contains(listed.Body.String(), `"subscription_url"`) {
		t.Fatalf("subscriber list secret boundary/status = %d", listed.Code)
	}
	reset := performRequest(t, fixture.handler, http.MethodPost, path+"/traffic/reset", nil, fixture.cookie)
	if reset.Code != http.StatusOK || json.Unmarshal(reset.Body.Bytes(), &detail) != nil || detail.User.UsedBytes != 0 || detail.User.NodeUsage[0].UsedBytes != 0 {
		t.Fatalf("reset detail status/total = %d/%d", reset.Code, detail.User.UsedBytes)
	}
	if _, _, err := service.SetPlanNodes(t.Context(), plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	empty := performRequest(t, fixture.handler, http.MethodGet, path, nil, fixture.cookie)
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), `"node_usage":[]`) {
		t.Fatalf("empty detail status = %d", empty.Code)
	}
}
