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
	if err := ensureActiveServer(ctx, tx, value.ServerID); err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := validateTarget(ctx, tx, &value); err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := ensurePortAvailable(ctx, tx, value.ServerID, value.ListenPort, value.Network, 0); err != nil {
		return Relay{}, Mutation{}, err
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO relays
		 (server_id, name, listen_address, listen_port, entry_host_mode, entry_host,
		  target_type, target_proxy_id, target_client_id, target_host, target_port, network, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.ServerID, value.Name, value.ListenAddress, value.ListenPort, value.EntryHostMode, value.EntryHost, value.TargetType,
		nullableID(value.TargetProxyID), nullableID(value.TargetClientID), value.TargetHost, nullablePort(value), value.Network,
		value.Enabled, now.Unix(), now.Unix(),
	)
	if err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("create relay: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("read relay id: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Relay{}, Mutation{}, fmt.Errorf("commit relay creation: %w", err)
	}
	created, err := s.Get(ctx, id)
	return created, Mutation{ServerID: value.ServerID, Version: version}, err
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
	targetProxyChanged := input.TargetProxyID != nil && (value.TargetProxyID == nil || *value.TargetProxyID != *input.TargetProxyID)
	if input.Name != nil {
		value.Name = *input.Name
	}
	if input.ListenAddress != nil {
		value.ListenAddress = *input.ListenAddress
	}
	if input.ListenPort != nil {
		value.ListenPort = *input.ListenPort
	}
	if input.EntryHostMode != nil {
		value.EntryHostMode = *input.EntryHostMode
	}
	if input.EntryHost != nil {
		value.EntryHost = *input.EntryHost
	}
	if input.TargetType != nil {
		value.TargetType = *input.TargetType
	}
	if input.TargetProxyID != nil {
		id := *input.TargetProxyID
		value.TargetProxyID = &id
	}
	if input.TargetClientID != nil {
		id := *input.TargetClientID
		value.TargetClientID = &id
	} else if targetProxyChanged {
		// Selecting a different proxy must also select one of its clients.
		value.TargetClientID = nil
	}
	if input.TargetHost != nil {
		value.TargetHost = *input.TargetHost
	}
	if input.TargetPort != nil {
		value.TargetPort = *input.TargetPort
	}
	if input.Network != nil {
		value.Network = *input.Network
	}
	if input.Enabled != nil {
		value.Enabled = *input.Enabled
	}
	value, err = normalizeRelay(value)
	if err != nil {
		return Relay{}, Mutation{}, err
	}
	if value.TargetType == TargetProxy && targetProxyChanged && value.TargetClientID == nil {
		return Relay{}, Mutation{}, ErrInvalidTargetClient
	}
	if err := validateTarget(ctx, tx, &value); err != nil {
		return Relay{}, Mutation{}, err
	}
	if err := ensurePortAvailable(ctx, tx, value.ServerID, value.ListenPort, value.Network, id); err != nil {
		return Relay{}, Mutation{}, err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE relays SET name = ?, listen_address = ?, listen_port = ?, entry_host_mode = ?, entry_host = ?,
		 target_type = ?, target_proxy_id = ?, target_client_id = ?, target_host = ?, target_port = ?, network = ?, enabled = ?, updated_at = ?
		 WHERE id = ?`,
		value.Name, value.ListenAddress, value.ListenPort, value.EntryHostMode, value.EntryHost, value.TargetType,
		nullableID(value.TargetProxyID), nullableID(value.TargetClientID), value.TargetHost, nullablePort(value), value.Network,
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

func list(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, condition string, arguments ...any) ([]Relay, error) {
	rows, err := query.QueryContext(ctx,
		`SELECT relays.id, relays.server_id, source.name, COALESCE(source_info.public_ipv4, ''),
		 relays.name, relays.listen_address, relays.listen_port, relays.entry_host_mode, relays.entry_host, relays.target_type,
		 relays.target_proxy_id, relays.target_client_id, relays.target_host, relays.target_port,
		 relays.network, relays.enabled, relays.created_at, relays.updated_at,
		 target_proxy.name, target_proxy.listen_port, target_proxy.entry_host_mode,
		 target_proxy.entry_host, COALESCE(target_info.public_ipv4, ''), target_server.archived_at
		 FROM relays
		 JOIN servers AS source ON source.id = relays.server_id
		 LEFT JOIN server_system_info AS source_info ON source_info.server_id = source.id
		 LEFT JOIN proxies AS target_proxy ON target_proxy.id = relays.target_proxy_id
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
		var targetProxyID, targetClientID, storedTargetPort, proxyPort, targetArchived sql.NullInt64
		var targetProxyName, targetEntryMode, targetEntryHost, targetPublicIPv4 sql.NullString
		var enabled int
		var createdAt, updatedAt int64
		if err := rows.Scan(
			&value.ID, &value.ServerID, &value.ServerName, &value.ServerPublicIPv4,
			&value.Name, &value.ListenAddress, &value.ListenPort, &value.EntryHostMode, &value.EntryHost, &value.TargetType,
			&targetProxyID, &targetClientID, &value.TargetHost, &storedTargetPort,
			&value.Network, &enabled, &createdAt, &updatedAt,
			&targetProxyName, &proxyPort, &targetEntryMode, &targetEntryHost, &targetPublicIPv4, &targetArchived,
		); err != nil {
			return nil, fmt.Errorf("scan relay: %w", err)
		}
		value.Enabled = enabled != 0
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
		}
		if value.TargetType == TargetManual {
			value.TargetPort = int(storedTargetPort.Int64)
			value.TargetAddressReady = true
		} else if targetProxyID.Valid && proxyPort.Valid && !targetArchived.Valid {
			value.TargetProxyName = targetProxyName.String
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
	var targetProxyID, targetClientID, targetPort sql.NullInt64
	var enabled int
	err := query.QueryRowContext(ctx,
		`SELECT relays.id, relays.server_id, relays.name, relays.listen_address,
		 relays.listen_port, relays.entry_host_mode, relays.entry_host, relays.target_type, relays.target_proxy_id, relays.target_client_id,
		 relays.target_host, relays.target_port, relays.network, relays.enabled
		 FROM relays JOIN servers ON servers.id = relays.server_id
		 WHERE relays.id = ? AND servers.archived_at IS NULL`, id,
	).Scan(&value.ID, &value.ServerID, &value.Name, &value.ListenAddress,
		&value.ListenPort, &value.EntryHostMode, &value.EntryHost, &value.TargetType, &targetProxyID, &targetClientID,
		&value.TargetHost, &targetPort, &value.Network, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Relay{}, ErrNotFound
	}
	if err != nil {
		return Relay{}, fmt.Errorf("read relay: %w", err)
	}
	if targetProxyID.Valid {
		id := targetProxyID.Int64
		value.TargetProxyID = &id
	}
	if targetClientID.Valid {
		id := targetClientID.Int64
		value.TargetClientID = &id
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
	err := query.QueryRowContext(ctx,
		`SELECT 1 FROM servers WHERE id = ? AND archived_at IS NULL`, serverID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrServerNotFound
	}
	if err != nil {
		return fmt.Errorf("validate relay server: %w", err)
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
