package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestServerOwnerLifecycle(t *testing.T) {
	service, db := newTestService(t)
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (5, 'refrain', 'hash', 'admin', 1, 1)`,
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (6, 'member', 'hash', 'vip', 1, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	publicServer, err := service.CreateForUser(context.Background(), "Public", VisibilityPublic, nil, 5)
	if err != nil {
		t.Fatalf("create public server: %v", err)
	}
	privateServer, err := service.CreateForUser(context.Background(), "Private", VisibilityPrivate, []int64{6}, 5)
	if err != nil {
		t.Fatalf("create private server: %v", err)
	}
	for _, value := range []Server{publicServer.Server, privateServer.Server} {
		assertServerOwner(t, value, 5, "refrain")
		var storedOwner sql.NullInt64
		if err := db.QueryRow(`SELECT owner_user_id FROM servers WHERE id = ?`, value.ID).Scan(&storedOwner); err != nil {
			t.Fatal(err)
		}
		if !storedOwner.Valid || storedOwner.Int64 != 5 {
			t.Fatalf("stored owner for server %d = %v, want 5", value.ID, storedOwner)
		}
	}

	readPrivate, err := service.Get(context.Background(), privateServer.ID)
	if err != nil {
		t.Fatalf("get private server: %v", err)
	}
	assertServerOwner(t, readPrivate, 5, "refrain")
	if _, err := service.CreateEnrollment(context.Background(), privateServer.ID); err != nil {
		t.Fatalf("create enrollment with owner columns: %v", err)
	}
	if _, err := service.UpdateAccess(context.Background(), privateServer.ID, 5, VisibilityPublic, nil); err != nil {
		t.Fatalf("update access: %v", err)
	}
	readPrivate, err = service.Get(context.Background(), privateServer.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertServerOwner(t, readPrivate, 5, "refrain")

	if err := service.ForceArchive(context.Background(), publicServer.ID); err != nil {
		t.Fatalf("archive public server: %v", err)
	}
	archived, err := service.ListArchived(context.Background())
	if err != nil || len(archived) != 1 {
		t.Fatalf("list archived servers = (%+v, %v)", archived, err)
	}
	assertServerOwner(t, archived[0], 5, "refrain")

	if _, err := db.Exec(`DELETE FROM users WHERE id = 5`); err != nil {
		t.Fatalf("delete owner: %v", err)
	}
	active, err := service.Get(context.Background(), privateServer.ID)
	if err != nil {
		t.Fatalf("get server after owner deletion: %v", err)
	}
	if active.OwnerUserID != nil || active.OwnerUsername != "" {
		t.Fatalf("deleted owner remained on active server: (%v, %q)", active.OwnerUserID, active.OwnerUsername)
	}
	archived, err = service.ListArchived(context.Background())
	if err != nil || len(archived) != 1 || archived[0].OwnerUserID != nil || archived[0].OwnerUsername != "" {
		t.Fatalf("deleted owner remained on archived server: (%+v, %v)", archived, err)
	}
	var serverCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers WHERE id IN (?, ?)`, publicServer.ID, privateServer.ID).Scan(&serverCount); err != nil || serverCount != 2 {
		t.Fatalf("servers after owner deletion = %d, error %v", serverCount, err)
	}
}

func TestUpdateServerOwnerDoesNotChangeAccessOrRoles(t *testing.T) {
	service, db := newTestService(t)
	for _, statement := range []string{
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (1, 'admin', 'hash', 'admin', 1, 1)`,
		`INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		 VALUES (2, 'member', 'hash', 'vip', 1, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	created, err := service.CreateForUser(context.Background(), "Private", VisibilityPrivate, nil, 1)
	if err != nil {
		t.Fatal(err)
	}

	memberID := int64(2)
	updated, err := service.UpdateOwner(context.Background(), created.ID, &memberID)
	if err != nil {
		t.Fatalf("update owner: %v", err)
	}
	assertServerOwner(t, updated, 2, "member")
	if updated.Visibility != VisibilityPrivate || !equalInt64s(updated.AccessUserIDs, []int64{1}) {
		t.Fatalf("owner update changed access = (%q, %v)", updated.Visibility, updated.AccessUserIDs)
	}
	allowed, err := service.CanAccess(context.Background(), memberID, created.ID)
	if err != nil || allowed {
		t.Fatalf("new owner access = (%t, %v), want false", allowed, err)
	}
	var adminRole, memberRole string
	if err := db.QueryRow(`SELECT role FROM users WHERE id = 1`).Scan(&adminRole); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT role FROM users WHERE id = 2`).Scan(&memberRole); err != nil {
		t.Fatal(err)
	}
	if adminRole != "admin" || memberRole != "vip" {
		t.Fatalf("owner update changed roles = (%q, %q)", adminRole, memberRole)
	}

	cleared, err := service.UpdateOwner(context.Background(), created.ID, nil)
	if err != nil {
		t.Fatalf("clear owner: %v", err)
	}
	if cleared.OwnerUserID != nil || cleared.OwnerUsername != "" {
		t.Fatalf("cleared owner = (%v, %q)", cleared.OwnerUserID, cleared.OwnerUsername)
	}
	for _, invalidID := range []int64{0, -1, 999} {
		if _, err := service.UpdateOwner(context.Background(), created.ID, &invalidID); !errors.Is(err, ErrInvalidServerOwner) {
			t.Fatalf("UpdateOwner(%d) error = %v", invalidID, err)
		}
	}
	if err := service.ForceArchive(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateOwner(context.Background(), created.ID, &memberID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived UpdateOwner() error = %v", err)
	}
}

func assertServerOwner(t *testing.T, value Server, wantID int64, wantUsername string) {
	t.Helper()
	if value.OwnerUserID == nil || *value.OwnerUserID != wantID || value.OwnerUsername != wantUsername {
		t.Fatalf("server %d owner = (%v, %q), want (%d, %q)", value.ID, value.OwnerUserID, value.OwnerUsername, wantID, wantUsername)
	}
}
