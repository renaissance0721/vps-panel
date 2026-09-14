package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestRelayAPIAuthenticationAndCRUD(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/api/relays"}, {http.MethodPost, "/api/relays"},
		{http.MethodGet, "/api/relays/1"}, {http.MethodPatch, "/api/relays/1"},
		{http.MethodDelete, "/api/relays/1"},
	} {
		response := performRequest(t, handler, target.method, target.path, nil, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s = %d", target.method, target.path, response.Code)
		}
	}
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialization.Result().Cookies()[0]
	serverCreation := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "Relay Source"}, cookie)
	var server createdServerResponse
	if err := json.Unmarshal(serverCreation.Body.Bytes(), &server); err != nil {
		t.Fatal(err)
	}
	creation := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: server.Server.ID, Name: "Manual TCP", ListenPort: 9502,
		TargetType: "manual", TargetHost: "example.com", TargetPort: 443,
		Network: "tcp",
	}, cookie)
	if creation.Code != http.StatusCreated {
		t.Fatalf("create relay = %d, %s", creation.Code, creation.Body.String())
	}
	var created struct {
		Relay relayResponse `json:"relay"`
	}
	if err := json.Unmarshal(creation.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Relay.ListenAddress != "0.0.0.0" || !created.Relay.Enabled || !created.Relay.TargetAddressReady {
		t.Fatalf("created relay = %+v", created.Relay)
	}
	path := "/api/relays/" + strconv.FormatInt(created.Relay.ID, 10)
	if response := performRequest(t, handler, http.MethodGet, path, nil, cookie); response.Code != http.StatusOK {
		t.Fatalf("get relay = %d, %s", response.Code, response.Body.String())
	}
	name, enabled := "Disabled", false
	update := performRequest(t, handler, http.MethodPatch, path, updateRelayRequest{Name: &name, Enabled: &enabled}, cookie)
	if update.Code != http.StatusOK {
		t.Fatalf("update relay = %d, %s", update.Code, update.Body.String())
	}
	var updated struct {
		Relay relayResponse `json:"relay"`
	}
	if json.Unmarshal(update.Body.Bytes(), &updated) != nil || updated.Relay.Name != name || updated.Relay.Enabled {
		t.Fatalf("updated relay = %+v", updated.Relay)
	}
	list := performRequest(t, handler, http.MethodGet, "/api/relays", nil, cookie)
	if list.Code != http.StatusOK || !json.Valid(list.Body.Bytes()) {
		t.Fatalf("list relays = %d, %s", list.Code, list.Body.String())
	}
	if response := performRequest(t, handler, http.MethodDelete, path, nil, cookie); response.Code != http.StatusNoContent {
		t.Fatalf("delete relay = %d, %s", response.Code, response.Body.String())
	}
}

func TestReferencedProxyDeletionReturnsConflict(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialization.Result().Cookies()[0]
	serverCreation := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "Server"}, cookie)
	var server createdServerResponse
	if err := json.Unmarshal(serverCreation.Body.Bytes(), &server); err != nil {
		t.Fatal(err)
	}
	proxyCreation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: server.Server.ID, Name: "Target", ListenPort: 443, Security: "reality",
		ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Phone",
		EntryHostMode: "manual", EntryHost: "target.example.com",
	}, cookie)
	if proxyCreation.Code != http.StatusCreated {
		t.Fatalf("create proxy = %d, %s", proxyCreation.Code, proxyCreation.Body.String())
	}
	var proxy struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if err := json.Unmarshal(proxyCreation.Body.Bytes(), &proxy); err != nil {
		t.Fatal(err)
	}
	creation := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: server.Server.ID, Name: "Reference", ListenPort: 9502,
		TargetType: "proxy", TargetProxyID: &proxy.Proxy.ID, Network: "tcp",
	}, cookie)
	if creation.Code != http.StatusCreated {
		t.Fatalf("create proxy relay = %d, %s", creation.Code, creation.Body.String())
	}
	deletion := performRequest(t, handler, http.MethodDelete, "/api/proxies/"+strconv.FormatInt(proxy.Proxy.ID, 10), nil, cookie)
	if deletion.Code != http.StatusConflict {
		t.Fatalf("delete referenced proxy = %d, %s", deletion.Code, deletion.Body.String())
	}
}

func TestProxyTargetChangeBumpsRelaySourceVersion(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	initialization := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialization.Result().Cookies()[0]
	createServer := func(name string) createdServerResponse {
		response := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": name}, cookie)
		var created createdServerResponse
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil {
			t.Fatalf("create server %q = %d, %s", name, response.Code, response.Body.String())
		}
		return created
	}
	source, target := createServer("Relay Source"), createServer("Proxy Target")
	proxyCreation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: target.Server.ID, Name: "Target", ListenPort: 443, Security: "reality",
		ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Phone",
		EntryHostMode: "manual", EntryHost: "old.example.com",
	}, cookie)
	var proxy struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if proxyCreation.Code != http.StatusCreated || json.Unmarshal(proxyCreation.Body.Bytes(), &proxy) != nil {
		t.Fatalf("create proxy = %d, %s", proxyCreation.Code, proxyCreation.Body.String())
	}
	relayCreation := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: source.Server.ID, Name: "Proxy Relay", ListenPort: 9502,
		TargetType: "proxy", TargetProxyID: &proxy.Proxy.ID, Network: "tcp",
	}, cookie)
	if relayCreation.Code != http.StatusCreated {
		t.Fatalf("create relay = %d, %s", relayCreation.Code, relayCreation.Body.String())
	}
	var versionBefore int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, source.Server.ID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	listenPort := 8443
	proxyUpdate := performRequest(t, handler, http.MethodPatch, "/api/proxies/"+strconv.FormatInt(proxy.Proxy.ID, 10), updateProxyRequest{
		ListenPort: &listenPort,
	}, cookie)
	if proxyUpdate.Code != http.StatusOK {
		t.Fatalf("update target proxy = %d, %s", proxyUpdate.Code, proxyUpdate.Body.String())
	}
	var versionAfter int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, source.Server.ID).Scan(&versionAfter); err != nil {
		t.Fatal(err)
	}
	if versionAfter != versionBefore+1 {
		t.Fatalf("Relay source version = %d, want %d", versionAfter, versionBefore+1)
	}
	list := performRequest(t, handler, http.MethodGet, "/api/relays", nil, cookie)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"target_port":8443`) {
		t.Fatalf("resolved target after proxy update = %d, %s", list.Code, list.Body.String())
	}
}
