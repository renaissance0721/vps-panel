import type { RoutingGroup } from './components/subscription/RoutingGroupEditor.vue'

export const MAX_ROUTING_PRESET_NAME_RUNES = 100

export type RoutingRuleProvider = {
  name: string
  url: string
  type: 'http'
  behavior: string
  format: string
  interval: number
}

export type RoutingPreset = {
  id: number
  name: string
  enabled: boolean
  is_default: boolean
  groups: RoutingGroup[]
  rule_providers: RoutingRuleProvider[]
  rules: string[]
}

export type CreateRoutingPresetPayload = Pick<RoutingPreset, 'name' | 'enabled' | 'groups' | 'rule_providers' | 'rules'>

export function cloneRoutingGroups(values: RoutingGroup[]) {
  return values.map((group) => ({ ...group, proxies: [...group.proxies] }))
}

export function cloneRoutingProviders(values: RoutingRuleProvider[]) {
  return values.map((provider) => ({ ...provider }))
}

export function routingPresetCopyName(name: string, existingNames: Iterable<string>) {
  const usedNames = new Set(existingNames)
  const nameRunes = Array.from(name.trim())
  for (let copyNumber = 1; ; copyNumber++) {
    const suffix = copyNumber === 1 ? ' - 副本' : ` - 副本 ${copyNumber}`
    const suffixRunes = Array.from(suffix)
    const base = nameRunes.slice(0, MAX_ROUTING_PRESET_NAME_RUNES - suffixRunes.length).join('').trimEnd()
    const candidate = `${base}${suffix}`
    if (!usedNames.has(candidate)) return candidate
  }
}

export function createRoutingPresetCopyPayload(
  value: RoutingPreset,
  existingNames: Iterable<string>,
): CreateRoutingPresetPayload {
  return {
    name: routingPresetCopyName(value.name, existingNames),
    enabled: value.enabled,
    groups: cloneRoutingGroups(value.groups),
    rule_providers: cloneRoutingProviders(value.rule_providers),
    rules: [...value.rules],
  }
}
