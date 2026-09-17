package agentcontrol

import (
	"github.com/renaissance0721/vps-panel/panel/internal/version"
)

const (
	AgentVersionUnregistered     = "unregistered"
	AgentVersionUnknown          = "unknown"
	AgentVersionUpgradeAvailable = "upgrade_available"
	AgentVersionUpToDate         = "up_to_date"
	AgentVersionNewer            = "agent_newer"
)

func IsFormalReleaseVersion(value string) bool {
	return version.IsFormal(value)
}

func AgentVersionStatus(agentVersion, panelVersion string) string {
	if agentVersion == "" {
		return AgentVersionUnregistered
	}
	comparison, ok := version.Compare(agentVersion, panelVersion)
	if !ok {
		return AgentVersionUnknown
	}
	switch comparison {
	case -1:
		return AgentVersionUpgradeAvailable
	case 0:
		return AgentVersionUpToDate
	default:
		return AgentVersionNewer
	}
}
