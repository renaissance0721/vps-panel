package operation

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	StatusPending    = "pending"
	StatusSent       = "sent"
	StatusApplied    = "applied"
	StatusFailed     = "failed"
	StatusSuperseded = "superseded"
)

type Operation struct {
	ID                  int64
	ServerID            int64
	ResourceType        string
	ResourceID          int64
	Action              string
	DesiredStateVersion int64
	Status              string
	Error               string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	AppliedAt           *time.Time
}

func RecordTx(ctx context.Context, tx *sql.Tx, serverID int64, resourceType string, resourceID int64,
	action string, desiredStateVersion int64, now time.Time,
) error {
	now = now.UTC().Truncate(time.Second)
	if _, err := tx.ExecContext(ctx, `UPDATE configuration_operations
		SET status = ?, error = '', updated_at = ?
		WHERE server_id = ? AND desired_state_version < ? AND status IN (?, ?)`,
		StatusSuperseded, now.Unix(), serverID, desiredStateVersion, StatusPending, StatusSent); err != nil {
		return fmt.Errorf("supersede configuration operations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO configuration_operations
		(server_id, resource_type, resource_id, action, desired_state_version, status, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, '', ?, ?)`,
		serverID, resourceType, resourceID, action, desiredStateVersion, StatusPending, now.Unix(), now.Unix()); err != nil {
		return fmt.Errorf("record configuration operation: %w", err)
	}
	return nil
}

func MarkSentTx(ctx context.Context, tx *sql.Tx, serverID, version int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE configuration_operations
		SET status = ?, updated_at = ?
		WHERE server_id = ? AND desired_state_version <= ? AND status = ?`,
		StatusSent, now.UTC().Truncate(time.Second).Unix(), serverID, version, StatusPending); err != nil {
		return fmt.Errorf("mark configuration operations sent: %w", err)
	}
	return nil
}

func RecordReceiptTx(ctx context.Context, tx *sql.Tx, serverID, version int64, status, message string, now time.Time) error {
	now = now.UTC().Truncate(time.Second)
	if status == "success" {
		if _, err := tx.ExecContext(ctx, `UPDATE configuration_operations
			SET status = ?, error = '', updated_at = ?, applied_at = ?
			WHERE server_id = ? AND desired_state_version <= ? AND status IN (?, ?, ?)`,
			StatusApplied, now.Unix(), now.Unix(), serverID, version, StatusPending, StatusSent, StatusFailed); err != nil {
			return fmt.Errorf("apply configuration operation receipt: %w", err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE configuration_operations
		SET status = ?, error = ?, updated_at = ?, applied_at = NULL
		WHERE server_id = ? AND desired_state_version = ? AND status IN (?, ?)`,
		StatusFailed, message, now.Unix(), serverID, version, StatusPending, StatusSent); err != nil {
		return fmt.Errorf("fail configuration operation receipt: %w", err)
	}
	return nil
}

func Counts(ctx context.Context, db *sql.DB) (pending, failed int, err error) {
	err = db.QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER (WHERE status IN ('pending', 'sent')),
		COUNT(*) FILTER (WHERE status = 'failed')
		FROM configuration_operations`).Scan(&pending, &failed)
	if err != nil {
		return 0, 0, fmt.Errorf("count configuration operations: %w", err)
	}
	return pending, failed, nil
}

func CountsForUser(ctx context.Context, db *sql.DB, userID int64) (pending, failed int, err error) {
	err = db.QueryRowContext(ctx, `SELECT
		COUNT(*) FILTER (WHERE operations.status IN ('pending', 'sent')),
		COUNT(*) FILTER (WHERE operations.status = 'failed')
		FROM configuration_operations AS operations
		JOIN servers ON servers.id = operations.server_id
		WHERE servers.archived_at IS NULL
		  AND (servers.visibility = 'public' OR EXISTS (
			SELECT 1 FROM server_access
			WHERE server_access.server_id = servers.id AND server_access.user_id = ?
		  ))`, userID).Scan(&pending, &failed)
	if err != nil {
		return 0, 0, fmt.Errorf("count visible configuration operations: %w", err)
	}
	return pending, failed, nil
}

func List(ctx context.Context, db *sql.DB, serverID int64, limit int) ([]Operation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT id, server_id, resource_type, resource_id, action,
		desired_state_version, status, error, created_at, updated_at, applied_at
		FROM configuration_operations
		WHERE (? = 0 OR server_id = ?)
		ORDER BY created_at DESC, id DESC LIMIT ?`, serverID, serverID, limit)
	if err != nil {
		return nil, fmt.Errorf("list configuration operations: %w", err)
	}
	defer rows.Close()
	values := make([]Operation, 0)
	for rows.Next() {
		var value Operation
		var createdAt, updatedAt int64
		var appliedAt sql.NullInt64
		if err := rows.Scan(&value.ID, &value.ServerID, &value.ResourceType, &value.ResourceID,
			&value.Action, &value.DesiredStateVersion, &value.Status, &value.Error,
			&createdAt, &updatedAt, &appliedAt); err != nil {
			return nil, fmt.Errorf("scan configuration operation: %w", err)
		}
		value.CreatedAt = time.Unix(createdAt, 0).UTC()
		value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if appliedAt.Valid {
			applied := time.Unix(appliedAt.Int64, 0).UTC()
			value.AppliedAt = &applied
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate configuration operations: %w", err)
	}
	return values, nil
}
