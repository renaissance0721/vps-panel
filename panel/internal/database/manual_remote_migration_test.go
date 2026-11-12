package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestManualRemoteMigrationPreservesExistingSourcesAndNodes(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err = configure(db); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:38] {
		if err = applyMigration(t.Context(), db, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(1,'owner','hash','vip',1,1)`,
		`INSERT INTO remote_subscription_sources(id,owner_user_id,name,url_ciphertext,interval_seconds,created_at,updated_at) VALUES(2,1,'source','cipher',300,1,1)`,
		`INSERT INTO remote_subscription_nodes(id,source_id,identity,original_name,display_name,protocol,format,payload_ciphertext,position,generation) VALUES(3,2,'identity','a','b','ss','uri','c',0,1)`,
	} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
	var mode, cipher string
	if err = db.QueryRow(`SELECT source_mode,url_ciphertext FROM remote_subscription_sources WHERE id=2`).Scan(&mode, &cipher); err != nil || mode != "url" || cipher != "cipher" {
		t.Fatalf("source: %q %v", mode, err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM remote_subscription_nodes WHERE id=3 AND source_id=2`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("node: %d %v", count, err)
	}
	if _, err = db.Exec(`UPDATE remote_subscription_sources SET source_mode='invalid' WHERE id=2`); err == nil {
		t.Fatal("invalid mode accepted")
	}
}
