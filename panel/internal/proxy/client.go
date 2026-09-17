package proxy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Service) ListClients(ctx context.Context, proxyID int64) ([]Client, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM proxies JOIN servers ON servers.id = proxies.server_id
		 WHERE proxies.id = ? AND servers.archived_at IS NULL`, proxyID,
	).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("read proxy for client list: %w", err)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT clients.id, clients.proxy_id, clients.name, clients.credential_json,
		 clients.client_udp443, clients.enabled, clients.expires_at, clients.traffic_limit_bytes,
		 clients.traffic_reset_mode, clients.traffic_reset_weekday,
		 clients.traffic_reset_day, clients.traffic_reset_time,
		 clients.effective_enabled_snapshot,
		 clients.created_at, clients.updated_at,
		 proxies.protocol, proxies.config_json,
		 metrics.xray_uplink_bytes, metrics.xray_downlink_bytes,
		 metrics.cycle_uplink_bytes, metrics.cycle_downlink_bytes,
		 metrics.cycle_started_at, metrics.last_activity_at, metrics.updated_at
		 FROM clients JOIN proxies ON proxies.id = clients.proxy_id
		 LEFT JOIN client_metrics AS metrics ON metrics.client_id = clients.id
		 WHERE clients.proxy_id = ? ORDER BY clients.created_at, clients.id`, proxyID,
	)
	if err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}
	defer rows.Close()
	values := make([]Client, 0)
	for rows.Next() {
		value, err := scanClient(rows)
		if err != nil {
			return nil, fmt.Errorf("scan client: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Service) CreateClient(ctx context.Context, proxyID int64, input ClientCreateInput) (Client, Mutation, error) {
	name, err := validateName(input.Name)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	traffic, err := normalizeClientTrafficConfig(input.Traffic)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := normalizeClientExpiration(input.ExpiresAt)
	effectiveEnabled := Client{Enabled: input.Enabled, ExpiresAt: expiresAt}.LifecycleAt(now).EffectiveEnabled
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("begin client creation: %w", err)
	}
	defer tx.Rollback()
	proxyValue, config, err := getProxyForMutation(ctx, tx, proxyID)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	if proxyValue.Protocol == ProtocolShadowsocks && input.ClientUDP443 {
		return Client{}, Mutation{}, ErrShadowsocksClientUDP443
	}
	credential, err := newCredentialForProxy(proxyValue.Protocol, config)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("encode client credential: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO clients
		 (proxy_id, name, credential_json, client_udp443, enabled, expires_at, traffic_limit_bytes,
		  traffic_reset_mode, traffic_reset_weekday, traffic_reset_day, traffic_reset_time,
		  effective_enabled_snapshot, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		proxyID, name, string(credentialJSON), input.ClientUDP443, input.Enabled,
		nullableTime(expiresAt),
		nullableTrafficLimit(traffic.LimitBytes), traffic.ResetMode, traffic.Weekday, traffic.Day,
		traffic.ResetTime, effectiveEnabled, now.Unix(), now.Unix(),
	)
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("create client: %w", err)
	}
	clientID, err := result.LastInsertId()
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("read client id: %w", err)
	}
	version, err := bumpVersion(ctx, tx, proxyValue.ServerID, now)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Client{}, Mutation{}, fmt.Errorf("commit client creation: %w", err)
	}
	value, err := s.GetClient(ctx, clientID)
	return value, Mutation{ServerID: proxyValue.ServerID, Version: version}, err
}

func (s *Service) GetClient(ctx context.Context, id int64) (Client, error) {
	value, err := scanClient(s.db.QueryRowContext(ctx,
		`SELECT clients.id, clients.proxy_id, clients.name, clients.credential_json,
		 clients.client_udp443, clients.enabled, clients.expires_at, clients.traffic_limit_bytes,
		 clients.traffic_reset_mode, clients.traffic_reset_weekday,
		 clients.traffic_reset_day, clients.traffic_reset_time,
		 clients.effective_enabled_snapshot,
		 clients.created_at, clients.updated_at,
		 proxies.protocol, proxies.config_json,
		 metrics.xray_uplink_bytes, metrics.xray_downlink_bytes,
		 metrics.cycle_uplink_bytes, metrics.cycle_downlink_bytes,
		 metrics.cycle_started_at, metrics.last_activity_at, metrics.updated_at
		 FROM clients
		 JOIN proxies ON proxies.id = clients.proxy_id
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN client_metrics AS metrics ON metrics.client_id = clients.id
		 WHERE clients.id = ? AND servers.archived_at IS NULL`, id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Client{}, ErrClientNotFound
	}
	if err != nil {
		return Client{}, fmt.Errorf("get client: %w", err)
	}
	return value, nil
}

func (s *Service) UpdateClient(ctx context.Context, id int64, input ClientUpdateInput) (Client, Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Client{}, Mutation{}, fmt.Errorf("begin client update: %w", err)
	}
	defer tx.Rollback()
	value, serverID, err := getClientForMutation(ctx, tx, id)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	if input.Name != nil {
		value.Name, err = validateName(*input.Name)
		if err != nil {
			return Client{}, Mutation{}, err
		}
	}
	if input.ClientUDP443 != nil {
		if value.Protocol == ProtocolShadowsocks && *input.ClientUDP443 {
			return Client{}, Mutation{}, ErrShadowsocksClientUDP443
		}
		value.ClientUDP443 = *input.ClientUDP443
	}
	if input.Enabled != nil {
		value.Enabled = *input.Enabled
	}
	if input.ExpiresAtSet {
		value.ExpiresAt = normalizeClientExpiration(input.ExpiresAt)
	}
	if input.Traffic != nil {
		traffic, normalizeErr := normalizeClientTrafficConfig(*input.Traffic)
		if normalizeErr != nil {
			return Client{}, Mutation{}, normalizeErr
		}
		value.TrafficLimitBytes = traffic.LimitBytes
		value.TrafficResetMode = traffic.ResetMode
		value.TrafficResetWeekday = traffic.Weekday
		value.TrafficResetDay = traffic.Day
		value.TrafficResetTime = traffic.ResetTime
	}
	effectiveEnabled := value.LifecycleAt(now).EffectiveEnabled
	if _, err := tx.ExecContext(ctx,
		`UPDATE clients SET name = ?, client_udp443 = ?, enabled = ?, expires_at = ?, traffic_limit_bytes = ?,
		 traffic_reset_mode = ?, traffic_reset_weekday = ?, traffic_reset_day = ?,
		 traffic_reset_time = ?, effective_enabled_snapshot = ?, updated_at = ? WHERE id = ?`,
		value.Name, value.ClientUDP443, value.Enabled, nullableTime(value.ExpiresAt), nullableTrafficLimit(value.TrafficLimitBytes),
		value.TrafficResetMode, value.TrafficResetWeekday, value.TrafficResetDay,
		value.TrafficResetTime, effectiveEnabled, now.Unix(), id,
	); err != nil {
		return Client{}, Mutation{}, fmt.Errorf("update client: %w", err)
	}
	version, err := bumpVersion(ctx, tx, serverID, now)
	if err != nil {
		return Client{}, Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Client{}, Mutation{}, fmt.Errorf("commit client update: %w", err)
	}
	updated, err := s.GetClient(ctx, id)
	return updated, Mutation{ServerID: serverID, Version: version}, err
}

func (s *Service) DeleteClient(ctx context.Context, id int64) (Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Mutation{}, fmt.Errorf("begin client deletion: %w", err)
	}
	defer tx.Rollback()
	value, serverID, err := getClientForMutation(ctx, tx, id)
	if err != nil {
		return Mutation{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM clients WHERE proxy_id = ?`, value.ProxyID).Scan(&count); err != nil {
		return Mutation{}, fmt.Errorf("count proxy clients: %w", err)
	}
	if count <= 1 {
		return Mutation{}, ErrLastClient
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM clients WHERE id = ?`, id); err != nil {
		return Mutation{}, fmt.Errorf("delete client: %w", err)
	}
	version, err := bumpVersion(ctx, tx, serverID, now)
	if err != nil {
		return Mutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Mutation{}, fmt.Errorf("commit client deletion: %w", err)
	}
	return Mutation{ServerID: serverID, Version: version}, nil
}

func newVLESSCredential() (storedCredential, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return storedCredential{}, fmt.Errorf("generate client UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return storedCredential{UUID: encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]}, nil
}

func newCredentialForProxy(protocol string, config storedConfig) (storedCredential, error) {
	switch protocol {
	case ProtocolVLESS:
		return newVLESSCredential()
	case ProtocolShadowsocks:
		if config.Shadowsocks == nil {
			return storedCredential{}, errors.New("invalid stored Shadowsocks proxy config")
		}
		password, err := newShadowsocksKey(config.Shadowsocks.Method)
		return storedCredential{Password: password}, err
	default:
		return storedCredential{}, ErrInvalidProtocol
	}
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

func decodeCredential(protocol, value string) (storedCredential, error) {
	var credential storedCredential
	if err := decodeStrict(value, &credential); err != nil {
		return storedCredential{}, errors.New("invalid stored client credential")
	}
	switch protocol {
	case ProtocolVLESS:
		if credential.Password != "" || !validUUID(credential.UUID) {
			return storedCredential{}, errors.New("invalid stored client credential")
		}
	case ProtocolShadowsocks:
		if credential.UUID != "" {
			return storedCredential{}, ErrInvalidShadowsocksCredential
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(credential.Password)
		if err != nil || (len(decoded) != 16 && len(decoded) != 32) {
			return storedCredential{}, ErrInvalidShadowsocksCredential
		}
	default:
		return storedCredential{}, ErrInvalidProtocol
	}
	return credential, nil
}

func scanClient(row rowScanner) (Client, error) {
	var value Client
	var credentialJSON, configJSON string
	var udp443, enabled, effectiveEnabled int
	var expiresAt, trafficLimit sql.NullInt64
	var createdAt, updatedAt int64
	var xrayUplink, xrayDownlink, cycleUplink, cycleDownlink sql.NullInt64
	var cycleStartedAt, lastActivityAt, metricsUpdatedAt sql.NullInt64
	if err := row.Scan(
		&value.ID, &value.ProxyID, &value.Name, &credentialJSON, &udp443, &enabled,
		&expiresAt, &trafficLimit, &value.TrafficResetMode, &value.TrafficResetWeekday,
		&value.TrafficResetDay, &value.TrafficResetTime,
		&effectiveEnabled, &createdAt, &updatedAt, &value.Protocol, &configJSON,
		&xrayUplink, &xrayDownlink, &cycleUplink, &cycleDownlink,
		&cycleStartedAt, &lastActivityAt, &metricsUpdatedAt,
	); err != nil {
		return Client{}, err
	}
	credential, err := decodeCredential(value.Protocol, credentialJSON)
	if err != nil {
		return Client{}, err
	}
	config, err := decodeConfig(value.Protocol, configJSON)
	if err != nil {
		return Client{}, err
	}
	if value.Protocol == ProtocolShadowsocks && !validShadowsocksKey(credential.Password, config.Shadowsocks.Method) {
		return Client{}, ErrInvalidShadowsocksCredential
	}
	value.UUID = credential.UUID
	value.Password = credential.Password
	value.ClientUDP443 = udp443 != 0
	value.Enabled = enabled != 0
	value.ExpiresAt = nullableTimeValue(expiresAt)
	value.effectiveEnabled = effectiveEnabled != 0
	if trafficLimit.Valid && trafficLimit.Int64 > 0 {
		limit := trafficLimit.Int64
		value.TrafficLimitBytes = &limit
	}
	value.CreatedAt = time.Unix(createdAt, 0).UTC()
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if metricsUpdatedAt.Valid {
		value.Metrics = clientMetricsFromDatabase(
			xrayUplink.Int64, xrayDownlink.Int64, cycleUplink.Int64, cycleDownlink.Int64,
			cycleStartedAt.Int64, lastActivityAt, metricsUpdatedAt.Int64,
		)
	}
	return value, nil
}

func summarizeClient(value Client) ClientSummary {
	return ClientSummary{
		ID: value.ID, ProxyID: value.ProxyID, Name: value.Name,
		ClientUDP443: value.ClientUDP443, Enabled: value.Enabled, ExpiresAt: value.ExpiresAt,
		TrafficLimitBytes: value.TrafficLimitBytes, TrafficResetMode: value.TrafficResetMode,
		TrafficResetWeekday: value.TrafficResetWeekday, TrafficResetDay: value.TrafficResetDay,
		TrafficResetTime: value.TrafficResetTime, Metrics: value.Metrics,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func getClientForMutation(ctx context.Context, tx *sql.Tx, id int64) (Client, int64, error) {
	var serverID int64
	var value Client
	var credentialJSON, configJSON string
	var udp443, enabled, effectiveEnabled int
	var expiresAt, trafficLimit sql.NullInt64
	var cycleUplink, cycleDownlink int64
	var createdAt, updatedAt int64
	err := tx.QueryRowContext(ctx,
		`SELECT clients.id, clients.proxy_id, clients.name, clients.credential_json,
		 clients.client_udp443, clients.enabled, clients.expires_at, clients.traffic_limit_bytes,
		 clients.traffic_reset_mode, clients.traffic_reset_weekday,
		 clients.traffic_reset_day, clients.traffic_reset_time,
		 clients.effective_enabled_snapshot,
		 clients.created_at, clients.updated_at,
		 proxies.protocol, proxies.config_json, proxies.server_id,
		 COALESCE(metrics.cycle_uplink_bytes, 0), COALESCE(metrics.cycle_downlink_bytes, 0)
		 FROM clients JOIN proxies ON proxies.id = clients.proxy_id
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN client_metrics AS metrics ON metrics.client_id = clients.id
		 WHERE clients.id = ? AND servers.archived_at IS NULL`, id,
	).Scan(
		&value.ID, &value.ProxyID, &value.Name, &credentialJSON, &udp443, &enabled,
		&expiresAt, &trafficLimit, &value.TrafficResetMode, &value.TrafficResetWeekday,
		&value.TrafficResetDay, &value.TrafficResetTime,
		&effectiveEnabled, &createdAt, &updatedAt, &value.Protocol, &configJSON, &serverID,
		&cycleUplink, &cycleDownlink,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Client{}, 0, ErrClientNotFound
	}
	if err != nil {
		return Client{}, 0, fmt.Errorf("read client: %w", err)
	}
	credential, err := decodeCredential(value.Protocol, credentialJSON)
	if err != nil {
		return Client{}, 0, err
	}
	config, err := decodeConfig(value.Protocol, configJSON)
	if err != nil {
		return Client{}, 0, err
	}
	if value.Protocol == ProtocolShadowsocks && !validShadowsocksKey(credential.Password, config.Shadowsocks.Method) {
		return Client{}, 0, ErrInvalidShadowsocksCredential
	}
	value.UUID, value.Password, value.ClientUDP443, value.Enabled = credential.UUID, credential.Password, udp443 != 0, enabled != 0
	value.ExpiresAt = nullableTimeValue(expiresAt)
	value.effectiveEnabled = effectiveEnabled != 0
	if trafficLimit.Valid && trafficLimit.Int64 > 0 {
		limit := trafficLimit.Int64
		value.TrafficLimitBytes = &limit
	}
	value.Metrics = &ClientMetrics{CycleUplinkBytes: cycleUplink, CycleDownlinkBytes: cycleDownlink}
	value.CreatedAt, value.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return value, serverID, nil
}

func normalizeClientExpiration(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC().Truncate(time.Second)
	return &normalized
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Unix()
}

func nullableTimeValue(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.Unix(value.Int64, 0).UTC()
	return &result
}
