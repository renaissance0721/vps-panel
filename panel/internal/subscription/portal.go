package subscription

import (
	"context"
	"fmt"
)

func (s *Service) ListSubscriberNodes(ctx context.Context, userID int64) ([]SubscriberNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT nodes.id, nodes.name, nodes.mode,
		nodes.traffic_multiplier_bp, nodes.enabled
		FROM subscriber_profiles AS profiles
		JOIN subscription_plan_nodes AS mapping ON mapping.plan_id = profiles.plan_id
		JOIN subscription_published_nodes AS nodes ON nodes.id = mapping.published_node_id
		WHERE profiles.user_id = ? AND nodes.enabled = 1
		ORDER BY mapping.position`, userID)
	if err != nil {
		return nil, fmt.Errorf("list subscriber portal nodes: %w", err)
	}
	defer rows.Close()
	values := make([]SubscriberNode, 0)
	for rows.Next() {
		var value SubscriberNode
		if err := rows.Scan(&value.ID, &value.Name, &value.Mode, &value.TrafficMultiplierBP, &value.Enabled); err != nil {
			return nil, fmt.Errorf("scan subscriber portal node: %w", err)
		}
		value.Name = FormatNodeDisplayName(value.Name, value.TrafficMultiplierBP)
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriber portal nodes: %w", err)
	}
	return values, nil
}
