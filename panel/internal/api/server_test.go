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
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
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
		{http.MethodPatch, "/api/servers/1"},
		{http.MethodDelete, "/api/servers/1"},
		{http.MethodPatch, "/api/servers/1/traffic-adjustment"},
		{http.MethodDelete, "/api/servers/1/traffic-adjustment"},
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
	if created.Server.LastSeenAt != nil {
		t.Fatalf("new server last_seen_at = %v, want null", created.Server.LastSeenAt)
	}
	if created.Server.SystemInfo != nil {
		t.Fatalf("new server system_info = %+v, want null", created.Server.SystemInfo)
	}
	if created.Server.Metrics != nil {
		t.Fatalf("new server metrics = %+v, want null", created.Server.Metrics)
	}
	if created.Server.ExpiresAt != nil {
		t.Fatalf("new server expires_at = %v, want null", created.Server.ExpiresAt)
	}
	if created.Server.MonthlyTrafficLimitBytes != nil || created.Server.TrafficCountMode != "single" ||
		created.Server.TrafficResetDay != 1 || created.Server.TrafficResetTime != "00:00" ||
		created.Server.TrafficUsedBytes != 0 {
		t.Fatalf("new server traffic defaults = %+v", created.Server)
	}
	if created.EnrollmentToken == "" {
		t.Fatal("created enrollment token is empty")
	}
	if !strings.Contains(created.AgentInstallationCommand, "https://panel.example.com") ||
		!strings.Contains(created.AgentInstallationCommand, created.EnrollmentToken) ||
		!strings.Contains(created.AgentInstallationCommand, "| sh -s --") ||
		strings.Contains(created.AgentInstallationCommand, "| bash") ||
		strings.Contains(created.AgentInstallationCommand, "--force") ||
		strings.Contains(created.AgentInstallationCommand, "--version") {
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
	if !strings.Contains(listResponse.Body.String(), `"last_seen_at":null`) {
		t.Fatalf("server list = %q, want nullable last_seen_at", listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), `"system_info":null`) {
		t.Fatalf("server list = %q, want nullable system_info", listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), `"metrics":null`) {
		t.Fatalf("server list = %q, want nullable metrics", listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), `"expires_at":null`) {
		t.Fatalf("server list = %q, want nullable expires_at", listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), `"monthly_traffic_limit_bytes":null`) ||
		!strings.Contains(listResponse.Body.String(), `"traffic_count_mode":"single"`) ||
		!strings.Contains(listResponse.Body.String(), `"traffic_used_bytes":0`) {
		t.Fatalf("server list = %q, want default traffic fields", listResponse.Body.String())
	}
	if strings.Contains(listResponse.Body.String(), created.EnrollmentToken) {
		t.Fatal("server list returned the plaintext enrollment token")
	}

	serverPath := "/api/servers/" + strconv.FormatInt(created.Server.ID, 10)
	getResponse := performRequest(t, handler, http.MethodGet, serverPath, nil, sessionCookie)
	if getResponse.Code != http.StatusOK || strings.Contains(getResponse.Body.String(), created.EnrollmentToken) {
		t.Fatalf("get server = (%d, %q), token must not be returned", getResponse.Code, getResponse.Body.String())
	}
	trafficLimit := int64(500 << 30)
	updateTrafficResponse := performRequest(
		t, handler, http.MethodPatch, serverPath, map[string]any{
			"monthly_traffic_limit_bytes": trafficLimit,
			"traffic_count_mode":          "bidirectional",
			"traffic_reset_day":           31,
			"traffic_reset_time":          "08:30",
		}, sessionCookie,
	)
	if updateTrafficResponse.Code != http.StatusOK {
		t.Fatalf("update traffic status = %d, body = %q", updateTrafficResponse.Code, updateTrafficResponse.Body.String())
	}
	var trafficUpdated struct {
		Server serverResponse `json:"server"`
	}
	if err := json.Unmarshal(updateTrafficResponse.Body.Bytes(), &trafficUpdated); err != nil {
		t.Fatalf("decode updated traffic settings: %v", err)
	}
	if trafficUpdated.Server.MonthlyTrafficLimitBytes == nil ||
		*trafficUpdated.Server.MonthlyTrafficLimitBytes != trafficLimit ||
		trafficUpdated.Server.TrafficCountMode != "bidirectional" ||
		trafficUpdated.Server.TrafficResetDay != 31 || trafficUpdated.Server.TrafficResetTime != "08:30" {
		t.Fatalf("updated traffic settings = %+v", trafficUpdated.Server)
	}
	for _, invalidTraffic := range []map[string]any{
		{
			"monthly_traffic_limit_bytes": -1,
			"traffic_count_mode":          "single",
			"traffic_reset_day":           1,
			"traffic_reset_time":          "00:00",
		},
		{
			"monthly_traffic_limit_bytes": nil,
			"traffic_count_mode":          "invalid",
			"traffic_reset_day":           32,
			"traffic_reset_time":          "24:00",
		},
		{"traffic_count_mode": "single"},
	} {
		response := performRequest(t, handler, http.MethodPatch, serverPath, invalidTraffic, sessionCookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid traffic settings %v status = %d, body = %q", invalidTraffic, response.Code, response.Body.String())
		}
	}
	if _, err := db.Exec(
		`INSERT INTO server_metrics
		 (server_id, cpu_percent, memory_used_bytes, memory_total_bytes,
		  disk_used_bytes, disk_total_bytes, uptime_seconds, nic_rx_bytes, nic_tx_bytes,
		  cycle_rx_bytes, cycle_tx_bytes, cycle_started_at, updated_at)
		 VALUES (?, 1, 2, 3, 4, 5, 6, 1000, 2000, 100, 200, 10, 11)`,
		created.Server.ID,
	); err != nil {
		t.Fatalf("insert metrics for traffic adjustment: %v", err)
	}
	adjustmentPath := serverPath + "/traffic-adjustment"
	adjustmentResponse := performRequest(
		t, handler, http.MethodPatch, adjustmentPath, map[string]any{"target_used_bytes": 1000}, sessionCookie,
	)
	if adjustmentResponse.Code != http.StatusOK {
		t.Fatalf("update traffic adjustment status = %d, body = %q", adjustmentResponse.Code, adjustmentResponse.Body.String())
	}
	var adjusted struct {
		Server serverResponse `json:"server"`
	}
	if err := json.Unmarshal(adjustmentResponse.Body.Bytes(), &adjusted); err != nil {
		t.Fatalf("decode traffic adjustment: %v", err)
	}
	if adjusted.Server.TrafficUsedBytes != 1000 || adjusted.Server.Metrics == nil ||
		adjusted.Server.Metrics.TrafficAdjustmentBytes != 700 {
		t.Fatalf("adjusted traffic response = %+v", adjusted.Server)
	}
	var nicRX, nicTX, cycleRX, cycleTX, cycleStarted, adjustment int64
	if err := db.QueryRow(
		`SELECT nic_rx_bytes, nic_tx_bytes, cycle_rx_bytes, cycle_tx_bytes,
		 cycle_started_at, traffic_adjustment_bytes FROM server_metrics WHERE server_id = ?`,
		created.Server.ID,
	).Scan(&nicRX, &nicTX, &cycleRX, &cycleTX, &cycleStarted, &adjustment); err != nil {
		t.Fatalf("read adjusted traffic counters: %v", err)
	}
	if nicRX != 1000 || nicTX != 2000 || cycleRX != 100 || cycleTX != 200 || cycleStarted != 10 || adjustment != 700 {
		t.Fatalf("adjusted traffic storage = (%d, %d, %d, %d, %d, %d)",
			nicRX, nicTX, cycleRX, cycleTX, cycleStarted, adjustment)
	}
	for _, invalidAdjustment := range map[string]any{
		"missing":  map[string]any{},
		"null":     map[string]any{"target_used_bytes": nil},
		"negative": map[string]any{"target_used_bytes": -1},
	} {
		response := performRequest(t, handler, http.MethodPatch, adjustmentPath, invalidAdjustment, sessionCookie)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid traffic adjustment %v status = %d, body = %q", invalidAdjustment, response.Code, response.Body.String())
		}
	}
	clearAdjustmentResponse := performRequest(
		t, handler, http.MethodDelete, adjustmentPath, nil, sessionCookie,
	)
	if clearAdjustmentResponse.Code != http.StatusOK ||
		!strings.Contains(clearAdjustmentResponse.Body.String(), `"traffic_used_bytes":300`) ||
		!strings.Contains(clearAdjustmentResponse.Body.String(), `"traffic_adjustment_bytes":0`) {
		t.Fatalf("clear traffic adjustment = (%d, %q)", clearAdjustmentResponse.Code, clearAdjustmentResponse.Body.String())
	}
	updateExpirationResponse := performRequest(
		t, handler, http.MethodPatch, serverPath, map[string]any{"expires_at": "2026-12-31"}, sessionCookie,
	)
	if updateExpirationResponse.Code != http.StatusOK {
		t.Fatalf("update expiration status = %d, body = %q", updateExpirationResponse.Code, updateExpirationResponse.Body.String())
	}
	var expirationUpdated struct {
		Server serverResponse `json:"server"`
	}
	if err := json.Unmarshal(updateExpirationResponse.Body.Bytes(), &expirationUpdated); err != nil {
		t.Fatalf("decode updated expiration: %v", err)
	}
	expectedExpiration := time.Date(2026, 12, 31, 15, 59, 59, 0, time.UTC)
	if expirationUpdated.Server.ExpiresAt == nil || !expirationUpdated.Server.ExpiresAt.Equal(expectedExpiration) {
		t.Fatalf("Asia/Shanghai expiration = %v, want %v", expirationUpdated.Server.ExpiresAt, expectedExpiration)
	}
	getWithExpiration := performRequest(t, handler, http.MethodGet, serverPath, nil, sessionCookie)
	if getWithExpiration.Code != http.StatusOK ||
		!strings.Contains(getWithExpiration.Body.String(), `"expires_at":"2026-12-31T15:59:59Z"`) {
		t.Fatalf("get server expiration = (%d, %q)", getWithExpiration.Code, getWithExpiration.Body.String())
	}
	for _, invalidExpiration := range []any{
		"2026/12/31", "tomorrow", "12-31-2026", "2026-02-30", "2026-12-31 23:59", 123,
	} {
		response := performRequest(
			t, handler, http.MethodPatch, serverPath, map[string]any{"expires_at": invalidExpiration}, sessionCookie,
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid expiration %v status = %d, body = %q", invalidExpiration, response.Code, response.Body.String())
		}
	}
	missingExpiration := performRequest(
		t, handler, http.MethodPatch, serverPath, map[string]any{}, sessionCookie,
	)
	if missingExpiration.Code != http.StatusBadRequest {
		t.Fatalf("missing expiration status = %d, want %d", missingExpiration.Code, http.StatusBadRequest)
	}
	notFoundExpiration := performRequest(
		t, handler, http.MethodPatch, "/api/servers/999999", map[string]any{"expires_at": "2026-12-31"}, sessionCookie,
	)
	if notFoundExpiration.Code != http.StatusNotFound {
		t.Fatalf("missing server expiration status = %d, want %d", notFoundExpiration.Code, http.StatusNotFound)
	}
	clearExpirationResponse := performRequest(
		t, handler, http.MethodPatch, serverPath, map[string]any{"expires_at": nil}, sessionCookie,
	)
	if clearExpirationResponse.Code != http.StatusOK || !strings.Contains(clearExpirationResponse.Body.String(), `"expires_at":null`) {
		t.Fatalf("clear expiration = (%d, %q)", clearExpirationResponse.Code, clearExpirationResponse.Body.String())
	}
	setExpirationAgain := performRequest(
		t, handler, http.MethodPatch, serverPath, map[string]any{"expires_at": "2026-12-31"}, sessionCookie,
	)
	if setExpirationAgain.Code != http.StatusOK {
		t.Fatalf("restore expiration status = %d, body = %q", setExpirationAgain.Code, setExpirationAgain.Body.String())
	}
	regenerateResponse := performRequest(
		t, handler, http.MethodPost, serverPath+"/enrollment", nil, sessionCookie,
	)
	if regenerateResponse.Code != http.StatusCreated {
		t.Fatalf("regenerate enrollment status = %d, body = %q", regenerateResponse.Code, regenerateResponse.Body.String())
	}
	var regenerated createdServerResponse
	if err := json.Unmarshal(regenerateResponse.Body.Bytes(), &regenerated); err != nil {
		t.Fatalf("decode regenerated enrollment: %v", err)
	}
	if regenerated.Server.ID != created.Server.ID || regenerated.Server.Status != "pending" ||
		regenerated.Server.ExpiresAt == nil || !regenerated.Server.ExpiresAt.Equal(expectedExpiration) ||
		regenerated.EnrollmentToken == "" || regenerated.EnrollmentToken == created.EnrollmentToken ||
		strings.Contains(regenerated.AgentInstallationCommand, "--force") {
		t.Fatalf("regenerated enrollment = %+v", regenerated)
	}
	var oldEnrollmentCount, unusedEnrollmentCount int
	var regeneratedPurpose string
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM agent_enrollments WHERE token_hash = ?`, token.Hash(created.EnrollmentToken),
	).Scan(&oldEnrollmentCount); err != nil {
		t.Fatalf("count old enrollment: %v", err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*), purpose FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, created.Server.ID,
	).Scan(&unusedEnrollmentCount, &regeneratedPurpose); err != nil {
		t.Fatalf("read regenerated enrollment: %v", err)
	}
	if oldEnrollmentCount != 0 || unusedEnrollmentCount != 1 || regeneratedPurpose != "rebind" {
		t.Fatalf("regenerated enrollment state = (old %d, unused %d, purpose %q)", oldEnrollmentCount, unusedEnrollmentCount, regeneratedPurpose)
	}
	getAfterRegenerate := performRequest(t, handler, http.MethodGet, serverPath, nil, sessionCookie)
	if getAfterRegenerate.Code != http.StatusOK || strings.Contains(getAfterRegenerate.Body.String(), regenerated.EnrollmentToken) {
		t.Fatalf("get server after regenerate = (%d, %q), token must not be returned", getAfterRegenerate.Code, getAfterRegenerate.Body.String())
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
		!strings.Contains(archivedResponse.Body.String(), `"archived_at"`) ||
		!strings.Contains(archivedResponse.Body.String(), `"expires_at":"2026-12-31T15:59:59Z"`) {
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
	registrationResponse := performRequest(t, handler, http.MethodPost, "/api/agent/register", map[string]any{
		"enrollment_token": created.EnrollmentToken,
		"agent_version":    "v0.5.3",
		"existing_config":  false,
	}, nil)
	if registrationResponse.Code != http.StatusCreated {
		t.Fatalf("register Agent status = %d, body = %q", registrationResponse.Code, registrationResponse.Body.String())
	}
	serverPath := "/api/servers/" + strconv.FormatInt(created.Server.ID, 10)
	vipEnrollmentResponse := performRequest(t, handler, http.MethodPost, serverPath+"/enrollment", nil, vipCookie)
	if vipEnrollmentResponse.Code != http.StatusForbidden {
		t.Fatalf("VIP enrollment status = %d, want %d", vipEnrollmentResponse.Code, http.StatusForbidden)
	}
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
		strings.Contains(rebind.AgentInstallationCommand, "--force") {
		t.Fatalf("rebind response = %+v, want same server and command without --force", rebind)
	}
	var rebindPurpose string
	if err := db.QueryRow(
		`SELECT purpose FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, created.Server.ID,
	).Scan(&rebindPurpose); err != nil {
		t.Fatalf("read rebind purpose: %v", err)
	}
	if rebindPurpose != "rebind" {
		t.Fatalf("rebind purpose = %q, want rebind", rebindPurpose)
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
