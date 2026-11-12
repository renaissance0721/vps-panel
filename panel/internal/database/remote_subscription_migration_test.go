package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestRemoteSubscriptionMigrationPreservesUsersAndEnforcesOwnership(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := configure(db); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:36] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(1,'admin','hash','admin',1,1)`); err != nil {
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
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
	if _, err := db.Exec(`INSERT INTO remote_subscription_sources(owner_user_id,name,url_ciphertext,interval_seconds,created_at,updated_at) VALUES(99,'bad','cipher',300,1,1)`); err == nil {
		t.Fatal("missing owner accepted")
	}
	if _, err := db.Exec(`INSERT INTO remote_subscription_sources(id,owner_user_id,name,url_ciphertext,interval_seconds,created_at,updated_at) VALUES(1,1,'ok','cipher',300,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO remote_subscription_nodes(source_id,identity,original_name,display_name,protocol,format,payload_ciphertext,position,generation) VALUES(1,'id','name','name','ss','uri','cipher',0,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO remote_subscription_nodes(source_id,identity,original_name,display_name,protocol,format,payload_ciphertext,position,generation) VALUES(1,'id','name','name','ss','uri','cipher',1,1)`); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM remote_subscription_nodes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade count=%d err=%v", count, err)
	}
}
