package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"gopkg.in/yaml.v3"
)

func TestBuiltinMihomoConfigurationAPI(t *testing.T) {
	db, handler, adminCookie, _ := setupAccountTest(t)
	defer db.Close()
	vipCookie, _ := registerAccount(t, db, handler, adminCookie, "vip", "vip-user")

	unauthenticated := performRequest(t, handler, http.MethodGet, "/api/admin/subscription/builtin-mihomo", nil, nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated built-in Mihomo = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}
	forbidden := performRequest(t, handler, http.MethodGet, "/api/admin/subscription/builtin-mihomo", nil, vipCookie)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("vip built-in Mihomo = %d %s", forbidden.Code, forbidden.Body.String())
	}

	response := performRequest(t, handler, http.MethodGet, "/api/admin/subscription/builtin-mihomo", nil, adminCookie)
	var payload mihomoConfigurationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil {
		t.Fatalf("admin built-in Mihomo = %d %s", response.Code, response.Body.String())
	}
	wantProviders := []string{"OpenAI", "Claude", "Gemini", "YouTube", "Netflix", "Telegram", "TikTok", "Apple", "Copilot", "Microsoft"}
	if payload.Name != "内置默认 Mihomo 模板" || len(payload.Groups) != 8 || len(payload.Rules) != 12 ||
		!slices.Equal(payload.RuleProviders, wantProviders) || !slices.Contains(payload.Groups[0].Proxies, "{{all}}") {
		t.Fatalf("built-in Mihomo payload = %+v", payload)
	}
	var document struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(payload.YAML), &document); err != nil || len(document.Proxies) != 0 ||
		strings.Contains(payload.YAML, "uuid:") || strings.Contains(payload.YAML, "password:") {
		t.Fatalf("built-in Mihomo YAML exposes proxies = %+v, %v\n%s", document.Proxies, err, payload.YAML)
	}

	created := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/templates", map[string]any{
		"name": "Custom", "enabled": true,
		"config_yaml": "proxy-groups:\n  - name: Custom\n    type: select\n    proxies: [\"{{all}}\", DIRECT]\nrules:\n  - MATCH,Custom",
	}, adminCookie)
	var createdPayload struct {
		Template subscriptionTemplateResponse `json:"template"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &createdPayload) != nil {
		t.Fatalf("create custom Mihomo template = %d %s", created.Code, created.Body.String())
	}
	effective := performRequest(t, handler, http.MethodGet,
		"/api/admin/subscription/mihomo-configuration?template_id="+strconv.FormatInt(createdPayload.Template.ID, 10), nil, adminCookie)
	var effectivePayload mihomoConfigurationResponse
	if effective.Code != http.StatusOK || json.Unmarshal(effective.Body.Bytes(), &effectivePayload) != nil ||
		effectivePayload.Name != "Custom" || len(effectivePayload.Groups) != 1 ||
		effectivePayload.Groups[0].Name != "Custom" || !slices.Equal(effectivePayload.Rules, []string{"MATCH,Custom"}) {
		t.Fatalf("effective Mihomo template = %d %s", effective.Code, effective.Body.String())
	}
}

func TestSubscriptionPlanTitleAPI(t *testing.T) {
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
	created := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/plans", map[string]any{
		"name": "50G 月付套餐", "subscription_title": " Refrain Cloud ", "enabled": true,
		"routing_groups": []map[string]any{{"name": "Default", "type": "select", "proxies": []string{"DIRECT"}}},
		"routing_rules":  []string{"MATCH,Default"},
	}, adminCookie)
	var payload struct {
		Plan subscriptionPlanResponse `json:"plan"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &payload) != nil ||
		payload.Plan.Name != "50G 月付套餐" || payload.Plan.SubscriptionTitle != "Refrain Cloud" ||
		len(payload.Plan.RoutingGroups) != 1 || len(payload.Plan.RoutingRules) != 1 ||
		strings.Contains(created.Body.String(), "routing_preset_id") {
		t.Fatalf("create plan title = %d %s", created.Code, created.Body.String())
	}
	invalid := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/plans/"+strconv.FormatInt(payload.Plan.ID, 10),
		map[string]any{"subscription_title": strings.Repeat("长", 101)}, adminCookie)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid plan title = %d %s", invalid.Code, invalid.Body.String())
	}
	cleared := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/plans/"+strconv.FormatInt(payload.Plan.ID, 10),
		map[string]any{"subscription_title": ""}, adminCookie)
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"subscription_title":""`) {
		t.Fatalf("clear plan title = %d %s", cleared.Code, cleared.Body.String())
	}
	unpaired := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/plans/"+strconv.FormatInt(payload.Plan.ID, 10),
		map[string]any{"routing_groups": []any{}}, adminCookie)
	if unpaired.Code != http.StatusBadRequest {
		t.Fatalf("unpaired plan routing = %d %s", unpaired.Code, unpaired.Body.String())
	}
	restored := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/plans/"+strconv.FormatInt(payload.Plan.ID, 10),
		map[string]any{"routing_groups": []any{}, "routing_rules": []any{}}, adminCookie)
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), `"routing_groups":[]`) ||
		!strings.Contains(restored.Body.String(), `"routing_rules":[]`) {
		t.Fatalf("restore template routing = %d %s", restored.Code, restored.Body.String())
	}
	for _, field := range []string{
		"traffic_reset_mode", "traffic_reset_day", "traffic_reset_time", "default_validity_days", "billing_period_months", "routing_preset_id",
	} {
		legacy := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/plans", map[string]any{
			"name": "Legacy", field: 1,
		}, adminCookie)
		if legacy.Code != http.StatusBadRequest {
			t.Fatalf("legacy plan field %s accepted = %d %s", field, legacy.Code, legacy.Body.String())
		}
	}
}
