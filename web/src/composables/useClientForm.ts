import {
  ref,
} from 'vue'
import {
  formatClientExpirationInput,
  formatClientTrafficLimitInput,
  parseClientTrafficLimit,
  type ClientTrafficLimitUnit,
  type ClientTrafficResetMode,
} from '../proxy'
import type {
  ClientSummary,
  ClientRecord,
  ProxyRecord,
} from '../types/proxy'
import {
  api,
} from '../api/client'
import type {
  Ref,
} from 'vue'

export function useClientForm(selectedProxy: Ref<ProxyRecord | null>, error: Ref<string>, run: (action: () => Promise<void>) => Promise<void>, loadProxies: () => Promise<void>, refreshSelectedProxy: () => Promise<void>) {
  const clientFormOpen = ref(false)
  const clientFormMode = ref<'create' | 'edit'>('create')
  const editingClientID = ref<number | null>(null)
  const clientName = ref('')
  const clientEnabled = ref(true)
  const clientUDP443 = ref(false)
  const clientTrafficLimit = ref<string | number>('')
  const clientTrafficLimitUnit = ref<ClientTrafficLimitUnit>('G')
  const clientTrafficResetMode = ref<ClientTrafficResetMode>('never')
  const clientTrafficResetWeekday = ref(1)
  const clientTrafficResetDay = ref(1)
  const clientTrafficResetTime = ref('00:00')
  const clientExpirationMode = ref<'unlimited' | 'specified'>('unlimited')
  const clientExpiresAt = ref('')

  function openCreateClient() {
    if (!selectedProxy.value) return
    clientFormMode.value = 'create'
    editingClientID.value = null
    clientName.value = ''
    clientEnabled.value = true
    clientUDP443.value = false
    clientTrafficLimit.value = ''
    clientTrafficLimitUnit.value = 'G'
    clientTrafficResetMode.value = 'never'
    clientTrafficResetWeekday.value = 1
    clientTrafficResetDay.value = 1
    clientTrafficResetTime.value = '00:00'
    clientExpirationMode.value = 'unlimited'
    clientExpiresAt.value = ''
    clientFormOpen.value = true
  }

  async function openEditClient(client: ClientSummary) {
    await run(async () => {
      const response = await api<{ client: ClientRecord }>(`/api/clients/${client.id}`)
      clientFormMode.value = 'edit'
      editingClientID.value = client.id
      clientName.value = response.client.name
      clientEnabled.value = response.client.enabled
      clientUDP443.value = response.client.client_udp443
      const limit = formatClientTrafficLimitInput(response.client.traffic_limit_bytes)
      clientTrafficLimit.value = limit.value
      clientTrafficLimitUnit.value = limit.unit
      clientTrafficResetMode.value = response.client.traffic_reset_mode
      clientTrafficResetWeekday.value = response.client.traffic_reset_weekday
      clientTrafficResetDay.value = response.client.traffic_reset_day
      clientTrafficResetTime.value = response.client.traffic_reset_time
      clientExpirationMode.value = response.client.expires_at ? 'specified' : 'unlimited'
      clientExpiresAt.value = formatClientExpirationInput(response.client.expires_at)
      clientFormOpen.value = true
    })
  }

  async function saveClient() {
    if (!selectedProxy.value || !clientName.value.trim()) {
      error.value = '请填写客户端名称'
      return
    }
    const proxy = selectedProxy.value
    const trafficLimit = parseClientTrafficLimit(clientTrafficLimit.value, clientTrafficLimitUnit.value)
    if (trafficLimit === undefined) {
      error.value = '流量额度格式无效'
      return
    }
    if (clientTrafficResetMode.value === 'weekly' && (clientTrafficResetWeekday.value < 1 || clientTrafficResetWeekday.value > 7)) {
      error.value = '每周重置日期无效'
      return
    }
    if (clientTrafficResetMode.value === 'monthly' && (clientTrafficResetDay.value < 1 || clientTrafficResetDay.value > 31)) {
      error.value = '每月重置日期必须在 1–31 之间'
      return
    }
    if (clientTrafficResetMode.value !== 'never' && !/^([01]\d|2[0-3]):[0-5]\d$/.test(clientTrafficResetTime.value)) {
      error.value = '流量重置时间格式无效'
      return
    }
    if (clientExpirationMode.value === 'specified' && !/^\d{4}-\d{2}-\d{2}T([01]\d|2[0-3]):[0-5]\d$/.test(clientExpiresAt.value)) {
      error.value = '请选择有效的客户端到期时间'
      return
    }
    await run(async () => {
      const body = JSON.stringify({
        name: clientName.value,
        enabled: clientEnabled.value,
        client_udp443: proxy.protocol === 'vless' && clientUDP443.value,
        traffic_limit: clientTrafficLimit.value,
        limit_unit: clientTrafficLimitUnit.value,
        traffic_reset_mode: clientTrafficResetMode.value,
        traffic_reset_weekday: clientTrafficResetWeekday.value,
        traffic_reset_day: clientTrafficResetDay.value,
        traffic_reset_time: clientTrafficResetTime.value,
        expires_at: clientExpirationMode.value === 'specified' ? clientExpiresAt.value : null,
      })
      if (clientFormMode.value === 'create') {
        await api(`/api/proxies/${proxy.id}/clients`, { method: 'POST', body })
      } else {
        await api(`/api/clients/${editingClientID.value}`, { method: 'PATCH', body })
      }
      clientFormOpen.value = false
      await Promise.all([loadProxies(), refreshSelectedProxy()])
    })
  }
  return {
    clientFormOpen,
    clientFormMode,
    editingClientID,
    clientName,
    clientEnabled,
    clientUDP443,
    clientTrafficLimit,
    clientTrafficLimitUnit,
    clientTrafficResetMode,
    clientTrafficResetWeekday,
    clientTrafficResetDay,
    clientTrafficResetTime,
    clientExpirationMode,
    clientExpiresAt,
    openCreateClient,
    openEditClient,
    saveClient,
  }
}
