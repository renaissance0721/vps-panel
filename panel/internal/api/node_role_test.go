package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	landingstore "github.com/renaissance0721/vps-panel/panel/internal/landing"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestNodeRoleAPIDTOValidationAndPermissions(t *testing.T) {
	f := newProxyDeleteFixture(t)
	serverID := f.createServer(t, "target")
	var managed []proxyResponse
	for index, role := range []string{"", "direct", "landing"} {
		response := performRequest(t, f.handler, http.MethodPost, "/api/proxies", createProxyRequest{
			ServerID: serverID, Name: "managed", NodeRole: role, ListenPort: 443 + index, Security: "reality", ServerName: "example.com", RealityTarget: "example.com:443",
		}, f.cookie)
		if response.Code != http.StatusCreated {
			t.Fatalf("create managed %q: %d %s", role, response.Code, response.Body.String())
		}
		var payload struct {
			Proxy proxyResponse `json:"proxy"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		want := role
		if want == "" {
			want = "direct"
		}
		if payload.Proxy.NodeRole != want {
			t.Fatalf("create DTO role=%q", payload.Proxy.NodeRole)
		}
		managed = append(managed, payload.Proxy)
	}
	invalidCreate := performRequest(t, f.handler, http.MethodPost, "/api/proxies", createProxyRequest{ServerID: serverID, Name: "invalid", NodeRole: "other", ListenPort: 500, Security: "reality", ServerName: "example.com", RealityTarget: "example.com:443"}, f.cookie)
	if invalidCreate.Code != http.StatusBadRequest {
		t.Fatalf("invalid create role: %d", invalidCreate.Code)
	}
	var versionBefore int64
	if err := f.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	// Explicitly unsupported Agent capabilities must not block a metadata-only edit.
	if _, err := f.db.Exec(`INSERT INTO agents (server_id, token_hash, implementation, version, api_version, capabilities_json, registered_at, created_at, updated_at) VALUES (?, 'test-hash', 'third-party', 'test', ?, '[]', 1, 1, 1)`, serverID, agentcontrol.CurrentAPIVersion); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"landing", "direct"} {
		response := performRequest(t, f.handler, http.MethodPatch, fmt.Sprintf("/api/proxies/%d", managed[0].ID), map[string]any{"node_role": role}, f.cookie)
		if response.Code != http.StatusOK {
			t.Fatalf("patch role: %d %s", response.Code, response.Body.String())
		}
	}
	var versionAfter int64
	if err := f.db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, serverID).Scan(&versionAfter); err != nil || versionAfter != versionBefore {
		t.Fatalf("role bumped desired version: %v", err)
	}
	for _, path := range []string{"/api/proxies", fmt.Sprintf("/api/proxies/%d", managed[2].ID)} {
		response := performRequest(t, f.handler, http.MethodGet, path, nil, f.cookie)
		var body map[string]json.RawMessage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("GET %s: %d", path, response.Code)
		}
		if path == "/api/proxies" {
			var values []proxyResponse
			if err := json.Unmarshal(body["proxies"], &values); err != nil || len(values) != 3 || values[0].NodeRole != "landing" {
				t.Fatalf("List role DTO: %v", err)
			}
		} else {
			var value proxyResponse
			if err := json.Unmarshal(body["proxy"], &value); err != nil || value.NodeRole != "landing" {
				t.Fatalf("Get role DTO: %v", err)
			}
		}
	}
	vipCookie, _ := registerAccount(t, f.db, f.handler, f.cookie, "vip", "other-manager")
	if _, err := f.db.Exec(`UPDATE servers SET visibility = 'private' WHERE id = ?`, serverID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO server_access (server_id, user_id) VALUES (?, 1)`, serverID); err != nil {
		t.Fatal(err)
	}
	denied := performRequest(t, f.handler, http.MethodPatch, fmt.Sprintf("/api/proxies/%d", managed[0].ID), map[string]string{"node_role": "landing"}, vipCookie)
	if denied.Code != http.StatusForbidden && denied.Code != http.StatusNotFound {
		t.Fatalf("unauthorized managed role edit: %d", denied.Code)
	}
	for _, role := range []string{"", "direct", "landing"} {
		value := createLandingForAPI(t, f.handler, f.cookie, createLandingRequest{Name: "external", NodeRole: role, Visibility: "public", URI: "vless://uuid@example.com:443"})
		want := role
		if want == "" {
			want = "direct"
		}
		if value.NodeRole != want {
			t.Fatalf("external role DTO=%q", value.NodeRole)
		}
		path := fmt.Sprintf("/api/landings/%d", value.ID)
		denied := performRequest(t, f.handler, http.MethodPatch, path, map[string]string{"node_role": "landing"}, vipCookie)
		if denied.Code != http.StatusNotFound {
			t.Fatalf("public non-owner role update: %d", denied.Code)
		}
		for _, updatedRole := range []string{"landing", "direct"} {
			response := performRequest(t, f.handler, http.MethodPatch, path, map[string]string{"node_role": updatedRole}, f.cookie)
			var payload createdLandingResponse
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil || payload.Landing.NodeRole != updatedRole {
				t.Fatalf("external patch DTO: %d", response.Code)
			}
		}
		for _, invalid := range []string{"", "invalid"} {
			response := performRequest(t, f.handler, http.MethodPatch, path, map[string]string{"node_role": invalid}, f.cookie)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid external role: %d", response.Code)
			}
		}
	}
	for _, resource := range []string{fmt.Sprintf("proxies/%d", managed[0].ID), "landings/1"} {
		response := performRequest(t, f.handler, http.MethodPatch, "/api/"+resource, map[string]string{"node_role": "invalid"}, f.cookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid role PATCH %s: %d", resource, response.Code)
		}
	}
}

func TestPersonalSourceAPIRejectsClassifiedSourcesAndRelayFlagsAreReadOnly(t *testing.T) {
	f := newProxyDeleteFixture(t)
	targetID, sourceID := f.createServer(t, "target"), f.createServer(t, "source")
	direct := f.createProxy(t, targetID, 443, "direct")
	landing := f.createProxy(t, targetID, 444, "landing")
	role := "landing"
	if _, _, err := f.proxies.Update(t.Context(), landing.ID, proxystore.UpdateInput{NodeRole: &role}); err != nil {
		t.Fatal(err)
	}
	landings := landingstore.NewService(f.db)
	externalDirect, err := landings.Create(t.Context(), 1, landingstore.CreateInput{URI: "vless://uuid@example.com:443"})
	if err != nil {
		t.Fatal(err)
	}
	externalLanding, err := landings.Create(t.Context(), 1, landingstore.CreateInput{URI: "vless://uuid@example.com:443", NodeRole: "landing"})
	if err != nil {
		t.Fatal(err)
	}
	local, _, err := relaystore.NewService(f.db).Create(t.Context(), relaystore.CreateInput{ServerID: sourceID, Name: "local", ListenPort: 9000, EntryHostMode: "manual", EntryHost: "relay.example.com", TargetType: "landing", TargetLandingID: &externalLanding.ID, Network: "tcp", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	published, _, err := f.subscriptions.CreatePublishedNode(t.Context(), subscriptionstore.CreatePublishedNodeInput{Name: "published", Mode: "relay", SourceServerID: &sourceID, TargetProxyID: landing.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	group, err := f.subscriptions.CreatePersonalSubscription(t.Context(), subscriptionstore.PersonalSubscriptionActor{UserID: 1, Role: "admin"}, subscriptionstore.CreatePersonalSubscriptionInput{Name: "personal", ClientName: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []struct {
		kind    string
		id      int64
		allowed bool
	}{
		{"proxy", direct.ID, true}, {"landing", externalDirect.ID, true}, {"relay", local.ID, true},
		{"proxy", landing.ID, false}, {"landing", externalLanding.ID, false}, {"relay", *published.RelayID, false},
	} {
		body := map[string]any{"nodes": []map[string]any{{"source_type": source.kind, "source_id": source.id, "display_name": "node", "enabled": true}}}
		response := performRequest(t, f.handler, http.MethodPut, fmt.Sprintf("/api/personal-subscriptions/%d/nodes", group.ID), body, f.cookie)
		if source.allowed && response.Code != http.StatusOK || !source.allowed && response.Code != http.StatusBadRequest {
			t.Fatalf("personal API %s/%d allowed=%v: %d", source.kind, source.id, source.allowed, response.Code)
		}
	}
	response := performRequest(t, f.handler, http.MethodGet, "/api/relays", nil, f.cookie)
	var payload struct {
		Relays []relayResponse `json:"relays"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil || len(payload.Relays) != 2 {
		t.Fatalf("relay List DTO: %d", response.Code)
	}
	for _, value := range payload.Relays {
		if value.SubscriptionPublished != (value.ID == *published.RelayID) {
			t.Fatal("wrong relay classification DTO")
		}
	}
	for _, enabled := range []bool{true, false} {
		response := performRequest(t, f.handler, http.MethodPatch, fmt.Sprintf("/api/relays/%d", *published.RelayID), map[string]bool{"enabled": enabled}, f.cookie)
		if response.Code < 400 {
			t.Fatal("published relay toggle accepted")
		}
	}
	response = performRequest(t, f.handler, http.MethodDelete, fmt.Sprintf("/api/relays/%d", *published.RelayID), nil, f.cookie)
	if response.Code != http.StatusConflict {
		t.Fatalf("published relay delete: %d", response.Code)
	}
	response = performRequest(t, f.handler, http.MethodPatch, fmt.Sprintf("/api/relays/%d", local.ID), map[string]bool{"subscription_published": true}, f.cookie)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("writable relay classification: %d", response.Code)
	}
}
