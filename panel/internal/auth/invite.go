package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func (s *Service) CreateInvitation(ctx context.Context, createdBy int64) (CreatedInvitation, error) {
	tokenValue, tokenHash, err := token.New()
	if err != nil {
		return CreatedInvitation{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(InvitationLifetime)
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_invitations (token_hash, created_by, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		tokenHash, createdBy, expiresAt.Unix(), now.Unix(),
	)
	if err != nil {
		return CreatedInvitation{}, fmt.Errorf("create invitation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return CreatedInvitation{}, fmt.Errorf("read invitation id: %w", err)
	}
	return CreatedInvitation{
		Invitation: Invitation{ID: id, CreatedBy: createdBy, ExpiresAt: expiresAt, CreatedAt: now},
		Token:      tokenValue,
	}, nil
}

func (s *Service) ListActiveInvitations(ctx context.Context) ([]Invitation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT invitations.id, invitations.created_by, users.username,
		       invitations.expires_at, invitations.created_at
		FROM admin_invitations AS invitations
		JOIN users ON users.id = invitations.created_by
		WHERE invitations.used_at IS NULL AND invitations.expires_at > ?
		ORDER BY invitations.created_at DESC`, s.now().UTC().Unix())
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	defer rows.Close()

	invitations := make([]Invitation, 0)
	for rows.Next() {
		var invitation Invitation
		var expiresAt, createdAt int64
		if err := rows.Scan(
			&invitation.ID,
			&invitation.CreatedBy,
			&invitation.CreatedByUsername,
			&expiresAt,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan invitation: %w", err)
		}
		invitation.ExpiresAt = time.Unix(expiresAt, 0).UTC()
		invitation.CreatedAt = time.Unix(createdAt, 0).UTC()
		invitations = append(invitations, invitation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invitations: %w", err)
	}
	return invitations, nil
}

func (s *Service) RevokeInvitation(ctx context.Context, invitationID int64) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM admin_invitations WHERE id = ? AND used_at IS NULL AND expires_at > ?`,
		invitationID, s.now().UTC().Unix(),
	)
	if err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revoked invitation count: %w", err)
	}
	if count == 0 {
		return ErrInvitationNotFound
	}
	return nil
}

func (s *Service) RegisterWithInvitation(ctx context.Context, tokenValue, username, password string) (User, error) {
	if tokenValue == "" {
		return User{}, ErrInvalidInvitation
	}
	username, passwordHash, err := prepareCredentials(username, password)
	if err != nil {
		return User{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin invited registration: %w", err)
	}
	defer tx.Rollback()

	now := s.now().UTC().Truncate(time.Second)
	var invitationID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM admin_invitations WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		token.Hash(tokenValue), now.Unix(),
	).Scan(&invitationID)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidInvitation
	}
	if err != nil {
		return User{}, fmt.Errorf("find invitation: %w", err)
	}

	var existing int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE username = ?`, username).Scan(&existing)
	if err == nil {
		return User{}, ErrUsernameTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("check username: %w", err)
	}

	result, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		username, passwordHash, RoleVIP, now.Unix(), now.Unix(),
	)
	if err != nil {
		return User{}, fmt.Errorf("create invited user: %w", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("read invited user id: %w", err)
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE admin_invitations SET used_at = ? WHERE id = ? AND used_at IS NULL AND expires_at > ?`,
		now.Unix(), invitationID, now.Unix(),
	)
	if err != nil {
		return User{}, fmt.Errorf("use invitation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return User{}, fmt.Errorf("read used invitation count: %w", err)
	}
	if count != 1 {
		return User{}, ErrInvalidInvitation
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit invited registration: %w", err)
	}

	return User{ID: userID, Username: username, Role: RoleVIP, CreatedAt: now, UpdatedAt: now}, nil
}
