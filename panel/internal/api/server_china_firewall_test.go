package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

func TestServerChinaInboundBlockCapabilityAndDesiredState(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	servers := serverstore.NewService(db)
	created, err := servers.Create(t.Context(), "China firewall API")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgentWithMetadata(t.Context(), created.EnrollmentToken, agentcontrol.Metadata{
		Implementation: agentcontrol.OfficialImplementation,
		Version:        "v0.29.0",
		APIVersion:     agentcontrol.CurrentAPIVersion,
		Capabilities:   []string{agentcontrol.CapabilityFirewallCNBlock},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	cookie := initializeChinaFirewallAdmin(t, handler)
	path := "/api/servers/" + strconv.FormatInt(created.ID, 10)

	enabled := performRequest(t, handler, http.MethodPatch, path, map[string]any{"block_china_inbound": true}, cookie)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable China inbound block = %d %s", enabled.Code, enabled.Body.String())
	}
	var response struct {
		Server serverResponse `json:"server"`
	}
	if err := json.Unmarshal(enabled.Body.Bytes(), &response); err != nil || !response.Server.BlockChinaInbound {
		t.Fatalf("enabled response = %s, error = %v", enabled.Body.String(), err)
	}
	var version int64
	var status, syncError string
	if err := db.QueryRow(`SELECT servers.desired_state_version, agents.config_sync_status, agents.config_sync_error
		FROM servers JOIN agents ON agents.server_id = servers.id WHERE servers.id = ?`, created.ID,
	).Scan(&version, &status, &syncError); err != nil {
		t.Fatal(err)
	}
	if version != 2 || status != agentcontrol.ConfigSyncPending || syncError != "" {
		t.Fatalf("enabled state = (%d, %q, %q)", version, status, syncError)
	}
	configResponse := performAgentRequest(t, handler, http.MethodGet, "/api/agent/config", nil, registered.Token)
	var state agentDesiredStateResponse
	if configResponse.Code != http.StatusOK || json.Unmarshal(configResponse.Body.Bytes(), &state) != nil ||
		!state.BlockChinaInbound || state.Version != 2 {
		t.Fatalf("desired state = %d %s", configResponse.Code, configResponse.Body.String())
	}

	if _, err := db.Exec(`UPDATE agents SET config_sync_status = 'success' WHERE id = ?`, registered.ID); err != nil {
		t.Fatal(err)
	}
	noOp := performRequest(t, handler, http.MethodPatch, path, map[string]any{"block_china_inbound": true}, cookie)
	if noOp.Code != http.StatusOK {
		t.Fatalf("no-op enable = %d %s", noOp.Code, noOp.Body.String())
	}
	if err := db.QueryRow(`SELECT servers.desired_state_version, agents.config_sync_status
		FROM servers JOIN agents ON agents.server_id = servers.id WHERE servers.id = ?`, created.ID,
	).Scan(&version, &status); err != nil {
		t.Fatal(err)
	}
	if version != 2 || status != agentcontrol.ConfigSyncSuccess {
		t.Fatalf("no-op state = (%d, %q)", version, status)
	}

	if _, err := db.Exec(`UPDATE agents SET capabilities_json = '[]' WHERE id = ?`, registered.ID); err != nil {
		t.Fatal(err)
	}
	disabled := performRequest(t, handler, http.MethodPatch, path, map[string]any{"block_china_inbound": false}, cookie)
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body.String(), `"block_china_inbound":false`) {
		t.Fatalf("unsupported Agent disable = %d %s", disabled.Code, disabled.Body.String())
	}
	if err := db.QueryRow(`SELECT desired_state_version FROM servers WHERE id = ?`, created.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("disabled version = %d, want 3", version)
	}
}

func TestServerChinaInboundBlockRejectsAgentsWithoutDeclaredCapability(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata *agentcontrol.Metadata
	}{
		{name: "unregistered"},
		{name: "Legacy", metadata: &agentcontrol.Metadata{Version: "v0.29.0"}},
		{name: "API v1 missing capability", metadata: &agentcontrol.Metadata{
			Implementation: agentcontrol.OfficialImplementation,
			Version:        "v0.29.0",
			APIVersion:     agentcontrol.CurrentAPIVersion,
			Capabilities:   []string{agentcontrol.CapabilityMetrics},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := database.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			servers := serverstore.NewService(db)
			created, err := servers.Create(t.Context(), test.name)
			if err != nil {
				t.Fatal(err)
			}
			if test.metadata != nil {
				if _, err := agentcontrol.NewService(db, time.Now).RegisterAgentWithMetadata(
					t.Context(), created.EnrollmentToken, *test.metadata, false,
				); err != nil {
					t.Fatal(err)
				}
			}
			handler := NewHandler(db, t.TempDir())
			cookie := initializeChinaFirewallAdmin(t, handler)
			path := "/api/servers/" + strconv.FormatInt(created.ID, 10)
			response := performRequest(t, handler, http.MethodPatch, path, map[string]any{"block_china_inbound": true}, cookie)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "当前 Agent 不支持中国 IP 入站限制") {
				t.Fatalf("unsupported enable = %d %s", response.Code, response.Body.String())
			}
			var enabled bool
			var version int64
			if err := db.QueryRow(`SELECT block_china_inbound, desired_state_version FROM servers WHERE id = ?`, created.ID).Scan(&enabled, &version); err != nil {
				t.Fatal(err)
			}
			if enabled || version != 1 {
				t.Fatalf("rejected state = (%t, %d)", enabled, version)
			}
		})
	}
}

func TestServerResponseIncludesAgentConfigSyncState(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	servers := serverstore.NewService(db)
	created, err := servers.Create(t.Context(), "Config state API")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, t.TempDir())
	cookie := initializeChinaFirewallAdmin(t, handler)
	path := "/api/servers/" + strconv.FormatInt(created.ID, 10)

	readServer := func() serverResponse {
		t.Helper()
		response := performRequest(t, handler, http.MethodGet, path, nil, cookie)
		if response.Code != http.StatusOK {
			t.Fatalf("get Server = %d %s", response.Code, response.Body.String())
		}
		var payload struct {
			Server serverResponse `json:"server"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Server
	}

	unregistered := readServer()
	if unregistered.DesiredStateVersion != 1 || unregistered.AgentAppliedConfigVersion != 0 ||
		unregistered.AgentConfigSyncStatus != "" || unregistered.AgentConfigSyncError != "" ||
		unregistered.AgentConfigSyncedAt != nil {
		t.Fatalf("unregistered config state = %+v", unregistered)
	}

	registered, err := agentcontrol.NewService(db, time.Now).RegisterAgentWithMetadata(t.Context(), created.EnrollmentToken, agentcontrol.Metadata{
		Implementation: agentcontrol.OfficialImplementation,
		Version:        "v0.31.0",
		APIVersion:     agentcontrol.CurrentAPIVersion,
		Capabilities:   []string{agentcontrol.CapabilityFirewallCNBlock},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE servers SET desired_state_version = 4 WHERE id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}

	syncedAt := time.Date(2026, 9, 24, 12, 34, 56, 0, time.UTC)
	for _, test := range []struct {
		name      string
		applied   int64
		status    string
		syncError string
		syncedAt  any
	}{
		{name: "pending", applied: 3, status: agentcontrol.ConfigSyncPending},
		{name: "success", applied: 4, status: agentcontrol.ConfigSyncSuccess, syncedAt: syncedAt.Unix()},
		{name: "failed", applied: 3, status: agentcontrol.ConfigSyncFailed, syncError: "managed China inbound firewall requires nftables", syncedAt: syncedAt.Unix()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE agents SET applied_config_version = ?, config_sync_status = ?,
				config_sync_error = ?, config_synced_at = ? WHERE id = ?`,
				test.applied, test.status, test.syncError, test.syncedAt, registered.ID,
			); err != nil {
				t.Fatal(err)
			}
			value := readServer()
			if value.DesiredStateVersion != 4 || value.AgentAppliedConfigVersion != test.applied ||
				value.AgentConfigSyncStatus != test.status || value.AgentConfigSyncError != test.syncError {
				t.Fatalf("config state = %+v", value)
			}
			if test.syncedAt == nil {
				if value.AgentConfigSyncedAt != nil {
					t.Fatalf("synced at = %v, want nil", value.AgentConfigSyncedAt)
				}
			} else if value.AgentConfigSyncedAt == nil || !value.AgentConfigSyncedAt.Equal(syncedAt) {
				t.Fatalf("synced at = %v, want %v", value.AgentConfigSyncedAt, syncedAt)
			}
		})
	}

	listResponse := performRequest(t, handler, http.MethodGet, "/api/servers", nil, cookie)
	var listPayload struct {
		Servers []serverResponse `json:"servers"`
	}
	if listResponse.Code != http.StatusOK || json.Unmarshal(listResponse.Body.Bytes(), &listPayload) != nil ||
		len(listPayload.Servers) != 1 || listPayload.Servers[0].AgentConfigSyncStatus != agentcontrol.ConfigSyncFailed {
		t.Fatalf("list response = %d %s", listResponse.Code, listResponse.Body.String())
	}
}

func initializeChinaFirewallAdmin(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := performRequest(t, handler, http.MethodPost, "/api/auth/initialize", map[string]string{
		"username": "admin", "password": "strong-password",
	}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("initialize admin = %d %s", response.Code, response.Body.String())
	}
	return response.Result().Cookies()[0]
}
