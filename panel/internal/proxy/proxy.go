package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

type preparedCreate struct {
	protocol, name, clientName, entryHostMode, entryHost string
	configJSON, credentialJSON                           []byte
}

func prepareCreateInput(input CreateInput) (preparedCreate, error) {
	protocol, err := normalizeProtocol(input.Protocol)
	if err != nil {
		return preparedCreate{}, err
	}
	name, err := validateName(input.Name)
	if err != nil {
		return preparedCreate{}, err
	}
	clientName, err := validateName(input.FirstClientName)
	if err != nil {
		return preparedCreate{}, err
	}
	if input.ServerID <= 0 {
		return preparedCreate{}, ErrServerNotFound
	}
	if err := validatePort(input.ListenPort); err != nil {
		return preparedCreate{}, err
	}
	entryHostMode, entryHost, err := normalizeEntryHost(input.EntryHostMode, input.EntryHost)
	if err != nil {
		return preparedCreate{}, err
	}
	var config storedConfig
	var credential storedCredential
	switch protocol {
	case ProtocolVLESS:
		config, err = newStoredConfig(input.Security, input.ServerName, input.TLSMode, input.Certificate, input.PrivateKey, input.RealityTarget)
		if err == nil {
			credential, err = newVLESSCredential()
		}
	case ProtocolShadowsocks:
		if input.FirstClientUDP443 {
			return preparedCreate{}, ErrShadowsocksClientUDP443
		}
		config, credential, err = newShadowsocksConfigAndCredential(input.Method)
	}
	if err != nil {
		return preparedCreate{}, err
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return preparedCreate{}, fmt.Errorf("encode proxy config: %w", err)
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return preparedCreate{}, fmt.Errorf("encode client credential: %w", err)
	}
	return preparedCreate{
		protocol: protocol, name: name, clientName: clientName,
		entryHostMode: entryHostMode, entryHost: entryHost,
		configJSON: configJSON, credentialJSON: credentialJSON,
	}, nil
}

func ValidateCreateInput(input CreateInput) error {
	_, err := prepareCreateInput(input)
	return err
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Proxy, Mutation, error) {
	prepared, err := prepareCreateInput(input)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("begin proxy creation: %w", err)
	}
	defer tx.Rollback()

	if err := ensureActiveServer(ctx, tx, input.ServerID); err != nil {
		return Proxy{}, Mutation{}, err
	}
	if err := relaystore.ProxyPortAvailable(ctx, tx, input.ServerID, input.ListenPort, prepared.protocol); err != nil {
		if errors.Is(err, relaystore.ErrPortConflict) {
			return Proxy{}, Mutation{}, ErrPortConflict
		}
		return Proxy{}, Mutation{}, err
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO proxies
		 (server_id, name, protocol, listen_port, entry_host_mode, entry_host, enabled, config_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.ServerID, prepared.name, prepared.protocol, input.ListenPort, prepared.entryHostMode, prepared.entryHost, input.Enabled,
		string(prepared.configJSON), now.Unix(), now.Unix(),
	)
	if isUniqueConstraint(err) {
		return Proxy{}, Mutation{}, ErrPortConflict
	}
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("create proxy: %w", err)
	}
	proxyID, err := result.LastInsertId()
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("read proxy id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO clients
		 (proxy_id, name, credential_json, client_udp443, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 1, ?, ?)`,
		proxyID, prepared.clientName, string(prepared.credentialJSON), input.FirstClientUDP443, now.Unix(), now.Unix(),
	); err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("create first proxy client: %w", err)
	}
	version, err := bumpVersion(ctx, tx, input.ServerID, now)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("commit proxy creation: %w", err)
	}
	value, err := s.Get(ctx, proxyID)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	return value, Mutation{ServerID: input.ServerID, Version: version}, nil
}

func (s *Service) List(ctx context.Context) ([]Proxy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT proxies.id, proxies.server_id, servers.name, system_info.ipv4, system_info.ipv6,
		 system_info.public_ipv4, proxies.name, proxies.protocol, proxies.listen_port,
		 proxies.entry_host_mode, proxies.entry_host, proxies.enabled,
		 proxies.config_json, proxies.created_at, proxies.updated_at
		 FROM proxies
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE servers.archived_at IS NULL
		 ORDER BY proxies.created_at DESC, proxies.id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list proxies: %w", err)
	}
	defer rows.Close()
	values := make([]Proxy, 0)
	for rows.Next() {
		value, _, err := scanProxy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan proxy: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxies: %w", err)
	}
	return values, nil
}

func (s *Service) Get(ctx context.Context, id int64) (Proxy, error) {
	value, _, err := scanProxy(s.db.QueryRowContext(ctx,
		`SELECT proxies.id, proxies.server_id, servers.name, system_info.ipv4, system_info.ipv6,
		 system_info.public_ipv4, proxies.name, proxies.protocol, proxies.listen_port,
		 proxies.entry_host_mode, proxies.entry_host, proxies.enabled,
		 proxies.config_json, proxies.created_at, proxies.updated_at
		 FROM proxies
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE proxies.id = ? AND servers.archived_at IS NULL`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Proxy{}, ErrNotFound
	}
	if err != nil {
		return Proxy{}, fmt.Errorf("get proxy: %w", err)
	}
	clients, err := s.ListClients(ctx, id)
	if err != nil {
		return Proxy{}, err
	}
	value.Clients = make([]ClientSummary, 0, len(clients))
	for _, client := range clients {
		value.Clients = append(value.Clients, summarizeClient(client))
	}
	return value, nil
}

type proxyUpdateQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func prepareUpdate(ctx context.Context, query proxyUpdateQuery, id int64, input UpdateInput) (Proxy, storedConfig, error) {
	value, config, err := getProxyForMutation(ctx, query, id)
	if err != nil {
		return Proxy{}, storedConfig{}, err
	}
	if input.Protocol != nil {
		protocol, protocolErr := normalizeProtocol(*input.Protocol)
		if protocolErr != nil {
			return Proxy{}, storedConfig{}, protocolErr
		}
		if protocol != value.Protocol {
			return Proxy{}, storedConfig{}, ErrImmutableProtocol
		}
	}
	if input.Name != nil {
		value.Name, err = validateName(*input.Name)
		if err != nil {
			return Proxy{}, storedConfig{}, err
		}
	}
	if input.ListenPort != nil {
		if err := validatePort(*input.ListenPort); err != nil {
			return Proxy{}, storedConfig{}, err
		}
		value.ListenPort = *input.ListenPort
	}
	entryHostMode, entryHost := value.EntryHostMode, value.EntryHost
	if input.EntryHostMode != nil {
		entryHostMode = *input.EntryHostMode
	}
	if input.EntryHost != nil {
		entryHost = *input.EntryHost
	}
	value.EntryHostMode, value.EntryHost, err = normalizeEntryHost(entryHostMode, entryHost)
	if err != nil {
		return Proxy{}, storedConfig{}, err
	}
	if input.Enabled != nil {
		value.Enabled = *input.Enabled
	}
	switch value.Protocol {
	case ProtocolVLESS:
		if input.Method != nil {
			return Proxy{}, storedConfig{}, ErrInvalidShadowsocksMethod
		}
		if err := updateStoredConfig(&config, input); err != nil {
			return Proxy{}, storedConfig{}, err
		}
	case ProtocolShadowsocks:
		if config.Shadowsocks == nil {
			return Proxy{}, storedConfig{}, errors.New("invalid stored Shadowsocks proxy config")
		}
		if input.Method != nil {
			method, methodErr := normalizeShadowsocksMethod(*input.Method)
			if methodErr != nil {
				return Proxy{}, storedConfig{}, methodErr
			}
			if method != config.Shadowsocks.Method {
				return Proxy{}, storedConfig{}, ErrImmutableShadowsocksMethod
			}
		}
		if input.Security != nil || input.TLSMode != nil || input.ServerName != nil || input.Certificate != nil || input.PrivateKey != nil || input.RealityTarget != nil {
			return Proxy{}, storedConfig{}, ErrInvalidShadowsocksUpdate
		}
	default:
		return Proxy{}, storedConfig{}, ErrInvalidProtocol
	}
	if err := relaystore.ProxyPortAvailable(ctx, query, value.ServerID, value.ListenPort, value.Protocol); err != nil {
		if errors.Is(err, relaystore.ErrPortConflict) {
			return Proxy{}, storedConfig{}, ErrPortConflict
		}
		return Proxy{}, storedConfig{}, err
	}
	value.Config = publicConfig(config)
	return value, config, nil
}

func (s *Service) ValidateUpdate(ctx context.Context, id int64, input UpdateInput) (Proxy, error) {
	value, _, err := prepareUpdate(ctx, s.db, id, input)
	return value, err
}

func (s *Service) Update(ctx context.Context, id int64, input UpdateInput) (Proxy, Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("begin proxy update: %w", err)
	}
	defer tx.Rollback()
	value, config, err := prepareUpdate(ctx, tx, id, input)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("encode proxy config: %w", err)
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE proxies SET name = ?, listen_port = ?, entry_host_mode = ?, entry_host = ?, enabled = ?, config_json = ?, updated_at = ?
		 WHERE id = ?`,
		value.Name, value.ListenPort, value.EntryHostMode, value.EntryHost, value.Enabled, string(configJSON), now.Unix(), id,
	)
	if isUniqueConstraint(err) {
		return Proxy{}, Mutation{}, ErrPortConflict
	}
	if err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("update proxy: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return Proxy{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Proxy{}, Mutation{}, fmt.Errorf("commit proxy update: %w", err)
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
		return Mutation{}, fmt.Errorf("begin proxy deletion: %w", err)
	}
	defer tx.Rollback()
	value, _, err := getProxyForMutation(ctx, tx, id)
	if err != nil {
		return Mutation{}, err
	}
	referenced, err := relaystore.IsProxyReferenced(ctx, tx, id)
	if err != nil {
		return Mutation{}, err
	}
	if referenced {
		return Mutation{}, ErrReferencedByRelay
	}
	var proxyCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM proxies WHERE server_id = ?`, value.ServerID,
	).Scan(&proxyCount); err != nil {
		return Mutation{}, fmt.Errorf("count server proxies: %w", err)
	}
	if proxyCount == 1 && !allowManagedPurge {
		return Mutation{}, ErrManagedRuntimePurgeUnsupported
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM proxies WHERE id = ?`, id); err != nil {
		return Mutation{}, fmt.Errorf("delete proxy: %w", err)
	}
	version, err := bumpVersion(ctx, tx, value.ServerID, now)
	if err != nil {
		return Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Mutation{}, fmt.Errorf("commit proxy deletion: %w", err)
	}
	return Mutation{ServerID: value.ServerID, Version: version}, nil
}

func scanProxy(row rowScanner) (Proxy, storedConfig, error) {
	var value Proxy
	var ipv4JSON, ipv6JSON, publicIPv4 sql.NullString
	var enabled int
	var configJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.ServerID, &value.ServerName, &ipv4JSON, &ipv6JSON,
		&publicIPv4, &value.Name, &value.Protocol, &value.ListenPort, &value.EntryHostMode, &value.EntryHost, &enabled,
		&configJSON, &createdAt, &updatedAt); err != nil {
		return Proxy{}, storedConfig{}, err
	}
	entryHostMode, entryHost, err := normalizeEntryHost(value.EntryHostMode, value.EntryHost)
	if err != nil || entryHostMode != value.EntryHostMode || entryHost != value.EntryHost {
		return Proxy{}, storedConfig{}, errors.New("invalid stored proxy entry host")
	}
	config, err := decodeConfig(value.Protocol, configJSON)
	if err != nil {
		return Proxy{}, storedConfig{}, err
	}
	value.ServerPublicIPv4 = publicIPv4.String
	value.EntryAddress, err = resolveEntryAddress(value.EntryHostMode, value.EntryHost, value.ServerPublicIPv4)
	if err != nil && !errors.Is(err, ErrConnectionAddressUnavailable) {
		return Proxy{}, storedConfig{}, err
	}
	value.Enabled = enabled != 0
	value.Config = publicConfig(config)
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	_ = json.Unmarshal([]byte(ipv4JSON.String), &value.ServerIPv4)
	_ = json.Unmarshal([]byte(ipv6JSON.String), &value.ServerIPv6)
	if value.ServerIPv4 == nil {
		value.ServerIPv4 = []string{}
	}
	if value.ServerIPv6 == nil {
		value.ServerIPv6 = []string{}
	}
	return value, config, nil
}

func getProxyForMutation(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (Proxy, storedConfig, error) {
	var decommissionStatus string
	err := query.QueryRowContext(ctx,
		`SELECT servers.decommission_status FROM proxies
		 JOIN servers ON servers.id = proxies.server_id
		 WHERE proxies.id = ? AND servers.archived_at IS NULL`, id,
	).Scan(&decommissionStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return Proxy{}, storedConfig{}, ErrNotFound
	}
	if err != nil {
		return Proxy{}, storedConfig{}, fmt.Errorf("read proxy server lifecycle: %w", err)
	}
	if decommissionStatus != "" {
		return Proxy{}, storedConfig{}, ErrServerDecommissioning
	}
	value, config, err := scanProxy(query.QueryRowContext(ctx,
		`SELECT proxies.id, proxies.server_id, servers.name, system_info.ipv4, system_info.ipv6,
		 system_info.public_ipv4, proxies.name, proxies.protocol, proxies.listen_port,
		 proxies.entry_host_mode, proxies.entry_host, proxies.enabled,
		 proxies.config_json, proxies.created_at, proxies.updated_at
		 FROM proxies JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN server_system_info AS system_info ON system_info.server_id = servers.id
		 WHERE proxies.id = ? AND servers.archived_at IS NULL`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Proxy{}, storedConfig{}, ErrNotFound
	}
	if err != nil {
		return Proxy{}, storedConfig{}, fmt.Errorf("read proxy: %w", err)
	}
	return value, config, nil
}

func ensureActiveServer(ctx context.Context, tx *sql.Tx, serverID int64) error {
	var id int64
	var decommissionStatus string
	if err := tx.QueryRowContext(ctx,
		`SELECT id, decommission_status FROM servers WHERE id = ? AND archived_at IS NULL`, serverID,
	).Scan(&id, &decommissionStatus); errors.Is(err, sql.ErrNoRows) {
		return ErrServerNotFound
	} else if err != nil {
		return fmt.Errorf("read proxy server: %w", err)
	}
	if decommissionStatus != "" {
		return ErrServerDecommissioning
	}
	return nil
}
