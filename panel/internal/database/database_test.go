package database

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesUsableDatabase(t *testing.T) {
	dataDir := t.TempDir()
	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "panel.db")); err != nil {
		t.Fatalf("database file was not created: %v", err)
	}

	for _, table := range []string{"users", "admin_invitations", "sessions"} {
		var name string
		if err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&name); err != nil {
			t.Fatalf("authentication table %q was not created: %v", table, err)
		}
	}
}
