package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestSubscriberPortalAPIs(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	adminCookie := initialized.Result().Cookies()[0]
	invited := performRequest(t, handler, http.MethodPost, "/api/admin/invitations", map[string]string{
		"role": "subscriber",
	}, adminCookie)
	var invitation invitationResponse
	if invited.Code != http.StatusCreated || json.Unmarshal(invited.Body.Bytes(), &invitation) != nil {
		t.Fatalf("create subscriber invitation = %d, %s", invited.Code, invited.Body.String())
	}
	registered := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "alice", "password": "current-password",
	}, nil)
	if registered.Code != http.StatusCreated || !strings.Contains(registered.Body.String(), `"role":"subscriber"`) {
		t.Fatalf("register subscriber = %d, %s", registered.Code, registered.Body.String())
	}
	subscriberCookie := registered.Result().Cookies()[0]
	var userID int64
	if err := db.QueryRow(`SELECT id FROM users WHERE username = 'alice'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	unconfigured := performRequest(t, handler, http.MethodGet, "/api/subscriber/me", nil, subscriberCookie)
	if unconfigured.Code != http.StatusOK || !strings.Contains(unconfigured.Body.String(), `"status":"unconfigured"`) ||
		!strings.Contains(unconfigured.Body.String(), `"subscription_url":"http://example.com/sub/`) ||
		!strings.Contains(unconfigured.Body.String(), `"subscription_mihomo_url":"http://example.com/sub/`) ||
		!strings.Contains(unconfigured.Body.String(), `"subscription_auto_url":"http://example.com/sub/`) {
		t.Fatalf("unconfigured subscriber = %d, %s", unconfigured.Code, unconfigured.Body.String())
	}
	if denied := performRequest(t, handler, http.MethodGet, "/api/me/nodes", nil, subscriberCookie); denied.Code != http.StatusForbidden {
		t.Fatalf("subscriber user portal API = %d, %s", denied.Code, denied.Body.String())
	}
	if denied := performRequest(t, handler, http.MethodGet, "/api/subscriber/me", nil, adminCookie); denied.Code != http.StatusForbidden {
		t.Fatalf("admin subscriber portal API = %d, %s", denied.Code, denied.Body.String())
	}

	if _, err := db.Exec(`INSERT INTO servers (id, name, created_by_role, status, created_at, updated_at)
		VALUES (1, 'SG', 'admin', 'offline', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO server_system_info
		(server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, public_ipv4, agent_version, reported_at)
		VALUES (1, '', '', '', '', '', '[]', '[]', '203.0.113.10', '', 1)`); err != nil {
		t.Fatal(err)
	}
	proxyValue, _, err := proxystore.NewService(db).Create(t.Context(), proxystore.CreateInput{
		ServerID: 1, Name: "internal proxy", ListenPort: 443, EntryHostMode: proxystore.EntryHostAuto,
		Enabled: true, Security: proxystore.SecurityReality, ServerName: "www.example.com",
		RealityTarget: "www.example.com:443", FirstClientName: "internal client",
	})
	if err != nil {
		t.Fatal(err)
	}
	subscriptions := subscriptionstore.NewService(db, relaystore.NewService(db))
	node, _, err := subscriptions.CreatePublishedNode(t.Context(), subscriptionstore.CreatePublishedNodeInput{
		Name: "🇸🇬 SG-01", Mode: subscriptionstore.NodeModeDirect, TargetProxyID: proxyValue.ID,
		TrafficMultiplierBP: 50, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := subscriptions.CreatePlan(t.Context(), subscriptionstore.CreatePlanInput{
		Name: "Premium", SubscriptionTitle: "Refrain Cloud", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := subscriptions.SetPlanNodes(t.Context(), plan.ID, []int64{node.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := subscriptions.UpdateSubscriber(t.Context(), userID, subscriptionstore.UpdateSubscriberInput{
		PlanIDSet: true, PlanID: &plan.ID,
	}); err != nil {
		t.Fatal(err)
	}
	updated := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/users/"+strconv.FormatInt(userID, 10), map[string]any{
			"traffic_reset_mode": "monthly", "traffic_reset_day": 5,
			"traffic_reset_time": "03:00", "billing_period_months": 3,
		}, adminCookie)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"traffic_reset_mode":"monthly"`) ||
		!strings.Contains(updated.Body.String(), `"traffic_reset_day":5`) ||
		!strings.Contains(updated.Body.String(), `"billing_period_months":3`) {
		t.Fatalf("update subscriber lifecycle = %d, %s", updated.Code, updated.Body.String())
	}

	me := performRequest(t, handler, http.MethodGet, "/api/subscriber/me", nil, subscriberCookie)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"plan_name":"Premium"`) ||
		!strings.Contains(me.Body.String(), `"subscription_title":"Refrain Cloud"`) ||
		!strings.Contains(me.Body.String(), `"subscription_base64_url":"http://example.com/sub/`) ||
		!strings.Contains(me.Body.String(), `"subscription_mihomo_url":"http://example.com/sub/`) ||
		!strings.Contains(me.Body.String(), `"subscription_auto_url":"http://example.com/sub/`) ||
		!strings.Contains(me.Body.String(), `"traffic_reset_mode":"monthly"`) ||
		!strings.Contains(me.Body.String(), `"billing_period_months":3`) ||
		!strings.Contains(me.Body.String(), `"enabled_node_count":1`) {
		t.Fatalf("configured subscriber = %d, %s", me.Code, me.Body.String())
	}
	nodes := performRequest(t, handler, http.MethodGet, "/api/subscriber/nodes", nil, subscriberCookie)
	if nodes.Code != http.StatusOK || !strings.Contains(nodes.Body.String(), `"name":"🇸🇬 SG-01 [0.5×]"`) ||
		!strings.Contains(nodes.Body.String(), `"traffic_multiplier":0.5`) ||
		strings.Contains(nodes.Body.String(), "internal proxy") || strings.Contains(nodes.Body.String(), "target_proxy") {
		t.Fatalf("subscriber nodes = %d, %s", nodes.Code, nodes.Body.String())
	}
	var oldToken string
	if err := db.QueryRow(`SELECT subscription_token FROM subscriber_profiles WHERE user_id = ?`, userID).Scan(&oldToken); err != nil {
		t.Fatal(err)
	}
	regenerated := performRequest(t, handler, http.MethodPost, "/api/subscriber/subscription/regenerate", nil, subscriberCookie)
	var regeneratedURLs struct {
		Base64 string `json:"subscription_base64_url"`
		Mihomo string `json:"subscription_mihomo_url"`
		Auto   string `json:"subscription_auto_url"`
	}
	if regenerated.Code != http.StatusOK || json.Unmarshal(regenerated.Body.Bytes(), &regeneratedURLs) != nil ||
		regeneratedURLs.Base64 == "" || regeneratedURLs.Mihomo != regeneratedURLs.Base64+"/mihomo" ||
		regeneratedURLs.Auto != regeneratedURLs.Base64+"/auto" {
		t.Fatalf("regenerate subscriber token = %d, %s", regenerated.Code, regenerated.Body.String())
	}
	assertSubscriptionStatusForAllFormats(t, handler, oldToken, http.StatusNotFound)
	passwordRequest := performRequest(t, handler, http.MethodPost, "/api/account/password-reset-request", map[string]string{
		"new_password": "new-strong-password",
	}, subscriberCookie)
	if passwordRequest.Code != http.StatusAccepted {
		t.Fatalf("subscriber password request = %d, %s", passwordRequest.Code, passwordRequest.Body.String())
	}
}
