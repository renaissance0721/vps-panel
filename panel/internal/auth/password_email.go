package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

const (
	PasswordResetLifetime = 30 * time.Minute
	passwordResetPurpose  = "reset_password"
)

func invalidatePasswordResetTokensTx(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE account_tokens SET used_at = ?
		WHERE user_id = ? AND purpose = ? AND used_at IS NULL`, now, userID, passwordResetPurpose); err != nil {
		return fmt.Errorf("invalidate password reset tokens: %w", err)
	}
	return nil
}

// A nil request deliberately covers unknown, unverified and account-limited
// identifiers alike. The public handler must not expose these distinctions.
func (s *Service) PreparePasswordResetEmail(ctx context.Context, identifier string) (*PasswordResetEmailRequest, error) {
	condition, identifier, valid := loginIdentifierLookup(identifier)
	if !valid {
		return nil, nil
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin password reset email: %w", err)
	}
	defer tx.Rollback()
	var request PasswordResetEmailRequest
	err = tx.QueryRowContext(ctx, `SELECT id, username, email FROM users WHERE (`+condition+`)
		AND email IS NOT NULL AND email_verified_at IS NOT NULL`, identifier).
		Scan(&request.UserID, &request.Username, &request.Target)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find password reset email account: %w", err)
	}
	var previous int64
	err = tx.QueryRowContext(ctx, `SELECT created_at FROM account_tokens
		WHERE user_id = ? AND purpose = ? ORDER BY created_at DESC, id DESC LIMIT 1`, request.UserID, passwordResetPurpose).Scan(&previous)
	if err == nil && now.Sub(time.Unix(previous, 0)) < EmailRequestInterval {
		return nil, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read password reset send interval: %w", err)
	}
	rawToken, tokenHash, err := token.New()
	if err != nil {
		return nil, err
	}
	request.Token = rawToken
	if err := invalidatePasswordResetTokensTx(ctx, tx, request.UserID, now.Unix()); err != nil {
		return nil, err
	}
	request.ExpiresAt = now.Add(PasswordResetLifetime)
	result, err := tx.ExecContext(ctx, `INSERT INTO account_tokens
		(user_id, purpose, target, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		request.UserID, passwordResetPurpose, request.Target, tokenHash, request.ExpiresAt.Unix(), now.Unix())
	if err != nil {
		return nil, fmt.Errorf("create password reset email token: %w", err)
	}
	request.ID, err = result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("read password reset email token id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit password reset email: %w", err)
	}
	return &request, nil
}

func (s *Service) CancelPasswordResetEmail(ctx context.Context, requestID, userID int64) error {
	// Retain created_at so failed delivery does not bypass the account send limit.
	if _, err := s.db.ExecContext(ctx, `UPDATE account_tokens SET used_at = ?
		WHERE id = ? AND user_id = ? AND purpose = ? AND used_at IS NULL`,
		s.now().UTC().Unix(), requestID, userID, passwordResetPurpose); err != nil {
		return fmt.Errorf("cancel password reset email: %w", err)
	}
	return nil
}

func (s *Service) ResetPasswordByEmail(ctx context.Context, rawToken, newPassword string) (User, error) {
	if err := validatePassword(newPassword); err != nil {
		return User{}, err
	}
	if rawToken == "" {
		return User{}, ErrPasswordResetTokenInvalid
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin email password reset: %w", err)
	}
	defer tx.Rollback()
	var user User
	var requestID int64
	err = tx.QueryRowContext(ctx, `SELECT tokens.id, users.id, users.username, users.role
		FROM account_tokens AS tokens JOIN users ON users.id = tokens.user_id
		WHERE tokens.token_hash = ? AND tokens.purpose = ? AND tokens.used_at IS NULL AND tokens.expires_at > ?
		AND users.email_verified_at IS NOT NULL AND users.email = tokens.target`,
		token.Hash(rawToken), passwordResetPurpose, now.Unix()).Scan(&requestID, &user.ID, &user.Username, &user.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrPasswordResetTokenInvalid
	}
	if err != nil {
		return User{}, fmt.Errorf("read email password reset token: %w", err)
	}
	passwordHash, err := hashPassword(newPassword)
	if err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, now.Unix(), user.ID); err != nil {
		return User{}, fmt.Errorf("apply email reset password: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE account_tokens SET used_at = ? WHERE id = ? AND used_at IS NULL`, now.Unix(), requestID)
	if err != nil {
		return User{}, fmt.Errorf("consume email password reset token: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return User{}, fmt.Errorf("read consumed reset token count: %w", err)
	} else if count != 1 {
		return User{}, ErrPasswordResetTokenInvalid
	}
	if err := invalidatePasswordResetTokensTx(ctx, tx, user.ID, now.Unix()); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, user.ID); err != nil {
		return User{}, fmt.Errorf("invalidate email reset sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM password_change_requests WHERE user_id = ? AND status = 'pending'`, user.ID); err != nil {
		return User{}, fmt.Errorf("clear superseded password requests: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit email password reset: %w", err)
	}
	return user, nil
}
