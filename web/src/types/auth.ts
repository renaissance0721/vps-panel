export type User = {
  id: number
  username: string
  role: 'admin' | 'vip'
  created_at: string
}

export type AccessUser = Pick<User, 'id' | 'username' | 'role'>

export type AuthState = {
  requires_initialization: boolean
  authenticated: boolean
  user?: User
}
