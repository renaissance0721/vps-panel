import { ref, type Ref } from 'vue'

export type TrafficLimitUnit = 'G' | 'T'

export type TrafficCountMode = 'single' | 'bidirectional'

export type TrafficWarningLevel = 'warning' | 'exhausted' | null

export const GIBIBYTE = 1024 ** 3
export const TEBIBYTE = 1024 ** 4

export type TrafficConfigServer = {
  id: number
  archived_at?: string
  monthly_traffic_limit_bytes: number | null
  traffic_count_mode: TrafficCountMode
  traffic_reset_day: number
  traffic_reset_time: string
}

export type TrafficConfigPayload = {
  monthly_traffic_limit_bytes: number | null
  traffic_count_mode: TrafficCountMode
  traffic_reset_day: number
  traffic_reset_time: string
}

export function parseTrafficLimit(
  value: string | number,
  unit: TrafficLimitUnit,
): number | null | undefined {
  const normalized = String(value).trim()
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

export function normalizeTrafficResetTime(value: string): string {
  const match = /^([01]\d|2[0-3]):([0-5]\d)(?::[0-5]\d)?$/.exec(value)
  return match ? `${match[1]}:${match[2]}` : value
}

export function useTrafficForm<T extends TrafficConfigServer>(
  selectedServer: Ref<T | null>,
  submitting: Ref<boolean>,
  updateServer: (serverID: number, payload: TrafficConfigPayload) => Promise<T>,
  loadServers: () => Promise<void>,
  reportRefreshError: (message: string) => void,
) {
  const trafficModalOpen = ref(false)
  const trafficFormError = ref('')
  const trafficLimitInput = ref<string | number>('')
  const trafficLimitUnit = ref<TrafficLimitUnit>('G')
  const trafficCountMode = ref<TrafficCountMode>('single')
  const trafficResetDay = ref(1)
  const trafficResetTime = ref('00:00')

  function resetTrafficForm() {
    trafficFormError.value = ''
    trafficLimitInput.value = ''
    trafficLimitUnit.value = 'G'
    trafficCountMode.value = 'single'
    trafficResetDay.value = 1
    trafficResetTime.value = '00:00'
  }

  function openTrafficModal() {
    const server = selectedServer.value
    if (!server || server.archived_at) return
    resetTrafficForm()
    const limit = formatTrafficLimitInput(server.monthly_traffic_limit_bytes)
    trafficLimitInput.value = limit.value
    trafficLimitUnit.value = limit.unit
    trafficCountMode.value = server.traffic_count_mode
    trafficResetDay.value = server.traffic_reset_day
    trafficResetTime.value = normalizeTrafficResetTime(server.traffic_reset_time)
    trafficModalOpen.value = true
  }

  function closeTrafficModal() {
    trafficModalOpen.value = false
    resetTrafficForm()
  }

  async function saveTrafficConfig() {
    trafficFormError.value = ''
    const server = selectedServer.value
    if (!server || submitting.value) return

    const monthlyLimit = parseTrafficLimit(trafficLimitInput.value, trafficLimitUnit.value)
    if (monthlyLimit === undefined) {
      trafficFormError.value = '月流量额度格式无效，请输入大于 0 的数值，或留空表示不限'
      return
    }
    if (!Number.isInteger(trafficResetDay.value) || trafficResetDay.value < 1 || trafficResetDay.value > 31) {
      trafficFormError.value = '流量重置日必须在 1–31 之间'
      return
    }
    if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(trafficResetTime.value)) {
      trafficFormError.value = '流量重置时间格式无效'
      return
    }

    submitting.value = true
    let updatedServer: T
    try {
      updatedServer = await updateServer(server.id, {
        monthly_traffic_limit_bytes: monthlyLimit,
        traffic_count_mode: trafficCountMode.value,
        traffic_reset_day: trafficResetDay.value,
        traffic_reset_time: trafficResetTime.value,
      })
    } catch (reason) {
      trafficFormError.value = errorMessage(reason)
      submitting.value = false
      return
    }

    selectedServer.value = updatedServer
    trafficModalOpen.value = false
    resetTrafficForm()
    try {
      await loadServers()
    } catch (reason) {
      reportRefreshError(errorMessage(reason))
    } finally {
      submitting.value = false
    }
  }

  return {
    trafficModalOpen,
    trafficFormError,
    trafficLimitInput,
    trafficLimitUnit,
    trafficCountMode,
    trafficResetDay,
    trafficResetTime,
    openTrafficModal,
    closeTrafficModal,
    resetTrafficForm,
    saveTrafficConfig,
  }
}

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : '操作失败'
}
