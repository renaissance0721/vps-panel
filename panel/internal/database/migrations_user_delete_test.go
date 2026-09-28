package database_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

func TestLegacySchemaMigrationPreservesUserDeletionForeignKeys(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	legacyDB.SetMaxOpenConns(1)
	for _, statement := range legacyUserDeletionSchema() {
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare legacy schema: %v\n%s", err, statement)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := database.Open(dataDir)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	defer db.Close()

	for _, expected := range []struct {
		table, column, parent, onDelete string
	}{
		{"sessions", "user_id", "users", "CASCADE"},
		{"password_change_requests", "user_id", "users", "CASCADE"},
		{"password_change_requests", "reviewed_by", "users", "SET NULL"},
		{"servers", "owner_user_id", "users", "SET NULL"},
		{"server_access", "user_id", "users", "CASCADE"},
		{"user_server_order", "user_id", "users", "CASCADE"},
		{"user_proxy_order", "user_id", "users", "CASCADE"},
		{"landing_nodes", "owner_user_id", "users", "NO ACTION"},
		{"relays", "owner_user_id", "users", "CASCADE"},
		{"relays", "source_client_id", "clients", "RESTRICT"},
		{"relays", "target_client_id", "clients", "SET NULL"},
		{"relays", "target_landing_id", "landing_nodes", "RESTRICT"},
		{"user_relay_order", "user_id", "users", "CASCADE"},
		{"clients", "assigned_user_id", "users", "SET NULL"},
		{"client_relay_ports", "client_id", "clients", "CASCADE"},
		{"subscriber_profiles", "user_id", "users", "CASCADE"},
		{"subscriber_clients", "user_id", "users", "CASCADE"},
		{"subscriber_clients", "client_id", "clients", "CASCADE"},
		{"subscriber_usage", "user_id", "users", "CASCADE"},
		{"client_metrics", "client_id", "clients", "CASCADE"},
		{"admin_invitations", "created_by", "users", "NO ACTION"},
	} {
		assertMigratedForeignKey(t, db, expected.table, expected.column, expected.parent, expected.onDelete)
	}
	assertMigrationForeignKeyCheck(t, db)

	mustExecMigrationDelete(t, db, `UPDATE servers SET owner_user_id = 2 WHERE id = 10`)
	mustExecMigrationDelete(t, db, `INSERT INTO proxies
		(id, server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		VALUES (20, 10, 'legacy proxy', 'vless', 8443, '{}', 1, 1),
		       (21, 11, 'relay proxy', 'vless', 8444, '{}', 1, 1)`)
	mustExecMigrationDelete(t, db, `INSERT INTO clients
		(id, proxy_id, assigned_user_id, name, credential_json, created_at, updated_at)
		VALUES (30, 20, 2, 'assigned client', '{}', 1, 1)`)
	mustExecMigrationDelete(t, db, `INSERT INTO landing_nodes
		(id, owner_user_id, name, protocol, host, port, uri, created_at, updated_at)
		VALUES (50, 2, 'legacy landing', 'vless', 'example.com', 443, 'vless://example', 1, 1)`)
	mustExecMigrationDelete(t, db, `INSERT INTO relays
		(id, server_id, source_client_id, name, listen_port, target_type,
		 target_landing_id, network, created_at, updated_at)
		VALUES (40, 11, 30, 'legacy reference relay', 9500, 'landing', 50, 'tcp', 1, 1)`)
	mustExecMigrationDelete(t, db, `INSERT INTO admin_invitations
		(token_hash, created_by, expires_at, role, created_at)
		VALUES ('legacy-delete-invite', 2, 9999999999, 'vip', 1)`)

	service := auth.NewService(db)
	mutations, err := service.DeleteUser(t.Context(), 2)
	if err != nil {
		t.Fatalf("DeleteUser() after migration: %v", err)
	}
	if len(mutations) != 2 || mutations[0].ServerID != 10 || mutations[0].Version != 2 ||
		mutations[1].ServerID != 11 || mutations[1].Version != 2 {
		t.Fatalf("DeleteUser() mutations after migration = %+v", mutations)
	}
	var owner sql.NullInt64
	if err := db.QueryRow(`SELECT owner_user_id FROM servers WHERE id = 10`).Scan(&owner); err != nil || owner.Valid {
		t.Fatalf("migrated server owner after user deletion = %+v, %v", owner, err)
	}
	for table, condition := range map[string]string{
		"users":             "id = 2",
		"clients":           "id = 30",
		"landing_nodes":     "id = 50",
		"relays":            "id = 40",
		"admin_invitations": "created_by = 2",
	} {
		assertMigrationCount(t, db, table, condition, 0)
	}
	assertMigrationForeignKeyCheck(t, db)

	mustExecMigrationDelete(t, db, `INSERT INTO users
		(id, username, password_hash, role, created_at, updated_at)
		VALUES (3, 'subscriber', 'hash', 'subscriber', 1, 1)`)
	mustExecMigrationDelete(t, db, `INSERT INTO subscriber_profiles
		(user_id, subscription_token, created_at, updated_at) VALUES (3, 'token', 1, 1)`)
	mustExecMigrationDelete(t, db, `INSERT INTO subscriber_usage
		(user_id, cycle_started_at, updated_at) VALUES (3, 1, 1)`)
	mustExecMigrationDelete(t, db, `INSERT INTO clients
		(id, proxy_id, assigned_user_id, name, credential_json, created_at, updated_at)
		VALUES (31, 20, 3, 'managed client', '{}', 1, 1)`)
	mustExecMigrationDelete(t, db, `INSERT INTO subscriber_clients
		(user_id, proxy_id, client_id, created_at) VALUES (3, 20, 31, 1)`)
	mutations, err = service.DeleteUser(t.Context(), 3)
	if err != nil || len(mutations) != 1 || mutations[0].ServerID != 10 || mutations[0].Version != 3 {
		t.Fatalf("DeleteUser(subscriber) after migration = (%+v, %v)", mutations, err)
	}
	assertMigrationCount(t, db, "users", "id = 3", 0)
	for _, table := range []string{"subscriber_profiles", "subscriber_clients", "subscriber_usage"} {
		assertMigrationCount(t, db, table, "user_id = 3", 0)
	}
	assertMigrationCount(t, db, "clients", "id = 31", 0)
	assertMigrationForeignKeyCheck(t, db)
}

func legacyUserDeletionSchema() []string {
	return []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('admin', 'vip')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO users VALUES
			(1, 'admin', 'hash', 'admin', 1, 1),
			(2, 'legacy-vip', 'hash', 'vip', 1, 1)`,
		`CREATE TABLE admin_invitations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT NOT NULL UNIQUE,
			created_by INTEGER NOT NULL REFERENCES users(id),
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
			visibility TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
			outbound_preference TEXT NOT NULL DEFAULT 'auto'
				CHECK (outbound_preference IN ('auto', 'prefer_ipv4', 'prefer_ipv6')),
			block_china_inbound INTEGER NOT NULL DEFAULT 0 CHECK (block_china_inbound IN (0, 1)),
			desired_state_version INTEGER NOT NULL DEFAULT 1,
			archived_at INTEGER,
			expires_at INTEGER,
			monthly_traffic_limit_bytes INTEGER CHECK (monthly_traffic_limit_bytes >= 0),
			traffic_count_mode TEXT NOT NULL DEFAULT 'single'
				CHECK (traffic_count_mode IN ('single', 'bidirectional')),
			traffic_reset_day INTEGER NOT NULL DEFAULT 1 CHECK (traffic_reset_day BETWEEN 1 AND 31),
			traffic_reset_time TEXT NOT NULL DEFAULT '00:00',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES
			(10, 'client server', 'offline', 1, 1),
			(11, 'relay server', 'offline', 1, 1)`,
		`CREATE TABLE relays (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			listen_address TEXT NOT NULL DEFAULT '0.0.0.0',
			listen_port INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
			entry_host_mode TEXT NOT NULL DEFAULT 'auto' CHECK (entry_host_mode IN ('auto', 'manual')),
			entry_host TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL CHECK (target_type IN ('proxy', 'manual')),
			target_proxy_id INTEGER REFERENCES proxies(id) ON DELETE RESTRICT,
			target_client_id INTEGER REFERENCES clients(id) ON DELETE SET NULL,
			target_host TEXT NOT NULL DEFAULT '',
			target_port INTEGER CHECK (target_port BETWEEN 1 AND 65535),
			network TEXT NOT NULL CHECK (network IN ('tcp', 'udp', 'tcp,udp')),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK ((target_type = 'proxy' AND target_proxy_id IS NOT NULL AND target_host = '' AND target_port IS NULL)
				OR (target_type = 'manual' AND target_proxy_id IS NULL AND target_client_id IS NULL
					AND target_host != '' AND target_port IS NOT NULL))
		)`,
		`CREATE TABLE clients (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			proxy_id INTEGER NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			credential_json TEXT NOT NULL,
			client_udp443 INTEGER NOT NULL DEFAULT 0 CHECK (client_udp443 IN (0, 1)),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			expires_at INTEGER,
			traffic_limit_bytes INTEGER CHECK (traffic_limit_bytes >= 0),
			traffic_reset_mode TEXT NOT NULL DEFAULT 'never'
				CHECK (traffic_reset_mode IN ('never', 'daily', 'weekly', 'monthly')),
			traffic_reset_weekday INTEGER NOT NULL DEFAULT 1 CHECK (traffic_reset_weekday BETWEEN 1 AND 7),
			traffic_reset_day INTEGER NOT NULL DEFAULT 1 CHECK (traffic_reset_day BETWEEN 1 AND 31),
			traffic_reset_time TEXT NOT NULL DEFAULT '00:00',
			effective_enabled_snapshot INTEGER NOT NULL DEFAULT 1
				CHECK (effective_enabled_snapshot IN (0, 1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
	}
}

func assertMigratedForeignKey(t *testing.T, db *sql.DB, table, column, parent, onDelete string) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_list(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, sequence int
		var gotParent, gotColumn, target, onUpdate, gotOnDelete, match string
		if err := rows.Scan(&id, &sequence, &gotParent, &gotColumn, &target, &onUpdate, &gotOnDelete, &match); err != nil {
			t.Fatal(err)
		}
		if gotColumn == column {
			if gotParent != parent || gotOnDelete != onDelete {
				t.Fatalf("%s.%s foreign key = %s ON DELETE %s, want %s ON DELETE %s",
					table, column, gotParent, gotOnDelete, parent, onDelete)
			}
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("missing foreign key for %s.%s", table, column)
}

func assertMigrationForeignKeyCheck(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("PRAGMA foreign_key_check returned a violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func mustExecMigrationDelete(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("execute migrated deletion fixture: %v\n%s", err, statement)
	}
}

func assertMigrationCount(t *testing.T, db *sql.DB, table, condition string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE ` + condition).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count = %d via %s, want %d", table, count, condition, want)
	}
}
