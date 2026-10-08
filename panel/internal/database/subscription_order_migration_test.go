package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSubscriptionListOrderMigrationCreatesConstraintsAndSurvivesReopen(t *testing.T) {
	directory := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(directory, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := configure(db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	for _, item := range migrations[:27] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	var routingID int64
	if err := db.QueryRow(`SELECT id FROM subscription_routing_presets WHERE is_default = 1`).Scan(&routingID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (1, 'admin', 'hash', 'admin', 1, 1), (2, 'vip', 'hash', 'vip', 1, 1)`,
		`INSERT INTO servers (id, name, status, created_at, updated_at)
		 VALUES (1, 'Server', 'offline', 1, 1)`,
		`INSERT INTO proxies (id, server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		 VALUES (1, 1, 'Proxy', 'vless', 443, '{}', 1, 1)`,
		`INSERT INTO subscription_published_nodes
		 (id, name, mode, target_proxy_id, enabled, created_at, updated_at)
		 VALUES (10, 'A', 'direct', 1, 1, 1, 1), (11, 'B', 'direct', 1, 1, 2, 2)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatalf("seed v27 database: %v\n%s", err, statement)
		}
	}
	if _, err := db.Exec(`INSERT INTO personal_subscription_groups
		(id, owner_user_id, name, token, enabled, client_name, routing_preset_id, created_at, updated_at)
		VALUES (20, 1, 'A', 'token-a', 1, 'admin', ?, 1, 1),
		       (21, 2, 'B', 'token-b', 1, 'vip', ?, 2, 2)`, routingID, routingID); err != nil {
		db.Close()
		t.Fatalf("seed v27 personal subscriptions: %v", err)
	}
	for _, table := range []string{"user_personal_subscription_order", "user_published_node_order"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil || count != 0 {
			db.Close()
			t.Fatalf("pre-migration table %s count = %d, %v", table, count, err)
		}
	}
	if err := applyMigration(t.Context(), db, migrations[27]); err != nil {
		db.Close()
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO user_personal_subscription_order VALUES (1, 20, 1), (2, 21, 1)`,
		`INSERT INTO user_published_node_order VALUES (1, 10, 1), (2, 11, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO user_personal_subscription_order VALUES (1, 21, 1)`); err == nil {
		db.Close()
		t.Fatal("duplicate personal subscription position was accepted")
	}
	if _, err := db.Exec(`DELETE FROM personal_subscription_groups WHERE id = 20`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM subscription_published_nodes WHERE id = 10`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
	for _, table := range []string{"user_personal_subscription_order", "user_published_node_order"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("reopened %s count = %d, %v", table, count, err)
		}
	}
}
