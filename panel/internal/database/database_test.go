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
		"server_access",
		"user_server_order",
		"agent_enrollments",
		"agents",
		"server_system_info",
		"server_metrics",
		"proxies",
		"user_proxy_order",
		"relays",
		"user_relay_order",
		"clients",
		"client_metrics",
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
			"visibility", "desired_state_version", "monthly_traffic_limit_bytes", "traffic_count_mode", "traffic_reset_day", "traffic_reset_time",
		},
		"agents": {
			"applied_config_version", "config_sync_status", "config_sync_error", "config_synced_at",
			"upgrade_target_version", "upgrade_status", "upgrade_error",
		},
		"server_metrics": {
			"nic_rx_bytes", "nic_tx_bytes", "cycle_rx_bytes", "cycle_tx_bytes", "traffic_adjustment_bytes", "cycle_started_at",
		},
		"server_system_info": {"public_ipv4"},
		"proxies":            {"entry_host_mode", "entry_host"},
		"relays":             {"entry_host_mode", "entry_host"},
		"clients": {
			"expires_at", "effective_enabled_snapshot", "traffic_limit_bytes", "traffic_reset_mode",
			"traffic_reset_weekday", "traffic_reset_day", "traffic_reset_time",
		},
		"client_metrics": {
			"xray_uplink_bytes", "xray_downlink_bytes", "cycle_uplink_bytes", "cycle_downlink_bytes",
			"cycle_started_at", "last_activity_at", "updated_at",
		},
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
	if _, err := db.Exec(
		`INSERT INTO proxies
		 (id, server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		 VALUES (1, 1, 'proxy', 'vless', 443, '{}', 1, 1)`,
	); err != nil {
		t.Fatalf("insert proxy for client metrics: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO clients (id, proxy_id, name, credential_json, created_at, updated_at)
		 VALUES (1, 1, 'client', '{}', 1, 1)`,
	); err != nil {
		t.Fatalf("insert client for metrics: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO client_metrics
		 (client_id, xray_uplink_bytes, xray_downlink_bytes, cycle_started_at, updated_at)
		 VALUES (1, 10, 20, 1, 1)`,
	); err != nil {
		t.Fatalf("insert client metrics with defaults: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM clients WHERE id = 1`); err != nil {
		t.Fatalf("delete client with metrics: %v", err)
	}
	var clientMetricCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM client_metrics`).Scan(&clientMetricCount); err != nil {
		t.Fatalf("count cascaded client metrics: %v", err)
	}
	if clientMetricCount != 0 {
		t.Fatalf("client deletion left %d metrics rows", clientMetricCount)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("second migration error = %v", err)
	}
}
