import { computed, ref } from 'vue'

import { api } from '../api/client'
import {
  formatClientExpirationInput,
  formatClientTrafficLimitInput,
  parseClientTrafficLimit,
  type ClientTrafficLimitUnit,
  type ClientTrafficResetMode,
} from '../proxy'
import type { AccessUser } from '../types/auth'
import type { ManagedUserDetail, ManagedUserNode } from '../types/userManagement'

export function useUserManagement() {
  const users = ref<AccessUser[]>([])
  const selectedUserID = ref<number | null>(null)
  const detail = ref<ManagedUserDetail | null>(null)
  const loading = ref(false)
  const submitting = ref(false)
  const error = ref('')

  const formOpen = ref(false)
  const formMode = ref<'create' | 'edit'>('create')
  const editingNode = ref<ManagedUserNode | null>(null)
  const clientName = ref('')
  const clientEnabled = ref(true)
  const clientUDP443 = ref(false)
  const trafficLimit = ref('')
  const trafficLimitUnit = ref<ClientTrafficLimitUnit>('G')
  const trafficResetMode = ref<ClientTrafficResetMode>('never')
  const trafficResetWeekday = ref(1)
  const trafficResetDay = ref(1)
  const trafficResetTime = ref('00:00')
  const expirationMode = ref<'unlimited' | 'specified'>('unlimited')
  const expiresAt = ref('')
  const billingPeriodMonths = ref<0 | 1 | 3 | 6 | 12>(0)

  const assignedNodes = computed(() => detail.value?.nodes.filter((node) => node.client !== null) ?? [])
  const availableNodes = computed(() => detail.value?.nodes.filter((node) => node.client === null) ?? [])

  async function run(action: () => Promise<void>) {
    submitting.value = true
    error.value = ''
    try {
      await action()
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : '操作失败'
    } finally {
      submitting.value = false
    }
  }

  async function loadUsers() {
    const response = await api<{ users: AccessUser[] }>('/api/admin/users')
    users.value = response.users
    if (selectedUserID.value === null || !users.value.some((user) => user.id === selectedUserID.value)) {
      selectedUserID.value = users.value[0]?.id ?? null
    }
  }

  async function loadDetail() {
    if (selectedUserID.value === null) {
      detail.value = null
      return
    }
    detail.value = await api<ManagedUserDetail>(`/api/admin/users/${selectedUserID.value}`)
  }

  async function load() {
    loading.value = true
    error.value = ''
    try {
      await loadUsers()
      await loadDetail()
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : '无法加载用户管理'
    } finally {
      loading.value = false
    }
  }

  async function selectUser() {
    loading.value = true
    error.value = ''
    try {
      await loadDetail()
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : '无法加载用户详情'
    } finally {
      loading.value = false
    }
  }

  function resetForm(node: ManagedUserNode) {
    editingNode.value = node
    clientName.value = ''
    clientEnabled.value = true
    clientUDP443.value = false
    trafficLimit.value = ''
    trafficLimitUnit.value = 'G'
    trafficResetMode.value = 'never'
    trafficResetWeekday.value = 1
    trafficResetDay.value = 1
    trafficResetTime.value = '00:00'
    expirationMode.value = 'unlimited'
    expiresAt.value = ''
    billingPeriodMonths.value = 0
  }

  function openCreate(node: ManagedUserNode) {
    resetForm(node)
    formMode.value = 'create'
    clientName.value = `${detail.value?.user.username ?? 'user'}-${node.proxy_name}`
    formOpen.value = true
  }

  function openEdit(node: ManagedUserNode) {
    if (!node.client) return
    resetForm(node)
    formMode.value = 'edit'
    clientName.value = node.client.name
    clientEnabled.value = node.client.enabled
    clientUDP443.value = node.client.client_udp443
    const limit = formatClientTrafficLimitInput(node.client.traffic_limit_bytes)
    trafficLimit.value = String(limit.value)
    trafficLimitUnit.value = limit.unit
    trafficResetMode.value = node.client.traffic_reset_mode
    trafficResetWeekday.value = node.client.traffic_reset_weekday
    trafficResetDay.value = node.client.traffic_reset_day
    trafficResetTime.value = node.client.traffic_reset_time
    expirationMode.value = node.client.expires_at ? 'specified' : 'unlimited'
    expiresAt.value = formatClientExpirationInput(node.client.expires_at)
    billingPeriodMonths.value = node.client.billing_period_months ?? 0
    formOpen.value = true
  }

  function validateForm() {
    if (!clientName.value.trim()) return '请填写客户端名称'
    if (parseClientTrafficLimit(trafficLimit.value, trafficLimitUnit.value) === undefined) return '流量额度格式无效'
    if (trafficResetMode.value === 'weekly' && (trafficResetWeekday.value < 1 || trafficResetWeekday.value > 7)) return '每周重置日期无效'
    if (trafficResetMode.value === 'monthly' && (trafficResetDay.value < 1 || trafficResetDay.value > 31)) return '每月重置日期必须在 1–31 之间'
    if (trafficResetMode.value !== 'never' && !/^([01]\d|2[0-3]):[0-5]\d$/.test(trafficResetTime.value)) return '流量重置时间格式无效'
    if (expirationMode.value === 'specified' && !/^\d{4}-\d{2}-\d{2}T([01]\d|2[0-3]):[0-5]\d$/.test(expiresAt.value)) return '请选择有效的到期时间'
    return ''
  }

  async function saveNode() {
    const node = editingNode.value
    const userID = selectedUserID.value
    const validationError = validateForm()
    if (!node || userID === null || validationError) {
      error.value = validationError || '请选择用户和代理节点'
      return
    }
    await run(async () => {
      const payload = {
        name: clientName.value.trim(),
        enabled: clientEnabled.value,
        client_udp443: node.protocol === 'vless' && clientUDP443.value,
        traffic_limit: trafficLimit.value,
        limit_unit: trafficLimitUnit.value,
        traffic_reset_mode: trafficResetMode.value,
        traffic_reset_weekday: trafficResetWeekday.value,
        traffic_reset_day: trafficResetDay.value,
        traffic_reset_time: trafficResetTime.value,
        expires_at: expirationMode.value === 'specified' ? expiresAt.value : null,
      }
      if (formMode.value === 'create') {
        await api(`/api/admin/users/${userID}/nodes`, {
          method: 'POST',
          body: JSON.stringify({ ...payload, proxy_id: node.proxy_id, billing_period_months: billingPeriodMonths.value || null }),
        })
      } else if (node.client) {
        await api(`/api/clients/${node.client.id}`, { method: 'PATCH', body: JSON.stringify(payload) })
        await api(`/api/admin/clients/${node.client.id}/assignment`, {
          method: 'PATCH',
          body: JSON.stringify({ user_id: userID, billing_period_months: billingPeriodMonths.value || null }),
        })
      }
      formOpen.value = false
      await loadDetail()
    })
  }

  async function removeNode(node: ManagedUserNode) {
    if (!node.client || !window.confirm('将删除该用户在此节点的客户端凭据，并移除基于该节点创建的用户中转。是否继续？')) return
    await run(async () => {
      await api(`/api/clients/${node.client!.id}`, { method: 'DELETE' })
      await loadDetail()
    })
  }

  async function removeRelay(id: number) {
    if (!window.confirm('确定删除这条用户中转吗？')) return
    await run(async () => {
      await api(`/api/admin/user-relays/${id}`, { method: 'DELETE' })
      await loadDetail()
    })
  }

  async function reviewPasswordRequest(action: 'approve' | 'reject') {
    const request = detail.value?.password_request
    if (!request) return
    await run(async () => {
      await api(`/api/admin/password-change-requests/${request.id}/${action}`, { method: 'POST' })
      await loadDetail()
    })
  }

  return {
    users, selectedUserID, detail, assignedNodes, availableNodes, loading, submitting, error,
    formOpen, formMode, editingNode, clientName, clientEnabled, clientUDP443,
    trafficLimit, trafficLimitUnit, trafficResetMode, trafficResetWeekday,
    trafficResetDay, trafficResetTime, expirationMode, expiresAt, billingPeriodMonths,
    load, loadDetail, selectUser, openCreate, openEdit, saveNode, removeNode, removeRelay,
    reviewPasswordRequest,
  }
}
