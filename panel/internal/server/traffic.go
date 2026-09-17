package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

func (server Server) TrafficUsedBytes() int64 {
	measured := server.MeasuredTrafficUsedBytes()
	if server.Metrics == nil {
		return measured
	}
	adjustment := server.Metrics.TrafficAdjustmentBytes
	if adjustment > 0 && measured > math.MaxInt64-adjustment {
		return math.MaxInt64
	}
	if adjustment < 0 && adjustment < -measured {
		return 0
	}
	return measured + adjustment
}

func (server Server) MeasuredTrafficUsedBytes() int64 {
	if server.Metrics == nil {
		return 0
	}
	return measuredTrafficUsedBytes(
		server.TrafficCountMode, server.Metrics.CycleRXBytes, server.Metrics.CycleTXBytes,
	)
}

func measuredTrafficUsedBytes(countMode string, cycleRXBytes, cycleTXBytes int64) int64 {
	cycleTX := max(cycleTXBytes, 0)
	if countMode != TrafficBidirectional {
		return cycleTX
	}
	cycleRX := max(cycleRXBytes, 0)
	if cycleRX > math.MaxInt64-cycleTX {
		return math.MaxInt64
	}
	return cycleRX + cycleTX
}

type TrafficConfig struct {
	MonthlyLimitBytes *int64
	CountMode         string
	ResetDay          int
	ResetTime         string
}

func (s *Service) UpdateTrafficConfig(ctx context.Context, id int64, config TrafficConfig) (Server, error) {
	if config.MonthlyLimitBytes != nil && *config.MonthlyLimitBytes < 0 ||
		(config.CountMode != TrafficSingle && config.CountMode != TrafficBidirectional) ||
		config.ResetDay < 1 || config.ResetDay > 31 ||
		!validTrafficResetTime(config.ResetTime) {
		return Server{}, ErrInvalidTrafficConfig
	}

	var monthlyLimit any
	if config.MonthlyLimitBytes != nil && *config.MonthlyLimitBytes > 0 {
		monthlyLimit = *config.MonthlyLimitBytes
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers
		 SET monthly_traffic_limit_bytes = ?, traffic_count_mode = ?,
		     traffic_reset_day = ?, traffic_reset_time = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		monthlyLimit, config.CountMode, config.ResetDay, config.ResetTime,
		s.now().UTC().Truncate(time.Second).Unix(), id,
	)
	if err != nil {
		return Server{}, fmt.Errorf("update server traffic configuration: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, fmt.Errorf("read updated server traffic configuration count: %w", err)
	}
	if count != 1 {
		return Server{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Service) UpdateTrafficAdjustment(ctx context.Context, id, targetUsedBytes int64) (Server, error) {
	if targetUsedBytes < 0 {
		return Server{}, ErrInvalidTrafficTarget
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, fmt.Errorf("begin traffic adjustment: %w", err)
	}
	defer tx.Rollback()

	var countMode string
	var cycleRX, cycleTX sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT servers.traffic_count_mode, metrics.cycle_rx_bytes, metrics.cycle_tx_bytes
		 FROM servers
		 LEFT JOIN server_metrics AS metrics ON metrics.server_id = servers.id
		 WHERE servers.id = ? AND servers.archived_at IS NULL`, id,
	).Scan(&countMode, &cycleRX, &cycleTX)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	if err != nil {
		return Server{}, fmt.Errorf("read server traffic usage: %w", err)
	}
	measuredUsedBytes := measuredTrafficUsedBytes(countMode, cycleRX.Int64, cycleTX.Int64)
	adjustmentBytes := targetUsedBytes - measuredUsedBytes
	now := s.now().UTC().Truncate(time.Second).Unix()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO server_metrics
		 (server_id, cpu_percent, memory_used_bytes, memory_total_bytes,
		  disk_used_bytes, disk_total_bytes, uptime_seconds, traffic_adjustment_bytes, updated_at)
		 VALUES (?, 0, 0, 0, 0, 0, 0, ?, ?)
		 ON CONFLICT(server_id) DO UPDATE SET
		 traffic_adjustment_bytes = excluded.traffic_adjustment_bytes`,
		id, adjustmentBytes, now,
	); err != nil {
		return Server{}, fmt.Errorf("save traffic adjustment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Server{}, fmt.Errorf("commit traffic adjustment: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *Service) ClearTrafficAdjustment(ctx context.Context, id int64) (Server, error) {
	var existingID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM servers WHERE id = ? AND archived_at IS NULL`, id,
	).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	if err != nil {
		return Server{}, fmt.Errorf("read server for traffic adjustment clear: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE server_metrics SET traffic_adjustment_bytes = 0 WHERE server_id = ?`, id,
	); err != nil {
		return Server{}, fmt.Errorf("clear traffic adjustment: %w", err)
	}
	return s.Get(ctx, id)
}

func trafficDelta(current, previous int64) int64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func trafficCycleStart(now time.Time, resetDay int, resetTime string) (time.Time, error) {
	parsedTime, err := time.Parse("15:04", resetTime)
	if err != nil || parsedTime.Format("15:04") != resetTime || resetDay < 1 || resetDay > 31 {
		return time.Time{}, ErrInvalidTrafficConfig
	}
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	localNow := now.In(location)
	boundary := monthlyTrafficBoundary(
		localNow.Year(), localNow.Month(), resetDay, parsedTime.Hour(), parsedTime.Minute(), location,
	)
	if localNow.Before(boundary) {
		previousMonth := time.Date(localNow.Year(), localNow.Month()-1, 1, 0, 0, 0, 0, location)
		boundary = monthlyTrafficBoundary(
			previousMonth.Year(), previousMonth.Month(), resetDay,
			parsedTime.Hour(), parsedTime.Minute(), location,
		)
	}
	return boundary.UTC(), nil
}

func monthlyTrafficBoundary(year int, month time.Month, resetDay, hour, minute int, location *time.Location) time.Time {
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, location).Day()
	if resetDay > lastDay {
		resetDay = lastDay
	}
	return time.Date(year, month, resetDay, hour, minute, 0, 0, location)
}

func validTrafficResetTime(value string) bool {
	parsed, err := time.Parse("15:04", value)
	return err == nil && parsed.Format("15:04") == value
}
