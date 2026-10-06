package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Service) RequestPasswordResetForUser(ctx context.Context, userID int64, newPassword string) (PasswordChangeRequest, error) {
	if err := validatePassword(newPassword); err != nil {
		return PasswordChangeRequest{}, err
	}
	proposedHash, err := hashPassword(newPassword)
	if err != nil {
		return PasswordChangeRequest{}, err
	}
	return s.createPasswordResetRequest(ctx, userID, proposedHash)
}

func (s *Service) RequestPasswordResetByUsername(ctx context.Context, username, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	proposedHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	var userID int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, strings.TrimSpace(username)).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find password reset user: %w", err)
	}
	_, err = s.createPasswordResetRequest(ctx, userID, proposedHash)
	if errors.Is(err, ErrPasswordRequestPending) || errors.Is(err, ErrUserNotFound) {
		return nil
	}
	return err
}

func (s *Service) createPasswordResetRequest(ctx context.Context, userID int64, proposedHash string) (PasswordChangeRequest, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PasswordChangeRequest{}, fmt.Errorf("begin password reset request: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, userID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return PasswordChangeRequest{}, ErrUserNotFound
	} else if err != nil {
		return PasswordChangeRequest{}, fmt.Errorf("read password reset user: %w", err)
	}
	var pending int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM password_change_requests WHERE user_id = ? AND status = 'pending'`, userID,
	).Scan(&pending); err != nil {
		return PasswordChangeRequest{}, fmt.Errorf("count pending password requests: %w", err)
	}
	if pending != 0 {
		return PasswordChangeRequest{}, ErrPasswordRequestPending
	}
	now := s.now().UTC().Truncate(time.Second)
	result, err := tx.ExecContext(ctx,
		`INSERT INTO password_change_requests (user_id, proposed_password_hash, status, created_at)
		 VALUES (?, ?, 'pending', ?)`, userID, proposedHash, now.Unix(),
	)
	if err != nil {
		return PasswordChangeRequest{}, fmt.Errorf("create password reset request: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return PasswordChangeRequest{}, fmt.Errorf("read password reset request id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PasswordChangeRequest{}, fmt.Errorf("commit password reset request: %w", err)
	}
	return PasswordChangeRequest{ID: id, UserID: userID, Status: "pending", CreatedAt: now}, nil
}

func (s *Service) LatestPasswordChangeRequest(ctx context.Context, userID int64) (*PasswordChangeRequest, error) {
	request, err := scanPasswordChangeRequest(s.db.QueryRowContext(ctx,
		`SELECT requests.id, requests.user_id, users.username, users.role, requests.status,
		 requests.created_at, requests.reviewed_by, requests.reviewed_at
		 FROM password_change_requests AS requests
		 JOIN users ON users.id = requests.user_id
		 WHERE requests.user_id = ? ORDER BY requests.created_at DESC, requests.id DESC LIMIT 1`, userID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest password change request: %w", err)
	}
	return &request, nil
}

func (s *Service) ListPendingPasswordChangeRequests(ctx context.Context) ([]PasswordChangeRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT requests.id, requests.user_id, users.username, users.role, requests.status,
		 requests.created_at, requests.reviewed_by, requests.reviewed_at
		 FROM password_change_requests AS requests
		 JOIN users ON users.id = requests.user_id
		 WHERE requests.status = 'pending' ORDER BY requests.created_at, requests.id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending password change requests: %w", err)
	}
	defer rows.Close()
	requests := make([]PasswordChangeRequest, 0)
	for rows.Next() {
		request, err := scanPasswordChangeRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("scan password change request: %w", err)
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s *Service) ReviewPasswordChangeRequest(ctx context.Context, requestID, adminID int64, approve bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password request review: %w", err)
	}
	defer tx.Rollback()
	var userID int64
	var proposedHash sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT user_id, proposed_password_hash FROM password_change_requests
		 WHERE id = ? AND status = 'pending'`, requestID,
	).Scan(&userID, &proposedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPasswordRequestNotFound
	}
	if err != nil {
		return fmt.Errorf("read pending password request: %w", err)
	}
	if userID == adminID {
		return ErrPasswordRequestSelfReview
	}
	now := s.now().UTC().Truncate(time.Second)
	status := "rejected"
	if approve {
		if !proposedHash.Valid || proposedHash.String == "" {
			return ErrPasswordRequestNotFound
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, proposedHash.String, now.Unix(), userID,
		)
		if err != nil {
			return fmt.Errorf("apply approved password: %w", err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			if err != nil {
				return fmt.Errorf("read updated password count: %w", err)
			}
			return ErrPasswordRequestNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("invalidate approved password sessions: %w", err)
		}
		if err := invalidatePasswordResetTokensTx(ctx, tx, userID, now.Unix()); err != nil {
			return err
		}
		status = "approved"
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE password_change_requests
		 SET status = ?, proposed_password_hash = NULL, reviewed_by = ?, reviewed_at = ?
		 WHERE id = ? AND status = 'pending'`, status, adminID, now.Unix(), requestID,
	)
	if err != nil {
		return fmt.Errorf("finish password request review: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err != nil {
			return fmt.Errorf("read reviewed password request count: %w", err)
		}
		return ErrPasswordRequestNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password request review: %w", err)
	}
	return nil
}

type passwordRequestScanner interface {
	Scan(...any) error
}

func scanPasswordChangeRequest(row passwordRequestScanner) (PasswordChangeRequest, error) {
	var request PasswordChangeRequest
	var createdAt int64
	var reviewedBy, reviewedAt sql.NullInt64
	if err := row.Scan(
		&request.ID, &request.UserID, &request.Username, &request.Role, &request.Status,
		&createdAt, &reviewedBy, &reviewedAt,
	); err != nil {
		return PasswordChangeRequest{}, err
	}
	request.CreatedAt = time.Unix(createdAt, 0).UTC()
	if reviewedBy.Valid {
		value := reviewedBy.Int64
		request.ReviewedBy = &value
	}
	if reviewedAt.Valid {
		value := time.Unix(reviewedAt.Int64, 0).UTC()
		request.ReviewedAt = &value
	}
	return request, nil
}
