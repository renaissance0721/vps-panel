package subscription

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
)

var subscriberTrafficLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func (s *Service) RecordClientTraffic(
	ctx context.Context,
	serverID int64,
	reports []proxystore.ClientTrafficReport,
) ([]proxystore.Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin subscriber traffic report: %w", err)
	}
	defer tx.Rollback()
	affected := make(map[int64]struct{})
	ordinaryChanged, err := proxystore.RecordClientTrafficTx(ctx, tx, serverID, reports, now)
	if err != nil {
		return nil, err
	}
	if ordinaryChanged {
		affected[serverID] = struct{}{}
	}
	userIDs := make(map[int64]struct{})
	for _, report := range reports {
		var userID int64
		err := tx.QueryRowContext(ctx,
			`SELECT user_id FROM subscriber_clients WHERE client_id = ?`, report.ClientID,
		).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("find subscriber traffic owner: %w", err)
		}
		userIDs[userID] = struct{}{}
	}
	for userID := range userIDs {
		if err := s.reconcileSubscriberTx(ctx, tx, userID, now, affected); err != nil {
			return nil, err
		}
	}
	mutations, err := bumpAffectedServers(ctx, tx, affected, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit subscriber traffic report: %w", err)
	}
	return mutations, nil
}

func (s *Service) ResetSubscriberTraffic(ctx context.Context, userID int64) (Subscriber, []proxystore.Mutation, error) {
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Subscriber{}, nil, fmt.Errorf("begin subscriber traffic reset: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM subscriber_profiles WHERE user_id = ?`, userID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return Subscriber{}, nil, ErrSubscriberNotFound
	} else if err != nil {
		return Subscriber{}, nil, fmt.Errorf("find subscriber for traffic reset: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE subscriber_usage SET archived_uplink_bytes = 0,
		archived_downlink_bytes = 0, cycle_started_at = ?, updated_at = ? WHERE user_id = ?`,
		now.Unix(), now.Unix(), userID); err != nil {
		return Subscriber{}, nil, fmt.Errorf("reset subscriber archived traffic: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE client_metrics SET cycle_uplink_bytes = 0,
		cycle_downlink_bytes = 0, cycle_started_at = ?, updated_at = ?
		WHERE client_id IN (SELECT client_id FROM subscriber_clients WHERE user_id = ?)`,
		now.Unix(), now.Unix(), userID); err != nil {
		return Subscriber{}, nil, fmt.Errorf("reset subscriber client traffic: %w", err)
	}
	affected := make(map[int64]struct{})
	if err := s.reconcileSubscriberTx(ctx, tx, userID, now, affected); err != nil {
		return Subscriber{}, nil, err
	}
	mutations, err := bumpAffectedServers(ctx, tx, affected, now)
	if err != nil {
		return Subscriber{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Subscriber{}, nil, fmt.Errorf("commit subscriber traffic reset: %w", err)
	}
	updated, err := s.GetSubscriber(ctx, userID)
	return updated, mutations, err
}

func (s *Service) ReconcileServerSubscribersTx(
	ctx context.Context,
	tx *sql.Tx,
	serverID int64,
	now time.Time,
) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT mapping.user_id
		FROM subscriber_clients AS mapping
		JOIN proxies ON proxies.id = mapping.proxy_id
		WHERE proxies.server_id = ? ORDER BY mapping.user_id`, serverID)
	if err != nil {
		return nil, fmt.Errorf("list server subscribers: %w", err)
	}
	userIDs := make([]int64, 0)
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan server subscriber: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate server subscribers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close server subscribers: %w", err)
	}
	affected := make(map[int64]struct{})
	for _, userID := range userIDs {
		if err := s.reconcileSubscriberTx(ctx, tx, userID, now.UTC().Truncate(time.Second), affected); err != nil {
			return nil, err
		}
	}
	serverIDs := make([]int64, 0, len(affected))
	for affectedServerID := range affected {
		serverIDs = append(serverIDs, affectedServerID)
	}
	return serverIDs, nil
}

func currentSubscriberUsage(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, userID, archivedUplink, archivedDownlink int64) (int64, error) {
	used := saturatingAdd(archivedUplink, archivedDownlink)
	rows, err := query.QueryContext(ctx, `SELECT COALESCE(metrics.cycle_uplink_bytes, 0),
		COALESCE(metrics.cycle_downlink_bytes, 0)
		FROM subscriber_clients AS mapping
		LEFT JOIN client_metrics AS metrics ON metrics.client_id = mapping.client_id
		WHERE mapping.user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("list subscriber client usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var uplink, downlink int64
		if err := rows.Scan(&uplink, &downlink); err != nil {
			return 0, fmt.Errorf("scan subscriber client usage: %w", err)
		}
		used = saturatingAdd(used, saturatingAdd(uplink, downlink))
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate subscriber client usage: %w", err)
	}
	return used, nil
}

func currentSubscriberCycleStart(now time.Time, mode string, day int, resetTime string) (time.Time, error) {
	if mode == ResetModeNever {
		return now.UTC().Truncate(time.Second), nil
	}
	if mode != ResetModeMonthly || day < 1 || day > 31 || !validResetTime(resetTime) {
		return time.Time{}, ErrInvalidTrafficReset
	}
	localNow := now.In(subscriberTrafficLocation)
	parsed, _ := time.Parse("15:04", strings.TrimSpace(resetTime))
	boundary := subscriberMonthlyBoundary(localNow.Year(), localNow.Month(), day, parsed.Hour(), parsed.Minute())
	if localNow.Before(boundary) {
		previous := localNow.AddDate(0, -1, 0)
		boundary = subscriberMonthlyBoundary(previous.Year(), previous.Month(), day, parsed.Hour(), parsed.Minute())
	}
	return boundary.UTC(), nil
}

func nextSubscriberReset(now time.Time, mode string, day int, resetTime string) (*time.Time, error) {
	if mode == ResetModeNever {
		return nil, nil
	}
	current, err := currentSubscriberCycleStart(now, mode, day, resetTime)
	if err != nil {
		return nil, err
	}
	local := current.In(subscriberTrafficLocation).AddDate(0, 1, 0)
	parsed, _ := time.Parse("15:04", resetTime)
	next := subscriberMonthlyBoundary(local.Year(), local.Month(), day, parsed.Hour(), parsed.Minute()).UTC()
	return &next, nil
}

func subscriberMonthlyBoundary(year int, month time.Month, day, hour, minute int) time.Time {
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, subscriberTrafficLocation).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, hour, minute, 0, 0, subscriberTrafficLocation)
}

func saturatingAdd(left, right int64) int64 {
	if right > 0 && left > math.MaxInt64-right {
		return math.MaxInt64
	}
	return left + right
}
