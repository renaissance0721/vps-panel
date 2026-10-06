package auth

import (
	"database/sql"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestChangePasswordValidatesAndInvalidatesSessions(t *testing.T) {
	service, db := newTestService(t)
	user, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := service.CreateSession(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondToken, _, err := service.CreateSession(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ChangePassword(t.Context(), user.ID, "wrong-password", "replacement-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password error = %v", err)
	}
	if err := service.ChangePassword(t.Context(), user.ID, testPassword, testPassword); !errors.Is(err, ErrPasswordUnchanged) {
		t.Fatalf("unchanged password error = %v", err)
	}
	if err := service.ChangePassword(t.Context(), user.ID, testPassword, "replacement-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(t.Context(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old session authentication error = %v", err)
	}
	if _, err := service.Authenticate(t.Context(), secondToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("second old session authentication error = %v", err)
	}
	var sessionCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, user.ID).Scan(&sessionCount); err != nil || sessionCount != 0 {
		t.Fatalf("remaining sessions = %d, %v", sessionCount, err)
	}
	if _, err := service.Login(t.Context(), user.Username, testPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password login error = %v", err)
	}
	if _, err := service.Login(t.Context(), user.Username, "replacement-password"); err != nil {
		t.Fatalf("new password login error = %v", err)
	}
}

func TestPasswordResetRequestStoresOnlyHashAndHidesAccountState(t *testing.T) {
	service, db := newTestService(t)
	admin, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := service.CreateInvitation(t.Context(), admin.ID, RoleCarpool)
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.RegisterWithInvitation(t.Context(), invitation.Token, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RequestPasswordResetByUsername(t.Context(), "missing", "replacement-password"); err != nil {
		t.Fatalf("missing user reset error = %v", err)
	}
	if err := service.RequestPasswordResetByUsername(t.Context(), " alice ", "replacement-password"); err != nil {
		t.Fatalf("existing user reset error = %v", err)
	}
	if err := service.RequestPasswordResetByUsername(t.Context(), "alice", "another-password"); err != nil {
		t.Fatalf("pending user reset error = %v", err)
	}
	var requestCount int
	var storedHash string
	if err := db.QueryRow(`SELECT COUNT(*), proposed_password_hash FROM password_change_requests WHERE user_id = ?`, user.ID).
		Scan(&requestCount, &storedHash); err != nil {
		t.Fatal(err)
	}
	if requestCount != 1 || storedHash == "replacement-password" || bcrypt.CompareHashAndPassword([]byte(storedHash), []byte("replacement-password")) != nil {
		t.Fatalf("stored reset request = count %d hash %q", requestCount, storedHash)
	}
	if _, err := service.RequestPasswordResetForUser(t.Context(), user.ID, "another-password"); !errors.Is(err, ErrPasswordRequestPending) {
		t.Fatalf("authenticated duplicate error = %v", err)
	}
}

func TestPasswordResetReviewApprovalRejectionAndSelfReview(t *testing.T) {
	service, db := newTestService(t)
	admin, err := service.Initialize(t.Context(), "admin", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := service.CreateInvitation(t.Context(), admin.ID, RoleCarpool)
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.RegisterWithInvitation(t.Context(), invitation.Token, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := service.CreateSession(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	request, err := service.RequestPasswordResetForUser(t.Context(), user.ID, "replacement-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(t.Context(), "alice", testPassword); err != nil {
		t.Fatalf("old password before approval error = %v", err)
	}
	if _, err := service.Login(t.Context(), "alice", "replacement-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("new password before approval error = %v", err)
	}
	if err := service.ReviewPasswordChangeRequest(t.Context(), request.ID, admin.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(t.Context(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("approved reset session error = %v", err)
	}
	assertReviewedPasswordRequest(t, db, request.ID, "approved", admin.ID)
	if _, err := service.Login(t.Context(), "alice", "replacement-password"); err != nil {
		t.Fatalf("approved password login error = %v", err)
	}

	rejectRequest, err := service.RequestPasswordResetForUser(t.Context(), user.ID, "rejected-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ReviewPasswordChangeRequest(t.Context(), rejectRequest.ID, admin.ID, false); err != nil {
		t.Fatal(err)
	}
	assertReviewedPasswordRequest(t, db, rejectRequest.ID, "rejected", admin.ID)
	if _, err := service.Login(t.Context(), "alice", "replacement-password"); err != nil {
		t.Fatalf("password changed after rejection: %v", err)
	}

	selfRequest, err := service.RequestPasswordResetForUser(t.Context(), admin.ID, "admin-replacement")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ReviewPasswordChangeRequest(t.Context(), selfRequest.ID, admin.ID, true); !errors.Is(err, ErrPasswordRequestSelfReview) {
		t.Fatalf("self review error = %v", err)
	}
	var status string
	var proposedHash sql.NullString
	if err := db.QueryRow(`SELECT status, proposed_password_hash FROM password_change_requests WHERE id = ?`, selfRequest.ID).
		Scan(&status, &proposedHash); err != nil || status != "pending" || !proposedHash.Valid {
		t.Fatalf("self-reviewed request = (%q, %+v, %v)", status, proposedHash, err)
	}
}

func assertReviewedPasswordRequest(t *testing.T, db *sql.DB, requestID int64, wantStatus string, wantReviewer int64) {
	t.Helper()
	var status string
	var proposedHash sql.NullString
	var reviewedBy, reviewedAt sql.NullInt64
	if err := db.QueryRow(`SELECT status, proposed_password_hash, reviewed_by, reviewed_at
		FROM password_change_requests WHERE id = ?`, requestID).
		Scan(&status, &proposedHash, &reviewedBy, &reviewedAt); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || proposedHash.Valid || !reviewedBy.Valid || reviewedBy.Int64 != wantReviewer || !reviewedAt.Valid {
		t.Fatalf("reviewed request = (%q, %+v, %+v, %+v)", status, proposedHash, reviewedBy, reviewedAt)
	}
}
