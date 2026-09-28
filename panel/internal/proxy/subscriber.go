package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

func (s *Service) CreateSubscriberClientTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, proxyID int64,
	name string,
	effectiveEnabled bool,
	now time.Time,
) (int64, int64, error) {
	name, err := validateName(name)
	if err != nil {
		return 0, 0, err
	}
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, userID).Scan(&role); errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrAssignmentUserNotFound
	} else if err != nil {
		return 0, 0, fmt.Errorf("find subscriber for client creation: %w", err)
	}
	if role != "subscriber" {
		return 0, 0, ErrInvalidAssignmentRole
	}
	proxyValue, config, err := getProxyForMutation(ctx, tx, proxyID)
	if err != nil {
		return 0, 0, err
	}
	credential, err := newCredentialForProxy(proxyValue.Protocol, config)
	if err != nil {
		return 0, 0, err
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return 0, 0, fmt.Errorf("encode subscriber client credential: %w", err)
	}
	now = now.UTC().Truncate(time.Second)
	result, err := tx.ExecContext(ctx, `INSERT INTO clients
		(proxy_id, assigned_user_id, name, credential_json, client_udp443, enabled,
		 expires_at, traffic_limit_bytes, billing_period_months, traffic_reset_mode,
		 traffic_reset_weekday, traffic_reset_day, traffic_reset_time,
		 effective_enabled_snapshot, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, 1, NULL, NULL, NULL, 'never', 1, 1, '00:00', ?, ?, ?)`,
		proxyID, userID, name, string(credentialJSON), effectiveEnabled, now.Unix(), now.Unix())
	if err != nil {
		return 0, 0, fmt.Errorf("create subscriber client: %w", err)
	}
	clientID, err := result.LastInsertId()
	if err != nil {
		return 0, 0, fmt.Errorf("read subscriber client id: %w", err)
	}
	return clientID, proxyValue.ServerID, nil
}

func BumpServerVersionsTx(ctx context.Context, tx *sql.Tx, serverIDs []int64, now time.Time) ([]Mutation, error) {
	unique := make(map[int64]struct{}, len(serverIDs))
	for _, serverID := range serverIDs {
		if serverID > 0 {
			unique[serverID] = struct{}{}
		}
	}
	ordered := make([]int64, 0, len(unique))
	for serverID := range unique {
		ordered = append(ordered, serverID)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	mutations := make([]Mutation, 0, len(ordered))
	for _, serverID := range ordered {
		version, err := bumpVersion(ctx, tx, serverID, now.UTC().Truncate(time.Second))
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, Mutation{ServerID: serverID, Version: version})
	}
	return mutations, nil
}

func (s *Service) IsSubscriberClient(ctx context.Context, clientID int64) (bool, error) {
	return subscriptionManagedClient(ctx, s.db, clientID)
}

func subscriptionManagedClient(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, clientID int64) (bool, error) {
	var managed bool
	if err := query.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM subscriber_clients WHERE client_id = ?)`, clientID,
	).Scan(&managed); err != nil {
		return false, fmt.Errorf("check subscriber client: %w", err)
	}
	return managed, nil
}
