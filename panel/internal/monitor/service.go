package monitor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service { return &Service{db: db, now: time.Now} }

func (s *Service) List(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, type, target, port, interval_seconds, enabled, default_on, created_at, updated_at FROM monitor_probe_tasks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	values := []Task{}
	for rows.Next() {
		var v Task
		var created, updated int64
		if err := rows.Scan(&v.ID, &v.Name, &v.Type, &v.Target, &v.Port, &v.IntervalSeconds, &v.Enabled, &v.DefaultOn, &created, &updated); err != nil {
			rows.Close()
			return nil, err
		}
		v.CreatedAt, v.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
		v.ServerIDs = []int64{}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	assignments, err := s.db.QueryContext(ctx, `SELECT task_id, server_id FROM monitor_probe_servers ORDER BY server_id`)
	if err != nil {
		return nil, err
	}
	defer assignments.Close()
	indices := make(map[int64]int, len(values))
	for i := range values {
		indices[values[i].ID] = i
	}
	for assignments.Next() {
		var taskID, serverID int64
		if err := assignments.Scan(&taskID, &serverID); err != nil {
			return nil, err
		}
		if index, ok := indices[taskID]; ok {
			values[index].ServerIDs = append(values[index].ServerIDs, serverID)
		}
	}
	return values, assignments.Err()
}

// Save replaces a task and its assignments atomically. Capabilities must be explicitly
// reported by the current connection, or last reported by the Agent while offline.
func (s *Service) Save(ctx context.Context, id int64, value Task, capabilities map[int64]map[string]bool) (Task, error) {
	value.ID = id
	value.Name, value.Target = strings.TrimSpace(value.Name), strings.TrimSpace(value.Target)
	if err := ValidateTask(value.ProbeTask); err != nil {
		return Task{}, err
	}
	if value.DefaultOn {
		value.ServerIDs = nil // A rule, never a materialized copy of the current server list.
	}
	seen := map[int64]bool{}
	for _, serverID := range value.ServerIDs {
		if serverID <= 0 || seen[serverID] {
			return Task{}, fmt.Errorf("%w: invalid server assignment", ErrInvalid)
		}
		seen[serverID] = true
		if !Supports(capabilities[serverID], value.Type) {
			return Task{}, fmt.Errorf("%w: server %d", ErrUnsupported, serverID)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	now := s.now().UTC().Truncate(time.Second)
	value.CreatedAt, value.UpdatedAt = now, now
	if id != 0 {
		var created int64
		err := tx.QueryRowContext(ctx, `SELECT created_at FROM monitor_probe_tasks WHERE id = ?`, id).Scan(&created)
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		if err != nil {
			return Task{}, err
		}
		value.CreatedAt = time.Unix(created, 0).UTC()
	}
	for _, serverID := range value.ServerIDs {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM servers WHERE id = ? AND archived_at IS NULL AND decommission_status = '')`, serverID).Scan(&exists); err != nil {
			return Task{}, err
		}
		if !exists {
			return Task{}, fmt.Errorf("%w: server unavailable", ErrInvalid)
		}
	}
	if id == 0 {
		result, err := tx.ExecContext(ctx, `INSERT INTO monitor_probe_tasks (name, type, target, port, interval_seconds, enabled, default_on, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, value.Name, value.Type, value.Target, value.Port, value.IntervalSeconds, value.Enabled, value.DefaultOn, now.Unix(), now.Unix())
		if err != nil {
			return Task{}, err
		}
		value.ID, err = result.LastInsertId()
		if err != nil {
			return Task{}, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE monitor_probe_tasks SET name = ?, type = ?, target = ?, port = ?, interval_seconds = ?, enabled = ?, default_on = ?, updated_at = ? WHERE id = ?`, value.Name, value.Type, value.Target, value.Port, value.IntervalSeconds, value.Enabled, value.DefaultOn, now.Unix(), id); err != nil {
			return Task{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM monitor_probe_servers WHERE task_id = ?`, id); err != nil {
			return Task{}, err
		}
	}
	if value.ServerIDs == nil {
		value.ServerIDs = []int64{}
	}
	for _, serverID := range value.ServerIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_probe_servers (task_id, server_id) VALUES (?, ?)`, value.ID, serverID); err != nil {
			return Task{}, err
		}
	}
	// Reserve capacity for defaults even before servers connect or gain capabilities.
	// Count configured tasks (including disabled ones), as manual assignments do.
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM monitor_probe_tasks WHERE default_on = 1) +
		COALESCE((SELECT MAX(n) FROM (
			SELECT COUNT(*) AS n FROM monitor_probe_servers a
			JOIN monitor_probe_tasks t ON t.id = a.task_id
			JOIN servers s ON s.id = a.server_id
			WHERE t.default_on = 0 AND s.archived_at IS NULL AND s.decommission_status = ''
			GROUP BY a.server_id)), 0)`).Scan(&count); err != nil {
		return Task{}, err
	}
	if count > MaxProbeTasks {
		return Task{}, ErrTaskLimit
	}
	return value, tx.Commit()
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM monitor_probe_tasks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
