export function adminFirst<T extends { role: string }>(users: T[]): T[] {
  return [...users.filter((user) => user.role === 'admin'), ...users.filter((user) => user.role !== 'admin')]
}
