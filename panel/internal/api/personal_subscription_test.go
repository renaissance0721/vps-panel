package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
	"gopkg.in/yaml.v3"
)

func TestPersonalShadowrocketDefaultOptionsAndPersistence(t *testing.T) {
	db, handler, admin, owner := setupAccountTest(t)
	defer db.Close()
	other, _ := registerAccount(t, db, handler, admin, "vip", "other")
	node, err := landingstore.NewService(db).Create(t.Context(), owner, landingstore.CreateInput{
		URI: "vless://fake-secret@example.com:443?type=tcp&security=tls&sni=example.com#Example",
	})
	if err != nil {
		t.Fatal(err)
	}
	type payload struct {
		Personal personalSubscriptionResponse `json:"personal_subscription"`
	}
	endpoint := "/api/personal-subscriptions"
	makeGroup := func(name string) payload {
		t.Helper()
		created := performRequest(t, handler, http.MethodPost, endpoint, map[string]any{
			"name": name, "client_name": "admin", "enabled": true,
		}, admin)
		var data payload
		if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &data) != nil {
			t.Fatalf("create subscription: %d %s", created.Code, created.Body)
		}
		return data
	}
	first, second := makeGroup("iPhone"), makeGroup("iPad")
	path := func(data payload) string { return endpoint + "/" + strconv.FormatInt(data.Personal.ID, 10) }
	preset, err := subscriptionstore.NewService(db, nil).GetRoutingPreset(t.Context(), first.Personal.RoutingPresetID)
	if err != nil {
		t.Fatal(err)
	}
	key := preset.Groups[0].Key
	draft := map[string]any{
		"subscription_id": first.Personal.ID, "client_name": "admin", "routing_preset_id": preset.ID,
		"nodes": []map[string]any{{"id": -1, "source_type": "landing", "source_id": node.ID, "display_name": "🇯🇵 Tokyo", "enabled": true}},
	}
	optionsPath := endpoint + "/shadowrocket-policy-options"
	options := performRequest(t, handler, http.MethodPost, optionsPath, draft, admin)
	var result struct {
		Options map[string][]subscriptionstore.ShadowrocketPolicyOption `json:"options"`
	}
	if options.Code != http.StatusOK || json.Unmarshal(options.Body.Bytes(), &result) != nil ||
		len(result.Options[key]) < 2 || result.Options[key][1].Target.NodeID != -1 || result.Options[key][1].Name != "Tokyo" ||
		strings.Contains(options.Body.String(), "fake-secret") {
		t.Fatalf("draft options: %d %s", options.Code, options.Body)
	}
	if denied := performRequest(t, handler, http.MethodPost, optionsPath, draft, other); denied.Code != http.StatusNotFound {
		t.Fatalf("cross-owner draft: %d", denied.Code)
	}
	delete(draft, "subscription_id")
	if denied := performRequest(t, handler, http.MethodPost, optionsPath, draft, other); denied.Code != http.StatusBadRequest {
		t.Fatalf("private node accepted into another owner's draft: %d", denied.Code)
	}
	for _, item := range []*payload{&first, &second} {
		saved := performRequest(t, handler, http.MethodPut, path(*item)+"/nodes", map[string]any{"nodes": []map[string]any{
			{"source_type": "landing", "source_id": node.ID, "display_name": "🇯🇵 Tokyo", "enabled": true},
			{"source_type": "landing", "source_id": node.ID, "display_name": "Backup", "enabled": true},
			{"source_type": "landing", "source_id": node.ID, "display_name": "Spare", "enabled": true},
		}}, admin)
		if saved.Code != http.StatusOK || json.Unmarshal(saved.Body.Bytes(), item) != nil {
			t.Fatalf("save nodes: %d %s", saved.Code, saved.Body)
		}
	}
	settings := func(target subscriptionstore.ShadowrocketPolicyTarget) map[string]any {
		return map[string]any{"routing_bindings": map[string]any{}, "shadowrocket_defaults": subscriptionstore.ShadowrocketDefaults{key: target}}
	}
	target := subscriptionstore.ShadowrocketPolicyTarget{Type: "node", NodeID: first.Personal.Nodes[0].ID}
	bindingsPath := path(first) + "/routing-bindings"
	if denied := performRequest(t, handler, http.MethodPut, bindingsPath, settings(target), other); denied.Code != http.StatusNotFound {
		t.Fatalf("cross-owner write: %d", denied.Code)
	}
	for _, invalidID := range []int64{-1, second.Personal.Nodes[0].ID} {
		if invalid := performRequest(t, handler, http.MethodPut, bindingsPath, settings(subscriptionstore.ShadowrocketPolicyTarget{Type: "node", NodeID: invalidID}), admin); invalid.Code != http.StatusBadRequest {
			t.Fatalf("invalid default %d: %d %s", invalidID, invalid.Code, invalid.Body)
		}
	}
	tokyo, backup := first.Personal.Nodes[0], first.Personal.Nodes[1]
	for _, item := range []struct {
		subscription payload
		target       subscriptionstore.ShadowrocketPolicyTarget
		bindings     []int64
		members      []string
		wantDefault  string
	}{
		{first, target, []int64{backup.ID, tokyo.ID}, []string{"Backup", "🇯🇵 Tokyo", "DIRECT", "Spare"}, "Tokyo"},
		{first, target, []int64{tokyo.ID, backup.ID}, []string{"🇯🇵 Tokyo", "Backup", "DIRECT", "Spare"}, "Tokyo"},
		{second, subscriptionstore.ShadowrocketPolicyTarget{Type: "builtin", Policy: "DIRECT"}, nil, []string{"DIRECT", "🇯🇵 Tokyo", "Backup", "Spare"}, "DIRECT"},
	} {
		body := settings(item.target)
		if len(item.bindings) > 0 {
			body["routing_bindings"] = subscriptionstore.RoutingBindings{key: item.bindings}
		}
		save := performRequest(t, handler, http.MethodPut, path(item.subscription)+"/routing-bindings", body, admin)
		if save.Code != http.StatusOK {
			t.Fatalf("save routing settings: %d %s", save.Code, save.Body)
		}
		get := performRequest(t, handler, http.MethodGet, path(item.subscription), nil, admin)
		var stored payload
		if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &stored) != nil ||
			!slices.Equal(stored.Personal.RoutingBindings[key], item.bindings) || stored.Personal.ShadowrocketDefaults[key] != item.target {
			t.Fatalf("reopened routing settings = %d, bindings %v, default %+v", get.Code, stored.Personal.RoutingBindings[key], stored.Personal.ShadowrocketDefaults[key])
		}
		mihomo := performRequest(t, handler, http.MethodGet, personalPath(item.subscription.Personal.SubscriptionMihomoURL), nil, nil)
		var config struct {
			Groups []subscriptionstore.RoutingGroup `yaml:"proxy-groups"`
		}
		if mihomo.Code != http.StatusOK || yaml.Unmarshal(mihomo.Body.Bytes(), &config) != nil ||
			len(config.Groups) == 0 || !slices.Equal(config.Groups[0].Proxies, item.members) {
			t.Fatalf("Mihomo groups = %d, %+v, want first group members %v", mihomo.Code, config.Groups, item.members)
		}
		public := performRequest(t, handler, http.MethodGet, personalPath(item.subscription.Personal.SubscriptionShadowrocketURL), nil, nil)
		preview := performRequest(t, handler, http.MethodGet, path(item.subscription)+"/shadowrocket-preview", nil, admin)
		var conf map[string]string
		if public.Code != http.StatusOK || preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &conf) != nil ||
			conf["conf"] != public.Body.String() {
			t.Fatalf("preview/public output mismatch: %d %d", preview.Code, public.Code)
		}
		rocketMembers := strings.ReplaceAll(strings.Join(item.members, ","), "🇯🇵 ", "")
		wantGroup := preset.Groups[0].Name + " = select," + rocketMembers + ",policy-select-name=" + item.wantDefault
		if !strings.Contains(conf["conf"], "\n"+wantGroup+"\n") {
			t.Fatalf("missing Shadowrocket group %q", wantGroup)
		}
	}
	var reopened payload
	legacy := performRequest(t, handler, http.MethodPut, bindingsPath, map[string]any{"routing_bindings": subscriptionstore.RoutingBindings{key: {backup.ID}}}, admin)
	if legacy.Code != http.StatusOK || json.Unmarshal(legacy.Body.Bytes(), &reopened) != nil || reopened.Personal.ShadowrocketDefaults[key] != target {
		t.Fatal("legacy client lost the stored default")
	}
	remove := performRequest(t, handler, http.MethodPut, path(first)+"/nodes", map[string]any{"nodes": []map[string]any{
		{"id": backup.ID, "source_type": "landing", "source_id": node.ID, "display_name": "Backup", "enabled": true},
	}}, admin)
	if remove.Code != http.StatusOK || json.Unmarshal(remove.Body.Bytes(), &reopened) != nil || reopened.Personal.ShadowrocketDefaults[key] != target {
		t.Fatal("node deletion altered the optional default")
	}
	preview := performRequest(t, handler, http.MethodGet, path(first)+"/shadowrocket-preview", nil, admin)
	var conf map[string]string
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &conf) != nil ||
		conf["policy_warning"] == "" || strings.Contains(conf["conf"], "policy-select-name") {
		t.Fatal("preview did not report the missing default")
	}
	clear := performRequest(t, handler, http.MethodPut, bindingsPath, map[string]any{
		"shadowrocket_defaults": map[string]any{key: nil},
	}, admin)
	reopened = payload{}
	if clear.Code != http.StatusOK || json.Unmarshal(clear.Body.Bytes(), &reopened) != nil || len(reopened.Personal.ShadowrocketDefaults) != 0 ||
		len(reopened.Personal.RoutingBindings[key]) != 1 || reopened.Personal.RoutingBindings[key][0] != backup.ID {
		t.Fatalf("explicit default clear: %d %s", clear.Code, clear.Body)
	}
}

func TestPersonalSubscriptionAPIAdminVIPOwnerAndPublicLinks(t *testing.T) {
	db, handler, adminCookie, adminID := setupAccountTest(t)
	defer db.Close()
	vipCookie, _ := registerAccount(t, db, handler, adminCookie, "vip", "vip-user")
	userCookie, _ := registerAccount(t, db, handler, adminCookie, "carpool", "normal-user")

	forbidden := performRequest(t, handler, http.MethodGet, "/api/personal-subscriptions", nil, userCookie)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("normal user personal subscriptions = %d %s", forbidden.Code, forbidden.Body.String())
	}
	created := performRequest(t, handler, http.MethodPost, "/api/personal-subscriptions", map[string]any{
		"name": "我的日常", "subscription_title": "My Daily", "client_name": "admin", "enabled": true,
	}, adminCookie)
	var payload struct {
		Personal personalSubscriptionResponse `json:"personal_subscription"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &payload) != nil ||
		payload.Personal.Name != "我的日常" || payload.Personal.ClientName != "admin" ||
		!strings.Contains(payload.Personal.SubscriptionAutoURL, "/sub/personal/") {
		t.Fatalf("create personal subscription = %d %s", created.Code, created.Body.String())
	}
	id := payload.Personal.ID
	crossOwner := performRequest(t, handler, http.MethodGet,
		"/api/personal-subscriptions/"+strconv.FormatInt(id, 10), nil, vipCookie)
	if crossOwner.Code != http.StatusNotFound {
		t.Fatalf("VIP read admin personal subscription = %d %s", crossOwner.Code, crossOwner.Body.String())
	}
	vipCreated := performRequest(t, handler, http.MethodPost, "/api/personal-subscriptions", map[string]any{
		"name": "VIP 自用", "client_name": "vip-user", "enabled": true,
	}, vipCookie)
	if vipCreated.Code != http.StatusCreated {
		t.Fatalf("VIP create personal subscription = %d %s", vipCreated.Code, vipCreated.Body.String())
	}

	landing, err := landingstore.NewService(db).Create(t.Context(), adminID, landingstore.CreateInput{
		Name: "UK", Visibility: landingstore.VisibilityPrivate,
		URI: "vless://uuid@uk.example.com:443?type=tcp&security=tls&sni=uk.example.com#Old",
	})
	if err != nil {
		t.Fatal(err)
	}
	nodes := performRequest(t, handler, http.MethodPut,
		"/api/personal-subscriptions/"+strconv.FormatInt(id, 10)+"/nodes", map[string]any{
			"nodes": []map[string]any{
				{
					"source_type": "landing", "source_id": landing.ID, "display_name": "🇬🇧 英国 | 家宽", "enabled": true,
				},
				{
					"source_type": "landing", "source_id": landing.ID, "display_name": "🇬🇧 英国 | 备用入口", "enabled": true,
					"entry_host": "backup.example.com", "entry_port": 8443,
				},
			},
		}, adminCookie)
	if nodes.Code != http.StatusOK || json.Unmarshal(nodes.Body.Bytes(), &payload) != nil ||
		len(payload.Personal.Nodes) != 2 || payload.Personal.Nodes[1].EntryHost == nil ||
		*payload.Personal.Nodes[1].EntryHost != "backup.example.com" || payload.Personal.Nodes[1].EntryPort == nil ||
		*payload.Personal.Nodes[1].EntryPort != 8443 {
		t.Fatalf("set personal nodes = %d %s", nodes.Code, nodes.Body.String())
	}

	base64Response := performRequest(t, handler, http.MethodGet, personalPath(payload.Personal.SubscriptionBase64URL), nil, nil)
	decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(base64Response.Body.String()))
	if base64Response.Code != http.StatusOK || decodeErr != nil ||
		strings.Count(string(decoded), "vless://") != 2 || !strings.Contains(string(decoded), "%F0%9F%87%AC%F0%9F%87%A7") ||
		!strings.Contains(string(decoded), "backup.example.com:8443") {
		t.Fatalf("public personal Base64 = %d %q, decoded %q, %v", base64Response.Code, base64Response.Body.String(), decoded, decodeErr)
	}
	if got := base64Response.Header().Get("Content-Disposition"); got != "inline; filename*=UTF-8''My%20Daily" {
		t.Fatalf("personal Base64 Content-Disposition = %q", got)
	}
	if value := base64Response.Header().Get("Subscription-Userinfo"); value != "" {
		t.Fatalf("personal subscription must not advertise a subscription-level quota: %q", value)
	}
	autoHTTP := jsonRequest(t, http.MethodGet, personalPath(payload.Personal.SubscriptionAutoURL), nil)
	autoHTTP.Header.Set("User-Agent", "Mihomo/1.0")
	autoRequest := httptest.NewRecorder()
	handler.ServeHTTP(autoRequest, autoHTTP)
	var autoYAML struct {
		Proxies []struct {
			Name   string `yaml:"name"`
			Server string `yaml:"server"`
			Port   int    `yaml:"port"`
		} `yaml:"proxies"`
	}
	if autoRequest.Code != http.StatusOK || yaml.Unmarshal(autoRequest.Body.Bytes(), &autoYAML) != nil ||
		len(autoYAML.Proxies) != 2 || autoYAML.Proxies[0].Name != "🇬🇧 英国 | 家宽" ||
		autoYAML.Proxies[1].Name != "🇬🇧 英国 | 备用入口" || autoYAML.Proxies[1].Server != "backup.example.com" ||
		autoYAML.Proxies[1].Port != 8443 {
		t.Fatalf("public personal Auto = %d %s", autoRequest.Code, autoRequest.Body.String())
	}
	if got := autoRequest.Header().Get("Content-Disposition"); got != "inline; filename*=UTF-8''My%20Daily" {
		t.Fatalf("personal Auto Content-Disposition = %q", got)
	}
	mihomoResponse := performRequest(t, handler, http.MethodGet,
		personalPath(payload.Personal.SubscriptionMihomoURL), nil, nil)
	if mihomoResponse.Code != http.StatusOK ||
		mihomoResponse.Header().Get("Content-Disposition") != "inline; filename*=UTF-8''My%20Daily" {
		t.Fatalf("personal Mihomo headers = %d %v", mihomoResponse.Code, mihomoResponse.Header())
	}
	preview := performRequest(t, handler, http.MethodGet,
		"/api/personal-subscriptions/"+strconv.FormatInt(id, 10)+"/mihomo-preview", nil, adminCookie)
	var previewPayload map[string]string
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &previewPayload) != nil ||
		!strings.Contains(previewPayload["yaml"], "英国 | 家宽") {
		t.Fatalf("personal Mihomo preview = %d %s", preview.Code, preview.Body.String())
	}

	shadowrocketPath := personalPath(payload.Personal.SubscriptionShadowrocketURL)
	rocket := performRequest(t, handler, http.MethodGet, shadowrocketPath, nil, nil)
	if rocket.Code != http.StatusOK || !strings.Contains(rocket.Body.String(), "[General]") ||
		!strings.Contains(rocket.Body.String(), "backup.example.com,8443") ||
		rocket.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		rocket.Header().Get("Content-Disposition") != "inline; filename*=UTF-8''My%20Daily.conf" {
		t.Fatalf("personal Shadowrocket status %d", rocket.Code)
	}
	rocketPreviewPath := "/api/personal-subscriptions/" + strconv.FormatInt(id, 10) + "/shadowrocket-preview"
	rocketPreview := performRequest(t, handler, http.MethodGet, rocketPreviewPath, nil, adminCookie)
	var rocketPayload map[string]string
	if rocketPreview.Code != http.StatusOK || json.Unmarshal(rocketPreview.Body.Bytes(), &rocketPayload) != nil ||
		rocketPayload["conf"] != rocket.Body.String() {
		t.Fatal("Shadowrocket preview differs from public output")
	}
	for _, check := range []struct {
		cookie *http.Cookie
		status int
	}{{nil, http.StatusUnauthorized}, {vipCookie, http.StatusNotFound}, {userCookie, http.StatusForbidden}} {
		if response := performRequest(t, handler, http.MethodGet, rocketPreviewPath, nil, check.cookie); response.Code != check.status {
			t.Fatalf("preview permission: %d want %d", response.Code, check.status)
		}
	}
	rocketAuto := httptest.NewRecorder()
	rocketRequest := httptest.NewRequest(http.MethodGet, personalPath(payload.Personal.SubscriptionAutoURL), nil)
	rocketRequest.Header.Set("User-Agent", "Shadowrocket/2.2")
	handler.ServeHTTP(rocketAuto, rocketRequest)
	if rocketAuto.Code != http.StatusOK || rocketAuto.Body.String() != rocket.Body.String() {
		t.Fatal("personal Shadowrocket auto detection")
	}
	providerPath := personalPath(payload.Personal.SubscriptionMihomoProviderURL)
	nodes = performRequest(t, handler, http.MethodPut, "/api/personal-subscriptions/"+strconv.FormatInt(id, 10)+"/nodes", map[string]any{
		"nodes": []map[string]any{
			{"id": payload.Personal.Nodes[0].ID, "source_type": "landing", "source_id": landing.ID, "display_name": "🇬🇧 英国 | 家宽", "enabled": true, "dialer_proxy": "机场入口"},
			{"id": payload.Personal.Nodes[1].ID, "source_type": "landing", "source_id": landing.ID, "display_name": "🇬🇧 英国 | 备用入口", "enabled": true, "entry_host": "backup.example.com", "entry_port": 8443},
		},
	}, adminCookie)
	if nodes.Code != http.StatusOK || json.Unmarshal(nodes.Body.Bytes(), &payload) != nil || payload.Personal.Nodes[0].DialerProxy != "机场入口" {
		t.Fatal("save chain setting failed")
	}
	provider := performRequest(t, handler, http.MethodGet, providerPath, nil, nil)
	var providerYAML map[string]any
	if provider.Code != http.StatusOK || yaml.Unmarshal(provider.Body.Bytes(), &providerYAML) != nil || len(providerYAML) != 1 || !strings.Contains(provider.Body.String(), "dialer-proxy: 机场入口") || provider.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("provider status %d", provider.Code)
	}
	if response := performRequest(t, handler, http.MethodGet, personalPath(payload.Personal.SubscriptionMihomoURL), nil, nil); response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "未在完整配置中定义") {
		t.Fatal("full config accepted unresolved upstream")
	}
	if response := performRequest(t, handler, http.MethodGet, personalPath(payload.Personal.SubscriptionBase64URL), nil, nil); response.Code != http.StatusOK || response.Body.String() != base64Response.Body.String() || response.Header().Get("X-VPS-Panel-Chain-Warning") == "" {
		t.Fatal("base64 compatibility or warning")
	}
	if response := performRequest(t, handler, http.MethodGet, shadowrocketPath, nil, nil); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "# 第 1 个节点的前置代理未写入 Shadowrocket 配置") ||
		!strings.Contains(response.Body.String(), "英国 | 家宽 = vless,") || response.Header().Get("X-VPS-Panel-Chain-Warning") == "" {
		t.Fatal("Shadowrocket compatibility or warning")
	}
	if response := performRequest(t, handler, http.MethodGet, "/sub/personal/not-a-token/mihomo-provider", nil, nil); response.Code != http.StatusNotFound {
		t.Fatal("invalid provider token accepted")
	}
	// Restore the unchained fixture for the remaining format lifecycle checks.
	if _, err := db.Exec(`UPDATE personal_subscription_nodes SET dialer_proxy = '' WHERE group_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var originalProviders string
	if err := db.QueryRow(`SELECT rule_providers_yaml FROM subscription_routing_presets WHERE is_default = 1`).Scan(&originalProviders); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []bool{false, true} {
		var providers map[string]map[string]any
		if err := yaml.Unmarshal([]byte(originalProviders), &providers); err != nil {
			t.Fatal(err)
		}
		reason := `format="yaml"`
		if missing {
			delete(providers, "Gemini")
			reason = "未找到规则源"
		} else {
			providers["Gemini"]["format"] = "yaml"
			providers["Gemini"]["url"] = "https://example.com/custom.yaml"
		}
		encoded, err := yaml.Marshal(providers)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE subscription_routing_presets SET rule_providers_yaml = ? WHERE is_default = 1`, string(encoded)); err != nil {
			t.Fatal(err)
		}
		response := performRequest(t, handler, http.MethodGet, rocketPreviewPath, nil, adminCookie)
		var problem map[string]string
		if response.Code != http.StatusUnprocessableEntity || json.Unmarshal(response.Body.Bytes(), &problem) != nil {
			t.Fatalf("preview error status %d", response.Code)
		}
		for _, want := range []string{"第 4 条规则", "RULE-SET,Gemini,🤖 AI", `规则源 "Gemini"`, reason} {
			if !strings.Contains(problem["error"], want) {
				t.Errorf("preview lost %q: %s", want, problem["error"])
			}
		}
		if strings.Contains(problem["error"], "password=") || strings.Contains(problem["error"], "uuid") {
			t.Fatal("preview error contains credentials")
		}
		if !missing {
			if response := performRequest(t, handler, http.MethodGet, personalPath(payload.Personal.SubscriptionMihomoURL), nil, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "https://example.com/custom.yaml") {
				t.Fatal("custom YAML provider no longer works in Mihomo")
			}
		}
		if response := performRequest(t, handler, http.MethodGet, personalPath(payload.Personal.SubscriptionBase64URL), nil, nil); response.Code != http.StatusOK || response.Body.String() != base64Response.Body.String() {
			t.Fatal("Base64 output changed")
		}
	}
	if _, err := db.Exec(`UPDATE subscription_routing_presets SET rule_providers_yaml = ? WHERE is_default = 1`, originalProviders); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE personal_subscription_groups SET enabled = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if response := performRequest(t, handler, http.MethodGet, shadowrocketPath, nil, nil); response.Code != http.StatusForbidden {
		t.Fatal("disabled subscription is available")
	}
	if response := performRequest(t, handler, http.MethodGet, providerPath, nil, nil); response.Code != http.StatusForbidden {
		t.Fatal("disabled provider is available")
	}
	if _, err := db.Exec(`UPDATE personal_subscription_groups SET enabled = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	oldPath := personalPath(payload.Personal.SubscriptionBase64URL)
	regenerated := performRequest(t, handler, http.MethodPost,
		"/api/personal-subscriptions/"+strconv.FormatInt(id, 10)+"/token/regenerate", nil, adminCookie)
	if regenerated.Code != http.StatusOK || json.Unmarshal(regenerated.Body.Bytes(), &payload) != nil {
		t.Fatalf("regenerate personal token = %d %s", regenerated.Code, regenerated.Body.String())
	}
	if old := performRequest(t, handler, http.MethodGet, shadowrocketPath, nil, nil); old.Code != http.StatusNotFound {
		t.Fatal("old Shadowrocket token still available")
	}
	if old := performRequest(t, handler, http.MethodGet, oldPath, nil, nil); old.Code != http.StatusNotFound {
		t.Fatalf("old personal token = %d %s", old.Code, old.Body.String())
	}
	if old := performRequest(t, handler, http.MethodGet, providerPath, nil, nil); old.Code != http.StatusNotFound {
		t.Fatal("old provider token still available")
	}
	if current := performRequest(t, handler, http.MethodGet, personalPath(payload.Personal.SubscriptionBase64URL), nil, nil); current.Code != http.StatusOK {
		t.Fatalf("new personal token = %d %s", current.Code, current.Body.String())
	}
}

func TestRoutingProvidersUseStructuredAPI(t *testing.T) {
	db, handler, adminCookie, _ := setupAccountTest(t)
	defer db.Close()
	provider := map[string]any{
		"name": "Google", "url": "https://example.com/google.yaml", "type": "http",
		"behavior": "classical", "format": "yaml", "interval": 86400,
	}
	created := performRequest(t, handler, http.MethodPost, "/api/admin/subscription/routing-presets", map[string]any{
		"name": "Structured", "enabled": true,
		"groups":         []map[string]any{{"name": "Google", "type": "select", "proxies": []string{"DIRECT"}}},
		"rule_providers": []map[string]any{provider},
		"rules":          []string{"RULE-SET,Google,Google", "MATCH,Google"},
	}, adminCookie)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), "rule_providers_yaml") ||
		!strings.Contains(created.Body.String(), `"rule_providers":[{"name":"Google"`) {
		t.Fatalf("structured routing provider create = %d %s", created.Code, created.Body.String())
	}
	var stored string
	if err := db.QueryRow(`SELECT rule_providers_yaml FROM subscription_routing_presets WHERE name = 'Structured'`).Scan(&stored); err != nil ||
		!strings.Contains(stored, "Google:") || !strings.Contains(stored, "interval: 86400") {
		t.Fatalf("stored provider YAML = %q, %v", stored, err)
	}
}

func personalPath(raw string) string {
	value, _ := url.Parse(raw)
	return value.RequestURI()
}
