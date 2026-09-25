package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

type createdLandingResponse struct {
	Landing landingResponse `json:"landing"`
}

func createLandingForAPI(t *testing.T, handler http.Handler, cookie *http.Cookie, request createLandingRequest) landingResponse {
	t.Helper()
	response := performRequest(t, handler, http.MethodPost, "/api/landings", request, cookie)
	if response.Code != http.StatusCreated {
		t.Fatalf("create landing = %d, %s", response.Code, response.Body.String())
	}
	var created createdLandingResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created.Landing
}

func TestLandingAPIAccessAndSecretRedaction(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/api/landings"}, {http.MethodPost, "/api/landings"},
		{http.MethodGet, "/api/landings/1"}, {http.MethodPatch, "/api/landings/1"},
		{http.MethodDelete, "/api/landings/1"}, {http.MethodGet, "/api/relays/1/landing-share"},
	} {
		response := performRequest(t, handler, target.method, target.path, nil, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s = %d", target.method, target.path, response.Code)
		}
	}

	privateSecret := "private-uuid"
	private := createLandingForAPI(t, handler, accounts.adminCookie, createLandingRequest{
		URI: "vless://" + privateSecret + "@private.example.com:443?type=tcp#Private",
	})
	if private.Visibility != "private" || private.Name != "Private" || !private.OwnedByMe {
		t.Fatalf("private landing = %+v", private)
	}
	publicSecret := "public-password"
	public := createLandingForAPI(t, handler, accounts.adminCookie, createLandingRequest{
		Name: "Public SS", Visibility: "public",
		URI: "ss://aes-256-gcm:" + publicSecret + "@public.example.com:8388#Public",
	})

	adminList := performRequest(t, handler, http.MethodGet, "/api/landings", nil, accounts.adminCookie)
	memberList := performRequest(t, handler, http.MethodGet, "/api/landings", nil, accounts.memberCookie)
	for label, response := range map[string]string{"admin": adminList.Body.String(), "member": memberList.Body.String()} {
		if strings.Contains(response, `"uri"`) || strings.Contains(response, privateSecret) || strings.Contains(response, publicSecret) {
			t.Fatalf("%s landing list leaked secret: %s", label, response)
		}
	}
	if !strings.Contains(adminList.Body.String(), `"Private"`) || !strings.Contains(adminList.Body.String(), `"Public SS"`) {
		t.Fatalf("admin list = %s", adminList.Body.String())
	}
	if strings.Contains(memberList.Body.String(), `"Private"`) || !strings.Contains(memberList.Body.String(), `"Public SS"`) ||
		!strings.Contains(memberList.Body.String(), `"owned_by_me":false`) {
		t.Fatalf("member list = %s", memberList.Body.String())
	}

	privatePath := "/api/landings/" + strconv.FormatInt(private.ID, 10)
	publicPath := "/api/landings/" + strconv.FormatInt(public.ID, 10)
	for _, target := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, privatePath, nil},
		{http.MethodPatch, privatePath, map[string]string{"name": "Nope"}},
		{http.MethodDelete, privatePath, nil},
		{http.MethodPatch, publicPath, map[string]string{"name": "Nope"}},
		{http.MethodDelete, publicPath, nil},
	} {
		response := performRequest(t, handler, target.method, target.path, target.body, accounts.memberCookie)
		if response.Code != http.StatusNotFound {
			t.Fatalf("member %s %s = %d, %s", target.method, target.path, response.Code, response.Body.String())
		}
	}
	publicGet := performRequest(t, handler, http.MethodGet, publicPath, nil, accounts.memberCookie)
	if publicGet.Code != http.StatusOK || strings.Contains(publicGet.Body.String(), `"uri"`) || strings.Contains(publicGet.Body.String(), publicSecret) {
		t.Fatalf("member public landing Get = %d, %s", publicGet.Code, publicGet.Body.String())
	}
	changedProtocol := "vless://uuid@example.com:443"
	protocolUpdate := performRequest(t, handler, http.MethodPatch, publicPath,
		updateLandingRequest{URI: &changedProtocol}, accounts.adminCookie)
	if protocolUpdate.Code != http.StatusConflict || !strings.Contains(protocolUpdate.Body.String(), "外部节点协议创建后不能修改") {
		t.Fatalf("landing protocol update = %d, %s", protocolUpdate.Code, protocolUpdate.Body.String())
	}

	unsupported := performRequest(t, handler, http.MethodPost, "/api/landings", createLandingRequest{URI: "trojan://secret@example.com:443"}, accounts.adminCookie)
	if unsupported.Code != http.StatusBadRequest || !strings.Contains(unsupported.Body.String(), "当前仅支持导入 VLESS 和 Shadowsocks 外部节点") {
		t.Fatalf("unsupported landing = %d, %s", unsupported.Code, unsupported.Body.String())
	}
	plugin := performRequest(t, handler, http.MethodPost, "/api/landings", createLandingRequest{URI: "ss://aes-256-gcm:password@example.com:8388?plugin=x"}, accounts.adminCookie)
	if plugin.Code != http.StatusBadRequest || !strings.Contains(plugin.Body.String(), "当前暂不支持带 plugin 的 Shadowsocks 外部节点") {
		t.Fatalf("plugin landing = %d, %s", plugin.Code, plugin.Body.String())
	}
	memberPrivate := createLandingForAPI(t, handler, accounts.memberCookie, createLandingRequest{
		Name: "Member Private", URI: "vless://member-uuid@example.com:443",
	})
	adminBypass := performRequest(t, handler, http.MethodGet,
		"/api/landings/"+strconv.FormatInt(memberPrivate.ID, 10), nil, accounts.adminCookie)
	if adminBypass.Code != http.StatusNotFound {
		t.Fatalf("admin bypassed member private landing = %d, %s", adminBypass.Code, adminBypass.Body.String())
	}
}

func TestLandingRelaySharePermissionsRewritesAndDependencyBumps(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	source := createAccessTestServer(t, handler, accounts.memberCookie, "Relay Source", "public", nil)
	vlessSecret := "uuid-a"
	landing := createLandingForAPI(t, handler, accounts.adminCookie, createLandingRequest{
		Name: "US Home", Visibility: "public",
		URI: "vless://" + vlessSecret + "@landing.example.com:443?security=reality&type=tcp&sni=a.com&pbk=abc&sid=def&flow=xtls-rprx-vision#US%20Home",
	})
	landingID := landing.ID
	relayCreation := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: source.Server.ID, Name: "Tokyo Relay", ListenPort: 9502,
		EntryHostMode: "manual", EntryHost: "relay.example.com",
		TargetType: "landing", TargetLandingID: &landingID, TargetHost: "ignored.example.com", TargetPort: 1,
		Network: "tcp",
	}, accounts.memberCookie)
	if relayCreation.Code != http.StatusCreated {
		t.Fatalf("create landing Relay = %d, %s", relayCreation.Code, relayCreation.Body.String())
	}
	if strings.Contains(relayCreation.Body.String(), vlessSecret) || strings.Contains(relayCreation.Body.String(), `"uri"`) {
		t.Fatalf("Relay response leaked landing secret: %s", relayCreation.Body.String())
	}
	var created struct {
		Relay relayResponse `json:"relay"`
	}
	if err := json.Unmarshal(relayCreation.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Relay.TargetLandingID == nil || *created.Relay.TargetLandingID != landing.ID ||
		created.Relay.TargetLandingName != "US Home" || created.Relay.TargetLandingProtocol != "vless" ||
		created.Relay.TargetProxyID != nil || created.Relay.TargetClientID != nil ||
		created.Relay.TargetHost != "landing.example.com" || created.Relay.TargetPort != 443 {
		t.Fatalf("created landing Relay = %+v", created.Relay)
	}
	relayList := performRequest(t, handler, http.MethodGet, "/api/relays", nil, accounts.memberCookie)
	if relayList.Code != http.StatusOK || strings.Contains(relayList.Body.String(), `"uri"`) || strings.Contains(relayList.Body.String(), vlessSecret) {
		t.Fatalf("Relay list leaked landing secret = %d, %s", relayList.Code, relayList.Body.String())
	}
	relayPath := "/api/relays/" + strconv.FormatInt(created.Relay.ID, 10)
	share := performRequest(t, handler, http.MethodGet, relayPath+"/landing-share", nil, accounts.memberCookie)
	if share.Code != http.StatusOK {
		t.Fatalf("landing share = %d, %s", share.Code, share.Body.String())
	}
	var shared relayLandingShareResponse
	if err := json.Unmarshal(share.Body.Bytes(), &shared); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(shared.URI)
	if err != nil || parsed.Host != "relay.example.com:9502" || parsed.User.String() != vlessSecret ||
		parsed.Query().Get("pbk") != "abc" || parsed.Query().Get("sid") != "def" || parsed.Query().Get("sni") != "a.com" ||
		parsed.Query().Get("flow") != "xtls-rprx-vision" || parsed.Fragment != "US Home - Tokyo Relay" || !shared.NetworkCompatible {
		t.Fatalf("landing share = %+v, URI %q, error %v", shared, shared.URI, err)
	}
	udp := "udp"
	if response := performRequest(t, handler, http.MethodPatch, relayPath, updateRelayRequest{Network: &udp}, accounts.memberCookie); response.Code != http.StatusOK {
		t.Fatalf("set landing Relay UDP = %d, %s", response.Code, response.Body.String())
	}
	incompatible := performRequest(t, handler, http.MethodGet, relayPath+"/landing-share", nil, accounts.memberCookie)
	if incompatible.Code != http.StatusOK || json.Unmarshal(incompatible.Body.Bytes(), &shared) != nil || shared.NetworkCompatible ||
		shared.NetworkNotice != "当前中转 Network 与该 VLESS 外部节点不兼容" {
		t.Fatalf("incompatible VLESS landing share = %d, %s", incompatible.Code, incompatible.Body.String())
	}
	tcp := "tcp"
	if response := performRequest(t, handler, http.MethodPatch, relayPath, updateRelayRequest{Network: &tcp}, accounts.memberCookie); response.Code != http.StatusOK {
		t.Fatalf("restore landing Relay TCP = %d, %s", response.Code, response.Body.String())
	}

	var versionBefore int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, source.Server.ID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	credentialOnly := "vless://uuid-b@landing.example.com:443?security=reality&type=tcp&sni=b.com&pbk=new#US"
	update := performRequest(t, handler, http.MethodPatch, "/api/landings/"+strconv.FormatInt(landing.ID, 10),
		updateLandingRequest{URI: &credentialOnly}, accounts.adminCookie)
	if update.Code != http.StatusOK {
		t.Fatalf("credential-only update = %d, %s", update.Code, update.Body.String())
	}
	var versionAfterCredential int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, source.Server.ID).Scan(&versionAfterCredential); err != nil {
		t.Fatal(err)
	}
	if versionAfterCredential != versionBefore {
		t.Fatalf("credential-only update version = %d, want %d", versionAfterCredential, versionBefore)
	}
	share = performRequest(t, handler, http.MethodGet, relayPath+"/landing-share", nil, accounts.memberCookie)
	if share.Code != http.StatusOK || !strings.Contains(share.Body.String(), "uuid-b") || strings.Contains(share.Body.String(), "uuid-a") {
		t.Fatalf("share after credential update = %d, %s", share.Code, share.Body.String())
	}
	endpointChange := "vless://uuid-b@new.example.com:8443?security=reality&type=tcp&sni=b.com&pbk=new#US"
	update = performRequest(t, handler, http.MethodPatch, "/api/landings/"+strconv.FormatInt(landing.ID, 10),
		updateLandingRequest{URI: &endpointChange}, accounts.adminCookie)
	if update.Code != http.StatusOK {
		t.Fatalf("endpoint update = %d, %s", update.Code, update.Body.String())
	}
	var versionAfterEndpoint int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, source.Server.ID).Scan(&versionAfterEndpoint); err != nil {
		t.Fatal(err)
	}
	if versionAfterEndpoint != versionBefore+1 {
		t.Fatalf("endpoint update version = %d, want %d", versionAfterEndpoint, versionBefore+1)
	}
	getRelay := performRequest(t, handler, http.MethodGet, relayPath, nil, accounts.memberCookie)
	if getRelay.Code != http.StatusOK || !strings.Contains(getRelay.Body.String(), `"target_host":"new.example.com"`) ||
		!strings.Contains(getRelay.Body.String(), `"target_port":8443`) || strings.Contains(getRelay.Body.String(), "uuid-b") {
		t.Fatalf("Relay after endpoint update = %d, %s", getRelay.Code, getRelay.Body.String())
	}

	private := "private"
	visibilityUpdate := performRequest(t, handler, http.MethodPatch, "/api/landings/"+strconv.FormatInt(landing.ID, 10),
		updateLandingRequest{Visibility: &private}, accounts.adminCookie)
	if visibilityUpdate.Code != http.StatusConflict || !strings.Contains(visibilityUpdate.Body.String(), "该外部节点正在被中转使用") {
		t.Fatalf("referenced visibility update = %d, %s", visibilityUpdate.Code, visibilityUpdate.Body.String())
	}
	deletion := performRequest(t, handler, http.MethodDelete, "/api/landings/"+strconv.FormatInt(landing.ID, 10), nil, accounts.adminCookie)
	if deletion.Code != http.StatusConflict {
		t.Fatalf("referenced landing deletion = %d, %s", deletion.Code, deletion.Body.String())
	}
}

func TestLandingRelayShareShadowsocksUnavailableAndPrivateIDOR(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	source := createAccessTestServer(t, handler, accounts.adminCookie, "Source", "public", nil)
	private := createLandingForAPI(t, handler, accounts.adminCookie, createLandingRequest{
		Name: "Private", URI: "ss://aes-256-gcm:secret@example.com:8388#SS",
	})
	privateID := private.ID
	for _, cookie := range []*http.Cookie{accounts.memberCookie} {
		response := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
			ServerID: source.Server.ID, Name: "Forbidden", ListenPort: 9502,
			TargetType: "landing", TargetLandingID: &privateID, Network: "tcp,udp",
		}, cookie)
		if response.Code != http.StatusNotFound {
			t.Fatalf("private landing Relay create = %d, %s", response.Code, response.Body.String())
		}
	}
	creation := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: source.Server.ID, Name: "Private Relay", ListenPort: 9502,
		EntryHostMode: "manual", EntryHost: "2001:db8::10",
		TargetType: "landing", TargetLandingID: &privateID, Network: "tcp,udp",
	}, accounts.adminCookie)
	if creation.Code != http.StatusCreated {
		t.Fatalf("owner private Relay create = %d, %s", creation.Code, creation.Body.String())
	}
	var created struct {
		Relay relayResponse `json:"relay"`
	}
	if err := json.Unmarshal(creation.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	path := "/api/relays/" + strconv.FormatInt(created.Relay.ID, 10)
	for _, suffix := range []string{"", "/landing-share"} {
		response := performRequest(t, handler, http.MethodGet, path+suffix, nil, accounts.memberCookie)
		if response.Code != http.StatusNotFound {
			t.Fatalf("private landing Relay member Get%s = %d, %s", suffix, response.Code, response.Body.String())
		}
	}
	ownerShare := performRequest(t, handler, http.MethodGet, path+"/landing-share", nil, accounts.adminCookie)
	if ownerShare.Code != http.StatusOK {
		t.Fatalf("owner Shadowsocks landing share = %d, %s", ownerShare.Code, ownerShare.Body.String())
	}
	var ssShare relayLandingShareResponse
	if err := json.Unmarshal(ownerShare.Body.Bytes(), &ssShare); err != nil {
		t.Fatal(err)
	}
	parsedSS, err := url.Parse(ssShare.URI)
	password, hasPassword := parsedSS.User.Password()
	if err != nil || parsedSS.Host != "[2001:db8::10]:9502" || parsedSS.User.Username() != "aes-256-gcm" ||
		!hasPassword || password != "secret" || parsedSS.Fragment != "SS - Private Relay" {
		t.Fatalf("Shadowsocks landing share = %q, %v", ssShare.URI, err)
	}
	unavailableCreation := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: source.Server.ID, Name: "Unavailable", ListenPort: 9504,
		TargetType: "landing", TargetLandingID: &privateID, Network: "tcp,udp",
	}, accounts.adminCookie)
	var unavailableRelay struct {
		Relay relayResponse `json:"relay"`
	}
	if unavailableCreation.Code != http.StatusCreated || json.Unmarshal(unavailableCreation.Body.Bytes(), &unavailableRelay) != nil {
		t.Fatalf("unavailable Relay create = %d, %s", unavailableCreation.Code, unavailableCreation.Body.String())
	}
	unavailable := performRequest(t, handler, http.MethodGet,
		"/api/relays/"+strconv.FormatInt(unavailableRelay.Relay.ID, 10)+"/landing-share", nil, accounts.adminCookie)
	if unavailable.Code != http.StatusConflict || !strings.Contains(unavailable.Body.String(), "中转入口地址不可用") {
		t.Fatalf("unavailable landing share = %d, %s", unavailable.Code, unavailable.Body.String())
	}
	manualCreation := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: source.Server.ID, Name: "Manual", ListenPort: 9503,
		EntryHostMode: "manual", EntryHost: "relay.example.com",
		TargetType: "manual", TargetHost: "example.com", TargetPort: 443, Network: "tcp",
	}, accounts.adminCookie)
	var manual struct {
		Relay relayResponse `json:"relay"`
	}
	if manualCreation.Code != http.StatusCreated || json.Unmarshal(manualCreation.Body.Bytes(), &manual) != nil {
		t.Fatalf("manual Relay create = %d, %s", manualCreation.Code, manualCreation.Body.String())
	}
	wrongTarget := performRequest(t, handler, http.MethodGet,
		"/api/relays/"+strconv.FormatInt(manual.Relay.ID, 10)+"/landing-share", nil, accounts.adminCookie)
	if wrongTarget.Code != http.StatusBadRequest || !strings.Contains(wrongTarget.Body.String(), "仅外部节点中转") {
		t.Fatalf("manual landing share = %d, %s", wrongTarget.Code, wrongTarget.Body.String())
	}
}
