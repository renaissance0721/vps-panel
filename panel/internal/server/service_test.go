package server

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/database"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestCreateStoresPendingServerAndHashedEnrollment(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "  JP Native 01  ")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Name != "JP Native 01" || created.Status != StatusPending {
		t.Fatalf("Create() server = %+v, want trimmed name and pending status", created.Server)
	}
	if created.EnrollmentToken == "" {
		t.Fatal("Create() enrollment token is empty")
	}
	if created.EnrollmentExpiresAt.Sub(created.CreatedAt) != EnrollmentLifetime {
		t.Fatalf("enrollment lifetime = %v, want %v", created.EnrollmentExpiresAt.Sub(created.CreatedAt), EnrollmentLifetime)
	}

	var storedHash string
	if err := db.QueryRow(
		`SELECT token_hash FROM agent_enrollments WHERE server_id = ?`, created.ID,
	).Scan(&storedHash); err != nil {
		t.Fatalf("read enrollment hash: %v", err)
	}
	if storedHash == created.EnrollmentToken || storedHash != token.Hash(created.EnrollmentToken) {
		t.Fatal("enrollment token was not stored as its hash")
	}
}

func TestListGetAndDeleteServer(t *testing.T) {
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

	if err := service.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := service.Get(context.Background(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}
	var enrollmentCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM agent_enrollments WHERE server_id = ?`, created.ID,
	).Scan(&enrollmentCount); err != nil {
		t.Fatalf("count enrollments: %v", err)
	}
	if enrollmentCount != 0 {
		t.Fatalf("enrollment count after delete = %d, want 0", enrollmentCount)
	}
}

func TestCreateRejectsInvalidName(t *testing.T) {
	service, _ := newTestService(t)
	for _, name := range []string{"   ", strings.Repeat("a", maxNameLength+1)} {
		if _, err := service.Create(context.Background(), name); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Create(%q) error = %v, want ErrInvalidName", name, err)
		}
	}
}

func TestRegisterAgentConsumesEnrollmentAndStoresHashedToken(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "JP Native 01")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.4.0")
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if registered.ID <= 0 || registered.ServerID != created.ID || registered.Token == "" {
		t.Fatalf("RegisterAgent() = %+v, want agent for server %d with token", registered, created.ID)
	}

	var storedHash, version, status string
	var usedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT agents.token_hash, agents.version, enrollments.used_at, servers.status
		 FROM agents
		 JOIN agent_enrollments AS enrollments ON enrollments.server_id = agents.server_id
		 JOIN servers ON servers.id = agents.server_id
		 WHERE agents.id = ?`, registered.ID,
	).Scan(&storedHash, &version, &usedAt, &status); err != nil {
		t.Fatalf("read registered agent: %v", err)
	}
	if storedHash == registered.Token || storedHash != token.Hash(registered.Token) {
		t.Fatal("agent token was not stored as its hash")
	}
	if version != "v0.4.0" || !usedAt.Valid || status != StatusOffline {
		t.Fatalf("registered state = (version %q, used %v, status %q)", version, usedAt.Valid, status)
	}

	if _, err := service.RegisterAgent(
		context.Background(), created.EnrollmentToken, "v0.4.0",
	); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("second RegisterAgent() error = %v, want ErrInvalidEnrollment", err)
	}
}

func TestFailedAgentRegistrationDoesNotConsumeEnrollment(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Singapore 01")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := service.RegisterAgent(
		context.Background(), "invalid-token", "v0.4.0",
	); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("invalid token error = %v, want ErrInvalidEnrollment", err)
	}
	if _, err := service.RegisterAgent(
		context.Background(), created.EnrollmentToken, " ",
	); !errors.Is(err, ErrInvalidAgentVersion) {
		t.Fatalf("invalid version error = %v, want ErrInvalidAgentVersion", err)
	}

	var usedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT used_at FROM agent_enrollments WHERE server_id = ?`, created.ID,
	).Scan(&usedAt); err != nil {
		t.Fatalf("read enrollment: %v", err)
	}
	if usedAt.Valid {
		t.Fatal("failed registration consumed enrollment token")
	}
	if _, err := service.RegisterAgent(
		context.Background(), created.EnrollmentToken, "v0.4.0",
	); err != nil {
		t.Fatalf("valid RegisterAgent() after failures error = %v", err)
	}
}

func TestExpiredEnrollmentCannotRegisterAgent(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Expired 01")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	service.now = func() time.Time { return created.EnrollmentExpiresAt.Add(time.Second) }

	if _, err := service.RegisterAgent(
		context.Background(), created.EnrollmentToken, "v0.4.0",
	); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("expired enrollment error = %v, want ErrInvalidEnrollment", err)
	}
	var usedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT used_at FROM agent_enrollments WHERE server_id = ?`, created.ID,
	).Scan(&usedAt); err != nil {
		t.Fatalf("read enrollment: %v", err)
	}
	if usedAt.Valid {
		t.Fatal("expired enrollment token was consumed")
	}
}

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(db), db
}
