package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenMigratesExistingServersToPublicVisibility(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`CREATE TABLE servers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`); err != nil {
		legacyDB.Close()
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO servers (id, name, status, created_at, updated_at)
		VALUES (1, 'Existing', 'offline', 1, 1)`); err != nil {
		legacyDB.Close()
		t.Fatal(err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() migration error = %v", err)
	}
	defer db.Close()
	var visibility, outboundPreference string
	if err := db.QueryRow(`SELECT visibility, outbound_preference FROM servers WHERE id = 1`).Scan(&visibility, &outboundPreference); err != nil {
		t.Fatal(err)
	}
	if visibility != "public" {
		t.Fatalf("existing server visibility = %q, want public", visibility)
	}
	if outboundPreference != "auto" {
		t.Fatalf("existing server outbound preference = %q, want auto", outboundPreference)
	}
	for _, table := range []string{"user_server_order", "user_proxy_order", "user_relay_order"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("legacy database missing %s after upgrade: %v", table, err)
		}
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
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
