package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func (s *Service) CreateInvitation(ctx context.Context, createdBy int64, roles ...string) (CreatedInvitation, error) {
	role := RoleVIP
	if len(roles) == 1 {
		role = roles[0]
	}
	if len(roles) > 1 || !validInvitationRole(role) {
		return CreatedInvitation{}, ErrInvalidInvitationRole
	}
	tokenValue, tokenHash, err := token.New()
	if err != nil {
		return CreatedInvitation{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(InvitationLifetime)
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_invitations (token_hash, created_by, expires_at, role, created_at) VALUES (?, ?, ?, ?, ?)`,
		tokenHash, createdBy, expiresAt.Unix(), role, now.Unix(),
	)
	if err != nil {
		return CreatedInvitation{}, fmt.Errorf("create invitation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return CreatedInvitation{}, fmt.Errorf("read invitation id: %w", err)
	}
	return CreatedInvitation{
		Invitation: Invitation{ID: id, CreatedBy: createdBy, Role: role, ExpiresAt: expiresAt, CreatedAt: now},
		Token:      tokenValue,
	}, nil
}

func validInvitationRole(role string) bool {
	return role == RoleVIP || role == RoleCarpool || role == RoleSubscriber
}

func (s *Service) GetInvitation(ctx context.Context, tokenValue string) (Invitation, error) {
	if tokenValue == "" {
		return Invitation{}, ErrInvalidInvitation
	}
	var invitation Invitation
	var expiresAt, createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT invitations.id, invitations.created_by, users.username,
		       invitations.role, invitations.expires_at, invitations.created_at
		FROM admin_invitations AS invitations
		JOIN users ON users.id = invitations.created_by
		WHERE invitations.token_hash = ? AND invitations.used_at IS NULL AND invitations.expires_at > ?`,
		token.Hash(tokenValue), s.now().UTC().Unix(),
	).Scan(
		&invitation.ID, &invitation.CreatedBy, &invitation.CreatedByUsername,
		&invitation.Role, &expiresAt, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrInvalidInvitation
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("get invitation: %w", err)
	}
	if !validInvitationRole(invitation.Role) {
		return Invitation{}, ErrInvalidInvitation
	}
	invitation.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	invitation.CreatedAt = time.Unix(createdAt, 0).UTC()
	return invitation, nil
}

func (s *Service) ListActiveInvitations(ctx context.Context) ([]Invitation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT invitations.id, invitations.created_by, users.username,
		       invitations.role, invitations.expires_at, invitations.created_at
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
			&invitation.Role,
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
	var invitationRole string
	err = tx.QueryRowContext(ctx,
		`SELECT id, role FROM admin_invitations WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		token.Hash(tokenValue), now.Unix(),
	).Scan(&invitationID, &invitationRole)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidInvitation
	}
	if err != nil {
		return User{}, fmt.Errorf("find invitation: %w", err)
	}
	if !validInvitationRole(invitationRole) {
		return User{}, ErrInvalidInvitation
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
		username, passwordHash, invitationRole, now.Unix(), now.Unix(),
	)
	if err != nil {
		return User{}, fmt.Errorf("create invited user: %w", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("read invited user id: %w", err)
	}
	if invitationRole == RoleSubscriber {
		subscriptionToken, _, err := token.New()
		if err != nil {
			return User{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscriber_profiles
			(user_id, enabled, subscription_token, created_at, updated_at) VALUES (?, 1, ?, ?, ?)`,
			userID, subscriptionToken, now.Unix(), now.Unix()); err != nil {
			return User{}, fmt.Errorf("create subscriber profile: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscriber_usage
			(user_id, cycle_started_at, updated_at) VALUES (?, ?, ?)`,
			userID, now.Unix(), now.Unix()); err != nil {
			return User{}, fmt.Errorf("create subscriber usage: %w", err)
		}
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

	return User{ID: userID, Username: username, Role: invitationRole, CreatedAt: now, UpdatedAt: now}, nil
}
