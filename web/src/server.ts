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

export type ChinaInboundApplyState = {
  key: 'readonly' | 'not_enabled' | 'unsupported' | 'unsupported_enabled' | 'waiting_enable' | 'waiting_disable' | 'applying' | 'disabling' | 'applied_enabled' | 'applied_disabled' | 'failed'
  label: string
  type: 'default' | 'success' | 'warning' | 'error'
  detail: string
}

export function chinaInboundSupported(server: AgentMetadata): boolean {
  return agentDeclaresCapability(server, agentCapabilities.firewallCNBlock)
}

export function chinaInboundUnsupportedReason(server: ServerRecord): string {
  if (server.agent_version_status === 'unregistered') return '服务器尚未注册支持该功能的 Agent。'
  if (server.agent_api_version === 0) return '当前 Agent 不支持中国 IP 入站限制，请先升级 Agent。'
  return '当前 Agent 不支持中国 IP 入站限制。'
}

export function chinaInboundApplyState(server: ServerRecord): ChinaInboundApplyState {
  if (server.archived_at) {
    return { key: 'readonly', label: '只读', type: 'default', detail: '服务器已移除，仅显示最后保存的设置和配置同步记录。' }
  }
  if (server.agent_version_status === 'unregistered') {
    return server.block_china_inbound
      ? { key: 'waiting_enable', label: '等待 Agent', type: 'warning', detail: '已配置开启，但服务器尚未注册 Agent，尚未应用。' }
      : { key: 'not_enabled', label: '未启用', type: 'default', detail: '服务器尚未注册支持该功能的 Agent。' }
  }
  if (!chinaInboundSupported(server)) {
    return server.block_china_inbound
      ? { key: 'unsupported_enabled', label: '无法确认', type: 'warning', detail: '已配置禁止中国 IP 入站，但当前 Agent 不支持该功能，无法确认规则仍然生效。' }
      : { key: 'unsupported', label: '当前 Agent 不支持', type: 'default', detail: chinaInboundUnsupportedReason(server) }
  }
  const currentVersionApplied = server.agent_config_sync_status === 'success'
    && server.agent_applied_config_version >= server.desired_state_version
  if (currentVersionApplied) {
    return server.block_china_inbound
      ? { key: 'applied_enabled', label: '已生效', type: 'success', detail: server.status === 'online' ? '当前配置已由 Agent 成功应用。' : 'Agent 当前离线，以上为最近一次成功应用状态。' }
      : { key: 'applied_disabled', label: '已关闭', type: 'default', detail: server.status === 'online' ? '关闭配置已由 Agent 成功应用。' : 'Agent 当前离线，以上为最近一次成功应用状态。' }
  }
  if (server.agent_config_sync_status === 'failed') {
    return {
      key: 'failed',
      label: '配置应用失败',
      type: 'error',
      detail: server.block_china_inbound
        ? '最近一次服务器配置应用失败，无法确认中国 IP 入站限制已生效。'
        : '最近一次服务器配置应用失败，无法确认中国 IP 入站限制已关闭。',
    }
  }
  if (server.status !== 'online') {
    return server.block_china_inbound
      ? { key: 'waiting_enable', label: '等待 Agent 上线', type: 'warning', detail: '设置已保存，Agent 上线后会自动应用。' }
      : { key: 'waiting_disable', label: '等待 Agent 上线关闭', type: 'warning', detail: '关闭设置已保存，Agent 上线后会自动应用。' }
  }
  return server.block_china_inbound
    ? { key: 'applying', label: '应用中', type: 'warning', detail: '设置已保存，正在等待 Agent 应用当前配置。' }
    : { key: 'disabling', label: '关闭中', type: 'warning', detail: '关闭设置已保存，正在等待 Agent 应用当前配置。' }
}

export function chinaInboundConfigNeedsPolling(server: ServerRecord): boolean {
  const state = chinaInboundApplyState(server)
  return state.key === 'applying' || state.key === 'disabling'
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

export function renewalPeriodLabel(value: ServerRecord['renewal_period_months']): string {
  switch (value) {
    case 1: return '月付'
    case 3: return '季付'
    case 6: return '半年付'
    case 12: return '年付'
    case 24: return '两年付'
    case 36: return '三年付'
    default: return '不设置'
  }
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
