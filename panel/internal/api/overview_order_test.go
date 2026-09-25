package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func registerOverviewVIP(t *testing.T, handler http.Handler, adminCookie *http.Cookie) (*http.Cookie, int64) {
	t.Helper()
	invited := performRequest(t, handler, http.MethodPost, "/api/admin/invitations", nil, adminCookie)
	var invitation invitationResponse
	if invited.Code != http.StatusCreated || json.Unmarshal(invited.Body.Bytes(), &invitation) != nil {
		t.Fatalf("invite vip2 = %d, %s", invited.Code, invited.Body.String())
	}
	registered := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "vip2", "password": "another-strong-password",
	}, nil)
	if registered.Code != http.StatusCreated {
		t.Fatalf("register vip2 = %d, %s", registered.Code, registered.Body.String())
	}
	cookie := registered.Result().Cookies()[0]
	listed := performRequest(t, handler, http.MethodGet, "/api/users", nil, cookie)
	var users struct {
		Users []accessUserResponse `json:"users"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &users) != nil {
		t.Fatalf("read vip2 ID = %d, %s", listed.Code, listed.Body.String())
	}
	for _, user := range users.Users {
		if user.Username == "vip2" {
			return cookie, user.ID
		}
	}
	t.Fatal("vip2 missing from users")
	return nil, 0
}

func createOrderProxy(t *testing.T, handler http.Handler, cookie *http.Cookie, serverID int64, name string, port int) proxyResponse {
	t.Helper()
	response := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
		ServerID: serverID, Name: name, ListenPort: port, Security: "reality",
		EntryHostMode: "manual", EntryHost: "node.example.com",
		ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Client",
	}, cookie)
	var created struct {
		Proxy proxyResponse `json:"proxy"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("create Proxy %s = %d, %s", name, response.Code, response.Body.String())
	}
	return created.Proxy
}

func createOrderRelay(t *testing.T, handler http.Handler, cookie *http.Cookie, serverID int64, name string, port int) relayResponse {
	t.Helper()
	response := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: serverID, Name: name, ListenPort: port, TargetType: "manual",
		TargetHost: "example.com", TargetPort: 443, Network: "tcp",
	}, cookie)
	var created struct {
		Relay relayResponse `json:"relay"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("create Relay %s = %d, %s", name, response.Code, response.Body.String())
	}
	return created.Relay
}

func orderListIDs(t *testing.T, handler http.Handler, cookie *http.Cookie, path, key string) []int64 {
	t.Helper()
	response := performRequest(t, handler, http.MethodGet, path, nil, cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("list %s = %d, %s", path, response.Code, response.Body.String())
	}
	var data map[string][]struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(data[key]))
	for _, value := range data[key] {
		ids = append(ids, value.ID)
	}
	return ids
}

func expectOrderAPI(t *testing.T, handler http.Handler, cookie *http.Cookie, path, key string, want ...int64) {
	t.Helper()
	if got := orderListIDs(t, handler, cookie, path, key); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s order = %v, want %v", path, got, want)
	}
}

func moveOrderAPI(t *testing.T, handler http.Handler, cookie *http.Cookie, kind string, id int64, direction string, status int) {
	t.Helper()
	path := fmt.Sprintf("/api/%s/%d/reorder", kind, id)
	response := performRequest(t, handler, http.MethodPost, path, map[string]string{"direction": direction}, cookie)
	if response.Code != status {
		t.Fatalf("%s %s = %d, want %d: %s", path, direction, response.Code, status, response.Body.String())
	}
}

func TestOverviewCountsOnlyAccessibleActiveResourcesAndExposesSafeAccountSummary(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	vip2Cookie, vip2ID := registerOverviewVIP(t, handler, accounts.adminCookie)
	a := createAccessTestServer(t, handler, accounts.adminCookie, "A", "public", nil)
	b := createAccessTestServer(t, handler, accounts.adminCookie, "B", "private", []int64{accounts.adminID})
	c := createAccessTestServer(t, handler, accounts.memberCookie, "C", "private", []int64{accounts.memberID})
	d := createAccessTestServer(t, handler, vip2Cookie, "D", "private", []int64{vip2ID})
	e := createAccessTestServer(t, handler, accounts.adminCookie, "E", "public", nil)
	base := createOrderProxy(t, handler, accounts.adminCookie, a.Server.ID, "A-1", 8701)
	for _, test := range []struct {
		serverID int64
		count    int
		name     string
	}{
		{a.Server.ID, 1, "A"}, {b.Server.ID, 3, "B"},
		{c.Server.ID, 4, "C"}, {d.Server.ID, 5, "D"}, {e.Server.ID, 6, "E"},
	} {
		for i := 0; i < test.count; i++ {
			port := 8702 + int(test.serverID)*20 + i
			enabled := 1
			if test.name == "B" && i == 0 {
				enabled = 0 // Disabled records still count.
			}
			_, err := db.Exec(`INSERT INTO proxies
				(server_id,name,protocol,listen_port,entry_host_mode,entry_host,enabled,config_json,created_at,updated_at)
				SELECT ?, ?, protocol, ?, entry_host_mode, entry_host, ?, config_json, created_at, updated_at
				FROM proxies WHERE id = ?`, test.serverID, fmt.Sprintf("%s-%d", test.name, i+2), port, enabled, base.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	archived := performRequest(t, handler, http.MethodDelete, "/api/servers/"+strconv.FormatInt(e.Server.ID, 10)+"/force", nil, accounts.adminCookie)
	if archived.Code != http.StatusNoContent {
		t.Fatalf("archive E = %d, %s", archived.Code, archived.Body.String())
	}
	if response := performRequest(t, handler, http.MethodGet, "/api/overview", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous overview = %d", response.Code)
	}
	for _, test := range []struct {
		name       string
		cookie     *http.Cookie
		proxyCount int
		listCount  int
	}{
		{"admin", accounts.adminCookie, 5, 5},
		{"vip1", accounts.memberCookie, 6, 6},
		{"vip2", vip2Cookie, 7, 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(t, handler, http.MethodGet, "/api/overview", nil, test.cookie)
			if response.Code != http.StatusOK {
				t.Fatalf("overview = %d, %s", response.Code, response.Body.String())
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			if len(raw) != 3 {
				t.Fatalf("unexpected overview fields: %s", response.Body.String())
			}
			var data struct {
				ServerCount int                          `json:"server_count"`
				ProxyCount  int                          `json:"proxy_count"`
				Users       []map[string]json.RawMessage `json:"users"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if data.ServerCount != 2 || data.ProxyCount != test.proxyCount || len(data.Users) != 3 {
				t.Fatalf("overview = %+v", data)
			}
			for _, user := range data.Users {
				if len(user) != 2 || len(user["username"]) == 0 || len(user["role"]) == 0 {
					t.Fatalf("unsafe user summary: %v", user)
				}
			}
			roles := make(map[string]string)
			for _, user := range data.Users {
				var username, role string
				if json.Unmarshal(user["username"], &username) != nil || json.Unmarshal(user["role"], &role) != nil {
					t.Fatalf("invalid user summary: %v", user)
				}
				roles[username] = role
			}
			if !reflect.DeepEqual(roles, map[string]string{"admin": "admin", "member": "vip", "vip2": "vip"}) {
				t.Fatalf("account roles = %v", roles)
			}
			if got := len(orderListIDs(t, handler, test.cookie, "/api/proxies", "proxies")); got != test.listCount {
				t.Fatalf("overview/list proxy mismatch: %d != %d", data.ProxyCount, got)
			}
			for _, secret := range []string{"password_hash", "session", "token", "invitation", "agent_token"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatalf("overview leaked %s", secret)
				}
			}
		})
	}
}

func TestListReorderAPIIsPerUserPersistentAndDoesNotChangeBusinessState(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	a := createAccessTestServer(t, handler, accounts.adminCookie, "A", "public", nil)
	b := createAccessTestServer(t, handler, accounts.adminCookie, "B", "public", nil)
	c := createAccessTestServer(t, handler, accounts.adminCookie, "C", "public", nil)
	expectOrderAPI(t, handler, accounts.adminCookie, "/api/servers", "servers", c.Server.ID, b.Server.ID, a.Server.ID)
	moveOrderAPI(t, handler, accounts.adminCookie, "servers", b.Server.ID, "up", http.StatusNoContent)
	expectOrderAPI(t, handler, accounts.adminCookie, "/api/servers", "servers", b.Server.ID, c.Server.ID, a.Server.ID)
	moveOrderAPI(t, handler, accounts.memberCookie, "servers", b.Server.ID, "down", http.StatusNoContent)
	expectOrderAPI(t, handler, accounts.memberCookie, "/api/servers", "servers", c.Server.ID, a.Server.ID, b.Server.ID)
	expectOrderAPI(t, handler, accounts.adminCookie, "/api/servers", "servers", b.Server.ID, c.Server.ID, a.Server.ID)

	p1 := createOrderProxy(t, handler, accounts.adminCookie, a.Server.ID, "P1", 8801)
	p2 := createOrderProxy(t, handler, accounts.adminCookie, a.Server.ID, "P2", 8802)
	p3 := createOrderProxy(t, handler, accounts.adminCookie, a.Server.ID, "P3", 8803)
	r1 := createOrderRelay(t, handler, accounts.adminCookie, a.Server.ID, "R1", 9801)
	r2 := createOrderRelay(t, handler, accounts.adminCookie, a.Server.ID, "R2", 9802)
	r3 := createOrderRelay(t, handler, accounts.adminCookie, a.Server.ID, "R3", 9803)
	overviewBefore := performRequest(t, handler, http.MethodGet, "/api/overview", nil, accounts.adminCookie)
	if overviewBefore.Code != http.StatusOK {
		t.Fatalf("overview before reorder = %d", overviewBefore.Code)
	}
	var version, serverUpdatedAt, proxyUpdatedAt, relayUpdatedAt int64
	if err := db.QueryRow(`SELECT desired_state_version, updated_at FROM servers WHERE id = ?`, a.Server.ID).Scan(&version, &serverUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT updated_at FROM proxies WHERE id = ?`, p2.ID).Scan(&proxyUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT updated_at FROM relays WHERE id = ?`, r2.ID).Scan(&relayUpdatedAt); err != nil {
		t.Fatal(err)
	}
	moveOrderAPI(t, handler, accounts.adminCookie, "proxies", p2.ID, "up", http.StatusNoContent)
	moveOrderAPI(t, handler, accounts.memberCookie, "proxies", p1.ID, "up", http.StatusNoContent)
	expectOrderAPI(t, handler, accounts.adminCookie, "/api/proxies", "proxies", p2.ID, p3.ID, p1.ID)
	expectOrderAPI(t, handler, accounts.memberCookie, "/api/proxies", "proxies", p3.ID, p1.ID, p2.ID)
	moveOrderAPI(t, handler, accounts.adminCookie, "relays", r2.ID, "up", http.StatusNoContent)
	moveOrderAPI(t, handler, accounts.memberCookie, "relays", r1.ID, "up", http.StatusNoContent)
	expectOrderAPI(t, handler, accounts.adminCookie, "/api/relays", "relays", r2.ID, r3.ID, r1.ID)
	expectOrderAPI(t, handler, accounts.memberCookie, "/api/relays", "relays", r3.ID, r1.ID, r2.ID)
	overviewAfter := performRequest(t, handler, http.MethodGet, "/api/overview", nil, accounts.adminCookie)
	if overviewAfter.Code != http.StatusOK || overviewAfter.Body.String() != overviewBefore.Body.String() {
		t.Fatalf("reorder changed overview counts: before %s, after %s", overviewBefore.Body.String(), overviewAfter.Body.String())
	}
	var gotVersion, gotServerUpdatedAt, gotProxyUpdatedAt, gotRelayUpdatedAt int64
	if err := db.QueryRow(`SELECT desired_state_version, updated_at FROM servers WHERE id = ?`, a.Server.ID).Scan(&gotVersion, &gotServerUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT updated_at FROM proxies WHERE id = ?`, p2.ID).Scan(&gotProxyUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT updated_at FROM relays WHERE id = ?`, r2.ID).Scan(&gotRelayUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if gotVersion != version || gotServerUpdatedAt != serverUpdatedAt || gotProxyUpdatedAt != proxyUpdatedAt || gotRelayUpdatedAt != relayUpdatedAt {
		t.Fatalf("reorder changed business state: version %d→%d, times %d→%d %d→%d %d→%d",
			version, gotVersion, serverUpdatedAt, gotServerUpdatedAt, proxyUpdatedAt, gotProxyUpdatedAt, relayUpdatedAt, gotRelayUpdatedAt)
	}
	login := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d, %s", login.Code, login.Body.String())
	}
	newHandler := NewHandler(db, t.TempDir())
	expectOrderAPI(t, newHandler, login.Result().Cookies()[0], "/api/servers", "servers", b.Server.ID, c.Server.ID, a.Server.ID)
	expectOrderAPI(t, newHandler, login.Result().Cookies()[0], "/api/proxies", "proxies", p2.ID, p3.ID, p1.ID)
	expectOrderAPI(t, newHandler, login.Result().Cookies()[0], "/api/relays", "relays", r2.ID, r3.ID, r1.ID)
	moveOrderAPI(t, handler, accounts.adminCookie, "servers", b.Server.ID, "up", http.StatusNoContent)
	moveOrderAPI(t, handler, accounts.adminCookie, "servers", a.Server.ID, "down", http.StatusNoContent)
	moveOrderAPI(t, handler, accounts.adminCookie, "servers", b.Server.ID, "sideways", http.StatusBadRequest)
	moveOrderAPI(t, handler, accounts.adminCookie, "servers", 999999, "up", http.StatusNotFound)
	moveOrderAPI(t, handler, nil, "servers", b.Server.ID, "up", http.StatusUnauthorized)
	private := createAccessTestServer(t, handler, accounts.adminCookie, "Hidden", "private", []int64{accounts.adminID})
	moveOrderAPI(t, handler, accounts.memberCookie, "servers", private.Server.ID, "up", http.StatusNotFound)
	hiddenProxy := createOrderProxy(t, handler, accounts.adminCookie, private.Server.ID, "Hidden Proxy", 8804)
	moveOrderAPI(t, handler, accounts.memberCookie, "proxies", hiddenProxy.ID, "up", http.StatusNotFound)
	hiddenRelay := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: a.Server.ID, Name: "Hidden Target Relay", ListenPort: 9804,
		TargetType: "proxy", TargetProxyID: &hiddenProxy.ID, TargetClientID: &hiddenProxy.Clients[0].ID, Network: "tcp",
	}, accounts.adminCookie)
	var createdRelay struct {
		Relay relayResponse `json:"relay"`
	}
	if hiddenRelay.Code != http.StatusCreated || json.Unmarshal(hiddenRelay.Body.Bytes(), &createdRelay) != nil {
		t.Fatalf("create hidden-target relay = %d, %s", hiddenRelay.Code, hiddenRelay.Body.String())
	}
	moveOrderAPI(t, handler, accounts.memberCookie, "relays", createdRelay.Relay.ID, "up", http.StatusNotFound)
	if got := orderListIDs(t, handler, accounts.memberCookie, "/api/relays", "relays"); len(got) != 3 {
		t.Fatalf("hidden-target Relay leaked into list: %v", got)
	}
	archived := performRequest(t, handler, http.MethodDelete, "/api/servers/"+strconv.FormatInt(private.Server.ID, 10)+"/force", nil, accounts.adminCookie)
	if archived.Code != http.StatusNoContent {
		t.Fatalf("archive private Server = %d, %s", archived.Code, archived.Body.String())
	}
	moveOrderAPI(t, handler, accounts.adminCookie, "servers", private.Server.ID, "up", http.StatusNoContent)
	expectOrderAPI(t, handler, accounts.adminCookie, "/api/servers?archived=true", "servers", private.Server.ID)
}
