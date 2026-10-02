package monitor

import (
	"context"
	"time"
)

func (s *Service) History(ctx context.Context, serverID int64, hours int, capabilities map[string]bool) (History, error) {
	if hours != 1 && hours != 6 && hours != 24 {
		return History{}, ErrInvalid
	}
	now := s.now().UTC()
	response := History{Tasks: []ProbeSummary{}, Samples: []ProbeRecord{}, From: now.Add(-time.Duration(hours) * time.Hour), To: now}
	tasks, err := s.Desired(ctx, serverID, capabilities)
	if err != nil {
		return History{}, err
	}
	for _, task := range tasks {
		summary := ProbeSummary{ProbeTask: task}
		rows, err := s.db.QueryContext(ctx, `SELECT ts, outcome, latency_ms FROM monitor_probe_records
			WHERE server_id = ? AND task_id = ? AND ts >= ? AND ts <= ? ORDER BY ts, rowid`, serverID, task.ID, now.Add(-24*time.Hour).UnixMilli(), now.UnixMilli())
		if err != nil {
			return History{}, err
		}
		var total, failed int
		for rows.Next() {
			var ts int64
			sample := ProbeRecord{TaskID: task.ID}
			if err := rows.Scan(&ts, &sample.Outcome, &sample.LatencyMS); err != nil {
				rows.Close()
				return History{}, err
			}
			sample.Timestamp = time.UnixMilli(ts).UTC()
			summary.LatestOutcome, summary.LatestLatencyMS = sample.Outcome, sample.LatencyMS
			// ICMP loss measures sent Echoes only. Local permission/DNS failures and
			// cancellations are shown explicitly but are never counted as packet loss.
			if sample.Outcome == "success" || sample.Outcome == "timeout" || task.Type == "tcp" && (sample.Outcome == "dns_error" || sample.Outcome == "connect_error") {
				total++
				if sample.Outcome != "success" {
					failed++
				}
			}
			if !sample.Timestamp.Before(response.From) {
				response.Samples = append(response.Samples, sample)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return History{}, err
		}
		rows.Close()
		if total > 0 {
			rate := float64(failed) * 100 / float64(total)
			summary.FailureRate = &rate
		}
		response.Tasks = append(response.Tasks, summary)
	}
	return response, nil
}

func (s *Service) Cleanup(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM monitor_probe_records WHERE ts < ?`, s.now().Add(-7*24*time.Hour).UnixMilli())
	return err
}
