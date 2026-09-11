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
	} {
		var name string
		if err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&name); err != nil {
			t.Fatalf("table %q was not created: %v", table, err)
		}
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
