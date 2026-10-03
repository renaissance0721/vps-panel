package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
	"gopkg.in/yaml.v3"
)

func TestPublicSubscriptionResponseAndAvailability(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initialized.Code != http.StatusCreated || len(initialized.Result().Cookies()) == 0 {
		t.Fatalf("initialize admin = %d %s", initialized.Code, initialized.Body.String())
	}
	adminCookie := initialized.Result().Cookies()[0]
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
		Name: "🇸🇬 SG-01", Mode: subscriptionstore.NodeModeDirect, TargetProxyID: proxyValue.ID,
		TrafficMultiplierBP: 50, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(1024)
	plan, err := subscriptions.CreatePlan(t.Context(), subscriptionstore.CreatePlanInput{
		Name: "Basic", SubscriptionTitle: "我的机场", Enabled: true, TrafficLimitBytes: &limit,
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

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub/public-token", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Subscription-Userinfo") != "upload=0; download=0; total=1024; expire=0" ||
		response.Header().Get("Profile-Title") != "base64:"+base64.StdEncoding.EncodeToString([]byte("我的机场")) ||
		response.Header().Get("Profile-Update-Interval") != "24" ||
		response.Header().Get("Content-Disposition") != "inline; filename*=UTF-8''%E6%88%91%E7%9A%84%E6%9C%BA%E5%9C%BA" {
		t.Fatalf("public subscription response = status %d headers %v body %q", response.Code, response.Header(), response.Body.String())
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Body.String())
	if err != nil || !strings.Contains(string(decoded), "#%F0%9F%87%B8%F0%9F%87%AC%20SG-01%20%5B0.5%C3%97%5D") {
		t.Fatalf("decoded public subscription = %q, error = %v", decoded, err)
	}

	mihomo := httptest.NewRecorder()
	handler.ServeHTTP(mihomo, httptest.NewRequest(http.MethodGet, "/sub/public-token/mihomo", nil))
	if mihomo.Code != http.StatusOK || mihomo.Header().Get("Content-Type") != "text/yaml; charset=utf-8" ||
		mihomo.Header().Get("Content-Disposition") != "inline; filename*=UTF-8''%E6%88%91%E7%9A%84%E6%9C%BA%E5%9C%BA" ||
		mihomo.Header().Get("Subscription-Userinfo") != response.Header().Get("Subscription-Userinfo") ||
		mihomo.Header().Get("Profile-Title") != response.Header().Get("Profile-Title") ||
		mihomo.Header().Get("Profile-Update-Interval") != "24" {
		t.Fatalf("Mihomo subscription response = status %d headers %v body %q", mihomo.Code, mihomo.Header(), mihomo.Body.String())
	}
	var config struct {
		Proxies []struct {
			Name              string `yaml:"name"`
			Type              string `yaml:"type"`
			ServerName        string `yaml:"servername"`
			Flow              string `yaml:"flow"`
			ClientFingerprint string `yaml:"client-fingerprint"`
			RealityOptions    struct {
				PublicKey string `yaml:"public-key"`
				ShortID   string `yaml:"short-id"`
			} `yaml:"reality-opts"`
		} `yaml:"proxies"`
		ProxyGroups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(mihomo.Body.Bytes(), &config); err != nil || len(config.Proxies) != 1 ||
		config.Proxies[0].Name != subscriptionstore.FormatNodeDisplayName(node.Name, 50) || config.Proxies[0].Type != "vless" ||
		config.Proxies[0].ServerName != "www.example.com" || config.Proxies[0].Flow != proxystore.ServerFlow ||
		config.Proxies[0].ClientFingerprint != proxystore.Fingerprint ||
		config.Proxies[0].RealityOptions.PublicKey == "" || config.Proxies[0].RealityOptions.ShortID == "" ||
		len(config.ProxyGroups) != 8 || config.ProxyGroups[0].Name != "🚀 默认代理" ||
		len(config.ProxyGroups[0].Proxies) != 2 || config.ProxyGroups[0].Proxies[0] != "DIRECT" ||
		config.ProxyGroups[0].Proxies[1] != config.Proxies[0].Name ||
		len(config.Rules) != 14 || config.Rules[len(config.Rules)-1] != "MATCH,🚀 默认代理" {
		t.Fatalf("Mihomo YAML = %+v, error = %v\n%s", config, err, mihomo.Body.String())
	}
	shadowrocket := performRequest(t, handler, http.MethodGet, "/sub/public-token/shadowrocket", nil, nil)
	if shadowrocket.Code != http.StatusOK || shadowrocket.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		!strings.HasSuffix(shadowrocket.Header().Get("Content-Disposition"), ".conf") ||
		shadowrocket.Header().Get("Subscription-Userinfo") != response.Header().Get("Subscription-Userinfo") ||
		shadowrocket.Header().Get("Profile-Title") != response.Header().Get("Profile-Title") ||
		shadowrocket.Header().Get("Profile-Update-Interval") != "24" || shadowrocket.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(shadowrocket.Body.String(), "reality=true") || !strings.Contains(shadowrocket.Body.String(), "[Proxy Group]") {
		t.Fatalf("Shadowrocket response: status %d", shadowrocket.Code)
	}
	rocketAuto := httptest.NewRecorder()
	rocketRequest := httptest.NewRequest(http.MethodGet, "/sub/public-token/auto", nil)
	rocketRequest.Header.Set("User-Agent", "Shadowrocket/2.2")
	handler.ServeHTTP(rocketAuto, rocketRequest)
	if rocketAuto.Code != http.StatusOK || rocketAuto.Body.String() != shadowrocket.Body.String() ||
		rocketAuto.Header().Get("Subscription-Userinfo") != shadowrocket.Header().Get("Subscription-Userinfo") {
		t.Fatal("Shadowrocket auto detection failed")
	}
	rocketRoot := httptest.NewRecorder()
	rocketRequest = httptest.NewRequest(http.MethodGet, "/sub/public-token", nil)
	rocketRequest.Header.Set("User-Agent", "Shadowrocket/2.2")
	handler.ServeHTTP(rocketRoot, rocketRequest)
	if rocketRoot.Body.String() != response.Body.String() {
		t.Fatal("root URL no longer returns Base64")
	}
	preview := performRequest(t, handler, http.MethodGet, "/api/admin/subscription/users/100/mihomo-preview", nil, adminCookie)
	var previewPayload struct {
		YAML string `json:"yaml"`
	}
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &previewPayload) != nil ||
		previewPayload.YAML != mihomo.Body.String() {
		t.Fatalf("admin Mihomo preview = %d %s", preview.Code, preview.Body.String())
	}
	var storedName string
	if err := db.QueryRow(`SELECT name FROM subscription_published_nodes WHERE id = ?`, node.ID).Scan(&storedName); err != nil || storedName != node.Name {
		t.Fatalf("stored published node name = %q, %v; want base name %q", storedName, err, node.Name)
	}
	for _, userAgent := range []string{"Clash-Verge/2.4", "mihomo/1.19"} {
		automatic := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/sub/public-token/auto", nil)
		request.Header.Set("User-Agent", userAgent)
		handler.ServeHTTP(automatic, request)
		if automatic.Code != http.StatusOK || automatic.Header().Get("Content-Type") != "text/yaml; charset=utf-8" ||
			automatic.Header().Get("Subscription-Userinfo") != response.Header().Get("Subscription-Userinfo") ||
			automatic.Header().Get("Profile-Title") != response.Header().Get("Profile-Title") ||
			automatic.Header().Get("Profile-Update-Interval") != "24" ||
			automatic.Header().Get("Content-Disposition") != mihomo.Header().Get("Content-Disposition") ||
			automatic.Body.String() != mihomo.Body.String() {
			t.Fatalf("auto Mihomo for %q = %d %v %q", userAgent, automatic.Code, automatic.Header(), automatic.Body.String())
		}
	}
	automaticBase64 := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/sub/public-token/auto", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0")
	handler.ServeHTTP(automaticBase64, request)
	if automaticBase64.Code != http.StatusOK || automaticBase64.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		automaticBase64.Header().Get("Subscription-Userinfo") != response.Header().Get("Subscription-Userinfo") ||
		automaticBase64.Header().Get("Profile-Title") != response.Header().Get("Profile-Title") ||
		automaticBase64.Header().Get("Profile-Update-Interval") != "24" ||
		automaticBase64.Header().Get("Content-Disposition") != response.Header().Get("Content-Disposition") ||
		automaticBase64.Body.String() != response.Body.String() {
		t.Fatalf("auto Base64 = %d %v %q", automaticBase64.Code, automaticBase64.Header(), automaticBase64.Body.String())
	}

	for _, suffix := range []string{"", "/mihomo", "/shadowrocket", "/auto"} {
		missing := httptest.NewRecorder()
		handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/sub/not-a-token"+suffix, nil))
		if missing.Code != http.StatusNotFound || strings.Contains(missing.Body.String(), "not-a-token") {
			t.Fatalf("missing token response for %q = %d %q", suffix, missing.Code, missing.Body.String())
		}
	}
	if _, err := db.Exec(`UPDATE subscriber_profiles SET enabled = 0 WHERE user_id = 100`); err != nil {
		t.Fatal(err)
	}
	assertSubscriptionStatusForAllFormats(t, handler, "public-token", http.StatusForbidden)
	if _, err := db.Exec(`UPDATE subscriber_profiles SET enabled = 1, expires_at = 1 WHERE user_id = 100`); err != nil {
		t.Fatal(err)
	}
	assertSubscriptionStatusForAllFormats(t, handler, "public-token", http.StatusForbidden)
	if _, err := db.Exec(`UPDATE subscriber_profiles SET expires_at = NULL WHERE user_id = 100`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE subscription_plans SET traffic_limit_bytes = 0 WHERE id = ?`, plan.ID); err != nil {
		t.Fatal(err)
	}
	assertSubscriptionStatusForAllFormats(t, handler, "public-token", http.StatusForbidden)
}

func assertSubscriptionStatusForAllFormats(t *testing.T, handler http.Handler, tokenValue string, want int) {
	t.Helper()
	for _, suffix := range []string{"", "/mihomo", "/shadowrocket", "/auto"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sub/"+tokenValue+suffix, nil))
		if response.Code != want {
			t.Fatalf("subscription status for %q = %d, want %d, body %q", suffix, response.Code, want, response.Body.String())
		}
	}
}

func TestSubscriptionFormatDetectionKeepsExistingClients(t *testing.T) {
	for _, ua := range []string{"SHADOWROCKET/2", "Shadowrocket Meta"} {
		if detectSubscriptionFormat(ua) != subscriptionFormatShadowrocket {
			t.Errorf("Shadowrocket UA %q", ua)
		}
	}
	for _, ua := range []string{"Clash", "Mihomo", "Clash Verge", "FlClash", "Stash", "clashmeta", "meta"} {
		if detectSubscriptionFormat(ua) != subscriptionFormatMihomo {
			t.Errorf("Mihomo UA %q", ua)
		}
	}
	for _, ua := range []string{"", "Mozilla/5.0", "v2rayN", "Surge"} {
		if detectSubscriptionFormat(ua) != subscriptionFormatBase64 {
			t.Errorf("Base64 UA %q", ua)
		}
	}
}
