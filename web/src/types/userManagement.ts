import type { AccessUser } from './auth'
import type { ClientRecord } from './proxy'

export type ManagedUserNode = {
  proxy_id: number
  server_name: string
  proxy_name: string
  protocol: 'vless' | 'shadowsocks'
  client: ClientRecord | null
}

export type ManagedUserRelay = {
  id: number
  name: string
  mode: 'assigned_node' | 'custom'
  source: { server_name: string; proxy_name: string }
  target?: { server_name: string; proxy_name: string }
  entry_address: string
  enabled: boolean
  created_at: string
}

export type ManagedPasswordRequest = {
  id: number
  user_id: number
  username: string
  status: 'pending'
  created_at: string
  reviewed_at: string | null
}

export type ManagedUserDetail = {
  user: AccessUser
  nodes: ManagedUserNode[]
  relays: ManagedUserRelay[]
  password_request: ManagedPasswordRequest | null
}
