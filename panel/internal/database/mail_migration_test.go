package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMailSettingsMigrationPreservesExistingData(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, item := range migrations[:23] {
		if err := applyMigration(context.Background(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (99, 'existing-admin', 'hash', 'admin', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(context.Background(), db, migrations[23]); err != nil {
		t.Fatal(err)
	}
	var enabled int
	var host, security, password string
	var port int
	if err := db.QueryRow(`SELECT enabled, host, port, security, password_ciphertext FROM mail_settings WHERE id = 1`).
		Scan(&enabled, &host, &port, &security, &password); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 || host != "" || port != 587 || security != "starttls" || password != "" {
		t.Fatalf("mail defaults = %d %q %d %q %q", enabled, host, port, security, password)
	}
	var username string
	if err := db.QueryRow(`SELECT username FROM users WHERE id = 99`).Scan(&username); err != nil || username != "existing-admin" {
		t.Fatalf("existing user = %q, %v", username, err)
	}
	for _, item := range migrations[24:] {
		if err := applyMigration(context.Background(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
}
