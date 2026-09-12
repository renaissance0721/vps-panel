package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesUsableDatabase(t *testing.T) {
	dataDir := t.TempDir()
	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "panel.db")); err != nil {
		t.Fatalf("database file was not created: %v", err)
	}

	for _, table := range []string{
		"users",
		"admin_invitations",
		"sessions",
		"servers",
		"agent_enrollments",
		"agents",
		"server_system_info",
		"server_metrics",
		"proxies",
		"clients",
	} {
		var name string
		if err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&name); err != nil {
			t.Fatalf("table %q was not created: %v", table, err)
		}
	}
	var expirationColumnCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('servers') WHERE name = 'expires_at'`,
	).Scan(&expirationColumnCount); err != nil {
		t.Fatalf("inspect servers expires_at column: %v", err)
	}
	if expirationColumnCount != 1 {
		t.Fatalf("servers expires_at column count = %d, want 1", expirationColumnCount)
	}
	for table, columns := range map[string][]string{
		"servers": {
			"desired_state_version", "monthly_traffic_limit_bytes", "traffic_count_mode", "traffic_reset_day", "traffic_reset_time",
		},
		"agents": {
			"applied_config_version", "config_sync_status", "config_sync_error", "config_synced_at",
		},
		"server_metrics": {
			"nic_rx_bytes", "nic_tx_bytes", "cycle_rx_bytes", "cycle_tx_bytes", "traffic_adjustment_bytes", "cycle_started_at",
		},
		"server_system_info": {"public_ipv4"},
		"proxies":            {"entry_host_mode", "entry_host"},
	} {
		for _, column := range columns {
			var count int
			if err := db.QueryRow(
				`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column,
			).Scan(&count); err != nil {
				t.Fatalf("inspect %s.%s: %v", table, column, err)
			}
			if count != 1 {
				t.Fatalf("%s.%s column count = %d, want 1", table, column, count)
			}
		}
	}
	var legacyPublicHostCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('proxies') WHERE name = 'public_host'`,
	).Scan(&legacyPublicHostCount); err != nil {
		t.Fatalf("inspect legacy proxies.public_host: %v", err)
	}
	if legacyPublicHostCount != 0 {
		t.Fatalf("fresh proxies.public_host column count = %d, want 0", legacyPublicHostCount)
	}
	if _, err := db.Exec(
		`INSERT INTO servers (id, name, status, created_at, updated_at) VALUES (1, 'Defaults', 'pending', 1, 1)`,
	); err != nil {
		t.Fatalf("insert server for metrics defaults: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO server_metrics
		 (server_id, cpu_percent, memory_used_bytes, memory_total_bytes,
		  disk_used_bytes, disk_total_bytes, uptime_seconds, updated_at)
		 VALUES (1, 0, 0, 0, 0, 0, 0, 1)`,
	); err != nil {
		t.Fatalf("insert metrics with defaults: %v", err)
	}
	var adjustment int64
	if err := db.QueryRow(
		`SELECT traffic_adjustment_bytes FROM server_metrics WHERE server_id = 1`,
	).Scan(&adjustment); err != nil {
		t.Fatalf("read traffic adjustment default: %v", err)
	}
	if adjustment != 0 {
		t.Fatalf("traffic adjustment default = %d, want 0", adjustment)
	}
	if _, err := db.Exec(
		`INSERT INTO agents (server_id, token_hash, version, registered_at, created_at, updated_at)
		 VALUES (1, 'agent-hash', 'test', 1, 1, 1)`,
	); err != nil {
		t.Fatalf("insert Agent with config sync defaults: %v", err)
	}
	var desiredVersion, appliedVersion int64
	var syncStatus, syncError string
	var syncedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT servers.desired_state_version, agents.applied_config_version,
		 agents.config_sync_status, agents.config_sync_error, agents.config_synced_at
		 FROM servers JOIN agents ON agents.server_id = servers.id WHERE servers.id = 1`,
	).Scan(&desiredVersion, &appliedVersion, &syncStatus, &syncError, &syncedAt); err != nil {
		t.Fatalf("read config sync defaults: %v", err)
	}
	if desiredVersion != 1 || appliedVersion != 0 || syncStatus != "pending" || syncError != "" || syncedAt.Valid {
		t.Fatalf("config sync defaults = (%d, %d, %q, %q, %v)",
			desiredVersion, appliedVersion, syncStatus, syncError, syncedAt.Valid)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("second migration error = %v", err)
	}
}

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

func TestOpenMigratesTrafficColumnsWithoutLosingMetrics(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			archived_at INTEGER,
			expires_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO servers (id, name, status, created_at, updated_at)
		 VALUES (1, 'Legacy Traffic', 'offline', 1, 1)`,
		`CREATE TABLE server_metrics (
			server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			cpu_percent REAL NOT NULL,
			memory_used_bytes INTEGER NOT NULL,
			memory_total_bytes INTEGER NOT NULL,
			disk_used_bytes INTEGER NOT NULL,
			disk_total_bytes INTEGER NOT NULL,
			uptime_seconds INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO server_metrics
		 (server_id, cpu_percent, memory_used_bytes, memory_total_bytes,
		  disk_used_bytes, disk_total_bytes, uptime_seconds, updated_at)
		 VALUES (1, 12.5, 10, 20, 30, 40, 50, 60)`,
	} {
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare legacy traffic database: %v", err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() migrated traffic database error = %v", err)
	}
	defer db.Close()
	var mode, resetTime string
	var resetDay int
	var limit sql.NullInt64
	var cpu float64
	var nicRX, nicTX, cycleRX, cycleTX, adjustment int64
	var cycleStarted sql.NullInt64
	if err := db.QueryRow(
		`SELECT servers.monthly_traffic_limit_bytes, servers.traffic_count_mode,
		 servers.traffic_reset_day, servers.traffic_reset_time, metrics.cpu_percent,
		 metrics.nic_rx_bytes, metrics.nic_tx_bytes, metrics.cycle_rx_bytes,
		 metrics.cycle_tx_bytes, metrics.traffic_adjustment_bytes, metrics.cycle_started_at
		 FROM servers JOIN server_metrics AS metrics ON metrics.server_id = servers.id
		 WHERE servers.id = 1`,
	).Scan(&limit, &mode, &resetDay, &resetTime, &cpu, &nicRX, &nicTX, &cycleRX, &cycleTX, &adjustment, &cycleStarted); err != nil {
		t.Fatalf("read migrated traffic data: %v", err)
	}
	if limit.Valid || mode != "single" || resetDay != 1 || resetTime != "00:00" ||
		cpu != 12.5 || nicRX != 0 || nicTX != 0 || cycleRX != 0 || cycleTX != 0 || adjustment != 0 || cycleStarted.Valid {
		t.Fatalf("migrated traffic defaults = (%v, %q, %d, %q, %.1f, %d, %d, %d, %d, %d, %v)",
			limit, mode, resetDay, resetTime, cpu, nicRX, nicTX, cycleRX, cycleTX, adjustment, cycleStarted)
	}
}

func TestOpenMigratesExistingUsersWithoutLosingData(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			password_hash TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO users (id, username, password_hash, created_at, updated_at) VALUES
			(1, 'first', 'hash-one', 1, 1),
			(2, 'second', 'hash-two', 2, 2)`,
		`CREATE TABLE admin_invitations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT NOT NULL UNIQUE,
			created_by INTEGER NOT NULL REFERENCES users(id),
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`INSERT INTO admin_invitations
			(token_hash, created_by, expires_at, created_at) VALUES ('invitation-hash', 1, 100, 1)`,
		`CREATE TABLE sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`INSERT INTO sessions (user_id, token_hash, expires_at, created_at)
			VALUES (2, 'session-hash', 100, 2)`,
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO servers (id, name, status, created_at, updated_at)
			VALUES (12, 'Legacy Server', 'offline', 3, 3)`,
	} {
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare legacy database: %v", err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() migrated database error = %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT username, role FROM users ORDER BY id`)
	if err != nil {
		t.Fatalf("read migrated users: %v", err)
	}
	defer rows.Close()
	want := []struct{ username, role string }{{"first", "admin"}, {"second", "vip"}}
	for index, expected := range want {
		if !rows.Next() {
			t.Fatalf("migrated users ended at index %d", index)
		}
		var username, role string
		if err := rows.Scan(&username, &role); err != nil {
			t.Fatalf("scan migrated user: %v", err)
		}
		if username != expected.username || role != expected.role {
			t.Fatalf("migrated user = (%q, %q), want (%q, %q)", username, role, expected.username, expected.role)
		}
	}
	if rows.Next() {
		t.Fatal("migration returned unexpected extra user")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrated users: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close migrated users: %v", err)
	}

	for table, expected := range map[string]int{"admin_invitations": 1, "sessions": 1} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count preserved %s: %v", table, err)
		}
		if count != expected {
			t.Fatalf("preserved %s count = %d, want %d", table, count, expected)
		}
	}
	var serverID int64
	var serverName string
	var archivedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT id, name, archived_at FROM servers WHERE id = 12`,
	).Scan(&serverID, &serverName, &archivedAt); err != nil {
		t.Fatalf("read migrated server: %v", err)
	}
	if serverID != 12 || serverName != "Legacy Server" || archivedAt.Valid {
		t.Fatalf("migrated server = (%d, %q, archived %v), want preserved active server", serverID, serverName, archivedAt.Valid)
	}
}

func TestOpenMigratesAgentEnrollmentPurposes(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
			archived_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO servers (id, name, status, archived_at, created_at, updated_at) VALUES
			(1, 'Active', 'pending', NULL, 1, 1),
			(2, 'Archived', 'pending', 2, 2, 2)`,
		`CREATE TABLE agent_enrollments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`INSERT INTO agent_enrollments
			(id, server_id, token_hash, expires_at, used_at, created_at) VALUES
			(1, 1, 'active-unused', 100, NULL, 1),
			(2, 2, 'archived-unused', 100, NULL, 2),
			(3, 2, 'archived-used', 100, 3, 2)`,
	} {
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare legacy enrollment database: %v", err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() migrated database error = %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, purpose FROM agent_enrollments ORDER BY id`)
	if err != nil {
		t.Fatalf("read migrated enrollment purposes: %v", err)
	}
	defer rows.Close()
	want := []struct {
		id      int64
		purpose string
	}{{1, "initial"}, {2, "rebind"}, {3, "initial"}}
	for _, expected := range want {
		if !rows.Next() {
			t.Fatalf("missing migrated enrollment %d", expected.id)
		}
		var id int64
		var purpose string
		if err := rows.Scan(&id, &purpose); err != nil {
			t.Fatalf("scan migrated enrollment: %v", err)
		}
		if id != expected.id || purpose != expected.purpose {
			t.Fatalf("migrated enrollment = (%d, %q), want (%d, %q)", id, purpose, expected.id, expected.purpose)
		}
	}
	if rows.Next() {
		t.Fatal("migration returned unexpected extra enrollment")
	}
	if _, err := db.Exec(
		`INSERT INTO agent_enrollments (server_id, token_hash, purpose, expires_at, created_at)
		 VALUES (1, 'invalid-purpose', 'other', 100, 1)`,
	); err == nil {
		t.Fatal("agent_enrollments accepted an invalid purpose")
	}
}

func TestOpenMigratesAgentLastSeenWithoutLosingData(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
			archived_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO servers (id, name, status, created_at, updated_at)
		 VALUES (7, 'Legacy Agent Server', 'offline', 1, 1)`,
		`CREATE TABLE agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL UNIQUE REFERENCES servers(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			version TEXT NOT NULL,
			registered_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO agents
		 (id, server_id, token_hash, version, registered_at, created_at, updated_at)
		 VALUES (9, 7, 'legacy-token-hash', 'v0.5.0', 2, 2, 2)`,
	} {
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare legacy Agent database: %v", err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() migrated database error = %v", err)
	}
	defer db.Close()

	var columnCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name = 'last_seen_at'`,
	).Scan(&columnCount); err != nil {
		t.Fatalf("inspect migrated Agent column: %v", err)
	}
	if columnCount != 1 {
		t.Fatalf("last_seen_at column count = %d, want 1", columnCount)
	}
	var serverID, desiredVersion, appliedVersion int64
	var tokenHash, syncStatus, syncError string
	var lastSeenAt, syncedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT agents.server_id, agents.token_hash, agents.last_seen_at,
		 servers.desired_state_version, agents.applied_config_version,
		 agents.config_sync_status, agents.config_sync_error, agents.config_synced_at
		 FROM agents JOIN servers ON servers.id = agents.server_id WHERE agents.id = 9`,
	).Scan(&serverID, &tokenHash, &lastSeenAt, &desiredVersion, &appliedVersion, &syncStatus, &syncError, &syncedAt); err != nil {
		t.Fatalf("read migrated Agent: %v", err)
	}
	if serverID != 7 || tokenHash != "legacy-token-hash" || lastSeenAt.Valid ||
		desiredVersion != 1 || appliedVersion != 0 || syncStatus != "pending" || syncError != "" || syncedAt.Valid {
		t.Fatalf("migrated Agent = (%d, %q, %v, %d, %d, %q, %q, %v)",
			serverID, tokenHash, lastSeenAt.Valid, desiredVersion, appliedVersion, syncStatus, syncError, syncedAt.Valid)
	}
	var systemInfoTableCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'server_system_info'`,
	).Scan(&systemInfoTableCount); err != nil {
		t.Fatalf("inspect migrated system information table: %v", err)
	}
	if systemInfoTableCount != 1 {
		t.Fatalf("server_system_info table count = %d, want 1", systemInfoTableCount)
	}
	var serverCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers WHERE id = 7`).Scan(&serverCount); err != nil {
		t.Fatalf("count preserved server: %v", err)
	}
	if serverCount != 1 {
		t.Fatalf("preserved server count = %d, want 1", serverCount)
	}
}

func TestOpenAddsSystemInfoWithoutLosingExistingAgentData(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
			archived_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO servers (id, name, status, created_at, updated_at)
		 VALUES (15, 'Existing Server', 'offline', 1, 1)`,
		`CREATE TABLE agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL UNIQUE REFERENCES servers(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			version TEXT NOT NULL,
			registered_at INTEGER NOT NULL,
			last_seen_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO agents
		 (id, server_id, token_hash, version, registered_at, last_seen_at, created_at, updated_at)
		 VALUES (16, 15, 'existing-agent-hash', 'v0.6.0', 2, 55, 2, 2)`,
	} {
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare existing Agent database: %v", err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close existing Agent database: %v", err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() migrated database error = %v", err)
	}
	defer db.Close()
	var serverName, tokenHash string
	var lastSeenAt int64
	if err := db.QueryRow(
		`SELECT servers.name, agents.token_hash, agents.last_seen_at
		 FROM servers JOIN agents ON agents.server_id = servers.id WHERE servers.id = 15`,
	).Scan(&serverName, &tokenHash, &lastSeenAt); err != nil {
		t.Fatalf("read preserved Server and Agent: %v", err)
	}
	if serverName != "Existing Server" || tokenHash != "existing-agent-hash" || lastSeenAt != 55 {
		t.Fatalf("preserved data = (%q, %q, %d)", serverName, tokenHash, lastSeenAt)
	}
	var tableCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'server_system_info'`,
	).Scan(&tableCount); err != nil {
		t.Fatalf("inspect server_system_info migration: %v", err)
	}
	if tableCount != 1 {
		t.Fatalf("server_system_info table count = %d, want 1", tableCount)
	}
}

func TestOpenAddsServerExpirationWithoutLosingExistingData(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
			archived_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO servers (id, name, status, archived_at, created_at, updated_at)
		 VALUES (21, 'Existing Server', 'offline', 77, 1, 2)`,
		`CREATE TABLE agent_enrollments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			purpose TEXT NOT NULL DEFAULT 'initial' CHECK (purpose IN ('initial', 'rebind')),
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`INSERT INTO agent_enrollments
		 (id, server_id, token_hash, purpose, expires_at, used_at, created_at)
		 VALUES (22, 21, 'existing-enrollment', 'initial', 100, 3, 2)`,
		`CREATE TABLE agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL UNIQUE REFERENCES servers(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			version TEXT NOT NULL,
			registered_at INTEGER NOT NULL,
			last_seen_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO agents
		 (id, server_id, token_hash, version, registered_at, last_seen_at, created_at, updated_at)
		 VALUES (23, 21, 'existing-agent', 'v0.6.1', 3, 55, 3, 3)`,
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
		 VALUES (21, 'existing-host', 'Debian GNU/Linux', '12', '6.1', 'amd64', '[]', '[]', 'v0.6.1', 55)`,
	} {
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare existing database: %v", err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close existing database: %v", err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() migrated database error = %v", err)
	}
	defer db.Close()
	var name, enrollmentHash, agentHash, hostname string
	var archivedAt, expiresAt sql.NullInt64
	var lastSeenAt int64
	if err := db.QueryRow(
		`SELECT servers.name, servers.archived_at, servers.expires_at, enrollments.token_hash,
		 agents.token_hash, agents.last_seen_at, system_info.hostname
		 FROM servers
		 JOIN agent_enrollments AS enrollments ON enrollments.server_id = servers.id
		 JOIN agents ON agents.server_id = servers.id
		 JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE servers.id = 21`,
	).Scan(&name, &archivedAt, &expiresAt, &enrollmentHash, &agentHash, &lastSeenAt, &hostname); err != nil {
		t.Fatalf("read preserved data: %v", err)
	}
	if name != "Existing Server" || !archivedAt.Valid || archivedAt.Int64 != 77 || expiresAt.Valid || enrollmentHash != "existing-enrollment" ||
		agentHash != "existing-agent" || lastSeenAt != 55 || hostname != "existing-host" {
		t.Fatalf("preserved data = (%q, archived %v, expires %v, %q, %q, %d, %q)",
			name, archivedAt, expiresAt.Valid, enrollmentHash, agentHash, lastSeenAt, hostname)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var metricsTableCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'server_metrics'`,
	).Scan(&metricsTableCount); err != nil {
		t.Fatalf("inspect server_metrics migration: %v", err)
	}
	if metricsTableCount != 1 {
		t.Fatalf("server_metrics table count = %d, want 1", metricsTableCount)
	}
}
