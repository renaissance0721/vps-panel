<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NInputNumber, NModal, NRadio, NRadioGroup, NSpin, NSwitch, NTag } from 'naive-ui'
import { api } from '../api/client'
import RoutingGroupEditor, { type RoutingGroup } from '../components/subscription/RoutingGroupEditor.vue'
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
  source_server_id?: number
  source_server_name?: string
  entry_host_mode: 'inherit' | 'auto' | 'manual'
  entry_host: string
  entry_port_mode: 'inherit' | 'auto' | 'manual'
  entry_address: string
  entry_port: number
  traffic_multiplier: number
  enabled: boolean
  distributable: boolean
  position?: number
}
type RelayServer = { id: number; name: string }
type EntryAddressChoice = 'inherit' | 'auto' | 'existing' | 'manual'
type EntryPortChoice = 'inherit' | 'auto' | 'manual'
type EntryAddressCandidate = { host: string; sources: string[] }
type Plan = {
  id: number
  name: string
  subscription_title: string
  enabled: boolean
  traffic_limit_bytes: number | null
  routing_groups: RoutingGroup[]
  routing_rules: string[]
  template_id: number | null
  nodes: PublishedNode[]
}
type RoutingPreset = { id: number; name: string; enabled: boolean; groups: RoutingGroup[]; rules: string[] }
type SubscriptionTemplate = { id: number; name: string; enabled: boolean; config_yaml: string }
type MihomoConfiguration = {
  name: string
  groups: RoutingGroup[]
  rules: string[]
  rule_providers: string[]
  yaml: string
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
  traffic_reset_mode: 'never' | 'monthly'
  traffic_reset_day: number
  traffic_reset_time: string
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

type Tab = 'users' | 'plans' | 'nodes' | 'configuration'
const currentTab = ref<Tab>('users')
const users = ref<Subscriber[]>([])
const plans = ref<Plan[]>([])
const nodes = ref<PublishedNode[]>([])
const proxies = ref<ProxyRecord[]>([])
const relayServers = ref<RelayServer[]>([])
const routingPresets = ref<RoutingPreset[]>([])
const templates = ref<SubscriptionTemplate[]>([])
const builtinMihomo = ref<MihomoConfiguration | null>(null)
const loading = ref(true)
const busy = ref(false)
const error = ref('')

const nodeModalOpen = ref(false)
const editingNode = ref<PublishedNode | null>(null)
const nodeName = ref('')
const nodeMode = ref<'direct' | 'relay'>('direct')
const nodeTargetProxyID = ref<number | null>(null)
const nodeSourceServerID = ref<number | null>(null)
const nodeEntryAddressMode = ref<EntryAddressChoice>('inherit')
const nodeExistingEntryHost = ref('')
const nodeEntryHost = ref('')
const nodeEntryPortMode = ref<EntryPortChoice>('inherit')
const nodeEntryPort = ref<number | null>(null)
const nodeTrafficMultiplier = ref<number | null>(1)
const nodeEnabled = ref(true)
const nodePlanIDs = ref<number[]>([])
const nodeFormError = ref('')

const planModalOpen = ref(false)
const editingPlan = ref<Plan | null>(null)
const planName = ref('')
const planSubscriptionTitle = ref('')
const planEnabled = ref(true)
const planTrafficGiB = ref('')
const planNodeIDs = ref<number[]>([])
const planRoutingGroups = ref<RoutingGroup[]>([])
const planRoutingRulesText = ref('')
const planRoutingEditorOpen = ref(false)
const planPresetID = ref(0)
const planTemplateID = ref(0)
const planFormError = ref('')

const routingModalOpen = ref(false)
const editingRoutingPreset = ref<RoutingPreset | null>(null)
const routingName = ref('')
const routingEnabled = ref(true)
const routingGroups = ref<RoutingGroup[]>([])
const routingRulesText = ref('')
const routingFormError = ref('')
const templateModalOpen = ref(false)
const editingTemplate = ref<SubscriptionTemplate | null>(null)
const templateName = ref('')
const templateEnabled = ref(true)
const templateYAML = ref('dns:\n  enable: true')
const templateFormError = ref('')
const routingPreviewOpen = ref(false)
const routingPreviewTitle = ref('')
const routingPreviewGroups = ref<RoutingGroup[]>([])
const routingPreviewRules = ref<string[]>([])
const routingPreviewProviders = ref<string[]>([])
const routingPreviewHelp = ref('')
const templatePreviewOpen = ref(false)
const templatePreviewYAML = ref('')

const userModalOpen = ref(false)
const selectedUser = ref<Subscriber | null>(null)
const userPlanID = ref(0)
const userEnabled = ref(true)
const userExpiresAt = ref('')
const userResetMode = ref<'never' | 'monthly'>('never')
const userResetDay = ref(1)
const userResetTime = ref('00:00')
const userBillingMonths = ref(0)
const userFormError = ref('')
const copiedUserURL = ref(false)
const mihomoPreviewOpen = ref(false)
const mihomoPreviewYAML = ref('')
const copiedMihomoPreview = ref(false)

const tabs: { id: Tab; label: string }[] = [
  { id: 'users', label: '订阅用户' },
  { id: 'plans', label: '套餐' },
  { id: 'nodes', label: '发布节点' },
  { id: 'configuration', label: '分流与模板' },
]
const enabledPlans = computed(() => plans.value.filter((plan) => plan.enabled || plan.id === selectedUser.value?.plan_id))
const planUsesTemplateRouting = computed(() => planRoutingGroups.value.length === 0 && routingLines(planRoutingRulesText.value).length === 0)
const planRoutingStatus = computed(() => planUsesTemplateRouting.value
  ? '当前：继承 Mihomo 模板分流'
  : `当前：自定义 · ${planRoutingGroups.value.length} 个分组 · ${routingLines(planRoutingRulesText.value).length} 条规则`)
const orderedPlanNodes = computed(() => {
  const byID = new Map(nodes.value.map((node) => [node.id, node]))
  const selected = planNodeIDs.value.flatMap((id) => {
    const node = byID.get(id)
    return node ? [node] : []
  })
  const selectedIDs = new Set(planNodeIDs.value)
  return [...selected, ...nodes.value.filter((node) => node.distributable && !selectedIDs.has(node.id))]
})
const selectedTargetProxy = computed(() => proxies.value.find((proxy) => proxy.id === nodeTargetProxyID.value))
const nodeEntryAddressServerID = computed(() => nodeMode.value === 'relay'
  ? nodeSourceServerID.value
  : selectedTargetProxy.value?.server_id ?? null)
const nodeExistingAddresses = computed<EntryAddressCandidate[]>(() => {
  const grouped = new Map<string, EntryAddressCandidate>()
  for (const proxy of proxies.value) {
    const host = proxy.entry_host.trim()
    if (proxy.server_id !== nodeEntryAddressServerID.value || proxy.entry_host_mode !== 'manual' || !host) continue
    const key = host.toLowerCase()
    const existing = grouped.get(key)
    if (existing) {
      if (!existing.sources.includes(proxy.name)) existing.sources.push(proxy.name)
    } else {
      grouped.set(key, { host, sources: [proxy.name] })
    }
  }
  return [...grouped.values()]
})
const nodeResolvedEntryHost = computed(() => {
  if (nodeEntryAddressMode.value === 'manual') return nodeEntryHost.value.trim()
  if (nodeEntryAddressMode.value === 'existing') return nodeExistingEntryHost.value
  if (nodeMode.value === 'direct') return selectedTargetProxy.value?.entry_address ?? ''
  return proxies.value.find((proxy) => proxy.server_id === nodeSourceServerID.value)?.server_public_ipv4 ?? ''
})
const nodeResolvedEntryPort = computed(() => {
  if (nodeMode.value === 'direct') return selectedTargetProxy.value?.listen_port ?? null
  if (nodeEntryPortMode.value === 'manual') return nodeEntryPort.value
  if (editingNode.value?.mode === 'relay' && editingNode.value.entry_port_mode === 'auto') return editingNode.value.entry_port
  return null
})
const nodeEntryPreview = computed(() => {
  const rawHost = nodeResolvedEntryHost.value || (nodeMode.value === 'relay' ? '服务器公网地址' : '入口地址未检测')
  const host = rawHost.includes(':') && !rawHost.startsWith('[') ? `[${rawHost}]` : rawHost
  const port = nodeResolvedEntryPort.value ?? '<自动分配端口>'
  return `${host}:${port}`
})

const statusLabels: Record<string, string> = {
  normal: '正常', unconfigured: '未开通套餐', disabled: '已停用', plan_disabled: '套餐已停用',
  expired: '已到期', exhausted: '流量已用完',
}

async function loadAll() {
  const [userResult, planResult, nodeResult, proxyResult, relayServerResult, routingResult, templateResult, builtinResult] = await Promise.all([
    api<{ users: Subscriber[] }>('/api/admin/subscription/users'),
    api<{ plans: Plan[] }>('/api/admin/subscription/plans'),
    api<{ nodes: PublishedNode[] }>('/api/admin/subscription/nodes'),
    api<{ proxies: ProxyRecord[] }>('/api/admin/distributable-proxies'),
    api<{ servers: RelayServer[] }>('/api/admin/subscription/relay-servers'),
    api<{ routing_presets: RoutingPreset[] }>('/api/admin/subscription/routing-presets'),
    api<{ templates: SubscriptionTemplate[] }>('/api/admin/subscription/templates'),
    api<MihomoConfiguration>('/api/admin/subscription/builtin-mihomo'),
  ])
  users.value = userResult.users
  plans.value = planResult.plans
  nodes.value = nodeResult.nodes
  proxies.value = proxyResult.proxies
  relayServers.value = relayServerResult.servers
  routingPresets.value = routingResult.routing_presets
  templates.value = templateResult.templates
  builtinMihomo.value = builtinResult
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
  nodeFormError.value = ''
  editingNode.value = null
  nodeName.value = ''
  nodeMode.value = 'direct'
  nodeTargetProxyID.value = proxies.value[0]?.id ?? null
  nodeSourceServerID.value = null
  resetNodeEndpointForMode()
  nodeTrafficMultiplier.value = 1
  nodeEnabled.value = true
  nodePlanIDs.value = []
  nodeModalOpen.value = true
}

function openEditNode(value: PublishedNode) {
  nodeFormError.value = ''
  editingNode.value = value
  nodeName.value = value.name
  nodeMode.value = value.mode
  nodeTargetProxyID.value = value.target_proxy_id
  nodeSourceServerID.value = value.source_server_id ?? null
  nodeEntryAddressMode.value = value.entry_host_mode
  nodeExistingEntryHost.value = ''
  nodeEntryHost.value = value.entry_host
  nodeEntryPortMode.value = value.entry_port_mode
  nodeEntryPort.value = value.mode === 'relay' ? value.entry_port : null
  nodeTrafficMultiplier.value = value.traffic_multiplier
  nodeEnabled.value = value.enabled
  nodePlanIDs.value = plans.value.filter((plan) => plan.nodes.some((node) => node.id === value.id)).map((plan) => plan.id)
  nodeModalOpen.value = true
}

function resetNodeEndpointForMode() {
  nodeEntryAddressMode.value = nodeMode.value === 'relay' ? 'auto' : 'inherit'
  nodeExistingEntryHost.value = ''
  nodeEntryHost.value = ''
  nodeEntryPortMode.value = nodeMode.value === 'relay' ? 'auto' : 'inherit'
  nodeEntryPort.value = null
}

function resetExistingEntryAddress() {
  nodeExistingEntryHost.value = nodeExistingAddresses.value[0]?.host ?? ''
}

function onNodeEntryAddressModeChange(value: EntryAddressChoice) {
  nodeEntryAddressMode.value = value
  if (value === 'existing') resetExistingEntryAddress()
}

function toggleNodePlan(id: number, checked: boolean) {
  if (checked && !nodePlanIDs.value.includes(id)) nodePlanIDs.value.push(id)
  if (!checked) nodePlanIDs.value = nodePlanIDs.value.filter((value) => value !== id)
}

async function saveNode() {
  nodeFormError.value = ''
  const multiplier = nodeTrafficMultiplier.value
  if (multiplier === null || !Number.isFinite(multiplier) || multiplier < 0.1 || multiplier > 5 ||
    Math.abs(multiplier * 100 - Math.round(multiplier * 100)) > 1e-8) {
    nodeFormError.value = '流量倍率必须为 0.10–5.00，且最多两位小数'
    return
  }
  const entryHost = nodeEntryAddressMode.value === 'existing'
    ? nodeExistingEntryHost.value
    : nodeEntryAddressMode.value === 'manual' ? nodeEntryHost.value.trim() : ''
  if ((nodeEntryAddressMode.value === 'existing' || nodeEntryAddressMode.value === 'manual') && !entryHost) {
    nodeFormError.value = '请选择或填写入口域名 / IP'
    return
  }
  if (nodeMode.value === 'relay' && nodeEntryPortMode.value === 'manual' &&
    (!Number.isInteger(nodeEntryPort.value) || (nodeEntryPort.value ?? 0) < 1 || (nodeEntryPort.value ?? 0) > 65535)) {
    nodeFormError.value = '入口端口必须在 1–65535 之间。'
    return
  }
  const entryHostMode = nodeMode.value === 'direct'
    ? (nodeEntryAddressMode.value === 'inherit' ? 'inherit' : 'manual')
    : (nodeEntryAddressMode.value === 'auto' ? 'auto' : 'manual')
  const entryPortMode = nodeMode.value === 'relay' ? nodeEntryPortMode.value : 'inherit'
  const endpoint = {
    entry_host_mode: entryHostMode,
    entry_host: entryHost,
    entry_port_mode: entryPortMode,
    entry_port: nodeMode.value === 'relay' && nodeEntryPortMode.value === 'manual' ? nodeEntryPort.value : null,
  }
  await run(async () => {
    if (editingNode.value) {
      await api(`/api/admin/subscription/nodes/${editingNode.value.id}`, {
        method: 'PATCH', body: JSON.stringify({
          name: nodeName.value, traffic_multiplier: multiplier, enabled: nodeEnabled.value,
          plan_ids: nodePlanIDs.value, ...endpoint,
        }),
      })
    } else {
      await api('/api/admin/subscription/nodes', {
        method: 'POST',
        body: JSON.stringify({
          name: nodeName.value, mode: nodeMode.value, target_proxy_id: nodeTargetProxyID.value,
          source_server_id: nodeMode.value === 'relay' ? nodeSourceServerID.value : null,
          traffic_multiplier: multiplier, enabled: nodeEnabled.value, plan_ids: nodePlanIDs.value, ...endpoint,
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
  planSubscriptionTitle.value = ''
  planEnabled.value = true
  planTrafficGiB.value = ''
  planNodeIDs.value = []
  planRoutingGroups.value = []
  planRoutingRulesText.value = ''
  planRoutingEditorOpen.value = false
  planPresetID.value = 0
  planTemplateID.value = 0
  planModalOpen.value = true
}

function populatePlanForm(value: Plan) {
  editingPlan.value = value
  planName.value = value.name
  planSubscriptionTitle.value = value.subscription_title
  planEnabled.value = value.enabled
  planTrafficGiB.value = value.traffic_limit_bytes === null ? '' : String(value.traffic_limit_bytes / 1024 ** 3)
  planNodeIDs.value = value.nodes.map((node) => node.id)
  planRoutingGroups.value = cloneRoutingGroups(value.routing_groups)
  planRoutingRulesText.value = value.routing_rules.join('\n')
  planRoutingEditorOpen.value = false
  planPresetID.value = 0
  planTemplateID.value = value.template_id ?? 0
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

function routingLines(value: string) {
  return value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)
}

function cloneRoutingGroups(values: RoutingGroup[]) {
  return values.map((group) => ({ ...group, proxies: [...group.proxies], node_ids: [...(group.node_ids ?? [])] }))
}

function templateGroupsToRoutingGroups(values: RoutingGroup[]) {
  return values.map((group) => ({
    name: group.name,
    type: group.type,
    proxies: group.proxies.filter((proxy) => proxy !== '{{all}}'),
    node_ids: [],
    include_all: group.proxies.includes('{{all}}'),
  }))
}

function stripInjectedProxies(value: string) {
  return value.replace(/^proxies:\s*\[\]\s*\r?\n?/m, '').trim()
}

function openRoutingPreview(title: string, groups: RoutingGroup[], rules: string[], providers: string[], help: string) {
  routingPreviewTitle.value = title
  routingPreviewGroups.value = cloneRoutingGroups(groups)
  routingPreviewRules.value = [...rules]
  routingPreviewProviders.value = [...providers]
  routingPreviewHelp.value = help
  routingPreviewOpen.value = true
}

function viewBuiltinRouting() {
  const value = builtinMihomo.value
  if (!value) return
  openRoutingPreview(
    '内置默认分流', value.groups, value.rules, value.rule_providers,
    '{{all}} 表示生成订阅时展开为当前套餐的全部实际节点。',
  )
}

function copyBuiltinRoutingToPreset() {
  const value = builtinMihomo.value
  if (!value) return
  editingRoutingPreset.value = null
  routingName.value = '内置默认分流 - 副本'
  routingEnabled.value = true
  routingGroups.value = templateGroupsToRoutingGroups(value.groups)
  routingRulesText.value = value.rules.join('\n')
  routingFormError.value = ''
  routingModalOpen.value = true
}

function viewBuiltinTemplate() {
  if (!builtinMihomo.value) return
  templatePreviewYAML.value = builtinMihomo.value.yaml
  templatePreviewOpen.value = true
}

function copyBuiltinTemplate() {
  if (!builtinMihomo.value) return
  editingTemplate.value = null
  templateName.value = '内置默认 Mihomo 模板 - 副本'
  templateEnabled.value = true
  templateYAML.value = stripInjectedProxies(builtinMihomo.value.yaml)
  templateFormError.value = ''
  templateModalOpen.value = true
}

async function loadPlanTemplateConfiguration() {
  const query = planTemplateID.value ? `?template_id=${planTemplateID.value}` : ''
  return api<MihomoConfiguration>(`/api/admin/subscription/mihomo-configuration${query}`)
}

async function viewCurrentPlanRouting() {
  planFormError.value = ''
  if (!planUsesTemplateRouting.value) {
    openRoutingPreview(
      '当前套餐自定义分流', planRoutingGroups.value, routingLines(planRoutingRulesText.value), [],
      '这里展示套餐表单当前保存的策略组与规则。',
    )
    return
  }
  busy.value = true
  try {
    const value = await loadPlanTemplateConfiguration()
    openRoutingPreview(
      `${value.name} · 当前有效分流`, value.groups, value.rules, value.rule_providers,
      '{{all}} 表示生成订阅时展开为当前套餐的全部实际节点。',
    )
  } catch (reason) {
    planFormError.value = reason instanceof Error ? reason.message : '无法读取当前有效分流'
  } finally {
    busy.value = false
  }
}

async function copyCurrentPlanRouting() {
  if (!planUsesTemplateRouting.value) return
  planFormError.value = ''
  busy.value = true
  try {
    const value = await loadPlanTemplateConfiguration()
    if (value.groups.some((group) => group.type !== 'select')) {
      planFormError.value = '当前模板包含非 select 策略组，无法复制为套餐自定义分流'
      return
    }
    planRoutingGroups.value = templateGroupsToRoutingGroups(value.groups)
    planRoutingRulesText.value = value.rules.join('\n')
    planRoutingEditorOpen.value = true
    planPresetID.value = 0
  } catch (reason) {
    planFormError.value = reason instanceof Error ? reason.message : '无法复制当前有效分流'
  } finally {
    busy.value = false
  }
}

function applyRoutingPreset() {
  const preset = routingPresets.value.find((value) => value.id === planPresetID.value)
  if (!preset) return
  planRoutingGroups.value = cloneRoutingGroups(preset.groups)
  planRoutingRulesText.value = preset.rules.join('\n')
  planRoutingEditorOpen.value = true
}

function restoreTemplateRouting() {
  planRoutingGroups.value = []
  planRoutingRulesText.value = ''
  planRoutingEditorOpen.value = false
  planPresetID.value = 0
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
  const routingRules = routingLines(planRoutingRulesText.value)
  if (planRoutingGroups.value.length === 0 && routingRules.length !== 0) {
    planFormError.value = '自定义分流规则必须配合策略组使用'
    return
  }
  const body = {
    name, subscription_title: planSubscriptionTitle.value.trim(),
    enabled: planEnabled.value, traffic_limit_bytes: trafficLimit,
    routing_groups: planRoutingGroups.value,
    routing_rules: routingRules,
    template_id: planTemplateID.value || null,
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

function openCreateRoutingPreset() {
  editingRoutingPreset.value = null
  routingName.value = ''
  routingEnabled.value = true
  routingGroups.value = []
  routingRulesText.value = ''
  routingFormError.value = ''
  routingModalOpen.value = true
}

function openEditRoutingPreset(value: RoutingPreset) {
  editingRoutingPreset.value = value
  routingName.value = value.name
  routingEnabled.value = value.enabled
  routingGroups.value = cloneRoutingGroups(value.groups)
  routingRulesText.value = value.rules.join('\n')
  routingFormError.value = ''
  routingModalOpen.value = true
}

async function saveRoutingPreset() {
  routingFormError.value = ''
  const rules = routingLines(routingRulesText.value)
  if (!routingName.value.trim() || routingGroups.value.length === 0) {
    routingFormError.value = '名称和至少一个策略组不能为空'
    return
  }
  await run(async () => {
    const id = editingRoutingPreset.value?.id
    await api(id ? `/api/admin/subscription/routing-presets/${id}` : '/api/admin/subscription/routing-presets', {
      method: id ? 'PATCH' : 'POST',
      body: JSON.stringify({ name: routingName.value.trim(), enabled: routingEnabled.value, groups: routingGroups.value, rules }),
    })
    routingModalOpen.value = false
    await loadAll()
  })
}

async function deleteRoutingPreset(value: RoutingPreset) {
  if (!window.confirm(`确定删除分流预设“${value.name}”吗？`)) return
  await run(async () => {
    await api(`/api/admin/subscription/routing-presets/${value.id}`, { method: 'DELETE' })
    await loadAll()
  })
}

function openCreateTemplate() {
  editingTemplate.value = null
  templateName.value = ''
  templateEnabled.value = true
  templateYAML.value = 'dns:\n  enable: true'
  templateFormError.value = ''
  templateModalOpen.value = true
}

function openEditTemplate(value: SubscriptionTemplate) {
  editingTemplate.value = value
  templateName.value = value.name
  templateEnabled.value = value.enabled
  templateYAML.value = value.config_yaml
  templateFormError.value = ''
  templateModalOpen.value = true
}

async function saveTemplate() {
  templateFormError.value = ''
  if (!templateName.value.trim() || !templateYAML.value.trim()) {
    templateFormError.value = '名称和 YAML 不能为空'
    return
  }
  await run(async () => {
    const id = editingTemplate.value?.id
    await api(id ? `/api/admin/subscription/templates/${id}` : '/api/admin/subscription/templates', {
      method: id ? 'PATCH' : 'POST',
      body: JSON.stringify({ name: templateName.value.trim(), enabled: templateEnabled.value, config_yaml: templateYAML.value }),
    })
    templateModalOpen.value = false
    await loadAll()
  })
}

async function deleteTemplate(value: SubscriptionTemplate) {
  if (!window.confirm(`确定删除订阅模板“${value.name}”吗？`)) return
  await run(async () => {
    await api(`/api/admin/subscription/templates/${value.id}`, { method: 'DELETE' })
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
    userResetMode.value = response.user.traffic_reset_mode
    userResetDay.value = response.user.traffic_reset_day
    userResetTime.value = response.user.traffic_reset_time
    userBillingMonths.value = response.user.billing_period_months ?? 0
    userFormError.value = ''
    copiedUserURL.value = false
    userModalOpen.value = true
  })
}

async function saveUser() {
  const value = selectedUser.value
  if (!value) return
  userFormError.value = ''
  if (userResetMode.value === 'monthly' && (!Number.isInteger(userResetDay.value) || userResetDay.value < 1 || userResetDay.value > 31)) {
    userFormError.value = '每月重置日期必须是 1–31 的整数'
    return
  }
  if (userResetMode.value === 'monthly' && !/^(?:[01]\d|2[0-3]):[0-5]\d$/.test(userResetTime.value)) {
    userFormError.value = '每月重置时间必须是有效的 HH:mm'
    return
  }
  if (![0, 1, 3, 6, 12].includes(userBillingMonths.value)) {
    userFormError.value = '付款周期仅支持未设置、1、3、6 或 12 个月'
    return
  }
  await run(async () => {
    await api(`/api/admin/subscription/users/${value.user_id}`, {
      method: 'PATCH',
      body: JSON.stringify({
        plan_id: userPlanID.value || null,
        enabled: userEnabled.value,
        expires_at: userExpiresAt.value || null,
        traffic_reset_mode: userResetMode.value,
        traffic_reset_day: userResetDay.value,
        traffic_reset_time: userResetTime.value,
        billing_period_months: userBillingMonths.value || null,
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

async function previewUserMihomo() {
  const value = selectedUser.value
  if (!value) return
  await run(async () => {
    const response = await api<{ yaml: string }>(`/api/admin/subscription/users/${value.user_id}/mihomo-preview`)
    mihomoPreviewYAML.value = response.yaml
    copiedMihomoPreview.value = false
    mihomoPreviewOpen.value = true
  })
}

async function copyMihomoPreview() {
  await navigator.clipboard.writeText(mihomoPreviewYAML.value)
  copiedMihomoPreview.value = true
}

async function reviewUserPassword(action: 'approve' | 'reject') {
  const request = selectedUser.value?.password_request
  if (!request) return
  await run(async () => {
    await api(`/api/admin/password-change-requests/${request.id}/${action}`, { method: 'POST' })
    if (selectedUser.value) selectedUser.value.password_request = undefined
  })
}

function nodeDisplayName(value: PublishedNode) {
  return `${value.name} [${Number(value.traffic_multiplier.toFixed(2))}×]`
}

function nodePlanNames(nodeID: number) {
  return plans.value.filter((plan) => plan.nodes.some((node) => node.id === nodeID)).map((plan) => plan.name).join('、')
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
        <p>{{ value.traffic_limit_bytes !== null ? formatClientTrafficBytes(value.traffic_limit_bytes) : '不限流量' }}</p>
        <p>{{ value.nodes.length }} 个节点</p>
        <div class="modal-actions"><n-button secondary @click="openEditPlan(value)">编辑</n-button><n-button type="error" secondary @click="deletePlan(value)">删除</n-button></div>
      </n-card>
    </div>
  </section>

  <section v-else-if="currentTab === 'nodes'" class="subscription-section">
    <div class="section-heading"><span></span><n-button type="primary" :disabled="proxies.length === 0" @click="openCreateNode">新增发布节点</n-button></div>
    <n-empty v-if="nodes.length === 0" description="暂无发布节点" />
    <div v-else class="user-management-grid">
      <n-card v-for="value in nodes" :key="value.id" :title="nodeDisplayName(value)">
        <template #header-extra><n-tag :type="value.enabled ? 'success' : 'default'">{{ value.enabled ? '启用' : '停用' }}</n-tag></template>
        <p v-if="value.mode === 'direct'">单一节点</p><p v-else>中转 + 落地</p>
        <p v-if="value.mode === 'relay'">{{ value.source_server_name }} · Realm → {{ value.target_server_name }} · {{ value.target_proxy_name }}</p>
        <p v-else>{{ value.target_server_name }} · {{ value.target_proxy_name }}</p>
        <p>入口：{{ value.entry_address }}:{{ value.entry_port }}</p>
        <p>所属套餐：{{ nodePlanNames(value.id) || '未加入套餐' }}</p>
        <div class="modal-actions"><n-button secondary @click="openEditNode(value)">编辑</n-button><n-button type="error" secondary @click="deleteNode(value)">删除</n-button></div>
      </n-card>
    </div>
  </section>

  <section v-else class="subscription-section configuration-grid">
    <p class="form-help">Mihomo 模板决定完整客户端配置；分流预设只保存策略组与规则，用于快速套用到套餐。</p>
    <n-card title="分流预设" :bordered="true">
      <p class="form-help">预设是可复用的策略组和规则方案；套用后复制到套餐，不与套餐长期绑定。</p>
      <n-button type="primary" @click="openCreateRoutingPreset">新增分流预设</n-button>
      <div class="configuration-list">
        <div class="invitation-row builtin-configuration-row">
          <div><strong>内置默认分流 <n-tag type="info" size="small">内置</n-tag></strong><span>{{ builtinMihomo?.groups.length ?? 0 }} 个分组 · {{ builtinMihomo?.rules.length ?? 0 }} 条规则</span></div>
          <div class="modal-actions"><n-button secondary size="small" @click="viewBuiltinRouting">查看</n-button><n-button secondary size="small" @click="copyBuiltinRoutingToPreset">复制为预设</n-button></div>
        </div>
        <div v-for="value in routingPresets" :key="value.id" class="invitation-row">
          <div><strong>{{ value.name }} <n-tag :type="value.enabled ? 'success' : 'default'" size="small">{{ value.enabled ? '已启用' : '已停用' }}</n-tag></strong><span>{{ value.groups.length }} 个分组 · {{ value.rules.length }} 条规则</span></div>
          <div class="modal-actions"><n-button secondary size="small" @click="openEditRoutingPreset(value)">编辑</n-button><n-button type="error" secondary size="small" @click="deleteRoutingPreset(value)">删除</n-button></div>
        </div>
      </div>
    </n-card>
    <n-card title="Mihomo 模板" :bordered="true">
      <p class="form-help">模板是完整 Mihomo 配置底稿，负责 DNS、TUN、sniffer、profile、rule-providers 和默认分流；真实 proxies 由 Panel 动态注入。</p>
      <n-button type="primary" @click="openCreateTemplate">新增订阅模板</n-button>
      <div class="configuration-list">
        <div class="invitation-row builtin-configuration-row">
          <div><strong>内置默认 Mihomo 模板 <n-tag type="info" size="small">内置</n-tag></strong><span>完整客户端配置底稿</span></div>
          <div class="modal-actions"><n-button secondary size="small" @click="viewBuiltinTemplate">查看</n-button><n-button secondary size="small" @click="copyBuiltinTemplate">复制为自定义模板</n-button></div>
        </div>
        <div v-for="value in templates" :key="value.id" class="invitation-row">
          <div><strong>{{ value.name }} <n-tag :type="value.enabled ? 'success' : 'default'" size="small">{{ value.enabled ? '已启用' : '已停用' }}</n-tag></strong></div>
          <div class="modal-actions"><n-button secondary size="small" @click="openEditTemplate(value)">编辑</n-button><n-button type="error" secondary size="small" @click="deleteTemplate(value)">删除</n-button></div>
        </div>
      </div>
    </n-card>
  </section>

  <n-modal v-model:show="routingPreviewOpen"><n-card class="client-form-card subscription-form-card" :title="routingPreviewTitle" closable @close="routingPreviewOpen = false">
    <p class="form-help">{{ routingPreviewHelp }}</p>
    <h3>策略组</h3>
    <RoutingGroupEditor :model-value="routingPreviewGroups" :nodes="nodes.map((node) => ({ id: node.id, name: nodeDisplayName(node) }))" readonly />
    <label><span>Rules</span><n-input :value="routingPreviewRules.join('\n')" type="textarea" readonly :autosize="{ minRows: 8, maxRows: 18 }" /></label>
    <div v-if="routingPreviewProviders.length"><strong>Rule Providers</strong><p class="form-help">{{ routingPreviewProviders.join(' / ') }}</p></div>
    <div class="modal-actions"><n-button @click="routingPreviewOpen = false">关闭</n-button></div>
  </n-card></n-modal>

  <n-modal v-model:show="templatePreviewOpen"><n-card class="client-form-card subscription-form-card" title="内置默认 Mihomo 模板" closable @close="templatePreviewOpen = false">
    <n-alert type="info">真实 proxies 会在生成订阅时由 Panel 动态注入；&#123;&#123;all&#125;&#125; 会展开为当前套餐实际节点。</n-alert>
    <n-input :value="templatePreviewYAML" type="textarea" readonly :autosize="{ minRows: 18, maxRows: 28 }" />
    <div class="modal-actions"><n-button @click="templatePreviewOpen = false">关闭</n-button></div>
  </n-card></n-modal>

  <n-modal v-model:show="routingModalOpen"><n-card class="client-form-card subscription-form-card" :title="editingRoutingPreset ? '编辑分流预设' : '新增分流预设'" closable @close="routingModalOpen = false"><form class="auth-form" @submit.prevent="saveRoutingPreset">
    <n-alert v-if="routingFormError" type="error" closable @close="routingFormError = ''">{{ routingFormError }}</n-alert>
    <label><span>名称</span><n-input v-model:value="routingName" maxlength="100" /></label>
    <div class="switch-row"><span>启用预设</span><n-switch v-model:value="routingEnabled" /></div>
    <RoutingGroupEditor v-model="routingGroups" :nodes="nodes.map((node) => ({ id: node.id, name: nodeDisplayName(node) }))" />
    <label><span>Rules（一行一条 Mihomo rule）</span><n-input v-model:value="routingRulesText" type="textarea" placeholder="RULE-SET,OpenAI,🤖 AI&#10;GEOIP,CN,DIRECT,no-resolve&#10;MATCH,🚀 默认代理" :autosize="{ minRows: 6, maxRows: 16 }" /></label>
    <div class="modal-actions"><n-button @click="routingModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-model:show="templateModalOpen"><n-card class="client-form-card subscription-form-card" :title="editingTemplate ? '编辑 Mihomo 模板' : '新增 Mihomo 模板'" closable @close="templateModalOpen = false"><form class="auth-form" @submit.prevent="saveTemplate">
    <n-alert v-if="templateFormError" type="error" closable @close="templateFormError = ''">{{ templateFormError }}</n-alert>
    <label><span>名称</span><n-input v-model:value="templateName" maxlength="100" /></label>
    <div class="switch-row"><span>启用模板</span><n-switch v-model:value="templateEnabled" /></div>
    <label><span>完整 Mihomo YAML</span><n-input v-model:value="templateYAML" type="textarea" :autosize="{ minRows: 12, maxRows: 24 }" /></label>
    <div class="modal-actions"><n-button @click="templateModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-model:show="nodeModalOpen"><n-card class="client-form-card" :title="editingNode ? '编辑发布节点' : '新增发布节点'" closable @close="nodeModalOpen = false"><form class="auth-form" @submit.prevent="saveNode">
    <n-alert v-if="nodeFormError" type="error" closable @close="nodeFormError = ''">{{ nodeFormError }}</n-alert>
    <label><span>发布名称</span><n-input v-model:value="nodeName" maxlength="100" /></label>
    <template v-if="!editingNode">
      <label><span>模式</span><select v-model="nodeMode" class="settings-input" @change="resetNodeEndpointForMode"><option value="direct">单一节点</option><option value="relay">中转 + 落地</option></select></label>
      <label v-if="nodeMode === 'relay'"><span>中转服务器</span><select v-model.number="nodeSourceServerID" class="settings-input" @change="resetExistingEntryAddress"><option :value="null">请选择</option><option v-for="server in relayServers" :key="server.id" :value="server.id">{{ server.name }}</option></select></label>
      <label><span>落地 Proxy</span><select v-model.number="nodeTargetProxyID" class="settings-input" @change="resetExistingEntryAddress"><option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">{{ proxy.server_name }} · {{ proxy.name }}</option></select></label>
    </template>
    <n-alert v-else type="info">创建后不能修改模式、中转服务器或落地 Proxy；如需改变拓扑，请删除后重新创建。</n-alert>
    <fieldset class="relay-mode-field"><legend>入口地址</legend><n-radio-group :value="nodeEntryAddressMode" @update:value="onNodeEntryAddressModeChange"><div class="relay-mode-options">
      <n-radio v-if="nodeMode === 'direct'" value="inherit">继承落地节点</n-radio>
      <n-radio v-else value="auto">自动</n-radio>
      <n-radio value="existing" :disabled="nodeExistingAddresses.length === 0">已有地址</n-radio>
      <n-radio value="manual">自定义</n-radio>
    </div></n-radio-group></fieldset>
    <label v-if="nodeEntryAddressMode === 'existing'"><span>已有入口地址</span><select v-model="nodeExistingEntryHost" class="settings-input"><option v-for="candidate in nodeExistingAddresses" :key="candidate.host" :value="candidate.host">{{ candidate.host }} · 来自：{{ candidate.sources.join('、') }}</option></select><small class="form-help">保存时只复制地址文本，不建立对来源 Proxy 的依赖。</small></label>
    <label v-if="nodeEntryAddressMode === 'manual'"><span>入口域名 / IP</span><n-input v-model:value="nodeEntryHost" placeholder="example.com 或 1.2.3.4" /><small class="form-help">不能包含协议、路径或端口。</small></label>
    <small v-if="nodeMode === 'direct' && nodeEntryAddressMode === 'inherit'" class="form-help">当前入口：{{ selectedTargetProxy?.entry_address || '尚未检测' }}:{{ selectedTargetProxy?.listen_port }}</small>
    <fieldset v-if="nodeMode === 'relay'" class="relay-mode-field"><legend>入口端口</legend><n-radio-group v-model:value="nodeEntryPortMode"><div class="relay-mode-options"><n-radio value="auto">自动分配</n-radio><n-radio value="manual">自定义</n-radio></div></n-radio-group></fieldset>
    <label v-if="nodeMode === 'relay' && nodeEntryPortMode === 'manual'"><span>端口</span><n-input-number v-model:value="nodeEntryPort" :min="1" :max="65535" :precision="0" /></label>
    <label><span>最终入口</span><n-input :value="nodeEntryPreview" readonly /><small v-if="nodeMode === 'relay' && nodeEntryPortMode === 'auto' && !nodeResolvedEntryPort" class="form-help">保存后自动分配 Realm 端口，不会为了预览提前占用。</small></label>
    <label><span>流量倍率</span><n-input-number v-model:value="nodeTrafficMultiplier" :min="0.1" :max="5" :step="0.1" :precision="2"><template #suffix>×</template></n-input-number><small class="form-help">实际使用 1 GB 时，按该倍率计入套餐流量。允许 0.10×–5.00×。</small></label>
    <fieldset class="subscription-node-picker"><legend>{{ editingNode ? '所属套餐' : '加入套餐' }}</legend>
      <span v-if="plans.length === 0" class="form-help">暂无套餐，可先创建备用发布节点。</span>
      <label v-for="plan in plans" :key="plan.id" class="subscription-node-option"><input type="checkbox" :checked="nodePlanIDs.includes(plan.id)" @change="toggleNodePlan(plan.id, ($event.target as HTMLInputElement).checked)" /><span>{{ plan.name }}</span></label>
    </fieldset>
    <div class="switch-row"><span>启用发布节点</span><n-switch v-model:value="nodeEnabled" /></div>
    <div class="modal-actions"><n-button @click="nodeModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy" :disabled="!nodeTargetProxyID || (nodeMode === 'relay' && !nodeSourceServerID)">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-model:show="planModalOpen"><n-card class="client-form-card subscription-form-card" :title="editingPlan ? '编辑套餐' : '新增套餐'" closable @close="planModalOpen = false"><form class="auth-form" novalidate @submit.prevent="savePlan">
    <n-alert v-if="planFormError" type="error" closable @close="planFormError = ''">{{ planFormError }}</n-alert>
    <label><span>套餐名称</span><n-input v-model:value="planName" maxlength="100" /></label>
    <label><span>订阅显示名称</span><n-input v-model:value="planSubscriptionTitle" maxlength="100" /><small class="form-help">客户端导入订阅后显示的名称。留空则使用套餐名称。</small></label>
    <label><span>流量额度（GiB，留空不限）</span><input v-model="planTrafficGiB" class="settings-input" type="number" min="0" step="any" /></label>
    <label><span>Mihomo 模板</span><select v-model.number="planTemplateID" class="settings-input"><option :value="0">内置默认 Mihomo 模板</option><option v-for="value in templates" :key="value.id" :value="value.id">{{ value.name }}</option></select><small class="form-help">完整配置底稿中的 proxies 会由 Panel 动态注入。</small></label>
    <fieldset class="subscription-node-picker"><legend>分流配置</legend>
      <strong>{{ planRoutingStatus }}</strong>
      <div class="routing-preset-apply"><select v-model.number="planPresetID" class="settings-input"><option :value="0">选择分组与规则预设</option><option v-for="value in routingPresets.filter((preset) => preset.enabled)" :key="value.id" :value="value.id">{{ value.name }}</option></select><n-button secondary attr-type="button" :disabled="!planPresetID" @click="applyRoutingPreset">套用预设</n-button></div>
      <div class="modal-actions">
        <n-button secondary attr-type="button" :loading="busy" @click="viewCurrentPlanRouting">查看当前有效分流</n-button>
        <n-button v-if="planUsesTemplateRouting" secondary attr-type="button" :loading="busy" @click="copyCurrentPlanRouting">复制为自定义分流</n-button>
        <n-button v-else secondary attr-type="button" @click="planRoutingEditorOpen = !planRoutingEditorOpen">编辑当前分流</n-button>
        <n-button v-if="!planUsesTemplateRouting" secondary attr-type="button" @click="restoreTemplateRouting">恢复模板分流</n-button>
      </div>
      <div v-if="planRoutingEditorOpen" class="routing-editor-panel"><RoutingGroupEditor v-model="planRoutingGroups" :nodes="nodes.map((node) => ({ id: node.id, name: nodeDisplayName(node) }))" /><label><span>Rules（一行一条 Mihomo rule）</span><n-input v-model:value="planRoutingRulesText" type="textarea" placeholder="RULE-SET,OpenAI,🤖 AI&#10;GEOIP,CN,DIRECT,no-resolve&#10;MATCH,🚀 默认代理" :autosize="{ minRows: 6, maxRows: 16 }" /></label></div>
    </fieldset>
    <div class="switch-row"><span>启用套餐</span><n-switch v-model:value="planEnabled" /></div>
    <fieldset class="subscription-node-picker"><legend>包含节点（上下调整订阅顺序）</legend>
      <label v-for="node in orderedPlanNodes" :key="node.id" class="subscription-node-option"><input type="checkbox" :checked="planNodeIDs.includes(node.id)" @change="togglePlanNode(node.id, ($event.target as HTMLInputElement).checked)" /><span>{{ nodeDisplayName(node) }}</span><template v-if="planNodeIDs.includes(node.id)"><n-button size="tiny" secondary attr-type="button" @click.prevent="movePlanNode(planNodeIDs.indexOf(node.id), -1)">上移</n-button><n-button size="tiny" secondary attr-type="button" @click.prevent="movePlanNode(planNodeIDs.indexOf(node.id), 1)">下移</n-button></template></label>
    </fieldset>
    <div class="modal-actions subscription-form-actions"><n-button @click="planModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy" :disabled="busy">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-if="selectedUser" v-model:show="userModalOpen"><n-card class="client-form-card subscription-form-card" title="订阅用户" closable @close="userModalOpen = false"><form class="auth-form" @submit.prevent="saveUser">
    <n-alert v-if="userFormError" type="error" closable @close="userFormError = ''">{{ userFormError }}</n-alert>
    <strong>{{ selectedUser.username }}</strong>
    <dl class="user-details"><div><dt>状态</dt><dd>{{ statusLabels[selectedUser.status] ?? selectedUser.status }}</dd></div><div><dt>已用 / 总量</dt><dd>{{ formatClientTrafficBytes(selectedUser.used_bytes) }} / {{ selectedUser.traffic_limit_bytes !== null ? formatClientTrafficBytes(selectedUser.traffic_limit_bytes) : '不限' }}</dd></div><div><dt>周期开始</dt><dd>{{ formatTime(selectedUser.cycle_started_at) }}</dd></div><div><dt>下次重置</dt><dd>{{ selectedUser.next_reset_at ? formatTime(selectedUser.next_reset_at) : '不重置' }}</dd></div><div><dt>可用节点</dt><dd>{{ selectedUser.enabled_node_count }}</dd></div><div><dt>密码重置申请</dt><dd>{{ selectedUser.password_request?.status === 'pending' ? '等待审核' : '无待审核申请' }}</dd></div></dl>
    <label><span>套餐</span><select v-model.number="userPlanID" class="settings-input"><option :value="0">未开通</option><option v-for="plan in enabledPlans" :key="plan.id" :value="plan.id">{{ plan.name }}</option></select></label>
    <label><span>到期时间（留空不限）</span><input v-model="userExpiresAt" class="settings-input" type="datetime-local" /></label>
    <label><span>流量重置</span><select v-model="userResetMode" class="settings-input"><option value="monthly">每月</option><option value="never">不重置</option></select></label>
    <label v-if="userResetMode === 'monthly'"><span>重置日期</span><input v-model.number="userResetDay" class="settings-input" type="number" min="1" max="31" /></label>
    <label v-if="userResetMode === 'monthly'"><span>重置时间（上海时区）</span><input v-model="userResetTime" class="settings-input" type="time" /></label>
    <label><span>付款周期</span><select v-model.number="userBillingMonths" class="settings-input"><option :value="0">未设置</option><option :value="1">月付</option><option :value="3">季付</option><option :value="6">半年付</option><option :value="12">年付</option></select></label>
    <div class="switch-row"><span>启用账号</span><n-switch v-model:value="userEnabled" /></div>
    <div v-if="selectedUser.password_request?.status === 'pending'" class="modal-actions"><span>密码重置申请等待审核</span><n-button secondary attr-type="button" @click="reviewUserPassword('reject')">拒绝</n-button><n-button type="primary" attr-type="button" @click="reviewUserPassword('approve')">批准</n-button></div>
    <label><span>Subscription URL</span><n-input :value="selectedUser.subscription_url" readonly /></label>
    <div class="modal-actions"><n-button secondary attr-type="button" @click="copyUserURL">{{ copiedUserURL ? '已复制' : '复制订阅链接' }}</n-button><n-button secondary attr-type="button" @click="previewUserMihomo">预览 Mihomo</n-button><n-button secondary attr-type="button" @click="regenerateUserToken">重置订阅链接</n-button><n-button secondary attr-type="button" @click="resetUserTraffic">重置流量</n-button></div>
    <div class="modal-actions"><n-button @click="userModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-model:show="mihomoPreviewOpen"><n-card class="client-form-card subscription-form-card" title="Mihomo 最终配置" closable @close="mihomoPreviewOpen = false">
    <n-input :value="mihomoPreviewYAML" type="textarea" readonly :autosize="{ minRows: 18, maxRows: 28 }" />
    <div class="modal-actions"><n-button secondary @click="copyMihomoPreview">{{ copiedMihomoPreview ? '已复制' : '复制 YAML' }}</n-button><n-button @click="mihomoPreviewOpen = false">关闭</n-button></div>
  </n-card></n-modal>
</template>
