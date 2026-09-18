package server

import (
	"context"
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
