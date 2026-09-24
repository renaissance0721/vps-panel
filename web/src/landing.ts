export type LandingVisibility = 'private' | 'public'
export type LandingProtocol = 'vless' | 'shadowsocks'

export type LandingRecord = {
  id: number
  name: string
  visibility: LandingVisibility
  protocol: LandingProtocol
  host: string
  port: number
  owned_by_me: boolean
  created_at: string
  updated_at: string
}

export function landingProtocolLabel(protocol: LandingProtocol) {
  return protocol === 'vless' ? 'VLESS' : 'Shadowsocks'
}

export function landingVisibilityLabel(visibility: LandingVisibility) {
  return visibility === 'public' ? '公开' : '私有'
}
