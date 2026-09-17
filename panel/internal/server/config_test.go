package server

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

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
	if _, err := service.GetDesiredState(context.Background(), firstAgent.ID, secondAgent.ServerID); !errors.Is(err, agentcontrol.ErrInvalidAgentToken) {
		t.Fatalf("cross-Server desired state error = %v, want ErrInvalidAgentToken", err)
	}

	failedAt := time.Date(2026, 9, 12, 1, 2, 3, 0, time.UTC)
	service.now = func() time.Time { return failedAt }
	if err := service.RecordConfigResult(context.Background(), firstAgent.ID, firstAgent.ServerID, agentcontrol.ConfigResult{
		Version: 2, Status: agentcontrol.ConfigSyncFailed, Message: "temporary apply failure",
	}); err != nil {
		t.Fatalf("record failed config result: %v", err)
	}
	assertAgentConfigState(t, db, firstAgent.ID, 0, agentcontrol.ConfigSyncFailed, "temporary apply failure", failedAt)

	succeededAt := failedAt.Add(time.Minute)
	service.now = func() time.Time { return succeededAt }
	if err := service.RecordConfigResult(context.Background(), firstAgent.ID, firstAgent.ServerID, agentcontrol.ConfigResult{
		Version: 2, Status: agentcontrol.ConfigSyncSuccess, Message: "ignored success message",
	}); err != nil {
		t.Fatalf("record successful config result: %v", err)
	}
	assertAgentConfigState(t, db, firstAgent.ID, 2, agentcontrol.ConfigSyncSuccess, "", succeededAt)

	if err := service.RecordConfigResult(context.Background(), firstAgent.ID, firstAgent.ServerID, agentcontrol.ConfigResult{
		Version: 1, Status: agentcontrol.ConfigSyncFailed, Message: "stale failure",
	}); err != nil {
		t.Fatalf("record stale config result: %v", err)
	}
	assertAgentConfigState(t, db, firstAgent.ID, 2, agentcontrol.ConfigSyncSuccess, "", succeededAt)

	for _, result := range []agentcontrol.ConfigResult{
		{Version: 0, Status: agentcontrol.ConfigSyncSuccess},
		{Version: 1, Status: "unknown"},
		{Version: 1, Status: agentcontrol.ConfigSyncFailed, Message: strings.Repeat("x", 4097)},
	} {
		if err := service.RecordConfigResult(context.Background(), firstAgent.ID, firstAgent.ServerID, result); !errors.Is(err, agentcontrol.ErrInvalidConfigResult) {
			t.Fatalf("invalid config result %+v error = %v", result, err)
		}
	}
	if err := service.RecordConfigResult(context.Background(), firstAgent.ID, firstAgent.ServerID, agentcontrol.ConfigResult{
		Version: 3, Status: agentcontrol.ConfigSyncSuccess,
	}); !errors.Is(err, agentcontrol.ErrConfigVersionAhead) {
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
	if err := service.RecordConfigResult(context.Background(), first.ID, first.ServerID, agentcontrol.ConfigResult{
		Version: 4, Status: agentcontrol.ConfigSyncSuccess,
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
	assertAgentConfigState(t, db, second.ID, 0, agentcontrol.ConfigSyncPending, "", time.Time{})
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
