package api

import (
	"bytes"
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
