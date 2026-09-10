package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

func Open(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)
	if err := configure(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('admin', 'vip')),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admin_invitations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash TEXT NOT NULL UNIQUE,
			created_by INTEGER NOT NULL REFERENCES users(id),
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_invitations_active
			ON admin_invitations(used_at, expires_at)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at)`,
		`CREATE TABLE IF NOT EXISTS servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'online', 'offline')),
			archived_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agent_enrollments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_enrollments_server_id
			ON agent_enrollments(server_id)`,
		`CREATE TABLE IF NOT EXISTS agents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL UNIQUE REFERENCES servers(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			version TEXT NOT NULL,
			registered_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate sqlite: %w", err)
		}
	}
	if err := migrateUserRoles(ctx, db); err != nil {
		return err
	}
	if err := migrateServerArchive(ctx, db); err != nil {
		return err
	}

	return nil
}

func migrateServerArchive(ctx context.Context, db *sql.DB) error {
	var archivedAtColumnCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('servers') WHERE name = 'archived_at'`,
	).Scan(&archivedAtColumnCount); err != nil {
		return fmt.Errorf("inspect server archived_at column: %w", err)
	}
	if archivedAtColumnCount == 0 {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE servers ADD COLUMN archived_at INTEGER`,
		); err != nil {
			return fmt.Errorf("add server archived_at column: %w", err)
		}
	}
	return nil
}

func migrateUserRoles(ctx context.Context, db *sql.DB) error {
	var roleColumnCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'role'`,
	).Scan(&roleColumnCount); err != nil {
		return fmt.Errorf("inspect user role column: %w", err)
	}
	if roleColumnCount == 0 {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'vip'
			 CHECK (role IN ('admin', 'vip'))`,
		); err != nil {
			return fmt.Errorf("add user role column: %w", err)
		}
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE users
		SET role = CASE
			WHEN id = (SELECT MIN(id) FROM users) THEN 'admin'
			ELSE 'vip'
		END`); err != nil {
		return fmt.Errorf("migrate user roles: %w", err)
	}
	return nil
}

func configure(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite: %w", err)
		}
	}

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}

	return nil
}
