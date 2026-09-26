package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func migrate(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	statements := schemaStatements()

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
	if err := migrateServerDecommission(ctx, db); err != nil {
		return err
	}
	if err := migrateServerOwner(ctx, db); err != nil {
		return err
	}
	if err := migrateServerAccess(ctx, db); err != nil {
		return err
	}
	if err := migrateServerOutboundPreference(ctx, db); err != nil {
		return err
	}
	if err := migrateServerBlockChinaInbound(ctx, db); err != nil {
		return err
	}
	if err := migrateServerExpiration(ctx, db); err != nil {
		return err
	}
	if err := migrateServerRenewal(ctx, db); err != nil {
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
	if err := migrateAgentUpgrade(ctx, db); err != nil {
		return err
	}
	if err := migrateAgentMetadata(ctx, db); err != nil {
		return err
	}
	if err := migrateServerPublicIPv4(ctx, db); err != nil {
		return err
	}
	if err := migrateProxyEntryHost(ctx, db); err != nil {
		return err
	}
	if err := migrateRelayEntryHost(ctx, db); err != nil {
		return err
	}
	if err := migrateRelayTargetClient(ctx, db); err != nil {
		return err
	}
	if err := migrateRelayLandingTarget(ctx, db); err != nil {
		return err
	}
	if err := migrateProxyProtocols(ctx, db); err != nil {
		return err
	}
	if err := migrateClientTrafficConfig(ctx, db); err != nil {
		return err
	}
	if err := migrateClientLifecycle(ctx, db); err != nil {
		return err
	}

	return nil
}

func migrateServerOwner(ctx context.Context, db *sql.DB) error {
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('servers') WHERE name = 'owner_user_id'`,
	).Scan(&count); err != nil {
		return fmt.Errorf("inspect servers.owner_user_id: %w", err)
	}
	if count == 0 {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE servers ADD COLUMN owner_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL`,
		); err != nil {
			return fmt.Errorf("add servers.owner_user_id: %w", err)
		}
	}
	return nil
}

func migrateRelayLandingTarget(ctx context.Context, db *sql.DB) error {
	var tableSQL string
	if err := db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'relays'`,
	).Scan(&tableSQL); err != nil {
		return fmt.Errorf("inspect relays table: %w", err)
	}
	var columnCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('relays') WHERE name = 'target_landing_id'`,
	).Scan(&columnCount); err != nil {
		return fmt.Errorf("inspect relays.target_landing_id: %w", err)
	}
	if columnCount != 0 && strings.Contains(strings.ToLower(tableSQL), "'landing'") {
		if _, err := db.ExecContext(ctx,
			`CREATE INDEX IF NOT EXISTS idx_relays_target_landing_id ON relays(target_landing_id)`,
		); err != nil {
			return fmt.Errorf("create relays target landing index: %w", err)
		}
		return nil
	}

	connection, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open relay landing migration connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for relay landing migration: %w", err)
	}
	defer connection.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`)

	tx, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin relay landing migration: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE relays_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			listen_address TEXT NOT NULL DEFAULT '0.0.0.0',
			listen_port INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
			entry_host_mode TEXT NOT NULL DEFAULT 'auto'
				CHECK (entry_host_mode IN ('auto', 'manual')),
			entry_host TEXT NOT NULL DEFAULT '',
			target_type TEXT NOT NULL CHECK (target_type IN ('proxy', 'landing', 'manual')),
			target_proxy_id INTEGER REFERENCES proxies(id) ON DELETE RESTRICT,
			target_client_id INTEGER NULL REFERENCES clients(id) ON DELETE SET NULL,
			target_landing_id INTEGER NULL REFERENCES landing_nodes(id) ON DELETE RESTRICT,
			target_host TEXT NOT NULL DEFAULT '',
			target_port INTEGER CHECK (target_port BETWEEN 1 AND 65535),
			network TEXT NOT NULL CHECK (network IN ('tcp', 'udp', 'tcp,udp')),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK (
				(target_type = 'proxy' AND target_proxy_id IS NOT NULL AND target_landing_id IS NULL AND target_host = '' AND target_port IS NULL)
				OR
				(target_type = 'landing' AND target_proxy_id IS NULL AND target_client_id IS NULL AND target_landing_id IS NOT NULL AND target_host = '' AND target_port IS NULL)
				OR
				(target_type = 'manual' AND target_proxy_id IS NULL AND target_client_id IS NULL AND target_landing_id IS NULL AND target_host != '' AND target_port IS NOT NULL)
			)
		)`,
		`INSERT INTO relays_new
			(id, server_id, name, listen_address, listen_port, entry_host_mode, entry_host,
			 target_type, target_proxy_id, target_client_id, target_landing_id, target_host,
			 target_port, network, enabled, created_at, updated_at)
		 SELECT id, server_id, name, listen_address, listen_port, entry_host_mode, entry_host,
			 target_type, target_proxy_id, target_client_id, NULL, target_host,
			 target_port, network, enabled, created_at, updated_at FROM relays`,
		`DROP TABLE relays`,
		`ALTER TABLE relays_new RENAME TO relays`,
		`CREATE INDEX idx_relays_server_id ON relays(server_id)`,
		`CREATE INDEX idx_relays_target_proxy_id ON relays(target_proxy_id)`,
		`CREATE INDEX idx_relays_target_landing_id ON relays(target_landing_id)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate relay landing target: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit relay landing migration: %w", err)
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("restore foreign keys after relay landing migration: %w", err)
	}
	rows, err := connection.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check relay landing foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("relay landing migration left invalid foreign keys")
	}
	return rows.Err()
}

func migrateAgentMetadata(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"implementation", "implementation TEXT NOT NULL DEFAULT ''"},
		{"api_version", "api_version INTEGER NOT NULL DEFAULT 0"},
		{"capabilities_json", "capabilities_json TEXT NOT NULL DEFAULT '[]'"},
	}
	for _, column := range columns {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name = ?`, column.name,
		).Scan(&count); err != nil {
			return fmt.Errorf("inspect agents.%s column: %w", column.name, err)
		}
		if count != 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE agents ADD COLUMN "+column.definition); err != nil {
			return fmt.Errorf("add agents.%s column: %w", column.name, err)
		}
	}
	return nil
}

func migrateRelayTargetClient(ctx context.Context, db *sql.DB) error {
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('relays') WHERE name = 'target_client_id'`,
	).Scan(&count); err != nil {
		return fmt.Errorf("inspect relays.target_client_id: %w", err)
	}
	if count == 0 {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE relays ADD COLUMN target_client_id INTEGER NULL REFERENCES clients(id) ON DELETE SET NULL`,
		); err != nil {
			return fmt.Errorf("add relays.target_client_id: %w", err)
		}
	}
	return nil
}

func migrateServerAccess(ctx context.Context, db *sql.DB) error {
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('servers') WHERE name = 'visibility'`,
	).Scan(&count); err != nil {
		return fmt.Errorf("inspect servers.visibility: %w", err)
	}
	if count == 0 {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE servers ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public'
			 CHECK (visibility IN ('public', 'private'))`,
		); err != nil {
			return fmt.Errorf("add servers.visibility: %w", err)
		}
	}
	return nil
}

func migrateServerOutboundPreference(ctx context.Context, db *sql.DB) error {
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('servers') WHERE name = 'outbound_preference'`,
	).Scan(&count); err != nil {
		return fmt.Errorf("inspect servers.outbound_preference: %w", err)
	}
	if count == 0 {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE servers ADD COLUMN outbound_preference TEXT NOT NULL DEFAULT 'auto'
			 CHECK (outbound_preference IN ('auto', 'prefer_ipv4', 'prefer_ipv6'))`,
		); err != nil {
			return fmt.Errorf("add servers.outbound_preference: %w", err)
		}
	}
	return nil
}

func migrateServerBlockChinaInbound(ctx context.Context, db *sql.DB) error {
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('servers') WHERE name = 'block_china_inbound'`,
	).Scan(&count); err != nil {
		return fmt.Errorf("inspect servers.block_china_inbound: %w", err)
	}
	if count == 0 {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE servers ADD COLUMN block_china_inbound INTEGER NOT NULL DEFAULT 0
			 CHECK (block_china_inbound IN (0, 1))`,
		); err != nil {
			return fmt.Errorf("add servers.block_china_inbound: %w", err)
		}
	}
	return nil
}

func migrateClientLifecycle(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"expires_at", "expires_at INTEGER"},
		{"effective_enabled_snapshot", "effective_enabled_snapshot INTEGER NOT NULL DEFAULT 1 CHECK (effective_enabled_snapshot IN (0, 1))"},
	}
	for _, column := range columns {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('clients') WHERE name = ?`, column.name,
		).Scan(&count); err != nil {
			return fmt.Errorf("inspect clients.%s column: %w", column.name, err)
		}
		if count != 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE clients ADD COLUMN "+column.definition); err != nil {
			return fmt.Errorf("add clients.%s column: %w", column.name, err)
		}
	}
	return nil
}

func migrateAgentUpgrade(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"upgrade_target_version", "upgrade_target_version TEXT NOT NULL DEFAULT ''"},
		{"upgrade_status", "upgrade_status TEXT NOT NULL DEFAULT '' CHECK (upgrade_status IN ('', 'upgrading', 'failed'))"},
		{"upgrade_error", "upgrade_error TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name = ?`, column.name,
		).Scan(&count); err != nil {
			return fmt.Errorf("inspect agents.%s column: %w", column.name, err)
		}
		if count != 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE agents ADD COLUMN "+column.definition); err != nil {
			return fmt.Errorf("add agents.%s column: %w", column.name, err)
		}
	}
	return nil
}

func migrateClientTrafficConfig(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"traffic_limit_bytes", "traffic_limit_bytes INTEGER CHECK (traffic_limit_bytes >= 0)"},
		{"traffic_reset_mode", "traffic_reset_mode TEXT NOT NULL DEFAULT 'never' CHECK (traffic_reset_mode IN ('never', 'daily', 'weekly', 'monthly'))"},
		{"traffic_reset_weekday", "traffic_reset_weekday INTEGER NOT NULL DEFAULT 1 CHECK (traffic_reset_weekday BETWEEN 1 AND 7)"},
		{"traffic_reset_day", "traffic_reset_day INTEGER NOT NULL DEFAULT 1 CHECK (traffic_reset_day BETWEEN 1 AND 31)"},
		{"traffic_reset_time", "traffic_reset_time TEXT NOT NULL DEFAULT '00:00'"},
	}
	for _, column := range columns {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('clients') WHERE name = ?`, column.name,
		).Scan(&count); err != nil {
			return fmt.Errorf("inspect clients.%s column: %w", column.name, err)
		}
		if count != 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE clients ADD COLUMN "+column.definition); err != nil {
			return fmt.Errorf("add clients.%s column: %w", column.name, err)
		}
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

func migrateRelayEntryHost(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"entry_host_mode", "entry_host_mode TEXT NOT NULL DEFAULT 'auto' CHECK (entry_host_mode IN ('auto', 'manual'))"},
		{"entry_host", "entry_host TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('relays') WHERE name = ?`, column.name,
		).Scan(&count); err != nil {
			return fmt.Errorf("inspect relays.%s: %w", column.name, err)
		}
		if count != 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE relays ADD COLUMN "+column.definition); err != nil {
			return fmt.Errorf("add relays.%s: %w", column.name, err)
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

func migrateServerRenewal(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"renewal_period_months", "renewal_period_months INTEGER CHECK (renewal_period_months IS NULL OR renewal_period_months IN (1, 3, 6, 12, 24, 36))"},
		{"auto_renew", "auto_renew INTEGER NOT NULL DEFAULT 0 CHECK (auto_renew IN (0, 1))"},
		{"renewal_anchor_day", "renewal_anchor_day INTEGER CHECK (renewal_anchor_day IS NULL OR renewal_anchor_day BETWEEN 1 AND 31)"},
	}
	for _, column := range columns {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('servers') WHERE name = ?`, column.name,
		).Scan(&count); err != nil {
			return fmt.Errorf("inspect servers.%s column: %w", column.name, err)
		}
		if count != 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE servers ADD COLUMN "+column.definition); err != nil {
			return fmt.Errorf("add servers.%s column: %w", column.name, err)
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

func migrateServerDecommission(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"decommissioning_at", "decommissioning_at INTEGER"},
		{"decommission_status", "decommission_status TEXT NOT NULL DEFAULT '' CHECK (decommission_status IN ('', 'pending', 'failed'))"},
		{"decommission_error", "decommission_error TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range columns {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info('servers') WHERE name = ?`, column.name,
		).Scan(&count); err != nil {
			return fmt.Errorf("inspect servers.%s: %w", column.name, err)
		}
		if count != 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE servers ADD COLUMN "+column.definition); err != nil {
			return fmt.Errorf("add servers.%s: %w", column.name, err)
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
