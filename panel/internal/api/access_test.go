package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

type accessTestAccounts struct {
	adminID      int64
	memberID     int64
	adminCookie  *http.Cookie
	memberCookie *http.Cookie
}

func setupAccessTest(t *testing.T) (*sql.DB, http.Handler, accessTestAccounts) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initialized.Code != http.StatusCreated {
		db.Close()
		t.Fatalf("initialize = %d, %s", initialized.Code, initialized.Body.String())
	}
	adminCookie := initialized.Result().Cookies()[0]
	invited := performRequest(t, handler, http.MethodPost, "/api/admin/invitations", nil, adminCookie)
	var invitation invitationResponse
	if invited.Code != http.StatusCreated || json.Unmarshal(invited.Body.Bytes(), &invitation) != nil {
		db.Close()
		t.Fatalf("create invitation = %d, %s", invited.Code, invited.Body.String())
	}
	registered := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "member", "password": "another-password",
	}, nil)
	if registered.Code != http.StatusCreated {
		db.Close()
		t.Fatalf("register member = %d, %s", registered.Code, registered.Body.String())
	}
	memberCookie := registered.Result().Cookies()[0]
	usersResponse := performRequest(t, handler, http.MethodGet, "/api/users", nil, memberCookie)
	var listed struct {
		Users []accessUserResponse `json:"users"`
	}
	if usersResponse.Code != http.StatusOK || json.Unmarshal(usersResponse.Body.Bytes(), &listed) != nil {
		db.Close()
		t.Fatalf("list users = %d, %s", usersResponse.Code, usersResponse.Body.String())
	}
	accounts := accessTestAccounts{adminCookie: adminCookie, memberCookie: memberCookie}
	for _, user := range listed.Users {
		switch user.Username {
		case "admin":
			accounts.adminID = user.ID
		case "member":
			accounts.memberID = user.ID
		}
	}
	if accounts.adminID == 0 || accounts.memberID == 0 || strings.Contains(usersResponse.Body.String(), "password") ||
		strings.Contains(usersResponse.Body.String(), "created_at") {
		db.Close()
		t.Fatalf("unexpected users response: %s", usersResponse.Body.String())
	}
	return db, handler, accounts
}

func createAccessTestServer(
	t *testing.T,
	handler http.Handler,
	cookie *http.Cookie,
	name, visibility string,
	userIDs []int64,
) createdServerResponse {
	t.Helper()
	response := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]any{
		"name": name, "visibility": visibility, "user_ids": userIDs,
	}, cookie)
	if response.Code != http.StatusCreated {
		t.Fatalf("create server %q = %d, %s", name, response.Code, response.Body.String())
	}
	var created createdServerResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created
}

func TestServerAccessScopesListsMutationsAndAdminOperations(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	publicServer := createAccessTestServer(t, handler, accounts.adminCookie, "Public", "public", nil)
	adminServer := createAccessTestServer(t, handler, accounts.adminCookie, "Admin Private", "private", nil)
	memberServer := createAccessTestServer(t, handler, accounts.memberCookie, "Member Private", "private", nil)

	if adminServer.Server.Visibility != "private" || len(adminServer.Server.AccessUserIDs) != 1 ||
		adminServer.Server.AccessUserIDs[0] != accounts.adminID {
		t.Fatalf("private creator access = %+v", adminServer.Server)
	}
	for _, test := range []struct {
		name    string
		cookie  *http.Cookie
		want    []string
		notWant string
	}{
		{"admin", accounts.adminCookie, []string{"Public", "Admin Private"}, "Member Private"},
		{"member", accounts.memberCookie, []string{"Public", "Member Private"}, "Admin Private"},
	} {
		response := performRequest(t, handler, http.MethodGet, "/api/servers", nil, test.cookie)
		for _, name := range test.want {
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), name) {
				t.Fatalf("%s server list = %d, %s", test.name, response.Code, response.Body.String())
			}
		}
		if strings.Contains(response.Body.String(), test.notWant) {
			t.Fatalf("%s server list leaked %q: %s", test.name, test.notWant, response.Body.String())
		}
	}
	adminPrivatePath := "/api/servers/" + strconv.FormatInt(adminServer.Server.ID, 10)
	if response := performRequest(t, handler, http.MethodDelete, adminPrivatePath, nil, accounts.adminCookie); response.Code != http.StatusNoContent {
		t.Fatalf("archive private server = %d, %s", response.Code, response.Body.String())
	}
	adminArchived := performRequest(t, handler, http.MethodGet, "/api/servers?archived=true", nil, accounts.adminCookie)
	memberArchived := performRequest(t, handler, http.MethodGet, "/api/servers?archived=true", nil, accounts.memberCookie)
	if !strings.Contains(adminArchived.Body.String(), "Admin Private") || strings.Contains(memberArchived.Body.String(), "Admin Private") {
		t.Fatalf("archived server access leak: admin=%s member=%s", adminArchived.Body.String(), memberArchived.Body.String())
	}

	memberPath := "/api/servers/" + strconv.FormatInt(memberServer.Server.ID, 10)
	for _, request := range []struct{ method, suffix string }{
		{http.MethodGet, ""},
		{http.MethodPatch, ""},
		{http.MethodDelete, ""},
		{http.MethodPatch, "/traffic-adjustment"},
		{http.MethodDelete, "/traffic-adjustment"},
		{http.MethodPost, "/enrollment"},
		{http.MethodPost, "/agent-upgrade"},
		{http.MethodDelete, "/permanent"},
		{http.MethodPatch, "/access"},
	} {
		response := performRequest(t, handler, request.method, memberPath+request.suffix, nil, accounts.adminCookie)
		if response.Code != http.StatusNotFound {
			t.Fatalf("admin inaccessible %s %s = %d, %s", request.method, request.suffix, response.Code, response.Body.String())
		}
	}

	for _, request := range []struct{ method, suffix string }{
		{http.MethodPost, "/enrollment"},
		{http.MethodPost, "/agent-upgrade"},
		{http.MethodDelete, "/permanent"},
	} {
		response := performRequest(t, handler, request.method, memberPath+request.suffix, nil, accounts.memberCookie)
		if response.Code != http.StatusForbidden {
			t.Fatalf("member admin-only %s = %d, want role-based 403", request.suffix, response.Code)
		}
	}
	invalidAccess := performRequest(t, handler, http.MethodPatch, memberPath+"/access", map[string]any{
		"visibility": "private", "user_ids": []int64{999999},
	}, accounts.memberCookie)
	if invalidAccess.Code != http.StatusBadRequest {
		t.Fatalf("invalid access user = %d, %s", invalidAccess.Code, invalidAccess.Body.String())
	}
	grantAdmin := performRequest(t, handler, http.MethodPatch, memberPath+"/access", map[string]any{
		"visibility": "private", "user_ids": []int64{accounts.adminID},
	}, accounts.memberCookie)
	if grantAdmin.Code != http.StatusOK {
		t.Fatalf("grant admin access = %d, %s", grantAdmin.Code, grantAdmin.Body.String())
	}
	adminEnrollment := performRequest(t, handler, http.MethodPost, memberPath+"/enrollment", nil, accounts.adminCookie)
	if adminEnrollment.Code != http.StatusCreated {
		t.Fatalf("authorized admin enrollment = %d, %s", adminEnrollment.Code, adminEnrollment.Body.String())
	}

	publicPath := "/api/servers/" + strconv.FormatInt(publicServer.Server.ID, 10)
	publicAccess := performRequest(t, handler, http.MethodPatch, publicPath+"/access", map[string]any{
		"visibility": "public", "user_ids": []int64{accounts.memberID},
	}, accounts.adminCookie)
	if publicAccess.Code != http.StatusOK {
		t.Fatalf("public access update = %d, %s", publicAccess.Code, publicAccess.Body.String())
	}
	var accessRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_access WHERE server_id = ?`, publicServer.Server.ID).Scan(&accessRows); err != nil || accessRows != 0 {
		t.Fatalf("public access rows = %d, error %v", accessRows, err)
	}
}

func TestDerivedResourcesRequireServerAccessAndRelayBothSides(t *testing.T) {
	db, handler, accounts := setupAccessTest(t)
	defer db.Close()
	publicServer := createAccessTestServer(t, handler, accounts.adminCookie, "Public Source", "public", nil)
	adminServer := createAccessTestServer(t, handler, accounts.adminCookie, "Admin Source", "private", nil)
	memberServer := createAccessTestServer(t, handler, accounts.memberCookie, "Member Source", "private", nil)

	createProxy := func(cookie *http.Cookie, serverID int64, name string, port int) proxyResponse {
		response := performRequest(t, handler, http.MethodPost, "/api/proxies", createProxyRequest{
			ServerID: serverID, Name: name, ListenPort: port, Security: "reality",
			EntryHostMode: "manual", EntryHost: "node.example.com",
			ServerName: "www.example.com", RealityTarget: "www.example.com:443", FirstClientName: "Client",
		}, cookie)
		if response.Code != http.StatusCreated {
			t.Fatalf("create proxy %q = %d, %s", name, response.Code, response.Body.String())
		}
		var created struct {
			Proxy proxyResponse `json:"proxy"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		return created.Proxy
	}
	publicProxy := createProxy(accounts.adminCookie, publicServer.Server.ID, "Public Proxy", 8443)
	adminProxy := createProxy(accounts.adminCookie, adminServer.Server.ID, "Admin Proxy", 8444)
	memberProxy := createProxy(accounts.memberCookie, memberServer.Server.ID, "Member Proxy", 8445)
	memberClientID := memberProxy.Clients[0].ID
	registration := performRequest(t, handler, http.MethodPost, "/api/agent/register", agentRegistrationRequest{
		EnrollmentToken: memberServer.EnrollmentToken, AgentVersion: "test", ExistingConfig: false,
	}, nil)
	var memberAgent agentRegistrationResponse
	if registration.Code != http.StatusCreated || json.Unmarshal(registration.Body.Bytes(), &memberAgent) != nil {
		t.Fatalf("register private Server Agent = %d, %s", registration.Code, registration.Body.String())
	}

	for _, request := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/proxies/" + strconv.FormatInt(memberProxy.ID, 10), nil},
		{http.MethodPatch, "/api/proxies/" + strconv.FormatInt(memberProxy.ID, 10), map[string]string{"name": "Hidden"}},
		{http.MethodDelete, "/api/proxies/" + strconv.FormatInt(memberProxy.ID, 10), nil},
		{http.MethodGet, "/api/proxies/" + strconv.FormatInt(memberProxy.ID, 10) + "/clients", nil},
		{http.MethodPost, "/api/proxies/" + strconv.FormatInt(memberProxy.ID, 10) + "/clients", nil},
		{http.MethodGet, "/api/clients/" + strconv.FormatInt(memberClientID, 10), nil},
		{http.MethodPatch, "/api/clients/" + strconv.FormatInt(memberClientID, 10), map[string]string{"name": "Hidden"}},
		{http.MethodDelete, "/api/clients/" + strconv.FormatInt(memberClientID, 10), nil},
		{http.MethodPost, "/api/clients/" + strconv.FormatInt(memberClientID, 10) + "/traffic/reset", nil},
		{http.MethodGet, "/api/clients/" + strconv.FormatInt(memberClientID, 10) + "/share", nil},
	} {
		response := performRequest(t, handler, request.method, request.path, request.body, accounts.adminCookie)
		if response.Code != http.StatusNotFound {
			t.Fatalf("inaccessible derived %s %s = %d, %s", request.method, request.path, response.Code, response.Body.String())
		}
	}
	adminList := performRequest(t, handler, http.MethodGet, "/api/proxies", nil, accounts.adminCookie)
	memberList := performRequest(t, handler, http.MethodGet, "/api/proxies", nil, accounts.memberCookie)
	if strings.Contains(adminList.Body.String(), "Member Proxy") || strings.Contains(memberList.Body.String(), "Admin Proxy") {
		t.Fatalf("proxy list access leak: admin=%s member=%s", adminList.Body.String(), memberList.Body.String())
	}

	createRelay := func(cookie *http.Cookie, input createRelayRequest) relayResponse {
		response := performRequest(t, handler, http.MethodPost, "/api/relays", input, cookie)
		if response.Code != http.StatusCreated {
			t.Fatalf("create relay %q = %d, %s", input.Name, response.Code, response.Body.String())
		}
		var created struct {
			Relay relayResponse `json:"relay"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		return created.Relay
	}
	memberRelay := createRelay(accounts.memberCookie, createRelayRequest{
		ServerID: memberServer.Server.ID, Name: "Member Relay", ListenPort: 9502,
		TargetType: "proxy", TargetProxyID: &publicProxy.ID, TargetClientID: &publicProxy.Clients[0].ID, Network: "tcp",
	})
	adminRelay := createRelay(accounts.adminCookie, createRelayRequest{
		ServerID: publicServer.Server.ID, Name: "Admin Target Relay", ListenPort: 9503,
		TargetType: "proxy", TargetProxyID: &adminProxy.ID, TargetClientID: &adminProxy.Clients[0].ID, Network: "tcp",
	})
	bothPrivateRelay := createRelay(accounts.adminCookie, createRelayRequest{
		ServerID: adminServer.Server.ID, Name: "Admin Private Relay", ListenPort: 9504,
		TargetType: "proxy", TargetProxyID: &adminProxy.ID, TargetClientID: &adminProxy.Clients[0].ID, Network: "tcp",
	})
	agentConfig := performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, memberAgent.AgentToken)
	if agentConfig.Code != http.StatusOK || !strings.Contains(agentConfig.Body.String(), `"port":8445`) ||
		!strings.Contains(agentConfig.Body.String(), `"listen_port":9502`) {
		t.Fatalf("private Server Agent desired state did not include its Proxy and Relay (status %d)", agentConfig.Code)
	}
	for _, test := range []struct {
		cookie *http.Cookie
		path   string
	}{
		{accounts.adminCookie, "/api/relays/" + strconv.FormatInt(memberRelay.ID, 10)},
		{accounts.memberCookie, "/api/relays/" + strconv.FormatInt(adminRelay.ID, 10)},
		{accounts.memberCookie, "/api/relays/" + strconv.FormatInt(bothPrivateRelay.ID, 10)},
	} {
		for _, request := range []struct{ method, suffix string }{
			{http.MethodGet, ""},
			{http.MethodGet, "/clients"},
			{http.MethodPatch, ""},
			{http.MethodDelete, ""},
		} {
			response := performRequest(t, handler, request.method, test.path+request.suffix, nil, test.cookie)
			if response.Code != http.StatusNotFound {
				t.Fatalf("inaccessible relay %s %s = %d, %s", request.method, test.path+request.suffix, response.Code, response.Body.String())
			}
		}
	}
	adminRelays := performRequest(t, handler, http.MethodGet, "/api/relays", nil, accounts.adminCookie)
	memberRelays := performRequest(t, handler, http.MethodGet, "/api/relays", nil, accounts.memberCookie)
	if !strings.Contains(adminRelays.Body.String(), "Admin Target Relay") ||
		!strings.Contains(memberRelays.Body.String(), "Member Relay") ||
		strings.Contains(adminRelays.Body.String(), "Member Relay") ||
		strings.Contains(memberRelays.Body.String(), "Admin Target Relay") ||
		strings.Contains(memberRelays.Body.String(), "Admin Private Relay") {
		t.Fatalf("relay list access leak: admin=%s member=%s", adminRelays.Body.String(), memberRelays.Body.String())
	}

	deniedSource := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: memberServer.Server.ID, Name: "Denied Source", ListenPort: 9600,
		TargetType: "manual", TargetHost: "example.com", TargetPort: 443, Network: "tcp",
	}, accounts.adminCookie)
	deniedTarget := performRequest(t, handler, http.MethodPost, "/api/relays", createRelayRequest{
		ServerID: publicServer.Server.ID, Name: "Denied Target", ListenPort: 9601,
		TargetType: "proxy", TargetProxyID: &adminProxy.ID, Network: "tcp",
	}, accounts.memberCookie)
	if deniedSource.Code != http.StatusNotFound || deniedTarget.Code != http.StatusNotFound {
		t.Fatalf("relay create access = source %d, target %d", deniedSource.Code, deniedTarget.Code)
	}
}
