package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestRegisterAgentConsumesEnrollmentAndStoresHashedToken(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "JP Native 01")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.4.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if registered.ID <= 0 || registered.ServerID != created.ID || registered.Token == "" {
		t.Fatalf("RegisterAgent() = %+v, want agent for server %d with token", registered, created.ID)
	}

	var storedHash, version, status, syncStatus, syncError string
	var usedAt, syncedAt sql.NullInt64
	var appliedVersion int64
	if err := db.QueryRow(
		`SELECT agents.token_hash, agents.version, enrollments.used_at, servers.status,
		 agents.applied_config_version, agents.config_sync_status,
		 agents.config_sync_error, agents.config_synced_at
		 FROM agents
		 JOIN agent_enrollments AS enrollments ON enrollments.server_id = agents.server_id
		 JOIN servers ON servers.id = agents.server_id
		 WHERE agents.id = ?`, registered.ID,
	).Scan(&storedHash, &version, &usedAt, &status, &appliedVersion, &syncStatus, &syncError, &syncedAt); err != nil {
		t.Fatalf("read registered agent: %v", err)
	}
	if storedHash == registered.Token || storedHash != token.Hash(registered.Token) {
		t.Fatal("agent token was not stored as its hash")
	}
	if version != "v0.4.0" || !usedAt.Valid || status != StatusOffline {
		t.Fatalf("registered state = (version %q, used %v, status %q)", version, usedAt.Valid, status)
	}
	if appliedVersion != 0 || syncStatus != agentcontrol.ConfigSyncPending || syncError != "" || syncedAt.Valid {
		t.Fatalf("new Agent config state = (%d, %q, %q, %v), want defaults",
			appliedVersion, syncStatus, syncError, syncedAt.Valid)
	}

	if _, err := service.RegisterAgent(
		context.Background(), created.EnrollmentToken, "v0.4.0", false,
	); !errors.Is(err, agentcontrol.ErrInvalidEnrollment) {
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
		context.Background(), "invalid-token", "v0.4.0", false,
	); !errors.Is(err, agentcontrol.ErrInvalidEnrollment) {
		t.Fatalf("invalid token error = %v, want ErrInvalidEnrollment", err)
	}
	if _, err := service.RegisterAgent(
		context.Background(), created.EnrollmentToken, " ", false,
	); !errors.Is(err, agentcontrol.ErrInvalidAgentVersion) {
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
		context.Background(), created.EnrollmentToken, "v0.4.0", false,
	); err != nil {
		t.Fatalf("valid RegisterAgent() after failures error = %v", err)
	}
}

func TestInitialEnrollmentAllowsExistingAgentConfig(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "New Server On Existing VPS")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.4.0", true)
	if err != nil || registered.ServerID != created.ID {
		t.Fatalf("register with existing config = (%+v, %v), want server %d", registered, err, created.ID)
	}
	var purpose string
	var usedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT purpose, used_at FROM agent_enrollments WHERE server_id = ?`, created.ID,
	).Scan(&purpose, &usedAt); err != nil {
		t.Fatalf("read enrollment: %v", err)
	}
	if purpose != agentcontrol.PurposeInitial || !usedAt.Valid {
		t.Fatalf("enrollment = (%q, used %v), want initial and used", purpose, usedAt.Valid)
	}
	if _, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.4.0", true); !errors.Is(err, agentcontrol.ErrInvalidEnrollment) {
		t.Fatalf("reused initial token error = %v, want ErrInvalidEnrollment", err)
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
		context.Background(), created.EnrollmentToken, "v0.4.0", false,
	); !errors.Is(err, agentcontrol.ErrInvalidEnrollment) {
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
