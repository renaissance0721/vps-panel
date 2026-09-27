export type Health = {
  status: string
  database: string
  version?: string
}

export type Invitation = {
  id: number
  created_by: number
  created_by_username: string
	role: 'vip' | 'user'
  expires_at: string
  created_at: string
  token?: string
}

export type Overview = {
  server_count: number
  proxy_count: number
  users: { username: string; role: 'admin' | 'vip' | 'user' }[]
}

export type PasswordChangeRequest = {
  id: number
  user_id: number
  username: string
  status: 'pending' | 'approved' | 'rejected'
  created_at: string
  reviewed_at: string | null
}

export type AdminUserRelay = {
  id: number
  name: string
  username: string
  mode: 'assigned_node' | 'custom'
  source: { server_name: string; proxy_name: string }
  target?: { server_name: string; proxy_name: string }
  entry_address: string
  created_at: string
}
