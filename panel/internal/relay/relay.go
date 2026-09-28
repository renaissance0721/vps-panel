package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Service) List(ctx context.Context) ([]Relay, error) {
	return list(ctx, s.db, `WHERE source.archived_at IS NULL ORDER BY relays.created_at DESC, relays.id DESC`)
}

func (s *Service) ListByOwner(ctx context.Context, userID int64) ([]Relay, error) {
	return list(ctx, s.db,
		`WHERE relays.owner_user_id = ? AND source.archived_at IS NULL ORDER BY relays.created_at DESC, relays.id DESC`,
		userID,
	)
}

func (s *Service) ListUserOwned(ctx context.Context) ([]Relay, error) {
	return list(ctx, s.db,
		`WHERE relays.owner_user_id IS NOT NULL AND source.archived_at IS NULL ORDER BY relays.created_at DESC, relays.id DESC`,
	)
}

func (s *Service) ListUserOwnedByClient(ctx context.Context, clientID int64) ([]Relay, error) {
	return list(ctx, s.db,
		`WHERE relays.owner_user_id IS NOT NULL AND (relays.source_client_id = ? OR relays.target_client_id = ?)
		 AND source.archived_at IS NULL ORDER BY relays.created_at, relays.id`,
		clientID, clientID,
	)
}

func (s *Service) Get(ctx context.Context, id int64) (Relay, error) {
	values, err := list(ctx, s.db, `WHERE relays.id = ? AND source.archived_at IS NULL`, id)
	if err != nil {
		return Relay{}, err
	}
	if len(values) == 0 {
		return Relay{}, ErrNotFound
	}
	return values[0], nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Relay, Mutation, error) {
	value, err := normalizeCreate(input)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if value.TargetType == TargetProxy && value.TargetClientID == nil {
		return Relay{}, Mutation{}, ErrInvalidTargetClient
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("begin relay creation: %w", err)
	}
	defer tx.Rollback()
	id, mutation, err := s.createTx(ctx, tx, value, now)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("commit relay creation: %w", err)
	}
	created, err := s.Get(ctx, id)
	return created, mutation, err
}

func (s *Service) CreateSubscriptionRelayTx(ctx context.Context, tx *sql.Tx, input CreateInput, now time.Time) (int64, Mutation, error) {
	value, err := normalizeCreate(input)
	if err != nil {
		return 0, Mutation{}, err
	}
	if value.OwnerUserID != nil || value.SourceClientID != nil || value.TargetType != TargetProxy ||
		value.TargetProxyID == nil || value.TargetClientID != nil {
		return 0, Mutation{}, ErrInvalidTarget
	}
	return s.createTx(ctx, tx, value, now.UTC().Truncate(time.Second))
}

func (s *Service) createTx(ctx context.Context, tx *sql.Tx, value Relay, now time.Time) (int64, Mutation, error) {
	if err := ensureActiveServer(ctx, tx, value.ServerID); err != nil {
		return 0, Mutation{}, err
	}
	if value.OwnerUserID != nil {
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM relays WHERE owner_user_id = ?`, *value.OwnerUserID,
		).Scan(&count); err != nil {
			return 0, Mutation{}, fmt.Errorf("count user relays: %w", err)
		}
		if count >= MaxUserRelays {
			return 0, Mutation{}, ErrUserRelayLimit
		}
	}
	if err := validateTarget(ctx, tx, &value); err != nil {
		return 0, Mutation{}, err
	}
	if err := ensurePortAvailable(ctx, tx, value.ServerID, value.ListenPort, value.Network, 0, value.SourceClientID); err != nil {
		return 0, Mutation{}, err
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO relays
		 (server_id, owner_user_id, source_client_id, name, listen_address, listen_port, entry_host_mode, entry_host,
		  target_type, target_proxy_id, target_client_id, target_landing_id, target_host, target_port, network, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.ServerID, nullableID(value.OwnerUserID), nullableID(value.SourceClientID), value.Name, value.ListenAddress, value.ListenPort, value.EntryHostMode, value.EntryHost, value.TargetType,
		nullableID(value.TargetProxyID), nullableID(value.TargetClientID), nullableID(value.TargetLandingID), value.TargetHost, nullablePort(value), value.Network,
		value.Enabled, now.Unix(), now.Unix(),
	)
	if err != nil {
		return 0, Mutation{}, fmt.Errorf("create relay: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, Mutation{}, fmt.Errorf("read relay id: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return 0, Mutation{}, err
	}
	return id, Mutation{ServerID: value.ServerID, Version: version}, nil
}

func (s *Service) Update(ctx context.Context, id int64, input UpdateInput) (Relay, Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("begin relay update: %w", err)
	}
	defer tx.Rollback()
	value, err := getForMutation(ctx, tx, id)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if managed, err := subscriptionManaged(ctx, tx, id); err != nil {
		return Relay{}, Mutation{}, err
	} else if managed {
		return Relay{}, Mutation{}, ErrSubscriptionManaged
	}
	value, err = ValidateUpdateInput(value, input)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := validateTarget(ctx, tx, &value); err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := ensurePortAvailable(ctx, tx, value.ServerID, value.ListenPort, value.Network, id, value.SourceClientID); err != nil {
		return Relay{}, Mutation{}, err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE relays SET name = ?, listen_address = ?, listen_port = ?, entry_host_mode = ?, entry_host = ?,
		 target_type = ?, target_proxy_id = ?, target_client_id = ?, target_landing_id = ?, target_host = ?, target_port = ?, network = ?, enabled = ?, updated_at = ?
		 WHERE id = ?`,
		value.Name, value.ListenAddress, value.ListenPort, value.EntryHostMode, value.EntryHost, value.TargetType,
		nullableID(value.TargetProxyID), nullableID(value.TargetClientID), nullableID(value.TargetLandingID), value.TargetHost, nullablePort(value), value.Network,
		value.Enabled, now.Unix(), id,
	)
	if err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("update relay: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("commit relay update: %w", err)
	}
	updated, err := s.Get(ctx, id)
	return updated, Mutation{ServerID: value.ServerID, Version: version}, err
}

func (s *Service) Delete(ctx context.Context, id int64) (Mutation, error) {
	return s.DeleteWithManagedPurge(ctx, id, true)
}

func (s *Service) DeleteWithManagedPurge(ctx context.Context, id int64, allowManagedPurge bool) (Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Mutation{}, fmt.Errorf("begin relay deletion: %w", err)
	}
	defer tx.Rollback()
	value, err := getForMutation(ctx, tx, id)
	if err != nil {
		return Mutation{}, err
	}
	if managed, err := subscriptionManaged(ctx, tx, id); err != nil {
		return Mutation{}, err
	} else if managed {
		return Mutation{}, ErrSubscriptionManaged
	}
	var relayCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM relays WHERE server_id = ?`, value.ServerID,
	).Scan(&relayCount); err != nil {
		return Mutation{}, fmt.Errorf("count server relays: %w", err)
	}
	if relayCount == 1 && !allowManagedPurge {
		return Mutation{}, ErrManagedRuntimePurgeUnsupported
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relays WHERE id = ?`, id); err != nil {
		return Mutation{}, fmt.Errorf("delete relay: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Mutation{}, fmt.Errorf("commit relay deletion: %w", err)
	}
	return Mutation{ServerID: value.ServerID, Version: version}, nil
}

func (s *Service) UpdateSubscriptionRelayTx(ctx context.Context, tx *sql.Tx, id int64, name string, enabled bool, now time.Time) (int64, bool, error) {
	value, err := getForMutation(ctx, tx, id)
	if err != nil {
		return 0, false, err
	}
	managed, err := subscriptionManaged(ctx, tx, id)
	if err != nil {
		return 0, false, err
	}
	if !managed {
		return 0, false, ErrSubscriptionManaged
	}
	now = now.UTC().Truncate(time.Second)
	if _, err := tx.ExecContext(ctx,
		`UPDATE relays SET name = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		name, enabled, now.Unix(), id,
	); err != nil {
		return 0, false, fmt.Errorf("update subscription relay: %w", err)
	}
	return value.ServerID, value.Name != name || value.Enabled != enabled, nil
}

func (s *Service) DeleteSubscriptionRelayTx(ctx context.Context, tx *sql.Tx, id int64, now time.Time) (Mutation, error) {
	value, err := getForMutation(ctx, tx, id)
	if err != nil {
		return Mutation{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relays WHERE id = ?`, id); err != nil {
		return Mutation{}, fmt.Errorf("delete subscription relay: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now.UTC().Truncate(time.Second))
	if err != nil {
		return Mutation{}, err
	}
	return Mutation{ServerID: value.ServerID, Version: version}, nil
}

func subscriptionManaged(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, relayID int64) (bool, error) {
	var managed bool
	if err := query.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM subscription_published_nodes WHERE relay_id = ?)`, relayID,
	).Scan(&managed); err != nil {
		return false, fmt.Errorf("check subscription relay: %w", err)
	}
	return managed, nil
}

func list(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, condition string, arguments ...any) ([]Relay, error) {
	rows, err := query.QueryContext(ctx,
		`SELECT relays.id, relays.server_id, relays.owner_user_id, owner.username,
		 relays.source_client_id, source_proxy.name, source.name, COALESCE(source_info.public_ipv4, ''),
		 relays.name, relays.listen_address, relays.listen_port, relays.entry_host_mode, relays.entry_host, relays.target_type,
		 relays.target_proxy_id, relays.target_client_id, target_client.name, relays.target_landing_id,
		 target_landing.name, target_landing.protocol, target_landing.visibility, target_landing.host, target_landing.port,
		 relays.target_host, relays.target_port,
		 relays.network, relays.enabled, relays.created_at, relays.updated_at,
		 target_proxy.name, target_server.name, target_proxy.listen_port, target_proxy.entry_host_mode,
		 target_proxy.entry_host, COALESCE(target_info.public_ipv4, ''), target_server.archived_at,
		 EXISTS(SELECT 1 FROM subscription_published_nodes WHERE relay_id = relays.id)
		 FROM relays
		 JOIN servers AS source ON source.id = relays.server_id
		 LEFT JOIN users AS owner ON owner.id = relays.owner_user_id
		 LEFT JOIN clients AS source_client ON source_client.id = relays.source_client_id
		 LEFT JOIN proxies AS source_proxy ON source_proxy.id = source_client.proxy_id
		 LEFT JOIN server_system_info AS source_info ON source_info.server_id = source.id
		 LEFT JOIN proxies AS target_proxy ON target_proxy.id = relays.target_proxy_id
		 LEFT JOIN clients AS target_client ON target_client.id = relays.target_client_id
		 LEFT JOIN landing_nodes AS target_landing ON target_landing.id = relays.target_landing_id
		 LEFT JOIN servers AS target_server ON target_server.id = target_proxy.server_id
		 LEFT JOIN server_system_info AS target_info ON target_info.server_id = target_server.id `+condition,
		arguments...,
	)
	if err != nil {
		return nil, fmt.Errorf("list relays: %w", err)
	}
	defer rows.Close()
	values := make([]Relay, 0)
	for rows.Next() {
		var value Relay
		var ownerUserID, sourceClientID sql.NullInt64
		var ownerUsername, sourceProxyName sql.NullString
		var targetProxyID, targetClientID, targetLandingID, storedTargetPort, proxyPort, landingPort, targetArchived sql.NullInt64
		var targetProxyName, targetServerName, targetClientName, targetLandingName, targetLandingProtocol, targetLandingVisibility sql.NullString
		var targetLandingHost, targetEntryMode, targetEntryHost, targetPublicIPv4 sql.NullString
		var enabled int
		var createdAt, updatedAt int64
		if err := rows.Scan(
			&value.ID, &value.ServerID, &ownerUserID, &ownerUsername, &sourceClientID, &sourceProxyName,
			&value.ServerName, &value.ServerPublicIPv4,
			&value.Name, &value.ListenAddress, &value.ListenPort, &value.EntryHostMode, &value.EntryHost, &value.TargetType,
			&targetProxyID, &targetClientID, &targetClientName, &targetLandingID,
			&targetLandingName, &targetLandingProtocol, &targetLandingVisibility, &targetLandingHost, &landingPort,
			&value.TargetHost, &storedTargetPort,
			&value.Network, &enabled, &createdAt, &updatedAt,
			&targetProxyName, &targetServerName, &proxyPort, &targetEntryMode, &targetEntryHost, &targetPublicIPv4, &targetArchived,
			&value.SubscriptionPublished,
		); err != nil {
			return nil, fmt.Errorf("scan relay: %w", err)
		}
		value.Enabled = enabled != 0
		if ownerUserID.Valid {
			id := ownerUserID.Int64
			value.OwnerUserID = &id
			value.OwnerUsername = ownerUsername.String
		}
		if sourceClientID.Valid {
			id := sourceClientID.Int64
			value.SourceClientID = &id
			value.SourceProxyName = sourceProxyName.String
		}
		entryHostMode, entryHost, err := normalizeRelayEntryHost(value.EntryHostMode, value.EntryHost)
		if err != nil || entryHostMode != value.EntryHostMode || entryHost != value.EntryHost {
			return nil, errors.New("invalid stored Relay entry host")
		}
		if value.EntryHostMode == EntryHostManual {
			value.EntryAddress = value.EntryHost
		} else {
			value.EntryAddress = value.ServerPublicIPv4
		}
		value.CreatedAt = time.Unix(createdAt, 0).UTC()
		value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if targetProxyID.Valid {
			id := targetProxyID.Int64
			value.TargetProxyID = &id
		}
		if targetClientID.Valid {
			id := targetClientID.Int64
			value.TargetClientID = &id
			value.TargetClientName = targetClientName.String
		}
		if targetLandingID.Valid {
			id := targetLandingID.Int64
			value.TargetLandingID = &id
		}
		if value.TargetType == TargetManual {
			value.TargetPort = int(storedTargetPort.Int64)
			value.TargetAddressReady = true
		} else if value.TargetType == TargetLanding && targetLandingID.Valid && landingPort.Valid {
			value.TargetLandingName = targetLandingName.String
			value.TargetLandingProtocol = targetLandingProtocol.String
			value.TargetLandingVisibility = targetLandingVisibility.String
			value.TargetHost = targetLandingHost.String
			value.TargetPort = int(landingPort.Int64)
			value.TargetAddressReady = true
		} else if targetProxyID.Valid && proxyPort.Valid && !targetArchived.Valid {
			value.TargetProxyName = targetProxyName.String
			value.TargetServerName = targetServerName.String
			value.TargetPort = int(proxyPort.Int64)
			if targetEntryMode.String == "manual" {
				value.TargetHost = targetEntryHost.String
			} else {
				value.TargetHost = targetPublicIPv4.String
			}
			value.TargetAddressReady = value.TargetHost != ""
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate relays: %w", err)
	}
	return values, nil
}

func getForMutation(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (Relay, error) {
	var value Relay
	var ownerUserID, sourceClientID, targetProxyID, targetClientID, targetLandingID, targetPort sql.NullInt64
	var enabled int
	var decommissionStatus string
	err := query.QueryRowContext(ctx,
		`SELECT relays.id, relays.server_id, relays.owner_user_id, relays.source_client_id, relays.name, relays.listen_address,
		 relays.listen_port, relays.entry_host_mode, relays.entry_host, relays.target_type, relays.target_proxy_id, relays.target_client_id, relays.target_landing_id,
		 relays.target_host, relays.target_port, relays.network, relays.enabled, servers.decommission_status
		 FROM relays JOIN servers ON servers.id = relays.server_id
		 WHERE relays.id = ? AND servers.archived_at IS NULL`, id,
	).Scan(&value.ID, &value.ServerID, &ownerUserID, &sourceClientID, &value.Name, &value.ListenAddress,
		&value.ListenPort, &value.EntryHostMode, &value.EntryHost, &value.TargetType, &targetProxyID, &targetClientID, &targetLandingID,
		&value.TargetHost, &targetPort, &value.Network, &enabled, &decommissionStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return Relay{}, ErrNotFound
	}
	if err != nil {
		return Relay{}, fmt.Errorf("read relay: %w", err)
	}
	if decommissionStatus != "" {
		return Relay{}, ErrServerDecommissioning
	}
	if ownerUserID.Valid {
		id := ownerUserID.Int64
		value.OwnerUserID = &id
	}
	if sourceClientID.Valid {
		id := sourceClientID.Int64
		value.SourceClientID = &id
	}
	if targetProxyID.Valid {
		id := targetProxyID.Int64
		value.TargetProxyID = &id
	}
	if targetClientID.Valid {
		id := targetClientID.Int64
		value.TargetClientID = &id
	}
	if targetLandingID.Valid {
		id := targetLandingID.Int64
		value.TargetLandingID = &id
	}
	if targetPort.Valid {
		value.TargetPort = int(targetPort.Int64)
	}
	value.Enabled = enabled != 0
	return value, nil
}

func ensureActiveServer(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, serverID int64) error {
	var exists int
	var decommissionStatus string
	err := query.QueryRowContext(ctx,
		`SELECT 1, decommission_status FROM servers WHERE id = ? AND archived_at IS NULL`, serverID,
	).Scan(&exists, &decommissionStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrServerNotFound
	}
	if err != nil {
		return fmt.Errorf("validate relay server: %w", err)
	}
	if decommissionStatus != "" {
		return ErrServerDecommissioning
	}
	return nil
}

func nullableID(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullablePort(value Relay) any {
	if value.TargetType == TargetManual {
		return value.TargetPort
	}
	return nil
}
