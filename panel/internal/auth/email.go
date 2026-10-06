package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	mailservice "github.com/renaissance0721/vps-panel/panel/internal/mail"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
	"golang.org/x/crypto/bcrypt"
)

const (
	EmailVerificationLifetime = 30 * time.Minute
	EmailRequestInterval      = 60 * time.Second
	emailPurposeVerify        = "verify_email"
	emailPurposeChange        = "change_email"
)

func (s *Service) GetEmailStatus(ctx context.Context, userID int64) (EmailStatus, error) {
	var email sql.NullString
	var verifiedAt sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT email, email_verified_at FROM users WHERE id = ?`, userID).
		Scan(&email, &verifiedAt); errors.Is(err, sql.ErrNoRows) {
		return EmailStatus{}, ErrUserNotFound
	} else if err != nil {
		return EmailStatus{}, fmt.Errorf("read account email: %w", err)
	}
	status := EmailStatus{Verified: email.Valid && verifiedAt.Valid}
	if email.Valid {
		status.Email = email.String
	}
	var pending string
	err := s.db.QueryRowContext(ctx, `SELECT target FROM account_tokens
		WHERE user_id = ? AND purpose IN (?, ?) AND used_at IS NULL AND expires_at > ?
		ORDER BY created_at DESC, id DESC LIMIT 1`, userID, emailPurposeVerify, emailPurposeChange, s.now().UTC().Unix()).Scan(&pending)
	if err == nil {
		status.PendingEmail = pending
	} else if !errors.Is(err, sql.ErrNoRows) {
		return EmailStatus{}, fmt.Errorf("read pending account email: %w", err)
	}
	return status, nil
}

func (s *Service) PrepareEmailVerification(ctx context.Context, userID int64, requestedEmail, currentPassword string) (EmailVerificationRequest, error) {
	target, err := mailservice.NormalizeMailbox(requestedEmail)
	if err != nil {
		return EmailVerificationRequest{}, ErrInvalidEmail
	}
	rawToken, tokenHash, err := token.New()
	if err != nil {
		return EmailVerificationRequest{}, err
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EmailVerificationRequest{}, fmt.Errorf("begin email verification request: %w", err)
	}
	defer tx.Rollback()

	var passwordHash string
	var currentEmail sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT password_hash, email FROM users WHERE id = ?`, userID).
		Scan(&passwordHash, &currentEmail); errors.Is(err, sql.ErrNoRows) {
		return EmailVerificationRequest{}, ErrUserNotFound
	} else if err != nil {
		return EmailVerificationRequest{}, fmt.Errorf("read email verification account: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(currentPassword)) != nil {
		return EmailVerificationRequest{}, ErrInvalidCredentials
	}
	if currentEmail.Valid && currentEmail.String == target {
		return EmailVerificationRequest{}, ErrEmailUnchanged
	}
	var existingID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE lower(email) = lower(?) AND id != ?`, target, userID).Scan(&existingID)
	if err == nil {
		return EmailVerificationRequest{}, ErrEmailTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return EmailVerificationRequest{}, fmt.Errorf("check account email uniqueness: %w", err)
	}

	var previousCreatedAt int64
	err = tx.QueryRowContext(ctx, `SELECT created_at FROM account_tokens WHERE user_id = ?
		AND purpose IN (?, ?) ORDER BY created_at DESC, id DESC LIMIT 1`, userID, emailPurposeVerify, emailPurposeChange).
		Scan(&previousCreatedAt)
	if err == nil {
		remaining := EmailRequestInterval - now.Sub(time.Unix(previousCreatedAt, 0).UTC())
		if remaining > 0 {
			return EmailVerificationRequest{}, &EmailRateLimitError{RetryAfter: remaining}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return EmailVerificationRequest{}, fmt.Errorf("read previous email verification request: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE account_tokens SET used_at = ?
		WHERE user_id = ? AND purpose IN (?, ?) AND used_at IS NULL`, now.Unix(), userID, emailPurposeVerify, emailPurposeChange); err != nil {
		return EmailVerificationRequest{}, fmt.Errorf("invalidate previous email verification: %w", err)
	}
	purpose := emailPurposeVerify
	if currentEmail.Valid {
		purpose = emailPurposeChange
	}
	expiresAt := now.Add(EmailVerificationLifetime)
	result, err := tx.ExecContext(ctx, `INSERT INTO account_tokens
		(user_id, purpose, target, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, purpose, target, tokenHash, expiresAt.Unix(), now.Unix())
	if err != nil {
		return EmailVerificationRequest{}, fmt.Errorf("create email verification request: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return EmailVerificationRequest{}, fmt.Errorf("read email verification request id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return EmailVerificationRequest{}, fmt.Errorf("commit email verification request: %w", err)
	}
	return EmailVerificationRequest{
		ID: id, UserID: userID, Purpose: purpose, Target: target, Token: rawToken, ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) CancelEmailVerification(ctx context.Context, requestID, userID int64) error {
	// Preserve the request timestamp so failed SMTP delivery cannot bypass the
	// per-account send interval, while making the undelivered token unusable.
	if _, err := s.db.ExecContext(ctx, `UPDATE account_tokens SET used_at = ?
		WHERE id = ? AND user_id = ? AND used_at IS NULL`, s.now().UTC().Unix(), requestID, userID); err != nil {
		return fmt.Errorf("cancel email verification request: %w", err)
	}
	return nil
}

func (s *Service) VerifyEmail(ctx context.Context, rawToken string) (EmailVerificationResult, error) {
	if rawToken == "" {
		return EmailVerificationResult{}, ErrEmailVerificationInvalid
	}
	now := s.now().UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EmailVerificationResult{}, fmt.Errorf("begin email verification: %w", err)
	}
	defer tx.Rollback()

	var requestID, userID int64
	var username, target string
	var currentEmail sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT tokens.id, tokens.user_id, users.username, users.email, tokens.target
		FROM account_tokens AS tokens JOIN users ON users.id = tokens.user_id
		WHERE tokens.token_hash = ? AND tokens.purpose IN (?, ?)
		AND tokens.used_at IS NULL AND tokens.expires_at > ?`,
		token.Hash(rawToken), emailPurposeVerify, emailPurposeChange, now.Unix()).
		Scan(&requestID, &userID, &username, &currentEmail, &target)
	if errors.Is(err, sql.ErrNoRows) {
		return EmailVerificationResult{}, ErrEmailVerificationInvalid
	}
	if err != nil {
		return EmailVerificationResult{}, fmt.Errorf("read email verification token: %w", err)
	}

	var existingID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE lower(email) = lower(?) AND id != ?`, target, userID).Scan(&existingID)
	if err == nil {
		return EmailVerificationResult{}, ErrEmailTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return EmailVerificationResult{}, fmt.Errorf("recheck account email uniqueness: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE users SET email = ?, email_verified_at = ?, updated_at = ?
		WHERE id = ? AND NOT EXISTS (SELECT 1 FROM users AS other WHERE other.id != ? AND lower(other.email) = lower(?))`,
		target, now.Unix(), now.Unix(), userID, userID, target)
	if err != nil {
		return EmailVerificationResult{}, fmt.Errorf("bind verified account email: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return EmailVerificationResult{}, fmt.Errorf("read verified account email count: %w", err)
	} else if count != 1 {
		return EmailVerificationResult{}, ErrEmailTaken
	}
	result, err = tx.ExecContext(ctx, `UPDATE account_tokens SET used_at = ?
		WHERE id = ? AND user_id = ? AND used_at IS NULL`, now.Unix(), requestID, userID)
	if err != nil {
		return EmailVerificationResult{}, fmt.Errorf("consume email verification token: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil {
		return EmailVerificationResult{}, fmt.Errorf("read consumed email verification count: %w", err)
	} else if count != 1 {
		return EmailVerificationResult{}, ErrEmailVerificationInvalid
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_tokens SET used_at = ?
		WHERE user_id = ? AND purpose IN (?, ?) AND used_at IS NULL`, now.Unix(), userID, emailPurposeVerify, emailPurposeChange); err != nil {
		return EmailVerificationResult{}, fmt.Errorf("invalidate remaining email verification tokens: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return EmailVerificationResult{}, fmt.Errorf("commit email verification: %w", err)
	}
	return EmailVerificationResult{
		UserID: userID, Username: username, Email: target,
		Changed: currentEmail.Valid && currentEmail.String != target, VerifiedAt: now,
	}, nil
}
