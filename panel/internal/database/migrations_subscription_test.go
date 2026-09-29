package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
	subscriptionstore "github.com/renaissance0721/vps-panel/panel/internal/subscription"
)

func TestMigrateSubscriptionPlanTitle(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE subscription_plans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscription_plans (name) VALUES ('Legacy')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateSubscriptionPlanTitle(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}
	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscription_plans')
		WHERE name = 'subscription_title'`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	var name string
	var title sql.NullString
	if err := db.QueryRow(`SELECT name, subscription_title FROM subscription_plans WHERE id = 1`).Scan(&name, &title); err != nil {
		t.Fatal(err)
	}
	if columnCount != 1 || name != "Legacy" || title.Valid {
		t.Fatalf("migrated plan = columns %d, name %q, title %+v", columnCount, name, title)
	}
}

func TestMigrateSubscriptionSourceProxyToServer(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE servers (id INTEGER PRIMARY KEY, name TEXT NOT NULL, created_by_role TEXT NOT NULL)`,
		`CREATE TABLE proxies (
			id INTEGER PRIMARY KEY, server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			name TEXT NOT NULL, listen_port INTEGER NOT NULL,
			entry_host_mode TEXT NOT NULL, entry_host TEXT NOT NULL)`,
		`CREATE TABLE relays (
			id INTEGER PRIMARY KEY, server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			target_proxy_id INTEGER REFERENCES proxies(id) ON DELETE RESTRICT,
			listen_port INTEGER NOT NULL, entry_host_mode TEXT NOT NULL, entry_host TEXT NOT NULL)`,
		`CREATE TABLE server_system_info (server_id INTEGER PRIMARY KEY, public_ipv4 TEXT NOT NULL)`,
		`CREATE TABLE subscription_published_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			mode TEXT NOT NULL CHECK (mode IN ('direct', 'relay')),
			target_proxy_id INTEGER NOT NULL REFERENCES proxies(id) ON DELETE RESTRICT,
			source_proxy_id INTEGER REFERENCES proxies(id) ON DELETE RESTRICT,
			relay_id INTEGER REFERENCES relays(id) ON DELETE SET NULL,
			traffic_multiplier_bp INTEGER NOT NULL DEFAULT 100,
			enabled INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK ((mode = 'direct' AND source_proxy_id IS NULL AND relay_id IS NULL)
				OR (mode = 'relay' AND source_proxy_id IS NOT NULL AND relay_id IS NOT NULL)))`,
		`CREATE TABLE subscription_plans (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE subscription_plan_nodes (
			plan_id INTEGER NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
			published_node_id INTEGER NOT NULL REFERENCES subscription_published_nodes(id) ON DELETE RESTRICT,
			position INTEGER NOT NULL, PRIMARY KEY (plan_id, published_node_id))`,
		`CREATE TABLE clients (
			id INTEGER PRIMARY KEY, proxy_id INTEGER NOT NULL REFERENCES proxies(id) ON DELETE CASCADE)`,
		`CREATE TABLE subscriber_clients (
			user_id INTEGER NOT NULL, proxy_id INTEGER NOT NULL REFERENCES proxies(id) ON DELETE RESTRICT,
			client_id INTEGER NOT NULL UNIQUE REFERENCES clients(id) ON DELETE CASCADE,
			created_at INTEGER NOT NULL, PRIMARY KEY (user_id, proxy_id))`,
		`INSERT INTO servers VALUES (1, 'Source', 'admin'), (2, 'Target', 'admin')`,
		`INSERT INTO proxies VALUES
			(10, 1, 'Legacy Source Proxy', 8443, 'manual', 'source.example.com'),
			(20, 2, 'Target Proxy', 443, 'auto', '')`,
		`INSERT INTO relays VALUES (30, 1, 20, 20000, 'auto', '')`,
		`INSERT INTO server_system_info VALUES (1, '198.51.100.10')`,
		`INSERT INTO subscription_published_nodes VALUES
			(40, 'Legacy Relay', 'relay', 20, 10, 30, 125, 1, 1, 1)`,
		`INSERT INTO subscription_plans VALUES (50)`,
		`INSERT INTO subscription_plan_nodes VALUES (50, 40, 1)`,
		`INSERT INTO clients VALUES (60, 20)`,
		`INSERT INTO subscriber_clients VALUES (70, 20, 60, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := migrateSubscriptionSourceServer(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := migrateSubscriptionEndpoint(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP INDEX idx_subscription_published_nodes_source_server`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSubscriptionSourceServer(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var sourceServerID, targetProxyID, relayID, multiplier int64
	if err := db.QueryRow(`SELECT source_server_id, target_proxy_id, relay_id, traffic_multiplier_bp
		FROM subscription_published_nodes WHERE id = 40`).
		Scan(&sourceServerID, &targetProxyID, &relayID, &multiplier); err != nil {
		t.Fatal(err)
	}
	if sourceServerID != 1 || targetProxyID != 20 || relayID != 30 || multiplier != 125 {
		t.Fatalf("migrated published node = source server %d target %d relay %d multiplier %d",
			sourceServerID, targetProxyID, relayID, multiplier)
	}
	legacyNode, err := subscriptionstore.NewService(db, relaystore.NewService(db)).GetPublishedNode(context.Background(), 40)
	if err != nil {
		t.Fatal(err)
	}
	if legacyNode.SourceServerID == nil || *legacyNode.SourceServerID != 1 ||
		legacyNode.SourceServerName != "Source" || legacyNode.EntryAddress != "198.51.100.10" || legacyNode.EntryPort != 20000 {
		t.Fatalf("migrated published node service result = %+v", legacyNode)
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM pragma_table_info('subscription_published_nodes') WHERE name = 'source_proxy_id'`:               0,
		`SELECT COUNT(*) FROM pragma_table_info('subscription_published_nodes') WHERE name = 'source_server_id'`:              1,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_subscription_published_nodes_source_server'`: 1,
		`SELECT COUNT(*) FROM subscription_plan_nodes WHERE plan_id = 50 AND published_node_id = 40`:                          1,
		`SELECT COUNT(*) FROM subscriber_clients WHERE user_id = 70 AND proxy_id = 20 AND client_id = 60`:                     1,
	} {
		var got int
		if err := db.QueryRow(query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("count for %s = %d, want %d", query, got, want)
		}
	}
	if _, err := db.Exec(`DELETE FROM proxies WHERE id = 10`); err != nil {
		t.Fatalf("delete former source proxy: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM proxies WHERE id = 20`); err == nil {
		t.Fatal("target proxy deletion unexpectedly succeeded")
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration left a foreign key violation")
	}
}

func TestOpenMigratesLegacySubscriptionSourceProxy(t *testing.T) {
	dataDir := t.TempDir()
	legacyDB, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	legacyDB.SetMaxOpenConns(1)
	if _, err := legacyDB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range schemaStatements() {
		if strings.Contains(statement, "CREATE TABLE IF NOT EXISTS subscription_published_nodes") {
			statement = `CREATE TABLE subscription_published_nodes (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				mode TEXT NOT NULL CHECK (mode IN ('direct', 'relay')),
				target_proxy_id INTEGER NOT NULL REFERENCES proxies(id) ON DELETE RESTRICT,
				source_proxy_id INTEGER REFERENCES proxies(id) ON DELETE RESTRICT,
				relay_id INTEGER REFERENCES relays(id) ON DELETE SET NULL,
				traffic_multiplier_bp INTEGER NOT NULL DEFAULT 100
					CHECK (traffic_multiplier_bp BETWEEN 10 AND 500),
				enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL,
				CHECK (
					(mode = 'direct' AND source_proxy_id IS NULL AND relay_id IS NULL)
					OR
					(mode = 'relay' AND source_proxy_id IS NOT NULL AND relay_id IS NOT NULL)
				)
			)`
		}
		if _, err := legacyDB.Exec(statement); err != nil {
			legacyDB.Close()
			t.Fatalf("create legacy database: %v", err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO servers (id, name, created_by_role, status, created_at, updated_at)
		 VALUES (1, 'Source', 'admin', 'online', 1, 1), (2, 'Target', 'admin', 'online', 1, 1)`,
		`INSERT INTO proxies (id, server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		 VALUES (10, 1, 'Legacy Source Proxy', 'vless', 443, '{}', 1, 1),
		        (20, 2, 'Target Proxy', 'shadowsocks', 8443, '{}', 1, 1)`,
		`INSERT INTO relays
		 (id, server_id, name, listen_port, entry_host_mode, entry_host,
		  target_type, target_proxy_id, network, created_at, updated_at)
		 VALUES (30, 1, 'Legacy Realm', 20000, 'manual', 'legacy.example.com', 'proxy', 20, 'tcp', 1, 1)`,
		`INSERT INTO subscription_published_nodes
		 (id, name, mode, target_proxy_id, source_proxy_id, relay_id, traffic_multiplier_bp, enabled, created_at, updated_at)
		 VALUES (40, 'Legacy Relay', 'relay', 20, 10, 30, 100, 1, 1, 1),
		        (41, 'Legacy Direct', 'direct', 20, NULL, NULL, 100, 1, 1, 1)`,
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
		t.Fatalf("Open() legacy subscription database: %v", err)
	}
	var relaySourceServerID, directSourceServerID sql.NullInt64
	var relayHostMode, relayHost, relayPortMode, directHostMode, directHost, directPortMode string
	if err := db.QueryRow(`SELECT source_server_id, entry_host_mode, entry_host, entry_port_mode
		FROM subscription_published_nodes WHERE id = 40`).
		Scan(&relaySourceServerID, &relayHostMode, &relayHost, &relayPortMode); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT source_server_id, entry_host_mode, entry_host, entry_port_mode
		FROM subscription_published_nodes WHERE id = 41`).
		Scan(&directSourceServerID, &directHostMode, &directHost, &directPortMode); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM pragma_table_info('subscription_published_nodes') WHERE name = 'source_proxy_id'`:               0,
		`SELECT COUNT(*) FROM pragma_table_info('subscription_published_nodes') WHERE name = 'source_server_id'`:              1,
		`SELECT COUNT(*) FROM pragma_table_info('subscription_published_nodes') WHERE name = 'entry_host_mode'`:               1,
		`SELECT COUNT(*) FROM pragma_table_info('subscription_published_nodes') WHERE name = 'entry_host'`:                    1,
		`SELECT COUNT(*) FROM pragma_table_info('subscription_published_nodes') WHERE name = 'entry_port_mode'`:               1,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_subscription_published_nodes_source_server'`: 1,
	} {
		var got int
		if err := db.QueryRow(query).Scan(&got); err != nil {
			db.Close()
			t.Fatal(err)
		}
		if got != want {
			db.Close()
			t.Fatalf("count for %s = %d, want %d", query, got, want)
		}
	}
	if !relaySourceServerID.Valid || relaySourceServerID.Int64 != 1 || directSourceServerID.Valid {
		db.Close()
		t.Fatalf("migrated source servers = relay %v, direct %v", relaySourceServerID, directSourceServerID)
	}
	if relayHostMode != "manual" || relayHost != "legacy.example.com" || relayPortMode != "auto" ||
		directHostMode != "inherit" || directHost != "" || directPortMode != "inherit" {
		db.Close()
		t.Fatalf("migrated endpoints = relay %q/%q/%q, direct %q/%q/%q",
			relayHostMode, relayHost, relayPortMode, directHostMode, directHost, directPortMode)
	}
	migratedNode, err := subscriptionstore.NewService(db, relaystore.NewService(db)).GetPublishedNode(t.Context(), 40)
	if err != nil || migratedNode.EntryAddress != "legacy.example.com" || migratedNode.EntryPort != 20000 {
		db.Close()
		t.Fatalf("migrated legacy endpoint = %+v, error = %v", migratedNode, err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if rows.Next() {
		rows.Close()
		db.Close()
		t.Fatal("migration left a foreign key violation")
	}
	if err := rows.Close(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(dataDir)
	if err != nil {
		t.Fatalf("Open() migrated subscription database again: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateSubscriberLifecycleAndTrafficCharging(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE subscription_plans (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL, subscription_title TEXT, enabled INTEGER NOT NULL,
			traffic_limit_bytes INTEGER, traffic_reset_mode TEXT NOT NULL, traffic_reset_day INTEGER NOT NULL,
			traffic_reset_time TEXT NOT NULL, default_validity_days INTEGER, billing_period_months INTEGER,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE TABLE subscriber_profiles (
			user_id INTEGER PRIMARY KEY, plan_id INTEGER, enabled INTEGER NOT NULL, expires_at INTEGER,
			subscription_token TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE TABLE subscription_published_nodes (
			id INTEGER PRIMARY KEY, name TEXT NOT NULL, mode TEXT NOT NULL, target_proxy_id INTEGER NOT NULL,
			source_proxy_id INTEGER, relay_id INTEGER, enabled INTEGER NOT NULL, created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL)`,
		`CREATE TABLE subscriber_clients (
			user_id INTEGER NOT NULL, proxy_id INTEGER NOT NULL, client_id INTEGER NOT NULL UNIQUE,
			created_at INTEGER NOT NULL, PRIMARY KEY (user_id, proxy_id))`,
		`CREATE TABLE client_metrics (
			client_id INTEGER PRIMARY KEY, cycle_uplink_bytes INTEGER NOT NULL,
			cycle_downlink_bytes INTEGER NOT NULL)`,
		`INSERT INTO subscription_plans VALUES
			(1, 'Legacy', NULL, 1, 100, 'monthly', 5, '03:00', 30, 3, 1, 1)`,
		`INSERT INTO subscriber_profiles VALUES
			(10, 1, 1, NULL, 'a', 1, 1), (11, NULL, 1, NULL, 'b', 1, 1)`,
		`INSERT INTO subscription_published_nodes VALUES
			(1, 'Node', 'direct', 20, NULL, NULL, 1, 1, 1)`,
		`INSERT INTO subscriber_clients VALUES (10, 20, 30, 1)`,
		`INSERT INTO client_metrics VALUES (30, 300, 700)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := migrateSubscriberLifecycle(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := migrateSubscriptionTrafficCharging(ctx, db); err != nil {
		t.Fatal(err)
	}

	var mode, resetTime string
	var day int
	var billing sql.NullInt64
	if err := db.QueryRow(`SELECT traffic_reset_mode, traffic_reset_day, traffic_reset_time,
		billing_period_months FROM subscriber_profiles WHERE user_id = 10`).
		Scan(&mode, &day, &resetTime, &billing); err != nil {
		t.Fatal(err)
	}
	if mode != "monthly" || day != 5 || resetTime != "03:00" || !billing.Valid || billing.Int64 != 3 {
		t.Fatalf("migrated planned subscriber lifecycle = %q/%d/%q/%v", mode, day, resetTime, billing)
	}
	if err := db.QueryRow(`SELECT traffic_reset_mode, traffic_reset_day, traffic_reset_time,
		billing_period_months FROM subscriber_profiles WHERE user_id = 11`).
		Scan(&mode, &day, &resetTime, &billing); err != nil {
		t.Fatal(err)
	}
	if mode != "never" || day != 1 || resetTime != "00:00" || billing.Valid {
		t.Fatalf("migrated unplanned subscriber lifecycle = %q/%d/%q/%v", mode, day, resetTime, billing)
	}
	var multiplier, chargedUplink, chargedDownlink, uplinkRemainder, downlinkRemainder int64
	if err := db.QueryRow(`SELECT traffic_multiplier_bp FROM subscription_published_nodes WHERE id = 1`).Scan(&multiplier); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT charged_uplink_bytes, charged_downlink_bytes,
		charge_uplink_remainder, charge_downlink_remainder FROM subscriber_clients WHERE client_id = 30`).
		Scan(&chargedUplink, &chargedDownlink, &uplinkRemainder, &downlinkRemainder); err != nil {
		t.Fatal(err)
	}
	if multiplier != 100 || chargedUplink != 300 || chargedDownlink != 700 || uplinkRemainder != 0 || downlinkRemainder != 0 {
		t.Fatalf("migrated charging state = multiplier %d charged %d/%d remainder %d/%d",
			multiplier, chargedUplink, chargedDownlink, uplinkRemainder, downlinkRemainder)
	}
	for _, removed := range []string{"traffic_reset_mode", "traffic_reset_day", "traffic_reset_time", "default_validity_days", "billing_period_months"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscription_plans') WHERE name = ?`, removed).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("legacy plan column %s still exists", removed)
		}
	}

	if _, err := db.Exec(`UPDATE subscriber_profiles SET traffic_reset_day = 9,
		billing_period_months = 12 WHERE user_id = 10`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE subscriber_clients SET charged_uplink_bytes = 777 WHERE client_id = 30`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE client_metrics SET cycle_uplink_bytes = 999 WHERE client_id = 30`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSubscriberLifecycle(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := migrateSubscriptionTrafficCharging(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT traffic_reset_day, billing_period_months FROM subscriber_profiles WHERE user_id = 10`).
		Scan(&day, &billing); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT charged_uplink_bytes FROM subscriber_clients WHERE client_id = 30`).Scan(&chargedUplink); err != nil {
		t.Fatal(err)
	}
	if day != 9 || !billing.Valid || billing.Int64 != 12 || chargedUplink != 777 {
		t.Fatalf("repeated migration overwrote state = day %d billing %v charged %d", day, billing, chargedUplink)
	}
}

func TestMigrateSubscriptionTrafficChargingRollsBackBackfillFailure(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE subscription_published_nodes (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE subscriber_clients (user_id INTEGER, proxy_id INTEGER, client_id INTEGER PRIMARY KEY)`,
		`CREATE TABLE client_metrics (client_id INTEGER PRIMARY KEY, cycle_uplink_bytes INTEGER, cycle_downlink_bytes INTEGER)`,
		`INSERT INTO subscriber_clients VALUES (1, 2, 3)`,
		`INSERT INTO client_metrics VALUES (3, 4, 5)`,
		`CREATE TRIGGER reject_charge_backfill BEFORE UPDATE ON subscriber_clients
		 BEGIN SELECT RAISE(ABORT, 'reject backfill'); END`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateSubscriptionTrafficCharging(context.Background(), db); err == nil {
		t.Fatal("expected charging migration failure")
	}
	for _, column := range []string{"charged_uplink_bytes", "charged_downlink_bytes", "charge_uplink_remainder", "charge_downlink_remainder"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscriber_clients') WHERE name = ?`, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("partial column %s remained after rollback", column)
		}
	}
}
