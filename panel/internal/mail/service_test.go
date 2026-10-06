package mail

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

type captureSender struct {
	config  Config
	message Message
	err     error
	calls   int
}

func (s *captureSender) Send(_ context.Context, config Config, message Message) error {
	s.config, s.message, s.calls = config, message, s.calls+1
	return s.err
}

func validUpdate() Update {
	return Update{
		Enabled: true, Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS,
		Username: "noreply@example.com", Password: "first-secret", FromAddress: "noreply@example.com",
		FromName: "VPS Panel", ReplyTo: "support@example.com",
	}
}

func TestServiceStoresEncryptedPasswordAndPreservesOrUpdatesIt(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sender := &captureSender{}
	service := NewService(db, filepath.Join(dataDir, KeyFileName), sender)
	saved, err := service.Save(t.Context(), validUpdate())
	if err != nil || !saved.PasswordConfigured {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	var ciphertext string
	if err := db.QueryRow(`SELECT password_ciphertext FROM mail_settings WHERE id = 1`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if ciphertext == "" || strings.Contains(ciphertext, "first-secret") {
		t.Fatalf("stored password = %q", ciphertext)
	}

	update := validUpdate()
	update.Password = ""
	update.FromName = "Renamed"
	if _, err := service.Save(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	var preserved string
	if err := db.QueryRow(`SELECT password_ciphertext FROM mail_settings WHERE id = 1`).Scan(&preserved); err != nil || preserved != ciphertext {
		t.Fatalf("preserved ciphertext = %q, %v", preserved, err)
	}

	restarted := NewService(db, filepath.Join(dataDir, KeyFileName), sender)
	if err := restarted.Send(t.Context(), Message{To: []string{"test@example.com"}, Subject: "Subject", Text: "Body"}); err != nil {
		t.Fatal(err)
	}
	if sender.config.Password != "first-secret" || sender.config.FromName != "Renamed" {
		t.Fatalf("resolved config = %+v", sender.config)
	}

	update.Password = "second-secret"
	if _, err := service.Save(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	var replaced string
	if err := db.QueryRow(`SELECT password_ciphertext FROM mail_settings WHERE id = 1`).Scan(&replaced); err != nil || replaced == ciphertext {
		t.Fatalf("replaced ciphertext = %q, %v", replaced, err)
	}
}

func TestServiceDisabledBusinessSendButAllowsUnsavedTest(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sender := &captureSender{}
	service := NewService(db, filepath.Join(dataDir, KeyFileName), sender)
	update := validUpdate()
	update.Enabled = false
	if _, err := service.Save(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	if err := service.Send(t.Context(), Message{To: []string{"test@example.com"}, Subject: "Subject", Text: "Body"}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled send error = %v", err)
	}
	testInput := TestInput{Update: update, To: "test@example.com"}
	testInput.Password = ""
	testInput.Host = "unsaved.example.com"
	if err := service.SendTest(t.Context(), testInput); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 1 || sender.config.Host != "unsaved.example.com" || sender.config.Password != "first-secret" ||
		sender.message.Subject != testSubject {
		t.Fatalf("test send = calls %d config %+v message %+v", sender.calls, sender.config, sender.message)
	}
}

func TestServiceValidation(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := NewService(db, filepath.Join(dataDir, KeyFileName), &captureSender{})
	valid := validUpdate()
	for name, mutate := range map[string]func(*Update){
		"host":          func(value *Update) { value.Host = "" },
		"invalid host":  func(value *Update) { value.Host = "[::1]" },
		"port":          func(value *Update) { value.Port = 0 },
		"security":      func(value *Update) { value.Security = "ssl" },
		"from":          func(value *Update) { value.FromAddress = "not-an-address" },
		"reply-to":      func(value *Update) { value.ReplyTo = "bad\naddress@example.com" },
		"from name":     func(value *Update) { value.FromName = "Injected\nBcc: victim@example.com" },
		"auth password": func(value *Update) { value.Password = "" },
	} {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if _, err := service.Save(t.Context(), value); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
	withoutAuth := valid
	withoutAuth.Username, withoutAuth.Password = "", ""
	if _, err := service.Save(t.Context(), withoutAuth); err != nil {
		t.Fatalf("save without auth: %v", err)
	}
	if err := service.Send(t.Context(), Message{
		To: []string{"test@example.com"}, Subject: "Injected\r\nBcc: victim@example.com", Text: "Body",
	}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("header injection error = %v", err)
	}
}
