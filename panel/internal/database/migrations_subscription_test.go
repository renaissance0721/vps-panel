package database

import (
	"context"
	"database/sql"
	"testing"
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
