package agentcontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Service) SetAgentOnline(ctx context.Context, serverID int64) error {
	return s.setStatus(ctx, serverID, statusOnline)
}

func (s *Service) SetAgentConnected(ctx context.Context, agentID, serverID int64) error {
	return s.SetAgentConnectedVersion(ctx, agentID, serverID, "")
}

func (s *Service) SetAgentConnectedVersion(ctx context.Context, agentID, serverID int64, agentVersion string) error {
	agentVersion = strings.TrimSpace(agentVersion)
	if utf8.RuneCountInString(agentVersion) > 64 {
		return ErrInvalidAgentVersion
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Agent connection update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`UPDATE agents SET last_seen_at = ?,
		 version = CASE WHEN ? = '' THEN version ELSE ? END,
		 upgrade_target_version = CASE WHEN ? != '' AND upgrade_target_version = ? THEN '' ELSE upgrade_target_version END,
		 upgrade_status = CASE WHEN ? != '' AND upgrade_target_version = ? THEN '' ELSE upgrade_status END,
		 upgrade_error = CASE WHEN ? != '' AND upgrade_target_version = ? THEN '' ELSE upgrade_error END,
		 updated_at = ? WHERE id = ? AND server_id = ?`,
		now, agentVersion, agentVersion,
		agentVersion, agentVersion, agentVersion, agentVersion, agentVersion, agentVersion,
		now, agentID, serverID,
	)
	if err != nil {
		return fmt.Errorf("update connected Agent: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read connected Agent count: %w", err)
	}
	if count != 1 {
		return ErrInvalidAgentToken
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ? WHERE id = ? AND archived_at IS NULL`,
		statusOnline, now, serverID,
	)
	if err != nil {
		return fmt.Errorf("update connected server: %w", err)
	}
	count, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read connected server count: %w", err)
	}
	if count != 1 {
		return ErrArchived
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Agent connection update: %w", err)
	}
	return nil
}

func (s *Service) TouchAgent(ctx context.Context, agentID, serverID int64) error {
	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := s.db.ExecContext(ctx,
		`UPDATE agents SET last_seen_at = ?, updated_at = ? WHERE id = ? AND server_id = ?`,
		now, now, agentID, serverID,
	)
	if err != nil {
		return fmt.Errorf("update Agent last seen: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated Agent count: %w", err)
	}
	if count != 1 {
		return ErrInvalidAgentToken
	}
	return nil
}

func (s *Service) SetAgentOffline(ctx context.Context, serverID int64) error {
	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL AND status = ?`,
		statusOffline, now, serverID, statusOnline,
	)
	if err != nil {
		return fmt.Errorf("set server offline: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read offline server count: %w", err)
	}
	if count == 1 {
		return nil
	}
	var existingID int64
	err = s.db.QueryRowContext(ctx,
		`SELECT id FROM servers WHERE id = ?`, serverID,
	).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrServerNotFound
	}
	if err != nil {
		return fmt.Errorf("read disconnected server state: %w", err)
	}
	return nil
}

func (s *Service) ResetOnline(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ?
		 WHERE status = ? AND archived_at IS NULL`,
		statusOffline, s.now().UTC().Truncate(time.Second).Unix(), statusOnline,
	)
	if err != nil {
		return fmt.Errorf("reset online servers: %w", err)
	}
	return nil
}

func (s *Service) setStatus(ctx context.Context, serverID int64, status string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET status = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		status, s.now().UTC().Truncate(time.Second).Unix(), serverID,
	)
	if err != nil {
		return fmt.Errorf("update server status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated server count: %w", err)
	}
	if count != 1 {
		var archivedAt sql.NullInt64
		err := s.db.QueryRowContext(ctx,
			`SELECT archived_at FROM servers WHERE id = ?`, serverID,
		).Scan(&archivedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrServerNotFound
		}
		if err != nil {
			return fmt.Errorf("read server lifecycle state: %w", err)
		}
		if archivedAt.Valid {
			return ErrArchived
		}
		return ErrServerNotFound
	}
	return nil
}
