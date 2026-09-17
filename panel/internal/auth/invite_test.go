package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	securetoken "github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestInvitationIsHashedAndSingleUse(t *testing.T) {
	service, db := newTestService(t)
	ctx := context.Background()
	owner, err := service.Initialize(ctx, "admin", testPassword)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	created, err := service.CreateInvitation(ctx, owner.ID)
	if err != nil {
		t.Fatalf("CreateInvitation() error = %v", err)
	}
	if created.ExpiresAt.Sub(created.CreatedAt) != InvitationLifetime {
		t.Fatalf("invitation lifetime = %v, want %v", created.ExpiresAt.Sub(created.CreatedAt), InvitationLifetime)
	}
	var storedHash string
	if err := db.QueryRow(
		`SELECT token_hash FROM admin_invitations WHERE id = ?`,
		created.ID,
	).Scan(&storedHash); err != nil {
		t.Fatalf("read invitation hash: %v", err)
	}
	if storedHash == created.Token || storedHash != securetoken.Hash(created.Token) {
		t.Fatalf("invitation token was not stored as its hash")
	}

	active, err := service.ListActiveInvitations(ctx)
	if err != nil || len(active) != 1 || active[0].CreatedByUsername != owner.Username {
		t.Fatalf("ListActiveInvitations() = (%+v, %v), want one owner invitation", active, err)
	}
	invited, err := service.RegisterWithInvitation(ctx, created.Token, "invited", testPassword)
	if err != nil {
		t.Fatalf("RegisterWithInvitation() error = %v", err)
	}
	if invited.Role != RoleVIP {
		t.Fatalf("invited user role = %q, want %q", invited.Role, RoleVIP)
	}
	if _, err := service.RegisterWithInvitation(ctx, created.Token, "another", testPassword); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("reused invitation error = %v, want ErrInvalidInvitation", err)
	}
	active, err = service.ListActiveInvitations(ctx)
	if err != nil || len(active) != 0 {
		t.Fatalf("ListActiveInvitations() after use = (%+v, %v), want empty", active, err)
	}
}

func TestInvitationExpirationAndRevocation(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	owner, err := service.Initialize(ctx, "admin", testPassword)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	created, err := service.CreateInvitation(ctx, owner.ID)
	if err != nil {
		t.Fatalf("CreateInvitation() error = %v", err)
	}
	service.now = func() time.Time { return created.ExpiresAt.Add(time.Second) }
	if _, err := service.RegisterWithInvitation(ctx, created.Token, "expired", testPassword); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("expired invitation error = %v, want ErrInvalidInvitation", err)
	}

	second, err := service.CreateInvitation(ctx, owner.ID)
	if err != nil {
		t.Fatalf("second CreateInvitation() error = %v", err)
	}
	if err := service.RevokeInvitation(ctx, second.ID); err != nil {
		t.Fatalf("RevokeInvitation() error = %v", err)
	}
	if _, err := service.RegisterWithInvitation(ctx, second.Token, "revoked", testPassword); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("revoked invitation error = %v, want ErrInvalidInvitation", err)
	}
}
