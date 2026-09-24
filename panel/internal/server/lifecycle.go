package server

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *Service) UpdateName(ctx context.Context, id int64, name string) (Server, error) {
	var err error
	name, err = normalizeServerName(name)
	if err != nil {
		return Server{}, err
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET name = ?, updated_at = ? WHERE id = ? AND archived_at IS NULL`,
		name, s.now().UTC().Truncate(time.Second).Unix(), id,
	)
	if err != nil {
		return Server{}, fmt.Errorf("update server name: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, fmt.Errorf("read updated server name count: %w", err)
	}
	if count != 1 {
		return Server{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Service) UpdateOutboundPreference(ctx context.Context, id int64, preference string) (Server, int64, error) {
	switch preference {
	case OutboundAuto, OutboundPreferIPv4, OutboundPreferIPv6:
	default:
		return Server{}, 0, ErrInvalidOutboundPreference
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, 0, fmt.Errorf("begin outbound preference update: %w", err)
	}
	defer tx.Rollback()
	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET outbound_preference = ?, desired_state_version = desired_state_version + 1, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`, preference, now, id,
	)
	if err != nil {
		return Server{}, 0, fmt.Errorf("update outbound preference: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, 0, fmt.Errorf("read updated outbound preference count: %w", err)
	}
	if count != 1 {
		return Server{}, 0, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agents SET config_sync_status = 'pending', config_sync_error = '', updated_at = ? WHERE server_id = ?`,
		now, id,
	); err != nil {
		return Server{}, 0, fmt.Errorf("mark Agent config pending: %w", err)
	}
	var version int64
	if err := tx.QueryRowContext(ctx,
		`SELECT desired_state_version FROM servers WHERE id = ?`, id,
	).Scan(&version); err != nil {
		return Server{}, 0, fmt.Errorf("read desired state version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Server{}, 0, fmt.Errorf("commit outbound preference update: %w", err)
	}
	updated, err := s.Get(ctx, id)
	return updated, version, err
}

func (s *Service) UpdateBlockChinaInbound(ctx context.Context, id int64, enabled bool) (Server, int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Server{}, 0, false, fmt.Errorf("begin China inbound block update: %w", err)
	}
	defer tx.Rollback()
	var current bool
	var version int64
	err = tx.QueryRowContext(ctx,
		`SELECT block_china_inbound, desired_state_version FROM servers
		 WHERE id = ? AND archived_at IS NULL`, id,
	).Scan(&current, &version)
	if err != nil {
		if err == sql.ErrNoRows {
			return Server{}, 0, false, ErrNotFound
		}
		return Server{}, 0, false, fmt.Errorf("read China inbound block setting: %w", err)
	}
	if current == enabled {
		_ = tx.Rollback()
		updated, getErr := s.Get(ctx, id)
		return updated, version, false, getErr
	}
	now := s.now().UTC().Truncate(time.Second).Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE servers SET block_china_inbound = ?, desired_state_version = desired_state_version + 1, updated_at = ?
		 WHERE id = ?`, enabled, now, id,
	); err != nil {
		return Server{}, 0, false, fmt.Errorf("update China inbound block setting: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agents SET config_sync_status = 'pending', config_sync_error = '', updated_at = ? WHERE server_id = ?`,
		now, id,
	); err != nil {
		return Server{}, 0, false, fmt.Errorf("mark Agent config pending: %w", err)
	}
	version++
	if err := tx.Commit(); err != nil {
		return Server{}, 0, false, fmt.Errorf("commit China inbound block update: %w", err)
	}
	updated, err := s.Get(ctx, id)
	return updated, version, true, err
}

func (s *Service) UpdateExpiration(ctx context.Context, id int64, expiresAt *time.Time) (Server, error) {
	var expiresAtValue any
	if expiresAt != nil {
		expiresAtValue = expiresAt.UTC().Truncate(time.Second).Unix()
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE servers SET expires_at = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		expiresAtValue, s.now().UTC().Truncate(time.Second).Unix(), id,
	)
	if err != nil {
		return Server{}, fmt.Errorf("update server expiration: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Server{}, fmt.Errorf("read updated server expiration count: %w", err)
	}
	if count != 1 {
		return Server{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Service) Archive(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin server archive: %w", err)
	}
	defer tx.Rollback()

	now := s.now().UTC().Truncate(time.Second).Unix()
	result, err := tx.ExecContext(ctx,
		`UPDATE servers SET status = ?, archived_at = ?, updated_at = ?
		 WHERE id = ? AND archived_at IS NULL`,
		StatusOffline, now, now, id,
	)
	if err != nil {
		return fmt.Errorf("archive server: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read archived server count: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, id); err != nil {
		return fmt.Errorf("revoke archived server agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, id,
	); err != nil {
		return fmt.Errorf("remove unused agent enrollments: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit server archive: %w", err)
	}
	return nil
}

func (s *Service) PermanentlyDelete(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM servers WHERE id = ? AND archived_at IS NOT NULL`, id,
	)
	if err != nil {
		return fmt.Errorf("permanently delete server: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read permanently deleted server count: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
