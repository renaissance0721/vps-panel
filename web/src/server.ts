import type {
  ServerRecord,
} from './types/server'

type AgentMetadata = Pick<ServerRecord, 'agent_implementation' | 'agent_api_version' | 'agent_capabilities'>

export const agentCapabilities = {
  proxyVLESSReality: 'proxy.vless.reality',
  proxyVLESSTLSACME: 'proxy.vless.tls.acme',
  proxyVLESSTLSManual: 'proxy.vless.tls.manual',
  proxyShadowsocks: 'proxy.shadowsocks',
  relayRealm: 'relay.realm',
  outboundPreference: 'outbound_preference',
  diagnosticsV1: 'diagnostics_v1',
  firewallCNBlock: 'firewall.cn_block',
} as const

export function agentSupportsCapability(server: AgentMetadata, capability: string): boolean {
  const implementation = server.agent_implementation ?? ''
  const apiVersion = server.agent_api_version ?? 0
  if (implementation === '' && apiVersion === 0) return true
  if (implementation === '' || apiVersion !== 1) return false
  return (server.agent_capabilities ?? []).includes(capability)
}

export function agentDeclaresCapability(server: AgentMetadata, capability: string): boolean {
  const implementation = server.agent_implementation ?? ''
  const apiVersion = server.agent_api_version ?? 0
  return implementation !== ''
    && apiVersion === 1
    && (server.agent_capabilities ?? []).includes(capability)
}

export function canBulkUpgradeAgent(server: ServerRecord): boolean {
  return (
    server.status === 'online'
    && server.agent_can_self_upgrade === true
    && server.agent_version_status === 'upgrade_available'
    && server.agent_upgrade_status !== 'upgrading'
  )
}

export function formatExpirationDate(value: string): string {
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
  const parts = Object.fromEntries(
    formatter.formatToParts(new Date(value)).map((part) => [part.type, part.value]),
  )
  return `${parts.year}-${parts.month}-${parts.day}`
}

export function formatServerExpiration(value: string | null): string {
  return value ? formatExpirationDate(value) : '不限'
}

export function formatPercent(value: number) {
  return `${value.toFixed(1).replace(/\.0$/, '')}%`
}

export function formatBytes(value: number) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let unitIndex = 0
  let scaled = value
  while (scaled >= 1024 && unitIndex < units.length - 1) {
    scaled /= 1024
    unitIndex++
  }
  const digits = unitIndex > 0 && scaled < 10 ? 1 : 0
  return `${scaled.toFixed(digits).replace(/\.0$/, '')} ${units[unitIndex]}`
}

export function formatTrafficBytes(value: number) {
  const units = ['B', 'K', 'M', 'G', 'T']
  let unitIndex = 0
  let scaled = value
  while (scaled >= 1024 && unitIndex < units.length - 1) {
    scaled /= 1024
    unitIndex++
  }
  const digits = unitIndex > 0 && scaled < 100 ? 1 : 0
  return `${scaled.toFixed(digits).replace(/\.0$/, '')}${units[unitIndex]}`
}

export function trafficUsageLabel(value: ServerRecord) {
  const total = value.monthly_traffic_limit_bytes
  return `${formatTrafficBytes(value.traffic_used_bytes)} / ${total ? formatTrafficBytes(total) : '不限'}`
}

export function trafficCountModeLabel(mode: ServerRecord['traffic_count_mode']) {
  return mode === 'bidirectional' ? '双向（RX + TX）' : '单向（TX）'
}

export function measuredTrafficUsed(value: ServerRecord) {
  const tx = value.metrics?.cycle_tx_bytes ?? 0
  return value.traffic_count_mode === 'bidirectional'
    ? (value.metrics?.cycle_rx_bytes ?? 0) + tx
    : tx
}

export function trafficAdjustmentLabel(value: ServerRecord) {
  const adjustment = value.metrics?.traffic_adjustment_bytes ?? 0
  if (adjustment === 0) return '未校准'
  const formatted = formatTrafficBytes(Math.abs(adjustment))
  return adjustment > 0 ? `+${formatted}` : `-${formatted}`
}

export function trafficUsagePercentLabel(value: ServerRecord) {
  if (!value.monthly_traffic_limit_bytes) return '—'
  return formatPercent((value.traffic_used_bytes / value.monthly_traffic_limit_bytes) * 100)
}

export function formatUptime(value: number) {
  const days = Math.floor(value / 86_400)
  const hours = Math.floor((value % 86_400) / 3_600)
  const minutes = Math.floor((value % 3_600) / 60)
  const seconds = Math.floor(value % 60)
  if (days > 0) return `${days} 天${hours > 0 ? ` ${hours} 小时` : ''}`
  if (hours > 0) return `${hours} 小时${minutes > 0 ? ` ${minutes} 分钟` : ''}`
  if (minutes > 0) return `${minutes} 分钟`
  return `${seconds} 秒`
}
export function visibilityLabel(value: ServerRecord['visibility']): string {
  return value === 'private' ? '私有' : '公开'
}

export function agentUpgradeStatus(value: ServerRecord) {
  if (value.agent_version_status === 'not_applicable') return '不适用'
  if (value.agent_version_status === 'agent_newer') return 'Agent 版本高于 Panel'
  if (value.agent_upgrade_status === 'upgrading') return '升级中'
  if (value.agent_upgrade_status === 'failed') return '升级失败'
  switch (value.agent_version_status) {
    case 'unregistered': return '尚未注册'
    case 'upgrade_available': return '可升级'
    case 'up_to_date': return '已是最新'
    default: return '版本未知或开发版本不可升级'
  }
}

export function agentImplementationLabel(implementation: string) {
  if (implementation === 'vps-panel-agent') return 'VPS Panel Agent'
  if (implementation === 'io.github.matthewlu070111.boardray') return 'BoardRay'
  return implementation || '旧版 / 未声明'
}

export function agentAPILabel(apiVersion: number) {
  return apiVersion === 0 ? 'Legacy' : `v${apiVersion}`
}

export function statusType(status: ServerRecord['status']): 'warning' | 'success' | 'default' {
  if (status === 'online') return 'success'
  if (status === 'pending') return 'warning'
  return 'default'
}

export function statusLabel(status: ServerRecord['status']): string {
  if (status === 'pending') return '待注册'
  if (status === 'online') return '在线'
  return '离线'
}
