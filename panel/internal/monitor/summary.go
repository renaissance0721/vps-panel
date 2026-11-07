package monitor

import (
	"context"
	"database/sql"
	"time"
)

// HistorySummary aggregates in SQLite so callers do not load raw samples into memory.
func (s *Service) HistorySummary(ctx context.Context, serverID int64, hours int, capabilities map[string]bool) (map[string]any, error) {
	if hours != 1 && hours != 6 && hours != 24 {
		return nil, ErrInvalid
	}
	now := s.now().UTC()
	from := now.Add(-time.Duration(hours) * time.Hour)
	tasks, err := s.Desired(ctx, serverID, capabilities)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(tasks))
	for _, task := range tasks {
		var count, eligible, failed int64
		var average sql.NullFloat64
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), AVG(CASE WHEN outcome='success' THEN latency_ms END),
   COALESCE(SUM(CASE WHEN outcome IN ('success','timeout') OR (?='tcp' AND outcome IN ('dns_error','connect_error')) THEN 1 ELSE 0 END),0),
   COALESCE(SUM(CASE WHEN outcome='timeout' OR (?='tcp' AND outcome IN ('dns_error','connect_error')) THEN 1 ELSE 0 END),0)
   FROM monitor_probe_records WHERE server_id=? AND task_id=? AND ts>=? AND ts<=?`, task.Type, task.Type, serverID, task.ID, from.UnixMilli(), now.UnixMilli()).Scan(&count, &average, &eligible, &failed)
		if err != nil {
			return nil, err
		}
		var avg, rate, latest any
		if average.Valid {
			avg = average.Float64
		}
		if eligible > 0 {
			rate = float64(failed) * 100 / float64(eligible)
		}
		var latency sql.NullFloat64
		var outcome string
		var timestamp int64
		err = s.db.QueryRowContext(ctx, `SELECT ts,outcome,latency_ms FROM monitor_probe_records WHERE server_id=? AND task_id=? AND ts>=? AND ts<=? ORDER BY ts DESC,rowid DESC LIMIT 1`, serverID, task.ID, from.UnixMilli(), now.UnixMilli()).Scan(&timestamp, &outcome, &latency)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil {
			var value any
			if latency.Valid {
				value = latency.Float64
			}
			latest = map[string]any{"timestamp": time.UnixMilli(timestamp).UTC(), "outcome": outcome, "latency_ms": value}
		}
		out = append(out, map[string]any{"id": task.ID, "name": task.Name, "type": task.Type, "target": task.Target, "port": task.Port, "sample_count": count, "average_latency_ms": avg, "failure_rate": rate, "latest": latest})
	}
	return map[string]any{"from": from, "to": now, "range_hours": hours, "tasks": out}, nil
}
