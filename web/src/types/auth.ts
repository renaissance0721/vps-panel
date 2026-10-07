export type User = {
  id: number
  username: string
  role: 'admin' | 'vip' | 'carpool' | 'subscriber'
  created_at: string
}

export type AccessUser = Pick<User, 'id' | 'username' | 'role'> & {
  email_masked?: string
  email_verified: boolean
}

export type AuthState = {
  requires_initialization: boolean
  authenticated: boolean
  user?: User
}

export type EmailStatus = {
  email: string
  verified: boolean
  pending_email: string
  available: boolean
  unavailable_reason?: string
}
