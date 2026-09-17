package server

import (
	"context"
	"errors"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestNeverRegisteredServerCreatesRebindEnrollmentWhenRequested(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Pending Server")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	regenerated, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	if regenerated.ID != created.ID || regenerated.Status != StatusPending || regenerated.ArchivedAt != nil {
		t.Fatalf("regenerated server = %+v, want same active pending server", regenerated.Server)
	}
	if regenerated.EnrollmentToken == "" || regenerated.EnrollmentToken == created.EnrollmentToken {
		t.Fatal("CreateEnrollment() did not return a new token")
	}

	var serverCount, unusedCount int
	var purpose string
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&serverCount); err != nil {
		t.Fatalf("count servers: %v", err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*), purpose FROM agent_enrollments
		 WHERE server_id = ? AND used_at IS NULL`, created.ID,
	).Scan(&unusedCount, &purpose); err != nil {
		t.Fatalf("read regenerated enrollment: %v", err)
	}
	if serverCount != 1 || unusedCount != 1 || purpose != agentcontrol.PurposeRebind {
		t.Fatalf("regenerated state = (servers %d, unused %d, purpose %q)", serverCount, unusedCount, purpose)
	}
	if _, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.5.2", false); !errors.Is(err, agentcontrol.ErrInvalidEnrollment) {
		t.Fatalf("old enrollment error = %v, want ErrInvalidEnrollment", err)
	}
	if _, err := service.RegisterAgent(context.Background(), regenerated.EnrollmentToken, "v0.5.2", true); err != nil {
		t.Fatalf("register existing Agent config with regenerated enrollment: %v", err)
	}
	if _, err := service.RegisterAgent(context.Background(), regenerated.EnrollmentToken, "v0.5.2", false); !errors.Is(err, agentcontrol.ErrInvalidEnrollment) {
		t.Fatalf("reused regenerated enrollment error = %v, want ErrInvalidEnrollment", err)
	}
}

func TestOfflineServerCreatesRebindEnrollment(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Offline Server")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.5.4", false); err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}

	rebind, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	if rebind.ID != created.ID || rebind.Status != StatusPending {
		t.Fatalf("CreateEnrollment() server = %+v, want same pending server", rebind.Server)
	}
	var purpose string
	if err := db.QueryRow(
		`SELECT purpose FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, created.ID,
	).Scan(&purpose); err != nil {
		t.Fatalf("read offline server enrollment purpose: %v", err)
	}
	if purpose != agentcontrol.PurposeRebind {
		t.Fatalf("offline server enrollment purpose = %q, want %q", purpose, agentcontrol.PurposeRebind)
	}
}

func TestRegisteredActiveServerCreatesRebindEnrollment(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Active Server")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.5.3", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if err := service.SetAgentOnline(context.Background(), created.ID); err != nil {
		t.Fatalf("SetAgentOnline() error = %v", err)
	}

	rebind, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	if rebind.ID != created.ID || rebind.Name != created.Name || !rebind.CreatedAt.Equal(created.CreatedAt) ||
		rebind.ArchivedAt != nil || rebind.Status != StatusPending {
		t.Fatalf("rebind server = %+v, want unchanged active server data and pending status", rebind.Server)
	}
	if _, err := service.AuthenticateAgent(context.Background(), registered.Token); !errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
		t.Fatalf("old Agent Token error = %v, want ErrInvalidAgentToken", err)
	}
	var serverCount, unusedCount int
	var purpose string
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&serverCount); err != nil {
		t.Fatalf("count servers: %v", err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*), purpose FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, created.ID,
	).Scan(&unusedCount, &purpose); err != nil {
		t.Fatalf("read rebind enrollment: %v", err)
	}
	if serverCount != 1 || unusedCount != 1 || purpose != agentcontrol.PurposeRebind {
		t.Fatalf("rebind state = (servers %d, unused %d, purpose %q)", serverCount, unusedCount, purpose)
	}
}

func TestPendingServerWithUsedEnrollmentCreatesRebindEnrollment(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Pending History")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.5.3", false); err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if _, err := db.Exec(`DELETE FROM agents WHERE server_id = ?`, created.ID); err != nil {
		t.Fatalf("remove current Agent: %v", err)
	}
	if _, err := db.Exec(`UPDATE servers SET status = ? WHERE id = ?`, StatusPending, created.ID); err != nil {
		t.Fatalf("set server pending: %v", err)
	}

	if _, err := service.CreateEnrollment(context.Background(), created.ID); err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	var purpose string
	if err := db.QueryRow(
		`SELECT purpose FROM agent_enrollments WHERE server_id = ? AND used_at IS NULL`, created.ID,
	).Scan(&purpose); err != nil {
		t.Fatalf("read pending enrollment purpose: %v", err)
	}
	if purpose != agentcontrol.PurposeRebind {
		t.Fatalf("pending enrollment purpose = %q, want %q", purpose, agentcontrol.PurposeRebind)
	}
}

func TestArchivedServerCanRebindAgentWithoutChangingServerIdentity(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "JP Entry")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	originalAgent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.5.1", false)
	if err != nil {
		t.Fatalf("RegisterAgent() original error = %v", err)
	}
	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if _, err := service.AuthenticateAgent(context.Background(), originalAgent.Token); !errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
		t.Fatalf("AuthenticateAgent() old token after archive error = %v, want ErrInvalidAgentToken", err)
	}
	if err := service.SetAgentOffline(context.Background(), created.ID); err != nil {
		t.Fatalf("SetAgentOffline() archived server error = %v, want safe no-op", err)
	}
	if err := service.SetAgentOnline(context.Background(), created.ID); !errors.Is(err, agentcontrol.ErrArchived) {
		t.Fatalf("SetAgentOnline() archived server error = %v, want ErrArchived", err)
	}

	rebind, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	if rebind.ID != created.ID || rebind.Name != created.Name || rebind.Status != StatusPending || rebind.ArchivedAt == nil {
		t.Fatalf("CreateEnrollment() server = %+v, want same archived server pending", rebind.Server)
	}
	var enrollmentServerID int64
	var enrollmentHash, purpose string
	if err := db.QueryRow(
		`SELECT server_id, token_hash, purpose FROM agent_enrollments
		 WHERE server_id = ? AND used_at IS NULL`, created.ID,
	).Scan(&enrollmentServerID, &enrollmentHash, &purpose); err != nil {
		t.Fatalf("read rebind enrollment: %v", err)
	}
	if enrollmentServerID != created.ID || enrollmentHash != token.Hash(rebind.EnrollmentToken) {
		t.Fatalf("rebind enrollment = (server %d, hash %q), want server %d hashed token", enrollmentServerID, enrollmentHash, created.ID)
	}
	if purpose != agentcontrol.PurposeRebind {
		t.Fatalf("rebind purpose = %q, want %q", purpose, agentcontrol.PurposeRebind)
	}

	newAgent, err := service.RegisterAgent(context.Background(), rebind.EnrollmentToken, "v0.5.2", true)
	if err != nil {
		t.Fatalf("RegisterAgent() replacement error = %v", err)
	}
	if newAgent.ServerID != created.ID || newAgent.Token == originalAgent.Token {
		t.Fatalf("replacement Agent = %+v, want server %d and a new token", newAgent, created.ID)
	}
	restored, err := service.Get(context.Background(), created.ID)
	if err != nil || restored.ID != created.ID || restored.Name != created.Name ||
		restored.ArchivedAt != nil || restored.Status != StatusOffline {
		t.Fatalf("restored server = (%+v, %v), want original active server offline", restored, err)
	}
	var serverCount, agentCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&serverCount); err != nil {
		t.Fatalf("count servers: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM agents WHERE server_id = ?`, created.ID).Scan(&agentCount); err != nil {
		t.Fatalf("count replacement Agents: %v", err)
	}
	if serverCount != 1 || agentCount != 1 {
		t.Fatalf("rebind counts = (servers %d, agents %d), want one each", serverCount, agentCount)
	}
	if _, err := service.AuthenticateAgent(context.Background(), originalAgent.Token); !errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
		t.Fatalf("AuthenticateAgent() old token after rebind error = %v, want ErrInvalidAgentToken", err)
	}
	if agent, err := service.AuthenticateAgent(context.Background(), newAgent.Token); err != nil || agent.ServerID != created.ID {
		t.Fatalf("AuthenticateAgent() new token = (%+v, %v), want server %d", agent, err, created.ID)
	}
	if _, err := service.RegisterAgent(context.Background(), rebind.EnrollmentToken, "v0.5.2", true); !errors.Is(err, agentcontrol.ErrInvalidEnrollment) {
		t.Fatalf("RegisterAgent() reused rebind token error = %v, want ErrInvalidEnrollment", err)
	}
}

func TestRebindEnrollmentAllowsMissingExistingConfig(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Reinstalled VPS")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.5.1", false); err != nil {
		t.Fatalf("RegisterAgent() original error = %v", err)
	}
	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	rebind, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	registered, err := service.RegisterAgent(context.Background(), rebind.EnrollmentToken, "v0.5.2", false)
	if err != nil {
		t.Fatalf("RegisterAgent() without existing config error = %v", err)
	}
	if registered.ServerID != created.ID {
		t.Fatalf("registered server ID = %d, want %d", registered.ServerID, created.ID)
	}
}
