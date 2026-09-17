export type Health = {
  status: string
  database: string
  version?: string
}

export type Invitation = {
  id: number
  created_by: number
  created_by_username: string
  expires_at: string
  created_at: string
  token?: string
}

export type Overview = {
  server_count: number
  proxy_count: number
  users: { username: string; role: 'admin' | 'vip' }[]
}
