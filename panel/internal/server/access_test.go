package server

import (
	"context"
	"errors"
	"testing"
)

func TestServerPrivateAccessLifecycle(t *testing.T) {
	service, db := newTestService(t)
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (1, 'alice', 'hash', 'admin', 1, 1)`,
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (2, 'bob', 'hash', 'vip', 1, 1)`,
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (3, 'carol', 'hash', 'vip', 1, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.CreateForUser(context.Background(), "Invalid visibility", "hidden", nil, 1); !errors.Is(err, ErrInvalidVisibility) {
		t.Fatalf("invalid visibility error = %v", err)
	}
	if _, err := service.CreateForUser(context.Background(), "Invalid user", VisibilityPrivate, []int64{999}, 1); !errors.Is(err, ErrInvalidServerAccess) {
		t.Fatalf("invalid access user creation error = %v", err)
	}
	var serverCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&serverCount); err != nil || serverCount != 0 {
		t.Fatalf("failed private creation left %d servers, error %v", serverCount, err)
	}

	created, err := service.CreateForUser(context.Background(), "Private", VisibilityPrivate, []int64{2}, 1)
	if err != nil {
		t.Fatalf("CreateForUser() error = %v", err)
	}
	if created.Visibility != VisibilityPrivate || !equalInt64s(created.AccessUserIDs, []int64{1, 2}) {
		t.Fatalf("created access = %q %v", created.Visibility, created.AccessUserIDs)
	}
	for userID, want := range map[int64]bool{1: true, 2: true, 3: false} {
		allowed, err := service.CanAccess(context.Background(), userID, created.ID)
		if err != nil || allowed != want {
			t.Fatalf("CanAccess(%d) = (%v, %v), want %v", userID, allowed, err, want)
		}
	}

	var desiredVersion int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, created.ID).Scan(&desiredVersion); err != nil {
		t.Fatal(err)
	}
	access, err := service.UpdateAccess(context.Background(), created.ID, 1, VisibilityPrivate, []int64{3})
	if err != nil {
		t.Fatalf("UpdateAccess() error = %v", err)
	}
	if !equalInt64s(access.UserIDs, []int64{1, 3}) {
		t.Fatalf("updated private users = %v, want [1 3]", access.UserIDs)
	}
	var updatedDesiredVersion int64
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, created.ID).Scan(&updatedDesiredVersion); err != nil {
		t.Fatal(err)
	}
	if updatedDesiredVersion != desiredVersion {
		t.Fatalf("desired state version changed from %d to %d", desiredVersion, updatedDesiredVersion)
	}

	if _, err := service.UpdateAccess(context.Background(), created.ID, 1, VisibilityPrivate, []int64{999}); !errors.Is(err, ErrInvalidServerAccess) {
		t.Fatalf("invalid user UpdateAccess() error = %v", err)
	}
	current, err := service.Get(context.Background(), created.ID)
	if err != nil || !equalInt64s(current.AccessUserIDs, []int64{1, 3}) {
		t.Fatalf("access changed after failed update: %+v, %v", current.AccessUserIDs, err)
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	current, err = service.Get(context.Background(), created.ID)
	if err != nil || !equalInt64s(current.AccessUserIDs, []int64{1}) {
		t.Fatalf("user cascade left access rows: %+v, %v", current.AccessUserIDs, err)
	}
	if _, err := service.UpdateAccess(context.Background(), created.ID, 1, VisibilityPrivate, []int64{1, 1}); err != nil {
		t.Fatalf("duplicate access update: %v", err)
	}
	var duplicateCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_access WHERE server_id = ?`, created.ID).Scan(&duplicateCount); err != nil || duplicateCount != 1 {
		t.Fatalf("normalized duplicate access rows = %d, error %v", duplicateCount, err)
	}

	access, err = service.UpdateAccess(context.Background(), created.ID, 1, VisibilityPublic, []int64{2})
	if err != nil || access.Visibility != VisibilityPublic || len(access.UserIDs) != 0 {
		t.Fatalf("public UpdateAccess() = (%+v, %v)", access, err)
	}
	var accessCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_access WHERE server_id = ?`, created.ID).Scan(&accessCount); err != nil {
		t.Fatal(err)
	}
	if accessCount != 0 {
		t.Fatalf("public server access rows = %d, want 0", accessCount)
	}
	access, err = service.UpdateAccess(context.Background(), created.ID, 1, VisibilityPrivate, []int64{2})
	if err != nil || access.Visibility != VisibilityPrivate || !equalInt64s(access.UserIDs, []int64{1, 2}) {
		t.Fatalf("public to private UpdateAccess() = (%+v, %v)", access, err)
	}
	if _, err := normalizeAccessUserIDs(nil, 0); !errors.Is(err, ErrInvalidServerAccess) {
		t.Fatalf("empty private access error = %v", err)
	}
	cascade, err := service.CreateForUser(context.Background(), "Cascade", VisibilityPrivate, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Archive(context.Background(), cascade.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.PermanentlyDelete(context.Background(), cascade.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_access WHERE server_id = ?`, cascade.ID).Scan(&accessCount); err != nil || accessCount != 0 {
		t.Fatalf("server cascade access rows = %d, error %v", accessCount, err)
	}
}

func equalInt64s(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
