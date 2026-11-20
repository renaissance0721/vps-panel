package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSOCKS5ImportToPersonalProviderAPI(t *testing.T) {
	db, handler, cookie, _ := setupAccountTest(t)
	defer db.Close()
	other, _ := registerAccount(t, db, handler, cookie, "vip", "other")
	created := performRequest(t, handler, http.MethodPost, "/api/landings", map[string]any{"uri": "socks5://test%40user:fictional%3Apassword@203.0.113.10:9492#Webshare-Japan", "node_role": "landing"}, cookie)
	var external struct {
		Landing landingResponse `json:"landing"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &external) != nil || external.Landing.Protocol != "socks5" || external.Landing.Visibility != "private" {
		t.Fatalf("import status %d", created.Code)
	}
	for _, path := range []string{"/api/landings", "/api/landings/" + strconv.FormatInt(external.Landing.ID, 10)} {
		response := performRequest(t, handler, http.MethodGet, path, nil, cookie)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "test%40user") || strings.Contains(response.Body.String(), "fictional") {
			t.Fatal("ordinary response leaked credentials")
		}
	}
	if response := performRequest(t, handler, http.MethodGet, "/api/landings/"+strconv.FormatInt(external.Landing.ID, 10)+"/share", nil, other); response.Code != http.StatusNotFound {
		t.Fatal("private share accessible")
	}
	created = performRequest(t, handler, http.MethodPost, "/api/personal-subscriptions", map[string]any{"name": "personal", "client_name": "admin", "enabled": true}, cookie)
	var personal struct {
		Personal personalSubscriptionResponse `json:"personal_subscription"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &personal) != nil {
		t.Fatalf("subscription status %d", created.Code)
	}
	path := "/api/personal-subscriptions/" + strconv.FormatInt(personal.Personal.ID, 10) + "/nodes"
	response := performRequest(t, handler, http.MethodPut, path, map[string]any{"nodes": []map[string]any{{"source_type": "landing", "source_id": external.Landing.ID, "display_name": "Webshare-Japan", "enabled": true, "dialer_proxy": "机场入口"}}}, cookie)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &personal) != nil {
		t.Fatalf("selection status %d", response.Code)
	}
	response = performRequest(t, handler, http.MethodGet, personalPath(personal.Personal.SubscriptionMihomoProviderURL), nil, nil)
	var out struct {
		Proxies []struct {
			Type     string `yaml:"type"`
			Username string `yaml:"username"`
			Password string `yaml:"password"`
			UDP      *bool  `yaml:"udp"`
			Dialer   string `yaml:"dialer-proxy"`
		} `yaml:"proxies"`
	}
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || yaml.Unmarshal(response.Body.Bytes(), &out) != nil || len(out.Proxies) != 1 {
		t.Fatalf("provider status %d", response.Code)
	}
	proxy := out.Proxies[0]
	if proxy.Type != "socks5" || proxy.Username != "test@user" || proxy.Password != "fictional:password" || proxy.UDP == nil || *proxy.UDP || proxy.Dialer != "机场入口" {
		t.Fatal("provider fields incorrect")
	}
	response = performRequest(t, handler, http.MethodGet, personalPath(personal.Personal.SubscriptionBase64URL), nil, nil)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "SOCKS5") || strings.Contains(response.Body.String(), "fictional") {
		t.Fatal("unsupported export must return safe explicit error")
	}
	rocket := performRequest(t, handler, http.MethodGet, personalPath(personal.Personal.SubscriptionShadowrocketURL), nil, nil)
	if rocket.Code != http.StatusOK || rocket.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
		rocket.Header().Get("Cache-Control") != "no-store" || rocket.Header().Get("X-VPS-Panel-Chain-Warning") == "" ||
		!strings.Contains(rocket.Body.String(), "Webshare-Japan = socks5,203.0.113.10,9492,test@user,fictional:password") {
		t.Fatalf("Shadowrocket SOCKS5 public export failed, status %d", rocket.Code)
	}
	previewPath := "/api/personal-subscriptions/" + strconv.FormatInt(personal.Personal.ID, 10) + "/shadowrocket-preview"
	preview := performRequest(t, handler, http.MethodGet, previewPath, nil, cookie)
	var previewBody map[string]string
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &previewBody) != nil ||
		previewBody["conf"] != rocket.Body.String() || !strings.Contains(previewBody["chain_warning"], "第 1 个节点") ||
		strings.Contains(previewBody["chain_warning"], "fictional") || strings.Contains(previewBody["chain_warning"], "test@user") {
		t.Fatalf("Shadowrocket preview lacks the public config or safe warning, status %d", preview.Code)
	}
}
