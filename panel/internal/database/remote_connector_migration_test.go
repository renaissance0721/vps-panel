package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestRemoteConnectorMigrationPreservesSourcesAndCache(t *testing.T) {
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
	for _, m := range migrations[:42] {
		if err := applyMigration(t.Context(), db, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(1,'owner','hash','vip',1,1)`,
		`INSERT INTO remote_subscription_sources(id,owner_user_id,name,url_ciphertext,interval_seconds,created_at,updated_at,source_mode)
			VALUES(2,1,'URL','cipher',300,1,1,'url'),(3,1,'File','emptycipher',300,1,1,'manual')`,
		`INSERT INTO remote_subscription_nodes(id,source_id,identity,original_name,display_name,protocol,format,payload_ciphertext,position,generation)
			VALUES(4,2,'identity','name','display','ss','uri','payloadcipher',0,1)`,
	} {
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
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
	checkCount := func(query string, want int) {
		t.Helper()
		var got int
		if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: count %d, want %d, error %v", query, got, want, err)
		}
	}
	checkCount(`SELECT COUNT(*) FROM remote_subscription_sources WHERE refresh_method='server' AND connector_id IS NULL`, 2)
	checkCount(`SELECT COUNT(*) FROM remote_subscription_sources WHERE id=2 AND url_ciphertext='cipher' AND source_mode='url'`, 1)
	checkCount(`SELECT COUNT(*) FROM remote_subscription_sources WHERE id=3 AND url_ciphertext='emptycipher' AND source_mode='manual'`, 1)
	checkCount(`SELECT COUNT(*) FROM remote_subscription_nodes WHERE id=4 AND source_id=2 AND payload_ciphertext='payloadcipher'`, 1)
	for _, query := range []string{
		`UPDATE remote_subscription_sources SET refresh_method='invalid' WHERE id=2`,
		`UPDATE remote_subscription_sources SET refresh_method='local_connector' WHERE id=3`,
		`UPDATE remote_subscription_sources SET refresh_method='local_connector',connector_id=99 WHERE id=2`,
		`INSERT INTO remote_subscription_connectors(owner_user_id,name,token_hash) VALUES(99,'unknown','hash')`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("constraint accepted %s", query)
		}
	}
	for _, query := range []string{
		`INSERT INTO remote_subscription_connectors(id,owner_user_id,name,token_hash) VALUES(5,1,'PC','hash')`,
		`UPDATE remote_subscription_sources SET refresh_method='local_connector',connector_id=5 WHERE id=2`,
		`DELETE FROM remote_subscription_connectors WHERE id=5`,
	} {
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
	checkCount(`SELECT COUNT(*) FROM remote_subscription_sources WHERE id=2 AND refresh_method='local_connector' AND connector_id IS NULL`, 1)
	checkCount(`SELECT COUNT(*) FROM remote_subscription_nodes WHERE id=4 AND source_id=2`, 1)
	assertForeignKeysValid(t, db)
}
