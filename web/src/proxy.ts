export type ProxyProtocol = 'vless' | 'shadowsocks'
export type ShadowsocksMethod = '2022-blake3-aes-128-gcm' | '2022-blake3-aes-256-gcm'

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
