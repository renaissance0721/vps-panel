package api

import (
	"encoding/json"
	"net/http"
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
	vipResponse := performRequest(t, handler, http.MethodGet, "/api/admin/subscription/builtin-mihomo", nil, vipCookie)
	if vipResponse.Code != http.StatusOK {
		t.Fatalf("vip built-in Mihomo = %d %s", vipResponse.Code, vipResponse.Body.String())
	}

	response := performRequest(t, handler, http.MethodGet, "/api/admin/subscription/builtin-mihomo", nil, adminCookie)
	var payload mihomoConfigurationResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil {
		t.Fatalf("admin built-in Mihomo = %d %s", response.Code, response.Body.String())
	}
	if payload.Name != "内置默认 Mihomo 模板" {
		t.Fatalf("built-in Mihomo payload = %+v", payload)
	}
	var document map[string]any
	if err := yaml.Unmarshal([]byte(payload.YAML), &document); err != nil ||
		strings.Contains(payload.YAML, "uuid:") || strings.Contains(payload.YAML, "password:") {
		t.Fatalf("built-in Mihomo YAML = %+v, %v\n%s", document, err, payload.YAML)
	}
	for _, key := range []string{"proxies", "proxy-groups", "rule-providers", "rules"} {
		if _, exists := document[key]; exists {
			t.Fatalf("built-in Mihomo YAML contains %q:\n%s", key, payload.YAML)
		}
	}

	created := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/templates", map[string]any{
		"name": "Custom", "enabled": true,
		"config_yaml": "dns:\n  enable: false\ntun:\n  enable: false",
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
		effectivePayload.Name != "Custom" || !strings.Contains(effectivePayload.YAML, "mixed-port: 7890") ||
		!strings.Contains(effectivePayload.YAML, "enable: false") {
		t.Fatalf("effective Mihomo template = %d %s", effective.Code, effective.Body.String())
	}
	for _, forbidden := range []string{"proxies", "proxy-groups", "rule-providers", "rules"} {
		invalid := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/templates", map[string]any{
			"name": "Invalid", "enabled": true, "config_yaml": forbidden + ": []",
		}, adminCookie)
		if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "不能包含") {
			t.Fatalf("template routing field %q = %d %s", forbidden, invalid.Code, invalid.Body.String())
		}
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
	}, adminCookie)
	var payload struct {
		Plan subscriptionPlanResponse `json:"plan"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &payload) != nil ||
		payload.Plan.Name != "50G 月付套餐" || payload.Plan.SubscriptionTitle != "Refrain Cloud" ||
		payload.Plan.RoutingPresetID == nil || strings.Contains(created.Body.String(), "routing_groups") ||
		strings.Contains(created.Body.String(), "routing_rules") {
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
	missingRouting := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/plans/"+strconv.FormatInt(payload.Plan.ID, 10),
		map[string]any{"routing_preset_id": nil}, adminCookie)
	if missingRouting.Code != http.StatusBadRequest {
		t.Fatalf("nil plan routing = %d %s", missingRouting.Code, missingRouting.Body.String())
	}
	for _, field := range []string{
		"traffic_reset_mode", "traffic_reset_day", "traffic_reset_time", "default_validity_days", "billing_period_months", "routing_groups", "routing_rules",
	} {
		legacy := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/plans", map[string]any{
			"name": "Legacy", field: 1,
		}, adminCookie)
		if legacy.Code != http.StatusBadRequest {
			t.Fatalf("legacy plan field %s accepted = %d %s", field, legacy.Code, legacy.Body.String())
		}
	}
}

func TestRoutingPresetProtectionAPI(t *testing.T) {
	db, handler, adminCookie, _ := setupAccountTest(t)
	defer db.Close()

	listed := performRequest(t, handler, http.MethodGet, "/api/admin/subscription/routing-presets", nil, adminCookie)
	var listPayload struct {
		RoutingPresets []routingPresetResponse `json:"routing_presets"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &listPayload) != nil ||
		len(listPayload.RoutingPresets) == 0 || !listPayload.RoutingPresets[0].IsDefault {
		t.Fatalf("list default routing preset = %d %s", listed.Code, listed.Body.String())
	}
	defaultPreset := listPayload.RoutingPresets[0]
	updated := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/routing-presets/"+strconv.FormatInt(defaultPreset.ID, 10),
		map[string]any{"name": "默认分流（可编辑）"}, adminCookie)
	if updated.Code != http.StatusOK {
		t.Fatalf("edit default routing preset = %d %s", updated.Code, updated.Body.String())
	}
	disabled := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/routing-presets/"+strconv.FormatInt(defaultPreset.ID, 10),
		map[string]any{"enabled": false}, adminCookie)
	if disabled.Code != http.StatusConflict {
		t.Fatalf("disable default routing preset = %d %s", disabled.Code, disabled.Body.String())
	}
	deleted := performRequest(t, handler, http.MethodDelete,
		"/api/admin/subscription/routing-presets/"+strconv.FormatInt(defaultPreset.ID, 10), nil, adminCookie)
	if deleted.Code != http.StatusConflict {
		t.Fatalf("delete default routing preset = %d %s", deleted.Code, deleted.Body.String())
	}

	createdPreset := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/routing-presets", map[string]any{
		"name": "Custom", "enabled": true,
		"groups":         []map[string]any{{"name": "Custom", "type": "select", "proxies": []string{"DIRECT"}}},
		"rule_providers": []any{}, "rules": []string{"MATCH,Custom"},
	}, adminCookie)
	var createdPresetPayload struct {
		RoutingPreset routingPresetResponse `json:"routing_preset"`
	}
	if createdPreset.Code != http.StatusCreated || json.Unmarshal(createdPreset.Body.Bytes(), &createdPresetPayload) != nil {
		t.Fatalf("create routing preset = %d %s", createdPreset.Code, createdPreset.Body.String())
	}
	createdPlan := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/plans", map[string]any{
		"name": "Plan", "routing_preset_id": createdPresetPayload.RoutingPreset.ID,
	}, adminCookie)
	if createdPlan.Code != http.StatusCreated {
		t.Fatalf("create plan with routing preset = %d %s", createdPlan.Code, createdPlan.Body.String())
	}
	deleted = performRequest(t, handler, http.MethodDelete,
		"/api/admin/subscription/routing-presets/"+strconv.FormatInt(createdPresetPayload.RoutingPreset.ID, 10), nil, adminCookie)
	if deleted.Code != http.StatusConflict {
		t.Fatalf("delete referenced routing preset = %d %s", deleted.Code, deleted.Body.String())
	}
}
