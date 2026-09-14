package server

import (
	"context"
	"database/sql"
	"errors"
	"math"
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
	if appliedVersion != 0 || syncStatus != ConfigSyncPending || syncError != "" || syncedAt.Valid {
		t.Fatalf("new Agent config state = (%d, %q, %q, %v), want defaults",
			appliedVersion, syncStatus, syncError, syncedAt.Valid)
	}

	if _, err := service.RegisterAgent(
		context.Background(), created.EnrollmentToken, "v0.4.0", false,
	); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("second RegisterAgent() error = %v, want ErrInvalidEnrollment", err)
	}
}

func TestDesiredStateAndConfigResultFollowAuthenticatedAgent(t *testing.T) {
	service, db := newTestService(t)
	firstServer, err := service.Create(context.Background(), "Config One")
	if err != nil {
		t.Fatalf("create first Server: %v", err)
	}
	firstAgent, err := service.RegisterAgent(context.Background(), firstServer.EnrollmentToken, "v0.10.0", false)
	if err != nil {
		t.Fatalf("register first Agent: %v", err)
	}
	secondServer, err := service.Create(context.Background(), "Config Two")
	if err != nil {
		t.Fatalf("create second Server: %v", err)
	}
	secondAgent, err := service.RegisterAgent(context.Background(), secondServer.EnrollmentToken, "v0.10.0", false)
	if err != nil {
		t.Fatalf("register second Agent: %v", err)
	}
	if _, err := db.Exec(`UPDATE servers SET desired_state_version = 2 WHERE id = ?`, firstServer.ID); err != nil {
		t.Fatalf("set desired state version: %v", err)
	}

	state, err := service.GetDesiredState(context.Background(), firstAgent.ID, firstAgent.ServerID)
	if err != nil || state.Version != 2 {
		t.Fatalf("GetDesiredState() = (%+v, %v), want version 2", state, err)
	}
	if _, err := service.GetDesiredState(context.Background(), firstAgent.ID, secondAgent.ServerID); !errors.Is(err, ErrInvalidAgentToken) {
		t.Fatalf("cross-Server desired state error = %v, want ErrInvalidAgentToken", err)
	}

	failedAt := time.Date(2026, 9, 12, 1, 2, 3, 0, time.UTC)
	service.now = func() time.Time { return failedAt }
	if err := service.RecordConfigResult(context.Background(), firstAgent.ID, firstAgent.ServerID, ConfigResult{
		Version: 2, Status: ConfigSyncFailed, Message: "temporary apply failure",
	}); err != nil {
		t.Fatalf("record failed config result: %v", err)
	}
	assertAgentConfigState(t, db, firstAgent.ID, 0, ConfigSyncFailed, "temporary apply failure", failedAt)

	succeededAt := failedAt.Add(time.Minute)
	service.now = func() time.Time { return succeededAt }
	if err := service.RecordConfigResult(context.Background(), firstAgent.ID, firstAgent.ServerID, ConfigResult{
		Version: 2, Status: ConfigSyncSuccess, Message: "ignored success message",
	}); err != nil {
		t.Fatalf("record successful config result: %v", err)
	}
	assertAgentConfigState(t, db, firstAgent.ID, 2, ConfigSyncSuccess, "", succeededAt)

	if err := service.RecordConfigResult(context.Background(), firstAgent.ID, firstAgent.ServerID, ConfigResult{
		Version: 1, Status: ConfigSyncFailed, Message: "stale failure",
	}); err != nil {
		t.Fatalf("record stale config result: %v", err)
	}
	assertAgentConfigState(t, db, firstAgent.ID, 2, ConfigSyncSuccess, "", succeededAt)

	for _, result := range []ConfigResult{
		{Version: 0, Status: ConfigSyncSuccess},
		{Version: 1, Status: "unknown"},
		{Version: 1, Status: ConfigSyncFailed, Message: strings.Repeat("x", maxConfigSyncErrorBytes+1)},
	} {
		if err := service.RecordConfigResult(context.Background(), firstAgent.ID, firstAgent.ServerID, result); !errors.Is(err, ErrInvalidConfigResult) {
			t.Fatalf("invalid config result %+v error = %v", result, err)
		}
	}
	if err := service.RecordConfigResult(context.Background(), firstAgent.ID, firstAgent.ServerID, ConfigResult{
		Version: 3, Status: ConfigSyncSuccess,
	}); !errors.Is(err, ErrConfigVersionAhead) {
		t.Fatalf("future config result error = %v, want ErrConfigVersionAhead", err)
	}
}

func TestAgentRebindKeepsDesiredStateAndResetsConfigSyncState(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Rebind Config")
	if err != nil {
		t.Fatalf("create Server: %v", err)
	}
	first, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.10.0", false)
	if err != nil {
		t.Fatalf("register first Agent: %v", err)
	}
	if _, err := db.Exec(`UPDATE servers SET desired_state_version = 4 WHERE id = ?`, created.ID); err != nil {
		t.Fatalf("set desired state version: %v", err)
	}
	if err := service.RecordConfigResult(context.Background(), first.ID, first.ServerID, ConfigResult{
		Version: 4, Status: ConfigSyncSuccess,
	}); err != nil {
		t.Fatalf("record first Agent config result: %v", err)
	}
	enrollment, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("create rebind enrollment: %v", err)
	}
	second, err := service.RegisterAgent(context.Background(), enrollment.EnrollmentToken, "v0.10.1", true)
	if err != nil {
		t.Fatalf("register replacement Agent: %v", err)
	}
	state, err := service.GetDesiredState(context.Background(), second.ID, second.ServerID)
	if err != nil || state.Version != 4 {
		t.Fatalf("replacement desired state = (%+v, %v), want version 4", state, err)
	}
	assertAgentConfigState(t, db, second.ID, 0, ConfigSyncPending, "", time.Time{})
}

func assertAgentConfigState(
	t *testing.T,
	db *sql.DB,
	agentID, appliedVersion int64,
	status, message string,
	syncedAt time.Time,
) {
	t.Helper()
	var actualApplied int64
	var actualStatus, actualMessage string
	var actualSyncedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT applied_config_version, config_sync_status, config_sync_error, config_synced_at
		 FROM agents WHERE id = ?`, agentID,
	).Scan(&actualApplied, &actualStatus, &actualMessage, &actualSyncedAt); err != nil {
		t.Fatalf("read Agent config sync state: %v", err)
	}
	if actualApplied != appliedVersion || actualStatus != status || actualMessage != message {
		t.Fatalf("Agent config state = (%d, %q, %q), want (%d, %q, %q)",
			actualApplied, actualStatus, actualMessage, appliedVersion, status, message)
	}
	if syncedAt.IsZero() {
		if actualSyncedAt.Valid {
			t.Fatalf("Agent config synced_at = %d, want NULL", actualSyncedAt.Int64)
		}
	} else if !actualSyncedAt.Valid || actualSyncedAt.Int64 != syncedAt.Unix() {
		t.Fatalf("Agent config synced_at = %v, want %d", actualSyncedAt, syncedAt.Unix())
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
		Hostname:   " jp-01 ",
		OSName:     "Debian GNU/Linux",
		OSVersion:  "12",
		Kernel:     "6.1.0-amd64",
		Arch:       "amd64",
		IPv4:       []string{"203.0.113.10", "10.0.0.2", "203.0.113.10"},
		IPv6:       []string{"2001:db8::10"},
		PublicIPv4: "198.51.100.20",
	}
	publicIPv4Changed, err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, report)
	if err != nil {
		t.Fatalf("ReportSystemInfo() error = %v", err)
	}
	if !publicIPv4Changed {
		t.Fatal("first public IPv4 report was not marked changed")
	}
	publicIPv4Changed, err = service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, report)
	if err != nil || publicIPv4Changed {
		t.Fatalf("unchanged public IPv4 report = (%v, %v), want false", publicIPv4Changed, err)
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
		strings.Join(value.SystemInfo.IPv6, ",") != "2001:db8::10" ||
		value.SystemInfo.PublicIPv4 != "198.51.100.20" {
		t.Fatalf("stored system information = %+v", value.SystemInfo)
	}

	service.now = func() time.Time { return reportedAt.Add(time.Minute) }
	publicIPv4Changed, err = service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		Hostname: "jp-02", Arch: "arm64", IPv4: []string{}, IPv6: []string{},
	})
	if err != nil {
		t.Fatalf("second ReportSystemInfo() error = %v", err)
	}
	if !publicIPv4Changed {
		t.Fatal("cleared public IPv4 was not marked changed")
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

	if _, err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		IPv4: []string{"not-an-ip"},
	}); !errors.Is(err, ErrInvalidSystemInfo) {
		t.Fatalf("invalid IP error = %v, want ErrInvalidSystemInfo", err)
	}
	if _, err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
		PublicIPv4: "172.26.1.10",
	}); !errors.Is(err, ErrInvalidSystemInfo) {
		t.Fatalf("private public IPv4 error = %v, want ErrInvalidSystemInfo", err)
	}
	if _, err := service.ReportSystemInfo(context.Background(), registered.ID, registered.ServerID, SystemInfoReport{
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
	if _, err := service.ReportSystemInfo(context.Background(), firstAgent.ID, firstAgent.ServerID, SystemInfoReport{
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
	if _, err := service.ReportSystemInfo(context.Background(), firstAgent.ID, firstAgent.ServerID, SystemInfoReport{
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
	if _, err := service.ReportSystemInfo(context.Background(), secondAgent.ID, secondAgent.ServerID, SystemInfoReport{
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

func TestReportMetricsUpsertsAndFollowsServerLifecycle(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Metrics Server")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	firstAgent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.8.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	firstUpdatedAt := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return firstUpdatedAt }
	first := MetricsReport{
		CPUPercent:       32.4,
		MemoryUsedBytes:  128 << 20,
		MemoryTotalBytes: 512 << 20,
		DiskUsedBytes:    5 << 30,
		DiskTotalBytes:   10 << 30,
		UptimeSeconds:    86400,
	}
	if err := service.ReportMetrics(context.Background(), firstAgent.ID, firstAgent.ServerID, first); err != nil {
		t.Fatalf("ReportMetrics() error = %v", err)
	}
	if err := service.SetAgentConnected(context.Background(), firstAgent.ID, firstAgent.ServerID); err != nil {
		t.Fatalf("SetAgentConnected() error = %v", err)
	}
	if err := service.SetAgentOffline(context.Background(), firstAgent.ServerID); err != nil {
		t.Fatalf("SetAgentOffline() error = %v", err)
	}
	value, err := service.Get(context.Background(), created.ID)
	if err != nil || value.Status != StatusOffline || value.Metrics == nil || value.Metrics.CPUPercent != first.CPUPercent ||
		value.Metrics.MemoryUsedBytes != first.MemoryUsedBytes ||
		value.Metrics.MemoryTotalBytes != first.MemoryTotalBytes ||
		value.Metrics.DiskUsedBytes != first.DiskUsedBytes ||
		value.Metrics.DiskTotalBytes != first.DiskTotalBytes ||
		value.Metrics.UptimeSeconds != first.UptimeSeconds ||
		!value.Metrics.UpdatedAt.Equal(firstUpdatedAt) {
		t.Fatalf("stored metrics = (%+v, %v)", value.Metrics, err)
	}

	secondUpdatedAt := firstUpdatedAt.Add(5 * time.Second)
	service.now = func() time.Time { return secondUpdatedAt }
	second := MetricsReport{CPUPercent: 100}
	if err := service.ReportMetrics(context.Background(), firstAgent.ID, firstAgent.ServerID, second); err != nil {
		t.Fatalf("second ReportMetrics() error = %v", err)
	}
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_metrics WHERE server_id = ?`, created.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count server metrics: %v", err)
	}
	updated, err := service.Get(context.Background(), created.ID)
	if err != nil || rowCount != 1 || updated.Metrics == nil || updated.Metrics.CPUPercent != 100 ||
		updated.Metrics.MemoryTotalBytes != 0 || !updated.Metrics.UpdatedAt.Equal(secondUpdatedAt) {
		t.Fatalf("updated metrics = (%+v, rows %d, %v)", updated.Metrics, rowCount, err)
	}

	rebind, err := service.CreateEnrollment(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	preserved, err := service.Get(context.Background(), created.ID)
	if err != nil || preserved.Metrics == nil || preserved.Metrics.CPUPercent != 100 {
		t.Fatalf("metrics after rebind enrollment = (%+v, %v)", preserved.Metrics, err)
	}
	if err := service.ReportMetrics(context.Background(), firstAgent.ID, firstAgent.ServerID, first); !errors.Is(err, ErrInvalidAgentToken) {
		t.Fatalf("old Agent metrics error = %v, want ErrInvalidAgentToken", err)
	}
	secondAgent, err := service.RegisterAgent(context.Background(), rebind.EnrollmentToken, "v0.8.0", true)
	if err != nil {
		t.Fatalf("replacement RegisterAgent() error = %v", err)
	}
	if err := service.ReportMetrics(context.Background(), secondAgent.ID, secondAgent.ServerID, first); err != nil {
		t.Fatalf("replacement ReportMetrics() error = %v", err)
	}
	if err := service.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	archived, err := service.ListArchived(context.Background())
	if err != nil || len(archived) != 1 || archived[0].Metrics == nil || archived[0].Metrics.CPUPercent != first.CPUPercent {
		t.Fatalf("archived metrics = (%+v, %v)", archived, err)
	}
	if err := service.PermanentlyDelete(context.Background(), created.ID); err != nil {
		t.Fatalf("PermanentlyDelete() error = %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM server_metrics WHERE server_id = ?`, created.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count deleted server metrics: %v", err)
	}
	if rowCount != 0 {
		t.Fatalf("server metrics rows after permanent delete = %d, want 0", rowCount)
	}
}

func TestReportMetricsRejectsInvalidValues(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Invalid Metrics")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	agent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.8.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	invalid := []MetricsReport{
		{CPUPercent: -0.1},
		{CPUPercent: 100.1},
		{CPUPercent: math.NaN()},
		{CPUPercent: math.Inf(1)},
		{MemoryUsedBytes: -1},
		{MemoryUsedBytes: 2, MemoryTotalBytes: 1},
		{DiskUsedBytes: -1},
		{DiskUsedBytes: 2, DiskTotalBytes: 1},
		{UptimeSeconds: -1},
		{HasNetworkUsage: true, NICRXBytes: -1},
		{HasNetworkUsage: true, NICTXBytes: -1},
	}
	for index, report := range invalid {
		if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, report); !errors.Is(err, ErrInvalidMetrics) {
			t.Fatalf("invalid metrics %d error = %v, want ErrInvalidMetrics", index, err)
		}
	}
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		CPUPercent: 100,
	}); err != nil {
		t.Fatalf("boundary metrics error = %v", err)
	}
}

func TestReportMetricsAccumulatesTrafficAndHandlesCounterReset(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Traffic Delta")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	agent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.9.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC) }
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 1000, NICTXBytes: 2000,
	}); err != nil {
		t.Fatalf("first ReportMetrics() error = %v", err)
	}
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 1300, NICTXBytes: 2600,
	}); err != nil {
		t.Fatalf("growing ReportMetrics() error = %v", err)
	}
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 100, NICTXBytes: 2700,
	}); err != nil {
		t.Fatalf("RX reset ReportMetrics() error = %v", err)
	}
	adjusted, err := service.UpdateTrafficAdjustment(context.Background(), created.ID, 1000)
	if err != nil || adjusted.Metrics == nil || adjusted.Metrics.TrafficAdjustmentBytes != 300 {
		t.Fatalf("set traffic adjustment before restart = (%+v, %v)", adjusted.Metrics, err)
	}

	restartedService := NewService(db)
	restartedService.now = service.now
	if err := restartedService.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 150, NICTXBytes: 50,
	}); err != nil {
		t.Fatalf("TX reset after Panel restart error = %v", err)
	}
	value, err := restartedService.Get(context.Background(), created.ID)
	if err != nil || value.Metrics == nil {
		t.Fatalf("Get() traffic = (%+v, %v)", value.Metrics, err)
	}
	if value.Metrics.NICRXBytes != 150 || value.Metrics.NICTXBytes != 50 ||
		value.Metrics.CycleRXBytes != 350 || value.Metrics.CycleTXBytes != 700 ||
		value.Metrics.TrafficAdjustmentBytes != 300 || value.TrafficUsedBytes() != 1000 {
		t.Fatalf("traffic after counter resets = %+v, used %d", value.Metrics, value.TrafficUsedBytes())
	}
}

func TestTrafficConfigurationControlsUsageAndUnlimitedLimit(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Traffic Config")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.MonthlyTrafficLimitBytes != nil || created.TrafficCountMode != TrafficSingle ||
		created.TrafficResetDay != 1 || created.TrafficResetTime != "00:00" {
		t.Fatalf("new server traffic defaults = %+v", created.Server)
	}
	limit := int64(500 << 30)
	updated, err := service.UpdateTrafficConfig(context.Background(), created.ID, TrafficConfig{
		MonthlyLimitBytes: &limit,
		CountMode:         TrafficBidirectional,
		ResetDay:          15,
		ResetTime:         "08:30",
	})
	if err != nil || updated.MonthlyTrafficLimitBytes == nil || *updated.MonthlyTrafficLimitBytes != limit ||
		updated.TrafficCountMode != TrafficBidirectional || updated.TrafficResetDay != 15 || updated.TrafficResetTime != "08:30" {
		t.Fatalf("UpdateTrafficConfig() = (%+v, %v)", updated, err)
	}
	agent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.9.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC) }
	for _, report := range []MetricsReport{
		{HasNetworkUsage: true, NICRXBytes: 1000, NICTXBytes: 2000},
		{HasNetworkUsage: true, NICRXBytes: 1100, NICTXBytes: 2200},
	} {
		if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, report); err != nil {
			t.Fatalf("ReportMetrics() error = %v", err)
		}
	}
	value, err := service.Get(context.Background(), created.ID)
	if err != nil || value.TrafficUsedBytes() != 300 {
		t.Fatalf("bidirectional traffic = (%d, %v), want 300", value.TrafficUsedBytes(), err)
	}
	zero := int64(0)
	value, err = service.UpdateTrafficConfig(context.Background(), created.ID, TrafficConfig{
		MonthlyLimitBytes: &zero,
		CountMode:         TrafficSingle,
		ResetDay:          1,
		ResetTime:         "00:00",
	})
	if err != nil || value.MonthlyTrafficLimitBytes != nil || value.TrafficUsedBytes() != 200 {
		t.Fatalf("unlimited single traffic = (limit %v, used %d, error %v)",
			value.MonthlyTrafficLimitBytes, value.TrafficUsedBytes(), err)
	}
}

func TestTrafficAdjustmentCalibratesDisplayedUsageWithoutChangingCounters(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(context.Background(), "Traffic Adjustment")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO server_metrics
		 (server_id, cpu_percent, memory_used_bytes, memory_total_bytes,
		  disk_used_bytes, disk_total_bytes, uptime_seconds, nic_rx_bytes, nic_tx_bytes,
		  cycle_rx_bytes, cycle_tx_bytes, cycle_started_at, updated_at)
		 VALUES (?, 1, 2, 3, 4, 5, 6, 1000, 2000, 20, 100, 10, 11)`,
		created.ID,
	); err != nil {
		t.Fatalf("insert traffic metrics: %v", err)
	}

	value, err := service.Get(context.Background(), created.ID)
	if err != nil || value.MeasuredTrafficUsedBytes() != 100 || value.TrafficUsedBytes() != 100 ||
		value.Metrics == nil || value.Metrics.TrafficAdjustmentBytes != 0 {
		t.Fatalf("initial measured traffic = (%+v, measured %d, used %d, %v)",
			value.Metrics, value.MeasuredTrafficUsedBytes(), value.TrafficUsedBytes(), err)
	}
	value, err = service.UpdateTrafficAdjustment(context.Background(), created.ID, 500)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != 400 || value.TrafficUsedBytes() != 500 {
		t.Fatalf("positive adjustment = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	assertTrafficCounters(t, value.Metrics, 1000, 2000, 20, 100, 10)

	restarted := NewService(db)
	value, err = restarted.Get(context.Background(), created.ID)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != 400 || value.TrafficUsedBytes() != 500 {
		t.Fatalf("persisted adjustment = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	value, err = restarted.UpdateTrafficConfig(context.Background(), created.ID, TrafficConfig{
		CountMode: TrafficBidirectional, ResetDay: 1, ResetTime: "00:00",
	})
	if err != nil || value.MeasuredTrafficUsedBytes() != 120 ||
		value.Metrics.TrafficAdjustmentBytes != 400 || value.TrafficUsedBytes() != 520 {
		t.Fatalf("adjustment after mode change = (measured %d, adjustment %d, used %d, %v)",
			value.MeasuredTrafficUsedBytes(), value.Metrics.TrafficAdjustmentBytes, value.TrafficUsedBytes(), err)
	}
	assertTrafficCounters(t, value.Metrics, 1000, 2000, 20, 100, 10)

	value, err = restarted.UpdateTrafficAdjustment(context.Background(), created.ID, 50)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != -70 || value.TrafficUsedBytes() != 50 {
		t.Fatalf("negative adjustment = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	value, err = restarted.UpdateTrafficAdjustment(context.Background(), created.ID, 0)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != -120 || value.TrafficUsedBytes() != 0 {
		t.Fatalf("zero target = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	value, err = restarted.UpdateTrafficAdjustment(context.Background(), created.ID, 120)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != 0 || value.TrafficUsedBytes() != 120 {
		t.Fatalf("target equal to measured usage = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	if _, err := db.Exec(
		`UPDATE server_metrics SET traffic_adjustment_bytes = -1000 WHERE server_id = ?`, created.ID,
	); err != nil {
		t.Fatalf("set oversized negative adjustment: %v", err)
	}
	value, err = restarted.Get(context.Background(), created.ID)
	if err != nil || value.TrafficUsedBytes() != 0 {
		t.Fatalf("clamped adjusted usage = (%d, %v), want 0", value.TrafficUsedBytes(), err)
	}
	value, err = restarted.ClearTrafficAdjustment(context.Background(), created.ID)
	if err != nil || value.Metrics.TrafficAdjustmentBytes != 0 || value.TrafficUsedBytes() != 120 {
		t.Fatalf("cleared adjustment = (%+v, used %d, %v)", value.Metrics, value.TrafficUsedBytes(), err)
	}
	assertTrafficCounters(t, value.Metrics, 1000, 2000, 20, 100, 10)

	limit := int64(200)
	if _, err := restarted.UpdateTrafficConfig(context.Background(), created.ID, TrafficConfig{
		MonthlyLimitBytes: &limit, CountMode: TrafficBidirectional, ResetDay: 1, ResetTime: "00:00",
	}); err != nil {
		t.Fatalf("set traffic limit: %v", err)
	}
	value, err = restarted.UpdateTrafficAdjustment(context.Background(), created.ID, 500)
	if err != nil || value.TrafficUsedBytes() != 500 {
		t.Fatalf("over-limit adjustment = (%d, %v), want 500", value.TrafficUsedBytes(), err)
	}
	if _, err := restarted.UpdateTrafficAdjustment(context.Background(), created.ID, -1); !errors.Is(err, ErrInvalidTrafficTarget) {
		t.Fatalf("negative target error = %v, want ErrInvalidTrafficTarget", err)
	}
}

func assertTrafficCounters(
	t *testing.T,
	metrics *Metrics,
	nicRX, nicTX, cycleRX, cycleTX, cycleStarted int64,
) {
	t.Helper()
	if metrics == nil || metrics.NICRXBytes != nicRX || metrics.NICTXBytes != nicTX ||
		metrics.CycleRXBytes != cycleRX || metrics.CycleTXBytes != cycleTX ||
		metrics.CycleStartedAt == nil || metrics.CycleStartedAt.Unix() != cycleStarted {
		t.Fatalf("traffic counters changed = %+v", metrics)
	}
}

func TestReportMetricsStartsNewTrafficCycleAtConfiguredBoundary(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Traffic Cycle")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := service.UpdateTrafficConfig(context.Background(), created.ID, TrafficConfig{
		CountMode: TrafficSingle, ResetDay: 15, ResetTime: "08:00",
	}); err != nil {
		t.Fatalf("UpdateTrafficConfig() error = %v", err)
	}
	agent, err := service.RegisterAgent(context.Background(), created.EnrollmentToken, "v0.9.0", false)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 14, 23, 0, 0, 0, time.UTC) }
	for _, report := range []MetricsReport{
		{HasNetworkUsage: true, NICRXBytes: 1000, NICTXBytes: 2000},
		{HasNetworkUsage: true, NICRXBytes: 1100, NICTXBytes: 2200},
	} {
		if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, report); err != nil {
			t.Fatalf("pre-boundary ReportMetrics() error = %v", err)
		}
	}
	adjusted, err := service.UpdateTrafficAdjustment(context.Background(), created.ID, 1000)
	if err != nil || adjusted.Metrics == nil || adjusted.Metrics.TrafficAdjustmentBytes != 800 {
		t.Fatalf("set pre-boundary adjustment = (%+v, %v)", adjusted.Metrics, err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 15, 0, 1, 0, 0, time.UTC) }
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 1300, NICTXBytes: 2500,
	}); err != nil {
		t.Fatalf("new-cycle ReportMetrics() error = %v", err)
	}
	value, err := service.Get(context.Background(), created.ID)
	wantStart := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	if err != nil || value.Metrics == nil || value.Metrics.CycleRXBytes != 0 || value.Metrics.CycleTXBytes != 0 ||
		value.Metrics.TrafficAdjustmentBytes != 0 || value.Metrics.NICRXBytes != 1300 || value.Metrics.NICTXBytes != 2500 ||
		value.Metrics.CycleStartedAt == nil || !value.Metrics.CycleStartedAt.Equal(wantStart) {
		t.Fatalf("new traffic cycle = (%+v, %v), want zero at %v", value.Metrics, err, wantStart)
	}
	if err := service.ReportMetrics(context.Background(), agent.ID, agent.ServerID, MetricsReport{
		HasNetworkUsage: true, NICRXBytes: 1350, NICTXBytes: 2580,
	}); err != nil {
		t.Fatalf("post-boundary ReportMetrics() error = %v", err)
	}
	value, err = service.Get(context.Background(), created.ID)
	if err != nil || value.Metrics.CycleRXBytes != 50 || value.Metrics.CycleTXBytes != 80 {
		t.Fatalf("post-boundary traffic = (%+v, %v)", value.Metrics, err)
	}
}

func TestTrafficCycleStartClampsMissingMonthDays(t *testing.T) {
	for _, test := range []struct {
		name string
		now  time.Time
		day  int
		want time.Time
	}{
		{
			name: "February 29 in non-leap year", day: 29,
			now:  time.Date(2027, 2, 28, 1, 0, 0, 0, time.UTC),
			want: time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "February 30 in leap year", day: 30,
			now:  time.Date(2028, 2, 29, 1, 0, 0, 0, time.UTC),
			want: time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "April 31", day: 31,
			now:  time.Date(2027, 4, 30, 1, 0, 0, 0, time.UTC),
			want: time.Date(2027, 4, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "before March 31 boundary", day: 31,
			now:  time.Date(2027, 3, 30, 23, 0, 0, 0, time.UTC),
			want: time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := trafficCycleStart(test.now, test.day, "08:00")
			if err != nil || !got.Equal(test.want) {
				t.Fatalf("trafficCycleStart() = (%v, %v), want %v", got, err, test.want)
			}
		})
	}
}

func TestUpdateTrafficConfigRejectsInvalidValues(t *testing.T) {
	service, _ := newTestService(t)
	created, err := service.Create(context.Background(), "Invalid Traffic Config")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	negative := int64(-1)
	for index, config := range []TrafficConfig{
		{MonthlyLimitBytes: &negative, CountMode: TrafficSingle, ResetDay: 1, ResetTime: "00:00"},
		{CountMode: "both", ResetDay: 1, ResetTime: "00:00"},
		{CountMode: TrafficSingle, ResetDay: 0, ResetTime: "00:00"},
		{CountMode: TrafficSingle, ResetDay: 1, ResetTime: "24:00"},
	} {
		if _, err := service.UpdateTrafficConfig(context.Background(), created.ID, config); !errors.Is(err, ErrInvalidTrafficConfig) {
			t.Fatalf("invalid traffic config %d error = %v", index, err)
		}
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
