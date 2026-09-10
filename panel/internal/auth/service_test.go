package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
)

const testPassword = "strong-password"

func TestInitializeCreatesOnlyFirstUserWithHashedPassword(t *testing.T) {
	service, db := newTestService(t)
	ctx := context.Background()

	needsInitialization, err := service.NeedsInitialization(ctx)
	if err != nil || !needsInitialization {
		t.Fatalf("NeedsInitialization() = (%v, %v), want (true, nil)", needsInitialization, err)
	}

	user, err := service.Initialize(ctx, "admin", testPassword)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if user.Username != "admin" {
		t.Fatalf("Initialize() username = %q, want admin", user.Username)
	}

	var passwordHash string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, user.ID).Scan(&passwordHash); err != nil {
		t.Fatalf("read password hash: %v", err)
	}
	if passwordHash == testPassword || passwordHash == "" {
		t.Fatalf("password was not safely hashed")
	}

	if _, err := service.Initialize(ctx, "second", testPassword); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Initialize() error = %v, want ErrAlreadyInitialized", err)
	}
	needsInitialization, err = service.NeedsInitialization(ctx)
	if err != nil || needsInitialization {
		t.Fatalf("NeedsInitialization() = (%v, %v), want (false, nil)", needsInitialization, err)
	}
}

func TestLoginSessionAndLogout(t *testing.T) {
	service, db := newTestService(t)
	ctx := context.Background()
	user, err := service.Initialize(ctx, "admin", testPassword)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	if _, err := service.Login(ctx, "admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() wrong password error = %v, want ErrInvalidCredentials", err)
	}
	loggedIn, err := service.Login(ctx, "ADMIN", testPassword)
	if err != nil || loggedIn.ID != user.ID {
		t.Fatalf("Login() = (%+v, %v), want user %d", loggedIn, err, user.ID)
	}

	token, _, err := service.CreateSession(ctx, user.ID)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	var storedHash string
	if err := db.QueryRow(`SELECT token_hash FROM sessions`).Scan(&storedHash); err != nil {
		t.Fatalf("read session hash: %v", err)
	}
	if storedHash == token || storedHash != hashToken(token) {
		t.Fatalf("session token was not stored as its hash")
	}
	authenticated, err := service.Authenticate(ctx, token)
	if err != nil || authenticated.ID != user.ID {
		t.Fatalf("Authenticate() = (%+v, %v), want user %d", authenticated, err, user.ID)
	}
	if err := service.Logout(ctx, token); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate() after logout error = %v, want ErrUnauthenticated", err)
	}
}

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
	if storedHash == created.Token || storedHash != hashToken(created.Token) {
		t.Fatalf("invitation token was not stored as its hash")
	}

	active, err := service.ListActiveInvitations(ctx)
	if err != nil || len(active) != 1 || active[0].CreatedByUsername != owner.Username {
		t.Fatalf("ListActiveInvitations() = (%+v, %v), want one owner invitation", active, err)
	}
	if _, err := service.RegisterWithInvitation(ctx, created.Token, "invited", testPassword); err != nil {
		t.Fatalf("RegisterWithInvitation() error = %v", err)
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

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(db), db
}
