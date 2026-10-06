package database

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestOpenExpandsUserRoleConstraintWithoutBreakingReferences(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('admin', 'vip')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO users VALUES (7, 'admin', 'a', 'admin', 10, 11), (9, 'vip', 'v', 'vip', 12, 13)`,
		`CREATE TABLE sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`INSERT INTO sessions VALUES (3, 9, 'session', 9999, 20)`,
		`CREATE TABLE admin_invitations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT NOT NULL UNIQUE,
			created_by INTEGER NOT NULL REFERENCES users(id),
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`INSERT INTO admin_invitations VALUES (4, 'invite', 7, 9999, NULL, 21)`,
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			owner_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO servers VALUES (5, 'server', 9, 'offline', 30, 31)`,
		`CREATE TABLE server_access (
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			PRIMARY KEY (server_id, user_id)
		)`,
		`INSERT INTO server_access VALUES (5, 9)`,
		`CREATE TABLE landing_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_user_id INTEGER NOT NULL REFERENCES users(id),
			name TEXT NOT NULL,
			visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public')),
			protocol TEXT NOT NULL CHECK (protocol IN ('vless', 'shadowsocks')),
			host TEXT NOT NULL,
			port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
			uri TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO landing_nodes VALUES (6, 9, 'landing', 'private', 'vless', 'example.com', 443, 'vless://x', 40, 41)`,
	} {
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare legacy schema: %v\n%s", err, statement)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	for id, want := range map[int64]string{7: "admin", 9: "vip"} {
		var role string
		if err := db.QueryRow(`SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil || role != want {
			t.Fatalf("user %d role = %q, %v; want %q", id, role, err, want)
		}
	}
	if _, err := db.Exec(`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES ('carpool', 'u', 'carpool', 50, 50)`); err != nil {
		t.Fatalf("insert carpool role: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES ('subscriber', 's', 'subscriber', 50, 50)`); err != nil {
		t.Fatalf("insert subscriber role: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO admin_invitations (token_hash, created_by, expires_at, role, created_at) VALUES ('subscriber-invite', 7, 9999, 'subscriber', 50)`); err != nil {
		t.Fatalf("insert subscriber invitation role: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES ('bad', 'b', 'unknown', 50, 50)`); err == nil {
		t.Fatal("unknown role was accepted")
	}
	for query, want := range map[string]int64{
		`SELECT user_id FROM sessions WHERE id = 3`:             9,
		`SELECT created_by FROM admin_invitations WHERE id = 4`: 7,
		`SELECT owner_user_id FROM servers WHERE id = 5`:        9,
		`SELECT user_id FROM server_access WHERE server_id = 5`: 9,
		`SELECT owner_user_id FROM landing_nodes WHERE id = 6`:  9,
	} {
		var got int64
		if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("preserved reference %q = %d, %v; want %d", query, got, err, want)
		}
	}
	var invitationRole string
	if err := db.QueryRow(`SELECT role FROM admin_invitations WHERE id = 4`).Scan(&invitationRole); err != nil || invitationRole != "vip" {
		t.Fatalf("legacy invitation role = %q, %v; want vip", invitationRole, err)
	}
	var tableSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&tableSQL); err != nil || !strings.Contains(tableSQL, "'subscriber'") {
		t.Fatalf("migrated users SQL = %q, %v", tableSQL, err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration left an invalid foreign key")
	}
}

func TestOpenBackfillsSubscriberProfileAndUsage(t *testing.T) {
	dataDir := t.TempDir()
	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users
		(username, password_hash, role, created_at, updated_at)
		VALUES ('subscriber', 'hash', 'subscriber', 50, 51)`); err != nil {
		db.Close()
		t.Fatalf("insert subscriber: %v", err)
	}
	// Legacy bootstrap invokes this backfill before recording schema version 1.
	if err := migrateSubscriberProfiles(t.Context(), db); err != nil {
		db.Close()
		t.Fatalf("backfill subscriber profiles: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close initial database: %v", err)
	}

	db, err = Open(dataDir)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer db.Close()

	var tokenValue string
	var enabled, profileCreatedAt, profileUpdatedAt int64
	if err := db.QueryRow(`SELECT subscription_token, enabled, created_at, updated_at
		FROM subscriber_profiles WHERE user_id = (SELECT id FROM users WHERE username = 'subscriber')`).
		Scan(&tokenValue, &enabled, &profileCreatedAt, &profileUpdatedAt); err != nil {
		t.Fatalf("load backfilled subscriber profile: %v", err)
	}
	if len(tokenValue) != 43 || enabled != 1 || profileCreatedAt != 50 || profileUpdatedAt != 50 {
		t.Fatalf("backfilled profile = token length %d, enabled %d, timestamps %d/%d", len(tokenValue), enabled, profileCreatedAt, profileUpdatedAt)
	}

	var archivedUplink, archivedDownlink, cycleStartedAt, usageUpdatedAt int64
	if err := db.QueryRow(`SELECT archived_uplink_bytes, archived_downlink_bytes, cycle_started_at, updated_at
		FROM subscriber_usage WHERE user_id = (SELECT id FROM users WHERE username = 'subscriber')`).
		Scan(&archivedUplink, &archivedDownlink, &cycleStartedAt, &usageUpdatedAt); err != nil {
		t.Fatalf("load backfilled subscriber usage: %v", err)
	}
	if archivedUplink != 0 || archivedDownlink != 0 || cycleStartedAt != 50 || usageUpdatedAt != 50 {
		t.Fatalf("backfilled usage = %d/%d, timestamps %d/%d", archivedUplink, archivedDownlink, cycleStartedAt, usageUpdatedAt)
	}
}
