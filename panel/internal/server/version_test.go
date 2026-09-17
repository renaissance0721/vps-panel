package server

import (
	"errors"
	"testing"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
)

func TestPrepareAgentUpgradeNeverDowngradesOrUpgradesUnknownVersion(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.Create(t.Context(), "Version comparison")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := service.RegisterAgent(t.Context(), created.EnrollmentToken, "v0.19.2", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetAgentConnectedVersion(t.Context(), registered.ID, created.ID, "v0.19.2"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareAgentUpgrade(t.Context(), created.ID, "v0.19.1"); !errors.Is(err, agentcontrol.ErrAgentNewer) {
		t.Fatalf("downgrade error = %v, want ErrAgentNewer", err)
	}
	if current, err := service.PrepareAgentUpgrade(t.Context(), created.ID, "v0.19.2"); err != nil || !current.AlreadyCurrent {
		t.Fatalf("current version = (%+v, %v)", current, err)
	}
	if _, err := service.PrepareAgentUpgrade(t.Context(), created.ID, "dev"); !errors.Is(err, agentcontrol.ErrInvalidUpgrade) {
		t.Fatalf("development target error = %v", err)
	}
	var target, status string
	if err := db.QueryRow(`SELECT upgrade_target_version, upgrade_status FROM agents WHERE server_id = ?`, created.ID).Scan(&target, &status); err != nil || target != "" || status != "" {
		t.Fatalf("rejected upgrade changed Agent: target %q status %q error %v", target, status, err)
	}
	if _, err := service.PrepareAgentUpgrade(t.Context(), created.ID, "v0.19.3"); err != nil {
		t.Fatalf("valid higher target: %v", err)
	}
	if err := db.QueryRow(`SELECT upgrade_target_version, upgrade_status FROM agents WHERE server_id = ?`, created.ID).Scan(&target, &status); err != nil || target != "v0.19.3" || status != agentcontrol.AgentUpgradeUpgrading {
		t.Fatalf("valid upgrade = (%q, %q, %v)", target, status, err)
	}
	if err := service.SetAgentConnectedVersion(t.Context(), registered.ID, created.ID, "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareAgentUpgrade(t.Context(), created.ID, "v0.19.3"); !errors.Is(err, agentcontrol.ErrUnknownAgentVersion) {
		t.Fatalf("unknown current Agent version error = %v", err)
	}
}
