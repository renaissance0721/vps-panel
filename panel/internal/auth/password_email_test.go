package auth

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	securetoken "github.com/renaissance0721/vps-panel/panel/internal/token"
	"golang.org/x/crypto/bcrypt"
)

func TestLoginVerifiedEmailForEveryRolePreservesUsernameRules(t *testing.T) {
	service, db := newTestService(t)
	user, err := service.Initialize(t.Context(), "AdminCase", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{RoleAdmin, RoleVIP, RoleCarpool, RoleSubscriber} {
		if _, err := db.Exec(`UPDATE users SET role = ?, email = 'login@example.com', email_verified_at = 1 WHERE id = ?`, role, user.ID); err != nil {
			t.Fatal(err)
		}
		for _, identifier := range []string{"AdminCase", " AdminCase ", "login@example.com", "  LOGIN@Example.COM  "} {
			got, err := service.Login(t.Context(), identifier, testPassword)
			if err != nil || got.ID != user.ID || got.Role != role {
				t.Fatalf("login %q/%s: %v", identifier, role, err)
			}
		}
	}
	for _, identifier := range []string{"admincase", "unknown", "missing@example.com", "Name <login@example.com>", "not-an-email@"} {
		if _, err := service.Login(t.Context(), identifier, testPassword); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("invalid identifier %q: %v", identifier, err)
		}
	}
	if _, err := service.Login(t.Context(), "login@example.com", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("wrong password accepted")
	}
	if _, err := db.Exec(`UPDATE users SET email_verified_at = NULL WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(t.Context(), "login@example.com", testPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("unverified email accepted")
	}
	if _, err := service.Login(t.Context(), user.Username, testPassword); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordResetEmailPreparationHashRateResendAndVerificationIsolation(t *testing.T) {
	service, db := newTestService(t)
	user, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.now = func() time.Time { return now }
	for _, identifier := range []string{"admin", "unknown", "missing@example.com", "Name <admin@example.com>"} {
		request, err := service.PreparePasswordResetEmail(t.Context(), identifier)
		if err != nil || request != nil {
			t.Fatalf("unavailable reset request: %v", err)
		}
	}
	if _, err := db.Exec(`UPDATE users SET email='admin@example.com' WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	request, err := service.PreparePasswordResetEmail(t.Context(), "admin")
	if err != nil || request != nil {
		t.Fatal("unverified email generated reset")
	}
	if _, err := db.Exec(`UPDATE users SET email_verified_at=1 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := service.PrepareEmailVerification(t.Context(), user.ID, "next@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.PreparePasswordResetEmail(t.Context(), "  ADMIN@Example.COM  ")
	if err != nil || first == nil {
		t.Fatalf("prepare reset: %v", err)
	}
	if first.Target != "admin@example.com" || first.ExpiresAt.Sub(now) != PasswordResetLifetime {
		t.Fatal("reset target/lifetime mismatch")
	}
	var hash string
	if err := db.QueryRow(`SELECT token_hash FROM account_tokens WHERE id=?`, first.ID).Scan(&hash); err != nil || hash != securetoken.Hash(first.Token) || hash == first.Token {
		t.Fatal("reset token hash mismatch")
	}
	if _, err := service.VerifyEmail(t.Context(), first.Token); !errors.Is(err, ErrEmailVerificationInvalid) {
		t.Fatal("reset token used as verification")
	}
	if _, err := service.ResetPasswordByEmail(t.Context(), pending.Token, "replacement-password"); !errors.Is(err, ErrPasswordResetTokenInvalid) {
		t.Fatal("verification token used as reset")
	}
	request, err = service.PreparePasswordResetEmail(t.Context(), user.Username)
	if err != nil || request != nil {
		t.Fatal("account reset send limit bypassed via username")
	}
	now = now.Add(60 * time.Second)
	second, err := service.PreparePasswordResetEmail(t.Context(), user.Username)
	if err != nil || second == nil {
		t.Fatalf("resend: %v", err)
	}
	if first.Token == second.Token {
		t.Fatal("reset reused token")
	}
	if _, err := service.ResetPasswordByEmail(t.Context(), first.Token, "replacement-password"); !errors.Is(err, ErrPasswordResetTokenInvalid) {
		t.Fatal("old reset token survived resend")
	}
	status, err := service.GetEmailStatus(t.Context(), user.ID)
	if err != nil || status.PendingEmail != "next@example.com" {
		t.Fatal("reset interfered with pending email")
	}
	if err := service.CancelPasswordResetEmail(t.Context(), second.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResetPasswordByEmail(t.Context(), second.Token, "replacement-password"); !errors.Is(err, ErrPasswordResetTokenInvalid) {
		t.Fatal("cancelled reset accepted")
	}
	request, err = service.PreparePasswordResetEmail(t.Context(), user.Username)
	if err != nil || request != nil {
		t.Fatal("failed delivery bypassed account send limit")
	}
}

func TestEmailResetInvalidTokensPasswordBytesAndAtomicCompletion(t *testing.T) {
	service, db := newTestService(t)
	user, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET email='admin@example.com', email_verified_at=1 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	service.now = func() time.Time { return now }
	request, err := service.PreparePasswordResetEmail(t.Context(), user.Username)
	if err != nil || request == nil {
		t.Fatalf("prepare reset: %v", err)
	}
	for _, invalid := range []string{"", "unknown", request.Token + "tampered"} {
		if _, err := service.ResetPasswordByEmail(t.Context(), invalid, "replacement-password"); !errors.Is(err, ErrPasswordResetTokenInvalid) {
			t.Fatal("invalid reset accepted")
		}
	}
	for _, password := range []string{"short", strings.Repeat("x", 73), strings.Repeat("中", 25)} {
		if _, err := service.ResetPasswordByEmail(t.Context(), request.Token, password); !errors.Is(err, ErrInvalidPassword) {
			t.Fatal("invalid password length accepted")
		}
	}
	now = now.Add(PasswordResetLifetime)
	if _, err := service.ResetPasswordByEmail(t.Context(), request.Token, "replacement-password"); !errors.Is(err, ErrPasswordResetTokenInvalid) {
		t.Fatal("expired reset accepted")
	}
	now = now.Add(-PasswordResetLifetime)
	for _, statement := range []string{`UPDATE users SET email='changed@example.com'`, `UPDATE users SET email='admin@example.com',email_verified_at=NULL`} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
		if _, err := service.ResetPasswordByEmail(t.Context(), request.Token, "replacement-password"); !errors.Is(err, ErrPasswordResetTokenInvalid) {
			t.Fatal("reset target ownership was not checked")
		}
	}
	if _, err := db.Exec(`UPDATE users SET email_verified_at=1`); err != nil {
		t.Fatal(err)
	}
	session, _, err := service.CreateSession(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CreateSession(t.Context(), user.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := service.RequestPasswordResetForUser(t.Context(), user.ID, "admin-proposed-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_reset_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT,'test rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResetPasswordByEmail(t.Context(), request.Token, "replacement-password"); err == nil {
		t.Fatal("reset ignored failed session revocation")
	}
	if _, err := service.Login(t.Context(), user.Username, testPassword); err != nil {
		t.Fatal("password changed despite rollback")
	}
	var active int
	if err := db.QueryRow(`SELECT COUNT(*) FROM account_tokens WHERE id=? AND used_at IS NULL`, request.ID).Scan(&active); err != nil || active != 1 {
		t.Fatal("token consumption was not rolled back")
	}
	if _, err := db.Exec(`DROP TRIGGER reject_reset_session_delete`); err != nil {
		t.Fatal(err)
	}
	newPassword := strings.Repeat("中", 24)
	if _, err := service.ResetPasswordByEmail(t.Context(), request.Token, newPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(t.Context(), session); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("reset preserved old session")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, user.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("reset did not revoke all sessions")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM account_tokens WHERE user_id=? AND purpose='reset_password' AND used_at IS NULL`, user.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("reset left active tokens")
	}
	if err := service.ReviewPasswordChangeRequest(t.Context(), pending.ID, 999, true); !errors.Is(err, ErrPasswordRequestNotFound) {
		t.Fatal("stale admin request could overwrite reset")
	}
	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, user.ID).Scan(&hash); err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(newPassword)) != nil {
		t.Fatal("reset password hash mismatch")
	}
	if _, err := service.Login(t.Context(), "ADMIN@EXAMPLE.COM", newPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResetPasswordByEmail(t.Context(), request.Token, "another-password"); !errors.Is(err, ErrPasswordResetTokenInvalid) {
		t.Fatal("reset token reused")
	}
}

func TestEmailResetCrossPathInvalidation(t *testing.T) {
	for _, path := range []string{"change_password", "admin_approve", "email_change", "admin_reject"} {
		t.Run(path, func(t *testing.T) {
			service, db := newTestService(t)
			admin, err := service.Initialize(t.Context(), "admin", testPassword)
			if err != nil {
				t.Fatal(err)
			}
			invite, err := service.CreateInvitation(t.Context(), admin.ID, RoleCarpool)
			if err != nil {
				t.Fatal(err)
			}
			user, err := service.RegisterWithInvitation(t.Context(), invite.Token, "member", testPassword)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE users SET email='old@example.com',email_verified_at=1 WHERE id=?`, user.ID); err != nil {
				t.Fatal(err)
			}
			request, err := service.PreparePasswordResetEmail(t.Context(), user.Username)
			if err != nil || request == nil {
				t.Fatalf("prepare: %v", err)
			}
			switch path {
			case "change_password":
				err = service.ChangePassword(t.Context(), user.ID, testPassword, "changed-password")
			case "admin_approve", "admin_reject":
				pending, prepareErr := service.RequestPasswordResetForUser(t.Context(), user.ID, "approved-password")
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				err = service.ReviewPasswordChangeRequest(t.Context(), pending.ID, admin.ID, path == "admin_approve")
			case "email_change":
				pending, prepareErr := service.PrepareEmailVerification(t.Context(), user.ID, "new@example.com", testPassword)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				_, err = service.VerifyEmail(t.Context(), pending.Token)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.ResetPasswordByEmail(t.Context(), request.Token, "replacement-password")
			if path == "admin_reject" {
				if err != nil {
					t.Fatal("rejection incorrectly invalidated reset")
				}
			} else if !errors.Is(err, ErrPasswordResetTokenInvalid) {
				t.Fatal("old reset survived security change")
			}
		})
	}
}

func TestConcurrentEmailResetConsumesTokenOnce(t *testing.T) {
	service, db := newTestService(t)
	user, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET email='admin@example.com',email_verified_at=1`); err != nil {
		t.Fatal(err)
	}
	request, err := service.PreparePasswordResetEmail(t.Context(), user.Username)
	if err != nil || request == nil {
		t.Fatalf("prepare: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := service.ResetPasswordByEmail(t.Context(), request.Token, "replacement-password")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrPasswordResetTokenInvalid) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("reset successes = %d", success)
	}
}
