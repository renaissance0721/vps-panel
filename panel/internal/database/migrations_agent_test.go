package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

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
	var tokenHash, syncStatus, syncError, upgradeTarget, upgradeStatus, upgradeError string
	var lastSeenAt, syncedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT agents.server_id, agents.token_hash, agents.last_seen_at,
		 servers.desired_state_version, agents.applied_config_version,
		 agents.config_sync_status, agents.config_sync_error, agents.config_synced_at,
		 agents.upgrade_target_version, agents.upgrade_status, agents.upgrade_error
		 FROM agents JOIN servers ON servers.id = agents.server_id WHERE agents.id = 9`,
	).Scan(
		&serverID, &tokenHash, &lastSeenAt, &desiredVersion, &appliedVersion,
		&syncStatus, &syncError, &syncedAt, &upgradeTarget, &upgradeStatus, &upgradeError,
	); err != nil {
		t.Fatalf("read migrated Agent: %v", err)
	}
	if serverID != 7 || tokenHash != "legacy-token-hash" || lastSeenAt.Valid ||
		desiredVersion != 1 || appliedVersion != 0 || syncStatus != "pending" || syncError != "" || syncedAt.Valid ||
		upgradeTarget != "" || upgradeStatus != "" || upgradeError != "" {
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
