package subscription

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PruneDeletedPersonalSubscriptionSourcesTx removes only references to physically
// deleted sources, including cascaded deletions and historical orphan references.
// Callers must delete sources and commit this cleanup in the same transaction.
func PruneDeletedPersonalSubscriptionSourcesTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	const missingSource = `(source_type = 'proxy' AND NOT EXISTS (
		SELECT 1 FROM proxies WHERE proxies.id = personal_subscription_nodes.source_id))
		OR (source_type = 'relay' AND NOT EXISTS (
		SELECT 1 FROM relays WHERE relays.id = personal_subscription_nodes.source_id))
		OR (source_type = 'landing' AND NOT EXISTS (
		SELECT 1 FROM landing_nodes WHERE landing_nodes.id = personal_subscription_nodes.source_id))`
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT groups.id, groups.routing_bindings_json
		FROM personal_subscription_groups AS groups
		JOIN personal_subscription_nodes ON personal_subscription_nodes.group_id = groups.id
		WHERE `+missingSource)
	if err != nil {
		return fmt.Errorf("list deleted personal subscription source references: %w", err)
	}
	groups := make(map[int64]string)
	for rows.Next() {
		var id int64
		var bindings string
		if err := rows.Scan(&id, &bindings); err != nil {
			rows.Close()
			return fmt.Errorf("scan deleted personal subscription source references: %w", err)
		}
		groups[id] = bindings
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate deleted personal subscription source references: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close deleted personal subscription source references: %w", err)
	}
	if len(groups) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM personal_subscription_nodes WHERE `+missingSource); err != nil {
		return fmt.Errorf("delete personal subscription source references: %w", err)
	}
	for id, raw := range groups {
		bindings, err := decodeRoutingBindings(raw)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM personal_subscription_nodes WHERE group_id = ?`, id)
		if err != nil {
			return fmt.Errorf("list remaining personal subscription nodes: %w", err)
		}
		allowed := make(map[int64]struct{})
		for rows.Next() {
			var nodeID int64
			if err := rows.Scan(&nodeID); err != nil {
				rows.Close()
				return fmt.Errorf("scan remaining personal subscription node: %w", err)
			}
			allowed[nodeID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate remaining personal subscription nodes: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close remaining personal subscription nodes: %w", err)
		}
		encoded, err := encodeRoutingBindings(pruneRoutingBindings(bindings, allowed))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE personal_subscription_groups
			SET routing_bindings_json = ?, updated_at = ? WHERE id = ?`, encoded, now.UTC().Truncate(time.Second).Unix(), id); err != nil {
			return fmt.Errorf("prune deleted personal subscription source routing bindings: %w", err)
		}
	}
	return nil
}
