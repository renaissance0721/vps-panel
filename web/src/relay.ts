export type RelayNetwork = 'tcp' | 'udp' | 'tcp,udp'
export type RelayTargetType = 'proxy' | 'manual'

export function relayNetworkLabel(network: RelayNetwork) {
  if (network === 'tcp') return 'TCP'
  if (network === 'udp') return 'UDP'
  return 'TCP + UDP'
}

export function relayTargetLabel(value: {
  target_type: RelayTargetType
  target_proxy_name: string
  target_host: string
  target_port: number
  target_address_ready: boolean
}) {
  if (value.target_type === 'proxy') {
    return value.target_address_ready
      ? `${value.target_proxy_name} · ${relayEndpointLabel(value.target_host, value.target_port)}`
      : `${value.target_proxy_name} · 目标地址不可用`
  }
  return relayEndpointLabel(value.target_host, value.target_port)
}

export function relayEndpointLabel(host: string, port: number) {
  if (!host) return '入口地址不可用'
  const displayHost = host.includes(':') ? `[${host}]` : host
  return `${displayHost}:${port}`
}
