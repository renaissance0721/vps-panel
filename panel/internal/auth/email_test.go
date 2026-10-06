package auth

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	securetoken "github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestEmailBindingNormalizesHashesAndChangesOnlyAfterVerification(t *testing.T) {
	service, db := newTestService(t)
	now := time.Date(2026, time.October, 6, 1, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	user, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	request, err := service.PrepareEmailVerification(t.Context(), user.ID, "  Test@Example.COM  ", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if request.Target != "test@example.com" || request.ExpiresAt.Sub(now) != EmailVerificationLifetime {
		t.Fatalf("request target/lifetime = %q/%s", request.Target, request.ExpiresAt.Sub(now))
	}
	var storedEmail, tokenHash *string
	if err := db.QueryRow(`SELECT email FROM users WHERE id = ?`, user.ID).Scan(&storedEmail); err != nil || storedEmail != nil {
		t.Fatalf("email before verification = %v, %v", storedEmail, err)
	}
	if err := db.QueryRow(`SELECT token_hash FROM account_tokens WHERE id = ?`, request.ID).Scan(&tokenHash); err != nil ||
		tokenHash == nil || *tokenHash == request.Token || *tokenHash != securetoken.Hash(request.Token) {
		t.Fatalf("stored token hash mismatch: %v", err)
	}
	status, err := service.GetEmailStatus(t.Context(), user.ID)
	if err != nil || status.Email != "" || status.Verified || status.PendingEmail != "test@example.com" {
		t.Fatalf("pending status = %+v, %v", status, err)
	}
	verified, err := service.VerifyEmail(t.Context(), request.Token)
	if err != nil || verified.Email != "test@example.com" || verified.Changed || !verified.VerifiedAt.Equal(now) {
		t.Fatalf("verify = %+v, %v", verified, err)
	}
	if _, err := service.VerifyEmail(t.Context(), request.Token); !errors.Is(err, ErrEmailVerificationInvalid) {
		t.Fatalf("reused token error = %v", err)
	}

	now = now.Add(EmailRequestInterval)
	change, err := service.PrepareEmailVerification(t.Context(), user.ID, "NEW@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	status, err = service.GetEmailStatus(t.Context(), user.ID)
	if err != nil || status.Email != "test@example.com" || !status.Verified || status.PendingEmail != "new@example.com" {
		t.Fatalf("change pending status = %+v, %v", status, err)
	}
	changed, err := service.VerifyEmail(t.Context(), change.Token)
	if err != nil || !changed.Changed || changed.Email != "new@example.com" {
		t.Fatalf("change verify = %+v, %v", changed, err)
	}
	status, err = service.GetEmailStatus(t.Context(), user.ID)
	if err != nil || status.Email != "new@example.com" || !status.Verified || status.PendingEmail != "" {
		t.Fatalf("changed status = %+v, %v", status, err)
	}
}

func TestEmailRequestValidatesPasswordAddressDuplicatesAndFinalOwnership(t *testing.T) {
	service, db := newTestService(t)
	now := time.Date(2026, time.October, 6, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	admin, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := service.CreateInvitation(t.Context(), admin.ID, RoleCarpool)
	if err != nil {
		t.Fatal(err)
	}
	carpool, err := service.RegisterWithInvitation(t.Context(), invitation.Token, "carpool", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareEmailVerification(t.Context(), admin.ID, "Name <test@example.com>", testPassword); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("display address error = %v", err)
	}
	if _, err := service.PrepareEmailVerification(t.Context(), admin.ID, "test@example.com", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
	request, err := service.PrepareEmailVerification(t.Context(), admin.ID, "owner@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyEmail(t.Context(), request.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareEmailVerification(t.Context(), carpool.ID, "OWNER@EXAMPLE.COM", testPassword); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("case-insensitive duplicate error = %v", err)
	}

	now = now.Add(EmailRequestInterval)
	pending, err := service.PrepareEmailVerification(t.Context(), carpool.ID, "race@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET email = 'race@example.com', email_verified_at = ? WHERE id = ?`, now.Unix(), admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyEmail(t.Context(), pending.Token); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("final duplicate check error = %v", err)
	}
	status, err := service.GetEmailStatus(t.Context(), carpool.ID)
	if err != nil || status.Email != "" || status.PendingEmail != "race@example.com" {
		t.Fatalf("failed verification changed account = %+v, %v", status, err)
	}
}

func TestEmailResendRateLimitExpiryAndOldTokenInvalidation(t *testing.T) {
	service, db := newTestService(t)
	now := time.Date(2026, time.October, 6, 3, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	user, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.PrepareEmailVerification(t.Context(), user.ID, "test@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareEmailVerification(t.Context(), user.ID, "test@example.com", testPassword); err == nil {
		t.Fatal("immediate resend was accepted")
	} else {
		var limited *EmailRateLimitError
		if !errors.As(err, &limited) || limited.RetryAfter != EmailRequestInterval {
			t.Fatalf("rate limit error = %v", err)
		}
	}
	now = now.Add(EmailRequestInterval)
	second, err := service.PrepareEmailVerification(t.Context(), user.ID, "test@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyEmail(t.Context(), first.Token); !errors.Is(err, ErrEmailVerificationInvalid) {
		t.Fatalf("old token error = %v", err)
	}
	var firstUsedAt *int64
	if err := db.QueryRow(`SELECT used_at FROM account_tokens WHERE id = ?`, first.ID).Scan(&firstUsedAt); err != nil || firstUsedAt == nil {
		t.Fatalf("first token used_at = %v, %v", firstUsedAt, err)
	}
	now = second.ExpiresAt.Add(time.Second)
	if _, err := service.VerifyEmail(t.Context(), second.Token); !errors.Is(err, ErrEmailVerificationInvalid) {
		t.Fatalf("expired token error = %v", err)
	}
	if _, err := service.VerifyEmail(t.Context(), second.Token+"tampered"); !errors.Is(err, ErrEmailVerificationInvalid) {
		t.Fatalf("tampered token error = %v", err)
	}
}

func TestEmailVerificationIsAtomicAndConcurrentClaimHasSingleWinner(t *testing.T) {
	service, db := newTestService(t)
	now := time.Date(2026, time.October, 6, 4, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	admin, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	makeUser := func(username string) User {
		invitation, err := service.CreateInvitation(t.Context(), admin.ID, RoleVIP)
		if err != nil {
			t.Fatal(err)
		}
		user, err := service.RegisterWithInvitation(t.Context(), invitation.Token, username, testPassword)
		if err != nil {
			t.Fatal(err)
		}
		return user
	}
	firstUser, secondUser := makeUser("first"), makeUser("second")
	first, err := service.PrepareEmailVerification(t.Context(), firstUser.ID, "shared@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.PrepareEmailVerification(t.Context(), secondUser.ID, "shared@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsFound := make(chan error, 2)
	for _, raw := range []string{first.Token, second.Token} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, verifyErr := service.VerifyEmail(t.Context(), raw)
			errorsFound <- verifyErr
		}()
	}
	wg.Wait()
	close(errorsFound)
	var successes, conflicts int
	for verifyErr := range errorsFound {
		if verifyErr == nil {
			successes++
		} else if errors.Is(verifyErr, ErrEmailTaken) {
			conflicts++
		} else {
			t.Fatalf("concurrent verify error = %v", verifyErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results success/conflict = %d/%d", successes, conflicts)
	}

	third := makeUser("third")
	now = now.Add(EmailRequestInterval)
	atomicRequest, err := service.PrepareEmailVerification(t.Context(), third.ID, "atomic@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_email_token_use BEFORE UPDATE OF used_at ON account_tokens
		WHEN NEW.id = ` + fmt.Sprint(atomicRequest.ID) + ` BEGIN SELECT RAISE(ABORT, 'injected token failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyEmail(t.Context(), atomicRequest.Token); err == nil {
		t.Fatal("injected token failure was ignored")
	}
	status, err := service.GetEmailStatus(t.Context(), third.ID)
	if err != nil || status.Email != "" || status.PendingEmail != "atomic@example.com" {
		t.Fatalf("atomic rollback status = %+v, %v", status, err)
	}
}
