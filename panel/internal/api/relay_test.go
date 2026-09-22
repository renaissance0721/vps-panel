package api

import (
	"encoding/json"
	"net/http"
	"net/url"
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
		{http.MethodDelete, "/api/relays/1"}, {http.MethodGet, "/api/relays/1/clients"},
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
	ignoredTargetID := int64(999)
	creation := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: server.Server.ID, Name: "Manual TCP", ListenPort: 9502,
		TargetType: "manual", TargetProxyID: &ignoredTargetID, TargetClientID: &ignoredTargetID,
		TargetHost: "example.com", TargetPort: 443,
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
	if created.Relay.ListenAddress != "0.0.0.0" || created.Relay.EntryHostMode != "auto" ||
		created.Relay.EntryHost != "" || !created.Relay.Enabled || !created.Relay.TargetAddressReady ||
		created.Relay.TargetProxyID != nil || created.Relay.TargetClientID != nil {
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

func TestRelayDerivedVLESSShareUsesRelayEndpointAndClientLifecycle(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO server_system_info
		(server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, public_ipv4, agent_version, reported_at)
		VALUES (?, '', '', '', '', '', '[]', '[]', '198.51.100.10', '', 1)`, source.Server.ID); err != nil {
		t.Fatal(err)
	}
	proxyCreation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: target.Server.ID, Name: "REALITY Target", ListenPort: 443,
		EntryHostMode: "manual", EntryHost: "target.example.com", Security: "reality",
		ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Phone",
	}, cookie)
	var proxy struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if proxyCreation.Code != http.StatusCreated || json.Unmarshal(proxyCreation.Body.Bytes(), &proxy) != nil {
		t.Fatalf("create VLESS proxy = %d, %s", proxyCreation.Code, proxyCreation.Body.String())
	}
	proxyID := proxy.Proxy.ID
	clientID := proxy.Proxy.Clients[0].ID
	secondClient := performRequest(t, handler, http.MethodPost,
		"/api/proxies/"+strconv.FormatInt(proxyID, 10)+"/clients", createClientRequest{Name: "Tablet"}, cookie)
	if secondClient.Code != http.StatusCreated {
		t.Fatalf("create second target Client = %d, %s", secondClient.Code, secondClient.Body.String())
	}
	missingClient := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: source.Server.ID, Name: "Missing Client", ListenPort: 35153,
		TargetType: "proxy", TargetProxyID: &proxyID, Network: "tcp",
	}, cookie)
	if missingClient.Code != http.StatusBadRequest {
		t.Fatalf("missing target Client = %d, %s", missingClient.Code, missingClient.Body.String())
	}
	otherProxyCreation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: target.Server.ID, Name: "Other Target", ListenPort: 8443,
		EntryHostMode: "manual", EntryHost: "other.example.com", Security: "reality",
		ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Other",
	}, cookie)
	var otherProxy struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if otherProxyCreation.Code != http.StatusCreated || json.Unmarshal(otherProxyCreation.Body.Bytes(), &otherProxy) != nil {
		t.Fatalf("create other Proxy = %d, %s", otherProxyCreation.Code, otherProxyCreation.Body.String())
	}
	foreignClientID := otherProxy.Proxy.Clients[0].ID
	mismatchedClient := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: source.Server.ID, Name: "Wrong Client", ListenPort: 35154,
		TargetType: "proxy", TargetProxyID: &proxyID, TargetClientID: &foreignClientID, Network: "tcp",
	}, cookie)
	if mismatchedClient.Code != http.StatusBadRequest {
		t.Fatalf("foreign target Client = %d, %s", mismatchedClient.Code, mismatchedClient.Body.String())
	}
	relayCreation := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: source.Server.ID, Name: "KR Relay", ListenPort: 35152,
		TargetType: "proxy", TargetProxyID: &proxyID, TargetClientID: &clientID, Network: "tcp",
	}, cookie)
	var relay struct {
		Relay relayResponse `json:"relay"`
	}
	if relayCreation.Code != http.StatusCreated || json.Unmarshal(relayCreation.Body.Bytes(), &relay) != nil ||
		relay.Relay.EntryHostMode != "auto" || relay.Relay.EntryAddress != "198.51.100.10" {
		t.Fatalf("create Relay = %d, %s", relayCreation.Code, relayCreation.Body.String())
	}
	path := "/api/relays/" + strconv.FormatInt(relay.Relay.ID, 10)
	if relay.Relay.TargetClientID == nil || *relay.Relay.TargetClientID != clientID {
		t.Fatalf("saved target client = %+v", relay.Relay.TargetClientID)
	}
	if relay.Relay.TargetClientName != "Phone" {
		t.Fatalf("saved target client name = %q", relay.Relay.TargetClientName)
	}
	listResponse := performRequest(t, handler, http.MethodGet, "/api/relays", nil, cookie)
	var listed struct {
		Relays []relayResponse `json:"relays"`
	}
	if listResponse.Code != http.StatusOK || json.Unmarshal(listResponse.Body.Bytes(), &listed) != nil ||
		len(listed.Relays) != 1 || listed.Relays[0].TargetClientName != "Phone" {
		t.Fatalf("list Relay target client name = %d, %s", listResponse.Code, listResponse.Body.String())
	}
	mismatchedUpdate := performRequest(t, handler, http.MethodPatch, path,
		updateRelayRequest{TargetClientID: &foreignClientID}, cookie)
	if mismatchedUpdate.Code != http.StatusBadRequest {
		t.Fatalf("update to foreign target Client = %d, %s", mismatchedUpdate.Code, mismatchedUpdate.Body.String())
	}
	missingClientOnProxyChange := performRequest(t, handler, http.MethodPatch, path,
		updateRelayRequest{TargetProxyID: &otherProxy.Proxy.ID}, cookie)
	if missingClientOnProxyChange.Code != http.StatusBadRequest {
		t.Fatalf("change target Proxy without Client = %d, %s", missingClientOnProxyChange.Code, missingClientOnProxyChange.Body.String())
	}
	directResponse := performRequest(t, handler, http.MethodGet,
		"/api/clients/"+strconv.FormatInt(proxy.Proxy.Clients[0].ID, 10)+"/share", nil, cookie)
	var direct struct {
		Share clientShareResponse `json:"share"`
	}
	if directResponse.Code != http.StatusOK || json.Unmarshal(directResponse.Body.Bytes(), &direct) != nil {
		t.Fatalf("direct share = %d, %s", directResponse.Code, directResponse.Body.String())
	}
	sharesResponse := performRequest(t, handler, http.MethodGet, path+"/clients", nil, cookie)
	var shares struct {
		Clients []relayClientShareResponse `json:"clients"`
	}
	if sharesResponse.Code != http.StatusOK || json.Unmarshal(sharesResponse.Body.Bytes(), &shares) != nil || len(shares.Clients) != 1 || shares.Clients[0].Client.ID != clientID {
		t.Fatalf("Relay shares = %d, %s", sharesResponse.Code, sharesResponse.Body.String())
	}
	directURI, _ := url.Parse(direct.Share.URI)
	relayURI, _ := url.Parse(shares.Clients[0].URI)
	if relayURI.Host != "198.51.100.10:35152" || directURI.User.String() != relayURI.User.String() ||
		directURI.RawQuery != relayURI.RawQuery || directURI.Fragment != relayURI.Fragment ||
		!shares.Clients[0].NetworkCompatible || shares.Clients[0].NetworkNotice != "" ||
		strings.Contains(sharesResponse.Body.String(), `"uuid"`) || strings.Contains(sharesResponse.Body.String(), `"private_key"`) {
		t.Fatalf("derived VLESS share = %+v, direct %q, Relay %q", shares.Clients[0], direct.Share.URI, shares.Clients[0].URI)
	}
	if _, err := db.Exec(`UPDATE server_system_info SET public_ipv4 = '198.51.100.11' WHERE server_id = ?`, source.Server.ID); err != nil {
		t.Fatal(err)
	}
	sharesResponse = performRequest(t, handler, http.MethodGet, path+"/clients", nil, cookie)
	if json.Unmarshal(sharesResponse.Body.Bytes(), &shares) != nil {
		t.Fatal("decode updated auto Relay share")
	}
	relayURI, _ = url.Parse(shares.Clients[0].URI)
	if relayURI.Host != "198.51.100.11:35152" || directURI.RawQuery != relayURI.RawQuery {
		t.Fatalf("updated auto Relay share URI = %q", shares.Clients[0].URI)
	}

	mode, host := "manual", "2001:db8::10"
	update := performRequest(t, handler, http.MethodPatch, path, updateRelayRequest{EntryHostMode: &mode, EntryHost: &host}, cookie)
	if update.Code != http.StatusOK {
		t.Fatalf("update Relay IPv6 entry = %d, %s", update.Code, update.Body.String())
	}
	sharesResponse = performRequest(t, handler, http.MethodGet, path+"/clients", nil, cookie)
	if json.Unmarshal(sharesResponse.Body.Bytes(), &shares) != nil {
		t.Fatal("decode IPv6 Relay share")
	}
	relayURI, _ = url.Parse(shares.Clients[0].URI)
	if relayURI.Host != "[2001:db8::10]:35152" {
		t.Fatalf("IPv6 Relay share URI = %q", shares.Clients[0].URI)
	}

	if _, err := db.Exec(`UPDATE clients SET expires_at = 1 WHERE id = ?`, proxy.Proxy.Clients[0].ID); err != nil {
		t.Fatal(err)
	}
	network := "udp"
	if response := performRequest(t, handler, http.MethodPatch, path, updateRelayRequest{Network: &network}, cookie); response.Code != http.StatusOK {
		t.Fatalf("update Relay network = %d, %s", response.Code, response.Body.String())
	}
	sharesResponse = performRequest(t, handler, http.MethodGet, path+"/clients", nil, cookie)
	if json.Unmarshal(sharesResponse.Body.Bytes(), &shares) != nil || shares.Clients[0].NetworkCompatible ||
		shares.Clients[0].NetworkNotice != "当前中转 Network 与该 Proxy 不兼容" ||
		shares.Clients[0].Client.Status != "expired" || shares.Clients[0].Client.EffectiveEnabled {
		t.Fatalf("incompatible expired Relay share = %s", sharesResponse.Body.String())
	}
	if _, err := db.Exec(`UPDATE clients SET expires_at = NULL, enabled = 0 WHERE id = ?`, proxy.Proxy.Clients[0].ID); err != nil {
		t.Fatal(err)
	}
	sharesResponse = performRequest(t, handler, http.MethodGet, path+"/clients", nil, cookie)
	if json.Unmarshal(sharesResponse.Body.Bytes(), &shares) != nil || shares.Clients[0].Client.Status != "disabled" ||
		shares.Clients[0].Client.EffectiveEnabled {
		t.Fatalf("disabled Relay client = %s", sharesResponse.Body.String())
	}
	if _, err := db.Exec(`UPDATE clients SET enabled = 1, traffic_limit_bytes = 1 WHERE id = ?`, proxy.Proxy.Clients[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO client_metrics
		(client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes, cycle_downlink_bytes, cycle_started_at, updated_at)
		VALUES (?, 1, 0, 1, 0, 1, 1)`, proxy.Proxy.Clients[0].ID); err != nil {
		t.Fatal(err)
	}
	sharesResponse = performRequest(t, handler, http.MethodGet, path+"/clients", nil, cookie)
	if json.Unmarshal(sharesResponse.Body.Bytes(), &shares) != nil || shares.Clients[0].Client.Status != "exhausted" ||
		shares.Clients[0].Client.EffectiveEnabled {
		t.Fatalf("quota-exhausted Relay client = %s", sharesResponse.Body.String())
	}
	deletedClient := performRequest(t, handler, http.MethodDelete,
		"/api/clients/"+strconv.FormatInt(clientID, 10), nil, cookie)
	if deletedClient.Code != http.StatusNoContent {
		t.Fatalf("delete selected Client = %d, %s", deletedClient.Code, deletedClient.Body.String())
	}
	var afterDelete struct {
		Relay relayResponse `json:"relay"`
	}
	getAfterDelete := performRequest(t, handler, http.MethodGet, path, nil, cookie)
	if getAfterDelete.Code != http.StatusOK || json.Unmarshal(getAfterDelete.Body.Bytes(), &afterDelete) != nil || afterDelete.Relay.TargetClientID != nil {
		t.Fatalf("Relay after Client deletion = %d, %s", getAfterDelete.Code, getAfterDelete.Body.String())
	}
	shareAfterDelete := performRequest(t, handler, http.MethodGet, path+"/clients", nil, cookie)
	if shareAfterDelete.Code != http.StatusOK || !strings.Contains(shareAfterDelete.Body.String(), `"clients":[]`) {
		t.Fatalf("Relay shares after Client deletion = %d, %s", shareAfterDelete.Code, shareAfterDelete.Body.String())
	}
}

func TestRelayDerivedShadowsocksSharePreservesCredentialsAndWarnsForTCPOnly(t *testing.T) {
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
	serverResponse := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "Relay and Target"}, cookie)
	var server createdServerResponse
	if json.Unmarshal(serverResponse.Body.Bytes(), &server) != nil {
		t.Fatal("decode server")
	}
	proxyCreation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: server.Server.ID, Name: "SS Target", Protocol: "shadowsocks",
		Method: "2022-blake3-aes-256-gcm", ListenPort: 8388,
		EntryHostMode: "manual", EntryHost: "target.example.com", FirstClientName: "Main",
	}, cookie)
	var proxy struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if proxyCreation.Code != http.StatusCreated || json.Unmarshal(proxyCreation.Body.Bytes(), &proxy) != nil {
		t.Fatalf("create SS proxy = %d, %s", proxyCreation.Code, proxyCreation.Body.String())
	}
	proxyID := proxy.Proxy.ID
	clientID := proxy.Proxy.Clients[0].ID
	relayCreation := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: server.Server.ID, Name: "SS Relay", ListenPort: 9502,
		EntryHostMode: "manual", EntryHost: "relay.example.com",
		TargetType: "proxy", TargetProxyID: &proxyID, TargetClientID: &clientID, Network: "tcp",
	}, cookie)
	var relay struct {
		Relay relayResponse `json:"relay"`
	}
	if relayCreation.Code != http.StatusCreated || json.Unmarshal(relayCreation.Body.Bytes(), &relay) != nil {
		t.Fatalf("create SS Relay = %d, %s", relayCreation.Code, relayCreation.Body.String())
	}
	directResponse := performRequest(t, handler, http.MethodGet,
		"/api/clients/"+strconv.FormatInt(proxy.Proxy.Clients[0].ID, 10)+"/share", nil, cookie)
	var direct struct {
		Share clientShareResponse `json:"share"`
	}
	_ = json.Unmarshal(directResponse.Body.Bytes(), &direct)
	sharesResponse := performRequest(t, handler, http.MethodGet,
		"/api/relays/"+strconv.FormatInt(relay.Relay.ID, 10)+"/clients", nil, cookie)
	var shares struct {
		Clients []relayClientShareResponse `json:"clients"`
	}
	if sharesResponse.Code != http.StatusOK || json.Unmarshal(sharesResponse.Body.Bytes(), &shares) != nil || len(shares.Clients) != 1 {
		t.Fatalf("SS Relay shares = %d, %s", sharesResponse.Code, sharesResponse.Body.String())
	}
	directURI, _ := url.Parse(direct.Share.URI)
	relayURI, _ := url.Parse(shares.Clients[0].URI)
	if relayURI.Host != "relay.example.com:9502" || directURI.User.String() != relayURI.User.String() ||
		directURI.Fragment != relayURI.Fragment || shares.Clients[0].Protocol != "shadowsocks" ||
		!shares.Clients[0].NetworkCompatible || shares.Clients[0].NetworkNotice != "此中转仅转发 TCP，UDP 不可用" {
		t.Fatalf("derived SS share = %+v, direct %q, Relay %q", shares.Clients[0], direct.Share.URI, shares.Clients[0].URI)
	}
}

func TestRelayClientSharesRejectUnavailableEntryAndSkipManualTarget(t *testing.T) {
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
	serverCreation := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "Source"}, cookie)
	var server createdServerResponse
	_ = json.Unmarshal(serverCreation.Body.Bytes(), &server)
	manual := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: server.Server.ID, Name: "Manual", ListenPort: 9502,
		TargetType: "manual", TargetHost: "target.example.com", TargetPort: 443, Network: "tcp",
	}, cookie)
	var relay struct {
		Relay relayResponse `json:"relay"`
	}
	_ = json.Unmarshal(manual.Body.Bytes(), &relay)
	response := performRequest(t, handler, http.MethodGet,
		"/api/relays/"+strconv.FormatInt(relay.Relay.ID, 10)+"/clients", nil, cookie)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"clients":[]`) {
		t.Fatalf("manual target Relay shares = %d, %s", response.Code, response.Body.String())
	}
	proxyCreation := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: server.Server.ID, Name: "Target", ListenPort: 8443,
		EntryHostMode: "manual", EntryHost: "target.example.com", Security: "reality",
		ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Phone",
	}, cookie)
	var proxy struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if proxyCreation.Code != http.StatusCreated || json.Unmarshal(proxyCreation.Body.Bytes(), &proxy) != nil {
		t.Fatalf("create target Proxy = %d, %s", proxyCreation.Code, proxyCreation.Body.String())
	}
	proxyID := proxy.Proxy.ID
	clientID := proxy.Proxy.Clients[0].ID
	unavailable := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: server.Server.ID, Name: "Unavailable entry", ListenPort: 9503,
		TargetType: "proxy", TargetProxyID: &proxyID, TargetClientID: &clientID, Network: "tcp",
	}, cookie)
	_ = json.Unmarshal(unavailable.Body.Bytes(), &relay)
	response = performRequest(t, handler, http.MethodGet,
		"/api/relays/"+strconv.FormatInt(relay.Relay.ID, 10)+"/clients", nil, cookie)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "中转入口地址不可用") {
		t.Fatalf("unavailable auto Relay entry = %d, %s", response.Code, response.Body.String())
	}
	invalidHost := "https://bad.example.com/path"
	response = performRequest(t, handler, http.MethodPatch,
		"/api/relays/"+strconv.FormatInt(relay.Relay.ID, 10), updateRelayRequest{
			EntryHostMode: stringPointer("manual"), EntryHost: &invalidHost,
		}, cookie)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid Relay manual entry = %d, %s", response.Code, response.Body.String())
	}
}

func stringPointer(value string) *string { return &value }

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
		TargetType: "proxy", TargetProxyID: &proxy.Proxy.ID, TargetClientID: &proxy.Proxy.Clients[0].ID, Network: "tcp",
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
		TargetType: "proxy", TargetProxyID: &proxy.Proxy.ID, TargetClientID: &proxy.Proxy.Clients[0].ID, Network: "tcp",
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
