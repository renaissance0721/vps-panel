package monitor

import (
	"context"
	"database/sql"
	"fmt"
)

func ensureAssignmentServer(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, serverID int64) error {
	var exists bool
	if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM servers
		WHERE id = ? AND archived_at IS NULL AND decommission_status = '')`, serverID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: 服务器不存在、已归档或正在退役", ErrInvalid)
	}
	return nil
}

func (s *Service) ListServerProbeAssignments(ctx context.Context, serverID int64) ([]ServerProbeAssignmentTask, error) {
	if err := ensureAssignmentServer(ctx, s.db, serverID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.name, t.type, t.target, t.port, t.interval_seconds, t.enabled,
		EXISTS(SELECT 1 FROM monitor_probe_servers a WHERE a.task_id = t.id AND a.server_id = ?)
		FROM monitor_probe_tasks t ORDER BY t.id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []ServerProbeAssignmentTask{}
	for rows.Next() {
		var value ServerProbeAssignmentTask
		if err := rows.Scan(&value.ID, &value.Name, &value.Type, &value.Target, &value.Port, &value.IntervalSeconds, &value.Enabled, &value.Assigned); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// SetServerProbeAssignments changes only this server's assignments. Global task
// switches, future-server defaults, capabilities and history remain independent.
func (s *Service) SetServerProbeAssignments(ctx context.Context, serverID int64, taskIDs []int64) error {
	if len(taskIDs) > MaxProbeTasks {
		return ErrTaskLimit
	}
	seen := make(map[int64]bool, len(taskIDs))
	for _, id := range taskIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("%w: 探测任务 ID 必须为正整数且不能重复", ErrInvalid)
		}
		seen[id] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureAssignmentServer(ctx, tx, serverID); err != nil {
		return err
	}
	for _, id := range taskIDs {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM monitor_probe_tasks WHERE id = ?)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: 探测任务不存在，请刷新后重试", ErrInvalid)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_probe_servers WHERE server_id = ?`, serverID); err != nil {
		return err
	}
	for _, id := range taskIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_probe_servers (task_id, server_id) VALUES (?, ?)`, id, serverID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
