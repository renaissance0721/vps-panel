package database

import (
	"context"
	"database/sql"
	"fmt"
)

// Ranges remain in proxy config_json. Triggers protect both directions: a new
// range cannot take an existing UDP listener, and future listeners cannot take
// a reserved range. No per-port rows are needed for large ranges.
func addHysteria2PortHoppingConflicts(ctx context.Context, tx *sql.Tx) error {
	for _, operation := range []string{"INSERT", "UPDATE OF server_id, listen_port, protocol, config_json"} {
		name := "insert"
		if operation != "INSERT" {
			name = "update"
		}
		statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS proxy_hopping_%s_conflict
		BEFORE %s ON proxies
		WHEN NEW.protocol = 'hysteria2' AND json_extract(NEW.config_json, '$.hysteria2.port_hopping_enabled') = 1
		BEGIN
		 SELECT CASE WHEN COALESCE(json_type(NEW.config_json, '$.hysteria2.port_hopping_start'), '') != 'integer'
		   OR COALESCE(json_type(NEW.config_json, '$.hysteria2.port_hopping_end'), '') != 'integer'
		   OR json_extract(NEW.config_json, '$.hysteria2.port_hopping_start') NOT BETWEEN 1024 AND 65535
		   OR json_extract(NEW.config_json, '$.hysteria2.port_hopping_end') NOT BETWEEN 1024 AND 65535
		   OR json_extract(NEW.config_json, '$.hysteria2.port_hopping_start') > json_extract(NEW.config_json, '$.hysteria2.port_hopping_end')
		   OR json_extract(NEW.config_json, '$.hysteria2.port_hopping_end') - json_extract(NEW.config_json, '$.hysteria2.port_hopping_start') + 1 > 10000
		   OR NEW.listen_port BETWEEN json_extract(NEW.config_json, '$.hysteria2.port_hopping_start') AND json_extract(NEW.config_json, '$.hysteria2.port_hopping_end')
		 THEN RAISE(ABORT, 'invalid Hysteria2 port hopping range') END;
		 SELECT CASE WHEN EXISTS (
		   SELECT 1 FROM server_listener_reservations r
		   WHERE r.server_id = NEW.server_id
		     AND r.network IN ('udp', 'both')
		     AND r.port BETWEEN json_extract(NEW.config_json, '$.hysteria2.port_hopping_start') AND json_extract(NEW.config_json, '$.hysteria2.port_hopping_end')
		     AND NOT (r.resource_type = 'proxy' AND r.resource_id = NEW.id)
		 ) OR EXISTS (
		   SELECT 1 FROM proxies p WHERE p.server_id = NEW.server_id AND p.id != NEW.id AND p.protocol = 'hysteria2'
		     AND json_extract(p.config_json, '$.hysteria2.port_hopping_enabled') = 1
		     AND json_extract(p.config_json, '$.hysteria2.port_hopping_start') <= json_extract(NEW.config_json, '$.hysteria2.port_hopping_end')
		     AND json_extract(p.config_json, '$.hysteria2.port_hopping_end') >= json_extract(NEW.config_json, '$.hysteria2.port_hopping_start')
		 ) THEN RAISE(ABORT, 'listener reservation conflict') END;
		END`, name, operation)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create proxy port hopping conflict trigger: %w", err)
		}
	}
	for _, operation := range []string{"INSERT", "UPDATE OF server_id, port, network"} {
		name := "insert"
		if operation != "INSERT" {
			name = "update"
		}
		statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS listener_hopping_%s_conflict
		BEFORE %s ON server_listener_reservations
		WHEN NEW.network IN ('udp', 'both') AND EXISTS (
		 SELECT 1 FROM proxies p WHERE p.server_id = NEW.server_id AND p.protocol = 'hysteria2'
		 AND NOT (NEW.resource_type = 'proxy' AND NEW.resource_id = p.id)
		 AND json_extract(p.config_json, '$.hysteria2.port_hopping_enabled') = 1
		 AND NEW.port BETWEEN json_extract(p.config_json, '$.hysteria2.port_hopping_start') AND json_extract(p.config_json, '$.hysteria2.port_hopping_end')
		) BEGIN SELECT RAISE(ABORT, 'listener reservation conflict'); END`, name, operation)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create listener port hopping conflict trigger: %w", err)
		}
	}
	return nil
}

func updateHysteria2PortHoppingConflicts(ctx context.Context, tx *sql.Tx) error {
	for _, name := range []string{"proxy_hopping_insert_conflict", "proxy_hopping_update_conflict", "listener_hopping_insert_conflict", "listener_hopping_update_conflict"} {
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+name); err != nil {
			return err
		}
	}
	return addHysteria2PortHoppingConflicts(ctx, tx)
}
