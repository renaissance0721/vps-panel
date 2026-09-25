export type RenewalPeriodMonths = 1 | 3 | 6 | 12 | 24 | 36

export type ServerRecord = {
  id: number
  name: string
  status: 'pending' | 'online' | 'offline'
  visibility: 'public' | 'private'
  outbound_preference: 'auto' | 'prefer_ipv4' | 'prefer_ipv6'
  block_china_inbound: boolean
  desired_state_version: number
  decommissioning_at: string | null
  decommission_status: '' | 'pending' | 'failed'
  decommission_error: string
  access_user_ids: number[]
  archived_at?: string
  expires_at: string | null
  renewal_period_months: RenewalPeriodMonths | null
  auto_renew: boolean
  monthly_traffic_limit_bytes: number | null
  traffic_count_mode: 'single' | 'bidirectional'
  traffic_reset_day: number
  traffic_reset_time: string
  traffic_used_bytes: number
  last_seen_at: string | null
  system_info: ServerSystemInfo | null
  metrics: ServerMetrics | null
  created_at: string
  updated_at: string
  agent_implementation: string
  agent_version: string
  agent_api_version: number
  agent_capabilities: string[]
  agent_can_self_upgrade: boolean
  agent_version_status: 'unregistered' | 'unknown' | 'upgrade_available' | 'up_to_date' | 'agent_newer' | 'not_applicable'
  agent_upgrade_target?: string
  agent_upgrade_status?: 'upgrading' | 'failed'
  agent_upgrade_error?: string
  agent_applied_config_version: number
  agent_config_sync_status: '' | 'pending' | 'success' | 'failed'
  agent_config_sync_error: string
  agent_config_synced_at: string | null
}

export type ServerSystemInfo = {
  hostname: string
  os_name: string
  os_version: string
  kernel: string
  arch: string
  ipv4: string[]
  ipv6: string[]
  public_ipv4: string
  agent_version: string
}

export type ServerMetrics = {
  cpu_percent: number
  memory_used_bytes: number
  memory_total_bytes: number
  disk_used_bytes: number
  disk_total_bytes: number
  uptime_seconds: number
  nic_rx_bytes: number
  nic_tx_bytes: number
  cycle_rx_bytes: number
  cycle_tx_bytes: number
  traffic_adjustment_bytes: number
  cycle_started_at: string | null
  updated_at: string
}

export type CreatedServer = {
  server: ServerRecord
  enrollment_token: string
  enrollment_token_expires_at: string
  agent_installation_command: string
}

export type DiagnosticStatus = 'pass' | 'warning' | 'fail' | 'skipped'

export type DiagnosticCheck = {
  code: string
  status: DiagnosticStatus
  resource_id?: number
  label?: string
  endpoint?: string
  protocol?: string
  latency_ms?: number
  expires_at?: number
  remaining_days?: number
  detail?: string
}

export type DiagnosticReport = {
  server_id: number
  started_at: string
  duration_ms: number
  checks: DiagnosticCheck[]
}
