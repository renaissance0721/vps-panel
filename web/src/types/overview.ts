export type Health = {
  status: string
  database: string
  version?: string
}

export type Invitation = {
  id: number
  created_by: number
  created_by_username: string
	role: 'vip' | 'carpool' | 'subscriber'
  expires_at: string
  created_at: string
  token?: string
}

export type Overview = {
  server_count: number
  proxy_count: number
  pending_operation_count: number
  failed_operation_count: number
  users: { username: string; role: 'admin' | 'vip' | 'carpool' | 'subscriber' }[]
}

export type PasswordChangeRequest = {
  id: number
  user_id: number
  username: string
  role: 'vip' | 'carpool' | 'subscriber'
  status: 'pending' | 'approved' | 'rejected'
  created_at: string
  reviewed_at: string | null
}
