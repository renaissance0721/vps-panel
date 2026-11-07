package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMCPMigrationPreservesExistingDataAndDefaultsDisabled(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:33] {
		if err := applyMigration(t.Context(), db, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users (id,username,password_hash,role,created_at,updated_at) VALUES (1,'admin','hash','admin',1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var enabled, users int
	if err := db.QueryRow(`SELECT enabled FROM mcp_settings WHERE id=1`).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("default: %d %v", enabled, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE username='admin'`).Scan(&users); err != nil || users != 1 {
		t.Fatalf("users: %d %v", users, err)
	}
	if _, err := db.Exec(`INSERT INTO mcp_tokens(name,token_hash,token_prefix,owner_user_id,scope,created_at,expires_at) VALUES ('test',x'01','vpsmcp_',1,'write',1,2)`); err == nil {
		t.Fatal("write token accepted")
	}
}
