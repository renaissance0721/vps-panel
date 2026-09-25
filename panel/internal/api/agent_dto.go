package api

import (
	proxystore "github.com/renaissance0721/vps-panel/panel/internal/proxy"
	relaystore "github.com/renaissance0721/vps-panel/panel/internal/relay"
)

type agentRegistrationRequest struct {
	EnrollmentToken     string   `json:"enrollment_token"`
	AgentVersion        string   `json:"agent_version"`
	ExistingConfig      bool     `json:"existing_config"`
	AgentImplementation string   `json:"agent_implementation"`
	AgentAPIVersion     int      `json:"agent_api_version"`
	AgentCapabilities   []string `json:"agent_capabilities"`
}

type agentRegistrationResponse struct {
	AgentID    int64  `json:"agent_id"`
	ServerID   int64  `json:"server_id"`
	AgentToken string `json:"agent_token"`
}

type agentDesiredStateResponse struct {
	Version           int64                  `json:"version"`
	Decommission      bool                   `json:"decommission"`
	BlockChinaInbound bool                   `json:"block_china_inbound"`
	Xray              agentDesiredXrayState  `json:"xray"`
	Realm             agentDesiredRealmState `json:"realm"`
}

type agentDesiredXrayState struct {
	Enabled            bool                      `json:"enabled"`
	Purge              bool                      `json:"purge"`
	OutboundPreference string                    `json:"outbound_preference"`
	Proxies            []proxystore.DesiredProxy `json:"proxies"`
}

type agentDesiredRealmState struct {
	Enabled bool                      `json:"enabled"`
	Purge   bool                      `json:"purge"`
	Relays  []relaystore.DesiredRelay `json:"relays"`
}

type agentConfigResultRequest struct {
	Version int64  `json:"version"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type agentUpgradeResultRequest struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type agentSystemInfoMessage struct {
	Type       string   `json:"type"`
	Hostname   string   `json:"hostname"`
	OSName     string   `json:"os_name"`
	OSVersion  string   `json:"os_version"`
	Kernel     string   `json:"kernel"`
	Arch       string   `json:"arch"`
	IPv4       []string `json:"ipv4"`
	IPv6       []string `json:"ipv6"`
	PublicIPv4 string   `json:"public_ipv4"`
}

type agentMetricsMessage struct {
	Type             string  `json:"type"`
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryUsedBytes  int64   `json:"memory_used_bytes"`
	MemoryTotalBytes int64   `json:"memory_total_bytes"`
	DiskUsedBytes    int64   `json:"disk_used_bytes"`
	DiskTotalBytes   int64   `json:"disk_total_bytes"`
	UptimeSeconds    int64   `json:"uptime_seconds"`
	NICRXBytes       *int64  `json:"nic_rx_bytes"`
	NICTXBytes       *int64  `json:"nic_tx_bytes"`
}
