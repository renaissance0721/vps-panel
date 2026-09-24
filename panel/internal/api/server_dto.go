package api

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/renaissance0721/vps-panel/panel/internal/agentcontrol"
	serverstore "github.com/renaissance0721/vps-panel/panel/internal/server"
)

const expirationDateLayout = "2006-01-02"

var shanghaiLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type createServerRequest struct {
	Name       string  `json:"name"`
	Visibility string  `json:"visibility"`
	UserIDs    []int64 `json:"user_ids"`
}

type updateServerAccessRequest struct {
	Visibility string  `json:"visibility"`
	UserIDs    []int64 `json:"user_ids"`
}

type updateServerRequest struct {
	Name                     *string         `json:"name"`
	OutboundPreference       *string         `json:"outbound_preference"`
	BlockChinaInbound        *bool           `json:"block_china_inbound"`
	ExpiresAt                json.RawMessage `json:"expires_at"`
	MonthlyTrafficLimitBytes json.RawMessage `json:"monthly_traffic_limit_bytes"`
	TrafficCountMode         *string         `json:"traffic_count_mode"`
	TrafficResetDay          *int            `json:"traffic_reset_day"`
	TrafficResetTime         *string         `json:"traffic_reset_time"`
}

type updateTrafficAdjustmentRequest struct {
	TargetUsedBytes *int64 `json:"target_used_bytes"`
}

type serverResponse struct {
	ID                        int64               `json:"id"`
	Name                      string              `json:"name"`
	Status                    string              `json:"status"`
	Visibility                string              `json:"visibility"`
	OutboundPreference        string              `json:"outbound_preference"`
	BlockChinaInbound         bool                `json:"block_china_inbound"`
	DesiredStateVersion       int64               `json:"desired_state_version"`
	AccessUserIDs             []int64             `json:"access_user_ids"`
	ArchivedAt                *time.Time          `json:"archived_at,omitempty"`
	ExpiresAt                 *time.Time          `json:"expires_at"`
	MonthlyTrafficLimitBytes  *int64              `json:"monthly_traffic_limit_bytes"`
	TrafficCountMode          string              `json:"traffic_count_mode"`
	TrafficResetDay           int                 `json:"traffic_reset_day"`
	TrafficResetTime          string              `json:"traffic_reset_time"`
	TrafficUsedBytes          int64               `json:"traffic_used_bytes"`
	LastSeenAt                *time.Time          `json:"last_seen_at"`
	SystemInfo                *systemInfoResponse `json:"system_info"`
	Metrics                   *metricsResponse    `json:"metrics"`
	CreatedAt                 time.Time           `json:"created_at"`
	UpdatedAt                 time.Time           `json:"updated_at"`
	AgentImplementation       string              `json:"agent_implementation"`
	AgentVersion              string              `json:"agent_version"`
	AgentAPIVersion           int                 `json:"agent_api_version"`
	AgentCapabilities         []string            `json:"agent_capabilities"`
	AgentCanSelfUpgrade       bool                `json:"agent_can_self_upgrade"`
	AgentVersionStatus        string              `json:"agent_version_status"`
	AgentUpgradeTarget        string              `json:"agent_upgrade_target,omitempty"`
	AgentUpgradeStatus        string              `json:"agent_upgrade_status,omitempty"`
	AgentUpgradeError         string              `json:"agent_upgrade_error,omitempty"`
	AgentAppliedConfigVersion int64               `json:"agent_applied_config_version"`
	AgentConfigSyncStatus     string              `json:"agent_config_sync_status"`
	AgentConfigSyncError      string              `json:"agent_config_sync_error"`
	AgentConfigSyncedAt       *time.Time          `json:"agent_config_synced_at"`
}

type systemInfoResponse struct {
	Hostname     string   `json:"hostname"`
	OSName       string   `json:"os_name"`
	OSVersion    string   `json:"os_version"`
	Kernel       string   `json:"kernel"`
	Arch         string   `json:"arch"`
	IPv4         []string `json:"ipv4"`
	IPv6         []string `json:"ipv6"`
	PublicIPv4   string   `json:"public_ipv4"`
	AgentVersion string   `json:"agent_version"`
}

type metricsResponse struct {
	CPUPercent             float64    `json:"cpu_percent"`
	MemoryUsedBytes        int64      `json:"memory_used_bytes"`
	MemoryTotalBytes       int64      `json:"memory_total_bytes"`
	DiskUsedBytes          int64      `json:"disk_used_bytes"`
	DiskTotalBytes         int64      `json:"disk_total_bytes"`
	UptimeSeconds          int64      `json:"uptime_seconds"`
	NICRXBytes             int64      `json:"nic_rx_bytes"`
	NICTXBytes             int64      `json:"nic_tx_bytes"`
	CycleRXBytes           int64      `json:"cycle_rx_bytes"`
	CycleTXBytes           int64      `json:"cycle_tx_bytes"`
	TrafficAdjustmentBytes int64      `json:"traffic_adjustment_bytes"`
	CycleStartedAt         *time.Time `json:"cycle_started_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type createdServerResponse struct {
	Server                   serverResponse `json:"server"`
	EnrollmentToken          string         `json:"enrollment_token"`
	EnrollmentTokenExpiresAt time.Time      `json:"enrollment_token_expires_at"`
	AgentInstallationCommand string         `json:"agent_installation_command"`
}

func toServerResponse(value serverstore.Server, panelVersion string) serverResponse {
	response := serverResponse{
		ID:                       value.ID,
		Name:                     value.Name,
		Status:                   value.Status,
		Visibility:               value.Visibility,
		OutboundPreference:       value.OutboundPreference,
		BlockChinaInbound:        value.BlockChinaInbound,
		DesiredStateVersion:      value.DesiredStateVersion,
		AccessUserIDs:            append([]int64{}, value.AccessUserIDs...),
		ArchivedAt:               value.ArchivedAt,
		ExpiresAt:                value.ExpiresAt,
		MonthlyTrafficLimitBytes: value.MonthlyTrafficLimitBytes,
		TrafficCountMode:         value.TrafficCountMode,
		TrafficResetDay:          value.TrafficResetDay,
		TrafficResetTime:         value.TrafficResetTime,
		TrafficUsedBytes:         value.TrafficUsedBytes(),
		LastSeenAt:               value.LastSeenAt,
		CreatedAt:                value.CreatedAt,
		UpdatedAt:                value.UpdatedAt,
		AgentImplementation:      value.AgentImplementation,
		AgentVersion:             value.AgentVersion,
		AgentAPIVersion:          value.AgentAPIVersion,
		AgentCapabilities:        append([]string{}, value.AgentCapabilities...),
		AgentCanSelfUpgrade:      value.AgentVersion != "" && agentcontrol.CanSelfUpgrade(value.AgentImplementation, value.AgentAPIVersion, value.AgentCapabilities),
		AgentVersionStatus: agentcontrol.AgentVersionStatusForMetadata(agentcontrol.Metadata{
			Implementation: value.AgentImplementation,
			Version:        value.AgentVersion,
			APIVersion:     value.AgentAPIVersion,
			Capabilities:   value.AgentCapabilities,
		}, panelVersion),
		AgentUpgradeTarget:        value.AgentUpgradeTarget,
		AgentUpgradeStatus:        value.AgentUpgradeStatus,
		AgentUpgradeError:         value.AgentUpgradeError,
		AgentAppliedConfigVersion: value.AgentAppliedConfigVersion,
		AgentConfigSyncStatus:     value.AgentConfigSyncStatus,
		AgentConfigSyncError:      value.AgentConfigSyncError,
		AgentConfigSyncedAt:       value.AgentConfigSyncedAt,
	}
	if value.SystemInfo != nil {
		response.SystemInfo = &systemInfoResponse{
			Hostname:     value.SystemInfo.Hostname,
			OSName:       value.SystemInfo.OSName,
			OSVersion:    value.SystemInfo.OSVersion,
			Kernel:       value.SystemInfo.Kernel,
			Arch:         value.SystemInfo.Arch,
			IPv4:         value.SystemInfo.IPv4,
			IPv6:         value.SystemInfo.IPv6,
			PublicIPv4:   value.SystemInfo.PublicIPv4,
			AgentVersion: value.SystemInfo.AgentVersion,
		}
	}
	if value.Metrics != nil {
		response.Metrics = &metricsResponse{
			CPUPercent:             value.Metrics.CPUPercent,
			MemoryUsedBytes:        value.Metrics.MemoryUsedBytes,
			MemoryTotalBytes:       value.Metrics.MemoryTotalBytes,
			DiskUsedBytes:          value.Metrics.DiskUsedBytes,
			DiskTotalBytes:         value.Metrics.DiskTotalBytes,
			UptimeSeconds:          value.Metrics.UptimeSeconds,
			NICRXBytes:             value.Metrics.NICRXBytes,
			NICTXBytes:             value.Metrics.NICTXBytes,
			CycleRXBytes:           value.Metrics.CycleRXBytes,
			CycleTXBytes:           value.Metrics.CycleTXBytes,
			TrafficAdjustmentBytes: value.Metrics.TrafficAdjustmentBytes,
			CycleStartedAt:         value.Metrics.CycleStartedAt,
			UpdatedAt:              value.Metrics.UpdatedAt,
		}
	}
	return response
}

func (s *server) toCreatedServerResponse(
	created serverstore.CreatedServer,
	baseURL string,
) createdServerResponse {
	command := fmt.Sprintf(
		"curl -fsSL %s/install-agent.sh | sh -s -- \\\n  --server %s \\\n  --token %s",
		baseURL,
		baseURL,
		created.EnrollmentToken,
	)
	if version := releaseVersion(s.panelVersion); version != "" {
		command += " \\\n  --version " + version
	}
	return createdServerResponse{
		Server:                   toServerResponse(created.Server, s.panelVersion),
		EnrollmentToken:          created.EnrollmentToken,
		EnrollmentTokenExpiresAt: created.EnrollmentExpiresAt,
		AgentInstallationCommand: command,
	}
}
