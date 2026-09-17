package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestArchiveKeepsServerAndRemovesUnusedEnrollment(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Singapore 01")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	servers, err := service.List(context.Background())
	if err != nil || len(servers) != 1 || servers[0].ID != created.ID {
		t.Fatalf("List() = (%+v, %v), want created server", servers, err)
	}
	got, err := service.Get(context.Background(), created.ID)
	if err != nil || got.Name != created.Name {
		t.Fatalf("Get() = (%+v, %v), want created server", got, err)
	}

	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if _, err := service.Get(context.Background(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after archive error = %v, want ErrNotFound", err)
	}
	active, err := service.List(context.Background())
	if err != nil || len(active) != 0 {
		t.Fatalf("List() after archive = (%+v, %v), want empty", active, err)
	}
	archived, err := service.ListArchived(context.Background())
	if err != nil || len(archived) != 1 || archived[0].ID != created.ID || archived[0].ArchivedAt == nil {
		t.Fatalf("ListArchived() = (%+v, %v), want archived server %d", archived, err, created.ID)
	}
	var serverCount int
	var archivedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT COUNT(*), archived_at FROM servers WHERE id = ?`, created.ID,
	).Scan(&serverCount, &archivedAt); err != nil {
		t.Fatalf("read archived server: %v", err)
	}
	if serverCount != 1 || !archivedAt.Valid {
		t.Fatalf("archived server state = (count %d, archived %v), want preserved and archived", serverCount, archivedAt.Valid)
	}
	var enrollmentCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM agent_enrollments WHERE server_id = ?`, created.ID,
	).Scan(&enrollmentCount); err != nil {
		t.Fatalf("count enrollments: %v", err)
	}
	if enrollmentCount != 0 {
		t.Fatalf("unused enrollment count after archive = %d, want 0", enrollmentCount)
	}
}

func TestPermanentlyDeleteOnlyDeletesArchivedServer(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Permanent Delete")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := service.PermanentlyDelete(context.Background(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PermanentlyDelete() active server error = %v, want ErrNotFound", err)
	}
	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if err := service.PermanentlyDelete(context.Background(), created.ID); err != nil {
		t.Fatalf("PermanentlyDelete() error = %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers WHERE id = ?`, created.ID).Scan(&count); err != nil {
		t.Fatalf("count permanently deleted server: %v", err)
	}
	if count != 0 {
		t.Fatalf("server count after permanent delete = %d, want 0", count)
	}
}

func TestUpdateExpirationSetsModifiesClearsAndReturnsFromQueries(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Expiration")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	firstUpdatedAt := time.Date(2026, 9, 11, 13, 0, 0, 0, time.UTC)
	firstExpiration := time.Date(2026, 12, 31, 15, 59, 0, 0, time.UTC)
	service.now = func() time.Time { return firstUpdatedAt }
	updated, err := service.UpdateExpiration(context.Background(), created.ID, &firstExpiration)
	if err != nil {
		t.Fatalf("UpdateExpiration() error = %v", err)
	}
	if updated.ExpiresAt == nil || !updated.ExpiresAt.Equal(firstExpiration) || !updated.UpdatedAt.Equal(firstUpdatedAt) {
		t.Fatalf("updated expiration = (%v, updated %v)", updated.ExpiresAt, updated.UpdatedAt)
	}
	listed, err := service.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ExpiresAt == nil || !listed[0].ExpiresAt.Equal(firstExpiration) {
		t.Fatalf("List() expiration = (%+v, %v)", listed, err)
	}
	got, err := service.Get(context.Background(), created.ID)
	if err != nil || got.ExpiresAt == nil || !got.ExpiresAt.Equal(firstExpiration) {
		t.Fatalf("Get() expiration = (%v, %v)", got.ExpiresAt, err)
	}

	secondUpdatedAt := firstUpdatedAt.Add(time.Minute)
	secondExpiration := firstExpiration.Add(24 * time.Hour)
	service.now = func() time.Time { return secondUpdatedAt }
	updated, err = service.UpdateExpiration(context.Background(), created.ID, &secondExpiration)
	if err != nil || updated.ExpiresAt == nil || !updated.ExpiresAt.Equal(secondExpiration) ||
		!updated.UpdatedAt.Equal(secondUpdatedAt) {
		t.Fatalf("modified expiration = (%v, updated %v, error %v)", updated.ExpiresAt, updated.UpdatedAt, err)
	}

	clearedAt := secondUpdatedAt.Add(time.Minute)
	service.now = func() time.Time { return clearedAt }
	updated, err = service.UpdateExpiration(context.Background(), created.ID, nil)
	if err != nil || updated.ExpiresAt != nil || !updated.UpdatedAt.Equal(clearedAt) {
		t.Fatalf("cleared expiration = (%v, updated %v, error %v)", updated.ExpiresAt, updated.UpdatedAt, err)
	}
	if _, err := service.UpdateExpiration(context.Background(), created.ID+100, &firstExpiration); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing server expiration error = %v, want ErrNotFound", err)
	}
}

func TestExpirationSurvivesAgentLifecycleAndArchive(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Expiration Lifecycle")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	expiration := time.Date(2020, 1, 2, 3, 4, 0, 0, time.UTC)
	if _, err := service.UpdateExpiration(context.Background(), created.ID, &expiration); err != nil {
		t.Fatalf("UpdateExpiration() error = %v", err)
	}
	firstAgent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.6.1", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if err := service.SetAgentConnected(context.Background(), firstAgent.ID, firstAgent.ServerID); err != nil {
		t.Fatalf("SetAgentConnected() error = %v", err)
	}
	online, err := service.Get(context.Background(), created.ID)
	if err != nil || online.Status != StatusOnline || online.ExpiresAt == nil || !online.ExpiresAt.Equal(expiration) {
		t.Fatalf("online expired server = (%+v, %v)", online, err)
	}

	rebind, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil || rebind.ExpiresAt == nil || !rebind.ExpiresAt.Equal(expiration) {
		t.Fatalf("CreateEnrollment() expiration = (%v, %v)", rebind.ExpiresAt, err)
	}
	if _, err := service.RegisterAgent(context.Background(), rebind.EnrollmentToken, "v0.7.0", true); err != nil {
		t.Fatalf("replacement RegisterAgent() error = %v", err)
	}
	afterRebind, err := service.Get(context.Background(), created.ID)
	if err != nil || afterRebind.ExpiresAt == nil || !afterRebind.ExpiresAt.Equal(expiration) {
		t.Fatalf("expiration after rebind = (%v, %v)", afterRebind.ExpiresAt, err)
	}
	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	archived, err := service.ListArchived(context.Background())
	if err != nil || len(archived) != 1 || archived[0].ExpiresAt == nil || !archived[0].ExpiresAt.Equal(expiration) {
		t.Fatalf("archived expiration = (%+v, %v)", archived, err)
	}
}
