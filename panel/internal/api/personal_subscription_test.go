package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	"gopkg.in/yaml.v3"
)

func TestPersonalSubscriptionAPIAdminVIPOwnerAndPublicLinks(t *testing.T) {
	db, handler, adminCookie, adminID := setupAccountTest(t)
	defer db.Close()
	vipCookie, _ := registerAccount(t, db, handler, adminCookie, "vip", "vip-user")
	userCookie, _ := registerAccount(t, db, handler, adminCookie, "user", "normal-user")

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
			"nodes": []map[string]any{{
				"source_type": "landing", "source_id": landing.ID, "display_name": "🇬🇧 英国 | 家宽", "enabled": true,
			}},
		}, adminCookie)
	if nodes.Code != http.StatusOK || !strings.Contains(nodes.Body.String(), `"status":"ready"`) {
		t.Fatalf("set personal nodes = %d %s", nodes.Code, nodes.Body.String())
	}

	base64Response := performRequest(t, handler, http.MethodGet, personalPath(payload.Personal.SubscriptionBase64URL), nil, nil)
	decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(base64Response.Body.String()))
	if base64Response.Code != http.StatusOK || decodeErr != nil || !strings.Contains(string(decoded), "%F0%9F%87%AC%F0%9F%87%A7") {
		t.Fatalf("public personal Base64 = %d %q, decoded %q, %v", base64Response.Code, base64Response.Body.String(), decoded, decodeErr)
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
			Name string `yaml:"name"`
		} `yaml:"proxies"`
	}
	if autoRequest.Code != http.StatusOK || yaml.Unmarshal(autoRequest.Body.Bytes(), &autoYAML) != nil ||
		len(autoYAML.Proxies) != 1 || autoYAML.Proxies[0].Name != "🇬🇧 英国 | 家宽" {
		t.Fatalf("public personal Auto = %d %s", autoRequest.Code, autoRequest.Body.String())
	}
	preview := performRequest(t, handler, http.MethodGet,
		"/api/personal-subscriptions/"+strconv.FormatInt(id, 10)+"/mihomo-preview", nil, adminCookie)
	var previewPayload map[string]string
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &previewPayload) != nil ||
		!strings.Contains(previewPayload["yaml"], "英国 | 家宽") {
		t.Fatalf("personal Mihomo preview = %d %s", preview.Code, preview.Body.String())
	}

	oldPath := personalPath(payload.Personal.SubscriptionBase64URL)
	regenerated := performRequest(t, handler, http.MethodPost,
		"/api/personal-subscriptions/"+strconv.FormatInt(id, 10)+"/token/regenerate", nil, adminCookie)
	if regenerated.Code != http.StatusOK || json.Unmarshal(regenerated.Body.Bytes(), &payload) != nil {
		t.Fatalf("regenerate personal token = %d %s", regenerated.Code, regenerated.Body.String())
	}
	if old := performRequest(t, handler, http.MethodGet, oldPath, nil, nil); old.Code != http.StatusNotFound {
		t.Fatalf("old personal token = %d %s", old.Code, old.Body.String())
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
