package proxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

const clientStatsIdentifierPrefix = "vp-client-"

var ErrInvalidClientTraffic = errors.New("invalid client traffic report")

type ClientMetrics struct {
	XrayUplinkBytes    int64
	XrayDownlinkBytes  int64
	CycleUplinkBytes   int64
	CycleDownlinkBytes int64
	CycleStartedAt     time.Time
	LastActivityAt     *time.Time
	UpdatedAt          time.Time
}

type ClientTrafficReport struct {
	ClientID      int64
	UplinkBytes   int64
	DownlinkBytes int64
}

func (s *Service) RecordClientTraffic(ctx context.Context, serverID int64, reports []ClientTrafficReport) error {
	if serverID <= 0 {
		return ErrInvalidClientTraffic
	}
	seen := make(map[int64]struct{}, len(reports))
	for _, report := range reports {
		if report.ClientID <= 0 || report.UplinkBytes < 0 || report.DownlinkBytes < 0 {
			return ErrInvalidClientTraffic
		}
		if _, exists := seen[report.ClientID]; exists {
			return ErrInvalidClientTraffic
		}
		seen[report.ClientID] = struct{}{}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin client traffic report: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Truncate(time.Second)
	for _, report := range reports {
		if err := recordClientTraffic(ctx, tx, serverID, report, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit client traffic report: %w", err)
	}
	return nil
}

func recordClientTraffic(ctx context.Context, tx *sql.Tx, serverID int64, report ClientTrafficReport, now time.Time) error {
	var previousUplink, previousDownlink, cycleUplink, cycleDownlink sql.NullInt64
	var cycleStartedAt, lastActivityAt sql.NullInt64
	err := tx.QueryRowContext(ctx,
		`SELECT metrics.xray_uplink_bytes, metrics.xray_downlink_bytes,
		 metrics.cycle_uplink_bytes, metrics.cycle_downlink_bytes,
		 metrics.cycle_started_at, metrics.last_activity_at
		 FROM clients
		 JOIN proxies ON proxies.id = clients.proxy_id
		 JOIN servers ON servers.id = proxies.server_id
		 LEFT JOIN client_metrics AS metrics ON metrics.client_id = clients.id
		 WHERE clients.id = ? AND proxies.server_id = ? AND servers.archived_at IS NULL`,
		report.ClientID, serverID,
	).Scan(&previousUplink, &previousDownlink, &cycleUplink, &cycleDownlink, &cycleStartedAt, &lastActivityAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidClientTraffic
	}
	if err != nil {
		return fmt.Errorf("read client traffic baseline: %w", err)
	}

	if !previousUplink.Valid {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO client_metrics
			 (client_id, xray_uplink_bytes, xray_downlink_bytes,
			  cycle_uplink_bytes, cycle_downlink_bytes, cycle_started_at, updated_at)
			 VALUES (?, ?, ?, 0, 0, ?, ?)`,
			report.ClientID, report.UplinkBytes, report.DownlinkBytes, now.Unix(), now.Unix(),
		)
		if err != nil {
			return fmt.Errorf("create client traffic baseline: %w", err)
		}
		return nil
	}

	deltaUplink := counterDelta(previousUplink.Int64, report.UplinkBytes)
	deltaDownlink := counterDelta(previousDownlink.Int64, report.DownlinkBytes)
	if cycleUplink.Int64 > math.MaxInt64-deltaUplink || cycleDownlink.Int64 > math.MaxInt64-deltaDownlink {
		return ErrInvalidClientTraffic
	}
	newCycleUplink := cycleUplink.Int64 + deltaUplink
	newCycleDownlink := cycleDownlink.Int64 + deltaDownlink
	if newCycleUplink > math.MaxInt64-newCycleDownlink {
		return ErrInvalidClientTraffic
	}
	if deltaUplink > 0 || deltaDownlink > 0 {
		lastActivityAt = sql.NullInt64{Int64: now.Unix(), Valid: true}
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE client_metrics
		 SET xray_uplink_bytes = ?, xray_downlink_bytes = ?,
		     cycle_uplink_bytes = ?, cycle_downlink_bytes = ?,
		     last_activity_at = ?, updated_at = ?
		 WHERE client_id = ?`,
		report.UplinkBytes, report.DownlinkBytes, newCycleUplink, newCycleDownlink,
		nullableUnix(lastActivityAt), now.Unix(), report.ClientID,
	)
	if err != nil {
		return fmt.Errorf("update client traffic: %w", err)
	}
	return nil
}

func counterDelta(previous, current int64) int64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func (s *Service) getClientMetrics(ctx context.Context, clientID int64) (*ClientMetrics, error) {
	var uplink, downlink, cycleUplink, cycleDownlink, cycleStartedAt, updatedAt int64
	var lastActivityAt sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT xray_uplink_bytes, xray_downlink_bytes, cycle_uplink_bytes,
		 cycle_downlink_bytes, cycle_started_at, last_activity_at, updated_at
		 FROM client_metrics WHERE client_id = ?`, clientID,
	).Scan(&uplink, &downlink, &cycleUplink, &cycleDownlink, &cycleStartedAt, &lastActivityAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read client metrics: %w", err)
	}
	return clientMetricsFromDatabase(
		uplink, downlink, cycleUplink, cycleDownlink, cycleStartedAt, lastActivityAt, updatedAt,
	), nil
}

func clientMetricsFromDatabase(
	xrayUplink, xrayDownlink, cycleUplink, cycleDownlink, cycleStartedAt int64,
	lastActivityAt sql.NullInt64,
	updatedAt int64,
) *ClientMetrics {
	metrics := &ClientMetrics{
		XrayUplinkBytes: xrayUplink, XrayDownlinkBytes: xrayDownlink,
		CycleUplinkBytes: cycleUplink, CycleDownlinkBytes: cycleDownlink,
		CycleStartedAt: time.Unix(cycleStartedAt, 0).UTC(), UpdatedAt: time.Unix(updatedAt, 0).UTC(),
	}
	if lastActivityAt.Valid {
		value := time.Unix(lastActivityAt.Int64, 0).UTC()
		metrics.LastActivityAt = &value
	}
	return metrics
}

func nullableUnix(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func clientStatsIdentifier(clientID int64) string {
	return clientStatsIdentifierPrefix + strconv.FormatInt(clientID, 10)
}
