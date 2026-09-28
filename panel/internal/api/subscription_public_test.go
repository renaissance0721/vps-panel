package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestPublicSubscriptionResponseAndAvailability(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO servers (id, name, status, created_at, updated_at)
		VALUES (1, 'SG', 'offline', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO server_system_info
		(server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, public_ipv4, agent_version, reported_at)
		VALUES (1, '', '', '', '', '', '[]', '[]', '203.0.113.10', '', 1)`); err != nil {
		t.Fatal(err)
	}
	proxyValue, _, err := proxystore.NewService(db).Create(t.Context(), proxystore.CreateInput{
		ServerID: 1, Name: "internal", ListenPort: 443, EntryHostMode: proxystore.EntryHostAuto,
		Enabled: true, Security: proxystore.SecurityReality, ServerName: "www.example.com",
		RealityTarget: "www.example.com:443", FirstClientName: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (100, 'alice', 'hash', 'subscriber', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscriber_profiles
		(user_id, enabled, subscription_token, created_at, updated_at) VALUES (100, 1, 'public-token', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscriber_usage (user_id, cycle_started_at, updated_at) VALUES (100, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	subscriptions := subscriptionstore.NewService(db, relay.NewService(db))
	node, _, err := subscriptions.CreatePublishedNode(t.Context(), subscriptionstore.CreatePublishedNodeInput{
		Name: "🇸🇬 SG-01", Mode: subscriptionstore.NodeModeDirect, TargetProxyID: proxyValue.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(1024)
	plan, err := subscriptions.CreatePlan(t.Context(), subscriptionstore.CreatePlanInput{
		Name: "Basic", Enabled: true, TrafficLimitBytes: &limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := subscriptions.SetPlanNodes(t.Context(), plan.ID, []int64{node.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := subscriptions.UpdateSubscriber(t.Context(), 100, subscriptionstore.UpdateSubscriberInput{
		PlanIDSet: true, PlanID: &plan.ID,
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(db, t.TempDir())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub/public-token", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Subscription-Userinfo") != "upload=0; download=0; total=1024; expire=0" {
		t.Fatalf("public subscription response = status %d headers %v body %q", response.Code, response.Header(), response.Body.String())
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Body.String())
	if err != nil || !strings.Contains(string(decoded), "#%F0%9F%87%B8%F0%9F%87%AC%20SG-01") {
		t.Fatalf("decoded public subscription = %q, error = %v", decoded, err)
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/sub/not-a-token", nil))
	if missing.Code != http.StatusNotFound || strings.Contains(missing.Body.String(), "not-a-token") {
		t.Fatalf("missing token response = %d %q", missing.Code, missing.Body.String())
	}
	if _, err := db.Exec(`UPDATE subscriber_profiles SET enabled = 0 WHERE user_id = 100`); err != nil {
		t.Fatal(err)
	}
	disabled := httptest.NewRecorder()
	handler.ServeHTTP(disabled, httptest.NewRequest(http.MethodGet, "/sub/public-token", nil))
	if disabled.Code != http.StatusForbidden {
		t.Fatalf("disabled subscription status = %d, body %q", disabled.Code, disabled.Body.String())
	}
}
