package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	"github.com/renaissance0721/vps-panel/panel/internal/database"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
	"github.com/renaissance0721/vps-panel/panel/internal/token"
)

func TestAgentRegistrationAPI(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	created, err := serverstore.NewService(db).Create(t.Context(), "JP Agent 01")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	handler := NewHandler(db, t.TempDir())
	bypass := performRequest(t, handler, http.MethodPost, "/api/agent/register", map[string]any{
		"enrollment_token": created.EnrollmentToken,
		"agent_version":    "v0.4.0",
		"server_id":        created.ID + 1,
	}, nil)
	if bypass.Code != http.StatusBadRequest {
		t.Fatalf("server ID bypass status = %d, want %d", bypass.Code, http.StatusBadRequest)
	}
	existingConfig := performRequest(t, handler, http.MethodPost, "/api/agent/register", map[string]any{
		"enrollment_token": created.EnrollmentToken,
		"agent_version":    "v0.4.0",
		"existing_config":  true,
	}, nil)
	if existingConfig.Code != http.StatusCreated {
		t.Fatalf("initial enrollment with existing config status = %d, body = %q", existingConfig.Code, existingConfig.Body.String())
	}
	response := existingConfig
	if response.Code != http.StatusCreated {
		t.Fatalf("registration status = %d, body = %q", response.Code, response.Body.String())
	}
	var registered agentRegistrationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &registered); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if registered.AgentID <= 0 || registered.ServerID != created.ID || registered.AgentToken == "" {
		t.Fatalf("registration response = %+v", registered)
	}

	var storedHash, status, implementation, capabilitiesJSON string
	var apiVersion int
	var usedAt sql.NullInt64
	if err := db.QueryRow(
		`SELECT agents.token_hash, enrollments.used_at, servers.status,
		 agents.implementation, agents.api_version, agents.capabilities_json
		 FROM agents
		 JOIN agent_enrollments AS enrollments ON enrollments.server_id = agents.server_id
		 JOIN servers ON servers.id = agents.server_id
		 WHERE agents.id = ?`, registered.AgentID,
	).Scan(&storedHash, &usedAt, &status, &implementation, &apiVersion, &capabilitiesJSON); err != nil {
		t.Fatalf("read registered state: %v", err)
	}
	if storedHash == registered.AgentToken || storedHash != token.Hash(registered.AgentToken) {
		t.Fatal("Agent API stored the long-term token in plaintext")
	}
	if !usedAt.Valid || status != serverstore.StatusOffline {
		t.Fatalf("registered state = (used %v, status %q), want used and offline", usedAt.Valid, status)
	}
	if implementation != "" || apiVersion != 0 || capabilitiesJSON != "[]" {
		t.Fatalf("legacy metadata = (%q, %d, %q)", implementation, apiVersion, capabilitiesJSON)
	}

	reused := performRequest(t, handler, http.MethodPost, "/api/agent/register", map[string]string{
		"enrollment_token": created.EnrollmentToken,
		"agent_version":    "v0.4.0",
	}, nil)
	if reused.Code != http.StatusUnauthorized {
		t.Fatalf("reused registration status = %d, want %d", reused.Code, http.StatusUnauthorized)
	}
}

func TestAgentRegistrationAPIAcceptsOfficialAndThirdPartyMetadata(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := serverstore.NewService(db)
	handler := NewHandler(db, t.TempDir())

	tests := []struct {
		name           string
		implementation string
		capabilities   []string
		want           []string
	}{
		{"official", agentcontrol.OfficialImplementation, []string{"self_upgrade", "metrics", "metrics"}, []string{"metrics", "self_upgrade"}},
		{"third-party", "io.github.matthewlu070111.boardray", []string{"metrics", "diagnostics_v1"}, []string{"diagnostics_v1", "metrics"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			created, err := service.Create(t.Context(), test.name)
			if err != nil {
				t.Fatal(err)
			}
			response := performRequest(t, handler, http.MethodPost, "/api/agent/register", map[string]any{
				"enrollment_token":     created.EnrollmentToken,
				"agent_version":        "v0.4.2",
				"agent_implementation": test.implementation,
				"agent_api_version":    1,
				"agent_capabilities":   test.capabilities,
			}, nil)
			if response.Code != http.StatusCreated {
				t.Fatalf("registration = %d, %s", response.Code, response.Body.String())
			}
			var implementation, capabilitiesJSON string
			var apiVersion int
			if err := db.QueryRow(
				`SELECT implementation, api_version, capabilities_json FROM agents WHERE server_id = ?`, created.ID,
			).Scan(&implementation, &apiVersion, &capabilitiesJSON); err != nil {
				t.Fatal(err)
			}
			var capabilities []string
			if err := json.Unmarshal([]byte(capabilitiesJSON), &capabilities); err != nil {
				t.Fatal(err)
			}
			if implementation != test.implementation || apiVersion != 1 || !reflect.DeepEqual(capabilities, test.want) {
				t.Fatalf("stored metadata = (%q, %d, %v)", implementation, apiVersion, capabilities)
			}
		})
	}
}
