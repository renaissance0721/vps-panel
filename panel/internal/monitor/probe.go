package monitor

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

func (s *Service) Desired(ctx context.Context, serverID int64, capabilities map[string]bool) ([]ProbeTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.name, t.type, t.target, t.port, t.interval_seconds
		FROM monitor_probe_tasks t JOIN servers s ON s.id = ?
		WHERE t.enabled = 1 AND s.archived_at IS NULL AND s.decommission_status = ''
		AND (t.default_on = 1 OR EXISTS (SELECT 1 FROM monitor_probe_servers a WHERE a.task_id = t.id AND a.server_id = s.id)) ORDER BY t.id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []ProbeTask{}
	for rows.Next() {
		var v ProbeTask
		if err := rows.Scan(&v.ID, &v.Name, &v.Type, &v.Target, &v.Port, &v.IntervalSeconds); err != nil {
			return nil, err
		}
		if Supports(capabilities, v.Type) {
			values = append(values, v)
		}
	}
	if len(values) > MaxProbeTasks {
		return nil, ErrTaskLimit
	}
	return values, rows.Err()
}

// Ingest uses the authenticated connection's server ID, never an ID supplied in a result.
func (s *Service) Ingest(ctx context.Context, serverID int64, capabilities map[string]bool, result ProbeResult) error {
	switch result.Outcome {
	case "success":
		if result.LatencyMS == nil || math.IsNaN(*result.LatencyMS) || math.IsInf(*result.LatencyMS, 0) || *result.LatencyMS < 0 {
			return ErrInvalidResult
		}
	case "timeout", "dns_error", "connect_error", "permission_error", "cancelled":
		result.LatencyMS = nil
	default:
		return ErrInvalidResult
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind string
	err = tx.QueryRowContext(ctx, `SELECT t.type FROM monitor_probe_tasks t
		JOIN servers s ON s.id = ?
		WHERE t.id = ? AND t.enabled = 1 AND s.archived_at IS NULL AND s.decommission_status = ''
		AND (t.default_on = 1 OR EXISTS (SELECT 1 FROM monitor_probe_servers a WHERE a.task_id = t.id AND a.server_id = s.id))`, serverID, result.TaskID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidResult
	}
	if err != nil {
		return err
	}
	if !Supports(capabilities, kind) {
		return ErrInvalidResult
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_probe_records (server_id, task_id, ts, outcome, latency_ms) VALUES (?, ?, ?, ?, ?)`, serverID, result.TaskID, s.now().UnixMilli(), result.Outcome, result.LatencyMS); err != nil {
		return err
	}
	return tx.Commit()
}
