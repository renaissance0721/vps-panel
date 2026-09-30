package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacySchemaFixturesBootstrapToLatestAndReopen(t *testing.T) {
	tests := []struct {
		name        string
		fixture     string
		replace     string
		seed        []string
		assertValue func(*testing.T, *sql.DB)
	}{
		{
			name: "subscriber", fixture: "legacy_before_subscriber.sql",
			replace: "CREATE TABLE IF NOT EXISTS subscriber_profiles",
			seed: []string{
				`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
				 VALUES (10, 'legacy-subscriber', 'hash', 'subscriber', 10, 11)`,
			},
			assertValue: func(t *testing.T, db *sql.DB) {
				var mode string
				if err := db.QueryRow(`SELECT traffic_reset_mode FROM subscriber_profiles WHERE user_id = 10`).Scan(&mode); err != nil || mode != "never" {
					t.Fatalf("migrated subscriber reset mode = %q, %v", mode, err)
				}
				var count int
				if err := db.QueryRow(`SELECT COUNT(*) FROM subscriber_usage WHERE user_id = 10`).Scan(&count); err != nil || count != 1 {
					t.Fatalf("subscriber usage rows = %d, %v", count, err)
				}
			},
		},
		{
			name: "source_server", fixture: "legacy_before_source_server.sql",
			replace: "CREATE TABLE IF NOT EXISTS subscription_published_nodes",
			seed:    sourceServerFixtureSeed(),
			assertValue: func(t *testing.T, db *sql.DB) {
				var sourceServerID int64
				if err := db.QueryRow(`SELECT source_server_id FROM subscription_published_nodes WHERE id = 40`).Scan(&sourceServerID); err != nil || sourceServerID != 1 {
					t.Fatalf("migrated source server = %d, %v", sourceServerID, err)
				}
				var oldColumn int
				if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscription_published_nodes') WHERE name = 'source_proxy_id'`).Scan(&oldColumn); err != nil || oldColumn != 0 {
					t.Fatalf("legacy source_proxy_id count = %d, %v", oldColumn, err)
				}
			},
		},
		{
			name: "endpoint", fixture: "legacy_before_endpoint.sql",
			replace: "CREATE TABLE IF NOT EXISTS subscription_published_nodes",
			seed: []string{
				`INSERT INTO servers (id, name, created_by_role, status, created_at, updated_at)
				 VALUES (1, 'Target', 'admin', 'online', 1, 1)`,
				`INSERT INTO proxies (id, server_id, name, protocol, listen_port, config_json, created_at, updated_at)
				 VALUES (10, 1, 'Target Proxy', 'vless', 443, '{}', 1, 1)`,
				`INSERT INTO subscription_published_nodes
				 (id, name, mode, target_proxy_id, source_server_id, relay_id, traffic_multiplier_bp, enabled, created_at, updated_at)
				 VALUES (40, 'Legacy Direct', 'direct', 10, NULL, NULL, 100, 1, 1, 1)`,
			},
			assertValue: func(t *testing.T, db *sql.DB) {
				var hostMode, host, portMode string
				if err := db.QueryRow(`SELECT entry_host_mode, entry_host, entry_port_mode
					FROM subscription_published_nodes WHERE id = 40`).Scan(&hostMode, &host, &portMode); err != nil {
					t.Fatal(err)
				}
				if hostMode != "inherit" || host != "" || portMode != "inherit" {
					t.Fatalf("migrated endpoint = %q/%q/%q", hostMode, host, portMode)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			prepareLegacyFixture(t, dataDir, test.fixture, test.replace, test.seed)
			db, err := Open(dataDir)
			if err != nil {
				t.Fatalf("open legacy fixture: %v", err)
			}
			defer db.Close()
			assertLatestMigrationHistory(t, db)
			test.assertValue(t, db)
			assertForeignKeysValid(t, db)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(dataDir)
			if err != nil {
				t.Fatalf("reopen migrated fixture: %v", err)
			}
			defer db.Close()
			assertLatestMigrationHistory(t, db)
			assertForeignKeysValid(t, db)
		})
	}
}

func TestListenerMigrationConflictIsExplicitAndAtomic(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "panel.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy.SetMaxOpenConns(1)
	for _, statement := range schemaStatements() {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO servers (id, name, created_by_role, status, created_at, updated_at)
		 VALUES (1, 'Server', 'admin', 'online', 1, 1)`,
		`INSERT INTO proxies (id, server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		 VALUES (10, 1, 'Proxy', 'vless', 443, '{}', 1, 1)`,
		`INSERT INTO relays (id, server_id, name, listen_port, target_type, target_host, target_port, network, created_at, updated_at)
		 VALUES (20, 1, 'Relay', 443, 'manual', 'example.com', 443, 'tcp', 1, 1)`,
	} {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	if db, err := Open(dataDir); err == nil {
		db.Close()
		t.Fatal("conflicting legacy listeners unexpectedly migrated")
	} else if !strings.Contains(err.Error(), "listener conflict on server 1 port 443") ||
		!strings.Contains(err.Error(), "proxy 10") || !strings.Contains(err.Error(), "relay 20") {
		t.Fatalf("listener migration error = %v", err)
	}
	inspect, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var history, reservations int
	if err := inspect.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if err := inspect.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'server_listener_reservations'`).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	if history != 1 || reservations != 0 {
		t.Fatalf("failed migration state = history %d, reservation tables %d", history, reservations)
	}
	if _, err := inspect.Exec(`UPDATE relays SET listen_port = 444 WHERE id = 20`); err != nil {
		t.Fatal(err)
	}
	if err := inspect.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("resume migration after conflict repair: %v", err)
	}
	defer db.Close()
	assertLatestMigrationHistory(t, db)
}

func prepareLegacyFixture(t *testing.T, dataDir, fixtureName, replace string, seed []string) {
	t.Helper()
	fixture, err := os.ReadFile(filepath.Join("testdata", "schema", fixtureName))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range schemaStatements() {
		if strings.Contains(statement, replace) {
			statement = string(fixture)
		}
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare %s: %v", fixtureName, err)
		}
	}
	for _, statement := range seed {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed %s: %v", fixtureName, err)
		}
	}
}

func sourceServerFixtureSeed() []string {
	return []string{
		`INSERT INTO servers (id, name, created_by_role, status, created_at, updated_at)
		 VALUES (1, 'Source', 'admin', 'online', 1, 1), (2, 'Target', 'admin', 'online', 1, 1)`,
		`INSERT INTO proxies (id, server_id, name, protocol, listen_port, config_json, created_at, updated_at)
		 VALUES (10, 1, 'Source Proxy', 'vless', 443, '{}', 1, 1),
		        (20, 2, 'Target Proxy', 'vless', 8443, '{}', 1, 1)`,
		`INSERT INTO relays
		 (id, server_id, name, listen_port, target_type, target_proxy_id, network, created_at, updated_at)
		 VALUES (30, 1, 'Relay', 20000, 'proxy', 20, 'tcp', 1, 1)`,
		`INSERT INTO subscription_published_nodes
		 (id, name, mode, target_proxy_id, source_proxy_id, relay_id, traffic_multiplier_bp, enabled, created_at, updated_at)
		 VALUES (40, 'Legacy Relay', 'relay', 20, 10, 30, 100, 1, 1, 1)`,
	}
}

func assertLatestMigrationHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	var count, latest int
	if err := db.QueryRow(`SELECT COUNT(*), MAX(version) FROM schema_migrations`).Scan(&count, &latest); err != nil {
		t.Fatal(err)
	}
	if count != LatestSchemaVersion || latest != LatestSchemaVersion {
		t.Fatalf("migration history count/latest = %d/%d, want %d", count, latest, LatestSchemaVersion)
	}
}

func assertForeignKeysValid(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check returned a violation")
	}
}
