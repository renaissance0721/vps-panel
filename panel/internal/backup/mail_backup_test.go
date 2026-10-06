package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/backup"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
)

type backupMailSender struct{ password string }

func (s *backupMailSender) Send(_ context.Context, config mailservice.Config, _ mailservice.Message) error {
	s.password = config.Password
	return nil
}

func saveBackupMailSettings(t *testing.T, dbPath string, dbService *mailservice.Service) {
	t.Helper()
	_, err := dbService.Save(t.Context(), mailservice.Update{
		Enabled: true, Host: "smtp.example.com", Port: 587, Security: mailservice.SecuritySTARTTLS,
		Username: "noreply@example.com", Password: "backup-secret", FromAddress: "noreply@example.com", FromName: "VPS Panel",
	})
	if err != nil {
		t.Fatalf("save mail settings for %s: %v", dbPath, err)
	}
}

func TestBackupRestoresMailEncryptionKeyWithDatabase(t *testing.T) {
	const version, domain = "v1.2.3", "panel.example.com"
	sourceDir := t.TempDir()
	source, err := database.Open(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	saveBackupMailSettings(t, sourceDir, mailservice.NewService(source, filepath.Join(sourceDir, mailservice.KeyFileName), nil))
	archive, cleanup, err := backup.CreateArchive(t.Context(), source, sourceDir, version, domain, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	targetDir := t.TempDir()
	if err := backup.StageImport(t.Context(), archive, targetDir, version, domain); err != nil {
		t.Fatal(err)
	}
	attempt, err := backup.ApplyPendingRestore(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.Open(targetDir)
	if err != nil {
		_ = attempt.Rollback()
		t.Fatal(err)
	}
	defer target.Close()
	sender := &backupMailSender{}
	service := mailservice.NewService(target, filepath.Join(targetDir, mailservice.KeyFileName), sender)
	if err := service.Send(t.Context(), mailservice.Message{To: []string{"test@example.com"}, Subject: "Subject", Text: "Body"}); err != nil {
		_ = attempt.Rollback()
		t.Fatal(err)
	}
	if sender.password != "backup-secret" {
		t.Fatalf("restored password = %q", sender.password)
	}
	if err := attempt.Commit(); err != nil {
		t.Fatal(err)
	}
	if key, err := os.ReadFile(filepath.Join(targetDir, mailservice.KeyFileName)); err != nil || len(key) != 32 {
		t.Fatalf("restored key length = %d, %v", len(key), err)
	}
}

func TestBackupFailsExplicitlyWhenConfiguredMailKeyIsMissing(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := mailservice.NewService(db, filepath.Join(dataDir, mailservice.KeyFileName), nil)
	saveBackupMailSettings(t, dataDir, service)
	if err := os.Remove(filepath.Join(dataDir, mailservice.KeyFileName)); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := backup.CreateArchive(t.Context(), db, dataDir, "v1.2.3", "panel.example.com", "", "")
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "mail encryption key is unavailable") {
		t.Fatalf("backup without mail key error = %v", err)
	}
}
