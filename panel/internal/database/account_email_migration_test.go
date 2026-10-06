package database

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/auth"
	securetoken "github.com/renaissance0721/vps-panel/panel/internal/token"
	"golang.org/x/crypto/bcrypt"
)

func TestRoleAndAccountEmailMigrationsPreserveAccountsAndReferences(t *testing.T) {
	directory := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(directory, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := configure(db); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:24] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	// Recreate the invitation table exactly as v24 stored it. schemaStatements
	// intentionally describes the latest fresh schema, so applying historical
	// migrations from scratch cannot otherwise represent the former constraint.
	for _, statement := range []string{
		`DROP INDEX idx_admin_invitations_active`,
		`DROP TABLE admin_invitations`,
		`CREATE TABLE admin_invitations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT NOT NULL UNIQUE,
			created_by INTEGER NOT NULL REFERENCES users(id),
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			role TEXT NOT NULL DEFAULT 'vip' CHECK (role IN ('vip', 'user', 'subscriber')),
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX idx_admin_invitations_active ON admin_invitations(used_at, expires_at)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("current-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	rawInvitation, invitationHash, err := securetoken.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES
		 (1, 'admin', '` + string(passwordHash) + `', 'admin', 1, 1),
		 (2, 'vip', 'hash', 'vip', 1, 1),
		 (3, 'carpool-legacy', 'hash', 'user', 1, 1),
		 (4, 'subscriber', 'hash', 'subscriber', 1, 1),
		 (99, 'deleted-high-water', 'hash', 'vip', 1, 1)`,
		`DELETE FROM users WHERE id = 99`,
		`INSERT INTO sessions (id, user_id, token_hash, expires_at, created_at)
		 VALUES (1, 3, '` + securetoken.Hash("migration-session") + `', 9999999999, 1)`,
		`INSERT INTO admin_invitations (id, token_hash, created_by, expires_at, role, created_at)
		 VALUES (10, '` + invitationHash + `', 1, 9999999999, 'user', 1),
		 (50, 'deleted-invite', 1, 9999999999, 'vip', 1)`,
		`DELETE FROM admin_invitations WHERE id = 50`,
		`INSERT INTO password_change_requests (id, user_id, proposed_password_hash, status, created_at)
		 VALUES (1, 3, 'proposed', 'pending', 1)`,
		`INSERT INTO servers (id, name, owner_user_id, created_by_user_id, created_by_role, status, created_at, updated_at)
		 VALUES (1, 'Server', 3, 1, 'admin', 'offline', 1, 1)`,
		`INSERT INTO proxies (id, server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		 VALUES (1, 1, 'Proxy', 'vless', 443, '{}', 1, 1)`,
		`INSERT INTO clients (id, proxy_id, assigned_user_id, name, credential_json, created_at, updated_at)
		 VALUES (1, 1, 3, 'Client', '{}', 1, 1)`,
		`INSERT INTO relays (id, server_id, owner_user_id, name, listen_port, target_type, target_host, target_port, network, created_at, updated_at)
		 VALUES (1, 1, 3, 'Relay', 20000, 'manual', 'example.com', 443, 'tcp', 1, 1)`,
		`INSERT INTO personal_subscription_groups
		 (id, owner_user_id, name, token, client_name, routing_preset_id, created_at, updated_at)
		 SELECT 1, 3, 'Personal', 'personal-token', 'client', id, 1, 1
		 FROM subscription_routing_presets WHERE is_default = 1`,
		`INSERT INTO subscriber_profiles (user_id, subscription_token, created_at, updated_at)
		 VALUES (3, 'subscriber-token', 1, 1)`,
		`INSERT INTO user_account_order (user_id, account_user_id, position) VALUES (1, 3, 1)`,
		`INSERT INTO audit_logs (id, created_at, actor_user_id, actor_username, action, resource_type, resource_id, summary, request_id)
		 VALUES (1, 1, 3, 'carpool-legacy', 'user.create', 'user', 3, 'created', 'request')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed v24 data: %v\n%s", err, statement)
		}
	}
	if err := applyMigration(t.Context(), db, migrations[24]); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(t.Context(), db, migrations[25]); err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[26:] {
		if err := applyMigration(t.Context(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
	authenticated, err := auth.NewService(db).Authenticate(t.Context(), "migration-session")
	if err != nil || authenticated.ID != 3 || authenticated.Role != auth.RoleCarpool {
		t.Fatalf("migrated session authentication = %+v, %v", authenticated, err)
	}

	for id, want := range map[int64]string{1: "admin", 2: "vip", 3: "carpool", 4: "subscriber"} {
		var role string
		if err := db.QueryRow(`SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil || role != want {
			t.Fatalf("user %d role = %q, %v; want %q", id, role, err, want)
		}
	}
	var invitationRole string
	if err := db.QueryRow(`SELECT role FROM admin_invitations WHERE id = 10`).Scan(&invitationRole); err != nil || invitationRole != "carpool" {
		t.Fatalf("invitation role = %q, %v", invitationRole, err)
	}
	for query, want := range map[string]int64{
		`SELECT user_id FROM sessions WHERE id = 1`:                           3,
		`SELECT user_id FROM password_change_requests WHERE id = 1`:           3,
		`SELECT owner_user_id FROM servers WHERE id = 1`:                      3,
		`SELECT assigned_user_id FROM clients WHERE id = 1`:                   3,
		`SELECT owner_user_id FROM relays WHERE id = 1`:                       3,
		`SELECT owner_user_id FROM personal_subscription_groups WHERE id = 1`: 3,
		`SELECT user_id FROM subscriber_profiles WHERE user_id = 3`:           3,
		`SELECT account_user_id FROM user_account_order WHERE user_id = 1`:    3,
		`SELECT actor_user_id FROM audit_logs WHERE id = 1`:                   3,
	} {
		var got int64
		if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("preserved relation %q = %d, %v", query, got, err)
		}
	}
	registered, err := auth.NewService(db).RegisterWithInvitation(t.Context(), rawInvitation, "migrated-invite", "current-password")
	if err != nil || registered.Role != auth.RoleCarpool || registered.ID != 100 {
		t.Fatalf("migrated invitation registration = %+v, %v", registered, err)
	}
	created, err := auth.NewService(db).CreateInvitation(t.Context(), 1, auth.RoleVIP)
	if err != nil || created.ID != 51 {
		t.Fatalf("invitation sequence = %d, %v", created.ID, err)
	}
	if _, err := db.Exec(`INSERT INTO users (username, password_hash, role, created_at, updated_at)
		VALUES ('rejected-user-role', 'hash', 'user', 1, 1)`); err == nil {
		t.Fatal("new schema accepted role=user")
	}
	if _, err := db.Exec(`INSERT INTO users (username, password_hash, role, created_at, updated_at)
		VALUES ('accepted-carpool-role', 'hash', 'carpool', 1, 1)`); err != nil {
		t.Fatalf("new schema rejected role=carpool: %v", err)
	}
	var columns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name IN ('email', 'email_verified_at')`).Scan(&columns); err != nil || columns != 2 {
		t.Fatalf("email columns = %d, %v", columns, err)
	}
	var indexSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_users_email_unique'`).Scan(&indexSQL); err != nil ||
		!strings.Contains(strings.ToLower(indexSQL), "lower(email)") || !strings.Contains(strings.ToLower(indexSQL), "where email is not null") {
		t.Fatalf("email unique index = %q, %v", indexSQL, err)
	}
	var tokenTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'account_tokens'`).Scan(&tokenTable); err != nil || tokenTable != 1 {
		t.Fatalf("account_tokens table count = %d, %v", tokenTable, err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(directory)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer db.Close()
	assertLatestMigrationHistory(t, db)
	assertForeignKeysValid(t, db)
}

func TestFreshAccountEmailSchemaEnforcesRoleOwnershipAndTokenConstraints(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (1, 'admin', 'hash', 'admin', 1, 1), (2, 'member', 'hash', 'carpool', 1, 1)`,
		`UPDATE users SET email = 'owner@example.com', email_verified_at = 1 WHERE id = 1`,
		`INSERT INTO account_tokens (user_id, purpose, target, token_hash, expires_at, created_at)
		 VALUES (2, 'verify_email', 'pending@example.com', 'hash-one', 1000, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for name, statement := range map[string]string{
		"obsolete account role": `INSERT INTO users (username, password_hash, role, created_at, updated_at)
		 VALUES ('obsolete', 'hash', 'user', 1, 1)`,
		"obsolete invitation role": `INSERT INTO admin_invitations (token_hash, created_by, expires_at, role, created_at)
		 VALUES ('invite', 1, 1000, 'user', 1)`,
		"duplicate email":        `UPDATE users SET email = 'owner@example.com', email_verified_at = 1 WHERE id = 2`,
		"unnormalized email":     `UPDATE users SET email = 'OWNER@example.com', email_verified_at = 1 WHERE id = 2`,
		"verified without email": `UPDATE users SET email_verified_at = 1 WHERE id = 2`,
		"multiple active tokens": `INSERT INTO account_tokens (user_id, purpose, target, token_hash, expires_at, created_at)
		 VALUES (2, 'change_email', 'other@example.com', 'hash-two', 1000, 1)`,
		"orphan token": `INSERT INTO account_tokens (user_id, purpose, target, token_hash, expires_at, created_at)
		 VALUES (99, 'verify_email', 'other@example.com', 'hash-two', 1000, 1)`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("schema accepted %s", name)
		}
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM account_tokens`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("account token delete cascade = %d, %v", count, err)
	}
	assertForeignKeysValid(t, db)
}
