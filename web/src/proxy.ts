export type ProxyProtocol = 'vless' | 'shadowsocks'
export type ShadowsocksMethod = '2022-blake3-aes-128-gcm' | '2022-blake3-aes-256-gcm'

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
