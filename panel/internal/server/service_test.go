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
	if created.ExpiresAt != nil {
		t.Fatalf("Create() ExpiresAt = %v, want nil", created.ExpiresAt)
	}
	if created.EnrollmentToken == "" {
		t.Fatal("Create() enrollment token is empty")
	}
	if created.EnrollmentExpiresAt.Sub(created.CreatedAt) != EnrollmentLifetime {
		t.Fatalf("enrollment lifetime = %v, want %v", created.EnrollmentExpiresAt.Sub(created.CreatedAt), EnrollmentLifetime)
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
	if purpose != PurposeInitial {
		t.Fatalf("enrollment purpose = %q, want %q", purpose, PurposeInitial)
	}
}

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
	if serverCount != 1 || unusedCount != 1 || purpose != PurposeRebind {
		t.Fatalf("regenerated state = (servers %d, unused %d, purpose %q)", serverCount, unusedCount, purpose)
	}
	if _, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.5.2", false); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("old enrollment error = %v, want ErrInvalidEnrollment", err)
	}
	if _, err := service.RegisterAgent(context.Background(), regenerated.EnrollmentToken, "v0.5.2", true); err != nil {
		t.Fatalf("register existing Agent config with regenerated enrollment: %v", err)
	}
	if _, err := service.RegisterAgent(context.Background(), regenerated.EnrollmentToken, "v0.5.2", false); !errors.Is(err, ErrInvalidEnrollment) {
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
	if purpose != PurposeRebind {
		t.Fatalf("offline server enrollment purpose = %q, want %q", purpose, PurposeRebind)
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
	if _, err := service.AuthenticateAgent(context.Background(), registered.Token); !errors.Is(err, ErrInvalidAgentToken) {
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
	if serverCount != 1 || unusedCount != 1 || purpose != PurposeRebind {
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
	if purpose != PurposeRebind {
		t.Fatalf("pending enrollment purpose = %q, want %q", purpose, PurposeRebind)
	}
}

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
	if _, err := service.AuthenticateAgent(context.Background(), originalAgent.Token); !errors.Is(err, ErrInvalidAgentToken) {
		t.Fatalf("AuthenticateAgent() old token after archive error = %v, want ErrInvalidAgentToken", err)
	}
	if err := service.SetAgentOffline(context.Background(), created.ID); err != nil {
		t.Fatalf("SetAgentOffline() archived server error = %v, want safe no-op", err)
	}
	if err := service.SetAgentOnline(context.Background(), created.ID); !errors.Is(err, ErrArchived) {
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
	if purpose != PurposeRebind {
		t.Fatalf("rebind purpose = %q, want %q", purpose, PurposeRebind)
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
	if _, err := service.AuthenticateAgent(context.Background(), originalAgent.Token); !errors.Is(err, ErrInvalidAgentToken) {
		t.Fatalf("AuthenticateAgent() old token after rebind error = %v, want ErrInvalidAgentToken", err)
	}
	if agent, err := service.AuthenticateAgent(context.Background(), newAgent.Token); err != nil || agent.ServerID != created.ID {
		t.Fatalf("AuthenticateAgent() new token = (%+v, %v), want server %d", agent, err, created.ID)
	}
	if _, err := service.RegisterAgent(context.Background(), rebind.EnrollmentToken, "v0.5.2", true); !errors.Is(err, ErrInvalidEnrollment) {
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

	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.4.0", false)
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
		context.Background(), created.EnrollmentToken, "v0.4.0", false,
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
		context.Background(), "invalid-token", "v0.4.0", false,
	); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("invalid token error = %v, want ErrInvalidEnrollment", err)
	}
	if _, err := service.RegisterAgent(
		context.Background(), created.EnrollmentToken, " ", false,
	); !errors.Is(err, ErrInvalidAgentVersion) {
		t.Fatalf("invalid version error = %v, want ErrInvalidAgentVersion", err)
	}
	if _, err := service.RegisterAgent(
		context.Background(), created.EnrollmentToken, "v0.4.0", true,
	); !errors.Is(err, ErrInitialConfigExists) {
		t.Fatalf("initial enrollment with existing config error = %v, want ErrInitialConfigExists", err)
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

func TestExpiredEnrollmentCannotRegisterAgent(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Expired 01")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	service.now = func() time.Time { return created.EnrollmentExpiresAt.Add(time.Second) }

	if _, err := service.RegisterAgent(
		context.Background(), created.EnrollmentToken, "v0.4.0", false,
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

func TestAuthenticateAgentAndUpdateConnectionStatus(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "WebSocket Agent")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.5.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}

	if _, err := service.AuthenticateAgent(context.Background(), "invalid-token"); !errors.Is(err, ErrInvalidAgentToken) {
		t.Fatalf("AuthenticateAgent() invalid token error = %v, want ErrInvalidAgentToken", err)
	}
	agent, err := service.AuthenticateAgent(context.Background(), registered.Token)
	if err != nil {
		t.Fatalf("AuthenticateAgent() error = %v", err)
	}
	if agent.ID != registered.ID || agent.ServerID != registered.ServerID {
		t.Fatalf("AuthenticateAgent() = %+v, want agent %d for server %d", agent, registered.ID, registered.ServerID)
	}

	if err := service.SetAgentOnline(context.Background(), agent.ServerID); err != nil {
		t.Fatalf("SetAgentOnline() error = %v", err)
	}
	connected, err := service.Get(context.Background(), agent.ServerID)
	if err != nil || connected.Status != StatusOnline {
		t.Fatalf("connected server = (%+v, %v), want online", connected, err)
	}

	if err := service.SetAgentOffline(context.Background(), agent.ServerID); err != nil {
		t.Fatalf("SetAgentOffline() error = %v", err)
	}
	disconnected, err := service.Get(context.Background(), agent.ServerID)
	if err != nil || disconnected.Status != StatusOffline {
		t.Fatalf("disconnected server = (%+v, %v), want offline", disconnected, err)
	}
}

func TestAgentConnectionAndHeartbeatUpdateLastSeen(t *testing.T) {
	service, _ := newTestService(t)
	connectedAt := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return connectedAt }
	created, err := service.Create(context.Background(), "Heartbeat Agent")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.6.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if err := service.SetAgentConnected(context.Background(), registered.ID, registered.ServerID); err != nil {
		t.Fatalf("SetAgentConnected() error = %v", err)
	}
	connected, err := service.Get(context.Background(), registered.ServerID)
	if err != nil || connected.Status != StatusOnline || connected.LastSeenAt == nil || !connected.LastSeenAt.Equal(connectedAt) {
		t.Fatalf("connected server = (%+v, %v), want online at %v", connected, err, connectedAt)
	}

	heartbeatAt := connectedAt.Add(10 * time.Second)
	service.now = func() time.Time { return heartbeatAt }
	if err := service.TouchAgent(context.Background(), registered.ID, registered.ServerID); err != nil {
		t.Fatalf("TouchAgent() error = %v", err)
	}
	if err := service.SetAgentOffline(context.Background(), registered.ServerID); err != nil {
		t.Fatalf("SetAgentOffline() error = %v", err)
	}
	disconnected, err := service.Get(context.Background(), registered.ServerID)
	if err != nil || disconnected.Status != StatusOffline || disconnected.LastSeenAt == nil || !disconnected.LastSeenAt.Equal(heartbeatAt) {
		t.Fatalf("disconnected server = (%+v, %v), want offline with preserved last seen %v", disconnected, err, heartbeatAt)
	}
	if err := service.TouchAgent(context.Background(), registered.ID+1, registered.ServerID); !errors.Is(err, ErrInvalidAgentToken) {
		t.Fatalf("TouchAgent() invalid identity error = %v, want ErrInvalidAgentToken", err)
	}
}

func TestResetOnlineServers(t *testing.T) {
	service, _ := newTestService(t)
	online, err := service.Create(context.Background(), "Online Agent")
	if err != nil {
		t.Fatalf("Create() online server error = %v", err)
	}
	pending, err := service.Create(context.Background(), "Pending Agent")
	if err != nil {
		t.Fatalf("Create() pending server error = %v", err)
	}
	if err := service.SetAgentOnline(context.Background(), online.ID); err != nil {
		t.Fatalf("SetAgentOnline() error = %v", err)
	}

	if err := service.ResetOnline(context.Background()); err != nil {
		t.Fatalf("ResetOnline() error = %v", err)
	}
	reset, err := service.Get(context.Background(), online.ID)
	if err != nil || reset.Status != StatusOffline {
		t.Fatalf("reset server = (%+v, %v), want offline", reset, err)
	}
	unchanged, err := service.Get(context.Background(), pending.ID)
	if err != nil || unchanged.Status != StatusPending {
		t.Fatalf("pending server = (%+v, %v), want pending", unchanged, err)
	}
}

func TestDisconnectDoesNotOverwritePendingServer(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Pending Disconnect")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := service.SetAgentOffline(context.Background(), created.ID); err != nil {
		t.Fatalf("SetAgentOffline() pending error = %v", err)
	}
	value, err := service.Get(context.Background(), created.ID)
	if err != nil || value.Status != StatusPending {
		t.Fatalf("pending server after disconnect = (%+v, %v), want pending", value, err)
	}
}

func TestReportSystemInfoUpsertsForCurrentAgent(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "System Information")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	registered, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.7.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	reportedAt := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return reportedAt }
	report := SystemInfoReport{
		Hostname:  " jp-01 ",
		OSName:    "Debian GNU/Linux",
		OSVersion: "12",
		Kernel:    "6.1.0-amd64",
		Arch:      "amd64",
		IPv4:      []string{"203.0.113.10", "10.0.0.2", "203.0.113.10"},
		IPv6:      []string{"2001:db8::10"},
	}
	if err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, report); err != nil {
		t.Fatalf("ReportSystemInfo() error = %v", err)
	}
	value, err := service.Get(context.Background(), created.ID)
	if err != nil || value.SystemInfo == nil {
		t.Fatalf("Get() system information = (%+v, %v)", value.SystemInfo, err)
	}
	if value.SystemInfo.Hostname != "jp-01" || value.SystemInfo.OSName != report.OSName ||
		value.SystemInfo.OSVersion != report.OSVersion || value.SystemInfo.Kernel != report.Kernel ||
		value.SystemInfo.Arch != report.Arch || value.SystemInfo.AgentVersion != "v0.7.0" ||
		!value.SystemInfo.ReportedAt.Equal(reportedAt) ||
		strings.Join(value.SystemInfo.IPv4, ",") != "10.0.0.2,203.0.113.10" ||
		strings.Join(value.SystemInfo.IPv6, ",") != "2001:db8::10" {
		t.Fatalf("stored system information = %+v", value.SystemInfo)
	}

	service.now = func() time.Time { return reportedAt.Add(time.Minute) }
	if err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		Hostname: "jp-02", Arch: "arm64", IPv4: []string{}, IPv6: []string{},
	}); err != nil {
		t.Fatalf("second ReportSystemInfo() error = %v", err)
	}
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_system_info WHERE server_id = ?`, created.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count system information rows: %v", err)
	}
	updated, err := service.Get(context.Background(), created.ID)
	if err != nil || rowCount != 1 || updated.SystemInfo == nil || updated.SystemInfo.Hostname != "jp-02" ||
		updated.SystemInfo.Arch != "arm64" {
		t.Fatalf("updated system information = (%+v, rows %d, %v)", updated.SystemInfo, rowCount, err)
	}

	if err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		IPv4: []string{"not-an-ip"},
	}); !errors.Is(err, ErrInvalidSystemInfo) {
		t.Fatalf("invalid IP error = %v, want ErrInvalidSystemInfo", err)
	}
	if err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		Hostname: strings.Repeat("a", 256),
	}); !errors.Is(err, ErrInvalidSystemInfo) {
		t.Fatalf("oversized hostname error = %v, want ErrInvalidSystemInfo", err)
	}
}

func TestSystemInfoSurvivesAgentReplacementAndArchiveThenCascadesOnDelete(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Lifecycle Information")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	firstAgent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.6.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if err := service.ReportSystemInfo(context.Background(), firstAgent.ID, firstAgent.ServerID, SystemInfoReport{
		Hostname: "old-host", Arch: "amd64", IPv4: []string{}, IPv6: []string{},
	}); err != nil {
		t.Fatalf("initial ReportSystemInfo() error = %v", err)
	}

	rebind, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	preserved, err := service.Get(context.Background(), created.ID)
	if err != nil || preserved.SystemInfo == nil || preserved.SystemInfo.Hostname != "old-host" {
		t.Fatalf("system information after rebind = (%+v, %v)", preserved.SystemInfo, err)
	}
	if err := service.ReportSystemInfo(context.Background(), firstAgent.ID, firstAgent.ServerID, SystemInfoReport{
		Hostname: "stale-host", IPv4: []string{}, IPv6: []string{},
	}); !errors.Is(err, ErrInvalidAgentToken) {
		t.Fatalf("old Agent report error = %v, want ErrInvalidAgentToken", err)
	}
	secondAgent, err := service.RegisterAgent(context.Background(), rebind.EnrollmentToken, "v0.7.0", true)
	if err != nil {
		t.Fatalf("replacement RegisterAgent() error = %v", err)
	}
	stillPreserved, err := service.Get(context.Background(), created.ID)
	if err != nil || stillPreserved.SystemInfo == nil || stillPreserved.SystemInfo.Hostname != "old-host" {
		t.Fatalf("system information after Agent registration = (%+v, %v)", stillPreserved.SystemInfo, err)
	}
	if err := service.ReportSystemInfo(context.Background(), secondAgent.ID, secondAgent.ServerID, SystemInfoReport{
		Hostname: "new-host", Arch: "arm64", IPv4: []string{}, IPv6: []string{},
	}); err != nil {
		t.Fatalf("replacement ReportSystemInfo() error = %v", err)
	}

	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	archived, err := service.ListArchived(context.Background())
	if err != nil || len(archived) != 1 || archived[0].SystemInfo == nil || archived[0].SystemInfo.Hostname != "new-host" {
		t.Fatalf("archived system information = (%+v, %v)", archived, err)
	}
	if err := service.PermanentlyDelete(context.Background(), created.ID); err != nil {
		t.Fatalf("PermanentlyDelete() error = %v", err)
	}
	var infoCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_system_info WHERE server_id = ?`, created.ID).Scan(&infoCount); err != nil {
		t.Fatalf("count deleted system information: %v", err)
	}
	if infoCount != 0 {
		t.Fatalf("system information rows after permanent delete = %d, want 0", infoCount)
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

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(db), db
}
