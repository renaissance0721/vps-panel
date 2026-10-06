package api

import (
	"encoding/json"
	"net/http"
	"reflect"
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
		"type": "mihomo", "content": "dns:\n  enable: false\ntun:\n  enable: false",
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
			"name": "Invalid", "enabled": true, "type": "mihomo", "content": forbidden + ": []",
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

func TestRoutingPresetCreatePayloadCanCopyAndEditAPI(t *testing.T) {
	db, handler, adminCookie, _ := setupAccountTest(t)
	defer db.Close()

	created := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/routing-presets", map[string]any{
		"name": "个人自用", "enabled": true,
		"groups": []map[string]any{
			{"name": "代理", "type": "select", "proxies": []string{"DIRECT", "REJECT"}, "include_all": true},
			{"name": "AI", "type": "select", "proxies": []string{"代理"}, "include_all": false},
		},
		"rule_providers": []map[string]any{
			{"name": "OpenAI", "url": "https://example.com/openai.list", "type": "http", "behavior": "classical", "format": "text", "interval": 86400},
			{"name": "Lan", "url": "https://example.com/lan.list", "type": "http", "behavior": "classical", "format": "text", "interval": 3600},
		},
		"rules": []string{"RULE-SET,OpenAI,AI", "RULE-SET,Lan,DIRECT", "MATCH,代理"},
	}, adminCookie)
	var originalPayload struct {
		RoutingPreset routingPresetResponse `json:"routing_preset"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &originalPayload) != nil {
		t.Fatalf("create original routing preset = %d %s", created.Code, created.Body.String())
	}
	original := originalPayload.RoutingPreset

	copied := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/routing-presets", map[string]any{
		"name": "个人自用 - 副本", "enabled": original.Enabled,
		"groups": original.Groups, "rule_providers": original.RuleProviders, "rules": original.Rules,
	}, adminCookie)
	var copiedPayload struct {
		RoutingPreset routingPresetResponse `json:"routing_preset"`
	}
	if copied.Code != http.StatusCreated || json.Unmarshal(copied.Body.Bytes(), &copiedPayload) != nil {
		t.Fatalf("copy routing preset through create API = %d %s", copied.Code, copied.Body.String())
	}
	copyValue := copiedPayload.RoutingPreset
	if copyValue.ID == original.ID || copyValue.IsDefault || copyValue.Name != "个人自用 - 副本" || copyValue.Enabled != original.Enabled {
		t.Fatalf("copied routing preset identity = %+v", copyValue)
	}
	if len(copyValue.Groups) != len(original.Groups) {
		t.Fatalf("copied routing groups = %+v", copyValue.Groups)
	}
	for index := range original.Groups {
		originalGroup, copiedGroup := original.Groups[index], copyValue.Groups[index]
		if copiedGroup.Key == "" || copiedGroup.Key == originalGroup.Key {
			t.Fatalf("copied routing group key was not regenerated: original=%q copied=%q", originalGroup.Key, copiedGroup.Key)
		}
		originalGroup.Key, copiedGroup.Key = "", ""
		if !reflect.DeepEqual(copiedGroup, originalGroup) {
			t.Fatalf("copied routing group %d = %+v, want %+v", index, copiedGroup, originalGroup)
		}
	}
	if !reflect.DeepEqual(copyValue.RuleProviders, original.RuleProviders) || !reflect.DeepEqual(copyValue.Rules, original.Rules) {
		t.Fatalf("copied routing content = providers %+v rules %+v", copyValue.RuleProviders, copyValue.Rules)
	}

	editedGroups := []map[string]any{
		{"key": copyValue.Groups[0].Key, "name": "主策略", "type": "select", "proxies": []string{"DIRECT"}, "include_all": false},
		{"key": copyValue.Groups[1].Key, "name": "开发", "type": "select", "proxies": []string{"主策略"}, "include_all": true},
	}
	editedProviders := []map[string]any{
		{"name": "Development", "url": "https://example.com/development.list", "type": "http", "behavior": "classical", "format": "text", "interval": 7200},
	}
	editedRules := []string{"RULE-SET,Development,开发", "MATCH,主策略"}
	updated := performRequest(t, handler, http.MethodPatch,
		"/api/admin/subscription/routing-presets/"+strconv.FormatInt(copyValue.ID, 10), map[string]any{
			"name": "个人自用副本（已编辑）", "enabled": false,
			"groups": editedGroups, "rule_providers": editedProviders, "rules": editedRules,
		}, adminCookie)
	var updatedPayload struct {
		RoutingPreset routingPresetResponse `json:"routing_preset"`
	}
	if updated.Code != http.StatusOK || json.Unmarshal(updated.Body.Bytes(), &updatedPayload) != nil {
		t.Fatalf("edit copied routing preset = %d %s", updated.Code, updated.Body.String())
	}
	updatedValue := updatedPayload.RoutingPreset
	if updatedValue.Name != "个人自用副本（已编辑）" || updatedValue.Enabled || updatedValue.IsDefault ||
		len(updatedValue.Groups) != 2 || updatedValue.Groups[0].Name != "主策略" || updatedValue.Groups[1].Name != "开发" ||
		len(updatedValue.RuleProviders) != 1 || updatedValue.RuleProviders[0].Name != "Development" ||
		!reflect.DeepEqual(updatedValue.Rules, editedRules) {
		t.Fatalf("edited copied routing preset = %+v", updatedValue)
	}

	listed := performRequest(t, handler, http.MethodGet, "/api/admin/subscription/routing-presets", nil, adminCookie)
	var listPayload struct {
		RoutingPresets []routingPresetResponse `json:"routing_presets"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &listPayload) != nil {
		t.Fatalf("list routing presets after copy = %d %s", listed.Code, listed.Body.String())
	}
	for _, value := range listPayload.RoutingPresets {
		if value.ID == original.ID {
			if !reflect.DeepEqual(value, original) {
				t.Fatalf("original routing preset changed after copying and editing: got %+v want %+v", value, original)
			}
			return
		}
	}
	t.Fatal("original routing preset missing after copy")
}
