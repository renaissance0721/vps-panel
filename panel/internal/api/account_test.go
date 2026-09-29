package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func setupAccountTest(t *testing.T) (*sql.DB, http.Handler, *http.Cookie, int64) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	response := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if response.Code != http.StatusCreated {
		db.Close()
		t.Fatalf("initialize = %d, %s", response.Code, response.Body.String())
	}
	var adminID int64
	if err := db.QueryRow(`SELECT id FROM users WHERE username = 'admin'`).Scan(&adminID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db, handler, response.Result().Cookies()[0], adminID
}

func registerAccount(t *testing.T, db *sql.DB, handler http.Handler, adminCookie *http.Cookie, role, username string) (*http.Cookie, int64) {
	t.Helper()
	created := performRequest(t, handler, http.MethodPost, "/api/admin/invitations", map[string]string{"role": role}, adminCookie)
	var invitation invitationResponse
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &invitation) != nil {
		t.Fatalf("create %s invitation = %d, %s", role, created.Code, created.Body.String())
	}
	registered := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": username, "password": "current-password",
	}, nil)
	if registered.Code != http.StatusCreated {
		t.Fatalf("register %s = %d, %s", role, registered.Code, registered.Body.String())
	}
	var userID int64
	if err := db.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return registered.Result().Cookies()[0], userID
}

func TestAccountRenameAndAdminPasswordChange(t *testing.T) {
	db, handler, adminCookie, _ := setupAccountTest(t)
	defer db.Close()
	_, _ = registerAccount(t, db, handler, adminCookie, "vip", "existing-user")

	for _, test := range []struct {
		name string
		body map[string]string
		want int
	}{
		{"wrong password", map[string]string{"username": "renamed-admin", "current_password": "wrong-password"}, http.StatusUnauthorized},
		{"empty username", map[string]string{"username": "", "current_password": "strong-password"}, http.StatusBadRequest},
		{"duplicate username", map[string]string{"username": "EXISTING-USER", "current_password": "strong-password"}, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(t, handler, http.MethodPatch, "/api/account/username", test.body, adminCookie)
			if response.Code != test.want {
				t.Fatalf("rename = %d, %s; want %d", response.Code, response.Body.String(), test.want)
			}
		})
	}
	renamed := performRequest(t, handler, http.MethodPatch, "/api/account/username", map[string]string{
		"username": "renamed-admin", "current_password": "strong-password",
	}, adminCookie)
	if renamed.Code != http.StatusOK || !strings.Contains(renamed.Body.String(), `"username":"renamed-admin"`) {
		t.Fatalf("rename admin = %d, %s", renamed.Code, renamed.Body.String())
	}
	state := performRequest(t, handler, http.MethodGet, "/api/auth/state", nil, adminCookie)
	if state.Code != http.StatusOK || !strings.Contains(state.Body.String(), `"username":"renamed-admin"`) {
		t.Fatalf("renamed session state = %d, %s", state.Code, state.Body.String())
	}
	wrong := performRequest(t, handler, http.MethodPost, "/api/account/password", map[string]string{
		"current_password": "wrong-password", "new_password": "replacement-password",
	}, adminCookie)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong admin password = %d, %s", wrong.Code, wrong.Body.String())
	}
	changed := performRequest(t, handler, http.MethodPost, "/api/account/password", map[string]string{
		"current_password": "strong-password", "new_password": "replacement-password",
	}, adminCookie)
	if changed.Code != http.StatusOK || !strings.Contains(changed.Body.String(), `"status":"changed"`) {
		t.Fatalf("change admin password = %d, %s", changed.Code, changed.Body.String())
	}
	var requests int
	if err := db.QueryRow(`SELECT COUNT(*) FROM password_change_requests WHERE user_id = (SELECT id FROM users WHERE role = 'admin')`).Scan(&requests); err != nil || requests != 0 {
		t.Fatalf("admin password requests = %d, %v", requests, err)
	}
	oldLogin := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{"username": "renamed-admin", "password": "strong-password"}, nil)
	newLogin := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{"username": "renamed-admin", "password": "replacement-password"}, nil)
	if oldLogin.Code != http.StatusUnauthorized || newLogin.Code != http.StatusOK {
		t.Fatalf("admin login after password change = old %d, new %d", oldLogin.Code, newLogin.Code)
	}
}

func TestAllRolesChangePasswordDirectlyAndLoseExistingSessions(t *testing.T) {
	db, handler, adminCookie, _ := setupAccountTest(t)
	defer db.Close()
	for _, account := range []struct {
		role     string
		username string
	}{
		{role: "vip", username: "vip-user"},
		{role: "user", username: "normal-user"},
		{role: "subscriber", username: "subscriber-user"},
	} {
		cookie, userID := registerAccount(t, db, handler, adminCookie, account.role, account.username)
		changed := performRequest(t, handler, http.MethodPost, "/api/account/password", map[string]string{
			"current_password": "current-password", "new_password": "replacement-password",
		}, cookie)
		if changed.Code != http.StatusOK || !strings.Contains(changed.Body.String(), `"status":"changed"`) {
			t.Fatalf("change %s password = %d, %s", account.role, changed.Code, changed.Body.String())
		}
		state := performRequest(t, handler, http.MethodGet, "/api/auth/state", nil, cookie)
		if state.Code != http.StatusOK || strings.Contains(state.Body.String(), `"authenticated":true`) {
			t.Fatalf("%s session survived password change: %s", account.role, state.Body.String())
		}
		var requests int
		if err := db.QueryRow(`SELECT COUNT(*) FROM password_change_requests WHERE user_id = ?`, userID).Scan(&requests); err != nil || requests != 0 {
			t.Fatalf("%s direct change requests = %d, %v", account.role, requests, err)
		}
		oldLogin := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{"username": account.username, "password": "current-password"}, nil)
		newLogin := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{"username": account.username, "password": "replacement-password"}, nil)
		if oldLogin.Code != http.StatusUnauthorized || newLogin.Code != http.StatusOK {
			t.Fatalf("%s logins after direct change = old %d, new %d", account.role, oldLogin.Code, newLogin.Code)
		}
	}
}

func TestPublicPasswordResetRequestIsOpaqueAndRateLimited(t *testing.T) {
	db, handler, adminCookie, _ := setupAccountTest(t)
	defer db.Close()
	_, _ = registerAccount(t, db, handler, adminCookie, "user", "reset-user")

	request := func(username, password string) *httptest.ResponseRecorder {
		return performRequest(t, handler, http.MethodPost, "/api/auth/password-reset-request", map[string]string{
			"username": username, "new_password": password,
		}, nil)
	}
	real := request("reset-user", "replacement-password")
	pending := request("reset-user", "another-password")
	missing := request("missing-user", "replacement-password")
	for name, response := range map[string]*httptest.ResponseRecorder{"real": real, "pending": pending, "missing": missing} {
		if response.Code != http.StatusAccepted || response.Body.String() != "{\"status\":\"accepted\"}\n" {
			t.Fatalf("%s reset response = %d, %s", name, response.Code, response.Body.String())
		}
	}
	var requests int
	if err := db.QueryRow(`SELECT COUNT(*) FROM password_change_requests`).Scan(&requests); err != nil || requests != 1 {
		t.Fatalf("password reset request count = %d, %v", requests, err)
	}
	if unauthenticated := performRequest(t, handler, http.MethodPost, "/api/account/password-reset-request",
		map[string]string{"new_password": "replacement-password"}, nil); unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated reset = %d, %s", unauthenticated.Code, unauthenticated.Body.String())
	}
	for attempt := 0; attempt < loginPairFailureLimit; attempt++ {
		if response := request("rate-limited-user", "replacement-password"); response.Code != http.StatusAccepted {
			t.Fatalf("reset attempt %d = %d, %s", attempt+1, response.Code, response.Body.String())
		}
	}
	limited := request("rate-limited-user", "replacement-password")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("limited reset = %d, headers %v, body %s", limited.Code, limited.Header(), limited.Body.String())
	}
}

func TestAdminDeleteSubscriberCleansRelatedDataAndPreventsLogin(t *testing.T) {
	db, handler, adminCookie, adminID := setupAccountTest(t)
	defer db.Close()
	subscriberCookie, subscriberID := registerAccount(t, db, handler, adminCookie, "subscriber", "delete-subscriber")
	fixture := userPortalFixture{db: db, handler: handler, adminCookie: adminCookie}
	server, proxyValue := createPortalServerAndProxy(t, fixture, proxystore.ProtocolVLESS, 8443)
	subscriptions := subscriptionstore.NewService(db, relaystore.NewService(db))
	node, _, err := subscriptions.CreatePublishedNode(t.Context(), subscriptionstore.CreatePublishedNodeInput{
		Name: "Delete Test", Mode: subscriptionstore.NodeModeDirect, TargetProxyID: proxyValue.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := subscriptions.CreatePlan(t.Context(), subscriptionstore.CreatePlanInput{Name: "Delete Plan", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := subscriptions.SetPlanNodes(t.Context(), plan.ID, []int64{node.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := subscriptions.UpdateSubscriber(t.Context(), subscriberID, subscriptionstore.UpdateSubscriberInput{PlanIDSet: true, PlanID: &plan.ID}); err != nil {
		t.Fatal(err)
	}
	if response := performRequest(t, handler, http.MethodPost, "/api/account/password-reset-request", map[string]string{
		"new_password": "replacement-password",
	}, subscriberCookie); response.Code != http.StatusAccepted {
		t.Fatalf("subscriber password request = %d, %s", response.Code, response.Body.String())
	}
	if response := performRequest(t, handler, http.MethodDelete, "/api/admin/users/"+strconv.FormatInt(subscriberID, 10), nil, subscriberCookie); response.Code != http.StatusForbidden {
		t.Fatalf("subscriber deleting account = %d, %s", response.Code, response.Body.String())
	}
	var versionBefore int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	if response := performRequest(t, handler, http.MethodDelete, "/api/admin/users/"+strconv.FormatInt(adminID, 10), nil, adminCookie); response.Code != http.StatusBadRequest {
		t.Fatalf("delete admin = %d, %s", response.Code, response.Body.String())
	}
	deleted := performRequest(t, handler, http.MethodDelete, "/api/admin/users/"+strconv.FormatInt(subscriberID, 10), nil, adminCookie)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete subscriber = %d, %s", deleted.Code, deleted.Body.String())
	}
	for table, condition := range map[string]string{
		"users":                    "id = ?",
		"sessions":                 "user_id = ?",
		"password_change_requests": "user_id = ?",
		"subscriber_profiles":      "user_id = ?",
		"subscriber_usage":         "user_id = ?",
		"subscriber_clients":       "user_id = ?",
		"clients":                  "assigned_user_id = ?",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+condition, subscriberID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("remaining %s rows = %d, %v", table, count, err)
		}
	}
	var versionAfter int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, server.Server.ID).Scan(&versionAfter); err != nil || versionAfter != versionBefore+1 {
		t.Fatalf("server version after user deletion = %d -> %d, %v", versionBefore, versionAfter, err)
	}
	state := performRequest(t, handler, http.MethodGet, "/api/auth/state", nil, subscriberCookie)
	login := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{"username": "delete-subscriber", "password": "current-password"}, nil)
	if state.Code != http.StatusOK || strings.Contains(state.Body.String(), `"authenticated":true`) || login.Code != http.StatusUnauthorized {
		t.Fatalf("deleted subscriber access = state %d %s, login %d %s", state.Code, state.Body.String(), login.Code, login.Body.String())
	}
}

func TestAdminDeleteUserLogsInternalErrorWithoutExposingIt(t *testing.T) {
	db, handler, adminCookie, _ := setupAccountTest(t)
	defer db.Close()
	_, userID := registerAccount(t, db, handler, adminCookie, "vip", "delete-log-user")
	if _, err := db.Exec(`CREATE TRIGGER reject_user_delete BEFORE DELETE ON users
		BEGIN SELECT RAISE(ABORT, 'sensitive delete failure'); END`); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&logs)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	response := performRequest(t, handler, http.MethodDelete,
		"/api/admin/users/"+strconv.FormatInt(userID, 10), nil, adminCookie)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "服务器内部错误") {
		t.Fatalf("delete response = %d, %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sensitive delete failure") {
		t.Fatalf("delete response exposed database error: %s", response.Body.String())
	}
	wantPrefix := "delete user " + strconv.FormatInt(userID, 10) + " failed: delete user:"
	if !strings.Contains(logs.String(), wantPrefix) || !strings.Contains(logs.String(), "sensitive delete failure") {
		t.Fatalf("delete log = %q, want wrapped internal error", logs.String())
	}
}
