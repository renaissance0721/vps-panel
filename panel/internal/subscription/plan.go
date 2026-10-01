package subscription

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

func (s *Service) ListPlans(ctx context.Context) ([]Plan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, subscription_title, enabled, traffic_limit_bytes,
		routing_preset_id, template_id, created_at, updated_at
		FROM subscription_plans ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list subscription plans: %w", err)
	}
	values := make([]Plan, 0)
	for rows.Next() {
		value, err := scanPlan(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan subscription plan: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate subscription plans: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close subscription plans: %w", err)
	}
	for index := range values {
		values[index].Nodes, err = listPlanNodes(ctx, s.db, values[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (s *Service) GetPlan(ctx context.Context, id int64) (Plan, error) {
	value, err := scanPlan(s.db.QueryRowContext(ctx, `SELECT id, name, subscription_title, enabled, traffic_limit_bytes,
		routing_preset_id, template_id, created_at, updated_at
		FROM subscription_plans WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, ErrPlanNotFound
	}
	if err != nil {
		return Plan{}, fmt.Errorf("get subscription plan: %w", err)
	}
	value.Nodes, err = listPlanNodes(ctx, s.db, id)
	if err != nil {
		return Plan{}, err
	}
	return value, nil
}

func (s *Service) CreatePlan(ctx context.Context, input CreatePlanInput) (Plan, error) {
	value, err := normalizePlan(Plan{
		Name: input.Name, SubscriptionTitle: input.SubscriptionTitle,
		Enabled: input.Enabled, TrafficLimitBytes: input.TrafficLimitBytes,
		RoutingPresetID: input.RoutingPresetID, TemplateID: input.TemplateID,
	})
	if err != nil {
		return Plan{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	if err := validatePlanTemplateRef(ctx, s.db, value.TemplateID); err != nil {
		return Plan{}, err
	}
	value.RoutingPresetID, err = resolvePlanRoutingPresetRef(ctx, s.db, value.RoutingPresetID, nil)
	if err != nil {
		return Plan{}, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO subscription_plans
		(name, subscription_title, enabled, traffic_limit_bytes, routing_preset_id, template_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		value.Name, nullableString(value.SubscriptionTitle), value.Enabled,
		nullableInt64(value.TrafficLimitBytes), nullableInt64(value.RoutingPresetID),
		nullableInt64(value.TemplateID), now.Unix(), now.Unix())
	if err != nil {
		return Plan{}, fmt.Errorf("create subscription plan: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Plan{}, fmt.Errorf("read subscription plan id: %w", err)
	}
	return s.GetPlan(ctx, id)
}

func (s *Service) UpdatePlan(ctx context.Context, id int64, input UpdatePlanInput) (Plan, []proxystore.Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, nil, fmt.Errorf("begin subscription plan update: %w", err)
	}
	defer tx.Rollback()
	current, err := scanPlan(tx.QueryRowContext(ctx, `SELECT id, name, subscription_title, enabled, traffic_limit_bytes,
		routing_preset_id, template_id, created_at, updated_at
		FROM subscription_plans WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, nil, ErrPlanNotFound
	}
	if err != nil {
		return Plan{}, nil, fmt.Errorf("read subscription plan for update: %w", err)
	}
	if input.Name != nil {
		current.Name = *input.Name
	}
	if input.SubscriptionTitle != nil {
		current.SubscriptionTitle = *input.SubscriptionTitle
	}
	if input.Enabled != nil {
		current.Enabled = *input.Enabled
	}
	if input.TrafficLimitBytesSet {
		current.TrafficLimitBytes = input.TrafficLimitBytes
	}
	previousRoutingPresetID := current.RoutingPresetID
	if input.RoutingPresetIDSet {
		current.RoutingPresetID = input.RoutingPresetID
	}
	if input.TemplateIDSet {
		current.TemplateID = input.TemplateID
	}
	current, err = normalizePlan(current)
	if err != nil {
		return Plan{}, nil, err
	}
	if err := validatePlanTemplateRef(ctx, tx, current.TemplateID); err != nil {
		return Plan{}, nil, err
	}
	current.RoutingPresetID, err = resolvePlanRoutingPresetRef(ctx, tx, current.RoutingPresetID, previousRoutingPresetID)
	if err != nil {
		return Plan{}, nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE subscription_plans SET
		name = ?, subscription_title = ?, enabled = ?, traffic_limit_bytes = ?,
		routing_preset_id = ?, template_id = ?, updated_at = ? WHERE id = ?`,
		current.Name, nullableString(current.SubscriptionTitle), current.Enabled,
		nullableInt64(current.TrafficLimitBytes), nullableInt64(current.RoutingPresetID),
		nullableInt64(current.TemplateID), now.Unix(), id)
	if err != nil {
		return Plan{}, nil, fmt.Errorf("update subscription plan: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Plan{}, nil, fmt.Errorf("read subscription plan update: %w", err)
	}
	if count != 1 {
		return Plan{}, nil, ErrPlanNotFound
	}
	affected := make(map[int64]struct{})
	if err := s.reconcilePlanSubscribersTx(ctx, tx, id, now, affected); err != nil {
		return Plan{}, nil, err
	}
	mutations, err := bumpAffectedServers(ctx, tx, affected, now)
	if err != nil {
		return Plan{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Plan{}, nil, fmt.Errorf("commit subscription plan update: %w", err)
	}
	updated, err := s.GetPlan(ctx, id)
	return updated, mutations, err
}

func (s *Service) SetPlanNodes(ctx context.Context, planID int64, nodeIDs []int64) (Plan, []proxystore.Mutation, error) {
	seen := make(map[int64]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID <= 0 {
			return Plan{}, nil, ErrInvalidPlanNodes
		}
		if _, exists := seen[nodeID]; exists {
			return Plan{}, nil, ErrInvalidPlanNodes
		}
		seen[nodeID] = struct{}{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, nil, fmt.Errorf("begin subscription plan node update: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM subscription_plans WHERE id = ?`, planID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return Plan{}, nil, ErrPlanNotFound
	} else if err != nil {
		return Plan{}, nil, fmt.Errorf("find subscription plan: %w", err)
	}
	existingNodes := make(map[int64]struct{})
	rows, err := tx.QueryContext(ctx, `SELECT published_node_id FROM subscription_plan_nodes WHERE plan_id = ?`, planID)
	if err != nil {
		return Plan{}, nil, fmt.Errorf("list existing subscription plan nodes: %w", err)
	}
	for rows.Next() {
		var nodeID int64
		if err := rows.Scan(&nodeID); err != nil {
			rows.Close()
			return Plan{}, nil, fmt.Errorf("scan existing subscription plan node: %w", err)
		}
		existingNodes[nodeID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Plan{}, nil, fmt.Errorf("iterate existing subscription plan nodes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Plan{}, nil, fmt.Errorf("close existing subscription plan nodes: %w", err)
	}
	targets := make(map[int64]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		var targetProxyID int64
		var mode, targetRole string
		var sourceRole sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT nodes.target_proxy_id, nodes.mode,
			target_server.created_by_role, source_server.created_by_role
			FROM subscription_published_nodes AS nodes
			JOIN proxies AS target ON target.id = nodes.target_proxy_id
			JOIN servers AS target_server ON target_server.id = target.server_id
			LEFT JOIN servers AS source_server ON source_server.id = nodes.source_server_id
			WHERE nodes.id = ?`, nodeID).Scan(&targetProxyID, &mode, &targetRole, &sourceRole); errors.Is(err, sql.ErrNoRows) {
			return Plan{}, nil, ErrPublishedNodeNotFound
		} else if err != nil {
			return Plan{}, nil, fmt.Errorf("find published node for plan: %w", err)
		}
		_, alreadyIncluded := existingNodes[nodeID]
		if !alreadyIncluded && (targetRole != "admin" || mode == NodeModeRelay && sourceRole.String != "admin") {
			return Plan{}, nil, ErrServerNotDistributable
		}
		if _, duplicate := targets[targetProxyID]; duplicate {
			return Plan{}, nil, ErrInvalidPlanNodes
		}
		targets[targetProxyID] = struct{}{}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscription_plan_nodes WHERE plan_id = ?`, planID); err != nil {
		return Plan{}, nil, fmt.Errorf("clear subscription plan nodes: %w", err)
	}
	for position, nodeID := range nodeIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO subscription_plan_nodes (plan_id, published_node_id, position) VALUES (?, ?, ?)`,
			planID, nodeID, position+1); err != nil {
			return Plan{}, nil, fmt.Errorf("add subscription plan node: %w", err)
		}
	}
	now := s.now().UTC().Truncate(time.Second)
	if _, err := tx.ExecContext(ctx, `UPDATE subscription_plans SET updated_at = ? WHERE id = ?`, now.Unix(), planID); err != nil {
		return Plan{}, nil, fmt.Errorf("touch subscription plan: %w", err)
	}
	affected := make(map[int64]struct{})
	if err := s.reconcilePlanSubscribersTx(ctx, tx, planID, now, affected); err != nil {
		return Plan{}, nil, err
	}
	mutations, err := bumpAffectedServers(ctx, tx, affected, now)
	if err != nil {
		return Plan{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Plan{}, nil, fmt.Errorf("commit subscription plan node update: %w", err)
	}
	updated, err := s.GetPlan(ctx, planID)
	return updated, mutations, err
}

func (s *Service) DeletePlan(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin subscription plan deletion: %w", err)
	}
	defer tx.Rollback()
	var references int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscriber_profiles WHERE plan_id = ?`, id,
	).Scan(&references); err != nil {
		return fmt.Errorf("count subscription plan references: %w", err)
	}
	if references != 0 {
		return ErrPlanReferenced
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM subscription_plans WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete subscription plan: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read subscription plan deletion: %w", err)
	}
	if count != 1 {
		return ErrPlanNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit subscription plan deletion: %w", err)
	}
	return nil
}

func (s *Service) reconcilePlanSubscribersTx(
	ctx context.Context,
	tx *sql.Tx,
	planID int64,
	now time.Time,
	affected map[int64]struct{},
) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT user_id FROM subscriber_profiles WHERE plan_id = ? ORDER BY user_id`, planID)
	if err != nil {
		return fmt.Errorf("list subscription plan subscribers: %w", err)
	}
	userIDs := make([]int64, 0)
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return fmt.Errorf("scan subscription plan subscriber: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate subscription plan subscribers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close subscription plan subscribers: %w", err)
	}
	for _, userID := range userIDs {
		if err := s.reconcileSubscriberTx(ctx, tx, userID, now, affected); err != nil {
			return err
		}
	}
	return nil
}

func listPlanNodes(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, planID int64) ([]PlanNode, error) {
	rows, err := query.QueryContext(ctx, `SELECT nodes.id, nodes.name, nodes.mode, nodes.target_proxy_id, target.name,
		target.server_id, target_server.name, target_server.created_by_role,
		target.entry_host_mode, target.entry_host, target.listen_port, COALESCE(target_info.public_ipv4, ''),
		nodes.source_server_id, source_server.name, source_server.created_by_role,
		nodes.relay_id, nodes.entry_host_mode, nodes.entry_host, nodes.entry_port_mode, relay.listen_port,
		COALESCE(source_info.public_ipv4, ''), nodes.traffic_multiplier_bp,
		nodes.enabled, nodes.created_at, nodes.updated_at, mapping.position
		FROM subscription_plan_nodes AS mapping
		JOIN subscription_published_nodes AS nodes ON nodes.id = mapping.published_node_id
		JOIN proxies AS target ON target.id = nodes.target_proxy_id
		JOIN servers AS target_server ON target_server.id = target.server_id
		LEFT JOIN server_system_info AS target_info ON target_info.server_id = target.server_id
		LEFT JOIN servers AS source_server ON source_server.id = nodes.source_server_id
		LEFT JOIN server_system_info AS source_info ON source_info.server_id = nodes.source_server_id
		LEFT JOIN relays AS relay ON relay.id = nodes.relay_id
		WHERE mapping.plan_id = ? ORDER BY mapping.position`, planID)
	if err != nil {
		return nil, fmt.Errorf("list subscription plan nodes: %w", err)
	}
	defer rows.Close()
	values := make([]PlanNode, 0)
	for rows.Next() {
		var value PlanNode
		var sourceServerID, relayID, relayEntryPort sql.NullInt64
		var sourceServerName, sourceCreatorRole, sourcePublicIPv4 sql.NullString
		var targetCreatorRole, targetEntryHostMode, targetEntryHost, targetPublicIPv4 string
		var targetEntryPort int
		var enabled int
		var createdAt, updatedAt int64
		if err := rows.Scan(&value.ID, &value.Name, &value.Mode, &value.TargetProxyID, &value.TargetProxyName,
			&value.TargetServerID, &value.TargetServerName, &targetCreatorRole,
			&targetEntryHostMode, &targetEntryHost, &targetEntryPort, &targetPublicIPv4,
			&sourceServerID, &sourceServerName, &sourceCreatorRole, &relayID,
			&value.EntryHostMode, &value.EntryHost, &value.EntryPortMode, &relayEntryPort,
			&sourcePublicIPv4, &value.TrafficMultiplierBP, &enabled, &createdAt, &updatedAt, &value.Position); err != nil {
			return nil, fmt.Errorf("scan subscription plan node: %w", err)
		}
		populatePlanNode(&value, sourceServerID, relayID, relayEntryPort, sourceServerName,
			targetEntryHostMode, targetEntryHost, targetEntryPort, targetPublicIPv4,
			sourcePublicIPv4, enabled, createdAt, updatedAt)
		value.Distributable = targetCreatorRole == "admin" && (value.Mode == NodeModeDirect || sourceCreatorRole.String == "admin")
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscription plan nodes: %w", err)
	}
	return values, nil
}

func populatePlanNode(value *PlanNode, sourceServerID, relayID, relayEntryPort sql.NullInt64,
	sourceServerName sql.NullString, targetEntryHostMode, targetEntryHost string,
	targetEntryPort int, targetPublicIPv4 string, sourcePublicIPv4 sql.NullString,
	enabled int, createdAt, updatedAt int64,
) {
	if sourceServerID.Valid {
		id := sourceServerID.Int64
		value.SourceServerID = &id
		value.SourceServerName = sourceServerName.String
	}
	if relayID.Valid {
		id := relayID.Int64
		value.RelayID = &id
	}
	populatePublishedNodeEndpoint(&value.PublishedNode, targetEntryHostMode, targetEntryHost,
		targetEntryPort, targetPublicIPv4, sourcePublicIPv4.String, relayEntryPort)
	value.Enabled = enabled != 0
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
}

type rowScanner interface{ Scan(...any) error }

func scanPlan(row rowScanner) (Plan, error) {
	var value Plan
	var trafficLimit sql.NullInt64
	var subscriptionTitle sql.NullString
	var routingPresetID, templateID sql.NullInt64
	var enabled int
	var createdAt, updatedAt int64
	err := row.Scan(&value.ID, &value.Name, &subscriptionTitle, &enabled, &trafficLimit,
		&routingPresetID, &templateID, &createdAt, &updatedAt)
	if err != nil {
		return Plan{}, err
	}
	value.Enabled = enabled != 0
	value.SubscriptionTitle = strings.TrimSpace(subscriptionTitle.String)
	if trafficLimit.Valid {
		limit := trafficLimit.Int64
		value.TrafficLimitBytes = &limit
	}
	if routingPresetID.Valid {
		id := routingPresetID.Int64
		value.RoutingPresetID = &id
	}
	if templateID.Valid {
		id := templateID.Int64
		value.TemplateID = &id
	}
	value.Nodes = make([]PlanNode, 0)
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func normalizePlan(value Plan) (Plan, error) {
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" || utf8.RuneCountInString(value.Name) > maxNameRunes {
		return Plan{}, ErrInvalidPlanName
	}
	value.SubscriptionTitle = strings.TrimSpace(value.SubscriptionTitle)
	if utf8.RuneCountInString(value.SubscriptionTitle) > maxNameRunes {
		return Plan{}, ErrInvalidSubscriptionTitle
	}
	if value.TrafficLimitBytes != nil && *value.TrafficLimitBytes < 0 {
		return Plan{}, ErrInvalidTrafficLimit
	}
	if value.RoutingPresetID != nil && *value.RoutingPresetID <= 0 {
		return Plan{}, ErrRoutingPresetNotFound
	}
	return value, nil
}

func validResetTime(value string) bool {
	parsed, err := time.Parse("15:04", value)
	return err == nil && parsed.Format("15:04") == value
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
