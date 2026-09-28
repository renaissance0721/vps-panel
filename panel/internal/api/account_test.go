package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
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

func TestAllNonAdminRolesUseReviewedPasswordRequests(t *testing.T) {
	db, handler, adminCookie, _ := setupAccountTest(t)
	defer db.Close()
	type account struct {
		role     string
		username string
		cookie   *http.Cookie
		request  int64
	}
	accounts := []account{{role: "vip", username: "vip-user"}, {role: "user", username: "normal-user"}, {role: "subscriber", username: "subscriber-user"}}
	for index := range accounts {
		accounts[index].cookie, _ = registerAccount(t, db, handler, adminCookie, accounts[index].role, accounts[index].username)
		created := performRequest(t, handler, http.MethodPost, "/api/account/password", map[string]string{
			"current_password": "current-password", "new_password": "replacement-password",
		}, accounts[index].cookie)
		var body struct {
			Request passwordChangeRequestResponse `json:"request"`
		}
		if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &body) != nil || body.Request.Role != accounts[index].role {
			t.Fatalf("create %s request = %d, %s", accounts[index].role, created.Code, created.Body.String())
		}
		accounts[index].request = body.Request.ID
		duplicate := performRequest(t, handler, http.MethodPost, "/api/account/password", map[string]string{
			"current_password": "current-password", "new_password": "another-password",
		}, accounts[index].cookie)
		if duplicate.Code != http.StatusConflict {
			t.Fatalf("duplicate %s request = %d, %s", accounts[index].role, duplicate.Code, duplicate.Body.String())
		}
	}
	listed := performRequest(t, handler, http.MethodGet, "/api/admin/password-change-requests", nil, adminCookie)
	if listed.Code != http.StatusOK {
		t.Fatalf("list requests = %d, %s", listed.Code, listed.Body.String())
	}
	for _, account := range accounts {
		if !strings.Contains(listed.Body.String(), `"username":"`+account.username+`","role":"`+account.role+`"`) {
			t.Fatalf("password request list missing role for %s: %s", account.username, listed.Body.String())
		}
	}
	for index, account := range accounts {
		action := "approve"
		wantStatus := "approved"
		if index == 0 {
			action = "reject"
			wantStatus = "rejected"
		}
		response := performRequest(t, handler, http.MethodPost,
			"/api/admin/password-change-requests/"+strconv.FormatInt(account.request, 10)+"/"+action, nil, adminCookie)
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s %s request = %d, %s", action, account.role, response.Code, response.Body.String())
		}
		var status string
		var proposed sql.NullString
		if err := db.QueryRow(`SELECT status, proposed_password_hash FROM password_change_requests WHERE id = ?`, account.request).Scan(&status, &proposed); err != nil || status != wantStatus || proposed.Valid {
			t.Fatalf("reviewed %s request = status %q hash %+v error %v", account.role, status, proposed, err)
		}
		password := "replacement-password"
		want := http.StatusOK
		if action == "reject" {
			password = "current-password"
		}
		login := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{"username": account.username, "password": password}, nil)
		if login.Code != want {
			t.Fatalf("%s login after %s = %d, %s", account.role, action, login.Code, login.Body.String())
		}
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
	if response := performRequest(t, handler, http.MethodPost, "/api/account/password", map[string]string{
		"current_password": "current-password", "new_password": "replacement-password",
	}, subscriberCookie); response.Code != http.StatusCreated {
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
