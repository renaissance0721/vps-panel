export type TrafficLimitUnit = 'G' | 'T'

export type TrafficWarningLevel = 'warning' | 'exhausted' | null

export const GIBIBYTE = 1024 ** 3
export const TEBIBYTE = 1024 ** 4

export function parseTrafficLimit(
  value: string,
  unit: TrafficLimitUnit,
): number | null | undefined {
  const normalized = value.trim()
  if (normalized === '') return null
  if (!/^\d+(?:\.\d+)?$/.test(normalized)) return undefined

  const amount = Number(normalized)
  if (amount === 0) return null

  const multiplier = unit === 'T' ? TEBIBYTE : GIBIBYTE
  const bytes = Math.round(amount * multiplier)
  if (!Number.isSafeInteger(bytes) || bytes <= 0) return undefined
  return bytes
}

export function formatTrafficLimitInput(value: number | null): {
  value: string
  unit: TrafficLimitUnit
} {
  if (!value) return { value: '', unit: 'G' }

  const unit: TrafficLimitUnit = value % TEBIBYTE === 0 ? 'T' : 'G'
  const multiplier = unit === 'T' ? TEBIBYTE : GIBIBYTE
  return {
    value: Number((value / multiplier).toFixed(6)).toString(),
    unit,
  }
}

export function trafficWarningLevel(
  usedBytes: number,
  limitBytes: number | null,
): TrafficWarningLevel {
  if (!limitBytes || limitBytes <= 0) return null
  if (usedBytes >= limitBytes) return 'exhausted'
  if (usedBytes / limitBytes >= 0.9) return 'warning'
  return null
}
