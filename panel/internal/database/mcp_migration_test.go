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
func TestMCPWriteMigrationPreservesLegacyReadTokens(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:34] {
		if err := applyMigration(t.Context(), db, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(1,'legacy','hash','admin',1,1)`, `INSERT INTO mcp_tokens(id,name,token_hash,token_prefix,owner_user_id,scope,created_at,expires_at) VALUES(1,'old',x'0123','vpsmcp_',1,'read',1,4000000000)`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var scope string
	var enabled, capabilities int
	if err := db.QueryRow(`SELECT scope FROM mcp_tokens WHERE id=1`).Scan(&scope); err != nil || scope != "read" {
		t.Fatalf("legacy token: %q %v", scope, err)
	}
	if err := db.QueryRow(`SELECT access_scope FROM mcp_tokens WHERE id=1`).Scan(&scope); err != nil || scope != "read" {
		t.Fatalf("legacy effective scope: %q %v", scope, err)
	}
	if err := db.QueryRow(`SELECT write_enabled FROM mcp_settings WHERE id=1`).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatal("write enabled on migration")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM mcp_token_capabilities WHERE token_id=1`).Scan(&capabilities); err != nil || capabilities != 0 {
		t.Fatal("legacy token gained write capability")
	}
}
