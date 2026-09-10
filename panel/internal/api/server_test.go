package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestHealth(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	response := httptest.NewRecorder()
	NewHandler(db, t.TempDir()).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"database":"ok"`) {
		t.Fatalf("body = %q, want healthy database", response.Body.String())
	}
}

func TestSPAFallbackAndUnknownAPI(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	webRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("panel shell"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	handler := NewHandler(db, webRoot)

	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	pageBody, _ := io.ReadAll(pageResponse.Result().Body)
	if pageResponse.Code != http.StatusOK || string(pageBody) != "panel shell" {
		t.Fatalf("SPA response = (%d, %q)", pageResponse.Code, pageBody)
	}

	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api/missing", nil))
	if apiResponse.Code != http.StatusNotFound {
		t.Fatalf("unknown API status = %d, want %d", apiResponse.Code, http.StatusNotFound)
	}
}

func TestAuthenticationAndInvitationLifecycle(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())

	stateResponse := performRequest(t, handler, http.MethodGet, "/api/auth/state", nil, nil)
	if stateResponse.Code != http.StatusOK || !strings.Contains(stateResponse.Body.String(), `"requires_initialization":true`) {
		t.Fatalf("initial state = (%d, %q)", stateResponse.Code, stateResponse.Body.String())
	}

	unauthorized := performRequest(t, handler, http.MethodGet, "/api/admin/invitations", nil, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized invitations status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	initializeRequest := jsonRequest(t, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin",
		"password": "strong-password",
	})
	initializeRequest.Header.Set("X-Forwarded-Proto", "https")
	initializeResponse := httptest.NewRecorder()
	handler.ServeHTTP(initializeResponse, initializeRequest)
	if initializeResponse.Code != http.StatusCreated {
		t.Fatalf("initialize status = %d, body = %q", initializeResponse.Code, initializeResponse.Body.String())
	}
	if !strings.Contains(initializeResponse.Body.String(), `"role":"admin"`) {
		t.Fatalf("initialize body = %q, want admin role", initializeResponse.Body.String())
	}
	cookies := initializeResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("initialize cookies = %d, want 1", len(cookies))
	}
	sessionCookie := cookies[0]
	if sessionCookie.Name != sessionCookieName || !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie flags = %+v, want HttpOnly Secure SameSite=Lax", sessionCookie)
	}

	secondInitialize := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "second",
		"password": "strong-password",
	}, nil)
	if secondInitialize.Code != http.StatusConflict {
		t.Fatalf("second initialize status = %d, want %d", secondInitialize.Code, http.StatusConflict)
	}

	createResponse := performRequest(t, handler, http.MethodPost, "/api/admin/invitations", nil, sessionCookie)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create invitation status = %d, body = %q", createResponse.Code, createResponse.Body.String())
	}
	var invitation invitationResponse
	if err := json.Unmarshal(createResponse.Body.Bytes(), &invitation); err != nil {
		t.Fatalf("decode invitation: %v", err)
	}
	if invitation.Token == "" {
		t.Fatal("created invitation token is empty")
	}

	listResponse := performRequest(t, handler, http.MethodGet, "/api/admin/invitations", nil, sessionCookie)
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), invitation.Token) {
		t.Fatalf("invitation list = (%d, %q), plaintext token must not be returned", listResponse.Code, listResponse.Body.String())
	}

	registerResponse := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token":    invitation.Token,
		"username": "invited",
		"password": "another-password",
	}, nil)
	if registerResponse.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %q", registerResponse.Code, registerResponse.Body.String())
	}
	if !strings.Contains(registerResponse.Body.String(), `"role":"vip"`) {
		t.Fatalf("register body = %q, want vip role", registerResponse.Body.String())
	}
	invitedCookies := registerResponse.Result().Cookies()
	if len(invitedCookies) != 1 {
		t.Fatalf("register cookies = %d, want 1", len(invitedCookies))
	}

	reuseResponse := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token":    invitation.Token,
		"username": "third",
		"password": "another-password",
	}, nil)
	if reuseResponse.Code != http.StatusBadRequest {
		t.Fatalf("reused invitation status = %d, want %d", reuseResponse.Code, http.StatusBadRequest)
	}

	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/invitations"},
		{http.MethodPost, "/api/admin/invitations"},
		{http.MethodDelete, "/api/admin/invitations/" + strconv.FormatInt(invitation.ID, 10)},
	} {
		response := performRequest(t, handler, request.method, request.path, nil, invitedCookies[0])
		if response.Code != http.StatusForbidden {
			t.Fatalf("vip %s %s status = %d, want %d", request.method, request.path, response.Code, http.StatusForbidden)
		}
	}

	sharedServer := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{
		"name": "Shared Server",
	}, sessionCookie)
	if sharedServer.Code != http.StatusCreated {
		t.Fatalf("admin create shared server status = %d, body = %q", sharedServer.Code, sharedServer.Body.String())
	}
	vipServers := performRequest(t, handler, http.MethodGet, "/api/servers", nil, invitedCookies[0])
	if vipServers.Code != http.StatusOK || !strings.Contains(vipServers.Body.String(), "Shared Server") {
		t.Fatalf("vip shared servers = (%d, %q), want shared server", vipServers.Code, vipServers.Body.String())
	}

	logoutResponse := performRequest(t, handler, http.MethodPost, "/api/auth/logout", nil, invitedCookies[0])
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d", logoutResponse.Code, http.StatusNoContent)
	}
	afterLogout := performRequest(t, handler, http.MethodGet, "/api/admin/invitations", nil, invitedCookies[0])
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("after logout status = %d, want %d", afterLogout.Code, http.StatusUnauthorized)
	}
}

func TestRevokeInvitation(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())

	initializeResponse := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin",
		"password": "strong-password",
	}, nil)
	if initializeResponse.Code != http.StatusCreated {
		t.Fatalf("initialize status = %d, body = %q", initializeResponse.Code, initializeResponse.Body.String())
	}
	sessionCookie := initializeResponse.Result().Cookies()[0]
	createResponse := performRequest(t, handler, http.MethodPost, "/api/admin/invitations", nil, sessionCookie)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create invitation status = %d, body = %q", createResponse.Code, createResponse.Body.String())
	}
	var invitation invitationResponse
	if err := json.Unmarshal(createResponse.Body.Bytes(), &invitation); err != nil {
		t.Fatalf("decode invitation: %v", err)
	}

	revokeResponse := performRequest(
		t,
		handler,
		http.MethodDelete,
		"/api/admin/invitations/"+strconv.FormatInt(invitation.ID, 10),
		nil,
		sessionCookie,
	)
	if revokeResponse.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, body = %q", revokeResponse.Code, revokeResponse.Body.String())
	}
	registration := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token":    invitation.Token,
		"username": "revoked",
		"password": "another-password",
	}, nil)
	if registration.Code != http.StatusBadRequest {
		t.Fatalf("revoked registration status = %d, want %d", registration.Code, http.StatusBadRequest)
	}
}

func TestServerAPILifecycle(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())

	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/servers"},
		{http.MethodPost, "/api/servers"},
		{http.MethodGet, "/api/servers/1"},
		{http.MethodDelete, "/api/servers/1"},
		{http.MethodPost, "/api/servers/1/enrollment"},
		{http.MethodDelete, "/api/servers/1/permanent"},
	} {
		response := performRequest(t, handler, request.method, request.path, map[string]string{"name": "test"}, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s status = %d, want %d", request.method, request.path, response.Code, http.StatusUnauthorized)
		}
	}

	initializeResponse := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin",
		"password": "strong-password",
	}, nil)
	if initializeResponse.Code != http.StatusCreated {
		t.Fatalf("initialize status = %d, body = %q", initializeResponse.Code, initializeResponse.Body.String())
	}
	sessionCookie := initializeResponse.Result().Cookies()[0]

	invalidResponse := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{"name": "  "}, sessionCookie)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid server status = %d, want %d", invalidResponse.Code, http.StatusBadRequest)
	}

	createRequest := jsonRequest(t, http.MethodPost, "/api/servers", map[string]string{"name": "JP Native 01"})
	createRequest.Host = "panel.example.com"
	createRequest.Header.Set("X-Forwarded-Proto", "https")
	createRequest.AddCookie(sessionCookie)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create server status = %d, body = %q", createResponse.Code, createResponse.Body.String())
	}
	var created createdServerResponse
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created server: %v", err)
	}
	if created.Server.Name != "JP Native 01" || created.Server.Status != "pending" {
		t.Fatalf("created server = %+v, want named pending server", created.Server)
	}
	if created.EnrollmentToken == "" {
		t.Fatal("created enrollment token is empty")
	}
	if !strings.Contains(created.AgentInstallationCommand, "https://panel.example.com") ||
		!strings.Contains(created.AgentInstallationCommand, created.EnrollmentToken) {
		t.Fatalf("agent command = %q, want panel URL and enrollment token", created.AgentInstallationCommand)
	}

	var storedHash string
	if err := db.QueryRow(
		`SELECT token_hash FROM agent_enrollments WHERE server_id = ?`, created.Server.ID,
	).Scan(&storedHash); err != nil {
		t.Fatalf("read enrollment hash: %v", err)
	}
	if storedHash == created.EnrollmentToken {
		t.Fatal("database contains the plaintext enrollment token")
	}

	listResponse := performRequest(t, handler, http.MethodGet, "/api/servers", nil, sessionCookie)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), "JP Native 01") {
		t.Fatalf("server list = (%d, %q), want created server", listResponse.Code, listResponse.Body.String())
	}
	if strings.Contains(listResponse.Body.String(), created.EnrollmentToken) {
		t.Fatal("server list returned the plaintext enrollment token")
	}

	serverPath := "/api/servers/" + strconv.FormatInt(created.Server.ID, 10)
	getResponse := performRequest(t, handler, http.MethodGet, serverPath, nil, sessionCookie)
	if getResponse.Code != http.StatusOK || strings.Contains(getResponse.Body.String(), created.EnrollmentToken) {
		t.Fatalf("get server = (%d, %q), token must not be returned", getResponse.Code, getResponse.Body.String())
	}

	deleteResponse := performRequest(t, handler, http.MethodDelete, serverPath, nil, sessionCookie)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete server status = %d, body = %q", deleteResponse.Code, deleteResponse.Body.String())
	}
	getDeletedResponse := performRequest(t, handler, http.MethodGet, serverPath, nil, sessionCookie)
	if getDeletedResponse.Code != http.StatusNotFound {
		t.Fatalf("get archived server status = %d, want %d", getDeletedResponse.Code, http.StatusNotFound)
	}
	archivedResponse := performRequest(t, handler, http.MethodGet, "/api/servers?archived=true", nil, sessionCookie)
	if archivedResponse.Code != http.StatusOK ||
		!strings.Contains(archivedResponse.Body.String(), "JP Native 01") ||
		!strings.Contains(archivedResponse.Body.String(), `"archived_at"`) {
		t.Fatalf("archived server list = (%d, %q), want archived server", archivedResponse.Code, archivedResponse.Body.String())
	}
	var enrollmentCount, serverCount int
	var archivedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT COUNT(*), archived_at FROM servers WHERE id = ?`, created.Server.ID,
	).Scan(&serverCount, &archivedAt); err != nil {
		t.Fatalf("read archived server: %v", err)
	}
	if serverCount != 1 || !archivedAt.Valid {
		t.Fatalf("archived server state = (count %d, archived %v), want preserved", serverCount, archivedAt.Valid)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM agent_enrollments WHERE server_id = ?`, created.Server.ID,
	).Scan(&enrollmentCount); err != nil {
		t.Fatalf("count enrollments: %v", err)
	}
	if enrollmentCount != 0 {
		t.Fatalf("unused enrollment count after server archive = %d, want 0", enrollmentCount)
	}
}

func TestAgentRebindAndPermanentDeleteRequireAdmin(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())

	initializeResponse := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin",
		"password": "strong-password",
	}, nil)
	if initializeResponse.Code != http.StatusCreated {
		t.Fatalf("initialize status = %d, body = %q", initializeResponse.Code, initializeResponse.Body.String())
	}
	adminCookie := initializeResponse.Result().Cookies()[0]
	createInvitationResponse := performRequest(t, handler, http.MethodPost, "/api/admin/invitations", nil, adminCookie)
	if createInvitationResponse.Code != http.StatusCreated {
		t.Fatalf("create invitation status = %d, body = %q", createInvitationResponse.Code, createInvitationResponse.Body.String())
	}
	var invitation invitationResponse
	if err := json.Unmarshal(createInvitationResponse.Body.Bytes(), &invitation); err != nil {
		t.Fatalf("decode invitation: %v", err)
	}
	vipResponse := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": "vip-user", "password": "another-password",
	}, nil)
	if vipResponse.Code != http.StatusCreated {
		t.Fatalf("register VIP status = %d, body = %q", vipResponse.Code, vipResponse.Body.String())
	}
	vipCookie := vipResponse.Result().Cookies()[0]

	createdResponse := performRequest(t, handler, http.MethodPost, "/api/servers", map[string]string{
		"name": "Rebind Server",
	}, adminCookie)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create server status = %d, body = %q", createdResponse.Code, createdResponse.Body.String())
	}
	var created createdServerResponse
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created server: %v", err)
	}
	serverPath := "/api/servers/" + strconv.FormatInt(created.Server.ID, 10)
	archiveResponse := performRequest(t, handler, http.MethodDelete, serverPath, nil, vipCookie)
	if archiveResponse.Code != http.StatusNoContent {
		t.Fatalf("VIP archive status = %d, body = %q", archiveResponse.Code, archiveResponse.Body.String())
	}

	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, serverPath + "/enrollment"},
		{http.MethodDelete, serverPath + "/permanent"},
	} {
		response := performRequest(t, handler, request.method, request.path, nil, vipCookie)
		if response.Code != http.StatusForbidden {
			t.Fatalf("VIP %s %s status = %d, want %d", request.method, request.path, response.Code, http.StatusForbidden)
		}
	}

	rebindResponse := performRequest(t, handler, http.MethodPost, serverPath+"/enrollment", nil, adminCookie)
	if rebindResponse.Code != http.StatusCreated {
		t.Fatalf("admin rebind status = %d, body = %q", rebindResponse.Code, rebindResponse.Body.String())
	}
	var rebind createdServerResponse
	if err := json.Unmarshal(rebindResponse.Body.Bytes(), &rebind); err != nil {
		t.Fatalf("decode rebind response: %v", err)
	}
	if rebind.Server.ID != created.Server.ID || rebind.EnrollmentToken == "" ||
		!strings.Contains(rebind.AgentInstallationCommand, "--force") {
		t.Fatalf("rebind response = %+v, want same server, token and --force command", rebind)
	}

	permanentResponse := performRequest(t, handler, http.MethodDelete, serverPath+"/permanent", nil, adminCookie)
	if permanentResponse.Code != http.StatusNoContent {
		t.Fatalf("admin permanent delete status = %d, body = %q", permanentResponse.Code, permanentResponse.Body.String())
	}
	var serverCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers WHERE id = ?`, created.Server.ID).Scan(&serverCount); err != nil {
		t.Fatalf("count permanently deleted server: %v", err)
	}
	if serverCount != 0 {
		t.Fatalf("server count after permanent delete = %d, want 0", serverCount)
	}
}

func performRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body any,
	cookie *http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()
	request := jsonRequest(t, method, path, body)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	return httptest.NewRequest(method, path, reader)
}
