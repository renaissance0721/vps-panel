package subscription

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	"github.com/renaissance0721/vps-panel/panel/internal/relay"
)

type proxyTopology struct {
	id            int64
	serverID      int64
	entryHostMode string
	entryHost     string
}

func (s *Service) ListPublishedNodes(ctx context.Context) ([]PublishedNode, error) {
	return listPublishedNodes(ctx, s.db, "ORDER BY nodes.created_at DESC, nodes.id DESC")
}

func (s *Service) GetPublishedNode(ctx context.Context, id int64) (PublishedNode, error) {
	values, err := listPublishedNodes(ctx, s.db, "WHERE nodes.id = ?", id)
	if err != nil {
		return PublishedNode{}, err
	}
	if len(values) == 0 {
		return PublishedNode{}, ErrPublishedNodeNotFound
	}
	return values[0], nil
}

func (s *Service) CreatePublishedNode(ctx context.Context, input CreatePublishedNodeInput) (PublishedNode, *relay.Mutation, error) {
	created, relayMutation, _, err := s.CreatePublishedNodeWithPlans(ctx, input)
	return created, relayMutation, err
}

func (s *Service) CreatePublishedNodeWithPlans(
	ctx context.Context,
	input CreatePublishedNodeInput,
) (PublishedNode, *relay.Mutation, []proxystore.Mutation, error) {
	name, err := validatePublishedNodeName(input.Name)
	if err != nil {
		return PublishedNode{}, nil, nil, err
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode != NodeModeDirect && mode != NodeModeRelay {
		return PublishedNode{}, nil, nil, ErrInvalidNodeMode
	}
	if input.TargetProxyID <= 0 {
		return PublishedNode{}, nil, nil, ErrTargetProxyNotFound
	}
	if mode == NodeModeDirect && input.SourceServerID != nil {
		return PublishedNode{}, nil, nil, ErrInvalidNodeTopology
	}
	if mode == NodeModeRelay && (input.SourceServerID == nil || *input.SourceServerID <= 0) {
		return PublishedNode{}, nil, nil, ErrSourceServerRequired
	}
	entryHostMode, entryHost, entryPortMode, err := normalizePublishedNodeEndpoint(
		mode, input.EntryHostMode, input.EntryHost, input.EntryPortMode,
	)
	if err != nil {
		return PublishedNode{}, nil, nil, err
	}
	if mode == NodeModeDirect && input.EntryPort != nil {
		return PublishedNode{}, nil, nil, ErrInvalidEntryPortMode
	}
	if mode == NodeModeRelay && entryPortMode == EntryPortModeManual &&
		(input.EntryPort == nil || *input.EntryPort < 1 || *input.EntryPort > 65535) {
		return PublishedNode{}, nil, nil, relay.ErrInvalidPort
	}
	if mode == NodeModeRelay && entryPortMode == EntryPortModeAuto && input.EntryPort != nil {
		return PublishedNode{}, nil, nil, ErrInvalidEntryPortMode
	}
	multiplierBP, err := normalizeTrafficMultiplierBP(input.TrafficMultiplierBP)
	if err != nil {
		return PublishedNode{}, nil, nil, err
	}
	if err := validatePublishedNodePlanIDs(input.PlanIDs); err != nil {
		return PublishedNode{}, nil, nil, err
	}

	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishedNode{}, nil, nil, fmt.Errorf("begin published node creation: %w", err)
	}
	defer tx.Rollback()
	if _, err := getProxyTopology(ctx, tx, input.TargetProxyID, ErrTargetProxyNotFound); err != nil {
		return PublishedNode{}, nil, nil, err
	}
	if err := proxystore.RequireAdminCreatedProxy(ctx, tx, input.TargetProxyID); err != nil {
		if errors.Is(err, proxystore.ErrNotDistributable) {
			return PublishedNode{}, nil, nil, ErrServerNotDistributable
		}
		return PublishedNode{}, nil, nil, err
	}

	var relayID any
	var mutation *relay.Mutation
	if mode == NodeModeRelay {
		if err := requireAdminSourceServer(ctx, tx, *input.SourceServerID); err != nil {
			return PublishedNode{}, nil, nil, err
		}
		port := 0
		if entryPortMode == EntryPortModeAuto {
			port, err = relay.RandomUserRelayPort(ctx, tx, *input.SourceServerID)
			if err != nil {
				return PublishedNode{}, nil, nil, err
			}
		} else {
			port = *input.EntryPort
		}
		createdRelayID, createdMutation, err := s.relays.CreateSubscriptionRelayTx(ctx, tx, relay.CreateInput{
			ServerID: *input.SourceServerID, Name: name, ListenAddress: "0.0.0.0", ListenPort: port,
			EntryHostMode: entryHostMode, EntryHost: entryHost,
			TargetType: relay.TargetProxy, TargetProxyID: &input.TargetProxyID,
			Network: relay.NetworkTCP, Enabled: input.Enabled,
		}, now)
		if err != nil {
			return PublishedNode{}, nil, nil, err
		}
		relayID = createdRelayID
		mutation = &createdMutation
	}

	result, err := tx.ExecContext(ctx,
		`INSERT INTO subscription_published_nodes
		 (name, mode, target_proxy_id, source_server_id, relay_id, entry_host_mode, entry_host,
		  entry_port_mode, traffic_multiplier_bp, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, mode, input.TargetProxyID, nullableID(input.SourceServerID), relayID,
		entryHostMode, entryHost, entryPortMode, multiplierBP,
		input.Enabled, now.Unix(), now.Unix(),
	)
	if err != nil {
		return PublishedNode{}, nil, nil, fmt.Errorf("create published node: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return PublishedNode{}, nil, nil, fmt.Errorf("read published node id: %w", err)
	}
	changedPlanIDs, err := syncPublishedNodePlansTx(
		ctx, tx, id, input.TargetProxyID, input.PlanIDs, now,
	)
	if err != nil {
		return PublishedNode{}, nil, nil, err
	}
	affected := make(map[int64]struct{})
	for _, planID := range changedPlanIDs {
		if err := s.reconcilePlanSubscribersTx(ctx, tx, planID, now, affected); err != nil {
			return PublishedNode{}, nil, nil, err
		}
	}
	mutations, err := bumpAffectedServers(ctx, tx, affected, now)
	if err != nil {
		return PublishedNode{}, nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return PublishedNode{}, nil, nil, fmt.Errorf("commit published node creation: %w", err)
	}
	created, err := s.GetPublishedNode(ctx, id)
	return created, mutation, mutations, err
}

func (s *Service) UpdatePublishedNode(ctx context.Context, id int64, input UpdatePublishedNodeInput) (PublishedNode, []proxystore.Mutation, error) {
	if input.Name == nil && input.EntryHostMode == nil && input.EntryHost == nil && input.EntryPortMode == nil &&
		input.EntryPort == nil && input.TrafficMultiplierBP == nil && input.Enabled == nil && !input.PlanIDsSet {
		return PublishedNode{}, nil, ErrInvalidNodeUpdate
	}
	if input.PlanIDsSet {
		if err := validatePublishedNodePlanIDs(input.PlanIDs); err != nil {
			return PublishedNode{}, nil, err
		}
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishedNode{}, nil, fmt.Errorf("begin published node update: %w", err)
	}
	defer tx.Rollback()

	var currentName, mode, currentEntryHostMode, currentEntryHost, currentEntryPortMode string
	var targetProxyID int64
	var currentMultiplierBP, currentEnabled int
	var sourceServerID, relayID, currentEntryPort sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT nodes.name, nodes.mode, nodes.target_proxy_id, nodes.source_server_id, nodes.relay_id,
			nodes.entry_host_mode, nodes.entry_host, nodes.entry_port_mode,
			nodes.traffic_multiplier_bp, nodes.enabled, relay.listen_port
		 FROM subscription_published_nodes AS nodes
		 LEFT JOIN relays AS relay ON relay.id = nodes.relay_id WHERE nodes.id = ?`, id,
	).Scan(&currentName, &mode, &targetProxyID, &sourceServerID, &relayID,
		&currentEntryHostMode, &currentEntryHost, &currentEntryPortMode,
		&currentMultiplierBP, &currentEnabled, &currentEntryPort)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishedNode{}, nil, ErrPublishedNodeNotFound
	}
	if err != nil {
		return PublishedNode{}, nil, fmt.Errorf("read published node for update: %w", err)
	}
	name := currentName
	if input.Name != nil {
		name, err = validatePublishedNodeName(*input.Name)
		if err != nil {
			return PublishedNode{}, nil, err
		}
	}
	enabled := currentEnabled != 0
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	multiplierBP := currentMultiplierBP
	if input.TrafficMultiplierBP != nil {
		multiplierBP, err = validateTrafficMultiplierBP(*input.TrafficMultiplierBP)
		if err != nil {
			return PublishedNode{}, nil, err
		}
	}
	entryHostMode := currentEntryHostMode
	entryHost := currentEntryHost
	entryPortMode := currentEntryPortMode
	if input.EntryHostMode != nil {
		entryHostMode = *input.EntryHostMode
	}
	if input.EntryHost != nil {
		entryHost = *input.EntryHost
	}
	if input.EntryPortMode != nil {
		entryPortMode = *input.EntryPortMode
	}
	entryHostMode, entryHost, entryPortMode, err = normalizePublishedNodeEndpoint(
		mode, entryHostMode, entryHost, entryPortMode,
	)
	if err != nil {
		return PublishedNode{}, nil, err
	}
	listenPort := int(currentEntryPort.Int64)
	if mode == NodeModeDirect {
		if input.EntryPort != nil {
			return PublishedNode{}, nil, ErrInvalidEntryPortMode
		}
	} else {
		if !relayID.Valid || !sourceServerID.Valid || !currentEntryPort.Valid {
			return PublishedNode{}, nil, ErrInvalidNodeTopology
		}
		switch entryPortMode {
		case EntryPortModeManual:
			if input.EntryPort != nil {
				listenPort = *input.EntryPort
			}
			if listenPort < 1 || listenPort > 65535 {
				return PublishedNode{}, nil, relay.ErrInvalidPort
			}
		case EntryPortModeAuto:
			if input.EntryPort != nil {
				return PublishedNode{}, nil, ErrInvalidEntryPortMode
			}
			if currentEntryPortMode != EntryPortModeAuto {
				listenPort, err = relay.RandomUserRelayPort(ctx, tx, sourceServerID.Int64)
				if err != nil {
					return PublishedNode{}, nil, err
				}
			}
		}
	}

	affected := make(map[int64]struct{})
	if mode == NodeModeRelay && (input.Name != nil || input.Enabled != nil || input.EntryHostMode != nil ||
		input.EntryHost != nil || input.EntryPortMode != nil || input.EntryPort != nil) {
		if !relayID.Valid {
			return PublishedNode{}, nil, ErrInvalidNodeTopology
		}
		serverID, changed, err := s.relays.UpdateSubscriptionRelayTx(ctx, tx, relayID.Int64, relay.SubscriptionRelayUpdateInput{
			Name: name, ListenPort: listenPort, EntryHostMode: entryHostMode, EntryHost: entryHost, Enabled: enabled,
		}, now)
		if err != nil {
			return PublishedNode{}, nil, err
		}
		if changed {
			affected[serverID] = struct{}{}
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE subscription_published_nodes
		 SET name = ?, entry_host_mode = ?, entry_host = ?, entry_port_mode = ?,
		     traffic_multiplier_bp = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		name, entryHostMode, entryHost, entryPortMode, multiplierBP, enabled, now.Unix(), id,
	); err != nil {
		return PublishedNode{}, nil, fmt.Errorf("update published node: %w", err)
	}
	if input.PlanIDsSet {
		changedPlanIDs, err := syncPublishedNodePlansTx(ctx, tx, id, targetProxyID, input.PlanIDs, now)
		if err != nil {
			return PublishedNode{}, nil, err
		}
		for _, planID := range changedPlanIDs {
			if err := s.reconcilePlanSubscribersTx(ctx, tx, planID, now, affected); err != nil {
				return PublishedNode{}, nil, err
			}
		}
	}
	if enabled != (currentEnabled != 0) {
		if err := s.reconcilePublishedNodeSubscribersTx(ctx, tx, id, now, affected); err != nil {
			return PublishedNode{}, nil, err
		}
	}
	mutations, err := bumpAffectedServers(ctx, tx, affected, now)
	if err != nil {
		return PublishedNode{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return PublishedNode{}, nil, fmt.Errorf("commit published node update: %w", err)
	}
	updated, err := s.GetPublishedNode(ctx, id)
	return updated, mutations, err
}

func (s *Service) reconcilePublishedNodeSubscribersTx(
	ctx context.Context,
	tx *sql.Tx,
	nodeID int64,
	now time.Time,
	affected map[int64]struct{},
) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT profiles.user_id
		FROM subscriber_profiles AS profiles
		JOIN subscription_plan_nodes AS mapping ON mapping.plan_id = profiles.plan_id
		WHERE mapping.published_node_id = ? ORDER BY profiles.user_id`, nodeID)
	if err != nil {
		return fmt.Errorf("list published node subscribers: %w", err)
	}
	userIDs := make([]int64, 0)
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return fmt.Errorf("scan published node subscriber: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate published node subscribers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close published node subscribers: %w", err)
	}
	for _, userID := range userIDs {
		if err := s.reconcileSubscriberTx(ctx, tx, userID, now, affected); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) DeletePublishedNode(ctx context.Context, id int64) (*relay.Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin published node deletion: %w", err)
	}
	defer tx.Rollback()

	var mode string
	var relayID sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT mode, relay_id FROM subscription_published_nodes WHERE id = ?`, id,
	).Scan(&mode, &relayID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPublishedNodeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read published node for deletion: %w", err)
	}
	if mode == NodeModeRelay && !relayID.Valid {
		return nil, ErrInvalidNodeTopology
	}
	var planReferences int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscription_plan_nodes WHERE published_node_id = ?`, id,
	).Scan(&planReferences); err != nil {
		return nil, fmt.Errorf("count published node plan references: %w", err)
	}
	if planReferences != 0 {
		return nil, ErrPublishedNodeReferenced
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscription_published_nodes WHERE id = ?`, id); err != nil {
		return nil, fmt.Errorf("delete published node: %w", err)
	}
	var mutation *relay.Mutation
	if relayID.Valid {
		deletedMutation, err := s.relays.DeleteSubscriptionRelayTx(ctx, tx, relayID.Int64, now)
		if err != nil {
			return nil, err
		}
		mutation = &deletedMutation
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit published node deletion: %w", err)
	}
	return mutation, nil
}

func listPublishedNodes(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, condition string, arguments ...any) ([]PublishedNode, error) {
	rows, err := query.QueryContext(ctx, `
		SELECT nodes.id, nodes.name, nodes.mode, nodes.target_proxy_id, target.name,
		 target.server_id, target_server.name, target_server.created_by_role,
		 target.entry_host_mode, target.entry_host, target.listen_port, COALESCE(target_info.public_ipv4, ''),
		 nodes.source_server_id, source_server.name, source_server.created_by_role, nodes.relay_id,
		 nodes.entry_host_mode, nodes.entry_host, nodes.entry_port_mode, relay.listen_port,
		 COALESCE(source_info.public_ipv4, ''), nodes.traffic_multiplier_bp,
		 nodes.enabled, nodes.created_at, nodes.updated_at
		FROM subscription_published_nodes AS nodes
		JOIN proxies AS target ON target.id = nodes.target_proxy_id
		JOIN servers AS target_server ON target_server.id = target.server_id
		LEFT JOIN server_system_info AS target_info ON target_info.server_id = target.server_id
		LEFT JOIN servers AS source_server ON source_server.id = nodes.source_server_id
		LEFT JOIN server_system_info AS source_info ON source_info.server_id = nodes.source_server_id
		LEFT JOIN relays AS relay ON relay.id = nodes.relay_id `+condition, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list published nodes: %w", err)
	}
	defer rows.Close()
	values := make([]PublishedNode, 0)
	for rows.Next() {
		var value PublishedNode
		var sourceServerID, relayID, relayEntryPort sql.NullInt64
		var sourceServerName, sourceCreatorRole, sourcePublicIPv4 sql.NullString
		var targetCreatorRole, targetEntryHostMode, targetEntryHost, targetPublicIPv4 string
		var targetEntryPort int
		var enabled int
		var createdAt, updatedAt int64
		if err := rows.Scan(
			&value.ID, &value.Name, &value.Mode, &value.TargetProxyID, &value.TargetProxyName,
			&value.TargetServerID, &value.TargetServerName, &targetCreatorRole,
			&targetEntryHostMode, &targetEntryHost, &targetEntryPort, &targetPublicIPv4,
			&sourceServerID, &sourceServerName, &sourceCreatorRole, &relayID,
			&value.EntryHostMode, &value.EntryHost, &value.EntryPortMode, &relayEntryPort,
			&sourcePublicIPv4, &value.TrafficMultiplierBP, &enabled, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan published node: %w", err)
		}
		if sourceServerID.Valid {
			id := sourceServerID.Int64
			value.SourceServerID = &id
			value.SourceServerName = sourceServerName.String
		}
		if relayID.Valid {
			id := relayID.Int64
			value.RelayID = &id
		}
		populatePublishedNodeEndpoint(&value, targetEntryHostMode, targetEntryHost, targetEntryPort,
			targetPublicIPv4, sourcePublicIPv4.String, relayEntryPort)
		value.Enabled = enabled != 0
		value.Distributable = targetCreatorRole == "admin" && (value.Mode == NodeModeDirect || sourceCreatorRole.String == "admin")
		value.CreatedAt = time.Unix(createdAt, 0).UTC()
		value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate published nodes: %w", err)
	}
	return values, nil
}

func populatePublishedNodeEndpoint(value *PublishedNode, targetEntryHostMode, targetEntryHost string,
	targetEntryPort int, targetPublicIPv4, sourcePublicIPv4 string, relayEntryPort sql.NullInt64,
) {
	if value.Mode == NodeModeDirect {
		value.EntryPort = targetEntryPort
		if value.EntryHostMode == EntryHostModeManual {
			value.EntryAddress = value.EntryHost
		} else if targetEntryHostMode == proxystore.EntryHostManual {
			value.EntryAddress = targetEntryHost
		} else {
			value.EntryAddress = targetPublicIPv4
		}
		return
	}
	if relayEntryPort.Valid {
		value.EntryPort = int(relayEntryPort.Int64)
	}
	if value.EntryHostMode == EntryHostModeManual {
		value.EntryAddress = value.EntryHost
	} else {
		value.EntryAddress = sourcePublicIPv4
	}
}

func syncPublishedNodePlansTx(
	ctx context.Context,
	tx *sql.Tx,
	nodeID, targetProxyID int64,
	planIDs []int64,
	now time.Time,
) ([]int64, error) {
	if err := validatePublishedNodePlanIDs(planIDs); err != nil {
		return nil, err
	}
	current := make(map[int64]struct{})
	rows, err := tx.QueryContext(ctx,
		`SELECT plan_id FROM subscription_plan_nodes WHERE published_node_id = ?`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list published node plans: %w", err)
	}
	for rows.Next() {
		var planID int64
		if err := rows.Scan(&planID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan published node plan: %w", err)
		}
		current[planID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate published node plans: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close published node plans: %w", err)
	}

	requested := make(map[int64]struct{}, len(planIDs))
	for _, planID := range planIDs {
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM subscription_plans WHERE id = ?`, planID,
		).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPlanNotFound
		} else if err != nil {
			return nil, fmt.Errorf("find subscription plan for published node: %w", err)
		}
		var conflicts int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)
			FROM subscription_plan_nodes AS mapping
			JOIN subscription_published_nodes AS nodes ON nodes.id = mapping.published_node_id
			WHERE mapping.plan_id = ? AND nodes.target_proxy_id = ? AND nodes.id <> ?`,
			planID, targetProxyID, nodeID,
		).Scan(&conflicts); err != nil {
			return nil, fmt.Errorf("check published node plan target: %w", err)
		}
		if conflicts != 0 {
			return nil, ErrInvalidPlanNodes
		}
		requested[planID] = struct{}{}
	}

	changed := make(map[int64]struct{})
	for planID := range current {
		if _, keep := requested[planID]; keep {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM subscription_plan_nodes WHERE plan_id = ? AND published_node_id = ?`,
			planID, nodeID,
		); err != nil {
			return nil, fmt.Errorf("remove published node from subscription plan: %w", err)
		}
		changed[planID] = struct{}{}
	}
	for _, planID := range planIDs {
		if _, exists := current[planID]; exists {
			continue
		}
		var position int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(position), 0) + 1 FROM subscription_plan_nodes WHERE plan_id = ?`, planID,
		).Scan(&position); err != nil {
			return nil, fmt.Errorf("read subscription plan append position: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO subscription_plan_nodes (plan_id, published_node_id, position) VALUES (?, ?, ?)`,
			planID, nodeID, position,
		); err != nil {
			return nil, fmt.Errorf("add published node to subscription plan: %w", err)
		}
		changed[planID] = struct{}{}
	}

	changedPlanIDs := make([]int64, 0, len(changed))
	for planID := range changed {
		if _, err := tx.ExecContext(ctx,
			`UPDATE subscription_plans SET updated_at = ? WHERE id = ?`, now.Unix(), planID,
		); err != nil {
			return nil, fmt.Errorf("touch subscription plan for published node: %w", err)
		}
		changedPlanIDs = append(changedPlanIDs, planID)
	}
	sort.Slice(changedPlanIDs, func(i, j int) bool { return changedPlanIDs[i] < changedPlanIDs[j] })
	return changedPlanIDs, nil
}

func validatePublishedNodePlanIDs(planIDs []int64) error {
	seen := make(map[int64]struct{}, len(planIDs))
	for _, planID := range planIDs {
		if planID <= 0 {
			return ErrInvalidPlanNodes
		}
		if _, exists := seen[planID]; exists {
			return ErrInvalidPlanNodes
		}
		seen[planID] = struct{}{}
	}
	return nil
}

func getProxyTopology(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64, notFound error) (proxyTopology, error) {
	var value proxyTopology
	err := query.QueryRowContext(ctx,
		`SELECT proxies.id, proxies.server_id, proxies.entry_host_mode, proxies.entry_host
		 FROM proxies JOIN servers ON servers.id = proxies.server_id
		 WHERE proxies.id = ? AND servers.archived_at IS NULL`, id,
	).Scan(&value.id, &value.serverID, &value.entryHostMode, &value.entryHost)
	if errors.Is(err, sql.ErrNoRows) {
		return proxyTopology{}, notFound
	}
	if err != nil {
		return proxyTopology{}, fmt.Errorf("read published node proxy: %w", err)
	}
	return value, nil
}

func requireAdminSourceServer(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) error {
	var createdByRole string
	err := query.QueryRowContext(ctx,
		`SELECT created_by_role FROM servers WHERE id = ? AND archived_at IS NULL`, id,
	).Scan(&createdByRole)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSourceServerNotFound
	}
	if err != nil {
		return fmt.Errorf("read published node source server: %w", err)
	}
	if createdByRole != "admin" {
		return ErrServerNotDistributable
	}
	return nil
}

func normalizePublishedNodeEndpoint(mode, hostMode, host, portMode string) (string, string, string, error) {
	hostMode = strings.ToLower(strings.TrimSpace(hostMode))
	portMode = strings.ToLower(strings.TrimSpace(portMode))
	if mode == NodeModeDirect {
		if hostMode == "" {
			hostMode = EntryHostModeInherit
		}
		if portMode == "" {
			portMode = EntryPortModeInherit
		}
		if hostMode != EntryHostModeInherit && hostMode != EntryHostModeManual {
			return "", "", "", ErrInvalidEntryHostMode
		}
		if portMode != EntryPortModeInherit {
			return "", "", "", ErrInvalidEntryPortMode
		}
	} else {
		if hostMode == "" {
			hostMode = EntryHostModeAuto
		}
		if portMode == "" {
			portMode = EntryPortModeAuto
		}
		if hostMode != EntryHostModeAuto && hostMode != EntryHostModeManual {
			return "", "", "", ErrInvalidEntryHostMode
		}
		if portMode != EntryPortModeAuto && portMode != EntryPortModeManual {
			return "", "", "", ErrInvalidEntryPortMode
		}
	}
	if hostMode == EntryHostModeManual {
		_, normalized, err := relay.NormalizeEntryHost(relay.EntryHostManual, host)
		if err != nil {
			return "", "", "", err
		}
		host = normalized
	} else {
		host = ""
	}
	return hostMode, host, portMode, nil
}

func validatePublishedNodeName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > maxNameRunes {
		return "", ErrInvalidNodeName
	}
	return value, nil
}

func normalizeTrafficMultiplierBP(value int) (int, error) {
	if value == 0 {
		value = 100
	}
	return validateTrafficMultiplierBP(value)
}

func validateTrafficMultiplierBP(value int) (int, error) {
	if value < 10 || value > 500 {
		return 0, ErrInvalidTrafficMultiplier
	}
	return value, nil
}

func nullableID(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
