package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/backup"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestOpenDatabaseAppliesPendingRestoreBeforeOpeningSQLite(t *testing.T) {
	sourceDir := t.TempDir()
	source, err := database.Open(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES (11,'backed-up','hash','admin',1,1)`); err != nil {
		t.Fatal(err)
	}
	archive, cleanup, err := backup.CreateArchive(context.Background(), source, sourceDir, "v0.23.0", "panel.example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	source.Close()
	targetDir := t.TempDir()
	target, err := database.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Exec(`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES (99,'discarded','hash','admin',1,1)`); err != nil {
		t.Fatal(err)
	}
	target.Close()
	if err := backup.StageImport(context.Background(), archive, targetDir, "v0.23.0", "panel.example.com"); err != nil {
		t.Fatal(err)
	}
	restored, err := openDatabase(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var username string
	if err := restored.QueryRow(`SELECT username FROM users WHERE id=11`).Scan(&username); err != nil || username != "backed-up" {
		t.Fatalf("restored user=%q: %v", username, err)
	}
	var count int
	if err := restored.QueryRow(`SELECT count(*) FROM users WHERE id=99`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("target-only user survived: %d, %v", count, err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "restore", "pending.json")); !os.IsNotExist(err) {
		t.Fatalf("pending marker remained: %v", err)
	}
}

func TestOpenDatabaseKeepsOriginalAfterInvalidPendingRestore(t *testing.T) {
	sourceDir := t.TempDir()
	source, err := database.Open(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	archive, cleanup, err := backup.CreateArchive(context.Background(), source, sourceDir, "v0.23.0", "panel.example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	source.Close()
	targetDir := t.TempDir()
	target, err := database.Open(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Exec(`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES (99,'original','hash','admin',1,1)`); err != nil {
		t.Fatal(err)
	}
	target.Close()
	if err := backup.StageImport(context.Background(), archive, targetDir, "v0.23.0", "panel.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "restore", "panel.db"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	current, err := openDatabase(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	var username string
	if err := current.QueryRow(`SELECT username FROM users WHERE id=99`).Scan(&username); err != nil || username != "original" {
		t.Fatalf("original user=%q: %v", username, err)
	}
}
