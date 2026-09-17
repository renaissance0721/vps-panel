package server

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Access struct {
	Visibility string
	UserIDs    []int64
}

func (s *Service) CanAccess(ctx context.Context, userID, serverID int64) (bool, error) {
	return canAccessServer(ctx, s.db, userID, serverID)
}

func (s *Service) UpdateAccess(
	ctx context.Context,
	serverID, currentUserID int64,
	visibility string,
	userIDs []int64,
) (Access, error) {
	visibility, err := normalizeVisibility(visibility)
	if err != nil {
		return Access{}, err
	}
	if visibility == VisibilityPrivate {
		userIDs, err = normalizeAccessUserIDs(userIDs, currentUserID)
		if err != nil {
			return Access{}, err
		}
	} else {
		userIDs = []int64{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Access{}, fmt.Errorf("begin server access update: %w", err)
	}
	defer tx.Rollback()
	allowed, err := canAccessServer(ctx, tx, currentUserID, serverID)
	if err != nil {
		return Access{}, err
	}
	if !allowed {
		return Access{}, ErrNotFound
	}
	if visibility == VisibilityPrivate {
		if err := validateAccessUsers(ctx, tx, userIDs); err != nil {
			return Access{}, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE servers SET visibility = ?, updated_at = ? WHERE id = ?`,
		visibility, s.now().UTC().Truncate(time.Second).Unix(), serverID,
	); err != nil {
		return Access{}, fmt.Errorf("update server visibility: %w", err)
	}
	if err := replaceServerAccess(ctx, tx, serverID, userIDs); err != nil {
		return Access{}, err
	}
	if err := tx.Commit(); err != nil {
		return Access{}, fmt.Errorf("commit server access update: %w", err)
	}
	return Access{Visibility: visibility, UserIDs: userIDs}, nil
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func canAccessServer(ctx context.Context, query rowQuerier, userID, serverID int64) (bool, error) {
	if userID <= 0 || serverID <= 0 {
		return false, nil
	}
	var allowed bool
	err := query.QueryRowContext(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM servers
			WHERE id = ? AND (
				visibility = 'public' OR EXISTS (
					SELECT 1 FROM server_access
					WHERE server_access.server_id = servers.id AND server_access.user_id = ?
				)
			)
		)`,
		serverID, userID,
	).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check server access: %w", err)
	}
	return allowed, nil
}

func normalizeVisibility(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return VisibilityPublic, nil
	}
	if value != VisibilityPublic && value != VisibilityPrivate {
		return "", ErrInvalidVisibility
	}
	return value, nil
}

func normalizeAccessUserIDs(values []int64, currentUserID int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(values)+1)
	for _, userID := range values {
		if userID <= 0 {
			return nil, ErrInvalidServerAccess
		}
		seen[userID] = struct{}{}
	}
	if currentUserID > 0 {
		seen[currentUserID] = struct{}{}
	}
	if len(seen) == 0 {
		return nil, ErrInvalidServerAccess
	}
	result := make([]int64, 0, len(seen))
	for userID := range seen {
		result = append(result, userID)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func validateAccessUsers(ctx context.Context, query rowQuerier, userIDs []int64) error {
	for _, userID := range userIDs {
		var exists bool
		if err := query.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, userID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("validate server access user: %w", err)
		}
		if !exists {
			return ErrInvalidServerAccess
		}
	}
	return nil
}

func replaceServerAccess(ctx context.Context, tx *sql.Tx, serverID int64, userIDs []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM server_access WHERE server_id = ?`, serverID); err != nil {
		return fmt.Errorf("clear server access: %w", err)
	}
	for _, userID := range userIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO server_access (server_id, user_id) VALUES (?, ?)`, serverID, userID,
		); err != nil {
			return fmt.Errorf("create server access: %w", err)
		}
	}
	return nil
}
