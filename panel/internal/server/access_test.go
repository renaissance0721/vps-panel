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
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (4, 'dave', 'hash', 'carpool', 1, 1), (5, 'eve', 'hash', 'subscriber', 1, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.CreateForUser(context.Background(), "Invalid visibility", "hidden", nil, 1); !errors.Is(err, ErrInvalidVisibility) {
		t.Fatalf("invalid visibility error = %v", err)
	}
	for _, userIDs := range [][]int64{{999}, {4}, {5}, {1, 2, 4}, {1, 2, 5}} {
		if _, err := service.CreateForUser(context.Background(), "Invalid user", VisibilityPrivate, userIDs, 1); !errors.Is(err, ErrInvalidServerAccess) {
			t.Fatalf("invalid access %v creation error = %v", userIDs, err)
		}
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

	for _, userIDs := range [][]int64{{999}, {4}, {5}, {1, 2, 4}, {1, 2, 5}} {
		if _, err := service.UpdateAccess(context.Background(), created.ID, 1, VisibilityPrivate, userIDs); !errors.Is(err, ErrInvalidServerAccess) {
			t.Fatalf("invalid access %v UpdateAccess() error = %v", userIDs, err)
		}
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
	public, err := service.CreateForUser(t.Context(), "Public", VisibilityPublic, []int64{4, 5}, 1)
	if err != nil || len(public.AccessUserIDs) != 0 {
		t.Fatalf("public creation returned access IDs: %v, %v", public.AccessUserIDs, err)
	}
}

func TestServerAccessIgnoresNonManagerRows(t *testing.T) {
	service, db := newTestService(t)
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (1, 'alice', 'hash', 'admin', 1, 1), (2, 'bob', 'hash', 'vip', 1, 1),
		(4, 'dave', 'hash', 'carpool', 1, 1), (5, 'eve', 'hash', 'subscriber', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateForUser(t.Context(), "Private", VisibilityPrivate, []int64{1}, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{4, 5} {
		if _, err := db.Exec(`INSERT INTO server_access (server_id, user_id) VALUES (?, ?)`, created.ID, id); err != nil {
			t.Fatal(err)
		}
		allowed, err := service.CanAccess(t.Context(), id, created.ID)
		if err != nil || allowed {
			t.Fatalf("dirty row granted access to %d: %v, %v", id, allowed, err)
		}
		listed, err := service.ListForUser(t.Context(), id)
		if err != nil || len(listed) != 0 {
			t.Fatalf("dirty row leaked private server to %d: %+v, %v", id, listed, err)
		}
		if _, err := service.UpdateAccess(t.Context(), created.ID, id, VisibilityPublic, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("dirty row allowed access mutation by %d: %v", id, err)
		}
	}
	current, err := service.Get(t.Context(), created.ID)
	if err != nil || !equalInt64s(current.AccessUserIDs, []int64{1, 2}) {
		t.Fatalf("Get returned invalid access IDs: %+v, %v", current.AccessUserIDs, err)
	}
	listed, err := service.ListForUser(t.Context(), 1)
	if err != nil || len(listed) != 1 || !equalInt64s(listed[0].AccessUserIDs, []int64{1, 2}) {
		t.Fatalf("List returned invalid access IDs: %+v, %v", listed, err)
	}
	enrollment, err := service.CreateEnrollment(t.Context(), created.ID)
	if err != nil || !equalInt64s(enrollment.AccessUserIDs, []int64{1, 2}) {
		t.Fatalf("CreateEnrollment returned invalid access IDs: %+v, %v", enrollment.AccessUserIDs, err)
	}
	if err := service.Archive(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	archived, err := service.ListArchivedForUser(t.Context(), 1)
	if err != nil || len(archived) != 1 || !equalInt64s(archived[0].AccessUserIDs, []int64{1, 2}) {
		t.Fatalf("archived list returned invalid access IDs: %+v, %v", archived, err)
	}
	for _, id := range []int64{4, 5} {
		archived, err := service.ListArchivedForUser(t.Context(), id)
		if err != nil || len(archived) != 0 {
			t.Fatalf("dirty row leaked archived private server to %d: %+v, %v", id, archived, err)
		}
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
