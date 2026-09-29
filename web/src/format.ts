import type { User } from './types/auth'

export function formatTime(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

export function userRoleLabel(role: User['role']) {
  if (role === 'admin') return '管理员'
  if (role === 'vip') return 'VIP用户'
  if (role === 'subscriber') return '订阅用户'
  return '拼车用户'
}
