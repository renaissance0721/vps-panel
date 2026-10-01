package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMigrationSixToSevenCreatesRuntimeRoutingProfiles(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "panel.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:5] {
		if err := applyMigration(context.Background(), db, item); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	oldGroups := `[{"id":"default","name":"Default","type":"select","members":[{"type":"published_node","published_node_id":7},{"type":"direct"}]},{"id":"stream","name":"Streaming","type":"select","members":[{"type":"group","group_id":"default"}]}]`
	oldRules := `[{"type":"DOMAIN-SUFFIX","value":"example.com","target_group_id":"stream"},{"type":"MATCH","target_group_id":"default"}]`
	if _, err := db.Exec(`INSERT INTO subscription_routing_presets
		(id, name, enabled, groups_json, rules_json, created_at, updated_at) VALUES (1, 'Legacy', 1, ?, ?, 1, 1)`, oldGroups, oldRules); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscription_plans
		(id, name, enabled, routing_preset_id, created_at, updated_at) VALUES (2, 'Plan', 1, 1, 1, 1)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := applyMigration(context.Background(), db, migrations[5]); err != nil {
		db.Close()
		t.Fatal(err)
	}
	customTemplate := `dns:
  enable: false
proxies: []
rule-providers:
  Custom:
    type: http
    behavior: classical
    format: yaml
    interval: 86400
    url: https://example.com/custom.yaml
proxy-groups:
  - name: Default
    type: select
    proxies: [DIRECT]
rules:
  - RULE-SET,Custom,Default
  - MATCH,Default`
	if _, err := db.Exec(`INSERT INTO subscription_templates
		(id, name, enabled, config_yaml, created_at, updated_at) VALUES (3, 'Custom', 1, ?, 1, 1)`, customTemplate); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE subscription_plans SET template_id = 3,
		routing_rules_json = '["RULE-SET,Custom,Default","MATCH,Default"]' WHERE id = 2`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscription_plans
		(id, name, enabled, routing_groups_json, routing_rules_json, created_at, updated_at)
		VALUES (4, 'Default Plan', 1, '[]', '[]', 1, 1)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := applyMigration(context.Background(), db, migrations[6]); err != nil {
		db.Close()
		t.Fatal(err)
	}
	defer db.Close()

	var defaultID int64
	var defaultName string
	var defaultEnabled int
	var defaultGroups, defaultProviders, defaultRules string
	if err := db.QueryRow(`SELECT id, name, enabled, groups_json, rule_providers_yaml, rules_json
		FROM subscription_routing_presets WHERE is_default = 1`).
		Scan(&defaultID, &defaultName, &defaultEnabled, &defaultGroups, &defaultProviders, &defaultRules); err != nil {
		t.Fatal(err)
	}
	var groups []migratedRoutingGroup
	var rules []string
	var providers map[string]any
	if err := json.Unmarshal([]byte(defaultGroups), &groups); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(defaultRules), &rules); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(defaultProviders), &providers); err != nil {
		t.Fatal(err)
	}
	if defaultName != "默认分流" || defaultEnabled != 1 ||
		defaultGroups != defaultRoutingGroupsJSON || defaultProviders != defaultRoutingProvidersYAML ||
		defaultRules != defaultRoutingRulesJSON || len(groups) != 8 || len(providers) != 10 || len(rules) != 12 ||
		groups[0].Name != "🚀 默认代理" || rules[len(rules)-1] != "MATCH,🚀 默认代理" {
		t.Fatalf("default routing = name %q, enabled %d, groups %v, providers %v, rules %v",
			defaultName, defaultEnabled, groups, providers, rules)
	}

	var planRoutingID, defaultPlanRoutingID int64
	var planGroups, planRules, migratedProviders string
	if err := db.QueryRow(`SELECT routing_preset_id, routing_groups_json, routing_rules_json
		FROM subscription_plans WHERE id = 2`).Scan(&planRoutingID, &planGroups, &planRules); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT routing_preset_id FROM subscription_plans WHERE id = 4`).Scan(&defaultPlanRoutingID); err != nil {
		t.Fatal(err)
	}
	if planRoutingID == defaultID || defaultPlanRoutingID != defaultID || planGroups != "[]" || planRules != "[]" {
		t.Fatalf("migrated plan refs = custom %d, default %d, default id %d, legacy %s/%s",
			planRoutingID, defaultPlanRoutingID, defaultID, planGroups, planRules)
	}
	if err := db.QueryRow(`SELECT rule_providers_yaml FROM subscription_routing_presets WHERE id = ?`, planRoutingID).
		Scan(&migratedProviders); err != nil || !strings.Contains(migratedProviders, "Custom:") {
		t.Fatalf("migrated plan providers = %q, %v", migratedProviders, err)
	}
	var cleanedTemplate string
	if err := db.QueryRow(`SELECT config_yaml FROM subscription_templates WHERE id = 3`).Scan(&cleanedTemplate); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"proxies:", "proxy-groups:", "rule-providers:", "rules:"} {
		if strings.Contains(cleanedTemplate, forbidden) {
			t.Fatalf("migrated template still contains %q:\n%s", forbidden, cleanedTemplate)
		}
	}
	if !strings.Contains(cleanedTemplate, "dns:") {
		t.Fatalf("migrated template lost base config:\n%s", cleanedTemplate)
	}
	var existingProviders string
	if err := db.QueryRow(`SELECT rule_providers_yaml FROM subscription_routing_presets WHERE id = 1`).Scan(&existingProviders); err != nil ||
		!strings.Contains(existingProviders, "OpenAI:") {
		t.Fatalf("existing preset providers = %q, %v", existingProviders, err)
	}
	if _, err := db.Exec(`INSERT INTO subscription_routing_presets
		(name, enabled, groups_json, rules_json, rule_providers_yaml, is_default, created_at, updated_at)
		VALUES ('Another Default', 1, '[]', '[]', '{}', 1, 1, 1)`); err == nil {
		t.Fatal("second default routing preset unexpectedly inserted")
	}
	assertLatestMigrationHistory(t, db)
}

func TestMigrationSixRejectsCorruptRoutingJSONAtomically(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "panel.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:5] {
		if err := applyMigration(context.Background(), db, item); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO subscription_routing_presets
		(id, name, enabled, groups_json, rules_json, created_at, updated_at)
		VALUES (1, 'Broken', 1,
		'[{"id":"default","name":"Default","type":"select","members":[{"type":"direct"}],"unknown":true}]',
		'[{"type":"MATCH","target_group_id":"default"}]', 1, 1)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if migrated, err := Open(dataDir); err == nil {
		migrated.Close()
		t.Fatal("corrupt routing preset unexpectedly migrated")
	} else if !strings.Contains(err.Error(), `unknown field "unknown"`) {
		t.Fatalf("corrupt routing migration error = %v", err)
	}
	inspect, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer inspect.Close()
	var version, newColumns int
	if err := inspect.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := inspect.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscription_plans')
		WHERE name IN ('routing_groups_json', 'routing_rules_json')`).Scan(&newColumns); err != nil {
		t.Fatal(err)
	}
	if version != 5 || newColumns != 0 {
		t.Fatalf("failed migration state = version %d, new columns %d", version, newColumns)
	}
}

func TestMigrationSevenRejectsUnsafeTemplateAtomically(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, item := range migrations[:6] {
		if err := applyMigration(context.Background(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO subscription_templates
		(name, enabled, config_yaml, created_at, updated_at)
		VALUES ('Unsafe', 1, ?, 1, 1)`, "dns: &shared\n  enable: true\ntun: *shared"); err != nil {
		t.Fatal(err)
	}

	err = applyMigration(context.Background(), db, migrations[6])
	if err == nil || !strings.Contains(err.Error(), "safe mapping") {
		t.Fatalf("unsafe template migration error = %v", err)
	}
	var version, addedColumns int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscription_routing_presets')
		WHERE name IN ('rule_providers_yaml', 'is_default')`).Scan(&addedColumns); err != nil {
		t.Fatal(err)
	}
	if version != 6 || addedColumns != 0 {
		t.Fatalf("failed migration state = version %d, added columns %d", version, addedColumns)
	}
}

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
