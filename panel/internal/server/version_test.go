package server

import (
	"errors"
	"testing"
)

func TestCompareReleaseVersions(t *testing.T) {
	for _, test := range []struct {
		agent, panel, status string
	}{
		{"v0.16.0", "v0.19.2", AgentVersionUpgradeAvailable},
		{"v0.19.1", "v0.19.2", AgentVersionUpgradeAvailable},
		{"v0.19.2", "v0.19.2", AgentVersionUpToDate},
		{"v0.19.2", "v0.19.1", AgentVersionNewer},
		{"v0.19.10", "v0.19.9", AgentVersionNewer},
		{"v1.0.0", "v0.99.99", AgentVersionNewer},
		{"v0.9.0", "v0.10.0", AgentVersionUpgradeAvailable},
		{"dev", "v0.19.2", AgentVersionUnknown},
		{"unknown", "v0.19.2", AgentVersionUnknown},
		{"v0.invalid.0", "v0.19.2", AgentVersionUnknown},
		{"v0.19.2", "dev", AgentVersionUnknown},
		{"v0.19.2", "", AgentVersionUnknown},
		{"", "v0.19.2", AgentVersionUnregistered},
	} {
		t.Run(test.agent+"_"+test.panel, func(t *testing.T) {
			if status := AgentVersionStatus(test.agent, test.panel); status != test.status {
				t.Fatalf("AgentVersionStatus(%q, %q) = %q, want %q", test.agent, test.panel, status, test.status)
			}
		})
	}
	for _, invalid := range []string{"dev", "unknown", "", "v0.1", "v0.1.2-beta", "v01.2.3", "v18446744073709551616.0.0"} {
		if IsFormalReleaseVersion(invalid) {
			t.Fatalf("invalid release %q was accepted", invalid)
		}
	}
}

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
	if _, err := service.PrepareAgentUpgrade(t.Context(), created.ID, "v0.19.1"); !errors.Is(err, ErrAgentNewer) {
		t.Fatalf("downgrade error = %v, want ErrAgentNewer", err)
	}
	if current, err := service.PrepareAgentUpgrade(t.Context(), created.ID, "v0.19.2"); err != nil || !current.AlreadyCurrent {
		t.Fatalf("current version = (%+v, %v)", current, err)
	}
	if _, err := service.PrepareAgentUpgrade(t.Context(), created.ID, "dev"); !errors.Is(err, ErrInvalidUpgrade) {
		t.Fatalf("development target error = %v", err)
	}
	var target, status string
	if err := db.QueryRow(`SELECT upgrade_target_version, upgrade_status FROM agents WHERE server_id = ?`, created.ID).Scan(&target, &status); err != nil || target != "" || status != "" {
		t.Fatalf("rejected upgrade changed Agent: target %q status %q error %v", target, status, err)
	}
	if _, err := service.PrepareAgentUpgrade(t.Context(), created.ID, "v0.19.3"); err != nil {
		t.Fatalf("valid higher target: %v", err)
	}
	if err := db.QueryRow(`SELECT upgrade_target_version, upgrade_status FROM agents WHERE server_id = ?`, created.ID).Scan(&target, &status); err != nil || target != "v0.19.3" || status != AgentUpgradeUpgrading {
		t.Fatalf("valid upgrade = (%q, %q, %v)", target, status, err)
	}
	if err := service.SetAgentConnectedVersion(t.Context(), registered.ID, created.ID, "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareAgentUpgrade(t.Context(), created.ID, "v0.19.3"); !errors.Is(err, ErrUnknownAgentVersion) {
		t.Fatalf("unknown current Agent version error = %v", err)
	}
}
