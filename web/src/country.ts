import countries from 'flag-icons/country.json'
import type { ServerRecord } from './types/server'

const zhNames = new Intl.DisplayNames(['zh-CN'], { type: 'region' })
const enNames = new Intl.DisplayNames(['en'], { type: 'region' })
const chineseNames: Record<string, string> = { TW: '台湾', HK: '香港', MO: '澳门', CN: '中国大陆' }

export const countryOptions = countries.filter(country => country.iso).map(country => {
  const code = country.code.toUpperCase()
  const zhName = chineseNames[code] ?? zhNames.of(code) ?? code
  const enName = enNames.of(code) ?? code
  return { value: code, label: `${zhName} / ${enName}（${code}）`, zhName, enName }
}).sort((a, b) => a.zhName.localeCompare(b.zhName, 'zh-CN'))

const normalizeSearch = (value: string) => value.trim().toLocaleLowerCase().replace(/\s+/g, '')
export function filterCountry(pattern: string, option: { label?: unknown }): boolean {
  return normalizeSearch(String(option.label ?? '')).includes(normalizeSearch(pattern))
}
export function effectiveCountryCode(server: Pick<ServerRecord, 'country_override_code' | 'system_info'>): string {
  return server.country_override_code?.trim().toUpperCase() || server.system_info?.country_code?.trim().toUpperCase() || ''
}
export function countryLabel(server: Pick<ServerRecord, 'country_override_code' | 'system_info'>): string {
  const code = effectiveCountryCode(server)
  if (!code) return '自动识别'
  const name = countryOptions.find(option => option.value === code)?.zhName ?? code
  return server.country_override_code?.trim() ? name : `${name}（自动识别）`
}
