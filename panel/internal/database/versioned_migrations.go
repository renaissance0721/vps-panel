package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const LatestSchemaVersion = 11

type migration struct {
	version int
	name    string
	up      func(context.Context, *sql.Tx) error
}

var migrations = []migration{
	{version: 1, name: "legacy_baseline", up: createBaselineSchema},
	{version: 2, name: "server_listener_reservations", up: createListenerReservations},
	{version: 3, name: "configuration_operations", up: createConfigurationOperations},
	{version: 4, name: "subscription_templates_and_routing_presets", up: createSubscriptionConfiguration},
	{version: 5, name: "audit_logs", up: createAuditLogs},
	{version: 6, name: "materialize_subscription_plan_routing", up: materializeSubscriptionPlanRouting},
	{version: 7, name: "routing_presets_as_runtime_profiles", up: migrateRoutingPresetsAsRuntimeProfiles},
	{version: 8, name: "personal_subscriptions", up: createPersonalSubscriptions},
	{version: 9, name: "personal_subscription_node_instances", up: migratePersonalSubscriptionNodeInstances},
	{version: 10, name: "routing_bindings", up: migrateRoutingBindings},
	{version: 11, name: "monitor_probes", up: createMonitorProbes},
}

func createMonitorProbes(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE monitor_probe_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL CHECK (type IN ('tcp', 'icmp')),
			target TEXT NOT NULL,
			port INTEGER,
			interval_seconds INTEGER NOT NULL CHECK (interval_seconds BETWEEN 5 AND 86400),
			enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			CHECK ((type = 'tcp' AND port IS NOT NULL AND port BETWEEN 1 AND 65535)
			    OR (type = 'icmp' AND port IS NULL))
		)`,
		`CREATE TABLE monitor_probe_servers (
			task_id INTEGER NOT NULL REFERENCES monitor_probe_tasks(id) ON DELETE CASCADE,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			PRIMARY KEY (task_id, server_id)
		)`,
		`CREATE INDEX idx_monitor_probe_servers_server ON monitor_probe_servers(server_id)`,
		`CREATE TABLE monitor_probe_records (
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			task_id INTEGER NOT NULL REFERENCES monitor_probe_tasks(id) ON DELETE CASCADE,
			ts INTEGER NOT NULL,
			outcome TEXT NOT NULL CHECK (outcome IN ('success', 'timeout', 'dns_error', 'connect_error', 'permission_error', 'cancelled')),
			latency_ms REAL,
			CHECK ((outcome = 'success' AND latency_ms IS NOT NULL AND latency_ms >= 0)
			    OR (outcome != 'success' AND latency_ms IS NULL))
		)`,
		`CREATE INDEX idx_monitor_probe_records_history ON monitor_probe_records(server_id, task_id, ts)`,
		`CREATE INDEX idx_monitor_probe_records_ts ON monitor_probe_records(ts)`,
		`CREATE INDEX idx_monitor_probe_records_task ON monitor_probe_records(task_id)`,
		`CREATE TRIGGER monitor_probe_server_archived AFTER UPDATE OF archived_at ON servers
		 WHEN NEW.archived_at IS NOT NULL
		 BEGIN
			DELETE FROM monitor_probe_servers WHERE server_id = NEW.id;
			DELETE FROM monitor_probe_records WHERE server_id = NEW.id;
		 END`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create monitor probes: %w", err)
		}
	}
	return nil
}

func migrateRoutingBindings(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE subscription_plans ADD COLUMN routing_bindings_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE personal_subscription_groups ADD COLUMN routing_bindings_json TEXT NOT NULL DEFAULT '{}'`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add subscription routing bindings: %w", err)
		}
	}

	type migratedPreset struct {
		id       int64
		groups   string
		bindings map[string][]int64
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, groups_json FROM subscription_routing_presets ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list routing presets for binding migration: %w", err)
	}
	presets := make([]migratedPreset, 0)
	for rows.Next() {
		var id int64
		var groupsJSON string
		if err := rows.Scan(&id, &groupsJSON); err != nil {
			rows.Close()
			return fmt.Errorf("scan routing preset for binding migration: %w", err)
		}
		var groups []migratedRoutingGroup
		if err := decodeMigrationJSON(groupsJSON, &groups); err != nil {
			rows.Close()
			return fmt.Errorf("decode routing preset %d groups for binding migration: %w", id, err)
		}
		seenKeys := make(map[string]struct{}, len(groups))
		bindings := make(map[string][]int64)
		for index := range groups {
			key := strings.TrimSpace(groups[index].Key)
			if _, duplicate := seenKeys[key]; key == "" || duplicate {
				key = migrationRoutingGroupKey(id, index+1, seenKeys)
			}
			groups[index].Key = key
			seenKeys[key] = struct{}{}
			if len(groups[index].NodeIDs) != 0 {
				bindings[key] = append([]int64(nil), groups[index].NodeIDs...)
			}
			groups[index].NodeIDs = nil
		}
		encoded, err := json.Marshal(groups)
		if err != nil {
			rows.Close()
			return fmt.Errorf("encode routing preset %d groups for binding migration: %w", id, err)
		}
		presets = append(presets, migratedPreset{id: id, groups: string(encoded), bindings: bindings})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate routing presets for binding migration: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close routing presets for binding migration: %w", err)
	}
	for _, preset := range presets {
		if _, err := tx.ExecContext(ctx, `UPDATE subscription_routing_presets SET groups_json = ? WHERE id = ?`,
			preset.groups, preset.id); err != nil {
			return fmt.Errorf("store routing preset %d groups for binding migration: %w", preset.id, err)
		}
	}

	presetBindings := make(map[int64]map[string][]int64, len(presets))
	for _, preset := range presets {
		presetBindings[preset.id] = preset.bindings
	}
	planRows, err := tx.QueryContext(ctx, `SELECT id, routing_preset_id FROM subscription_plans
		WHERE routing_preset_id IS NOT NULL ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list plans for routing binding migration: %w", err)
	}
	type migratedPlan struct{ id, presetID int64 }
	plans := make([]migratedPlan, 0)
	for planRows.Next() {
		var value migratedPlan
		if err := planRows.Scan(&value.id, &value.presetID); err != nil {
			planRows.Close()
			return fmt.Errorf("scan plan for routing binding migration: %w", err)
		}
		plans = append(plans, value)
	}
	if err := planRows.Err(); err != nil {
		planRows.Close()
		return fmt.Errorf("iterate plans for routing binding migration: %w", err)
	}
	if err := planRows.Close(); err != nil {
		return fmt.Errorf("close plans for routing binding migration: %w", err)
	}
	for _, plan := range plans {
		legacy := presetBindings[plan.presetID]
		if len(legacy) == 0 {
			continue
		}
		allowed := make(map[int64]struct{})
		nodeRows, err := tx.QueryContext(ctx,
			`SELECT published_node_id FROM subscription_plan_nodes WHERE plan_id = ?`, plan.id)
		if err != nil {
			return fmt.Errorf("list plan %d nodes for routing binding migration: %w", plan.id, err)
		}
		for nodeRows.Next() {
			var nodeID int64
			if err := nodeRows.Scan(&nodeID); err != nil {
				nodeRows.Close()
				return fmt.Errorf("scan plan %d node for routing binding migration: %w", plan.id, err)
			}
			allowed[nodeID] = struct{}{}
		}
		if err := nodeRows.Err(); err != nil {
			nodeRows.Close()
			return fmt.Errorf("iterate plan %d nodes for routing binding migration: %w", plan.id, err)
		}
		if err := nodeRows.Close(); err != nil {
			return fmt.Errorf("close plan %d nodes for routing binding migration: %w", plan.id, err)
		}
		bindings := make(map[string][]int64)
		for key, nodeIDs := range legacy {
			for _, nodeID := range nodeIDs {
				if _, exists := allowed[nodeID]; exists {
					bindings[key] = append(bindings[key], nodeID)
				}
			}
			if len(bindings[key]) == 0 {
				delete(bindings, key)
			}
		}
		encoded, err := json.Marshal(bindings)
		if err != nil {
			return fmt.Errorf("encode plan %d routing bindings: %w", plan.id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE subscription_plans SET routing_bindings_json = ? WHERE id = ?`, encoded, plan.id); err != nil {
			return fmt.Errorf("store plan %d routing bindings: %w", plan.id, err)
		}
	}
	return nil
}

func migrationRoutingGroupKey(presetID int64, position int, existing map[string]struct{}) string {
	base := fmt.Sprintf("grp_%d_%d", presetID, position)
	value := base
	for suffix := 2; ; suffix++ {
		if _, exists := existing[value]; !exists {
			return value
		}
		value = fmt.Sprintf("%s_%d", base, suffix)
	}
}

func migratePersonalSubscriptionNodeInstances(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE personal_subscription_nodes_v9 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NOT NULL REFERENCES personal_subscription_groups(id) ON DELETE CASCADE,
			source_type TEXT NOT NULL CHECK (source_type IN ('proxy', 'relay', 'landing')),
			source_id INTEGER NOT NULL CHECK (source_id > 0),
			display_name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			position INTEGER NOT NULL CHECK (position > 0),
			entry_host TEXT CHECK (entry_host IS NULL OR entry_host != ''),
			entry_port INTEGER CHECK (entry_port IS NULL OR entry_port BETWEEN 1 AND 65535),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (group_id, display_name),
			UNIQUE (group_id, position)
		)`,
		`INSERT INTO personal_subscription_nodes_v9
			(id, group_id, source_type, source_id, display_name, enabled, position,
			 entry_host, entry_port, created_at, updated_at)
		 SELECT personal.id, personal.group_id,
			CASE
				WHEN personal.source_type = 'published' AND published.mode = 'relay' AND published.relay_id IS NOT NULL THEN 'relay'
				WHEN personal.source_type = 'published' THEN 'proxy'
				ELSE personal.source_type
			END,
			CASE
				WHEN personal.source_type = 'published' AND published.mode = 'relay' AND published.relay_id IS NOT NULL THEN published.relay_id
				WHEN personal.source_type = 'published' THEN published.target_proxy_id
				ELSE personal.source_id
			END,
			personal.display_name, personal.enabled, personal.position,
			CASE
				WHEN personal.source_type = 'published' AND published.mode = 'direct' AND published.entry_host_mode = 'manual'
					THEN published.entry_host
				ELSE NULL
			END,
			NULL, personal.created_at, personal.updated_at
		 FROM personal_subscription_nodes AS personal
		 LEFT JOIN subscription_published_nodes AS published
			ON personal.source_type = 'published' AND published.id = personal.source_id
		 WHERE personal.source_type != 'published' OR published.id IS NOT NULL`,
		`DROP TABLE personal_subscription_nodes`,
		`ALTER TABLE personal_subscription_nodes_v9 RENAME TO personal_subscription_nodes`,
		`CREATE INDEX idx_personal_subscription_nodes_source
			ON personal_subscription_nodes(source_type, source_id)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate personal subscription node instances: %w", err)
		}
	}
	return nil
}

func createPersonalSubscriptions(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE personal_subscription_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			subscription_title TEXT NOT NULL DEFAULT '',
			token TEXT NOT NULL UNIQUE,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			client_name TEXT NOT NULL,
			routing_preset_id INTEGER NOT NULL REFERENCES subscription_routing_presets(id) ON DELETE RESTRICT,
			mihomo_template_id INTEGER REFERENCES subscription_templates(id) ON DELETE RESTRICT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX idx_personal_subscription_groups_owner
			ON personal_subscription_groups(owner_user_id, created_at DESC, id DESC)`,
		`CREATE INDEX idx_personal_subscription_groups_routing
			ON personal_subscription_groups(routing_preset_id)`,
		`CREATE INDEX idx_personal_subscription_groups_mihomo_template
			ON personal_subscription_groups(mihomo_template_id)`,
		`CREATE TABLE personal_subscription_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NOT NULL REFERENCES personal_subscription_groups(id) ON DELETE CASCADE,
			source_type TEXT NOT NULL CHECK (source_type IN ('proxy', 'published', 'landing')),
			source_id INTEGER NOT NULL CHECK (source_id > 0),
			display_name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			position INTEGER NOT NULL CHECK (position > 0),
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE (group_id, source_type, source_id),
			UNIQUE (group_id, display_name),
			UNIQUE (group_id, position)
		)`,
		`CREATE INDEX idx_personal_subscription_nodes_source
			ON personal_subscription_nodes(source_type, source_id)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create personal subscription schema: %w", err)
		}
	}
	return nil
}

func migrate(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hasVersions, err := migrationTableExists(ctx, db)
	if err != nil {
		return err
	}
	if !hasVersions {
		hasLegacy, err := hasApplicationTables(ctx, db)
		if err != nil {
			return err
		}
		if hasLegacy {
			if err := migrateLegacySchema(db); err != nil {
				return fmt.Errorf("bootstrap legacy schema: %w", err)
			}
			if err := recordLegacyBaseline(ctx, db); err != nil {
				return err
			}
		} else if err := applyMigration(ctx, db, migrations[0]); err != nil {
			return err
		}
	}

	versions, err := appliedMigrationVersions(ctx, db)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		// A table with no rows can only be a partially initialized legacy
		// database. Finish the legacy bootstrap before recording v1.
		hasLegacy, err := hasApplicationTables(ctx, db)
		if err != nil {
			return err
		}
		if hasLegacy {
			if err := migrateLegacySchema(db); err != nil {
				return fmt.Errorf("bootstrap unversioned schema: %w", err)
			}
			if err := recordLegacyBaseline(ctx, db); err != nil {
				return err
			}
			versions = []int{1}
		} else if err := applyMigration(ctx, db, migrations[0]); err != nil {
			return err
		} else {
			versions = []int{1}
		}
	}
	if err := validateMigrationHistory(versions); err != nil {
		return err
	}
	current := versions[len(versions)-1]
	if current > LatestSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", current, LatestSchemaVersion)
	}
	for _, item := range migrations {
		if item.version <= current {
			continue
		}
		if err := applyMigration(ctx, db, item); err != nil {
			return err
		}
	}
	return nil
}

func createBaselineSchema(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, schemaMigrationsStatement); err != nil {
		return fmt.Errorf("create schema migration history: %w", err)
	}
	for _, statement := range schemaStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create baseline schema: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, subscriptionSourceServerIndexStatement); err != nil {
		return fmt.Errorf("create baseline subscription source server index: %w", err)
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, item migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema migration %d (%s): %w", item.version, item.name, err)
	}
	defer tx.Rollback()
	if err := item.up(ctx, tx); err != nil {
		return fmt.Errorf("apply schema migration %d (%s): %w", item.version, item.name, err)
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		return fmt.Errorf("validate schema migration %d (%s): %w", item.version, item.name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		item.version, item.name, time.Now().UTC().Unix(),
	); err != nil {
		return fmt.Errorf("record schema migration %d (%s): %w", item.version, item.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration %d (%s): %w", item.version, item.name, err)
	}
	return nil
}

const schemaMigrationsStatement = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at INTEGER NOT NULL
)`

func recordLegacyBaseline(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy schema bootstrap: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schemaMigrationsStatement); err != nil {
		return fmt.Errorf("create schema migration history: %w", err)
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		return fmt.Errorf("validate legacy schema bootstrap: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations (version, name, applied_at) VALUES (1, ?, ?)`,
		migrations[0].name, time.Now().UTC().Unix(),
	); err != nil {
		return fmt.Errorf("record legacy schema baseline: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy schema bootstrap: %w", err)
	}
	return nil
}

func migrationTableExists(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect schema migration history: %w", err)
	}
	return count == 1, nil
}

func hasApplicationTables(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'`).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect existing database schema: %w", err)
	}
	return count != 0, nil
}

func appliedMigrationVersions(ctx context.Context, db *sql.DB) ([]int, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("list applied schema migrations: %w", err)
	}
	defer rows.Close()
	versions := make([]int, 0, LatestSchemaVersion)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan applied schema migration: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied schema migrations: %w", err)
	}
	return versions, nil
}

func validateMigrationHistory(versions []int) error {
	for index, version := range versions {
		if version != index+1 {
			return fmt.Errorf("schema migration history is incomplete: expected version %d, found %d", index+1, version)
		}
	}
	return nil
}

func checkForeignKeys(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) error {
	rows, err := query.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("run foreign key check: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var constraint int
		if err := rows.Scan(&table, &rowID, &parent, &constraint); err != nil {
			return fmt.Errorf("scan foreign key violation: %w", err)
		}
		return fmt.Errorf("foreign key violation in %s row %v referencing %s", table, rowID, parent)
	}
	return rows.Err()
}

func createListenerReservations(ctx context.Context, tx *sql.Tx) error {
	if conflict, err := existingListenerConflict(ctx, tx); err != nil {
		return err
	} else if conflict != "" {
		return errors.New(conflict)
	}
	statements := []string{
		`CREATE TABLE server_listener_reservations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			resource_type TEXT NOT NULL CHECK (resource_type IN ('proxy', 'relay', 'client_relay')),
			resource_id INTEGER NOT NULL,
			port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
			network TEXT NOT NULL CHECK (network IN ('tcp', 'udp', 'both')),
			created_at INTEGER NOT NULL,
			UNIQUE (resource_type, resource_id, port)
		)`,
		`CREATE INDEX idx_server_listener_reservations_server_port
			ON server_listener_reservations(server_id, port)`,
		`CREATE TRIGGER server_listener_reservations_insert_conflict
			BEFORE INSERT ON server_listener_reservations
			WHEN EXISTS (
				SELECT 1 FROM server_listener_reservations AS existing
				WHERE existing.server_id = NEW.server_id AND existing.port = NEW.port
				AND (existing.network = 'both' OR NEW.network = 'both' OR existing.network = NEW.network)
				AND NOT (
					NEW.resource_type = 'relay' AND existing.resource_type = 'client_relay'
					AND COALESCE((SELECT source_client_id FROM relays WHERE id = NEW.resource_id), -1) =
						existing.resource_id
				)
				AND NOT (
					NEW.resource_type = 'client_relay' AND existing.resource_type = 'relay'
					AND NEW.resource_id = COALESCE(
						(SELECT source_client_id FROM relays WHERE id = existing.resource_id), -1)
				)
			)
			BEGIN SELECT RAISE(ABORT, 'listener reservation conflict'); END`,
		`CREATE TRIGGER server_listener_reservations_update_conflict
			BEFORE UPDATE OF server_id, port, network ON server_listener_reservations
			WHEN EXISTS (
				SELECT 1 FROM server_listener_reservations AS existing
				WHERE existing.id != OLD.id AND existing.server_id = NEW.server_id AND existing.port = NEW.port
				AND (existing.network = 'both' OR NEW.network = 'both' OR existing.network = NEW.network)
				AND NOT (
					NEW.resource_type = 'relay' AND existing.resource_type = 'client_relay'
					AND COALESCE((SELECT source_client_id FROM relays WHERE id = NEW.resource_id), -1) =
						existing.resource_id
				)
				AND NOT (
					NEW.resource_type = 'client_relay' AND existing.resource_type = 'relay'
					AND NEW.resource_id = COALESCE(
						(SELECT source_client_id FROM relays WHERE id = existing.resource_id), -1)
				)
			)
			BEGIN SELECT RAISE(ABORT, 'listener reservation conflict'); END`,
		`INSERT INTO server_listener_reservations
			(server_id, resource_type, resource_id, port, network, created_at)
			SELECT server_id, 'proxy', id, listen_port,
				CASE WHEN protocol = 'shadowsocks' THEN 'both' ELSE 'tcp' END, created_at FROM proxies`,
		`INSERT INTO server_listener_reservations
			(server_id, resource_type, resource_id, port, network, created_at)
			SELECT server_id, 'relay', id, listen_port,
				CASE WHEN network = 'tcp,udp' THEN 'both' ELSE network END, created_at FROM relays`,
		`INSERT INTO server_listener_reservations
			(server_id, resource_type, resource_id, port, network, created_at)
			SELECT server_id, 'client_relay', client_id, port, 'tcp', created_at FROM client_relay_ports`,
		`CREATE TRIGGER proxies_listener_insert AFTER INSERT ON proxies BEGIN
			INSERT INTO server_listener_reservations
				(server_id, resource_type, resource_id, port, network, created_at)
			VALUES (NEW.server_id, 'proxy', NEW.id, NEW.listen_port,
				CASE WHEN NEW.protocol = 'shadowsocks' THEN 'both' ELSE 'tcp' END, NEW.created_at);
		END`,
		`CREATE TRIGGER proxies_listener_update AFTER UPDATE OF server_id, listen_port, protocol ON proxies BEGIN
			UPDATE server_listener_reservations SET server_id = NEW.server_id, port = NEW.listen_port,
				network = CASE WHEN NEW.protocol = 'shadowsocks' THEN 'both' ELSE 'tcp' END
			WHERE resource_type = 'proxy' AND resource_id = NEW.id;
		END`,
		`CREATE TRIGGER proxies_listener_delete AFTER DELETE ON proxies BEGIN
			DELETE FROM server_listener_reservations WHERE resource_type = 'proxy' AND resource_id = OLD.id;
		END`,
		`CREATE TRIGGER relays_listener_insert AFTER INSERT ON relays BEGIN
			INSERT INTO server_listener_reservations
				(server_id, resource_type, resource_id, port, network, created_at)
			VALUES (NEW.server_id, 'relay', NEW.id, NEW.listen_port,
				CASE WHEN NEW.network = 'tcp,udp' THEN 'both' ELSE NEW.network END, NEW.created_at);
		END`,
		`CREATE TRIGGER relays_listener_update AFTER UPDATE OF server_id, listen_port, network ON relays BEGIN
			UPDATE server_listener_reservations SET server_id = NEW.server_id, port = NEW.listen_port,
				network = CASE WHEN NEW.network = 'tcp,udp' THEN 'both' ELSE NEW.network END
			WHERE resource_type = 'relay' AND resource_id = NEW.id;
		END`,
		`CREATE TRIGGER relays_listener_delete AFTER DELETE ON relays BEGIN
			DELETE FROM server_listener_reservations WHERE resource_type = 'relay' AND resource_id = OLD.id;
		END`,
		`CREATE TRIGGER client_relay_listener_insert AFTER INSERT ON client_relay_ports BEGIN
			INSERT INTO server_listener_reservations
				(server_id, resource_type, resource_id, port, network, created_at)
			VALUES (NEW.server_id, 'client_relay', NEW.client_id, NEW.port, 'tcp', NEW.created_at);
		END`,
		`CREATE TRIGGER client_relay_listener_update AFTER UPDATE OF client_id, server_id, port ON client_relay_ports BEGIN
			UPDATE server_listener_reservations SET server_id = NEW.server_id,
				resource_id = NEW.client_id, port = NEW.port
			WHERE resource_type = 'client_relay' AND resource_id = OLD.client_id AND port = OLD.port;
		END`,
		`CREATE TRIGGER client_relay_listener_delete AFTER DELETE ON client_relay_ports BEGIN
			DELETE FROM server_listener_reservations
			WHERE resource_type = 'client_relay' AND resource_id = OLD.client_id AND port = OLD.port;
		END`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create listener reservation schema: %w", err)
		}
	}
	return nil
}

func existingListenerConflict(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT server_id, port, resource_type, resource_id, network, client_id FROM (
		SELECT server_id, listen_port AS port, 'proxy' AS resource_type, id AS resource_id,
			CASE WHEN protocol = 'shadowsocks' THEN 'both' ELSE 'tcp' END AS network, NULL AS client_id FROM proxies
		UNION ALL
		SELECT server_id, listen_port, 'relay', id,
			CASE WHEN network = 'tcp,udp' THEN 'both' ELSE network END, source_client_id FROM relays
		UNION ALL
		SELECT server_id, port, 'client_relay', client_id, 'tcp', client_id FROM client_relay_ports
	) ORDER BY server_id, port, resource_type, resource_id`)
	if err != nil {
		return "", fmt.Errorf("inspect existing listener reservations: %w", err)
	}
	defer rows.Close()
	type occupied struct {
		resourceType string
		resourceID   int64
		network      string
		clientID     sql.NullInt64
	}
	byAddress := make(map[string][]occupied)
	for rows.Next() {
		var serverID, resourceID int64
		var port int
		var resourceType, network string
		var clientID sql.NullInt64
		if err := rows.Scan(&serverID, &port, &resourceType, &resourceID, &network, &clientID); err != nil {
			return "", fmt.Errorf("scan existing listener reservation: %w", err)
		}
		key := fmt.Sprintf("%d:%d", serverID, port)
		for _, other := range byAddress[key] {
			sharedClientReservation := clientID.Valid && other.clientID.Valid && clientID.Int64 == other.clientID.Int64 &&
				((resourceType == "relay" && other.resourceType == "client_relay") ||
					(resourceType == "client_relay" && other.resourceType == "relay"))
			if networksOverlap(network, other.network) && !sharedClientReservation {
				return fmt.Sprintf("listener conflict on server %d port %d between %s %d (%s) and %s %d (%s)",
					serverID, port, other.resourceType, other.resourceID, other.network, resourceType, resourceID, network), nil
			}
		}
		byAddress[key] = append(byAddress[key], occupied{
			resourceType: resourceType, resourceID: resourceID, network: network, clientID: clientID,
		})
	}
	return "", rows.Err()
}

func networksOverlap(left, right string) bool {
	return left == "both" || right == "both" || left == right
}

func createConfigurationOperations(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE configuration_operations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
			resource_type TEXT NOT NULL,
			resource_id INTEGER NOT NULL,
			action TEXT NOT NULL CHECK (action IN ('create', 'update', 'delete', 'reconcile')),
			desired_state_version INTEGER NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'applied', 'failed', 'superseded')),
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			applied_at INTEGER
		)`,
		`CREATE INDEX idx_configuration_operations_server_status
			ON configuration_operations(server_id, status, desired_state_version)`,
		`CREATE INDEX idx_configuration_operations_created_at
			ON configuration_operations(created_at DESC, id DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create configuration operation schema: %w", err)
		}
	}
	return nil
}

func createSubscriptionConfiguration(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE subscription_routing_presets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			groups_json TEXT NOT NULL,
			rules_json TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE subscription_templates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			config_yaml TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`ALTER TABLE subscription_plans ADD COLUMN routing_preset_id INTEGER
			REFERENCES subscription_routing_presets(id) ON DELETE RESTRICT`,
		`ALTER TABLE subscription_plans ADD COLUMN template_id INTEGER
			REFERENCES subscription_templates(id) ON DELETE RESTRICT`,
		`CREATE INDEX idx_subscription_plans_routing_preset ON subscription_plans(routing_preset_id)`,
		`CREATE INDEX idx_subscription_plans_template ON subscription_plans(template_id)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create subscription configuration schema: %w", err)
		}
	}
	return nil
}

type legacyRoutingGroup struct {
	ID      string                     `json:"id"`
	Name    string                     `json:"name"`
	Type    string                     `json:"type"`
	Members []legacyRoutingGroupMember `json:"members"`
}

type legacyRoutingGroupMember struct {
	Type            string `json:"type"`
	PublishedNodeID int64  `json:"published_node_id,omitempty"`
	GroupID         string `json:"group_id,omitempty"`
}

type legacyRoutingRule struct {
	Type          string `json:"type"`
	Value         string `json:"value,omitempty"`
	TargetGroupID string `json:"target_group_id"`
}

type migratedRoutingGroup struct {
	Key        string   `json:"key,omitempty"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Proxies    []string `json:"proxies"`
	NodeIDs    []int64  `json:"node_ids,omitempty"`
	IncludeAll bool     `json:"include_all,omitempty"`
}

func materializeSubscriptionPlanRouting(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE subscription_plans ADD COLUMN routing_groups_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE subscription_plans ADD COLUMN routing_rules_json TEXT NOT NULL DEFAULT '[]'`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add subscription plan routing columns: %w", err)
		}
	}

	type convertedPreset struct {
		groups string
		rules  string
	}
	converted := make(map[int64]convertedPreset)
	rows, err := tx.QueryContext(ctx, `SELECT id, groups_json, rules_json FROM subscription_routing_presets ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list routing presets for migration: %w", err)
	}
	for rows.Next() {
		var id int64
		var groupsJSON, rulesJSON string
		if err := rows.Scan(&id, &groupsJSON, &rulesJSON); err != nil {
			rows.Close()
			return fmt.Errorf("scan routing preset for migration: %w", err)
		}
		groups, rules, err := convertLegacyRoutingPreset(groupsJSON, rulesJSON)
		if err != nil {
			rows.Close()
			return fmt.Errorf("convert routing preset %d: %w", id, err)
		}
		converted[id] = convertedPreset{groups: groups, rules: rules}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate routing presets for migration: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close routing presets for migration: %w", err)
	}
	for id, value := range converted {
		if _, err := tx.ExecContext(ctx, `UPDATE subscription_routing_presets SET groups_json = ?, rules_json = ? WHERE id = ?`,
			value.groups, value.rules, id); err != nil {
			return fmt.Errorf("store converted routing preset %d: %w", id, err)
		}
	}

	planRows, err := tx.QueryContext(ctx, `SELECT id, routing_preset_id FROM subscription_plans WHERE routing_preset_id IS NOT NULL ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list plans with routing presets: %w", err)
	}
	type planPreset struct{ planID, presetID int64 }
	plans := make([]planPreset, 0)
	for planRows.Next() {
		var value planPreset
		if err := planRows.Scan(&value.planID, &value.presetID); err != nil {
			planRows.Close()
			return fmt.Errorf("scan plan routing preset: %w", err)
		}
		plans = append(plans, value)
	}
	if err := planRows.Err(); err != nil {
		planRows.Close()
		return fmt.Errorf("iterate plan routing presets: %w", err)
	}
	if err := planRows.Close(); err != nil {
		return fmt.Errorf("close plan routing presets: %w", err)
	}
	for _, plan := range plans {
		preset, exists := converted[plan.presetID]
		if !exists {
			return fmt.Errorf("plan %d references missing routing preset %d", plan.planID, plan.presetID)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE subscription_plans
			SET routing_groups_json = ?, routing_rules_json = ? WHERE id = ?`, preset.groups, preset.rules, plan.planID); err != nil {
			return fmt.Errorf("materialize routing preset for plan %d: %w", plan.planID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE subscription_plans SET routing_preset_id = NULL WHERE routing_preset_id IS NOT NULL`); err != nil {
		return fmt.Errorf("clear legacy plan routing preset references: %w", err)
	}
	return nil
}

func convertLegacyRoutingPreset(groupsJSON, rulesJSON string) (string, string, error) {
	var oldGroups []legacyRoutingGroup
	if err := decodeMigrationJSON(groupsJSON, &oldGroups); err != nil {
		return "", "", fmt.Errorf("decode groups_json: %w", err)
	}
	var oldRules []legacyRoutingRule
	if err := decodeMigrationJSON(rulesJSON, &oldRules); err != nil {
		return "", "", fmt.Errorf("decode rules_json: %w", err)
	}
	if len(oldGroups) == 0 {
		return "", "", errors.New("groups must not be empty")
	}
	groupNames := make(map[string]string, len(oldGroups))
	seenNames := make(map[string]struct{}, len(oldGroups))
	for _, group := range oldGroups {
		name := strings.TrimSpace(group.Name)
		if group.ID == "" || name == "" || group.Type != "select" || len(group.Members) == 0 {
			return "", "", fmt.Errorf("group %q has invalid id, name, type, or members", group.ID)
		}
		if _, exists := groupNames[group.ID]; exists {
			return "", "", fmt.Errorf("duplicate group id %q", group.ID)
		}
		if _, exists := seenNames[name]; exists {
			return "", "", fmt.Errorf("duplicate group name %q", name)
		}
		groupNames[group.ID] = name
		seenNames[name] = struct{}{}
	}
	newGroups := make([]migratedRoutingGroup, 0, len(oldGroups))
	for _, group := range oldGroups {
		value := migratedRoutingGroup{Name: groupNames[group.ID], Type: "select", Proxies: []string{}, NodeIDs: []int64{}}
		seenProxies := make(map[string]struct{})
		seenNodeIDs := make(map[int64]struct{})
		for _, member := range group.Members {
			switch member.Type {
			case "direct":
				if member.PublishedNodeID != 0 || member.GroupID != "" {
					return "", "", fmt.Errorf("group %q has invalid DIRECT member", group.ID)
				}
				if _, exists := seenProxies["DIRECT"]; !exists {
					value.Proxies = append(value.Proxies, "DIRECT")
					seenProxies["DIRECT"] = struct{}{}
				}
			case "published_node":
				if member.PublishedNodeID <= 0 || member.GroupID != "" {
					return "", "", fmt.Errorf("group %q has invalid published node member", group.ID)
				}
				if _, exists := seenNodeIDs[member.PublishedNodeID]; !exists {
					value.NodeIDs = append(value.NodeIDs, member.PublishedNodeID)
					seenNodeIDs[member.PublishedNodeID] = struct{}{}
				}
			case "group":
				name, exists := groupNames[member.GroupID]
				if !exists || member.PublishedNodeID != 0 || member.GroupID == group.ID {
					return "", "", fmt.Errorf("group %q has invalid group reference %q", group.ID, member.GroupID)
				}
				if _, exists := seenProxies[name]; !exists {
					value.Proxies = append(value.Proxies, name)
					seenProxies[name] = struct{}{}
				}
			default:
				return "", "", fmt.Errorf("group %q has unknown member type %q", group.ID, member.Type)
			}
		}
		newGroups = append(newGroups, value)
	}
	if migratedRoutingGroupsCyclic(newGroups) {
		return "", "", errors.New("routing groups contain a cycle")
	}
	newRules := make([]string, 0, len(oldRules))
	for _, rule := range oldRules {
		target, exists := groupNames[rule.TargetGroupID]
		if !exists {
			return "", "", fmt.Errorf("rule references unknown group %q", rule.TargetGroupID)
		}
		switch rule.Type {
		case "MATCH":
			if strings.TrimSpace(rule.Value) != "" {
				return "", "", errors.New("MATCH rule contains a value")
			}
			newRules = append(newRules, "MATCH,"+target)
		case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "IP-CIDR", "GEOIP", "GEOSITE":
			value := strings.TrimSpace(rule.Value)
			if value == "" || strings.ContainsAny(value, "\r\n,") {
				return "", "", fmt.Errorf("%s rule contains an invalid value", rule.Type)
			}
			newRules = append(newRules, strings.Join([]string{rule.Type, value, target}, ","))
		default:
			return "", "", fmt.Errorf("unknown rule type %q", rule.Type)
		}
	}
	groups, err := json.Marshal(newGroups)
	if err != nil {
		return "", "", fmt.Errorf("encode migrated groups: %w", err)
	}
	rules, err := json.Marshal(newRules)
	if err != nil {
		return "", "", fmt.Errorf("encode migrated rules: %w", err)
	}
	return string(groups), string(rules), nil
}

func migratedRoutingGroupsCyclic(groups []migratedRoutingGroup) bool {
	byName := make(map[string]migratedRoutingGroup, len(groups))
	for _, group := range groups {
		byName[group.Name] = group
	}
	visiting, visited := make(map[string]bool), make(map[string]bool)
	var visit func(string) bool
	visit = func(name string) bool {
		if visiting[name] {
			return true
		}
		if visited[name] {
			return false
		}
		visiting[name] = true
		for _, member := range byName[name].Proxies {
			if _, exists := byName[member]; exists && visit(member) {
				return true
			}
		}
		visiting[name] = false
		visited[name] = true
		return false
	}
	for name := range byName {
		if visit(name) {
			return true
		}
	}
	return false
}

func decodeMigrationJSON(raw string, value any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("unexpected trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing JSON data: %w", err)
	}
	return nil
}

const defaultRoutingGroupsJSON = `[{"name":"🚀 默认代理","type":"select","proxies":["DIRECT"],"include_all":true},{"name":"🤖 AI","type":"select","proxies":["🚀 默认代理"],"include_all":true},{"name":"▶️ YouTube","type":"select","proxies":["🚀 默认代理"],"include_all":true},{"name":"🎬 Netflix","type":"select","proxies":["🚀 默认代理"],"include_all":true},{"name":"✈️ Telegram","type":"select","proxies":["🚀 默认代理"],"include_all":true},{"name":"🎵 TikTok","type":"select","proxies":["🚀 默认代理"],"include_all":true},{"name":"🍎 Apple","type":"select","proxies":["DIRECT","🚀 默认代理"],"include_all":true},{"name":"Ⓜ️ Microsoft","type":"select","proxies":["DIRECT","🚀 默认代理"],"include_all":true}]`

const defaultRoutingRulesJSON = `["RULE-SET,OpenAI,🤖 AI","RULE-SET,Claude,🤖 AI","RULE-SET,Gemini,🤖 AI","RULE-SET,YouTube,▶️ YouTube","RULE-SET,Netflix,🎬 Netflix","RULE-SET,Telegram,✈️ Telegram","RULE-SET,TikTok,🎵 TikTok","RULE-SET,Apple,🍎 Apple","RULE-SET,Copilot,Ⓜ️ Microsoft","RULE-SET,Microsoft,Ⓜ️ Microsoft","GEOIP,CN,DIRECT,no-resolve","MATCH,🚀 默认代理"]`

const defaultRoutingProvidersYAML = `OpenAI:
  type: http
  behavior: classical
  format: yaml
  interval: 86400
  url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/OpenAI/OpenAI.yaml
Claude:
  type: http
  behavior: classical
  format: yaml
  interval: 86400
  url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Claude/Claude.yaml
Gemini:
  type: http
  behavior: classical
  format: yaml
  interval: 86400
  url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Gemini/Gemini.yaml
YouTube:
  type: http
  behavior: classical
  format: yaml
  interval: 86400
  url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/YouTube/YouTube.yaml
Netflix:
  type: http
  behavior: classical
  format: yaml
  interval: 86400
  url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Netflix/Netflix_Classical.yaml
Telegram:
  type: http
  behavior: classical
  format: yaml
  interval: 86400
  url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Telegram/Telegram.yaml
TikTok:
  type: http
  behavior: classical
  format: yaml
  interval: 86400
  url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/TikTok/TikTok.yaml
Apple:
  type: http
  behavior: classical
  format: yaml
  interval: 86400
  url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Apple/Apple_Classical.yaml
Copilot:
  type: http
  behavior: classical
  format: yaml
  interval: 86400
  url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Copilot/Copilot.yaml
Microsoft:
  type: http
  behavior: classical
  format: yaml
  interval: 86400
  url: https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Microsoft/Microsoft.yaml`

func migrateRoutingPresetsAsRuntimeProfiles(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE subscription_routing_presets ADD COLUMN rule_providers_yaml TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE subscription_routing_presets ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1))`,
		`CREATE UNIQUE INDEX idx_subscription_routing_presets_default
			ON subscription_routing_presets(is_default) WHERE is_default = 1`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("extend subscription routing presets: %w", err)
		}
	}

	now := time.Now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `INSERT INTO subscription_routing_presets
		(name, enabled, groups_json, rules_json, rule_providers_yaml, is_default, created_at, updated_at)
		VALUES ('默认分流', 1, ?, ?, ?, 1, ?, ?)`,
		defaultRoutingGroupsJSON, defaultRoutingRulesJSON, defaultRoutingProvidersYAML, now, now)
	if err != nil {
		return fmt.Errorf("create default subscription routing preset: %w", err)
	}
	defaultID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read default subscription routing preset id: %w", err)
	}

	type templateRouting struct {
		providers string
		has       bool
		enabled   bool
	}
	templateRoutings := make(map[int64]templateRouting)
	templateRows, err := tx.QueryContext(ctx, `SELECT id, enabled, config_yaml FROM subscription_templates ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list subscription templates for routing migration: %w", err)
	}
	type migratedTemplate struct {
		id         int64
		configYAML string
	}
	migratedTemplates := make([]migratedTemplate, 0)
	for templateRows.Next() {
		var id int64
		var enabled int
		var configYAML string
		if err := templateRows.Scan(&id, &enabled, &configYAML); err != nil {
			templateRows.Close()
			return fmt.Errorf("scan subscription template for routing migration: %w", err)
		}
		cleaned, providers, hasProviders, err := stripTemplateRoutingSections(configYAML)
		if err != nil {
			templateRows.Close()
			return fmt.Errorf("migrate subscription template %d: %w", id, err)
		}
		templateRoutings[id] = templateRouting{providers: providers, has: hasProviders, enabled: enabled != 0}
		migratedTemplates = append(migratedTemplates, migratedTemplate{id: id, configYAML: cleaned})
	}
	if err := templateRows.Err(); err != nil {
		templateRows.Close()
		return fmt.Errorf("iterate subscription templates for routing migration: %w", err)
	}
	if err := templateRows.Close(); err != nil {
		return fmt.Errorf("close subscription templates for routing migration: %w", err)
	}
	for _, value := range migratedTemplates {
		if _, err := tx.ExecContext(ctx, `UPDATE subscription_templates SET config_yaml = ? WHERE id = ?`,
			value.configYAML, value.id); err != nil {
			return fmt.Errorf("store migrated subscription template %d: %w", value.id, err)
		}
	}

	presetRows, err := tx.QueryContext(ctx,
		`SELECT id, groups_json, rules_json FROM subscription_routing_presets WHERE is_default = 0 ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list existing routing presets for provider migration: %w", err)
	}
	type existingPreset struct {
		id     int64
		groups string
		rules  string
	}
	existingPresets := make([]existingPreset, 0)
	for presetRows.Next() {
		var value existingPreset
		if err := presetRows.Scan(&value.id, &value.groups, &value.rules); err != nil {
			presetRows.Close()
			return fmt.Errorf("scan existing routing preset for provider migration: %w", err)
		}
		if err := validateMigratedRouting(value.groups, value.rules, defaultRoutingProvidersYAML); err != nil {
			presetRows.Close()
			return fmt.Errorf("validate existing routing preset %d: %w", value.id, err)
		}
		existingPresets = append(existingPresets, value)
	}
	if err := presetRows.Err(); err != nil {
		presetRows.Close()
		return fmt.Errorf("iterate existing routing presets for provider migration: %w", err)
	}
	if err := presetRows.Close(); err != nil {
		return fmt.Errorf("close existing routing presets for provider migration: %w", err)
	}
	for _, value := range existingPresets {
		if _, err := tx.ExecContext(ctx,
			`UPDATE subscription_routing_presets SET rule_providers_yaml = ? WHERE id = ?`,
			defaultRoutingProvidersYAML, value.id); err != nil {
			return fmt.Errorf("store providers for existing routing preset %d: %w", value.id, err)
		}
	}

	planRows, err := tx.QueryContext(ctx, `SELECT id, name, routing_groups_json, routing_rules_json, template_id
		FROM subscription_plans ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list subscription plans for routing migration: %w", err)
	}
	type migratedPlan struct {
		id, routingPresetID int64
	}
	plans := make([]migratedPlan, 0)
	for planRows.Next() {
		var id int64
		var name, groupsJSON, rulesJSON string
		var templateID sql.NullInt64
		if err := planRows.Scan(&id, &name, &groupsJSON, &rulesJSON, &templateID); err != nil {
			planRows.Close()
			return fmt.Errorf("scan subscription plan for routing migration: %w", err)
		}
		var groups []migratedRoutingGroup
		if err := decodeMigrationJSON(groupsJSON, &groups); err != nil {
			planRows.Close()
			return fmt.Errorf("decode subscription plan %d routing groups: %w", id, err)
		}
		var rules []string
		if err := decodeMigrationJSON(rulesJSON, &rules); err != nil {
			planRows.Close()
			return fmt.Errorf("decode subscription plan %d routing rules: %w", id, err)
		}
		routingPresetID := defaultID
		if len(groups) != 0 || len(rules) != 0 {
			if len(groups) == 0 {
				planRows.Close()
				return fmt.Errorf("subscription plan %d has rules without routing groups", id)
			}
			providers := defaultRoutingProvidersYAML
			if templateID.Valid {
				if template, exists := templateRoutings[templateID.Int64]; exists && template.enabled && template.has {
					providers = template.providers
				}
			}
			if err := validateMigratedRouting(groupsJSON, rulesJSON, providers); err != nil {
				planRows.Close()
				return fmt.Errorf("validate subscription plan %d routing: %w", id, err)
			}
			created, err := tx.ExecContext(ctx, `INSERT INTO subscription_routing_presets
				(name, enabled, groups_json, rules_json, rule_providers_yaml, is_default, created_at, updated_at)
				VALUES (?, 1, ?, ?, ?, 0, ?, ?)`, name+" 分流（迁移）", groupsJSON, rulesJSON, providers, now, now)
			if err != nil {
				planRows.Close()
				return fmt.Errorf("create migrated routing preset for subscription plan %d: %w", id, err)
			}
			routingPresetID, err = created.LastInsertId()
			if err != nil {
				planRows.Close()
				return fmt.Errorf("read migrated routing preset id for subscription plan %d: %w", id, err)
			}
		}
		plans = append(plans, migratedPlan{id: id, routingPresetID: routingPresetID})
	}
	if err := planRows.Err(); err != nil {
		planRows.Close()
		return fmt.Errorf("iterate subscription plans for routing migration: %w", err)
	}
	if err := planRows.Close(); err != nil {
		return fmt.Errorf("close subscription plans for routing migration: %w", err)
	}
	for _, plan := range plans {
		if _, err := tx.ExecContext(ctx, `UPDATE subscription_plans
			SET routing_preset_id = ?, routing_groups_json = '[]', routing_rules_json = '[]' WHERE id = ?`,
			plan.routingPresetID, plan.id); err != nil {
			return fmt.Errorf("store routing preset reference for subscription plan %d: %w", plan.id, err)
		}
	}
	return nil
}

func stripTemplateRoutingSections(source string) (string, string, bool, error) {
	if len(source) > 64<<10 {
		return "", "", false, errors.New("template YAML exceeds the supported size")
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(source), &document); err != nil || len(document.Content) != 1 ||
		document.Content[0].Kind != yaml.MappingNode || !safeMigrationYAMLNode(document.Content[0]) {
		if err != nil {
			return "", "", false, fmt.Errorf("decode template YAML: %w", err)
		}
		return "", "", false, errors.New("template YAML root must be a safe mapping")
	}
	root := document.Content[0]
	seen := make(map[string]struct{}, len(root.Content)/2)
	content := make([]*yaml.Node, 0, len(root.Content))
	providers, hasProviders := "", false
	for index := 0; index < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return "", "", false, errors.New("template YAML contains a non-string top-level key")
		}
		if _, exists := seen[key.Value]; exists {
			return "", "", false, fmt.Errorf("template YAML contains duplicate key %q", key.Value)
		}
		seen[key.Value] = struct{}{}
		switch key.Value {
		case "rule-providers":
			if value.Kind != yaml.MappingNode {
				return "", "", false, errors.New("template rule-providers must be a mapping")
			}
			encoded, err := yaml.Marshal(value)
			if err != nil {
				return "", "", false, fmt.Errorf("encode template rule-providers: %w", err)
			}
			providers, hasProviders = strings.TrimSpace(string(encoded)), true
		case "proxies", "proxy-groups", "rules":
		default:
			content = append(content, key, value)
		}
	}
	root.Content = content
	encoded, err := yaml.Marshal(&document)
	if err != nil {
		return "", "", false, fmt.Errorf("encode migrated template YAML: %w", err)
	}
	return strings.TrimSpace(string(encoded)), providers, hasProviders, nil
}

func validateMigratedRouting(groupsJSON, rulesJSON, providersYAML string) error {
	var groups []migratedRoutingGroup
	if err := decodeMigrationJSON(groupsJSON, &groups); err != nil {
		return fmt.Errorf("decode groups: %w", err)
	}
	if len(groups) == 0 {
		return errors.New("routing groups must not be empty")
	}
	var rules []string
	if err := decodeMigrationJSON(rulesJSON, &rules); err != nil {
		return fmt.Errorf("decode rules: %w", err)
	}
	providers, err := migrationProviderNames(providersYAML)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		parts := strings.Split(rule, ",")
		if len(parts) >= 3 && strings.TrimSpace(parts[0]) == "RULE-SET" {
			provider := strings.TrimSpace(parts[1])
			if _, exists := providers[provider]; !exists {
				return fmt.Errorf("rule %q references missing rule provider %q", rule, provider)
			}
		}
	}
	return nil
}

func migrationProviderNames(source string) (map[string]struct{}, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(source), &document); err != nil || len(document.Content) != 1 ||
		document.Content[0].Kind != yaml.MappingNode || !safeMigrationYAMLNode(document.Content[0]) {
		if err != nil {
			return nil, fmt.Errorf("decode rule providers YAML: %w", err)
		}
		return nil, errors.New("rule providers YAML root must be a safe mapping")
	}
	root := document.Content[0]
	names := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index < len(root.Content); index += 2 {
		key := root.Content[index]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || strings.TrimSpace(key.Value) == "" {
			return nil, errors.New("rule providers YAML contains an invalid key")
		}
		if _, exists := names[key.Value]; exists {
			return nil, fmt.Errorf("rule providers YAML contains duplicate key %q", key.Value)
		}
		names[key.Value] = struct{}{}
	}
	return names, nil
}

func safeMigrationYAMLNode(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return false
	}
	allowedTags := map[string]bool{
		"": true, "!!map": true, "!!seq": true, "!!str": true, "!!bool": true,
		"!!int": true, "!!float": true, "!!null": true, "tag:yaml.org,2002:map": true,
		"tag:yaml.org,2002:seq": true, "tag:yaml.org,2002:str": true,
		"tag:yaml.org,2002:bool": true, "tag:yaml.org,2002:int": true,
		"tag:yaml.org,2002:float": true, "tag:yaml.org,2002:null": true,
	}
	if !allowedTags[node.Tag] {
		return false
	}
	for _, child := range node.Content {
		if !safeMigrationYAMLNode(child) {
			return false
		}
	}
	return true
}

func createAuditLogs(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at INTEGER NOT NULL,
			actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			actor_username TEXT NOT NULL,
			action TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id INTEGER,
			summary TEXT NOT NULL,
			request_id TEXT NOT NULL
		)`,
		`CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at DESC, id DESC)`,
		`CREATE INDEX idx_audit_logs_action ON audit_logs(action, created_at DESC)`,
		`CREATE INDEX idx_audit_logs_actor ON audit_logs(actor_user_id, created_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create audit log schema: %w", err)
		}
	}
	return nil
}
