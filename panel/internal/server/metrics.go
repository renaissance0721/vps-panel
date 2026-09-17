package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

type Metrics struct {
	CPUPercent             float64
	MemoryUsedBytes        int64
	MemoryTotalBytes       int64
	DiskUsedBytes          int64
	DiskTotalBytes         int64
	UptimeSeconds          int64
	NICRXBytes             int64
	NICTXBytes             int64
	CycleRXBytes           int64
	CycleTXBytes           int64
	TrafficAdjustmentBytes int64
	CycleStartedAt         *time.Time
	UpdatedAt              time.Time
}

type MetricsReport struct {
	CPUPercent       float64
	MemoryUsedBytes  int64
	MemoryTotalBytes int64
	DiskUsedBytes    int64
	DiskTotalBytes   int64
	UptimeSeconds    int64
	HasNetworkUsage  bool
	NICRXBytes       int64
	NICTXBytes       int64
}

func (s *Service) ReportMetrics(ctx context.Context, agentID, serverID int64, report MetricsReport) error {
	if math.IsNaN(report.CPUPercent) || math.IsInf(report.CPUPercent, 0) ||
		report.CPUPercent < 0 || report.CPUPercent > 100 ||
		report.MemoryUsedBytes < 0 || report.MemoryTotalBytes < 0 ||
		report.MemoryUsedBytes > report.MemoryTotalBytes ||
		report.DiskUsedBytes < 0 || report.DiskTotalBytes < 0 ||
		report.DiskUsedBytes > report.DiskTotalBytes ||
		report.UptimeSeconds < 0 || report.NICRXBytes < 0 || report.NICTXBytes < 0 {
		return ErrInvalidMetrics
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metrics report: %w", err)
	}
	defer tx.Rollback()

	var currentAgentID int64
	var resetDay int
	var resetTime string
	err = tx.QueryRowContext(ctx,
		`SELECT agents.id, servers.traffic_reset_day, servers.traffic_reset_time
		 FROM agents JOIN servers ON servers.id = agents.server_id
		 WHERE agents.id = ? AND agents.server_id = ?`, agentID, serverID,
	).Scan(&currentAgentID, &resetDay, &resetTime)
	if errors.Is(err, sql.ErrNoRows) {
		return agentcontrol.ErrInvalidAgentToken
	}
	if err != nil {
		return fmt.Errorf("read reporting Agent for metrics: %w", err)
	}
	nowTime := s.now().UTC().Truncate(time.Second)
	var nicRX, nicTX, cycleRX, cycleTX, trafficAdjustment int64
	var storedCycleStart sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT nic_rx_bytes, nic_tx_bytes, cycle_rx_bytes, cycle_tx_bytes,
		 traffic_adjustment_bytes, cycle_started_at
		 FROM server_metrics WHERE server_id = ?`, serverID,
	).Scan(&nicRX, &nicTX, &cycleRX, &cycleTX, &trafficAdjustment, &storedCycleStart)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("read traffic baseline: %w", err)
	}
	var cycleStartValue any
	if storedCycleStart.Valid {
		cycleStartValue = storedCycleStart.Int64
	}
	if report.HasNetworkUsage {
		cycleStart, err := trafficCycleStart(nowTime, resetDay, resetTime)
		if err != nil {
			return fmt.Errorf("calculate traffic cycle: %w", err)
		}
		switch {
		case !storedCycleStart.Valid:
			cycleRX = 0
			cycleTX = 0
		case time.Unix(storedCycleStart.Int64, 0).UTC().Before(cycleStart):
			cycleRX = 0
			cycleTX = 0
			trafficAdjustment = 0
		default:
			deltaRX := trafficDelta(report.NICRXBytes, nicRX)
			deltaTX := trafficDelta(report.NICTXBytes, nicTX)
			if cycleRX > math.MaxInt64-deltaRX || cycleTX > math.MaxInt64-deltaTX {
				return ErrInvalidMetrics
			}
			cycleRX += deltaRX
			cycleTX += deltaTX
			cycleStart = time.Unix(storedCycleStart.Int64, 0).UTC()
		}
		nicRX = report.NICRXBytes
		nicTX = report.NICTXBytes
		cycleStartValue = cycleStart.Unix()
	}

	now := nowTime.Unix()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO server_metrics
		 (server_id, cpu_percent, memory_used_bytes, memory_total_bytes,
		  disk_used_bytes, disk_total_bytes, uptime_seconds, nic_rx_bytes, nic_tx_bytes,
		  cycle_rx_bytes, cycle_tx_bytes, traffic_adjustment_bytes, cycle_started_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(server_id) DO UPDATE SET
		 cpu_percent = excluded.cpu_percent,
		 memory_used_bytes = excluded.memory_used_bytes,
		 memory_total_bytes = excluded.memory_total_bytes,
		 disk_used_bytes = excluded.disk_used_bytes,
		 disk_total_bytes = excluded.disk_total_bytes,
		 uptime_seconds = excluded.uptime_seconds,
		 nic_rx_bytes = excluded.nic_rx_bytes,
		 nic_tx_bytes = excluded.nic_tx_bytes,
		 cycle_rx_bytes = excluded.cycle_rx_bytes,
		 cycle_tx_bytes = excluded.cycle_tx_bytes,
		 traffic_adjustment_bytes = excluded.traffic_adjustment_bytes,
		 cycle_started_at = excluded.cycle_started_at,
		 updated_at = excluded.updated_at`,
		serverID, report.CPUPercent, report.MemoryUsedBytes, report.MemoryTotalBytes,
		report.DiskUsedBytes, report.DiskTotalBytes, report.UptimeSeconds,
		nicRX, nicTX, cycleRX, cycleTX, trafficAdjustment, cycleStartValue, now,
	); err != nil {
		return fmt.Errorf("save server metrics: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit metrics report: %w", err)
	}
	return nil
}
