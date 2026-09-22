export type ServerRecord = {
  id: number
  name: string
  status: 'pending' | 'online' | 'offline'
  visibility: 'public' | 'private'
  outbound_preference: 'auto' | 'prefer_ipv4' | 'prefer_ipv6'
  access_user_ids: number[]
  archived_at?: string
  expires_at: string | null
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
  agent_version: string
  agent_version_status: 'unregistered' | 'unknown' | 'upgrade_available' | 'up_to_date' | 'agent_newer'
  agent_upgrade_target?: string
  agent_upgrade_status?: 'upgrading' | 'failed'
  agent_upgrade_error?: string
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
