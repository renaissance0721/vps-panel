package agentcontrol

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

// Enrollment is returned only when issuing a token; persistence stores its hash.
type Enrollment struct {
	Token     string
	ExpiresAt time.Time
	CreatedAt time.Time
	hash      string
}

func NewEnrollment(clock func() time.Time) (Enrollment, error) {
	value, hash, err := token.New()
	if err != nil {
		return Enrollment{}, err
	}
	now := clock().UTC().Truncate(time.Second)
	return Enrollment{Token: value, hash: hash, CreatedAt: now, ExpiresAt: now.Add(EnrollmentLifetime)}, nil
}

// Insert participates in the caller's Server creation/rotation transaction.
func (e Enrollment) Insert(ctx context.Context, tx *sql.Tx, serverID int64, purpose string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO agent_enrollments (server_id, token_hash, purpose, expires_at, created_at)
   VALUES (?, ?, ?, ?, ?)`,
		serverID, e.hash, purpose, e.ExpiresAt.Unix(), e.CreatedAt.Unix(),
	)
	return err
}

func RotateEnrollment(ctx context.Context, tx *sql.Tx, serverID int64, clock func() time.Time) (Enrollment, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, serverID); err != nil {
		return Enrollment{}, fmt.Errorf("remove previous Agent enrollment: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE server_id = ?`, serverID); err != nil {
		return Enrollment{}, fmt.Errorf("revoke previous Agent: %w", err)
	}
	enrollment, err := NewEnrollment(clock)
	if err != nil {
		return Enrollment{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE servers SET status = ?, updated_at = ? WHERE id = ?`, statusPending, enrollment.CreatedAt.Unix(), serverID); err != nil {
		return Enrollment{}, fmt.Errorf("prepare server Agent enrollment: %w", err)
	}
	if err := enrollment.Insert(ctx, tx, serverID, PurposeRebind); err != nil {
		return Enrollment{}, fmt.Errorf("create Agent enrollment: %w", err)
	}
	return enrollment, nil
}
