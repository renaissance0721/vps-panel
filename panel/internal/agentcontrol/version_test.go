package agentcontrol

import "testing"

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
