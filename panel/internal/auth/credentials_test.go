package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestCaseSensitiveUsernames(t *testing.T) {
	service, _ := newTestService(t)
	ctx := t.Context()
	admin, err := service.Initialize(ctx, "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	register := func(name, password string) (User, error) {
		t.Helper()
		invitation, err := service.CreateInvitation(ctx, admin.ID)
		if err != nil {
			t.Fatal(err)
		}
		return service.RegisterWithInvitation(ctx, invitation.Token, name, password)
	}
	users := map[string]User{}
	for _, name := range []string{"refrain", "Refrain", "REFRAIN", "test", "Test", "TEST"} {
		user, err := register(" "+name+" ", name+"-password")
		if err != nil || user.Username != name {
			t.Fatalf("register %q = %+v, %v", name, user, err)
		}
		users[name] = user
	}
	if _, err := register("test", testPassword); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("exact duplicate = %v", err)
	}
	for _, name := range []string{"refrain", "Refrain"} {
		for _, passwordName := range []string{"refrain", "Refrain"} {
			user, err := service.Login(ctx, " "+name+" ", passwordName+"-password")
			if name == passwordName {
				if err != nil || user.ID != users[name].ID {
					t.Fatalf("login %s = %+v, %v", name, user, err)
				}
			} else if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("cross-case password login = %v", err)
			}
		}
	}
	if _, err := service.Login(ctx, "rEfRaIn", "Refrain-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("nonexistent variant login = %v", err)
	}
	if _, err := service.RenameUser(ctx, users["test"].ID, "test-password", "Refrain"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("rename to exact duplicate = %v", err)
	}
	// A new capitalization is available even while the lowercase account exists.
	if _, err := service.RenameUser(ctx, users["test"].ID, "test-password", "rEfrain"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RenameUser(ctx, users["test"].ID, "test-password", "REfrain"); err != nil {
		t.Fatalf("case-only rename = %v", err)
	}

	if err := service.RequestPasswordResetByUsername(ctx, " rEfRaIn ", "reset1"); err != nil {
		t.Fatal(err)
	}
	requests, err := service.ListPendingPasswordChangeRequests(ctx)
	if err != nil || len(requests) != 0 {
		t.Fatalf("nonexistent variant reset = %+v, %v", requests, err)
	}
	if err := service.RequestPasswordResetByUsername(ctx, " Refrain ", "reset1"); err != nil {
		t.Fatal(err)
	}
	requests, err = service.ListPendingPasswordChangeRequests(ctx)
	if err != nil || len(requests) != 1 || requests[0].UserID != users["Refrain"].ID {
		t.Fatalf("case-sensitive reset = %+v, %v", requests, err)
	}
	if err := service.ReviewPasswordChangeRequest(ctx, requests[0].ID, admin.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, password string }{{"Refrain", "reset1"}, {"refrain", "refrain-password"}} {
		if user, err := service.Login(ctx, test.name, test.password); err != nil || user.ID != users[test.name].ID {
			t.Fatalf("login after reset %s = %+v, %v", test.name, user, err)
		}
	}
}

func TestCaseOnlyRenameAndMissingLowercaseLogin(t *testing.T) {
	service, _ := newTestService(t)
	user, err := service.Initialize(t.Context(), "refrain", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := service.RenameUser(t.Context(), user.ID, testPassword, "Refrain")
	if err != nil || renamed.ID != user.ID || renamed.Username != "Refrain" {
		t.Fatalf("case-only rename = %+v, %v", renamed, err)
	}
	if _, err := service.Login(t.Context(), "refrain", testPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old capitalization login = %v", err)
	}
	if _, err := service.Login(t.Context(), "Refrain", testPassword); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordByteBoundariesAcrossAllFlows(t *testing.T) {
	for _, flow := range []string{"initialize", "register", "change", "reset-username", "reset-user"} {
		for _, test := range []struct {
			name, password string
			valid          bool
		}{
			{"5", "12345", false}, {"6", "123456", true},
			{"72", strings.Repeat("a", 72), true}, {"73", strings.Repeat("a", 73), false},
			{"UTF8-6", "密码", true},
		} {
			t.Run(flow+"/"+test.name, func(t *testing.T) {
				service, _ := newTestService(t)
				ctx := t.Context()
				var user User
				var err error
				if flow == "initialize" {
					user, err = service.Initialize(ctx, "admin", test.password)
				} else {
					user, err = service.Initialize(ctx, "admin", testPassword)
					if err != nil {
						t.Fatal(err)
					}
					switch flow {
					case "register":
						invitation, inviteErr := service.CreateInvitation(ctx, user.ID)
						if inviteErr != nil {
							t.Fatal(inviteErr)
						}
						user, err = service.RegisterWithInvitation(ctx, invitation.Token, "member", test.password)
					case "change":
						err = service.ChangePassword(ctx, user.ID, testPassword, test.password)
					case "reset-username":
						err = service.RequestPasswordResetByUsername(ctx, user.Username, test.password)
					case "reset-user":
						_, err = service.RequestPasswordResetForUser(ctx, user.ID, test.password)
					}
				}
				if !test.valid {
					if !errors.Is(err, ErrInvalidPassword) {
						t.Fatalf("invalid password error = %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(flow, "reset-") {
					request, err := service.LatestPasswordChangeRequest(ctx, user.ID)
					if err != nil || request == nil || request.Status != "pending" {
						t.Fatalf("valid reset = %+v, %v", request, err)
					}
				} else if loggedIn, err := service.Login(ctx, user.Username, test.password); err != nil || loggedIn.ID != user.ID {
					t.Fatalf("valid password login = %+v, %v", loggedIn, err)
				}
			})
		}
	}
}
