package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Entry struct {
	ID            int64
	CreatedAt     time.Time
	ActorUserID   *int64
	ActorUsername string
	Action        string
	ResourceType  string
	ResourceID    *int64
	Summary       string
	RequestID     string
}

type Filter struct {
	Action string
	User   string
	Page   int
	Size   int
}

func Record(ctx context.Context, db *sql.DB, entry Entry) error {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO audit_logs
		(created_at, actor_user_id, actor_username, action, resource_type, resource_id, summary, request_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, entry.CreatedAt.UTC().Unix(), nullableID(entry.ActorUserID),
		entry.ActorUsername, entry.Action, entry.ResourceType, nullableID(entry.ResourceID), entry.Summary, entry.RequestID); err != nil {
		return fmt.Errorf("record audit log: %w", err)
	}
	return nil
}

func List(ctx context.Context, db *sql.DB, filter Filter) ([]Entry, int, error) {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.Size <= 0 || filter.Size > 100 {
		filter.Size = 50
	}
	filter.Action = strings.TrimSpace(filter.Action)
	filter.User = strings.TrimSpace(filter.User)
	where := ` WHERE (? = '' OR action = ?) AND (? = '' OR actor_username = ?)`
	args := []any{filter.Action, filter.Action, filter.User, filter.User}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}
	args = append(args, filter.Size, (filter.Page-1)*filter.Size)
	rows, err := db.QueryContext(ctx, `SELECT id, created_at, actor_user_id, actor_username,
		action, resource_type, resource_id, summary, request_id FROM audit_logs`+where+`
		ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()
	values := make([]Entry, 0, filter.Size)
	for rows.Next() {
		var value Entry
		var createdAt int64
		var actorID, resourceID sql.NullInt64
		if err := rows.Scan(&value.ID, &createdAt, &actorID, &value.ActorUsername,
			&value.Action, &value.ResourceType, &resourceID, &value.Summary, &value.RequestID); err != nil {
			return nil, 0, fmt.Errorf("scan audit log: %w", err)
		}
		value.CreatedAt = time.Unix(createdAt, 0).UTC()
		if actorID.Valid {
			id := actorID.Int64
			value.ActorUserID = &id
		}
		if resourceID.Valid {
			id := resourceID.Int64
			value.ResourceID = &id
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate audit logs: %w", err)
	}
	return values, total, nil
}

func nullableID(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
