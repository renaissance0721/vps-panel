package auth

import (
	"context"
	"errors"
	"testing"
)

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
	if user.Username != "admin" || user.Role != RoleAdmin {
		t.Fatalf("Initialize() user = %+v, want admin role", user)
	}

	var passwordHash, role string
	if err := db.QueryRow(`SELECT password_hash, role FROM users WHERE id = ?`, user.ID).Scan(&passwordHash, &role); err != nil {
		t.Fatalf("read password hash: %v", err)
	}
	if passwordHash == testPassword || passwordHash == "" {
		t.Fatalf("password was not safely hashed")
	}
	if role != RoleAdmin {
		t.Fatalf("stored first user role = %q, want %q", role, RoleAdmin)
	}

	if _, err := service.Initialize(ctx, "second", testPassword); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Initialize() error = %v, want ErrAlreadyInitialized", err)
	}
	needsInitialization, err = service.NeedsInitialization(ctx)
	if err != nil || needsInitialization {
		t.Fatalf("NeedsInitialization() = (%v, %v), want (false, nil)", needsInitialization, err)
	}
}

func TestListUsersReturnsAccountsWithoutCredentials(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	admin, err := service.Initialize(ctx, "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := service.CreateInvitation(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterWithInvitation(ctx, invitation.Token, "member", testPassword); err != nil {
		t.Fatal(err)
	}
	users, err := service.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Username != "admin" || users[0].Role != RoleAdmin ||
		users[1].Username != "member" || users[1].Role != RoleVIP {
		t.Fatalf("ListUsers() = %+v", users)
	}
}
