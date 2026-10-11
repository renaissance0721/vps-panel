export type NodeRole = 'direct' | 'landing'

export function nodeRoleLabel(value: NodeRole) {
  return value === 'landing' ? '落地节点' : '直连节点'
}
