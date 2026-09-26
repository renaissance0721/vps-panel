package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestSessionMutationRequiresSameOrigin(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandler(db, t.TempDir())
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if initialized.Code != http.StatusCreated {
		t.Fatalf("initialize = %d, %s", initialized.Code, initialized.Body.String())
	}
	cookie := initialized.Result().Cookies()[0]

	mutation := func(origin, referer, name string) *httptest.ResponseRecorder {
		request := jsonRequest(t, http.MethodPost, "/api/servers", map[string]string{"name": name})
		request.AddCookie(cookie)
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if referer != "" {
			request.Header.Set("Referer", referer)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := mutation("http://example.com", "", "Origin Allowed"); response.Code != http.StatusCreated {
		t.Fatalf("same-origin mutation = %d, %s", response.Code, response.Body.String())
	}
	for _, test := range []struct {
		name    string
		origin  string
		referer string
	}{
		{"cross origin", "https://evil.example", ""},
		{"wrong port", "http://example.com:80", ""},
		{"null origin", "null", ""},
		{"cross referer", "", "https://evil.example/page"},
		{"missing headers", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := mutation(test.origin, test.referer, test.name)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "请求来源无效") {
				t.Fatalf("blocked mutation = %d, %s", response.Code, response.Body.String())
			}
		})
	}
	if response := mutation("", "http://example.com/servers", "Referer Allowed"); response.Code != http.StatusCreated {
		t.Fatalf("same-origin Referer mutation = %d, %s", response.Code, response.Body.String())
	}
	if response := performRequest(t, handler, http.MethodGet, "/api/servers", nil, cookie); response.Code != http.StatusOK {
		t.Fatalf("authenticated GET without origin = %d, %s", response.Code, response.Body.String())
	}

	crossLogout := jsonRequest(t, http.MethodPost, "/api/auth/logout", nil)
	crossLogout.AddCookie(cookie)
	crossLogout.Header.Set("Origin", "https://evil.example")
	crossResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossResponse, crossLogout)
	if crossResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-origin logout = %d", crossResponse.Code)
	}
	if response := performRequest(t, handler, http.MethodGet, "/api/users", nil, cookie); response.Code != http.StatusOK {
		t.Fatalf("cross-origin logout removed session: %d", response.Code)
	}
	if response := performRequest(t, handler, http.MethodPost, "/api/auth/logout", nil, cookie); response.Code != http.StatusNoContent {
		t.Fatalf("same-origin logout = %d, %s", response.Code, response.Body.String())
	}
	if response := performRequest(t, handler, http.MethodGet, "/api/users", nil, cookie); response.Code != http.StatusUnauthorized {
		t.Fatalf("session remained after logout = %d", response.Code)
	}
}

func TestEnrollmentCommandsUseConfiguredPanelDomain(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandlerWithBackup(db, t.TempDir(), "dev", BackupConfig{Domain: "panel.example.com"})
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialized.Result().Cookies()[0]

	createRequest := jsonRequest(t, http.MethodPost, "/api/servers", map[string]string{"name": "Canonical URL"})
	createRequest.Host = "evil.example.com"
	createRequest.Header.Set("Origin", "https://panel.example.com")
	createRequest.AddCookie(cookie)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	var created createdServerResponse
	if createResponse.Code != http.StatusCreated || json.Unmarshal(createResponse.Body.Bytes(), &created) != nil {
		t.Fatalf("create with canonical domain = %d, %s", createResponse.Code, createResponse.Body.String())
	}
	assertCanonicalInstallationCommand(t, created.AgentInstallationCommand)
	crossOriginRequest := jsonRequest(t, http.MethodPost, "/api/servers", map[string]string{"name": "Blocked Origin"})
	crossOriginRequest.Host = "evil.example.com"
	crossOriginRequest.Header.Set("Origin", "https://evil.example.com")
	crossOriginRequest.AddCookie(cookie)
	crossOriginResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossOriginResponse, crossOriginRequest)
	if crossOriginResponse.Code != http.StatusForbidden {
		t.Fatalf("configured-domain cross origin = %d, %s", crossOriginResponse.Code, crossOriginResponse.Body.String())
	}

	enrollmentRequest := jsonRequest(t, http.MethodPost, "/api/servers/"+strconv.FormatInt(created.Server.ID, 10)+"/enrollment", nil)
	enrollmentRequest.Host = "evil.example.com"
	enrollmentRequest.Header.Set("Origin", "https://panel.example.com")
	enrollmentRequest.AddCookie(cookie)
	enrollmentResponse := httptest.NewRecorder()
	handler.ServeHTTP(enrollmentResponse, enrollmentRequest)
	var regenerated createdServerResponse
	if enrollmentResponse.Code != http.StatusCreated || json.Unmarshal(enrollmentResponse.Body.Bytes(), &regenerated) != nil {
		t.Fatalf("regenerate with canonical domain = %d, %s", enrollmentResponse.Code, enrollmentResponse.Body.String())
	}
	assertCanonicalInstallationCommand(t, regenerated.AgentInstallationCommand)
}

func TestPanelBaseURLRejectsUnsafeConfiguredDomainAndRequestHost(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/servers", nil)
	for _, domain := range []string{
		"https://panel.example.com",
		"panel.example.com/path",
		"panel.example.com?query",
		"panel.example.com#fragment",
		"panel.example.com injected",
		"panel.example.com\ninvalid",
	} {
		service := &server{backup: BackupConfig{Domain: domain}}
		if baseURL, ok := service.panelBaseURL(request); ok || baseURL != "" {
			t.Fatalf("unsafe configured domain %q produced %q", domain, baseURL)
		}
	}
	service := &server{backup: BackupConfig{Domain: ":80"}}
	request.Host = "example.com;injected"
	if baseURL, ok := service.panelBaseURL(request); ok || baseURL != "" {
		t.Fatalf("unsafe request Host produced %q", baseURL)
	}
}

func TestEnrollmentCommandDirectIPFallback(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := NewHandlerWithBackup(db, t.TempDir(), "dev", BackupConfig{Domain: ":80"})
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	cookie := initialized.Result().Cookies()[0]
	request := jsonRequest(t, http.MethodPost, "/api/servers", map[string]string{"name": "Direct IP"})
	request.Host = "203.0.113.10:8080"
	request.Header.Set("Origin", "http://203.0.113.10:8080")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var created createdServerResponse
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("direct IP create = %d, %s", response.Code, response.Body.String())
	}
	if !strings.Contains(created.AgentInstallationCommand, "http://203.0.113.10:8080/install-agent.sh") ||
		!strings.Contains(created.AgentInstallationCommand, "--server http://203.0.113.10:8080") {
		t.Fatalf("direct IP command = %q", created.AgentInstallationCommand)
	}
}

func assertCanonicalInstallationCommand(t *testing.T, command string) {
	t.Helper()
	if !strings.Contains(command, "https://panel.example.com/install-agent.sh") ||
		!strings.Contains(command, "--server https://panel.example.com") ||
		strings.Contains(command, "evil.example.com") {
		t.Fatalf("canonical installation command = %q", command)
	}
}
