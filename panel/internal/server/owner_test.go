package server

import (
	"context"
	"database/sql"
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

func assertServerOwner(t *testing.T, value Server, wantID int64, wantUsername string) {
	t.Helper()
	if value.OwnerUserID == nil || *value.OwnerUserID != wantID || value.OwnerUsername != wantUsername {
		t.Fatalf("server %d owner = (%v, %q), want (%d, %q)", value.ID, value.OwnerUserID, value.OwnerUsername, wantID, wantUsername)
	}
}
