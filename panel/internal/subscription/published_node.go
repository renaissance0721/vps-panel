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
	name, err := validatePublishedNodeName(input.Name)
	if err != nil {
		return PublishedNode{}, nil, err
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode != NodeModeDirect && mode != NodeModeRelay {
		return PublishedNode{}, nil, ErrInvalidNodeMode
	}
	if input.TargetProxyID <= 0 {
		return PublishedNode{}, nil, ErrTargetProxyNotFound
	}
	if mode == NodeModeDirect && input.SourceProxyID != nil {
		return PublishedNode{}, nil, ErrInvalidNodeTopology
	}
	if mode == NodeModeRelay && (input.SourceProxyID == nil || *input.SourceProxyID <= 0) {
		return PublishedNode{}, nil, ErrSourceProxyRequired
	}
	multiplierBP, err := normalizeTrafficMultiplierBP(input.TrafficMultiplierBP)
	if err != nil {
		return PublishedNode{}, nil, err
	}

	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishedNode{}, nil, fmt.Errorf("begin published node creation: %w", err)
	}
	defer tx.Rollback()
	if _, err := getProxyTopology(ctx, tx, input.TargetProxyID, ErrTargetProxyNotFound); err != nil {
		return PublishedNode{}, nil, err
	}

	var relayID any
	var mutation *relay.Mutation
	if mode == NodeModeRelay {
		source, err := getProxyTopology(ctx, tx, *input.SourceProxyID, ErrSourceProxyNotFound)
		if err != nil {
			return PublishedNode{}, nil, err
		}
		port, err := relay.RandomUserRelayPort(ctx, tx, source.serverID)
		if err != nil {
			return PublishedNode{}, nil, err
		}
		createdRelayID, createdMutation, err := s.relays.CreateSubscriptionRelayTx(ctx, tx, relay.CreateInput{
			ServerID: source.serverID, Name: name, ListenAddress: "0.0.0.0", ListenPort: port,
			EntryHostMode: source.entryHostMode, EntryHost: source.entryHost,
			TargetType: relay.TargetProxy, TargetProxyID: &input.TargetProxyID,
			Network: relay.NetworkTCP, Enabled: input.Enabled,
		}, now)
		if err != nil {
			return PublishedNode{}, nil, err
		}
		relayID = createdRelayID
		mutation = &createdMutation
	}

	result, err := tx.ExecContext(ctx,
		`INSERT INTO subscription_published_nodes
		 (name, mode, target_proxy_id, source_proxy_id, relay_id, traffic_multiplier_bp, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, mode, input.TargetProxyID, nullableID(input.SourceProxyID), relayID, multiplierBP,
		input.Enabled, now.Unix(), now.Unix(),
	)
	if err != nil {
		return PublishedNode{}, nil, fmt.Errorf("create published node: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return PublishedNode{}, nil, fmt.Errorf("read published node id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PublishedNode{}, nil, fmt.Errorf("commit published node creation: %w", err)
	}
	created, err := s.GetPublishedNode(ctx, id)
	return created, mutation, err
}

func (s *Service) UpdatePublishedNode(ctx context.Context, id int64, input UpdatePublishedNodeInput) (PublishedNode, []proxystore.Mutation, error) {
	if input.Name == nil && input.TrafficMultiplierBP == nil && input.Enabled == nil {
		return PublishedNode{}, nil, ErrInvalidNodeUpdate
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishedNode{}, nil, fmt.Errorf("begin published node update: %w", err)
	}
	defer tx.Rollback()

	var currentName, mode string
	var currentMultiplierBP, currentEnabled int
	var relayID sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT name, mode, relay_id, traffic_multiplier_bp, enabled
		 FROM subscription_published_nodes WHERE id = ?`, id,
	).Scan(&currentName, &mode, &relayID, &currentMultiplierBP, &currentEnabled)
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

	affected := make(map[int64]struct{})
	if mode == NodeModeRelay && (input.Name != nil || input.Enabled != nil) {
		if !relayID.Valid {
			return PublishedNode{}, nil, ErrInvalidNodeTopology
		}
		serverID, changed, err := s.relays.UpdateSubscriptionRelayTx(ctx, tx, relayID.Int64, name, enabled, now)
		if err != nil {
			return PublishedNode{}, nil, err
		}
		if changed {
			affected[serverID] = struct{}{}
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE subscription_published_nodes
		 SET name = ?, traffic_multiplier_bp = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		name, multiplierBP, enabled, now.Unix(), id,
	); err != nil {
		return PublishedNode{}, nil, fmt.Errorf("update published node: %w", err)
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
		 target.server_id, target_server.name, nodes.source_proxy_id, source.name,
		 source.server_id, source_server.name, nodes.relay_id,
		 relay.listen_port, relay.entry_host_mode, relay.entry_host,
		 COALESCE(source_info.public_ipv4, ''), nodes.traffic_multiplier_bp,
		 nodes.enabled, nodes.created_at, nodes.updated_at
		FROM subscription_published_nodes AS nodes
		JOIN proxies AS target ON target.id = nodes.target_proxy_id
		JOIN servers AS target_server ON target_server.id = target.server_id
		LEFT JOIN proxies AS source ON source.id = nodes.source_proxy_id
		LEFT JOIN servers AS source_server ON source_server.id = source.server_id
		LEFT JOIN server_system_info AS source_info ON source_info.server_id = source.server_id
		LEFT JOIN relays AS relay ON relay.id = nodes.relay_id `+condition, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list published nodes: %w", err)
	}
	defer rows.Close()
	values := make([]PublishedNode, 0)
	for rows.Next() {
		var value PublishedNode
		var sourceProxyID, sourceServerID, relayID, entryPort sql.NullInt64
		var sourceProxyName, sourceServerName, entryHostMode, entryHost, publicIPv4 sql.NullString
		var enabled int
		var createdAt, updatedAt int64
		if err := rows.Scan(
			&value.ID, &value.Name, &value.Mode, &value.TargetProxyID, &value.TargetProxyName,
			&value.TargetServerID, &value.TargetServerName, &sourceProxyID, &sourceProxyName,
			&sourceServerID, &sourceServerName, &relayID, &entryPort, &entryHostMode, &entryHost,
			&publicIPv4, &value.TrafficMultiplierBP, &enabled, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan published node: %w", err)
		}
		if sourceProxyID.Valid {
			id := sourceProxyID.Int64
			value.SourceProxyID = &id
			value.SourceProxyName = sourceProxyName.String
		}
		if sourceServerID.Valid {
			id := sourceServerID.Int64
			value.SourceServerID = &id
			value.SourceServerName = sourceServerName.String
		}
		if relayID.Valid {
			id := relayID.Int64
			value.RelayID = &id
			value.EntryPort = int(entryPort.Int64)
			if entryHostMode.String == relay.EntryHostManual {
				value.EntryAddress = entryHost.String
			} else {
				value.EntryAddress = publicIPv4.String
			}
		}
		value.Enabled = enabled != 0
		value.CreatedAt = time.Unix(createdAt, 0).UTC()
		value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate published nodes: %w", err)
	}
	return values, nil
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
