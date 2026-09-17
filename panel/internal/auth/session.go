package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func (s *Service) CreateSession(ctx context.Context, userID int64) (string, time.Time, error) {
	tokenValue, tokenHash, err := token.New()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(SessionLifetime)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("begin session: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return "", time.Time{}, fmt.Errorf("remove expired sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		userID, tokenHash, expiresAt.Unix(), now.Unix(),
	); err != nil {
		return "", time.Time{}, fmt.Errorf("create session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, fmt.Errorf("commit session: %w", err)
	}
	return tokenValue, expiresAt, nil
}

func (s *Service) Authenticate(ctx context.Context, tokenValue string) (User, error) {
	if tokenValue == "" {
		return User{}, ErrUnauthenticated
	}
	var user User
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT users.id, users.username, users.role, users.created_at, users.updated_at
		FROM sessions
		JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND sessions.expires_at > ?`,
		token.Hash(tokenValue), s.now().UTC().Unix(),
	).Scan(&user.ID, &user.Username, &user.Role, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, fmt.Errorf("authenticate session: %w", err)
	}
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return user, nil
}

func (s *Service) Logout(ctx context.Context, tokenValue string) error {
	if tokenValue == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, token.Hash(tokenValue)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
