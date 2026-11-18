package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestConnectorProxySettingsMigrationLeavesConfigurationUninitialized(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	db.SetMaxOpenConns(1)
	if err := configure(db); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:43] {
		if err := applyMigration(t.Context(), db, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO users (id,username,password_hash,role,created_at,updated_at) VALUES (1,'owner','hash','admin',1,1)`,
		`INSERT INTO remote_subscription_connectors (id,owner_user_id,name,token_hash) VALUES (2,1,'Desktop','hash')`,
		`INSERT INTO remote_subscription_sources (owner_user_id,name,url_ciphertext,interval_seconds,created_at,updated_at,refresh_method,connector_id)
			VALUES (1,'Airport','cipher',300,1,1,'local_connector',2)`,
	} {
		if _, err := db.Exec(statement); err != nil {
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
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM connector_proxy_settings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expected no saved override after migration: %d, %v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM remote_subscription_sources WHERE connector_id=2 AND url_ciphertext='cipher'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing connector assignment was lost: %d, %v", count, err)
	}
	if _, err := db.Exec(`INSERT INTO connector_proxy_settings VALUES (2,'[]')`); err == nil {
		t.Fatal("settings table allowed a second key")
	}
	if _, err := db.Exec(`INSERT INTO connector_proxy_settings VALUES (1,'[]')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var saved string
	if err := db.QueryRow(`SELECT trusted_proxy_ips_json FROM connector_proxy_settings WHERE id=1`).Scan(&saved); err != nil || saved != "[]" {
		t.Fatalf("saved empty list was lost on startup: %q %v", saved, err)
	}
}
