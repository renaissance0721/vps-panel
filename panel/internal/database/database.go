package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
			desired_state_version INTEGER NOT NULL DEFAULT 1,
			archived_at INTEGER,
			expires_at INTEGER,
			monthly_traffic_limit_bytes INTEGER CHECK (monthly_traffic_limit_bytes >= 0),
			traffic_count_mode TEXT NOT NULL DEFAULT 'single'
				CHECK (traffic_count_mode IN ('single', 'bidirectional')),
			traffic_reset_day INTEGER NOT NULL DEFAULT 1
				CHECK (traffic_reset_day BETWEEN 1 AND 31),
			traffic_reset_time TEXT NOT NULL DEFAULT '00:00',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agent_enrollments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			purpose TEXT NOT NULL DEFAULT 'initial' CHECK (purpose IN ('initial', 'rebind')),
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
			last_seen_at INTEGER,
			applied_config_version INTEGER NOT NULL DEFAULT 0,
			config_sync_status TEXT NOT NULL DEFAULT 'pending'
				CHECK (config_sync_status IN ('pending', 'success', 'failed')),
			config_sync_error TEXT NOT NULL DEFAULT '',
			config_synced_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_system_info (
			server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			hostname TEXT NOT NULL,
			os_name TEXT NOT NULL,
			os_version TEXT NOT NULL,
			kernel TEXT NOT NULL,
			arch TEXT NOT NULL,
			ipv4 TEXT NOT NULL,
			ipv6 TEXT NOT NULL,
			public_ipv4 TEXT NOT NULL DEFAULT '',
			agent_version TEXT NOT NULL,
			reported_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS server_metrics (
			server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
			cpu_percent REAL NOT NULL,
			memory_used_bytes INTEGER NOT NULL,
			memory_total_bytes INTEGER NOT NULL,
			disk_used_bytes INTEGER NOT NULL,
			disk_total_bytes INTEGER NOT NULL,
			uptime_seconds INTEGER NOT NULL,
			nic_rx_bytes INTEGER NOT NULL DEFAULT 0,
			nic_tx_bytes INTEGER NOT NULL DEFAULT 0,
			cycle_rx_bytes INTEGER NOT NULL DEFAULT 0,
			cycle_tx_bytes INTEGER NOT NULL DEFAULT 0,
			traffic_adjustment_bytes INTEGER NOT NULL DEFAULT 0,
			cycle_started_at INTEGER,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS proxies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL CHECK (protocol IN ('vless', 'shadowsocks')),
			listen_port INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
			entry_host_mode TEXT NOT NULL DEFAULT 'auto'
				CHECK (entry_host_mode IN ('auto', 'manual')),
			entry_host TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			config_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (server_id, listen_port)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_proxies_server_id ON proxies(server_id)`,
		`CREATE TABLE IF NOT EXISTS clients (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			proxy_id INTEGER NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			credential_json TEXT NOT NULL,
			client_udp443 INTEGER NOT NULL DEFAULT 0 CHECK (client_udp443 IN (0, 1)),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_clients_proxy_id ON clients(proxy_id)`,
		`CREATE TABLE IF NOT EXISTS client_metrics (
			client_id INTEGER PRIMARY KEY REFERENCES clients(id) ON DELETE CASCADE,
			xray_uplink_bytes INTEGER NOT NULL CHECK (xray_uplink_bytes >= 0),
			xray_downlink_bytes INTEGER NOT NULL CHECK (xray_downlink_bytes >= 0),
			cycle_uplink_bytes INTEGER NOT NULL DEFAULT 0 CHECK (cycle_uplink_bytes >= 0),
			cycle_downlink_bytes INTEGER NOT NULL DEFAULT 0 CHECK (cycle_downlink_bytes >= 0),
			cycle_started_at INTEGER NOT NULL,
			last_activity_at INTEGER,
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
	if err := migrateServerExpiration(ctx, db); err != nil {
		return err
	}
	if err := migrateAgentEnrollmentPurpose(ctx, db); err != nil {
		return err
	}
	if err := migrateAgentLastSeen(ctx, db); err != nil {
		return err
	}
	if err := migrateServerTraffic(ctx, db); err != nil {
		return err
	}
	if err := migrateAgentConfigSync(ctx, db); err != nil {
		return err
	}
	if err := migrateServerPublicIPv4(ctx, db); err != nil {
		return err
	}
	if err := migrateProxyEntryHost(ctx, db); err != nil {
		return err
	}
	if err := migrateProxyProtocols(ctx, db); err != nil {
		return err
	}

	return nil
}

func migrateProxyProtocols(ctx context.Context, db *sql.DB) error {
	var tableSQL string
	if err := db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'proxies'`,
	).Scan(&tableSQL); err != nil {
		return fmt.Errorf("inspect proxies table: %w", err)
	}
	if strings.Contains(strings.ToLower(tableSQL), "'shadowsocks'") {
		return nil
	}

	connection, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open proxy protocol migration connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for proxy protocol migration: %w", err)
	}
	defer connection.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`)

	tx, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin proxy protocol migration: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE proxies_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL CHECK (protocol IN ('vless', 'shadowsocks')),
			listen_port INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
			entry_host_mode TEXT NOT NULL DEFAULT 'auto'
				CHECK (entry_host_mode IN ('auto', 'manual')),
			entry_host TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			config_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (server_id, listen_port)
		)`,
		`INSERT INTO proxies_new
			(id, server_id, name, protocol, listen_port, entry_host_mode, entry_host,
			 enabled, config_json, created_at, updated_at)
		 SELECT id, server_id, name, protocol, listen_port, entry_host_mode, entry_host,
			 enabled, config_json, created_at, updated_at FROM proxies`,
		`DROP TABLE proxies`,
		`ALTER TABLE proxies_new RENAME TO proxies`,
		`CREATE INDEX idx_proxies_server_id ON proxies(server_id)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate proxy protocols: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit proxy protocol migration: %w", err)
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("restore foreign keys after proxy protocol migration: %w", err)
	}
	rows, err := connection.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check proxy protocol foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("proxy protocol migration left invalid foreign keys")
	}
	return rows.Err()
}

func migrateServerPublicIPv4(ctx context.Context, db *sql.DB) error {
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('server_system_info') WHERE name = 'public_ipv4'`,
	).Scan(&count); err != nil {
		return fmt.Errorf("inspect server_system_info.public_ipv4: %w", err)
	}
	if count == 0 {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE server_system_info ADD COLUMN public_ipv4 TEXT NOT NULL DEFAULT ''`,
		); err != nil {
			return fmt.Errorf("add server_system_info.public_ipv4: %w", err)
		}
	}
	return nil
}

func migrateProxyEntryHost(ctx context.Context, db *sql.DB) error {
	var modeCount, hostCount, publicHostCount int
	for name, destination := range map[string]*int{
		"entry_host_mode": &modeCount,
		"entry_host":      &hostCount,
		"public_host":     &publicHostCount,
	} {
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('proxies') WHERE name = ?`, name,
		).Scan(destination); err != nil {
			return fmt.Errorf("inspect proxies.%s: %w", name, err)
		}
	}
	added := modeCount == 0 || hostCount == 0
	if modeCount == 0 {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE proxies ADD COLUMN entry_host_mode TEXT NOT NULL DEFAULT 'auto'
			 CHECK (entry_host_mode IN ('auto', 'manual'))`,
		); err != nil {
			return fmt.Errorf("add proxies.entry_host_mode: %w", err)
		}
	}
	if hostCount == 0 {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE proxies ADD COLUMN entry_host TEXT NOT NULL DEFAULT ''`,
		); err != nil {
			return fmt.Errorf("add proxies.entry_host: %w", err)
		}
	}
	if added && publicHostCount != 0 {
		if _, err := db.ExecContext(ctx,
			`UPDATE proxies
			 SET entry_host_mode = CASE WHEN trim(public_host) = '' THEN 'auto' ELSE 'manual' END,
			     entry_host = trim(public_host)`,
		); err != nil {
			return fmt.Errorf("migrate proxies.public_host: %w", err)
		}
	}
	return nil
}

func migrateAgentConfigSync(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		table      string
		name       string
		definition string
	}{
		{"servers", "desired_state_version", "desired_state_version INTEGER NOT NULL DEFAULT 1"},
		{"agents", "applied_config_version", "applied_config_version INTEGER NOT NULL DEFAULT 0"},
		{"agents", "config_sync_status", "config_sync_status TEXT NOT NULL DEFAULT 'pending' CHECK (config_sync_status IN ('pending', 'success', 'failed'))"},
		{"agents", "config_sync_error", "config_sync_error TEXT NOT NULL DEFAULT ''"},
		{"agents", "config_synced_at", "config_synced_at INTEGER"},
	}
	for _, column := range columns {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?", column.table)
		if err := db.QueryRowContext(ctx, query, column.name).Scan(&count); err != nil {
			return fmt.Errorf("inspect %s.%s column: %w", column.table, column.name, err)
		}
		if count != 0 {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", column.table, column.definition)
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add %s.%s column: %w", column.table, column.name, err)
		}
	}
	return nil
}

func migrateServerTraffic(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		table      string
		name       string
		definition string
	}{
		{"servers", "monthly_traffic_limit_bytes", "monthly_traffic_limit_bytes INTEGER CHECK (monthly_traffic_limit_bytes >= 0)"},
		{"servers", "traffic_count_mode", "traffic_count_mode TEXT NOT NULL DEFAULT 'single' CHECK (traffic_count_mode IN ('single', 'bidirectional'))"},
		{"servers", "traffic_reset_day", "traffic_reset_day INTEGER NOT NULL DEFAULT 1 CHECK (traffic_reset_day BETWEEN 1 AND 31)"},
		{"servers", "traffic_reset_time", "traffic_reset_time TEXT NOT NULL DEFAULT '00:00'"},
		{"server_metrics", "nic_rx_bytes", "nic_rx_bytes INTEGER NOT NULL DEFAULT 0"},
		{"server_metrics", "nic_tx_bytes", "nic_tx_bytes INTEGER NOT NULL DEFAULT 0"},
		{"server_metrics", "cycle_rx_bytes", "cycle_rx_bytes INTEGER NOT NULL DEFAULT 0"},
		{"server_metrics", "cycle_tx_bytes", "cycle_tx_bytes INTEGER NOT NULL DEFAULT 0"},
		{"server_metrics", "traffic_adjustment_bytes", "traffic_adjustment_bytes INTEGER NOT NULL DEFAULT 0"},
		{"server_metrics", "cycle_started_at", "cycle_started_at INTEGER"},
	}
	for _, column := range columns {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?", column.table)
		if err := db.QueryRowContext(ctx, query, column.name).Scan(&count); err != nil {
			return fmt.Errorf("inspect %s.%s column: %w", column.table, column.name, err)
		}
		if count != 0 {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", column.table, column.definition)
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add %s.%s column: %w", column.table, column.name, err)
		}
	}
	return nil
}

func migrateServerExpiration(ctx context.Context, db *sql.DB) error {
	var columnCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('servers') WHERE name = 'expires_at'`,
	).Scan(&columnCount); err != nil {
		return fmt.Errorf("inspect server expires_at column: %w", err)
	}
	if columnCount == 0 {
		if _, err := db.ExecContext(ctx, `ALTER TABLE servers ADD COLUMN expires_at INTEGER`); err != nil {
			return fmt.Errorf("add server expires_at column: %w", err)
		}
	}
	return nil
}

func migrateAgentLastSeen(ctx context.Context, db *sql.DB) error {
	var columnCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name = 'last_seen_at'`,
	).Scan(&columnCount); err != nil {
		return fmt.Errorf("inspect Agent last_seen_at column: %w", err)
	}
	if columnCount == 0 {
		if _, err := db.ExecContext(ctx, `ALTER TABLE agents ADD COLUMN last_seen_at INTEGER`); err != nil {
			return fmt.Errorf("add Agent last_seen_at column: %w", err)
		}
	}
	return nil
}

func migrateAgentEnrollmentPurpose(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Agent enrollment purpose migration: %w", err)
	}
	defer tx.Rollback()

	var purposeColumnCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('agent_enrollments') WHERE name = 'purpose'`,
	).Scan(&purposeColumnCount); err != nil {
		return fmt.Errorf("inspect Agent enrollment purpose column: %w", err)
	}
	if purposeColumnCount != 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`ALTER TABLE agent_enrollments ADD COLUMN purpose TEXT NOT NULL DEFAULT 'initial'
		 CHECK (purpose IN ('initial', 'rebind'))`,
	); err != nil {
		return fmt.Errorf("add Agent enrollment purpose column: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agent_enrollments SET purpose = 'rebind'
		 WHERE used_at IS NULL
		 AND server_id IN (SELECT id FROM servers WHERE archived_at IS NOT NULL)`,
	); err != nil {
		return fmt.Errorf("migrate Agent enrollment purposes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Agent enrollment purpose migration: %w", err)
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
