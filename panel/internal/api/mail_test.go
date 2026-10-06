package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
)

type mailCaptureSender struct {
	config  mailservice.Config
	message mailservice.Message
	calls   int
}

func (s *mailCaptureSender) Send(_ context.Context, config mailservice.Config, message mailservice.Message) error {
	s.config, s.message, s.calls = config, message, s.calls+1
	return nil
}

func setupMailAPI(t *testing.T) (*sql.DB, http.Handler, *http.Cookie, *mailCaptureSender) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sender := &mailCaptureSender{}
	handler := newHandlerWithMailSender(db, t.TempDir(), "dev", BackupConfig{DataDir: dataDir}, sender)
	response := performRequest(t, handler, http.MethodPost, "/api/auth/initialize",
		map[string]string{"username": "admin", "password": "strong-password"}, nil)
	if response.Code != http.StatusCreated {
		t.Fatal(response.Body.String())
	}
	return db, handler, response.Result().Cookies()[0], sender
}

func validMailRequest() map[string]any {
	return map[string]any{
		"enabled": true, "host": "smtp.example.com", "port": 587, "security": "starttls",
		"username": "noreply@example.com", "password": "smtp-secret", "from_address": "noreply@example.com",
		"from_name": "VPS Panel", "reply_to": "support@example.com",
	}
}

func TestMailSettingsPasswordSemanticsMaskingAndUnsavedTest(t *testing.T) {
	db, handler, cookie, sender := setupMailAPI(t)
	response := performRequest(t, handler, http.MethodGet, "/api/admin/settings/mail", nil, cookie)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "password\"") ||
		!strings.Contains(response.Body.String(), `"password_configured":false`) {
		t.Fatalf("initial settings = %d %s", response.Code, response.Body.String())
	}

	input := validMailRequest()
	response = performRequest(t, handler, http.MethodPut, "/api/admin/settings/mail", input, cookie)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "smtp-secret") ||
		strings.Contains(response.Body.String(), "password_ciphertext") || !strings.Contains(response.Body.String(), `"password_configured":true`) {
		t.Fatalf("save = %d %s", response.Code, response.Body.String())
	}
	var firstCiphertext string
	if err := db.QueryRow(`SELECT password_ciphertext FROM mail_settings`).Scan(&firstCiphertext); err != nil ||
		firstCiphertext == "" || strings.Contains(firstCiphertext, "smtp-secret") {
		t.Fatalf("ciphertext = %q, %v", firstCiphertext, err)
	}

	input["password"] = ""
	input["from_name"] = "Renamed"
	response = performRequest(t, handler, http.MethodPut, "/api/admin/settings/mail", input, cookie)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var preserved string
	if err := db.QueryRow(`SELECT password_ciphertext FROM mail_settings`).Scan(&preserved); err != nil || preserved != firstCiphertext {
		t.Fatalf("preserved ciphertext = %q, %v", preserved, err)
	}

	input["password"] = "replacement-secret"
	response = performRequest(t, handler, http.MethodPut, "/api/admin/settings/mail", input, cookie)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var replaced string
	if err := db.QueryRow(`SELECT password_ciphertext FROM mail_settings`).Scan(&replaced); err != nil || replaced == firstCiphertext {
		t.Fatalf("replaced ciphertext = %q, %v", replaced, err)
	}

	input["password"] = ""
	input["host"] = "unsaved.example.com"
	input["to"] = "test@example.com"
	response = performRequest(t, handler, http.MethodPost, "/api/admin/settings/mail/test", input, cookie)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"sent"`) {
		t.Fatalf("test = %d %s", response.Code, response.Body.String())
	}
	if sender.calls != 1 || sender.config.Host != "unsaved.example.com" || sender.config.Password != "replacement-secret" ||
		len(sender.message.To) != 1 || sender.message.To[0] != "test@example.com" {
		t.Fatalf("sender = calls %d config %+v message %+v", sender.calls, sender.config, sender.message)
	}
	response = performRequest(t, handler, http.MethodPost, "/api/admin/settings/mail/test", input, cookie)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit = %d %s", response.Code, response.Body.String())
	}

	rows, err := db.Query(`SELECT action, summary FROM audit_logs WHERE action LIKE 'mail.%' ORDER BY id`)
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
	if !strings.Contains(auditText.String(), "mail.settings.update") || !strings.Contains(auditText.String(), "mail.test") ||
		strings.Contains(auditText.String(), "secret") {
		t.Fatalf("audit logs = %s", auditText.String())
	}
}

func TestMailSettingsValidationAndPermissions(t *testing.T) {
	db, handler, cookie, _ := setupMailAPI(t)
	for name, change := range map[string]map[string]any{
		"port":     {"port": 0},
		"security": {"security": "ssl"},
		"from":     {"from_address": "invalid"},
		"reply-to": {"reply_to": "invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			input := validMailRequest()
			for key, value := range change {
				input[key] = value
			}
			response := performRequest(t, handler, http.MethodPut, "/api/admin/settings/mail", input, cookie)
			if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "smtp-secret") {
				t.Fatalf("validation = %d %s", response.Code, response.Body.String())
			}
		})
	}
	invalidRecipient := validMailRequest()
	invalidRecipient["to"] = "invalid-address"
	response := performRequest(t, handler, http.MethodPost, "/api/admin/settings/mail/test", invalidRecipient, cookie)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid test recipient = %d %s", response.Code, response.Body.String())
	}

	for _, role := range []string{"vip", "user", "subscriber", "anonymous"} {
		storedRole := role
		if storedRole == "anonymous" {
			storedRole = "user"
		}
		if _, err := db.Exec(`UPDATE users SET role = ? WHERE username = 'admin'`, storedRole); err != nil {
			t.Fatal(err)
		}
		requestCookie := cookie
		want := http.StatusForbidden
		if role == "anonymous" {
			requestCookie = nil
			want = http.StatusUnauthorized
		}
		for _, route := range []struct{ method, path string }{
			{http.MethodGet, "/api/admin/settings/mail"},
			{http.MethodPut, "/api/admin/settings/mail"},
			{http.MethodPost, "/api/admin/settings/mail/test"},
		} {
			response := performRequest(t, handler, route.method, route.path, validMailRequest(), requestCookie)
			if response.Code != want {
				t.Fatalf("role=%s route=%s status=%d body=%s", role, route.path, response.Code, response.Body.String())
			}
		}
	}
}

func TestMailSettingsJSONNeverIncludesPassword(t *testing.T) {
	_, handler, cookie, _ := setupMailAPI(t)
	response := performRequest(t, handler, http.MethodPut, "/api/admin/settings/mail", validMailRequest(), cookie)
	var body map[string]any
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatal(response.Body.String())
	}
	if _, exists := body["password"]; exists {
		t.Fatal("password field returned")
	}
	if _, exists := body["password_ciphertext"]; exists {
		t.Fatal("password ciphertext returned")
	}
}
