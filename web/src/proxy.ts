export type ProxyProtocol = 'vless' | 'shadowsocks'
export type ShadowsocksMethod = '2022-blake3-aes-128-gcm' | '2022-blake3-aes-256-gcm'
export type ClientTrafficLimitUnit = 'G' | 'T'
export type ClientTrafficResetMode = 'never' | 'daily' | 'weekly' | 'monthly'

export const CLIENT_GIBIBYTE = 1024 ** 3
export const CLIENT_TEBIBYTE = 1024 ** 4

export type ClientMetrics = {
  cycle_uplink_bytes: number
  cycle_downlink_bytes: number
  used_bytes: number
  cycle_started_at: string | null
  last_activity_at: string | null
  updated_at: string | null
}

export const shadowsocksMethods: ShadowsocksMethod[] = [
  '2022-blake3-aes-128-gcm',
  '2022-blake3-aes-256-gcm',
]

export function proxyListProtocolFields(protocol: ProxyProtocol, security?: 'tls' | 'reality') {
  if (protocol === 'shadowsocks') {
    return { protocol: 'Shadowsocks', transport: '--', security: '--', flow: '--' }
  }
  return {
    protocol: 'VLESS',
    transport: 'TCP',
    security: security === 'tls' ? 'TLS' : 'REALITY',
    flow: 'XTLS Vision',
  }
}

export function showsVLESSClientFields(protocol: ProxyProtocol) {
  return protocol === 'vless'
}

export function clientTrafficUsedBytes(metrics?: ClientMetrics | null) {
  return metrics?.used_bytes ?? 0
}

export function formatClientTrafficBytes(value: number) {
  const units = ['B', 'K', 'M', 'G', 'T']
  let unitIndex = 0
  let scaled = value
  while (scaled >= 1024 && unitIndex < units.length - 1) {
    scaled /= 1024
    unitIndex++
  }
  const digits = unitIndex > 0 && scaled < 100 ? 1 : 0
  return `${scaled.toFixed(digits).replace(/\.0$/, '')} ${units[unitIndex]}`
}

export function parseClientTrafficLimit(
  value: string | number,
  unit: ClientTrafficLimitUnit,
): number | null | undefined {
  const normalized = String(value).trim()
  if (normalized === '' || normalized === '0') return null
  if (!/^\d+(?:\.\d+)?$/.test(normalized)) return undefined
  const amount = Number(normalized)
  const bytes = Math.round(amount * (unit === 'T' ? CLIENT_TEBIBYTE : CLIENT_GIBIBYTE))
  if (!Number.isSafeInteger(bytes) || bytes <= 0) return undefined
  return bytes
}

export function formatClientTrafficLimitInput(bytes: number | null) {
  if (!bytes) return { value: '', unit: 'G' as ClientTrafficLimitUnit }
  if (bytes >= CLIENT_TEBIBYTE && bytes % CLIENT_TEBIBYTE === 0) {
    return { value: bytes / CLIENT_TEBIBYTE, unit: 'T' as ClientTrafficLimitUnit }
  }
  return { value: Number((bytes / CLIENT_GIBIBYTE).toFixed(3)), unit: 'G' as ClientTrafficLimitUnit }
}

export function clientTrafficUsageLabel(metrics: ClientMetrics | null, limit: number | null) {
  return `${formatClientTrafficBytes(clientTrafficUsedBytes(metrics))} / ${limit ? formatClientTrafficBytes(limit) : '不限'}`
}

export function clientTrafficCycleLabel(
  mode: ClientTrafficResetMode,
  weekday: number,
  day: number,
  time: string,
) {
  if (mode === 'daily') return `每日 ${time}`
  if (mode === 'weekly') {
    const weekdays = ['一', '二', '三', '四', '五', '六', '日']
    return `每周${weekdays[weekday - 1] ?? '一'} ${time}`
  }
  if (mode === 'monthly') return `每月 ${day} 日 ${time}`
  return '不重置'
}
