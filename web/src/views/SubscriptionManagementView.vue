<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NInputNumber, NModal, NRadio, NRadioGroup, NSpin, NSwitch, NTag } from 'naive-ui'
import { api } from '../api/client'
import RoutingBindingEditor, { type RoutingBindings } from '../components/subscription/RoutingBindingEditor.vue'
import RoutingGroupEditor, { type RoutingGroup } from '../components/subscription/RoutingGroupEditor.vue'
import QRCodeModal from '../components/share/QRCodeModal.vue'
import { beginDragPreview, endDragPreview } from '../drag'
import { formatTime } from '../format'
import { formatClientExpirationInput, formatClientTrafficBytes } from '../proxy'
import type { ProxyRecord } from '../types/proxy'

const props = defineProps<{ role?: 'admin' | 'vip' }>()

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
  routing_preset_id: number
  template_id: number | null
  nodes: PublishedNode[]
  routing_bindings: RoutingBindings
}
type RoutingPreset = {
  id: number
  name: string
  enabled: boolean
  is_default: boolean
  groups: RoutingGroup[]
  rule_providers: RoutingRuleProvider[]
  rules: string[]
}
type RoutingRuleProvider = {
  name: string
  url: string
  type: 'http'
  behavior: string
  format: string
  interval: number
}
type SubscriptionTemplate = { id: number; name: string; enabled: boolean; config_yaml: string }
type MihomoConfiguration = {
  name: string
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

type PersonalNode = {
  id: number
  source_type: 'proxy' | 'relay' | 'landing'
  source_id: number
  source_name: string
  source_detail: string
  display_name: string
  enabled: boolean
  position: number
  entry_host: string | null
  entry_port: number | null
  requires_client: boolean
  status: string
  status_detail: string
}
type PersonalSubscription = {
  id: number
  name: string
  subscription_title: string
  enabled: boolean
  client_name: string
  routing_preset_id: number
  routing_preset_name: string
  mihomo_template_id: number | null
  mihomo_template_name: string
  nodes: PersonalNode[]
  routing_bindings: RoutingBindings
  subscription_base64_url: string
  subscription_mihomo_url: string
  subscription_auto_url: string
}
type PersonalSource = {
  source_type: 'proxy' | 'relay' | 'landing'
  source_id: number
  name: string
  detail: string
  default_name: string
  status: string
  status_detail: string
  requires_client: boolean
}

type Tab = 'personal' | 'users' | 'plans' | 'nodes' | 'configuration'
const currentTab = ref<Tab>('personal')
const personalSubscriptions = ref<PersonalSubscription[]>([])
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
const planRoutingBindings = ref<RoutingBindings>({})
const planRoutingPresetID = ref(0)
const planTemplateID = ref(0)
const planFormError = ref('')

const routingModalOpen = ref(false)
const editingRoutingPreset = ref<RoutingPreset | null>(null)
const routingName = ref('')
const routingEnabled = ref(true)
const routingGroups = ref<RoutingGroup[]>([])
const routingProviders = ref<RoutingRuleProvider[]>([])
const routingRulesText = ref('')
const routingFormError = ref('')
const routingGroupsExpanded = ref(true)
const routingProvidersExpanded = ref(true)
const routingRulesExpanded = ref(true)
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
const routingPreviewProviders = ref<RoutingRuleProvider[]>([])
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

const personalModalOpen = ref(false)
const editingPersonal = ref<PersonalSubscription | null>(null)
const personalName = ref('')
const personalTitle = ref('')
const personalEnabled = ref(true)
const personalClientName = ref('')
const personalRoutingPresetID = ref(0)
const personalMihomoTemplateID = ref(0)
const personalNodes = ref<PersonalNode[]>([])
const personalRoutingBindings = ref<RoutingBindings>({})
const personalSources = ref<PersonalSource[]>([])
const personalSourcesLoading = ref(false)
const personalFormError = ref('')
const personalCopiedID = ref<number | null>(null)
const personalPreviewOpen = ref(false)
const personalPreviewYAML = ref('')
const personalQR = ref<PersonalSubscription | null>(null)
const personalQROpen = ref(false)
const personalSourceTypes: PersonalSource['source_type'][] = ['proxy', 'relay', 'landing']
const personalSourceAdderExpanded = ref(false)
const personalSourceSearch = ref('')
const personalBindingEditorKey = ref(0)
const planBindingEditorKey = ref(0)
const expandedPersonalNodeIDs = ref<Set<number>>(new Set())
const draggedPersonalNodeID = ref<number | null>(null)
const personalDropTargetID = ref<number | null>(null)
let nextPersonalNodeID = -1

const tabs = computed<{ id: Tab; label: string }[]>(() => props.role === 'vip'
  ? [{ id: 'personal', label: '个人订阅' }]
  : [
      { id: 'personal', label: '个人订阅' },
      { id: 'users', label: '订阅用户' },
      { id: 'plans', label: '共享订阅' },
      { id: 'nodes', label: '发布节点' },
      { id: 'configuration', label: '分流模板' },
    ])
const enabledPlans = computed(() => plans.value.filter((plan) => plan.enabled || plan.id === selectedUser.value?.plan_id))
const defaultRoutingPreset = computed(() => routingPresets.value.find((value) => value.is_default))
const selectableRoutingPresets = computed(() => routingPresets.value.filter((value) =>
  value.enabled || value.id === editingPlan.value?.routing_preset_id,
))
const selectablePersonalRoutingPresets = computed(() => routingPresets.value.filter((value) =>
  value.enabled || value.id === editingPersonal.value?.routing_preset_id,
))
const selectablePersonalTemplates = computed(() => templates.value.filter((value) =>
  value.enabled || value.id === editingPersonal.value?.mihomo_template_id,
))
const selectedPlanRoutingGroups = computed(() => routingPresets.value.find((value) => value.id === planRoutingPresetID.value)?.groups ?? [])
const selectedPersonalRoutingGroups = computed(() => routingPresets.value.find((value) => value.id === personalRoutingPresetID.value)?.groups ?? [])
const filteredPersonalSources = computed(() => {
  const query = personalSourceSearch.value.trim().toLocaleLowerCase()
  if (!query) return personalSources.value
  return personalSources.value.filter((source) => `${source.name}\n${source.detail}`.toLocaleLowerCase().includes(query))
})
const routingRuleLines = computed({
  get: () => routingLines(routingRulesText.value),
  set: (value: string[]) => { routingRulesText.value = value.join('\n') },
})
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
  normal: '正常', unconfigured: '未开通共享订阅', disabled: '已停用', plan_disabled: '共享订阅已停用',
  expired: '已到期', exhausted: '流量已用完',
}

async function loadAll() {
  const [personalResult, routingResult, templateResult] = await Promise.all([
    api<{ personal_subscriptions: PersonalSubscription[] }>('/api/personal-subscriptions'),
    api<{ routing_presets: RoutingPreset[] }>('/api/admin/subscription/routing-presets'),
    api<{ templates: SubscriptionTemplate[] }>('/api/admin/subscription/templates'),
  ])
  personalSubscriptions.value = personalResult.personal_subscriptions
  routingPresets.value = routingResult.routing_presets
  templates.value = templateResult.templates
  if (props.role === 'admin') {
    const [userResult, planResult, nodeResult, proxyResult, relayServerResult, builtinResult] = await Promise.all([
      api<{ users: Subscriber[] }>('/api/admin/subscription/users'),
      api<{ plans: Plan[] }>('/api/admin/subscription/plans'),
      api<{ nodes: PublishedNode[] }>('/api/admin/subscription/nodes'),
      api<{ proxies: ProxyRecord[] }>('/api/admin/distributable-proxies'),
      api<{ servers: RelayServer[] }>('/api/admin/subscription/relay-servers'),
      api<MihomoConfiguration>('/api/admin/subscription/builtin-mihomo'),
    ])
    users.value = userResult.users
    plans.value = planResult.plans
    nodes.value = nodeResult.nodes
    proxies.value = proxyResult.proxies
    relayServers.value = relayServerResult.servers
    builtinMihomo.value = builtinResult
  }
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

function clonePersonalNodes(values: PersonalNode[]) {
  return values.map((value) => ({ ...value }))
}

function cloneRoutingBindings(value: RoutingBindings) {
  return Object.fromEntries(Object.entries(value ?? {}).map(([key, ids]) => [key, [...ids]]))
}

function normalizeRoutingBindings(groups: RoutingGroup[], bindings: RoutingBindings, allowedIDs: number[]) {
  const allowed = new Set(allowedIDs)
  return Object.fromEntries(groups.filter((group) => group.key).map((group) => [
    group.key,
    (bindings[group.key] ?? []).filter((id, index, ids) => allowed.has(id) && ids.indexOf(id) === index),
  ]))
}

function pruneRoutingBindingNode(bindings: RoutingBindings, nodeID: number) {
  return Object.fromEntries(Object.entries(bindings).map(([key, ids]) => [key, ids.filter((id) => id !== nodeID)]))
}

function resetPersonalEditorUI() {
  personalSourceAdderExpanded.value = false
  personalSourceSearch.value = ''
  expandedPersonalNodeIDs.value = new Set()
  personalBindingEditorKey.value++
}

function openCreatePersonal() {
  editingPersonal.value = null
  personalName.value = ''
  personalTitle.value = ''
  personalEnabled.value = true
  personalClientName.value = ''
  personalRoutingPresetID.value = defaultRoutingPreset.value?.id ?? routingPresets.value.find((value) => value.enabled)?.id ?? 0
  personalMihomoTemplateID.value = 0
  personalNodes.value = []
  personalRoutingBindings.value = {}
  personalSources.value = []
  resetPersonalEditorUI()
  personalFormError.value = ''
  personalModalOpen.value = true
}

async function openEditPersonal(value: PersonalSubscription) {
  await run(async () => {
    const response = await api<{ personal_subscription: PersonalSubscription }>(`/api/personal-subscriptions/${value.id}`)
    const current = response.personal_subscription
    editingPersonal.value = current
    personalName.value = current.name
    personalTitle.value = current.subscription_title
    personalEnabled.value = current.enabled
    personalClientName.value = current.client_name
    personalRoutingPresetID.value = current.routing_preset_id
    personalMihomoTemplateID.value = current.mihomo_template_id ?? 0
    personalNodes.value = clonePersonalNodes(current.nodes)
    personalRoutingBindings.value = cloneRoutingBindings(current.routing_bindings)
    resetPersonalEditorUI()
    personalFormError.value = ''
    personalModalOpen.value = true
    await loadPersonalSources()
  })
}

async function togglePersonalSourceAdder() {
  personalSourceAdderExpanded.value = !personalSourceAdderExpanded.value
  if (personalSourceAdderExpanded.value && personalSources.value.length === 0 && personalClientName.value.trim()) {
    await loadPersonalSources()
  }
}

async function loadPersonalSources() {
  const clientName = personalClientName.value.trim()
  if (!clientName) {
    personalSources.value = []
    personalFormError.value = '请先填写同名 Client'
    return
  }
  personalSourcesLoading.value = true
  personalFormError.value = ''
  try {
    const response = await api<{ sources: PersonalSource[] }>(`/api/personal-subscriptions/sources?client_name=${encodeURIComponent(clientName)}`)
    personalSources.value = response.sources
    const bySource = new Map(response.sources.map((source) => [`${source.source_type}:${source.source_id}`, source]))
    personalNodes.value = personalNodes.value.map((node) => {
      const source = bySource.get(`${node.source_type}:${node.source_id}`)
      return source ? {
        ...node, source_name: source.name, source_detail: source.detail,
        status: source.status, status_detail: source.status_detail, requires_client: source.requires_client,
      } : node
    })
  } catch (reason) {
    personalFormError.value = reason instanceof Error ? reason.message : '加载节点来源失败'
  } finally {
    personalSourcesLoading.value = false
  }
}

function addPersonalSource(source: PersonalSource) {
  const id = nextPersonalNodeID--
  const usedNames = new Set(personalNodes.value.map((node) => node.display_name.trim()))
  let displayName = source.default_name
  for (let copy = 2; usedNames.has(displayName); copy++) displayName = `${source.default_name} ${copy}`
  personalNodes.value.push({
    id, source_type: source.source_type, source_id: source.source_id,
    source_name: source.name, source_detail: source.detail, display_name: displayName,
    enabled: true, position: personalNodes.value.length + 1,
    entry_host: null, entry_port: null, requires_client: source.requires_client,
    status: source.status, status_detail: source.status_detail,
  })
}

async function addAllPersonalSources() {
  if (personalSources.value.length === 0) await loadPersonalSources()
  for (const source of personalSources.value) addPersonalSource(source)
}

function removePersonalNode(index: number) {
  const id = personalNodes.value[index]?.id
  personalNodes.value.splice(index, 1)
  personalNodes.value.forEach((node, position) => { node.position = position + 1 })
  if (id !== undefined) {
    personalRoutingBindings.value = pruneRoutingBindingNode(personalRoutingBindings.value, id)
    const expanded = new Set(expandedPersonalNodeIDs.value)
    expanded.delete(id)
    expandedPersonalNodeIDs.value = expanded
  }
}

function togglePersonalNodeDetails(id: number) {
  const expanded = new Set(expandedPersonalNodeIDs.value)
  if (expanded.has(id)) expanded.delete(id)
  else expanded.add(id)
  expandedPersonalNodeIDs.value = expanded
}

function setPersonalEntryHost(node: PersonalNode, value: string) {
  node.entry_host = value.trim() || null
}

function startPersonalNodeDrag(event: DragEvent, id: number) {
  const source = (event.currentTarget as HTMLElement | null)?.closest('.personal-node-item') as HTMLElement | null
  if (!source || !beginDragPreview(event, source, String(id))) return
  draggedPersonalNodeID.value = id
}

function endPersonalNodeDrag() {
  endDragPreview()
  draggedPersonalNodeID.value = null
  personalDropTargetID.value = null
}

function dragOverPersonalNode(event: DragEvent, id: number) {
  if (draggedPersonalNodeID.value === null || draggedPersonalNodeID.value === id) return
  event.preventDefault()
  personalDropTargetID.value = id
}

function dropPersonalNode(id: number) {
  const sourceID = draggedPersonalNodeID.value
  endPersonalNodeDrag()
  if (sourceID === null || sourceID === id) return
  const oldIndex = personalNodes.value.findIndex((node) => node.id === sourceID)
  const newIndex = personalNodes.value.findIndex((node) => node.id === id)
  if (oldIndex < 0 || newIndex < 0) return
  const values = [...personalNodes.value]
  const [node] = values.splice(oldIndex, 1)
  values.splice(newIndex, 0, node)
  values.forEach((value, position) => { value.position = position + 1 })
  personalNodes.value = values
}

async function savePersonal() {
  if (busy.value) return
  personalFormError.value = ''
  if (!personalName.value.trim() || !personalClientName.value.trim()) {
    personalFormError.value = '名称和同名 Client 不能为空'
    return
  }
  if (!personalRoutingPresetID.value) {
    personalFormError.value = '请选择分流方案'
    return
  }
  const names = personalNodes.value.map((node) => node.display_name.trim())
  if (names.some((name) => !name) || new Set(names).size !== names.length) {
    personalFormError.value = '节点显示名称不能为空且不能重复'
    return
  }
  busy.value = true
  let id = editingPersonal.value?.id
  const creating = id === undefined
  let groupSaved = false
  let nodesSaved = false
  let bindingsSaved = false
  try {
    const body = {
      name: personalName.value.trim(), subscription_title: personalTitle.value.trim(),
      enabled: personalEnabled.value, client_name: personalClientName.value.trim(),
      routing_preset_id: personalRoutingPresetID.value,
      mihomo_template_id: personalMihomoTemplateID.value || null,
    }
    if (id !== undefined) {
      await api(`/api/personal-subscriptions/${id}`, { method: 'PATCH', body: JSON.stringify(body) })
    } else {
      const response = await api<{ personal_subscription: PersonalSubscription }>('/api/personal-subscriptions', {
        method: 'POST', body: JSON.stringify(body),
      })
      id = response.personal_subscription.id
    }
    groupSaved = true
    const submittedNodes = [...personalNodes.value]
    const savedNodes = await api<{ personal_subscription: PersonalSubscription }>(`/api/personal-subscriptions/${id}/nodes`, {
      method: 'PUT',
      body: JSON.stringify({
        nodes: submittedNodes.map((node) => ({
          id: node.id > 0 ? node.id : null,
          source_type: node.source_type, source_id: node.source_id,
          display_name: node.display_name.trim(), enabled: node.enabled,
          entry_host: node.entry_host?.trim() || null, entry_port: node.entry_port,
        })),
      }),
    })
    nodesSaved = true
    const savedIDs = savedNodes.personal_subscription.nodes.map((node) => node.id)
    const idMap = new Map(submittedNodes.map((node, index) => [node.id, savedIDs[index]]))
    const translatedBindings = Object.fromEntries(Object.entries(personalRoutingBindings.value).map(([key, ids]) => [
      key,
      ids.flatMap((nodeID) => {
        const savedID = idMap.get(nodeID)
        return savedID === undefined ? [] : [savedID]
      }),
    ]))
    personalNodes.value = clonePersonalNodes(savedNodes.personal_subscription.nodes)
    personalRoutingBindings.value = translatedBindings
    await api(`/api/personal-subscriptions/${id}/routing-bindings`, {
      method: 'PUT',
      body: JSON.stringify({
        routing_bindings: normalizeRoutingBindings(selectedPersonalRoutingGroups.value, translatedBindings, savedIDs),
      }),
    })
    bindingsSaved = true
    await loadAll()
    personalModalOpen.value = false
  } catch (reason) {
    const message = reason instanceof Error ? reason.message : '保存个人订阅失败'
    if (groupSaved && !nodesSaved) {
      personalFormError.value = creating
        ? `个人订阅已创建，但节点列表保存失败：${message}`
        : `个人订阅基本信息已保存，但节点列表保存失败：${message}`
      try {
        await loadAll()
        editingPersonal.value = personalSubscriptions.value.find((value) => value.id === id) ?? editingPersonal.value
      } catch (refreshReason) {
        const refreshMessage = refreshReason instanceof Error ? refreshReason.message : '未知错误'
        personalFormError.value += `；刷新服务端状态失败：${refreshMessage}`
      }
    } else if (nodesSaved && !bindingsSaved) {
      personalFormError.value = `个人订阅和节点列表已保存，但策略组节点绑定保存失败：${message}`
    } else if (groupSaved) {
      personalFormError.value = `个人订阅已保存，但刷新列表失败：${message}`
    } else {
      personalFormError.value = message
    }
  } finally {
    busy.value = false
  }
}

async function deletePersonal(value: PersonalSubscription) {
  if (!window.confirm(`确定删除个人订阅“${value.name}”吗？`)) return
  await run(async () => {
    await api(`/api/personal-subscriptions/${value.id}`, { method: 'DELETE' })
    await loadAll()
  })
}

async function regeneratePersonalToken(value: PersonalSubscription) {
  if (!window.confirm('重新生成后，旧订阅链接会立即失效。确定继续吗？')) return
  await run(async () => {
    await api(`/api/personal-subscriptions/${value.id}/token/regenerate`, { method: 'POST' })
    await loadAll()
  })
}

async function copyPersonalURL(value: PersonalSubscription) {
  await navigator.clipboard.writeText(value.subscription_auto_url)
  personalCopiedID.value = value.id
}

function showPersonalQR(value: PersonalSubscription) {
  personalQR.value = value
  personalQROpen.value = true
}

async function previewPersonal(value: PersonalSubscription) {
  await run(async () => {
    const response = await api<{ yaml: string }>(`/api/personal-subscriptions/${value.id}/mihomo-preview`)
    personalPreviewYAML.value = response.yaml
    personalPreviewOpen.value = true
  })
}

function personalSourceLabel(value: string) {
  return value === 'proxy' ? '本地 Proxy' : value === 'relay' ? '中转 Relay' : '外部节点 Landing'
}

function personalStatusType(value: string) {
  return value === 'ready' ? 'success' : 'warning'
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
  planRoutingBindings.value = {}
  planRoutingPresetID.value = defaultRoutingPreset.value?.id ?? routingPresets.value.find((value) => value.enabled)?.id ?? 0
  planTemplateID.value = 0
  planBindingEditorKey.value++
  planModalOpen.value = true
}

function populatePlanForm(value: Plan) {
  editingPlan.value = value
  planName.value = value.name
  planSubscriptionTitle.value = value.subscription_title
  planEnabled.value = value.enabled
  planTrafficGiB.value = value.traffic_limit_bytes === null ? '' : String(value.traffic_limit_bytes / 1024 ** 3)
  planNodeIDs.value = value.nodes.map((node) => node.id)
  planRoutingBindings.value = cloneRoutingBindings(value.routing_bindings)
  planRoutingPresetID.value = value.routing_preset_id
  planTemplateID.value = value.template_id ?? 0
}

function openEditPlan(value: Plan) {
  planFormError.value = ''
  populatePlanForm(value)
  planBindingEditorKey.value++
  planModalOpen.value = true
}

function togglePlanNode(id: number, checked: boolean) {
  if (checked && !planNodeIDs.value.includes(id)) planNodeIDs.value.push(id)
  if (!checked) {
    planNodeIDs.value = planNodeIDs.value.filter((value) => value !== id)
    planRoutingBindings.value = pruneRoutingBindingNode(planRoutingBindings.value, id)
  }
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
  return values.map((group) => ({ ...group, proxies: [...group.proxies] }))
}

function cloneRoutingProviders(values: RoutingRuleProvider[]) {
  return values.map((provider) => ({ ...provider }))
}

function openRoutingPreview(title: string, groups: RoutingGroup[], rules: string[], providers: RoutingRuleProvider[], help: string) {
  routingPreviewTitle.value = title
  routingPreviewGroups.value = cloneRoutingGroups(groups)
  routingPreviewRules.value = [...rules]
  routingPreviewProviders.value = cloneRoutingProviders(providers)
  routingPreviewHelp.value = help
  routingPreviewOpen.value = true
}

function viewSelectedPlanRouting() {
  const value = routingPresets.value.find((preset) => preset.id === planRoutingPresetID.value)
  if (!value) {
    planFormError.value = '请选择分流方案'
    return
  }
  openRoutingPreview(
    value.name, value.groups, value.rules, value.rule_providers,
    '共享订阅会实时使用该分流方案的最新内容；请到“分流模板”页面统一编辑。',
  )
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
  templateYAML.value = builtinMihomo.value.yaml.trim()
  templateFormError.value = ''
  templateModalOpen.value = true
}

async function savePlan() {
  if (busy.value) return
  planFormError.value = ''

  const name = planName.value.trim()
  if (!name) {
    planFormError.value = '共享订阅名称不能为空'
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
  if (!planRoutingPresetID.value) {
    planFormError.value = '请选择分流方案'
    return
  }
  const body = {
    name, subscription_title: planSubscriptionTitle.value.trim(),
    enabled: planEnabled.value, traffic_limit_bytes: trafficLimit,
    routing_preset_id: planRoutingPresetID.value,
    template_id: planTemplateID.value || null,
  }

  busy.value = true
  let id = editingPlan.value?.id
  const creating = id === undefined
  let planSaved = false
  let nodesSaved = false
  let bindingsSaved = false
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
    await api(`/api/admin/subscription/plans/${id}/routing-bindings`, {
      method: 'PUT',
      body: JSON.stringify({
        routing_bindings: normalizeRoutingBindings(selectedPlanRoutingGroups.value, planRoutingBindings.value, planNodeIDs.value),
      }),
    })
    bindingsSaved = true
    await loadAll()
    planModalOpen.value = false
  } catch (reason) {
    const message = reason instanceof Error ? reason.message : '保存共享订阅失败'
    if (planSaved && !nodesSaved) {
      planFormError.value = creating
        ? `共享订阅已创建，但节点列表保存失败：${message}`
        : `共享订阅基本信息已保存，但节点列表保存失败：${message}`
      try {
        await loadAll()
        const current = plans.value.find((value) => value.id === id)
        if (current) populatePlanForm(current)
      } catch (refreshReason) {
        const refreshMessage = refreshReason instanceof Error ? refreshReason.message : '未知错误'
        planFormError.value += `；刷新服务端状态失败：${refreshMessage}`
      }
    } else if (nodesSaved && !bindingsSaved) {
      planFormError.value = `共享订阅和节点列表已保存，但策略组节点绑定保存失败：${message}`
    } else if (planSaved) {
      planFormError.value = `共享订阅已保存，但刷新列表失败：${message}`
    } else {
      planFormError.value = message
    }
  } finally {
    busy.value = false
  }
}

async function deletePlan(value: Plan) {
  if (!window.confirm(`确定删除共享订阅“${value.name}”吗？`)) return
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
  routingProviders.value = []
  routingRulesText.value = ''
  routingFormError.value = ''
  routingGroupsExpanded.value = true
  routingProvidersExpanded.value = true
  routingRulesExpanded.value = true
  routingModalOpen.value = true
}

function openEditRoutingPreset(value: RoutingPreset) {
  editingRoutingPreset.value = value
  routingName.value = value.name
  routingEnabled.value = value.enabled
  routingGroups.value = cloneRoutingGroups(value.groups)
  routingProviders.value = cloneRoutingProviders(value.rule_providers)
  routingRulesText.value = value.rules.join('\n')
  routingFormError.value = ''
  routingGroupsExpanded.value = true
  routingProvidersExpanded.value = true
  routingRulesExpanded.value = true
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
      body: JSON.stringify({
        name: routingName.value.trim(), enabled: routingEnabled.value, groups: routingGroups.value,
        rule_providers: routingProviders.value, rules,
      }),
    })
    routingModalOpen.value = false
    await loadAll()
  })
}

function addRoutingProvider() {
  routingProviders.value.push({
    name: '', url: '', type: 'http', behavior: 'classical', format: 'yaml', interval: 86400,
  })
}

function ruleUsesProvider(rule: string, name: string) {
  const parts = rule.split(',').map((part) => part.trim())
  return parts[0] === 'RULE-SET' && parts[1] === name
}

function setRoutingProviderName(index: number, name: string) {
  const oldName = routingProviders.value[index]?.name
  if (oldName === undefined || oldName === name) return
  routingProviders.value[index].name = name
  routingRulesText.value = routingLines(routingRulesText.value).map((rule) => {
    const parts = rule.split(',').map((part) => part.trim())
    if (parts[0] === 'RULE-SET' && parts[1] === oldName) parts[1] = name
    return parts.join(',')
  }).join('\n')
  routingFormError.value = ''
}

function removeRoutingProvider(index: number) {
  const provider = routingProviders.value[index]
  const referencedAt = provider
    ? routingLines(routingRulesText.value).findIndex((rule) => ruleUsesProvider(rule, provider.name))
    : -1
  if (provider && referencedAt >= 0) {
    routingFormError.value = `规则源“${provider.name}”仍被第 ${referencedAt + 1} 条 Rule 使用，请先解除引用`
    return
  }
  routingProviders.value.splice(index, 1)
  routingFormError.value = ''
}

async function deleteRoutingPreset(value: RoutingPreset) {
  if (value.is_default || !window.confirm(`确定删除分流方案“${value.name}”吗？`)) return
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

  <section v-else-if="currentTab === 'personal'" class="subscription-section">
    <div class="section-heading"><span></span><n-button type="primary" @click="openCreatePersonal">新增个人订阅</n-button></div>
    <n-empty v-if="personalSubscriptions.length === 0" description="暂无个人订阅" />
    <div v-else class="user-management-grid">
      <n-card v-for="value in personalSubscriptions" :key="value.id" :title="value.name">
        <template #header-extra><n-tag :type="value.enabled ? 'success' : 'default'">{{ value.enabled ? '启用' : '停用' }}</n-tag></template>
        <p>Client：{{ value.client_name }}</p>
        <p>{{ value.nodes.length }} 个节点 · {{ value.routing_preset_name }}</p>
        <p>Mihomo 模板：{{ value.mihomo_template_name || '内置默认 Mihomo 模板' }}</p>
        <div class="modal-actions">
          <n-button secondary @click="copyPersonalURL(value)">{{ personalCopiedID === value.id ? '已复制' : '复制链接' }}</n-button>
          <n-button secondary @click="showPersonalQR(value)">二维码</n-button>
          <n-button secondary @click="previewPersonal(value)">预览</n-button>
          <n-button secondary @click="openEditPersonal(value)">编辑</n-button>
          <n-button secondary @click="regeneratePersonalToken(value)">重置链接</n-button>
          <n-button type="error" secondary @click="deletePersonal(value)">删除</n-button>
        </div>
      </n-card>
    </div>
  </section>

  <section v-else-if="currentTab === 'users'" class="subscription-section">
    <n-empty v-if="users.length === 0" description="暂无订阅用户" />
    <div v-else class="server-table-wrap"><table class="server-table"><thead><tr><th>用户</th><th>共享订阅</th><th>已用 / 总量</th><th>到期</th><th>节点</th><th>状态</th><th>操作</th></tr></thead><tbody>
      <tr v-for="value in users" :key="value.user_id"><td>{{ value.username }}</td><td>{{ value.plan_name || '未开通' }}</td><td>{{ formatClientTrafficBytes(value.used_bytes) }} / {{ value.traffic_limit_bytes !== null ? formatClientTrafficBytes(value.traffic_limit_bytes) : '不限' }}</td><td>{{ value.expires_at ? formatTime(value.expires_at) : '不限' }}</td><td>{{ value.enabled_node_count }}</td><td><n-tag :type="value.active ? 'success' : 'warning'" size="small">{{ statusLabels[value.status] ?? value.status }}</n-tag></td><td><n-button size="small" secondary @click="openUser(value)">查看 / 编辑</n-button></td></tr>
    </tbody></table></div>
  </section>

  <section v-else-if="currentTab === 'plans'" class="subscription-section">
    <div class="section-heading"><span></span><n-button type="primary" @click="openCreatePlan">新增共享订阅</n-button></div>
    <n-empty v-if="plans.length === 0" description="暂无共享订阅" />
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
        <p>所属共享订阅：{{ nodePlanNames(value.id) || '未加入共享订阅' }}</p>
        <div class="modal-actions"><n-button secondary @click="openEditNode(value)">编辑</n-button><n-button type="error" secondary @click="deleteNode(value)">删除</n-button></div>
      </n-card>
    </div>
  </section>

  <section v-else class="subscription-section configuration-grid">
    <p class="form-help">通用分流方案负责策略组、规则源和 Rules；客户端模板只负责对应客户端的基础配置。个人订阅和共享订阅分别选择一套分流方案与 Mihomo 模板。</p>
    <n-card title="通用分流方案" :bordered="true">
      <p class="form-help">修改分流方案后，所有引用它的个人订阅和共享订阅会在客户端下一次刷新时使用最新内容。</p>
      <n-button type="primary" @click="openCreateRoutingPreset">新增分流方案</n-button>
      <div class="configuration-list">
        <div v-for="value in routingPresets" :key="value.id" class="invitation-row">
          <div><strong>{{ value.name }} <n-tag v-if="value.is_default" type="info" size="small">默认</n-tag> <n-tag :type="value.enabled ? 'success' : 'default'" size="small">{{ value.enabled ? '已启用' : '已停用' }}</n-tag></strong><span>{{ value.groups.length }} 个分组 · {{ value.rules.length }} 条规则</span></div>
          <div class="modal-actions"><n-button secondary size="small" @click="openEditRoutingPreset(value)">编辑</n-button><n-button v-if="!value.is_default" type="error" secondary size="small" @click="deleteRoutingPreset(value)">删除</n-button></div>
        </div>
      </div>
    </n-card>
    <n-card title="客户端模板" :bordered="true">
      <h3>Mihomo 模板</h3>
      <p class="form-help">模板只负责 Mihomo 客户端基础配置，例如 DNS、sniffer、TUN、profile 等。proxies 由 Panel 动态生成，分流由所选分流方案提供。</p>
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

  <n-modal v-model:show="personalModalOpen"><n-card class="client-form-card subscription-form-card" :title="editingPersonal ? '编辑个人订阅' : '新增个人订阅'" closable @close="personalModalOpen = false"><form class="auth-form" novalidate @submit.prevent="savePersonal">
    <n-alert v-if="personalFormError" type="error" closable @close="personalFormError = ''">{{ personalFormError }}</n-alert>
    <h3>基本信息</h3>
    <label><span>名称</span><n-input v-model:value="personalName" maxlength="100" /></label>
    <label><span>订阅标题</span><n-input v-model:value="personalTitle" maxlength="100" /><small class="form-help">留空时使用个人订阅名称。</small></label>
    <label><span>同名 Client</span><n-input v-model:value="personalClientName" maxlength="100" placeholder="admin" /><small class="form-help">在每个本地 Proxy 上实时精确匹配同名、当前用户可用且非订阅托管的 Client。</small></label>
    <div class="switch-row"><span>启用个人订阅</span><n-switch v-model:value="personalEnabled" /></div>
    <label><span>分流方案</span><select v-model.number="personalRoutingPresetID" class="settings-input"><option v-for="value in selectablePersonalRoutingPresets" :key="value.id" :value="value.id">{{ value.name }}{{ value.is_default ? '（默认）' : '' }}</option></select></label>
    <h3>Mihomo 输出</h3>
    <label><span>Mihomo 模板</span><select v-model.number="personalMihomoTemplateID" class="settings-input"><option :value="0">内置默认 Mihomo 模板</option><option v-for="value in selectablePersonalTemplates" :key="value.id" :value="value.id">{{ value.name }}</option></select></label>
    <h3>节点</h3>
    <div class="modal-actions routing-add-actions">
      <n-button secondary attr-type="button" :aria-expanded="personalSourceAdderExpanded" @click="togglePersonalSourceAdder">{{ personalSourceAdderExpanded ? '收起添加' : '+ 添加节点' }}</n-button>
      <n-button secondary attr-type="button" :loading="personalSourcesLoading" :disabled="!personalClientName.trim()" @click="addAllPersonalSources">添加全部可用节点</n-button>
    </div>
    <section v-if="personalSourceAdderExpanded" class="subscription-node-picker personal-source-adder">
      <div class="collapsible-section-header"><strong>添加节点</strong><n-button size="tiny" secondary attr-type="button" @click="personalSourceAdderExpanded = false">收起</n-button></div>
      <div class="personal-source-toolbar">
        <n-input v-model:value="personalSourceSearch" clearable placeholder="搜索节点名称或详情" />
        <n-button secondary attr-type="button" :loading="personalSourcesLoading" @click="loadPersonalSources">刷新</n-button>
      </div>
      <div class="personal-source-scroll">
        <div v-for="sourceType in personalSourceTypes" :key="sourceType" class="personal-source-group">
          <strong>{{ personalSourceLabel(sourceType) }}</strong>
          <span v-if="filteredPersonalSources.every((source) => source.source_type !== sourceType)" class="form-help">暂无匹配的可访问来源</span>
          <div v-for="source in filteredPersonalSources.filter((item) => item.source_type === sourceType)" :key="`${source.source_type}:${source.source_id}`" class="invitation-row">
            <div><strong>{{ source.name }}</strong><span>{{ source.detail }}</span><small>{{ source.status_detail }}</small></div>
            <n-button secondary size="small" attr-type="button" @click="addPersonalSource(source)">添加</n-button>
          </div>
        </div>
      </div>
    </section>
    <fieldset class="subscription-node-picker"><legend>已选节点（拖动调整订阅顺序）</legend>
      <n-empty v-if="personalNodes.length === 0" description="尚未选择节点" />
      <TransitionGroup tag="div" class="personal-node-list" name="personal-node-order">
        <div
          v-for="(node, index) in personalNodes"
          :key="node.id"
          class="personal-node-item"
          :class="{ 'personal-node-dragging': draggedPersonalNodeID === node.id, 'personal-node-drop-target': personalDropTargetID === node.id }"
          @dragover="dragOverPersonalNode($event, node.id)"
          @dragleave="personalDropTargetID === node.id && (personalDropTargetID = null)"
          @drop.prevent="dropPersonalNode(node.id)"
        >
          <div class="personal-node-summary">
            <span class="drag-handle" draggable="true" aria-label="拖动个人订阅节点排序" title="拖动排序" @dragstart="startPersonalNodeDrag($event, node.id)" @dragend="endPersonalNodeDrag"><span></span><span></span><span></span></span>
            <div class="personal-node-heading"><strong>{{ node.display_name || '未命名节点' }}</strong><small>{{ personalSourceLabel(node.source_type) }} · {{ node.source_name }}</small></div>
            <n-tag :type="personalStatusType(node.status)" size="small">{{ node.status_detail }}</n-tag>
            <n-button class="personal-node-details-button" size="small" secondary attr-type="button" @click="togglePersonalNodeDetails(node.id)">{{ expandedPersonalNodeIDs.has(node.id) ? '收起详情' : '详情' }}</n-button>
            <n-button class="personal-node-delete-button" size="small" type="error" secondary attr-type="button" @click="removePersonalNode(index)">删除</n-button>
          </div>
          <div v-if="expandedPersonalNodeIDs.has(node.id)" class="personal-node-details">
            <dl><div><dt>来源类型</dt><dd>{{ personalSourceLabel(node.source_type) }}</dd></div><div><dt>实际来源</dt><dd>{{ node.source_name }}</dd></div></dl>
            <p class="form-help">{{ node.source_detail }}</p>
            <label><span>自定义显示名称</span><n-input v-model:value="node.display_name" maxlength="100" /></label>
            <label><span>入口地址</span><n-input :value="node.entry_host ?? ''" placeholder="留空使用来源默认入口" @update:value="setPersonalEntryHost(node, $event)" /></label>
            <label><span>入口端口</span><n-input-number v-model:value="node.entry_port" :min="1" :max="65535" :precision="0" placeholder="留空使用来源默认端口" /></label>
            <div v-if="node.requires_client" class="personal-node-client-status"><span>Client：{{ personalClientName }}</span><n-tag :type="personalStatusType(node.status)" size="small">{{ node.status_detail }}</n-tag></div>
            <div v-else class="personal-node-client-status"><span>凭据：节点自带</span><n-tag :type="personalStatusType(node.status)" size="small">{{ node.status_detail }}</n-tag></div>
            <div class="switch-row"><span>启用节点</span><n-switch v-model:value="node.enabled" /></div>
          </div>
        </div>
      </TransitionGroup>
    </fieldset>
    <RoutingBindingEditor
      v-model="personalRoutingBindings"
      :groups="selectedPersonalRoutingGroups"
      :nodes="personalNodes.map((node) => ({ id: node.id, name: node.display_name }))"
      default-collapsed
      :reset-key="personalBindingEditorKey"
    />
    <div class="modal-actions"><n-button @click="personalModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy" :disabled="busy">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-model:show="personalPreviewOpen"><n-card class="client-form-card subscription-form-card" title="个人订阅 Mihomo Preview" closable @close="personalPreviewOpen = false">
    <n-input :value="personalPreviewYAML" type="textarea" readonly :autosize="{ minRows: 18, maxRows: 28 }" />
    <div class="modal-actions"><n-button @click="personalPreviewOpen = false">关闭</n-button></div>
  </n-card></n-modal>

  <QRCodeModal
    v-if="personalQR"
    v-model:show="personalQROpen"
    :uri="personalQR.subscription_auto_url"
    :title="personalQR.name"
    modal-title="个人订阅二维码"
    instruction="使用支持订阅二维码的客户端扫描导入。"
    :links="[
      { label: 'Auto', value: personalQR.subscription_auto_url },
      { label: 'Mihomo', value: personalQR.subscription_mihomo_url },
      { label: 'Base64', value: personalQR.subscription_base64_url },
    ]"
  />

  <n-modal v-model:show="routingPreviewOpen"><n-card class="client-form-card subscription-form-card" :title="routingPreviewTitle" closable @close="routingPreviewOpen = false">
    <p class="form-help">{{ routingPreviewHelp }}</p>
    <h3>策略组</h3>
    <RoutingGroupEditor :model-value="routingPreviewGroups" readonly />
    <h3>远程规则集（Rule Providers）</h3>
    <p class="form-help">这里只用于 RULE-SET 远程规则。DOMAIN、DOMAIN-SUFFIX、GEOSITE、GEOIP 等单条规则请直接写在 Rules 中。</p>
    <div v-for="provider in routingPreviewProviders" :key="provider.name" class="invitation-row"><div><strong>{{ provider.name }}</strong><span>{{ provider.type }} · {{ provider.behavior }} · {{ provider.format }} · {{ provider.interval }} 秒</span><small>{{ provider.url }}</small></div></div>
    <label><span>Rules</span><n-input :value="routingPreviewRules.join('\n')" type="textarea" readonly :autosize="{ minRows: 8, maxRows: 18 }" /></label>
    <p class="form-help">Rules 按从上到下顺序匹配，先命中先生效。支持 DOMAIN、DOMAIN-SUFFIX、GEOSITE、GEOIP、RULE-SET、MATCH 等规则。</p>
    <div class="modal-actions"><n-button @click="routingPreviewOpen = false">关闭</n-button></div>
  </n-card></n-modal>

  <n-modal v-model:show="templatePreviewOpen"><n-card class="client-form-card subscription-form-card" title="内置默认 Mihomo 模板" closable @close="templatePreviewOpen = false">
    <n-alert type="info">这里只包含客户端基础配置。真实 proxies 由 Panel 动态注入，策略组、规则源和 Rules 来自订阅选择的分流方案。</n-alert>
    <n-input :value="templatePreviewYAML" type="textarea" readonly :autosize="{ minRows: 18, maxRows: 28 }" />
    <div class="modal-actions"><n-button @click="templatePreviewOpen = false">关闭</n-button></div>
  </n-card></n-modal>

  <n-modal v-model:show="routingModalOpen"><n-card class="client-form-card subscription-form-card" :title="editingRoutingPreset ? '编辑分流方案' : '新增分流方案'" closable @close="routingModalOpen = false"><form class="auth-form" @submit.prevent="saveRoutingPreset">
    <n-alert v-if="routingFormError" type="error" closable @close="routingFormError = ''">{{ routingFormError }}</n-alert>
    <label><span>名称</span><n-input v-model:value="routingName" maxlength="100" /></label>
    <div class="switch-row"><span>启用方案</span><n-switch v-model:value="routingEnabled" :disabled="Boolean(editingRoutingPreset?.is_default)" /></div>
    <section class="routing-config-section">
      <div class="collapsible-section-header"><strong>策略组</strong><n-button size="tiny" secondary attr-type="button" :aria-expanded="routingGroupsExpanded" @click="routingGroupsExpanded = !routingGroupsExpanded">{{ routingGroupsExpanded ? '收起' : '展开' }}</n-button></div>
      <div v-if="routingGroupsExpanded" class="routing-config-scroll routing-config-scroll-groups">
        <p class="form-help">策略组顺序决定生成到客户端后的策略组排列顺序。</p>
        <RoutingGroupEditor v-model="routingGroups" v-model:rules="routingRuleLines" @validation-error="routingFormError = $event" />
      </div>
    </section>
    <section class="routing-config-section">
      <div class="collapsible-section-header"><strong>远程规则集（Rule Providers）</strong><n-button size="tiny" secondary attr-type="button" :aria-expanded="routingProvidersExpanded" @click="routingProvidersExpanded = !routingProvidersExpanded">{{ routingProvidersExpanded ? '收起' : '展开' }}</n-button></div>
      <div v-if="routingProvidersExpanded" class="routing-config-scroll routing-config-scroll-providers">
        <p class="form-help">这里只用于 RULE-SET 远程规则。DOMAIN、DOMAIN-SUFFIX、GEOSITE、GEOIP 等单条规则请直接写在 Rules 中。</p>
        <div v-for="(provider, index) in routingProviders" :key="index" class="personal-node-editor">
          <label><span>名称</span><n-input :value="provider.name" placeholder="Google" @update:value="setRoutingProviderName(index, $event)" /></label>
          <label><span>URL</span><n-input v-model:value="provider.url" placeholder="https://example.com/rules.yaml" /></label>
          <label><span>类型</span><select v-model="provider.type" class="settings-input"><option value="http">http</option></select></label>
          <label><span>Behavior</span><n-input v-model:value="provider.behavior" placeholder="classical" /></label>
          <label><span>Format</span><n-input v-model:value="provider.format" placeholder="yaml" /></label>
          <label><span>更新间隔（秒）</span><n-input-number v-model:value="provider.interval" :min="1" :precision="0" /></label>
          <n-button type="error" secondary attr-type="button" @click="removeRoutingProvider(index)">删除规则集</n-button>
        </div>
        <n-button secondary attr-type="button" @click="addRoutingProvider">添加远程规则集</n-button>
      </div>
    </section>
    <section class="routing-config-section">
      <div class="collapsible-section-header"><strong>规则（Rules）</strong><n-button size="tiny" secondary attr-type="button" :aria-expanded="routingRulesExpanded" @click="routingRulesExpanded = !routingRulesExpanded">{{ routingRulesExpanded ? '收起' : '展开' }}</n-button></div>
      <div v-if="routingRulesExpanded" class="routing-config-scroll routing-config-scroll-rules">
        <p class="form-help">Rules 按从上到下顺序匹配，先命中先生效。支持 DOMAIN、DOMAIN-SUFFIX、GEOSITE、GEOIP、RULE-SET、MATCH 等规则。</p>
        <label><span>Rules（一行一条 Mihomo rule）</span><n-input v-model:value="routingRulesText" type="textarea" placeholder="RULE-SET,OpenAI,🤖 AI&#10;GEOIP,CN,DIRECT,no-resolve&#10;MATCH,🚀 默认代理" :autosize="{ minRows: 6, maxRows: 16 }" /></label>
      </div>
    </section>
    <div class="modal-actions subscription-form-actions"><n-button @click="routingModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-model:show="templateModalOpen"><n-card class="client-form-card subscription-form-card" :title="editingTemplate ? '编辑 Mihomo 模板' : '新增 Mihomo 模板'" closable @close="templateModalOpen = false"><form class="auth-form" @submit.prevent="saveTemplate">
    <n-alert v-if="templateFormError" type="error" closable @close="templateFormError = ''">{{ templateFormError }}</n-alert>
    <label><span>名称</span><n-input v-model:value="templateName" maxlength="100" /></label>
    <div class="switch-row"><span>启用模板</span><n-switch v-model:value="templateEnabled" /></div>
    <label><span>Mihomo 基础配置 YAML</span><n-input v-model:value="templateYAML" type="textarea" :autosize="{ minRows: 12, maxRows: 24 }" /><small class="form-help">不能包含 proxies、proxy-groups、rule-providers 或 rules。</small></label>
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
    <label><span>流量倍率</span><n-input-number v-model:value="nodeTrafficMultiplier" :min="0.1" :max="5" :step="0.1" :precision="2"><template #suffix>×</template></n-input-number><small class="form-help">实际使用 1 GB 时，按该倍率计入共享订阅流量。允许 0.10×–5.00×。</small></label>
    <fieldset class="subscription-node-picker"><legend>{{ editingNode ? '所属共享订阅' : '加入共享订阅' }}</legend>
      <span v-if="plans.length === 0" class="form-help">暂无共享订阅，可先创建备用发布节点。</span>
      <label v-for="plan in plans" :key="plan.id" class="subscription-node-option"><input type="checkbox" :checked="nodePlanIDs.includes(plan.id)" @change="toggleNodePlan(plan.id, ($event.target as HTMLInputElement).checked)" /><span>{{ plan.name }}</span></label>
    </fieldset>
    <div class="switch-row"><span>启用发布节点</span><n-switch v-model:value="nodeEnabled" /></div>
    <div class="modal-actions"><n-button @click="nodeModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy" :disabled="!nodeTargetProxyID || (nodeMode === 'relay' && !nodeSourceServerID)">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-model:show="planModalOpen"><n-card class="client-form-card subscription-form-card" :title="editingPlan ? '编辑共享订阅' : '新增共享订阅'" closable @close="planModalOpen = false"><form class="auth-form" novalidate @submit.prevent="savePlan">
    <n-alert v-if="planFormError" type="error" closable @close="planFormError = ''">{{ planFormError }}</n-alert>
    <label><span>共享订阅名称</span><n-input v-model:value="planName" maxlength="100" /></label>
    <label><span>订阅显示名称</span><n-input v-model:value="planSubscriptionTitle" maxlength="100" /><small class="form-help">客户端导入订阅后显示的名称。留空则使用共享订阅名称。</small></label>
    <label><span>流量额度（GiB，留空不限）</span><input v-model="planTrafficGiB" class="settings-input" type="number" min="0" step="any" /></label>
    <label><span>Mihomo 模板</span><select v-model.number="planTemplateID" class="settings-input"><option :value="0">内置默认 Mihomo 模板</option><option v-for="value in templates" :key="value.id" :value="value.id">{{ value.name }}</option></select><small class="form-help">模板只负责 DNS、sniffer 等客户端基础配置。</small></label>
    <label><span>分流方案</span><select v-model.number="planRoutingPresetID" class="settings-input"><option v-for="value in selectableRoutingPresets" :key="value.id" :value="value.id">{{ value.name }}{{ value.is_default ? '（默认）' : '' }}</option></select><small class="form-help">共享订阅直接引用分流方案，方案修改后无需重新保存共享订阅。</small></label>
    <div class="modal-actions"><n-button secondary attr-type="button" :disabled="!planRoutingPresetID" @click="viewSelectedPlanRouting">查看分流方案</n-button></div>
    <div class="switch-row"><span>启用共享订阅</span><n-switch v-model:value="planEnabled" /></div>
    <fieldset class="subscription-node-picker"><legend>包含节点（上下调整订阅顺序）</legend>
      <label v-for="node in orderedPlanNodes" :key="node.id" class="subscription-node-option"><input type="checkbox" :checked="planNodeIDs.includes(node.id)" @change="togglePlanNode(node.id, ($event.target as HTMLInputElement).checked)" /><span>{{ nodeDisplayName(node) }}</span><template v-if="planNodeIDs.includes(node.id)"><n-button size="tiny" secondary attr-type="button" @click.prevent="movePlanNode(planNodeIDs.indexOf(node.id), -1)">上移</n-button><n-button size="tiny" secondary attr-type="button" @click.prevent="movePlanNode(planNodeIDs.indexOf(node.id), 1)">下移</n-button></template></label>
    </fieldset>
    <RoutingBindingEditor
      v-model="planRoutingBindings"
      :groups="selectedPlanRoutingGroups"
      :nodes="nodes.filter((node) => planNodeIDs.includes(node.id)).map((node) => ({ id: node.id, name: nodeDisplayName(node) }))"
      :reset-key="planBindingEditorKey"
    />
    <div class="modal-actions subscription-form-actions"><n-button @click="planModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy" :disabled="busy">保存</n-button></div>
  </form></n-card></n-modal>

  <n-modal v-if="selectedUser" v-model:show="userModalOpen"><n-card class="client-form-card subscription-form-card" title="订阅用户" closable @close="userModalOpen = false"><form class="auth-form" @submit.prevent="saveUser">
    <n-alert v-if="userFormError" type="error" closable @close="userFormError = ''">{{ userFormError }}</n-alert>
    <strong>{{ selectedUser.username }}</strong>
    <dl class="user-details"><div><dt>状态</dt><dd>{{ statusLabels[selectedUser.status] ?? selectedUser.status }}</dd></div><div><dt>已用 / 总量</dt><dd>{{ formatClientTrafficBytes(selectedUser.used_bytes) }} / {{ selectedUser.traffic_limit_bytes !== null ? formatClientTrafficBytes(selectedUser.traffic_limit_bytes) : '不限' }}</dd></div><div><dt>周期开始</dt><dd>{{ formatTime(selectedUser.cycle_started_at) }}</dd></div><div><dt>下次重置</dt><dd>{{ selectedUser.next_reset_at ? formatTime(selectedUser.next_reset_at) : '不重置' }}</dd></div><div><dt>可用节点</dt><dd>{{ selectedUser.enabled_node_count }}</dd></div><div><dt>密码重置申请</dt><dd>{{ selectedUser.password_request?.status === 'pending' ? '等待审核' : '无待审核申请' }}</dd></div></dl>
    <label><span>共享订阅</span><select v-model.number="userPlanID" class="settings-input"><option :value="0">未开通</option><option v-for="plan in enabledPlans" :key="plan.id" :value="plan.id">{{ plan.name }}</option></select></label>
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
