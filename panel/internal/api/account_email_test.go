package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
	securetoken "github.com/renaissance0721/vps-panel/panel/internal/token"
)

func setupAccountEmailAPI(t *testing.T) (*sql.DB, http.Handler, *http.Cookie, *mailCaptureSender) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sender := &mailCaptureSender{}
	handler := newHandlerWithMailSender(db, t.TempDir(), "dev", BackupConfig{
		DataDir: dataDir, Domain: "panel.example.com",
	}, sender)
	initialized := performRequest(t, handler, http.MethodPost, "/api/auth/initialize",
		map[string]string{"username": "admin", "password": "strong-password"}, nil)
	if initialized.Code != http.StatusCreated {
		t.Fatal(initialized.Body.String())
	}
	adminCookie := initialized.Result().Cookies()[0]
	configured := performTrustedEmailRequest(t, handler, http.MethodPut, "/api/admin/settings/mail", validMailRequest(), adminCookie)
	if configured.Code != http.StatusOK {
		t.Fatal(configured.Body.String())
	}
	return db, handler, adminCookie, sender
}

func performTrustedEmailRequest(t *testing.T, handler http.Handler, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := jsonRequest(t, method, path, body)
	request.Host = "attacker.invalid"
	if cookie != nil {
		request.AddCookie(cookie)
		if isUnsafeSessionMethod(method) {
			request.Header.Set("Origin", "https://panel.example.com")
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func registerEmailAccount(t *testing.T, db *sql.DB, handler http.Handler, adminCookie *http.Cookie, role, username string) (*http.Cookie, int64) {
	t.Helper()
	created := performTrustedEmailRequest(t, handler, http.MethodPost, "/api/admin/invitations", map[string]string{"role": role}, adminCookie)
	var invitation invitationResponse
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &invitation) != nil {
		t.Fatalf("create %s invitation = %d %s", role, created.Code, created.Body.String())
	}
	registered := performRequest(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"token": invitation.Token, "username": username, "password": "current-password",
	}, nil)
	if registered.Code != http.StatusCreated {
		t.Fatalf("register %s = %d %s", role, registered.Code, registered.Body.String())
	}
	var id int64
	if err := db.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return registered.Result().Cookies()[0], id
}

func tokenFromEmail(t *testing.T, message mailservice.Message) string {
	t.Helper()
	const marker = "https://panel.example.com/verify-email?token="
	start := strings.Index(message.Text, marker)
	if start < 0 {
		t.Fatal("trusted verification URL missing from text")
	}
	value := message.Text[start+len(marker):]
	if end := strings.IndexByte(value, '\n'); end >= 0 {
		value = value[:end]
	}
	if value == "" || strings.Contains(message.HTML, "attacker.invalid") || !strings.Contains(message.HTML, marker+value) {
		t.Fatal("verification message does not use the trusted URL in both bodies")
	}
	return value
}

func TestAllAccountRolesCanRequestAndVerifyEmail(t *testing.T) {
	db, handler, adminCookie, sender := setupAccountEmailAPI(t)
	var adminID int64
	if err := db.QueryRow(`SELECT id FROM users WHERE role = 'admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	accounts := []struct {
		role, username, password string
		cookie                   *http.Cookie
		id                       int64
	}{
		{role: "admin", username: "admin", password: "strong-password", cookie: adminCookie, id: adminID},
	}
	for _, value := range []struct{ role, username string }{{"vip", "vip"}, {"carpool", "carpool"}, {"subscriber", "subscriber"}} {
		cookie, id := registerEmailAccount(t, db, handler, adminCookie, value.role, value.username)
		accounts = append(accounts, struct {
			role, username, password string
			cookie                   *http.Cookie
			id                       int64
		}{value.role, value.username, "current-password", cookie, id})
	}

	for _, account := range accounts {
		t.Run(account.role, func(t *testing.T) {
			target := strings.ToUpper(account.username) + "@Example.COM"
			response := performTrustedEmailRequest(t, handler, http.MethodPost, "/api/account/email/request", map[string]string{
				"email": target, "current_password": account.password,
			}, account.cookie)
			if response.Code != http.StatusAccepted || response.Body.String() != "{\"status\":\"sent\"}\n" || strings.Contains(response.Body.String(), "token") {
				t.Fatalf("request = %d %s", response.Code, response.Body.String())
			}
			wantEmail := strings.ToLower(target)
			if len(sender.message.To) != 1 || sender.message.To[0] != wantEmail ||
				sender.message.Subject != "验证你的 VPS Panel 邮箱" || sender.message.Text == "" || sender.message.HTML == "" {
				t.Fatal("verification recipient, subject or message bodies are incorrect")
			}
			rawToken := tokenFromEmail(t, sender.message)
			var storedHash string
			if err := db.QueryRow(`SELECT token_hash FROM account_tokens WHERE user_id = ? AND used_at IS NULL`, account.id).Scan(&storedHash); err != nil ||
				storedHash == rawToken || storedHash != securetoken.Hash(rawToken) {
				t.Fatalf("stored token hash mismatch: %v", err)
			}
			verified := performRequest(t, handler, http.MethodPost, "/api/auth/email/verify", map[string]string{"token": rawToken}, nil)
			if verified.Code != http.StatusOK || !strings.Contains(verified.Body.String(), `"status":"verified"`) {
				t.Fatalf("verify = %d %s", verified.Code, verified.Body.String())
			}
			status := performRequest(t, handler, http.MethodGet, "/api/account/email", nil, account.cookie)
			if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"email":"`+wantEmail+`"`) ||
				!strings.Contains(status.Body.String(), `"verified":true`) || !strings.Contains(status.Body.String(), `"available":true`) {
				t.Fatalf("status = %d %s", status.Code, status.Body.String())
			}
			reused := performRequest(t, handler, http.MethodPost, "/api/auth/email/verify", map[string]string{"token": rawToken}, nil)
			if reused.Code != http.StatusBadRequest {
				t.Fatalf("reused token = %d %s", reused.Code, reused.Body.String())
			}
		})
	}
}

func TestAccountEmailRequestRejectsUnsafeStatesAndCleansSendFailure(t *testing.T) {
	db, handler, adminCookie, sender := setupAccountEmailAPI(t)
	carpoolCookie, carpoolID := registerEmailAccount(t, db, handler, adminCookie, "carpool", "member")
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/account/email"},
		{http.MethodPost, "/api/account/email/request"},
		{http.MethodPost, "/api/account/email/resend"},
	} {
		if response := performRequest(t, handler, request.method, request.path, nil, nil); response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s = %d", request.path, response.Code)
		}
	}
	for name, body := range map[string]map[string]string{
		"wrong password": {"email": "member@example.com", "current_password": "wrong-password"},
		"invalid email":  {"email": "Name <member@example.com>", "current_password": "current-password"},
	} {
		t.Run(name, func(t *testing.T) {
			response := performTrustedEmailRequest(t, handler, http.MethodPost, "/api/account/email/request", body, carpoolCookie)
			if response.Code != http.StatusUnauthorized && response.Code != http.StatusBadRequest {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
	var tokenCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM account_tokens WHERE user_id = ?`, carpoolID).Scan(&tokenCount); err != nil || tokenCount != 0 {
		t.Fatalf("invalid request tokens = %d, %v", tokenCount, err)
	}
	if _, err := db.Exec(`UPDATE mail_settings SET enabled = 0`); err != nil {
		t.Fatal(err)
	}
	disabled := performTrustedEmailRequest(t, handler, http.MethodPost, "/api/account/email/request", map[string]string{
		"email": "member@example.com", "current_password": "current-password",
	}, carpoolCookie)
	if disabled.Code != http.StatusConflict || !strings.Contains(disabled.Body.String(), "管理员尚未启用邮件服务") {
		t.Fatalf("disabled = %d %s", disabled.Code, disabled.Body.String())
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM account_tokens WHERE user_id = ?`, carpoolID).Scan(&tokenCount); err != nil || tokenCount != 0 {
		t.Fatalf("disabled SMTP created a token: %d, %v", tokenCount, err)
	}
	if _, err := db.Exec(`UPDATE mail_settings SET enabled = 1`); err != nil {
		t.Fatal(err)
	}
	sender.err = &mailservice.DeliveryError{Kind: mailservice.ErrorConnect}
	failed := performTrustedEmailRequest(t, handler, http.MethodPost, "/api/account/email/request", map[string]string{
		"email": "member@example.com", "current_password": "current-password",
	}, carpoolCookie)
	if failed.Code != http.StatusBadGateway || !strings.Contains(failed.Body.String(), "无法连接 SMTP Server") {
		t.Fatalf("send failure = %d %s", failed.Code, failed.Body.String())
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM account_tokens WHERE user_id = ? AND used_at IS NULL`, carpoolID).Scan(&tokenCount); err != nil || tokenCount != 0 {
		t.Fatalf("failed send active tokens = %d, %v", tokenCount, err)
	}
	failedToken := tokenFromEmail(t, sender.message)
	if verified := performRequest(t, handler, http.MethodPost, "/api/auth/email/verify", map[string]string{"token": failedToken}, nil); verified.Code != http.StatusBadRequest {
		t.Fatal("undelivered token remained usable")
	}
	sender.err = nil
	retried := performTrustedEmailRequest(t, handler, http.MethodPost, "/api/account/email/request", map[string]string{
		"email": "member@example.com", "current_password": "current-password",
	}, carpoolCookie)
	if retried.Code != http.StatusTooManyRequests {
		t.Fatalf("failed delivery bypassed send interval: %d", retried.Code)
	}
}

func TestAccountEmailRequiresConfiguredTrustedDomain(t *testing.T) {
	db, _, cookie, sender := setupAccountEmailAPI(t)
	for _, domain := range []string{"", ":80", "https://panel.example.com", "panel.example.com/attacker"} {
		handler := newHandlerWithMailSender(db, t.TempDir(), "dev", BackupConfig{Domain: domain}, sender)
		response := performRequest(t, handler, http.MethodPost, "/api/account/email/request", map[string]string{
			"email": "admin@example.com", "current_password": "strong-password",
		}, cookie)
		// Session origin validation also rejects malformed deployment domains.
		if response.Code != http.StatusServiceUnavailable && response.Code != http.StatusForbidden {
			t.Fatalf("untrusted domain %q allowed an email request: %d", domain, response.Code)
		}
		status := performRequest(t, handler, http.MethodGet, "/api/account/email", nil, cookie)
		if !strings.Contains(status.Body.String(), `"available":false`) {
			t.Fatalf("untrusted domain status = %s", status.Body.String())
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM account_tokens`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("untrusted domain created tokens: %d, %v", count, err)
	}
}

func TestAccountEmailChangeResendDuplicateMaskingAndAudit(t *testing.T) {
	db, handler, adminCookie, sender := setupAccountEmailAPI(t)
	carpoolCookie, carpoolID := registerEmailAccount(t, db, handler, adminCookie, "carpool", "member")
	first := performTrustedEmailRequest(t, handler, http.MethodPost, "/api/account/email/request", map[string]string{
		"email": "Member@Example.COM", "current_password": "current-password",
	}, carpoolCookie)
	if first.Code != http.StatusAccepted {
		t.Fatal(first.Body.String())
	}
	firstToken := tokenFromEmail(t, sender.message)
	limited := performTrustedEmailRequest(t, handler, http.MethodPost, "/api/account/email/resend", map[string]string{
		"current_password": "current-password",
	}, carpoolCookie)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("resend limit = %d %s", limited.Code, limited.Body.String())
	}
	if _, err := db.Exec(`UPDATE account_tokens SET created_at = created_at - 61 WHERE user_id = ?`, carpoolID); err != nil {
		t.Fatal(err)
	}
	resent := performTrustedEmailRequest(t, handler, http.MethodPost, "/api/account/email/resend", map[string]string{
		"current_password": "current-password",
	}, carpoolCookie)
	if resent.Code != http.StatusAccepted {
		t.Fatalf("resend = %d %s", resent.Code, resent.Body.String())
	}
	secondToken := tokenFromEmail(t, sender.message)
	if secondToken == firstToken {
		t.Fatal("resend reused raw token")
	}
	if old := performRequest(t, handler, http.MethodPost, "/api/auth/email/verify", map[string]string{"token": firstToken}, nil); old.Code != http.StatusBadRequest {
		t.Fatalf("old token = %d %s", old.Code, old.Body.String())
	}
	if verified := performRequest(t, handler, http.MethodPost, "/api/auth/email/verify", map[string]string{"token": secondToken}, nil); verified.Code != http.StatusOK {
		t.Fatalf("verify resent = %d %s", verified.Code, verified.Body.String())
	}
	if _, err := db.Exec(`UPDATE account_tokens SET created_at = created_at - 61 WHERE user_id = ?`, carpoolID); err != nil {
		t.Fatal(err)
	}
	change := performTrustedEmailRequest(t, handler, http.MethodPost, "/api/account/email/request", map[string]string{
		"email": "new@example.com", "current_password": "current-password",
	}, carpoolCookie)
	if change.Code != http.StatusAccepted {
		t.Fatalf("change request = %d %s", change.Code, change.Body.String())
	}
	status := performRequest(t, handler, http.MethodGet, "/api/account/email", nil, carpoolCookie)
	if !strings.Contains(status.Body.String(), `"email":"member@example.com"`) ||
		!strings.Contains(status.Body.String(), `"pending_email":"new@example.com"`) {
		t.Fatalf("pending change status = %s", status.Body.String())
	}
	changeToken := tokenFromEmail(t, sender.message)
	if verified := performRequest(t, handler, http.MethodPost, "/api/auth/email/verify", map[string]string{"token": changeToken}, nil); verified.Code != http.StatusOK {
		t.Fatalf("verify change = %d %s", verified.Code, verified.Body.String())
	}

	users := performRequest(t, handler, http.MethodGet, "/api/users", nil, adminCookie)
	if users.Code != http.StatusOK || !strings.Contains(users.Body.String(), `"email_masked":"n***@example.com"`) ||
		strings.Contains(users.Body.String(), `"email":"new@example.com"`) {
		t.Fatalf("admin users = %d %s", users.Code, users.Body.String())
	}
	rows, err := db.Query(`SELECT action, summary FROM audit_logs WHERE action LIKE 'account.email.%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var auditText strings.Builder
	for rows.Next() {
		var action, summary string
		if err := rows.Scan(&action, &summary); err != nil {
			t.Fatal(err)
		}
		auditText.WriteString(action + " " + summary + "\n")
	}
	for _, action := range []string{"account.email.verification_requested", "account.email.verified", "account.email.change_requested", "account.email.changed"} {
		if !strings.Contains(auditText.String(), action) {
			t.Fatalf("audit missing %s: %s", action, auditText.String())
		}
	}
	for _, secret := range []string{"member@example.com", "new@example.com", firstToken, secondToken, changeToken} {
		if strings.Contains(auditText.String(), secret) {
			t.Fatal("audit leaked a private email address or verification token")
		}
	}
}
