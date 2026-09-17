package auth

import (
	"context"
	"errors"
	"testing"

	securetoken "github.com/renaissance0721/vps-panel/panel/internal/token"
)

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
	if err != nil || loggedIn.ID != user.ID || loggedIn.Role != RoleAdmin {
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
	if storedHash == token || storedHash != securetoken.Hash(token) {
		t.Fatalf("session token was not stored as its hash")
	}
	authenticated, err := service.Authenticate(ctx, token)
	if err != nil || authenticated.ID != user.ID || authenticated.Role != RoleAdmin {
		t.Fatalf("Authenticate() = (%+v, %v), want user %d", authenticated, err, user.ID)
	}
	if err := service.Logout(ctx, token); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate() after logout error = %v, want ErrUnauthenticated", err)
	}
}
