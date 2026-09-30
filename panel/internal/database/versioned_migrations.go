package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const LatestSchemaVersion = 5

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
