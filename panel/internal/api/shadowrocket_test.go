package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestShadowrocketTemplateAPIAndIndependentSelections(t *testing.T) {
	db, handler, admin, _ := setupAccountTest(t)
	defer db.Close()
	vip, _ := registerAccount(t, db, handler, admin, "vip", "vip")
	user, _ := registerAccount(t, db, handler, admin, "carpool", "normal")
	path := "/api/admin/subscription/builtin-shadowrocket"
	for _, check := range []struct {
		cookie *http.Cookie
		want   int
	}{{nil, 401}, {user, 403}, {vip, 200}, {admin, 200}} {
		if response := performRequest(t, handler, http.MethodGet, path, nil, check.cookie); response.Code != check.want {
			t.Fatalf("builtin permission: %d want %d", response.Code, check.want)
		}
	}
	content, err := subscriptionstore.BuildShadowrocketConfiguration(nil)
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"name": "Rocket", "type": "shadowrocket", "content": content, "enabled": true}
	if response := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/templates", request, vip); response.Code != 403 {
		t.Fatal("VIP wrote global template")
	}
	created := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/templates", request, admin)
	var payload struct {
		Template subscriptionTemplateResponse `json:"template"`
	}
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &payload) != nil || payload.Template.Type != "shadowrocket" || payload.Template.Content != strings.TrimSpace(content) || strings.Contains(created.Body.String(), "config_yaml") {
		t.Fatalf("create template status %d", created.Code)
	}
	rocketID := payload.Template.ID
	request = map[string]any{"name": "Mihomo", "type": "mihomo", "content": "mixed-port: 9999", "enabled": true}
	created = performRequest(t, handler, http.MethodPost, "/api/admin/subscription/templates", request, admin)
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &payload) != nil {
		t.Fatal("create Mihomo template")
	}
	mihomoID := payload.Template.ID
	for _, base := range []string{"/api/personal-subscriptions", "/api/admin/subscription/plans"} {
		body := map[string]any{"name": "Both", "enabled": true, "mihomo_template_id": mihomoID, "shadowrocket_template_id": rocketID}
		if base == "/api/personal-subscriptions" {
			body["client_name"] = "default"
		}
		response := performRequest(t, handler, http.MethodPost, base, body, admin)
		var result map[string]map[string]any
		if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("dual template creation %s: %d", base, response.Code)
		}
		key := "plan"
		if base == "/api/personal-subscriptions" {
			key = "personal_subscription"
		}
		value := result[key]
		if value["mihomo_template_id"] != float64(mihomoID) || value["shadowrocket_template_id"] != float64(rocketID) {
			t.Fatal("independent references missing")
		}
		id := strconv.FormatInt(int64(value["id"].(float64)), 10)
		for _, wrong := range []map[string]any{{"mihomo_template_id": rocketID}, {"shadowrocket_template_id": mihomoID}, {"shadowrocket_template_id": "invalid"}} {
			if invalid := performRequest(t, handler, http.MethodPatch, base+"/"+id, wrong, admin); invalid.Code != 400 {
				t.Fatalf("invalid template selection accepted: %d", invalid.Code)
			}
		}
		updated := performRequest(t, handler, http.MethodPatch, base+"/"+id, map[string]any{"shadowrocket_template_id": nil}, admin)
		if updated.Code != 200 || json.Unmarshal(updated.Body.Bytes(), &result) != nil || result[key]["shadowrocket_template_id"] != nil || result[key]["mihomo_template_id"] != float64(mihomoID) {
			t.Fatal("clearing Shadowrocket changed Mihomo")
		}
	}
	for _, invalid := range []map[string]any{
		{"name": "Bad", "type": "shadowrocket", "content": "dns: {}"},
		{"name": "Bad", "type": "shadowrocket", "content": ""},
		{"name": "Bad", "type": "mihomo", "content": content},
	} {
		if response := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/templates", invalid, admin); response.Code != 400 {
			t.Fatal("invalid template accepted")
		}
	}
	response := performRequest(t, handler, http.MethodPatch, "/api/admin/subscription/templates/"+strconv.FormatInt(rocketID, 10), map[string]string{"type": "mihomo"}, admin)
	if response.Code != 400 {
		t.Fatal("template type can change")
	}
}

func TestShadowrocketRenderErrorsAreActionableAndDoNotExposeCredentials(t *testing.T) {
	for _, check := range []struct {
		err  error
		want string
	}{
		{subscriptionstore.ErrInvalidShadowrocketTemplate, "模板格式无效"},
		{subscriptionstore.ErrTemplateTypeMismatch, "模板类型不匹配"},
		{subscriptionstore.ErrUnsupportedShadowrocketProtocol, "节点协议"},
		{subscriptionstore.ErrUnsupportedShadowrocketParams, "节点参数"},
		{subscriptionstore.ErrUnsupportedShadowrocketChain, "节点的代理链"},
		{subscriptionstore.ErrUnsupportedShadowrocketRule, "规则"},
		{subscriptionstore.ErrInvalidRoutingPreset, "分流方案无法转换"},
		{fmt.Errorf("private-group-name: %w", subscriptionstore.ErrRoutingGroupReferenceMissing), "引用了不存在的策略组"},
		{subscriptionstore.ErrRoutingGroupCycle, "循环引用"},
		{subscriptionstore.ErrInvalidRoutingBindings, "策略组节点绑定无效"},
	} {
		response := httptest.NewRecorder()
		writePublicSubscriptionRenderError(response, check.err)
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), check.want) ||
			strings.Contains(response.Body.String(), "private-group-name") {
			t.Fatalf("render error should contain only a safe diagnostic for %q", check.want)
		}
	}
}
