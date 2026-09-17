package agentcontrol

import (
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestEnrollmentRotationParticipatesInServerTransaction(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	if _, err = db.Exec(`INSERT INTO servers (id,name,status,created_at,updated_at) VALUES (7,'VPS','pending',?,?)`, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	enrollment, err := NewEnrollment(clock)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = enrollment.Insert(t.Context(), tx, 7, PurposeInitial); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	service := NewService(db, clock)
	registered, err := service.RegisterAgent(t.Context(), enrollment.Token, "v0.20.0", false)
	if err != nil {
		t.Fatal(err)
	}

	// The caller owns commit: failed resource work must not revoke a working Agent.
	tx, err = db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := RotateEnrollment(t.Context(), tx, 7, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthenticateAgent(t.Context(), registered.Token); err != nil {
		t.Fatalf("rollback revoked credential: %v", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM agent_enrollments WHERE token_hash = ?`, token.Hash(rolledBack.Token)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back enrollment persisted: %d, %v", count, err)
	}

	tx, err = db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := RotateEnrollment(t.Context(), tx, 7, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthenticateAgent(t.Context(), registered.Token); err != ErrInvalidAgentToken {
		t.Fatalf("old credential = %v", err)
	}
	var hash, purpose, status string
	var expiresAt int64
	if err = db.QueryRow(`SELECT e.token_hash,e.purpose,e.expires_at,s.status FROM agent_enrollments e JOIN servers s ON s.id=e.server_id WHERE e.used_at IS NULL`).Scan(&hash, &purpose, &expiresAt, &status); err != nil {
		t.Fatal(err)
	}
	if hash != token.Hash(rotated.Token) || hash == rotated.Token || purpose != PurposeRebind || status != statusPending || expiresAt != now.Add(24*time.Hour).Unix() {
		t.Fatal("rotation changed enrollment semantics")
	}
}
