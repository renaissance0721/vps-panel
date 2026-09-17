package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
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
	if created.ExpiresAt != nil {
		t.Fatalf("Create() ExpiresAt = %v, want nil", created.ExpiresAt)
	}
	if created.EnrollmentToken == "" {
		t.Fatal("Create() enrollment token is empty")
	}
	if created.EnrollmentExpiresAt.Sub(created.CreatedAt) != agentcontrol.EnrollmentLifetime {
		t.Fatalf("enrollment lifetime = %v, want %v", created.EnrollmentExpiresAt.Sub(created.CreatedAt), agentcontrol.EnrollmentLifetime)
	}

	var storedHash, purpose string
	if err := db.QueryRow(
		`SELECT token_hash, purpose FROM agent_enrollments WHERE server_id = ?`, created.ID,
	).Scan(&storedHash, &purpose); err != nil {
		t.Fatalf("read enrollment hash: %v", err)
	}
	if storedHash == created.EnrollmentToken || storedHash != token.Hash(created.EnrollmentToken) {
		t.Fatal("enrollment token was not stored as its hash")
	}
	if purpose != agentcontrol.PurposeInitial {
		t.Fatalf("enrollment purpose = %q, want %q", purpose, agentcontrol.PurposeInitial)
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
