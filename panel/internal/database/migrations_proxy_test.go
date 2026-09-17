package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenMigratesProxyEntryHostAndPublicIPv4(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO servers (id, name, status, created_at, updated_at)
		 VALUES (1, 'Legacy', 'offline', 1, 1)`,
		`CREATE TABLE server_system_info (
			server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			hostname TEXT NOT NULL,
			os_name TEXT NOT NULL,
			os_version TEXT NOT NULL,
			kernel TEXT NOT NULL,
			arch TEXT NOT NULL,
			ipv4 TEXT NOT NULL,
			ipv6 TEXT NOT NULL,
			agent_version TEXT NOT NULL,
			reported_at INTEGER NOT NULL
		)`,
		`INSERT INTO server_system_info
		 (server_id, hostname, os_name, os_version, kernel, arch, ipv4, ipv6, agent_version, reported_at)
		 VALUES (1, '', '', '', '', '', '[]', '[]', '', 1)`,
		`CREATE TABLE proxies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL,
			listen_port INTEGER NOT NULL,
			public_host TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			config_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (server_id, listen_port)
		)`,
		`INSERT INTO proxies
		 (id, server_id, name, protocol, listen_port, public_host, enabled, config_json, created_at, updated_at)
		 VALUES
		 (1, 1, 'Manual', 'vless', 443, 'jp.example.com', 1, '{}', 1, 1),
		 (2, 1, 'Auto', 'vless', 8443, '', 1, '{}', 1, 1)`,
	} {
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatal(err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer db.Close()
	for _, expected := range []struct {
		id   int64
		mode string
		host string
	}{{1, "manual", "jp.example.com"}, {2, "auto", ""}} {
		var mode, host string
		if err := db.QueryRow(
			`SELECT entry_host_mode, entry_host FROM proxies WHERE id = ?`, expected.id,
		).Scan(&mode, &host); err != nil {
			t.Fatal(err)
		}
		if mode != expected.mode || host != expected.host {
			t.Fatalf("proxy %d entry host = (%q, %q), want (%q, %q)", expected.id, mode, host, expected.mode, expected.host)
		}
	}
	var publicIPv4 string
	if err := db.QueryRow(`SELECT public_ipv4 FROM server_system_info WHERE server_id = 1`).Scan(&publicIPv4); err != nil {
		t.Fatal(err)
	}
	if publicIPv4 != "" {
		t.Fatalf("migrated public IPv4 = %q, want empty", publicIPv4)
	}

	if _, err := db.Exec(`UPDATE proxies SET entry_host_mode = 'manual', entry_host = 'new.example.com' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var mode, host string
	if err := db.QueryRow(`SELECT entry_host_mode, entry_host FROM proxies WHERE id = 2`).Scan(&mode, &host); err != nil {
		t.Fatal(err)
	}
	if mode != "manual" || host != "new.example.com" {
		t.Fatalf("repeat migration overwrote entry host: (%q, %q)", mode, host)
	}
}

func TestOpenMigratesProxyProtocolsWithoutLosingVLESSClients(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
			archived_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES (41, 'Legacy', 'offline', 1, 2)`,
		`CREATE TABLE proxies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL CHECK (protocol IN ('vless')),
			listen_port INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
			entry_host_mode TEXT NOT NULL DEFAULT 'auto' CHECK (entry_host_mode IN ('auto', 'manual')),
			entry_host TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			config_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (server_id, listen_port)
		)`,
		`CREATE INDEX idx_proxies_server_id ON proxies(server_id)`,
		`INSERT INTO proxies
			(id, server_id, name, protocol, listen_port, entry_host_mode, entry_host, enabled, config_json, created_at, updated_at)
		 VALUES (42, 41, 'Legacy VLESS', 'vless', 443, 'manual', 'node.example.com', 0, '{"kept":true}', 3, 4)`,
		`CREATE TABLE clients (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			proxy_id INTEGER NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			credential_json TEXT NOT NULL,
			client_udp443 INTEGER NOT NULL DEFAULT 0 CHECK (client_udp443 IN (0, 1)),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO clients
			(id, proxy_id, name, credential_json, client_udp443, enabled, created_at, updated_at)
		 VALUES (43, 42, 'Legacy Client', '{"uuid":"123e4567-e89b-42d3-a456-426614174000"}', 1, 1, 5, 6)`,
	} {
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare legacy proxy database: %v", err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("migrate proxy protocols: %v", err)
	}
	defer db.Close()
	var proxyID, serverID, port, enabled, createdAt, updatedAt int64
	var name, protocol, mode, host, config string
	if err := db.QueryRow(`SELECT id, server_id, name, protocol, listen_port, entry_host_mode,
		entry_host, enabled, config_json, created_at, updated_at FROM proxies WHERE id = 42`).Scan(
		&proxyID, &serverID, &name, &protocol, &port, &mode, &host, &enabled, &config, &createdAt, &updatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if proxyID != 42 || serverID != 41 || name != "Legacy VLESS" || protocol != "vless" || port != 443 ||
		mode != "manual" || host != "node.example.com" || enabled != 0 || config != `{"kept":true}` || createdAt != 3 || updatedAt != 4 {
		t.Fatalf("migrated proxy was changed: id=%d server=%d name=%q protocol=%q port=%d mode=%q host=%q enabled=%d config=%q times=%d/%d",
			proxyID, serverID, name, protocol, port, mode, host, enabled, config, createdAt, updatedAt)
	}
	var clientProxyID int64
	var expiresAt, trafficLimit sql.NullInt64
	var effectiveEnabled int
	var resetMode, resetTime string
	var resetWeekday, resetDay int
	if err := db.QueryRow(`SELECT proxy_id, expires_at, effective_enabled_snapshot, traffic_limit_bytes,
		traffic_reset_mode, traffic_reset_weekday, traffic_reset_day, traffic_reset_time
		FROM clients WHERE id = 43`).Scan(
		&clientProxyID, &expiresAt, &effectiveEnabled, &trafficLimit,
		&resetMode, &resetWeekday, &resetDay, &resetTime,
	); err != nil || clientProxyID != 42 || expiresAt.Valid || effectiveEnabled != 1 || trafficLimit.Valid ||
		resetMode != "never" || resetWeekday != 1 || resetDay != 1 || resetTime != "00:00" {
		t.Fatalf("migrated client = proxy %d, expires %v, effective %d, limit %v, reset %q/%d/%d/%q, error %v",
			clientProxyID, expiresAt, effectiveEnabled, trafficLimit, resetMode, resetWeekday, resetDay, resetTime, err)
	}
	if _, err := db.Exec(`INSERT INTO proxies
		(server_id, name, protocol, listen_port, entry_host_mode, entry_host, enabled, config_json, created_at, updated_at)
		VALUES (41, 'SS', 'shadowsocks', 8388, 'auto', '', 1, '{}', 7, 7)`); err != nil {
		t.Fatalf("migrated schema rejected Shadowsocks: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat proxy protocol migration: %v", err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("proxy protocol migration left a foreign key violation")
	}
	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_proxies_server_id'`).Scan(&indexCount); err != nil || indexCount != 1 {
		t.Fatalf("proxy server index count = %d, %v", indexCount, err)
	}
}
