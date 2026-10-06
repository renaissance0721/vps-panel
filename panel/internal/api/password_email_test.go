package api

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	securetoken "github.com/renaissance0721/vps-panel/panel/internal/token"
)

const resetEmailRequestPath = "/api/auth/password-reset/email/request"
const resetEmailConfirmPath = "/api/auth/password-reset/email/confirm"

func resetTokenFromMessage(t *testing.T, message string) string {
	t.Helper()
	for _, line := range strings.Split(message, "\n") {
		if strings.HasPrefix(line, "https://panel.example.com/reset-password?token=") {
			return strings.TrimPrefix(line, "https://panel.example.com/reset-password?token=")
		}
	}
	t.Fatal("reset link missing or untrusted")
	return ""
}

func TestEmailLoginAndResetAPIForEveryRole(t *testing.T) {
	for _, role := range []string{"admin", "vip", "carpool", "subscriber"} {
		t.Run(role, func(t *testing.T) {
			db, handler, adminCookie, sender := setupAccountEmailAPI(t)
			cookie, id, password := adminCookie, int64(1), "strong-password"
			username := "admin"
			if role != "admin" {
				username = "member"
				cookie, id = registerEmailAccount(t, db, handler, adminCookie, role, username)
				password = "current-password"
			}
			if _, err := db.Exec(`UPDATE users SET email='member@example.com',email_verified_at=1 WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			login := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{"username": " MEMBER@Example.COM ", "password": password}, nil)
			if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
				t.Fatalf("email login/session status=%d", login.Code)
			}
			if !strings.Contains(login.Body.String(), `"role":"`+role+`"`) {
				t.Fatal("email login returned wrong role")
			}
			requested := performTrustedEmailRequest(t, handler, http.MethodPost, resetEmailRequestPath, map[string]string{"identifier": username}, nil)
			if requested.Code != http.StatusAccepted || requested.Body.String() != "{\"status\":\"accepted\"}\n" {
				t.Fatalf("request=%d %s", requested.Code, requested.Body.String())
			}
			rawToken := resetTokenFromMessage(t, sender.message.Text)
			if sender.message.Subject != "重置你的 VPS Panel 密码" || len(sender.message.To) != 1 || sender.message.To[0] != "member@example.com" || !strings.Contains(sender.message.HTML, "重置密码") || !strings.Contains(sender.message.Text, "30 分钟") {
				t.Fatal("reset message content incorrect")
			}
			var hash string
			if err := db.QueryRow(`SELECT token_hash FROM account_tokens WHERE purpose='reset_password' AND user_id=?`, id).Scan(&hash); err != nil || hash != securetoken.Hash(rawToken) || hash == rawToken {
				t.Fatal("reset token not stored as hash")
			}
			confirmed := performRequest(t, handler, http.MethodPost, resetEmailConfirmPath, map[string]string{"token": rawToken, "new_password": "replacement-password"}, nil)
			if confirmed.Code != http.StatusOK || len(confirmed.Result().Cookies()) != 0 {
				t.Fatalf("confirm/no-auto-login=%d", confirmed.Code)
			}
			state := performRequest(t, handler, http.MethodGet, "/api/auth/state", nil, cookie)
			if strings.Contains(state.Body.String(), `"authenticated":true`) {
				t.Fatal("old session survived reset")
			}
			for _, identifier := range []string{username, "member@example.com"} {
				login := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{"username": identifier, "password": "replacement-password"}, nil)
				if login.Code != http.StatusOK {
					t.Fatal("new password login failed")
				}
			}
			reused := performRequest(t, handler, http.MethodPost, resetEmailConfirmPath, map[string]string{"token": rawToken, "new_password": "another-password"}, nil)
			if reused.Code != http.StatusBadRequest || !strings.Contains(reused.Body.String(), "密码重置链接无效或已过期") {
				t.Fatal("reused reset accepted")
			}
			var audit string
			if err := db.QueryRow(`SELECT group_concat(action || ' ' || summary) FROM audit_logs WHERE action LIKE 'account.password_reset.email_%'`).Scan(&audit); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(audit, "email_completed") || strings.Contains(audit, rawToken) || strings.Contains(audit, hash) || strings.Contains(audit, "member@example.com") || strings.Contains(audit, "replacement-password") {
				t.Fatal("reset audit missing or leaked secrets")
			}
		})
	}
}

func TestEmailResetPublicResponsesLimitsFailuresAndNoEnumeration(t *testing.T) {
	db, handler, _, sender := setupAccountEmailAPI(t)
	if _, err := db.Exec(`UPDATE users SET email='admin@example.com',email_verified_at=1`); err != nil {
		t.Fatal(err)
	}
	oldWriter := log.Writer()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	for _, identifier := range []string{"unknown", "missing@example.com", "Name <admin@example.com>", "admin@example.com"} {
		request := performRequest(t, handler, http.MethodPost, resetEmailRequestPath, map[string]string{"identifier": identifier}, nil)
		if request.Code != http.StatusAccepted || request.Body.String() != "{\"status\":\"accepted\"}\n" {
			t.Fatalf("public response differs: %d", request.Code)
		}
		if identifier != "admin@example.com" && sender.calls != 0 {
			t.Fatal("unknown identifier sent email")
		}
	}
	first := resetTokenFromMessage(t, sender.message.Text)
	limited := performRequest(t, handler, http.MethodPost, resetEmailRequestPath, map[string]string{"identifier": " ADMIN@Example.COM "}, nil)
	unknownLimited := performRequest(t, handler, http.MethodPost, resetEmailRequestPath, map[string]string{"identifier": "unknown"}, nil)
	if limited.Code != 429 || unknownLimited.Code != 429 || limited.Body.String() != unknownLimited.Body.String() {
		t.Fatal("identifier limiter leaks account state or permits email case bypass")
	}
	// Different identifier and IP still hits the account-level send interval.
	request := jsonRequest(t, http.MethodPost, resetEmailRequestPath, map[string]string{"identifier": "admin"})
	request.RemoteAddr = "198.51.100.22:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 202 || sender.calls != 1 {
		t.Fatal("account limit bypassed via username or another IP")
	}
	if _, err := db.Exec(`UPDATE account_tokens SET created_at=created_at-61`); err != nil {
		t.Fatal(err)
	}
	sender.err = errors.New("upstream contained SMTP password and raw reset token")
	request = jsonRequest(t, http.MethodPost, resetEmailRequestPath, map[string]string{"identifier": "admin"})
	request.RemoteAddr = "198.51.100.23:1234"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 202 || response.Body.String() != "{\"status\":\"accepted\"}\n" {
		t.Fatal("delivery failure exposed account existence")
	}
	failed := resetTokenFromMessage(t, sender.message.Text)
	if strings.Contains(logs.String(), first) || strings.Contains(logs.String(), failed) || strings.Contains(logs.String(), sender.err.Error()) {
		t.Fatal("delivery logs exposed sensitive sender data")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM account_tokens WHERE purpose='reset_password' AND used_at IS NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed send left active reset")
	}
	for _, raw := range []string{first, failed} {
		response := performRequest(t, handler, http.MethodPost, resetEmailConfirmPath, map[string]string{"token": raw, "new_password": "replacement-password"}, nil)
		if response.Code != 400 {
			t.Fatal("old or undelivered reset accepted")
		}
	}
}

func TestEmailResetGlobalUnavailableAndRecipientOwnership(t *testing.T) {
	for _, testCase := range []struct {
		name, domain string
		enabled      bool
		want         int
	}{
		{"disabled", "panel.example.com", false, 409}, {"missing domain", "", true, 503}, {"no email", "panel.example.com", true, 202}, {"unverified", "panel.example.com", true, 202},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, _, _, sender := setupAccountEmailAPI(t)
			if _, err := db.Exec(`UPDATE mail_settings SET enabled=?`, testCase.enabled); err != nil {
				t.Fatal(err)
			}
			if testCase.name == "unverified" {
				if _, err := db.Exec(`UPDATE users SET email='admin@example.com'`); err != nil {
					t.Fatal(err)
				}
			}
			handler := newHandlerWithMailSender(db, t.TempDir(), "dev", BackupConfig{Domain: testCase.domain}, sender)
			for _, identifier := range []string{"admin", "missing@example.com"} {
				response := performRequest(t, handler, http.MethodPost, resetEmailRequestPath, map[string]string{"identifier": identifier}, nil)
				if response.Code != testCase.want {
					t.Fatalf("global response=%d", response.Code)
				}
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM account_tokens`).Scan(&count); err != nil || count != 0 || sender.calls != 0 {
				t.Fatal("unavailable reset generated token or email")
			}
		})
	}
	db, handler, _, sender := setupAccountEmailAPI(t)
	if _, err := db.Exec(`UPDATE users SET email='admin@example.com',email_verified_at=1`); err != nil {
		t.Fatal(err)
	}
	response := performTrustedEmailRequest(t, handler, http.MethodPost, resetEmailRequestPath, map[string]string{"identifier": "admin", "email": "attacker@example.com"}, nil)
	// Strict DTO decoding rejects arbitrary recipient fields.
	if response.Code != 400 || sender.calls != 0 {
		t.Fatal("request accepted arbitrary recipient field")
	}
	message := passwordResetEmailMessage("admin@example.com", `https://panel.example.com/reset-password?token=a&x="b"`)
	if strings.Contains(message.HTML, `&x="b"`) || !strings.Contains(message.HTML, "&amp;x=&#34;b&#34;") {
		t.Fatal("reset HTML URL not escaped")
	}
}

func TestEmailLoginLimiterUsesNormalizedKeyAndResetsSuccessfulPair(t *testing.T) {
	db, handler, _, _ := setupAccountEmailAPI(t)
	if _, err := db.Exec(`UPDATE users SET email='admin@example.com',email_verified_at=1`); err != nil {
		t.Fatal(err)
	}
	limiter := newLoginLimiter()
	now := time.Now()
	for range 4 {
		limiter.RecordFailure("ip", "  ADMIN@Example.COM  ", now)
	}
	limiter.Reset("ip", "admin@example.com")
	if allowed, _ := limiter.Allow("ip", "ADMIN@example.com", now); !allowed {
		t.Fatal("successful normalized pair did not reset")
	}
	for range 5 {
		response := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{"username": " ADMIN@Example.COM ", "password": "wrong-password"}, nil)
		if response.Code != 401 || !strings.Contains(response.Body.String(), "账号或密码错误") {
			t.Fatal("login error exposed identifier status")
		}
	}
	response := performRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{"username": "admin@example.com", "password": "strong-password"}, nil)
	if response.Code != 429 {
		t.Fatal("email case bypassed login limiter")
	}
}

func TestEmailResetLimiterCountsUnknownIdentifiersAtomically(t *testing.T) {
	limiter := newLoginLimiter()
	limiter.pairLimit = 1
	now := time.Now()
	var wg sync.WaitGroup
	results := make(chan bool, 20)
	for index := range 20 {
		wg.Go(func() {
			identifier := "unknown@example.com"
			if index%2 == 0 {
				identifier = "  UNKNOWN@Example.COM  "
			}
			allowed, _ := limiter.Take("ip", identifier, now)
			results <- allowed
		})
	}
	wg.Wait()
	close(results)
	accepted := 0
	for allowed := range results {
		if allowed {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("concurrent reset attempts accepted: %d", accepted)
	}
	if allowed, _ := limiter.Take("ip", "unknown@example.com", now.Add(time.Minute)); !allowed {
		t.Fatal("reset send window did not expire")
	}
}
