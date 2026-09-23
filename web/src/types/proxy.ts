import type {
  ProxyProtocol,
  ShadowsocksMethod,
  ClientMetrics,
  ClientTrafficResetMode,
  ClientStatus,
} from '../proxy'
import type { ServerRecord } from './server'

export type ServerOption = Pick<ServerRecord,
  'id' | 'name' | 'system_info' | 'agent_implementation' | 'agent_api_version' | 'agent_capabilities'
>

export type ProxyConfig = {
  transport?: 'tcp'
  security?: 'tls' | 'reality'
  tls_mode?: 'acme' | 'manual'
  server_flow?: 'xtls-rprx-vision'
  server_name?: string
  fingerprint?: 'chrome'
  tls_certificate_configured: boolean
  reality_target?: string
  method?: ShadowsocksMethod
  network?: 'tcp,udp'
}

export type ClientSummary = {
  id: number
  proxy_id: number
  name: string
  client_udp443: boolean
  enabled: boolean
  expires_at: string | null
  expired: boolean
  quota_exhausted: boolean
  effective_enabled: boolean
  status: ClientStatus
  traffic_limit_bytes: number | null
  traffic_reset_mode: ClientTrafficResetMode
  traffic_reset_weekday: number
  traffic_reset_day: number
  traffic_reset_time: string
  next_reset_at: string | null
  metrics: ClientMetrics
  created_at: string
  updated_at: string
}

export type ClientRecord = {
  id: number
  proxy_id: number
  name: string
  client_udp443: boolean
  enabled: boolean
  expires_at: string | null
  expired: boolean
  quota_exhausted: boolean
  effective_enabled: boolean
  status: ClientStatus
  traffic_limit_bytes: number | null
  traffic_reset_mode: ClientTrafficResetMode
  traffic_reset_weekday: number
  traffic_reset_day: number
  traffic_reset_time: string
  next_reset_at: string | null
  metrics: ClientMetrics
  created_at: string
  updated_at: string
}

export type ProxyRecord = {
  id: number
  server_id: number
  server_name: string
  server_ipv4: string[]
  server_ipv6: string[]
  server_public_ipv4: string
  name: string
  protocol: ProxyProtocol
  listen_port: number
  entry_host_mode: 'auto' | 'manual'
  entry_host: string
  entry_address: string
  enabled: boolean
  config: ProxyConfig
  clients?: ClientSummary[]
  created_at: string
  updated_at: string
}

export type ClientShare = {
  client: ClientRecord
  proxy_name: string
  address: string
  port: number
  protocol: ProxyProtocol
  method?: ShadowsocksMethod
  network?: 'tcp,udp'
  security?: 'tls' | 'reality'
  server_name?: string
  fingerprint?: string
  flow?: string
  uri: string
}
