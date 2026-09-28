<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NCheckbox, NEmpty, NInput, NModal, NSpin, NTag } from 'naive-ui'
import { api } from '../api/client'
import { formatTime } from '../format'
import { formatClientExpirationInput, formatClientTrafficBytes } from '../proxy'
import type { ProxyRecord } from '../types/proxy'

type PublishedNode = {
  id: number
  name: string
  mode: 'direct' | 'relay'
  target_proxy_id: number
  target_proxy_name: string
  target_server_name: string
  source_proxy_id?: number
  source_proxy_name?: string
  source_server_name?: string
  entry_address?: string
  entry_port?: number
  enabled: boolean
  position?: number
}
type Plan = {
  id: number
  name: string
  enabled: boolean
  traffic_limit_bytes: number | null
  traffic_reset_mode: 'never' | 'monthly'
  traffic_reset_day: number
  traffic_reset_time: string
  default_validity_days: number | null
  billing_period_months: number | null
  nodes: PublishedNode[]
}
type PasswordRequest = { id: number; status: 'pending' | 'approved' | 'rejected' }
type Subscriber = {
  user_id: number
  username: string
  plan_id: number | null
  plan_name: string
  plan_enabled: boolean
  enabled: boolean
  expires_at: string | null
  enabled_node_count: number
  traffic_limit_bytes: number | null
  used_bytes: number
  cycle_started_at: string
  next_reset_at: string | null
  billing_period_months: number | null
  active: boolean
  status: string
  subscription_url?: string
  password_request?: PasswordRequest
}

type Tab = 'users' | 'plans' | 'nodes'
const currentTab = ref<Tab>('users')
const users = ref<Subscriber[]>([])
const plans = ref<Plan[]>([])
const nodes = ref<PublishedNode[]>([])
const proxies = ref<ProxyRecord[]>([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')

const nodeModalOpen = ref(false)
const editingNode = ref<PublishedNode | null>(null)
const nodeName = ref('')
const nodeMode = ref<'direct' | 'relay'>('direct')
const nodeTargetProxyID = ref<number | null>(null)
const nodeSourceProxyID = ref<number | null>(null)
const nodeEnabled = ref(true)

const planModalOpen = ref(false)
const editingPlan = ref<Plan | null>(null)
const planName = ref('')
const planEnabled = ref(true)
const planTrafficGiB = ref('')
const planResetMode = ref<'never' | 'monthly'>('monthly')
const planResetDay = ref(1)
const planResetTime = ref('00:00')
const planValidityDays = ref('')
const planBillingMonths = ref(0)
const planNodeIDs = ref<number[]>([])
const planFormError = ref('')

const userModalOpen = ref(false)
const selectedUser = ref<Subscriber | null>(null)
const userPlanID = ref(0)
const userEnabled = ref(true)
const userExpiresAt = ref('')
const copiedUserURL = ref(false)

const tabs: { id: Tab; label: string }[] = [
  { id: 'users', label: '订阅用户' },
  { id: 'plans', label: '套餐' },
  { id: 'nodes', label: '发布节点' },
]
const enabledPlans = computed(() => plans.value.filter((plan) => plan.enabled || plan.id === selectedUser.value?.plan_id))
const orderedPlanNodes = computed(() => {
  const byID = new Map(nodes.value.map((node) => [node.id, node]))
  const selected = planNodeIDs.value.flatMap((id) => {
    const node = byID.get(id)
    return node ? [node] : []
  })
  const selectedIDs = new Set(planNodeIDs.value)
  return [...selected, ...nodes.value.filter((node) => !selectedIDs.has(node.id))]
})

const statusLabels: Record<string, string> = {
  normal: '正常', unconfigured: '未开通套餐', disabled: '已停用', plan_disabled: '套餐已停用',
  expired: '已到期', exhausted: '流量已用完',
}

async function loadAll() {
  const [userResult, planResult, nodeResult, proxyResult] = await Promise.all([
    api<{ users: Subscriber[] }>('/api/admin/subscription/users'),
    api<{ plans: Plan[] }>('/api/admin/subscription/plans'),
    api<{ nodes: PublishedNode[] }>('/api/admin/subscription/nodes'),
    api<{ proxies: ProxyRecord[] }>('/api/proxies'),
  ])
  users.value = userResult.users
  plans.value = planResult.plans
  nodes.value = nodeResult.nodes
  proxies.value = proxyResult.proxies
}

async function run(action: () => Promise<void>) {
  busy.value = true
  error.value = ''
  try {
    await action()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '操作失败'
  } finally {
    busy.value = false
  }
}

function openCreateNode() {
  editingNode.value = null
  nodeName.value = ''
  nodeMode.value = 'direct'
  nodeTargetProxyID.value = proxies.value[0]?.id ?? null
  nodeSourceProxyID.value = null
  nodeEnabled.value = true
  nodeModalOpen.value = true
}

function openEditNode(value: PublishedNode) {
  editingNode.value = value
  nodeName.value = value.name
  nodeMode.value = value.mode
  nodeTargetProxyID.value = value.target_proxy_id
  nodeSourceProxyID.value = value.source_proxy_id ?? null
  nodeEnabled.value = value.enabled
  nodeModalOpen.value = true
}

async function saveNode() {
  await run(async () => {
    if (editingNode.value) {
      await api(`/api/admin/subscription/nodes/${editingNode.value.id}`, {
        method: 'PATCH', body: JSON.stringify({ name: nodeName.value, enabled: nodeEnabled.value }),
      })
    } else {
      await api('/api/admin/subscription/nodes', {
        method: 'POST',
        body: JSON.stringify({
          name: nodeName.value, mode: nodeMode.value, target_proxy_id: nodeTargetProxyID.value,
          source_proxy_id: nodeMode.value === 'relay' ? nodeSourceProxyID.value : null, enabled: nodeEnabled.value,
        }),
      })
    }
    nodeModalOpen.value = false
    await loadAll()
  })
}

async function deleteNode(value: PublishedNode) {
  if (!window.confirm(`确定删除发布节点“${value.name}”吗？`)) return
  await run(async () => {
    await api(`/api/admin/subscription/nodes/${value.id}`, { method: 'DELETE' })
    await loadAll()
  })
}

function openCreatePlan() {
  planFormError.value = ''
  editingPlan.value = null
  planName.value = ''
  planEnabled.value = true
  planTrafficGiB.value = ''
  planResetMode.value = 'monthly'
  planResetDay.value = 1
  planResetTime.value = '00:00'
  planValidityDays.value = ''
  planBillingMonths.value = 0
  planNodeIDs.value = []
  planModalOpen.value = true
}

function populatePlanForm(value: Plan) {
  editingPlan.value = value
  planName.value = value.name
  planEnabled.value = value.enabled
  planTrafficGiB.value = value.traffic_limit_bytes === null ? '' : String(value.traffic_limit_bytes / 1024 ** 3)
  planResetMode.value = value.traffic_reset_mode
  planResetDay.value = value.traffic_reset_day
  planResetTime.value = value.traffic_reset_time
  planValidityDays.value = value.default_validity_days === null ? '' : String(value.default_validity_days)
  planBillingMonths.value = value.billing_period_months ?? 0
  planNodeIDs.value = value.nodes.map((node) => node.id)
}

function openEditPlan(value: Plan) {
  planFormError.value = ''
  populatePlanForm(value)
  planModalOpen.value = true
}

function togglePlanNode(id: number, checked: boolean) {
  if (checked && !planNodeIDs.value.includes(id)) planNodeIDs.value.push(id)
  if (!checked) planNodeIDs.value = planNodeIDs.value.filter((value) => value !== id)
}

function movePlanNode(index: number, direction: -1 | 1) {
  const target = index + direction
  if (target < 0 || target >= planNodeIDs.value.length) return
  const values = [...planNodeIDs.value]
  ;[values[index], values[target]] = [values[target], values[index]]
  planNodeIDs.value = values
}

async function savePlan() {
  if (busy.value) return
  planFormError.value = ''

  const name = planName.value.trim()
  if (!name) {
    planFormError.value = '套餐名称不能为空'
    return
  }
  const trafficRaw = String(planTrafficGiB.value ?? '').trim()
  const trafficGiB = trafficRaw === '' ? null : Number(trafficRaw)
  if (trafficGiB !== null && (!Number.isFinite(trafficGiB) || trafficGiB < 0)) {
    planFormError.value = '流量额度必须是有限且不小于 0 的数字'
    return
  }
  const trafficLimit = trafficGiB === null ? null : Math.round(trafficGiB * 1024 ** 3)
  if (trafficLimit !== null && !Number.isFinite(trafficLimit)) {
    planFormError.value = '流量额度必须是有限且不小于 0 的数字'
    return
  }
  const validityRaw = String(planValidityDays.value ?? '').trim()
  const validityDays = validityRaw === '' ? null : Number(validityRaw)
  if (validityDays !== null && (!Number.isInteger(validityDays) || validityDays < 1)) {
    planFormError.value = '默认有效天数必须是大于等于 1 的整数'
    return
  }
  const resetDay = Number(planResetDay.value)
  if (planResetMode.value === 'monthly' && (!Number.isInteger(resetDay) || resetDay < 1 || resetDay > 31)) {
    planFormError.value = '每月重置日期必须是 1–31 的整数'
    return
  }
  if (planResetMode.value === 'monthly' && !/^(?:[01]\d|2[0-3]):[0-5]\d$/.test(planResetTime.value)) {
    planFormError.value = '每月重置时间必须是有效的 HH:mm'
    return
  }
  if (![0, 1, 3, 6, 12].includes(planBillingMonths.value)) {
    planFormError.value = '付款周期仅支持未设置、1、3、6 或 12 个月'
    return
  }

  const body = {
    name, enabled: planEnabled.value, traffic_limit_bytes: trafficLimit,
    traffic_reset_mode: planResetMode.value, traffic_reset_day: resetDay,
    traffic_reset_time: planResetTime.value, default_validity_days: validityDays,
    billing_period_months: planBillingMonths.value || null,
  }

  busy.value = true
  let id = editingPlan.value?.id
  const creating = id === undefined
  let planSaved = false
  let nodesSaved = false
  try {
    if (id !== undefined) {
      await api(`/api/admin/subscription/plans/${id}`, { method: 'PATCH', body: JSON.stringify(body) })
    } else {
      const created = await api<{ plan: Plan }>('/api/admin/subscription/plans', { method: 'POST', body: JSON.stringify(body) })
      id = created.plan.id
    }
    planSaved = true
    await api(`/api/admin/subscription/plans/${id}/nodes`, {
      method: 'PUT', body: JSON.stringify({ node_ids: planNodeIDs.value }),
    })
    nodesSaved = true
    await loadAll()
    planModalOpen.value = false
  } catch (reason) {
    const message = reason instanceof Error ? reason.message : '保存套餐失败'
    if (planSaved && !nodesSaved) {
      planFormError.value = creating
        ? `套餐已创建，但节点列表保存失败：${message}`
        : `套餐基本信息已保存，但节点列表保存失败：${message}`
      try {
        await loadAll()
        const current = plans.value.find((value) => value.id === id)
        if (current) populatePlanForm(current)
      } catch (refreshReason) {
        const refreshMessage = refreshReason instanceof Error ? refreshReason.message : '未知错误'
        planFormError.value += `；刷新服务端状态失败：${refreshMessage}`
      }
    } else if (planSaved) {
      planFormError.value = `套餐已保存，但刷新列表失败：${message}`
    } else {
      planFormError.value = message
    }
  } finally {
    busy.value = false
  }
}

async function deletePlan(value: Plan) {
  if (!window.confirm(`确定删除套餐“${value.name}”吗？`)) return
  await run(async () => {
    await api(`/api/admin/subscription/plans/${value.id}`, { method: 'DELETE' })
    await loadAll()
  })
}

async function openUser(value: Subscriber) {
  await run(async () => {
    const response = await api<{ user: Subscriber }>(`/api/admin/subscription/users/${value.user_id}`)
    selectedUser.value = response.user
    userPlanID.value = response.user.plan_id ?? 0
    userEnabled.value = response.user.enabled
    userExpiresAt.value = formatClientExpirationInput(response.user.expires_at)
    copiedUserURL.value = false
    userModalOpen.value = true
  })
}

async function saveUser() {
  const value = selectedUser.value
  if (!value) return
  await run(async () => {
    await api(`/api/admin/subscription/users/${value.user_id}`, {
      method: 'PATCH',
      body: JSON.stringify({
        plan_id: userPlanID.value || null,
        enabled: userEnabled.value,
        expires_at: userExpiresAt.value || null,
      }),
    })
    userModalOpen.value = false
    await loadAll()
  })
}

async function resetUserTraffic() {
  const value = selectedUser.value
  if (!value || !window.confirm(`确定重置 ${value.username} 的本周期流量吗？`)) return
  await run(async () => {
    const response = await api<{ user: Subscriber }>(`/api/admin/subscription/users/${value.user_id}/traffic/reset`, { method: 'POST' })
    selectedUser.value = response.user
    await loadAll()
  })
}

async function regenerateUserToken() {
  const value = selectedUser.value
  if (!value || !window.confirm('重新生成后，旧订阅链接会立即失效。确定继续吗？')) return
  await run(async () => {
    const response = await api<{ subscription_url: string }>(`/api/admin/subscription/users/${value.user_id}/token/regenerate`, { method: 'POST' })
    value.subscription_url = response.subscription_url
    copiedUserURL.value = false
  })
}

async function copyUserURL() {
  if (!selectedUser.value?.subscription_url) return
  await navigator.clipboard.writeText(selectedUser.value.subscription_url)
  copiedUserURL.value = true
}

async function reviewUserPassword(action: 'approve' | 'reject') {
  const request = selectedUser.value?.password_request
  if (!request) return
  await run(async () => {
    await api(`/api/admin/password-change-requests/${request.id}/${action}`, { method: 'POST' })
    if (selectedUser.value) selectedUser.value.password_request = undefined
  })
}

function billingLabel(months: number | null) {
  return ({ 1: '月付', 3: '季付', 6: '半年付', 12: '年付' } as Record<number, string>)[months ?? 0] ?? '未设置'
}

onMounted(async () => {
  try {
    await loadAll()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法加载订阅管理数据'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <n-alert v-if="error" class="page-alert" type="error" closable @close="error = ''">{{ error }}</n-alert>
  <div class="subscription-tabs" role="tablist">
    <n-button v-for="tab in tabs" :key="tab.id" :type="currentTab === tab.id ? 'primary' : 'default'" @click="currentTab = tab.id">{{ tab.label }}</n-button>
  </div>
  <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载订阅管理…</span></div>

  <section v-else-if="currentTab === 'users'" class="subscription-section">
    <n-empty v-if="users.length === 0" description="暂无订阅用户" />
    <div v-else class="server-table-wrap"><table class="server-table"><thead><tr><th>用户</th><th>套餐</th><th>已用 / 总量</th><th>到期</th><th>节点</th><th>状态</th><th>操作</th></tr></thead><tbody>
      <tr v-for="value in users" :key="value.user_id"><td>{{ value.username }}</td><td>{{ value.plan_name || '未开通' }}</td><td>{{ formatClientTrafficBytes(value.used_bytes) }} / {{ value.traffic_limit_bytes !== null ? formatClientTrafficBytes(value.traffic_limit_bytes) : '不限' }}</td><td>{{ value.expires_at ? formatTime(value.expires_at) : '不限' }}</td><td>{{ value.enabled_node_count }}</td><td><n-tag :type="value.active ? 'success' : 'warning'" size="small">{{ statusLabels[value.status] ?? value.status }}</n-tag></td><td><n-button size="small" secondary @click="openUser(value)">查看 / 编辑</n-button></td></tr>
    </tbody></table></div>
  </section>

  <section v-else-if="currentTab === 'plans'" class="subscription-section">
    <div class="section-heading"><span></span><n-button type="primary" @click="openCreatePlan">新增套餐</n-button></div>
    <n-empty v-if="plans.length === 0" description="暂无套餐" />
    <div v-else class="user-management-grid">
      <n-card v-for="value in plans" :key="value.id" :title="value.name">
        <template #header-extra><n-tag :type="value.enabled ? 'success' : 'default'">{{ value.enabled ? '启用' : '停用' }}</n-tag></template>
        <p>{{ value.traffic_limit_bytes !== null ? formatClientTrafficBytes(value.traffic_limit_bytes) : '不限流量' }} · {{ value.traffic_reset_mode === 'monthly' ? `每月 ${value.traffic_reset_day} 日 ${value.traffic_reset_time} 重置` : '不重置' }}</p>
        <p>默认 {{ value.default_validity_days ? `${value.default_validity_days} 天` : '不限期' }} · {{ billingLabel(value.billing_period_months) }} · {{ value.nodes.length }} 个节点</p>
        <div class="modal-actions"><n-button secondary @click="openEditPlan(value)">编辑</n-button><n-button type="error" secondary @click="deletePlan(value)">删除</n-button></div>
      </n-card>
    </div>
  </section>

  <section v-else class="subscription-section">
    <div class="section-heading"><span></span><n-button type="primary" :disabled="proxies.length === 0" @click="openCreateNode">新增发布节点</n-button></div>
    <n-empty v-if="nodes.length === 0" description="暂无发布节点" />
    <div v-else class="user-management-grid">
      <n-card v-for="value in nodes" :key="value.id" :title="value.name">
        <template #header-extra><n-tag :type="value.enabled ? 'success' : 'default'">{{ value.enabled ? '启用' : '停用' }}</n-tag></template>
        <p v-if="value.mode === 'direct'">单一节点</p><p v-else>中转 + 落地</p>
        <p v-if="value.mode === 'relay'">{{ value.source_server_name }} · {{ value.source_proxy_name }} → {{ value.target_server_name }} · {{ value.target_proxy_name }}</p>
        <p v-else>{{ value.target_server_name }} · {{ value.target_proxy_name }}</p>
        <p v-if="value.mode === 'relay'">入口：{{ value.entry_address }}:{{ value.entry_port }}</p>
        <div class="modal-actions"><n-button secondary @click="openEditNode(value)">编辑</n-button><n-button type="error" secondary @click="deleteNode(value)">删除</n-button></div>
      </n-card>
    </div>
  </section>

  <n-modal v-model:show="nodeModalOpen"><n-card class="client-form-card" :title="editingNode ? '编辑发布节点' : '新增发布节点'" closable @close="nodeModalOpen = false"><form class="auth-form" @submit.prevent="saveNode">
    <label><span>发布名称</span><n-input v-model:value="nodeName" maxlength="100" /></label>
    <template v-if="!editingNode">
      <label><span>模式</span><select v-model="nodeMode" class="settings-input"><option value="direct">单一节点</option><option value="relay">中转 + 落地</option></select></label>
      <label v-if="nodeMode === 'relay'"><span>中转 Proxy</span><select v-model.number="nodeSourceProxyID" class="settings-input"><option :value="null">请选择</option><option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">{{ proxy.server_name }} · {{ proxy.name }}</option></select></label>
      <label><span>落地 Proxy</span><select v-model.number="nodeTargetProxyID" class="settings-input"><option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">{{ proxy.server_name }} · {{ proxy.name }}</option></select></label>
    </template>
    <n-alert v-else type="info">创建后不能修改模式、中转 Proxy 或落地 Proxy；如需改变拓扑，请删除后重新创建。</n-alert>
    <n-checkbox v-model:checked="nodeEnabled">启用发布节点</n-checkbox>
    <div class="modal-actions"><n-button @click="nodeModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy" :disabled="!nodeTargetProxyID || (nodeMode === 'relay' && !nodeSourceProxyID)">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-model:show="planModalOpen"><n-card class="client-form-card subscription-form-card" :title="editingPlan ? '编辑套餐' : '新增套餐'" closable @close="planModalOpen = false"><form class="auth-form" novalidate @submit.prevent="savePlan">
    <n-alert v-if="planFormError" type="error" closable @close="planFormError = ''">{{ planFormError }}</n-alert>
    <label><span>名称</span><n-input v-model:value="planName" maxlength="100" /></label>
    <label><span>流量额度（GiB，留空不限）</span><input v-model="planTrafficGiB" class="settings-input" type="number" min="0" step="any" /></label>
    <label><span>重置模式</span><select v-model="planResetMode" class="settings-input"><option value="monthly">每月</option><option value="never">不重置</option></select></label>
    <label v-if="planResetMode === 'monthly'"><span>重置日期</span><input v-model.number="planResetDay" class="settings-input" type="number" min="1" max="31" /></label>
    <label v-if="planResetMode === 'monthly'"><span>重置时间（上海时区）</span><input v-model="planResetTime" class="settings-input" type="time" /></label>
    <label><span>默认有效天数（留空不限）</span><input v-model="planValidityDays" class="settings-input" type="number" min="1" /></label>
    <label><span>付款周期</span><select v-model.number="planBillingMonths" class="settings-input"><option :value="0">未设置</option><option :value="1">1 个月</option><option :value="3">3 个月</option><option :value="6">6 个月</option><option :value="12">12 个月</option></select></label>
    <n-checkbox v-model:checked="planEnabled">启用套餐</n-checkbox>
    <fieldset class="subscription-node-picker"><legend>包含节点（上下调整订阅顺序）</legend>
      <label v-for="node in orderedPlanNodes" :key="node.id" class="subscription-node-option"><input type="checkbox" :checked="planNodeIDs.includes(node.id)" @change="togglePlanNode(node.id, ($event.target as HTMLInputElement).checked)" /><span>{{ node.name }}</span><template v-if="planNodeIDs.includes(node.id)"><n-button size="tiny" secondary attr-type="button" @click.prevent="movePlanNode(planNodeIDs.indexOf(node.id), -1)">上移</n-button><n-button size="tiny" secondary attr-type="button" @click.prevent="movePlanNode(planNodeIDs.indexOf(node.id), 1)">下移</n-button></template></label>
    </fieldset>
    <div class="modal-actions subscription-form-actions"><n-button @click="planModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy" :disabled="busy">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-if="selectedUser" v-model:show="userModalOpen"><n-card class="client-form-card subscription-form-card" title="订阅用户" closable @close="userModalOpen = false"><form class="auth-form" @submit.prevent="saveUser">
    <strong>{{ selectedUser.username }}</strong>
    <dl class="user-details"><div><dt>状态</dt><dd>{{ statusLabels[selectedUser.status] ?? selectedUser.status }}</dd></div><div><dt>已用 / 总量</dt><dd>{{ formatClientTrafficBytes(selectedUser.used_bytes) }} / {{ selectedUser.traffic_limit_bytes !== null ? formatClientTrafficBytes(selectedUser.traffic_limit_bytes) : '不限' }}</dd></div><div><dt>周期开始</dt><dd>{{ formatTime(selectedUser.cycle_started_at) }}</dd></div><div><dt>下次重置</dt><dd>{{ selectedUser.next_reset_at ? formatTime(selectedUser.next_reset_at) : '不重置' }}</dd></div><div><dt>可用节点</dt><dd>{{ selectedUser.enabled_node_count }}</dd></div><div><dt>密码申请</dt><dd>{{ selectedUser.password_request?.status === 'pending' ? '等待审核' : '无待审核申请' }}</dd></div></dl>
    <label><span>套餐</span><select v-model.number="userPlanID" class="settings-input"><option :value="0">未开通</option><option v-for="plan in enabledPlans" :key="plan.id" :value="plan.id">{{ plan.name }}</option></select></label>
    <label><span>到期时间（留空不限）</span><input v-model="userExpiresAt" class="settings-input" type="datetime-local" /></label>
    <n-checkbox v-model:checked="userEnabled">启用账号</n-checkbox>
    <div v-if="selectedUser.password_request?.status === 'pending'" class="modal-actions"><span>密码修改申请等待审核</span><n-button secondary attr-type="button" @click="reviewUserPassword('reject')">拒绝</n-button><n-button type="primary" attr-type="button" @click="reviewUserPassword('approve')">批准</n-button></div>
    <label><span>Subscription URL</span><n-input :value="selectedUser.subscription_url" readonly /></label>
    <div class="modal-actions"><n-button secondary attr-type="button" @click="copyUserURL">{{ copiedUserURL ? '已复制' : '复制订阅链接' }}</n-button><n-button secondary attr-type="button" @click="regenerateUserToken">重置订阅链接</n-button><n-button secondary attr-type="button" @click="resetUserTraffic">重置流量</n-button></div>
    <div class="modal-actions"><n-button @click="userModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">保存</n-button></div>
  </form></n-card></n-modal>
</template>
