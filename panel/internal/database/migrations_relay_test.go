package database

import (
	"database/sql"
	"testing"
)

func TestMigrateRelayEntryHostDefaultsExistingRowsToAuto(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE relays (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO relays (id, name) VALUES (1, 'Legacy')`); err != nil {
		t.Fatal(err)
	}
	if err := migrateRelayEntryHost(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var mode, host string
	if err := db.QueryRow(`SELECT entry_host_mode, entry_host FROM relays WHERE id = 1`).Scan(&mode, &host); err != nil {
		t.Fatal(err)
	}
	if mode != "auto" || host != "" {
		t.Fatalf("migrated Relay entry host = (%q, %q), want auto and empty", mode, host)
	}
	if _, err := db.Exec(`UPDATE relays SET entry_host_mode = 'manual', entry_host = 'relay.example.com' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := migrateRelayEntryHost(t.Context(), db); err != nil {
		t.Fatalf("repeat Relay entry migration: %v", err)
	}
	if err := db.QueryRow(`SELECT entry_host_mode, entry_host FROM relays WHERE id = 1`).Scan(&mode, &host); err != nil {
		t.Fatal(err)
	}
	if mode != "manual" || host != "relay.example.com" {
		t.Fatalf("repeat migration changed Relay entry host = (%q, %q)", mode, host)
	}
}

func TestMigrateRelayTargetClientKeepsLegacyRowsAndClearsDeletedClient(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE clients (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE relays (id INTEGER PRIMARY KEY)`,
		`INSERT INTO relays (id) VALUES (1)`,
		`INSERT INTO clients (id) VALUES (2)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateRelayTargetClient(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var targetClientID sql.NullInt64
	if err := db.QueryRow(`SELECT target_client_id FROM relays WHERE id = 1`).Scan(&targetClientID); err != nil || targetClientID.Valid {
		t.Fatalf("legacy target client = %v, %v", targetClientID, err)
	}
	if _, err := db.Exec(`UPDATE relays SET target_client_id = 2 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := migrateRelayTargetClient(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM clients WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT target_client_id FROM relays WHERE id = 1`).Scan(&targetClientID); err != nil || targetClientID.Valid {
		t.Fatalf("deleted target client = %v, %v", targetClientID, err)
	}
}

func TestMigrateRelayLandingTargetPreservesRowsOrderAndForeignKeys(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE servers (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE proxies (id INTEGER PRIMARY KEY, server_id INTEGER NOT NULL REFERENCES servers(id))`,
		`CREATE TABLE clients (id INTEGER PRIMARY KEY, proxy_id INTEGER NOT NULL REFERENCES proxies(id))`,
		`CREATE TABLE landing_nodes (
			id INTEGER PRIMARY KEY, owner_user_id INTEGER NOT NULL REFERENCES users(id),
			name TEXT NOT NULL, visibility TEXT NOT NULL, protocol TEXT NOT NULL,
			host TEXT NOT NULL, port INTEGER NOT NULL, uri TEXT NOT NULL,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		)`,
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
			target_client_id INTEGER NULL REFERENCES clients(id) ON DELETE SET NULL,
			target_host TEXT NOT NULL DEFAULT '',
			target_port INTEGER CHECK (target_port BETWEEN 1 AND 65535),
			network TEXT NOT NULL CHECK (network IN ('tcp', 'udp', 'tcp,udp')),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (
				(target_type = 'proxy' AND target_proxy_id IS NOT NULL AND target_host = '' AND target_port IS NULL)
				OR
				(target_type = 'manual' AND target_proxy_id IS NULL AND target_client_id IS NULL AND target_host != '' AND target_port IS NOT NULL)
			)
		)`,
		`CREATE INDEX idx_relays_server_id ON relays(server_id)`,
		`CREATE INDEX idx_relays_target_proxy_id ON relays(target_proxy_id)`,
		`CREATE TABLE user_relay_order (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			relay_id INTEGER NOT NULL REFERENCES relays(id) ON DELETE CASCADE,
			position INTEGER NOT NULL,
			PRIMARY KEY (user_id, relay_id), UNIQUE (user_id, position)
		)`,
		`INSERT INTO users (id) VALUES (1)`,
		`INSERT INTO servers (id) VALUES (1), (2)`,
		`INSERT INTO proxies (id, server_id) VALUES (10, 2)`,
		`INSERT INTO clients (id, proxy_id) VALUES (20, 10)`,
		`INSERT INTO relays
			(id, server_id, name, listen_address, listen_port, entry_host_mode, entry_host,
			 target_type, target_proxy_id, target_client_id, target_host, target_port,
			 network, enabled, created_at, updated_at)
		 VALUES (100, 1, 'Proxy', '127.0.0.1', 9502, 'manual', 'relay.example.com',
			 'proxy', 10, 20, '', NULL, 'tcp,udp', 0, 11, 12)`,
		`INSERT INTO relays
			(id, server_id, name, listen_address, listen_port, entry_host_mode, entry_host,
			 target_type, target_proxy_id, target_client_id, target_host, target_port,
			 network, enabled, created_at, updated_at)
		 VALUES (101, 1, 'Manual', '0.0.0.0', 9503, 'auto', '',
			 'manual', NULL, NULL, 'target.example.com', 8443, 'udp', 1, 13, 14)`,
		`INSERT INTO user_relay_order (user_id, relay_id, position) VALUES (1, 101, 0), (1, 100, 1)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateRelayLandingTarget(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := migrateRelayLandingTarget(t.Context(), db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM relays`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("Relay count = %d, %v", count, err)
	}
	var targetType, name, listenAddress, entryMode, entryHost, network, targetHost string
	var targetProxyID, targetClientID, targetLandingID, targetPort sql.NullInt64
	var enabled, createdAt, updatedAt int64
	if err := db.QueryRow(`SELECT name, listen_address, entry_host_mode, entry_host, target_type,
		target_proxy_id, target_client_id, target_landing_id, target_host, target_port,
		network, enabled, created_at, updated_at FROM relays WHERE id = 100`).Scan(
		&name, &listenAddress, &entryMode, &entryHost, &targetType, &targetProxyID, &targetClientID,
		&targetLandingID, &targetHost, &targetPort, &network, &enabled, &createdAt, &updatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if name != "Proxy" || listenAddress != "127.0.0.1" || entryMode != "manual" || entryHost != "relay.example.com" ||
		targetType != "proxy" || targetProxyID.Int64 != 10 || targetClientID.Int64 != 20 || targetLandingID.Valid ||
		targetHost != "" || targetPort.Valid || network != "tcp,udp" || enabled != 0 || createdAt != 11 || updatedAt != 12 {
		t.Fatalf("migrated proxy Relay fields changed")
	}
	if err := db.QueryRow(`SELECT target_type, target_proxy_id, target_client_id, target_landing_id,
		target_host, target_port, network, enabled, created_at, updated_at FROM relays WHERE id = 101`).Scan(
		&targetType, &targetProxyID, &targetClientID, &targetLandingID, &targetHost, &targetPort,
		&network, &enabled, &createdAt, &updatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if targetType != "manual" || targetProxyID.Valid || targetClientID.Valid || targetLandingID.Valid ||
		targetHost != "target.example.com" || targetPort.Int64 != 8443 || network != "udp" || enabled != 1 ||
		createdAt != 13 || updatedAt != 14 {
		t.Fatalf("migrated manual Relay fields changed")
	}
	var firstRelayID, secondRelayID int64
	if err := db.QueryRow(`SELECT relay_id FROM user_relay_order WHERE user_id = 1 AND position = 0`).Scan(&firstRelayID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT relay_id FROM user_relay_order WHERE user_id = 1 AND position = 1`).Scan(&secondRelayID); err != nil {
		t.Fatal(err)
	}
	if firstRelayID != 101 || secondRelayID != 100 {
		t.Fatalf("Relay order = %d, %d", firstRelayID, secondRelayID)
	}
	if _, err := db.Exec(`INSERT INTO landing_nodes
		(id, owner_user_id, name, visibility, protocol, host, port, uri, created_at, updated_at)
		VALUES (30, 1, 'Landing', 'private', 'vless', 'example.com', 443, 'secret', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO relays
		(server_id, name, listen_address, listen_port, target_type, target_landing_id, network, enabled, created_at, updated_at)
		VALUES (1, 'Landing Relay', '0.0.0.0', 9504, 'landing', 30, 'tcp', 1, 1, 1)`); err != nil {
		t.Fatalf("insert landing Relay: %v", err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration left invalid foreign keys")
	}
}
