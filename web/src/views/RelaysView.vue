<script setup lang="ts">
import {
  computed,
  onMounted,
  ref,
  watch,
} from 'vue'

import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NInput,
  NModal,
  NSpin,
  NTag,
} from 'naive-ui'

import {
  relayNetworkLabel,
  relayEndpointLabel,
  relayTargetLabel,
  type RelayNetwork,
  type RelayTargetType,
} from '../relay'

import {
  clientStatusLabel,
  clientStatusTagType,
  type ClientStatus,
} from '../proxy'
import { moveRow, persistMove } from '../reorder'
import QRCodeModal from '../components/share/QRCodeModal.vue'
import {
  landingProtocolLabel,
  type LandingRecord,
} from '../landing'
import {
  agentCapabilities,
  agentSupportsCapability,
} from '../server'
import type { ServerRecord } from '../types/server'

type ServerOption = Pick<ServerRecord,
  'id' | 'name' | 'system_info' | 'agent_implementation' | 'agent_api_version' | 'agent_capabilities'
>

type ProxyOption = {
  id: number
  server_id: number
  server_name: string
  name: string
  protocol: 'vless' | 'shadowsocks'
  listen_port: number
  entry_address: string
}

type RelayRecord = {
  id: number
  server_id: number
  server_name: string
  server_public_ipv4: string
  name: string
  listen_address: string
  listen_port: number
  entry_host_mode: 'auto' | 'manual'
  entry_host: string
  entry_address: string
  target_type: RelayTargetType
  target_proxy_id: number | null
  target_client_id: number | null
  target_landing_id: number | null
  target_proxy_name: string
  target_client_name: string
  target_landing_name: string
  target_landing_protocol: 'vless' | 'shadowsocks' | ''
  target_landing_visibility: 'private' | 'public' | ''
  target_host: string
  target_port: number
  target_address_ready: boolean
  network: RelayNetwork
  enabled: boolean
  created_at: string
  updated_at: string
}

type RelayClientShare = {
  client: {
    id: number
    name: string
    effective_enabled: boolean
    status: ClientStatus
  }
  protocol: 'vless' | 'shadowsocks'
  uri: string
  network_compatible: boolean
  network_notice?: string
}

type RelayLandingShare = {
  landing: Pick<LandingRecord, 'id' | 'name' | 'protocol'>
  uri: string
  network_compatible: boolean
  network_notice?: string
}

type TargetClientOption = {
  id: number
  name: string
  status: ClientStatus
}

type RelayListenFamily = 'ipv4' | 'ipv6'

const props = defineProps<{ servers: ServerOption[] }>()

const relays = ref<RelayRecord[]>([])
const proxies = ref<ProxyOption[]>([])
const landings = ref<LandingRecord[]>([])
const loading = ref(true)
const submitting = ref(false)
const reorderingID = ref<number | null>(null)
const draggedID = ref<number | null>(null)
const dropTargetID = ref<number | null>(null)
const error = ref('')
const search = ref('')
const formOpen = ref(false)
const formMode = ref<'create' | 'edit'>('create')
const editingID = ref<number | null>(null)
const detailOpen = ref(false)
const selectedRelay = ref<RelayRecord | null>(null)
const relayClients = ref<RelayClientShare[]>([])
const relayLandingShare = ref<RelayLandingShare | null>(null)
const relayClientsLoading = ref(false)
const relayShareError = ref('')
const copiedRelayClientID = ref<number | null>(null)
const qrOpen = ref(false)
const qrURI = ref('')
const qrTitle = ref('')
const qrSubtitle = ref('')
const name = ref('')
const serverID = ref<number | null>(null)
const listenFamily = ref<RelayListenFamily>('ipv4')
const originalListenAddress = ref('0.0.0.0')
const listenPort = ref(9502)
const entryHostMode = ref<'auto' | 'manual'>('auto')
const entryHost = ref('')
const network = ref<RelayNetwork>('tcp')
const targetType = ref<RelayTargetType>('proxy')
const targetProxyID = ref<number | null>(null)
const targetClientID = ref<number | null>(null)
const targetLandingID = ref<number | null>(null)
const targetClients = ref<TargetClientOption[]>([])
const targetClientsLoading = ref(false)
const targetClientsError = ref('')
let targetClientsRequest = 0
const targetHost = ref('')
const targetPort = ref(443)
const enabled = ref(true)

const selectedServerPublicIPv4 = computed(() =>
  props.servers.find((server) => server.id === serverID.value)?.system_info?.public_ipv4 ?? '',
)
function serverSupportsRealm(id: number | null) {
  const server = props.servers.find((value) => value.id === id)
  return server !== undefined && agentSupportsCapability(server, agentCapabilities.relayRealm)
}
const hasRelayServer = computed(() => props.servers.some((server) => serverSupportsRealm(server.id)))
const selectedServerSupportsRealm = computed(() => serverSupportsRealm(serverID.value))
const relayCapabilityWarning = computed(() => selectedServerSupportsRealm.value ? '' : '当前 Agent 不支持 Realm 中转')

const filteredRelays = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  if (!keyword) return relays.value
  return relays.value.filter((value) =>
    [value.name, value.server_name, value.entry_address, relayTargetLabel(value), value.target_client_name]
      .some((field) => field.toLowerCase().includes(keyword)),
  )
})

async function run(action: () => Promise<void>) {
  submitting.value = true
  error.value = ''
  try {
    await action()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '操作失败'
    if (reason instanceof APIError && reason.status === 404) {
      setQRCodeOpen(false)
      detailOpen.value = false
      formOpen.value = false
      selectedRelay.value = null
      relayClients.value = []
      await Promise.all([loadRelays(), loadProxies(), loadLandings()]).catch(() => undefined)
    }
  } finally {
    submitting.value = false
  }
}

async function loadRelays() {
  const response = await api<{ relays: RelayRecord[] }>('/api/relays')
  relays.value = response.relays
}

async function reorderRelay(value: RelayRecord, targetID: number) {
  if (reorderingID.value !== null || search.value.trim()) return
  const move = moveRow(relays.value, value.id, targetID)
  if (!move) return
  reorderingID.value = value.id
  error.value = ''
  try {
    await persistMove(move, (direction) =>
      api(`/api/relays/${value.id}/reorder`, {
        method: 'POST',
        body: JSON.stringify({ direction }),
      }),
      loadRelays,
    )
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '调整中转顺序失败'
  } finally {
    reorderingID.value = null
  }
}

function startDrag(event: DragEvent, id: number) {
  if (reorderingID.value !== null || search.value.trim() || !event.dataTransfer) return
  draggedID.value = id
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', String(id))
}

function endDrag() {
  draggedID.value = null
  dropTargetID.value = null
}

function dragOver(event: DragEvent, id: number) {
  if (draggedID.value === null || draggedID.value === id || search.value.trim()) return
  event.preventDefault()
  dropTargetID.value = id
}

async function dropRelay(id: number) {
  const source = relays.value.find((row) => row.id === draggedID.value)
  endDrag()
  if (source && !search.value.trim()) await reorderRelay(source, id)
}

async function loadProxies() {
  const response = await api<{ proxies: ProxyOption[] }>('/api/proxies')
  proxies.value = response.proxies
}

async function loadLandings() {
  const response = await api<{ landings: LandingRecord[] }>('/api/landings')
  landings.value = response.landings
}

async function loadTargetClients(proxyID: number | null, selectedID: number | null = null) {
  const request = ++targetClientsRequest
  targetClients.value = []
  targetClientID.value = null
  targetClientsError.value = ''
  if (proxyID === null) {
    targetClientsLoading.value = false
    return
  }
  targetClientsLoading.value = true
  try {
    const response = await api<{ clients: TargetClientOption[] }>(`/api/proxies/${proxyID}/clients`)
    if (request !== targetClientsRequest || targetType.value !== 'proxy') return
    targetClients.value = response.clients
    if (selectedID !== null && response.clients.some((client) => client.id === selectedID)) {
      targetClientID.value = selectedID
    }
  } catch (reason) {
    if (request === targetClientsRequest) {
      targetClientsError.value = reason instanceof Error ? reason.message : '无法加载目标客户端'
    }
  } finally {
    if (request === targetClientsRequest) targetClientsLoading.value = false
  }
}

function onTargetProxyChange() {
  void loadTargetClients(targetProxyID.value)
}

function onTargetTypeChange() {
  if (targetType.value === 'proxy' && targetProxyID.value === null) {
    targetProxyID.value = proxies.value[0]?.id ?? null
  }
  if (targetType.value === 'landing' && targetLandingID.value === null) {
    targetLandingID.value = landings.value[0]?.id ?? null
  }
  if (targetType.value === 'landing') onTargetLandingChange()
  void loadTargetClients(targetType.value === 'proxy' ? targetProxyID.value : null)
}

function onTargetLandingChange() {
  const selected = landings.value.find((value) => value.id === targetLandingID.value)
  if (selected) network.value = selected.protocol === 'vless' ? 'tcp' : 'tcp,udp'
}

function relayListenFamily(address: string): RelayListenFamily {
  return address.includes(':') ? 'ipv6' : 'ipv4'
}

function selectedListenAddress() {
  if (formMode.value === 'edit' && listenFamily.value === relayListenFamily(originalListenAddress.value)) {
    return originalListenAddress.value
  }
  return listenFamily.value === 'ipv6' ? '::' : '0.0.0.0'
}

function resetForm() {
  editingID.value = null
  name.value = ''
  serverID.value = props.servers.find((server) => serverSupportsRealm(server.id))?.id ?? null
  listenFamily.value = 'ipv4'
  originalListenAddress.value = '0.0.0.0'
  listenPort.value = 9502
  entryHostMode.value = 'auto'
  entryHost.value = ''
  network.value = 'tcp'
  targetType.value = proxies.value.length > 0 ? 'proxy' : (landings.value.length > 0 ? 'landing' : 'manual')
  targetProxyID.value = proxies.value[0]?.id ?? null
  targetClientID.value = null
  targetLandingID.value = landings.value[0]?.id ?? null
  targetHost.value = ''
  targetPort.value = 443
  enabled.value = true
}

function openCreate() {
  resetForm()
  if (targetType.value === 'landing') onTargetLandingChange()
  void loadTargetClients(targetType.value === 'proxy' ? targetProxyID.value : null)
  error.value = ''
  formMode.value = 'create'
  formOpen.value = true
}

function openEdit(value: RelayRecord) {
  error.value = ''
  formMode.value = 'edit'
  editingID.value = value.id
  name.value = value.name
  serverID.value = value.server_id
  listenFamily.value = relayListenFamily(value.listen_address)
  originalListenAddress.value = value.listen_address
  listenPort.value = value.listen_port
  entryHostMode.value = value.entry_host_mode
  entryHost.value = value.entry_host
  network.value = value.network
  targetType.value = value.target_type
  targetProxyID.value = value.target_proxy_id
  targetLandingID.value = value.target_landing_id
  void loadTargetClients(value.target_type === 'proxy' ? value.target_proxy_id : null, value.target_client_id)
  targetHost.value = value.target_type === 'manual' ? value.target_host : ''
  targetPort.value = value.target_type === 'manual' ? value.target_port : 443
  enabled.value = value.enabled
  formOpen.value = true
}

async function saveRelay() {
  if (enabled.value && !selectedServerSupportsRealm.value) {
    error.value = '当前 Agent 不支持 Realm 中转'
    return
  }
  await run(async () => {
    const payload = {
      ...(formMode.value === 'create' ? { server_id: serverID.value } : {}),
      name: name.value,
      listen_address: selectedListenAddress(),
      listen_port: listenPort.value,
      entry_host_mode: entryHostMode.value,
      entry_host: entryHost.value,
      network: network.value,
      target_type: targetType.value,
      ...(targetType.value === 'proxy'
        ? { target_proxy_id: targetProxyID.value, target_client_id: targetClientID.value }
        : targetType.value === 'landing'
          ? { target_landing_id: targetLandingID.value }
          : { target_host: targetHost.value, target_port: targetPort.value }),
      enabled: enabled.value,
    }
    const path = formMode.value === 'create' ? '/api/relays' : `/api/relays/${editingID.value}`
    const response = await api<{ relay: RelayRecord }>(path, {
      method: formMode.value === 'create' ? 'POST' : 'PATCH',
      body: JSON.stringify(payload),
    })
    formOpen.value = false
    await loadRelays()
    if (selectedRelay.value?.id === response.relay.id) {
      selectedRelay.value = response.relay
      await loadRelayClientShares(response.relay)
    }
  })
}

async function loadRelayClientShares(value: RelayRecord) {
  relayClients.value = []
  relayLandingShare.value = null
  relayClientsLoading.value = false
  relayShareError.value = ''
  copiedRelayClientID.value = null
  if (!value.entry_address || !value.target_address_ready) return
  if (value.target_type === 'landing') {
    relayClientsLoading.value = true
    try {
      relayLandingShare.value = await api<RelayLandingShare>(`/api/relays/${value.id}/landing-share`)
    } catch (reason) {
      relayShareError.value = reason instanceof Error ? reason.message : '无法加载外部节点中转链接'
    } finally {
      relayClientsLoading.value = false
    }
    return
  }
  if (value.target_type !== 'proxy' || value.target_client_id === null) return
  relayClientsLoading.value = true
  try {
    const response = await api<{ clients: RelayClientShare[] }>(`/api/relays/${value.id}/clients`)
    relayClients.value = response.clients
  } catch (reason) {
    relayShareError.value = reason instanceof Error ? reason.message : '无法加载客户端节点链接'
  } finally {
    relayClientsLoading.value = false
  }
}

function setQRCodeOpen(show: boolean) {
  qrOpen.value = show
  if (!show) {
    qrURI.value = ''
    qrTitle.value = ''
    qrSubtitle.value = ''
  }
}

function showRelayQRCode(client: RelayClientShare) {
  if (!selectedRelay.value || selectedRelay.value.target_type !== 'proxy' || !client.network_compatible) return
  qrURI.value = client.uri
  qrTitle.value = `${selectedRelay.value.name} - ${client.client.name}`
  qrSubtitle.value = client.protocol === 'vless' ? 'VLESS · 中转' : 'Shadowsocks 2022 · 中转'
  qrOpen.value = true
}

function showLandingQRCode() {
  if (!selectedRelay.value || !relayLandingShare.value?.network_compatible) return
  qrURI.value = relayLandingShare.value.uri
  qrTitle.value = selectedRelay.value.name
  qrSubtitle.value = `${landingProtocolLabel(relayLandingShare.value.landing.protocol)} · 外部节点中转`
  qrOpen.value = true
}

async function showRelay(id: number) {
  await run(async () => {
    const response = await api<{ relay: RelayRecord }>(`/api/relays/${id}`)
    selectedRelay.value = response.relay
    detailOpen.value = true
    await loadRelayClientShares(response.relay)
  })
}

async function copyRelayClientURI(value: RelayClientShare) {
  try {
    await navigator.clipboard.writeText(value.uri)
    copiedRelayClientID.value = value.client.id
  } catch {
    relayShareError.value = '复制链接失败，请手动复制'
  }
}

async function copyLandingURI() {
  if (!relayLandingShare.value) return
  try {
    await navigator.clipboard.writeText(relayLandingShare.value.uri)
    copiedRelayClientID.value = 0
  } catch {
    relayShareError.value = '复制链接失败，请手动复制'
  }
}

function relayClientStatusLabel(value: RelayClientShare) {
  return value.network_compatible ? clientStatusLabel(value.client.status) : 'Network 不兼容'
}

function relayClientStatusTagType(value: RelayClientShare) {
  return value.network_compatible ? clientStatusTagType(value.client.status) : 'error'
}

async function toggleRelay(value: RelayRecord) {
  if (!value.enabled && !serverSupportsRealm(value.server_id)) return
  await run(async () => {
    await api(`/api/relays/${value.id}`, {
      method: 'PATCH',
      body: JSON.stringify({ enabled: !value.enabled }),
    })
    await loadRelays()
  })
}

async function removeRelay(value: RelayRecord) {
  if (!window.confirm(`确定删除中转“${value.name}”吗？`)) return
  await run(async () => {
    await api(`/api/relays/${value.id}`, { method: 'DELETE' })
    if (selectedRelay.value?.id === value.id) {
      setQRCodeOpen(false)
      selectedRelay.value = null
      detailOpen.value = false
    }
    await loadRelays()
  })
}

watch(
  () => props.servers.map((server) => server.id).join(','),
  async () => {
    try {
      await Promise.all([loadRelays(), loadProxies(), loadLandings()])
      if (selectedRelay.value && !relays.value.some((value) => value.id === selectedRelay.value?.id)) {
        setQRCodeOpen(false)
        selectedRelay.value = null
        detailOpen.value = false
        relayClients.value = []
        error.value = '中转规则不存在或当前账号无权访问'
      }
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : '无法加载中转规则'
    }
  },
)

onMounted(async () => {
  try {
    await Promise.all([loadRelays(), loadProxies(), loadLandings()])
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法加载中转规则'
  } finally {
    loading.value = false
  }
})
import {
  api,
  APIError,
} from '../api/client'
import {
  formatTime,
} from '../format'
</script>

<template>
<n-alert v-if="error" class="page-alert" type="error" closable @close="error = ''">
    {{ error }}
  </n-alert>

  <div class="relay-toolbar">
    <n-input v-model:value="search" clearable placeholder="搜索名称、服务器、入口地址或目标" />
    <div class="relay-toolbar-actions">
      <n-button type="primary" :disabled="!hasRelayServer" :title="hasRelayServer ? undefined : '当前没有支持 Realm 中转的服务器'" @click="openCreate">
        新增中转
      </n-button>
    </div>
  </div>

  <n-card :bordered="true">
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载中转规则…</span></div>
    <n-empty v-else-if="filteredRelays.length === 0" description="当前没有中转规则" />
    <div v-else class="server-table-wrap">
      <table class="server-table relay-table">
        <thead><tr><th class="reorder-cell" aria-label="排序"></th><th>名称</th><th>服务器</th><th>入口地址</th><th>监听端口</th><th>目标</th><th>客户端</th><th>Network</th><th>状态</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="value in filteredRelays" :key="value.id" :class="{ 'row-dragging': draggedID === value.id, 'row-drop-target': dropTargetID === value.id }" @dragover="dragOver($event, value.id)" @dragleave="dropTargetID === value.id && (dropTargetID = null)" @drop.prevent="dropRelay(value.id)">
            <td class="reorder-cell">
              <span class="drag-handle" :class="{ 'drag-handle--disabled': reorderingID !== null || !!search.trim() }" :title="search.trim() ? '清除搜索后可调整顺序' : '拖动排序'" :draggable="reorderingID === null && !search.trim()" aria-label="拖动中转排序" @dragstart="startDrag($event, value.id)" @dragend="endDrag"><span></span><span></span><span></span></span>
            </td>
            <td>{{ value.name }}</td>
            <td>{{ value.server_name }}</td>
            <td>
              <span>{{ value.entry_address || '入口地址不可用' }}</span>
              <small class="secondary-text">{{ value.entry_host_mode === 'auto' ? '自动检测' : '手动' }}</small>
            </td>
            <td>{{ value.listen_port }}</td>
            <td>{{ relayTargetLabel(value) }}</td>
            <td>{{ value.target_client_name || '—' }}</td>
            <td>{{ relayNetworkLabel(value.network) }}</td>
            <td><n-tag :type="value.enabled ? 'success' : 'default'" size="small">{{ value.enabled ? '启用' : '禁用' }}</n-tag></td>
            <td class="server-actions">
              <n-button size="small" secondary @click="showRelay(value.id)">查看</n-button>
              <n-button size="small" secondary @click="openEdit(value)">编辑</n-button>
              <n-button size="small" secondary :disabled="!value.enabled && !serverSupportsRealm(value.server_id)" :title="!value.enabled && !serverSupportsRealm(value.server_id) ? '当前 Agent 不支持 Realm 中转' : undefined" @click="toggleRelay(value)">{{ value.enabled ? '禁用' : '启用' }}</n-button>
              <n-button size="small" type="error" secondary @click="removeRelay(value)">删除</n-button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </n-card>

  <n-modal v-model:show="formOpen" :mask-closable="!submitting">
    <n-card class="relay-form-card" :title="formMode === 'create' ? '新增中转' : '编辑中转'" :bordered="false" closable @close="formOpen = false">
      <form class="relay-form" @submit.prevent="saveRelay">
        <n-alert v-if="error" type="error">{{ error }}</n-alert>
        <label><span>名称</span><n-input v-model:value="name" maxlength="100" /></label>
        <label>
          <span>源服务器</span>
          <select v-model.number="serverID" class="settings-input" :disabled="formMode === 'edit'">
            <option v-for="server in props.servers" :key="server.id" :value="server.id" :disabled="!serverSupportsRealm(server.id)">{{ server.name }}</option>
          </select>
        </label>
        <label>
          <span>监听协议</span>
          <select v-model="listenFamily" class="settings-input">
            <option value="ipv4">IPv4</option><option value="ipv6">IPv6</option>
          </select>
        </label>
        <p>实际监听地址：{{ selectedListenAddress() }}</p>
        <label><span>监听端口</span><input v-model.number="listenPort" class="settings-input" type="number" min="1" max="65535" /></label>
        <label>
          <span>客户端入口</span>
          <select v-model="entryHostMode" class="settings-input">
            <option value="auto">自动检测公网 IPv4</option><option value="manual">手动填写</option>
          </select>
        </label>
        <label v-if="entryHostMode === 'manual'"><span>入口 IP / 域名</span><n-input v-model:value="entryHost" placeholder="例如：1.2.3.4、2001:db8::1 或 relay.example.com" /></label>
        <p v-else>自动使用源服务器公网 IPv4。当前公网 IPv4：{{ selectedServerPublicIPv4 || '未检测到' }}</p>
        <label>
          <span>Network</span>
          <select v-model="network" class="settings-input"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="tcp,udp">TCP + UDP</option></select>
        </label>
        <label>
          <span>目标类型</span>
          <select v-model="targetType" class="settings-input" @change="onTargetTypeChange"><option value="proxy">Panel Proxy</option><option value="landing">外部节点</option><option value="manual">手动地址</option></select>
        </label>
        <label v-if="targetType === 'proxy'">
          <span>目标 Proxy</span>
          <select v-model.number="targetProxyID" class="settings-input" @change="onTargetProxyChange">
            <option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">
              {{ proxy.name }} · {{ proxy.server_name }} · {{ proxy.entry_address || '入口未检测' }}:{{ proxy.listen_port }} · {{ proxy.protocol === 'vless' ? 'VLESS' : 'Shadowsocks' }}
            </option>
          </select>
        </label>
        <template v-if="targetType === 'proxy'">
          <label>
            <span>目标客户端</span>
            <select v-model.number="targetClientID" class="settings-input" :disabled="targetClientsLoading || targetClients.length === 0">
              <option :value="null">请选择目标客户端</option>
              <option v-for="client in targetClients" :key="client.id" :value="client.id">{{ client.name }} · {{ clientStatusLabel(client.status) }}</option>
            </select>
          </label>
          <n-alert v-if="targetClientsError" type="error">{{ targetClientsError }}</n-alert>
          <p v-else-if="targetClientsLoading">正在加载目标客户端…</p>
          <n-alert v-else-if="targetProxyID !== null && targetClients.length === 0" type="warning">该 Proxy 暂无客户端，请先在代理节点中创建客户端</n-alert>
          <p v-else>中转分享链接使用所选客户端的现有凭据；Relay 转发仍允许目标 Proxy 的其他有效凭据连接。</p>
        </template>
        <template v-else-if="targetType === 'landing'">
          <label>
            <span>目标外部节点</span>
            <select v-model.number="targetLandingID" class="settings-input" @change="onTargetLandingChange">
              <option v-for="landing in landings" :key="landing.id" :value="landing.id">
                {{ landing.name }} · {{ landingProtocolLabel(landing.protocol) }} · {{ relayEndpointLabel(landing.host, landing.port) }} · {{ landing.visibility === 'public' ? '公开' : '私有' }}
              </option>
            </select>
          </label>
          <n-alert v-if="landings.length === 0" type="warning">暂无可用外部节点，请先前往“代理节点 → 外部节点”导入。</n-alert>
          <p v-else>{{ landings.find((value) => value.id === targetLandingID)?.protocol === 'vless' ? 'VLESS 建议使用 TCP' : 'Shadowsocks 建议使用 TCP + UDP' }}</p>
        </template>
        <template v-else>
          <label><span>目标 Host / IP</span><n-input v-model:value="targetHost" placeholder="例如：node.example.com 或 2001:db8::1" /></label>
          <label><span>目标端口</span><input v-model.number="targetPort" class="settings-input" type="number" min="1" max="65535" /></label>
        </template>
        <n-alert v-if="relayCapabilityWarning" type="warning">{{ relayCapabilityWarning }}</n-alert>
        <label class="checkbox-row"><input v-model="enabled" type="checkbox" /><span>启用中转</span></label>
        <div class="modal-actions"><n-button @click="formOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="submitting" :disabled="(enabled && !selectedServerSupportsRealm) || (targetType === 'proxy' && (targetClientID === null || targetClientsLoading || !!targetClientsError)) || (targetType === 'landing' && targetLandingID === null)">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>

  <n-modal v-if="selectedRelay" v-model:show="detailOpen">
    <n-card class="relay-detail-card" title="中转详情" :bordered="false" closable @close="detailOpen = false">
      <dl class="server-details">
        <div><dt>名称</dt><dd>{{ selectedRelay.name }}</dd></div><div><dt>服务器</dt><dd>{{ selectedRelay.server_name }}</dd></div>
        <div><dt>入口模式</dt><dd>{{ selectedRelay.entry_host_mode === 'auto' ? '自动检测' : '手动输入' }}</dd></div><div><dt>客户端入口</dt><dd>{{ relayEndpointLabel(selectedRelay.entry_address, selectedRelay.listen_port) }}</dd></div>
        <div><dt>监听地址</dt><dd>{{ relayEndpointLabel(selectedRelay.listen_address, selectedRelay.listen_port) }}</dd></div><div><dt>目标类型</dt><dd>{{ selectedRelay.target_type === 'proxy' ? 'Panel Proxy' : selectedRelay.target_type === 'landing' ? '外部节点' : '手动地址' }}</dd></div>
        <div><dt>目标</dt><dd>{{ relayTargetLabel(selectedRelay) }}</dd></div><div><dt>Network</dt><dd>{{ relayNetworkLabel(selectedRelay.network) }}</dd></div>
        <div v-if="selectedRelay.target_type === 'landing'"><dt>外部节点</dt><dd>{{ selectedRelay.target_landing_name }}</dd></div><div v-if="selectedRelay.target_type === 'landing'"><dt>协议</dt><dd>{{ selectedRelay.target_landing_protocol === 'vless' ? 'VLESS' : 'Shadowsocks' }}</dd></div>
        <div v-if="selectedRelay.target_type === 'landing'"><dt>可见性</dt><dd>{{ selectedRelay.target_landing_visibility === 'public' ? '公开' : '私有' }}</dd></div>
        <div><dt>状态</dt><dd>{{ selectedRelay.enabled ? '启用' : '禁用' }}</dd></div><div><dt>创建时间</dt><dd>{{ formatTime(selectedRelay.created_at) }}</dd></div>
        <div><dt>更新时间</dt><dd>{{ formatTime(selectedRelay.updated_at) }}</dd></div>
      </dl>
      <h3>{{ selectedRelay.target_type === 'landing' ? '外部节点中转链接' : '目标客户端' }}</h3>
      <n-alert v-if="selectedRelay.target_type === 'manual'" type="info">手动目标不支持自动生成客户端节点链接</n-alert>
      <n-alert v-else-if="selectedRelay.target_type === 'proxy' && selectedRelay.target_client_id === null" type="info">尚未选择目标客户端，请编辑中转后选择</n-alert>
      <n-alert v-else-if="!selectedRelay.entry_address" type="warning">入口地址不可用，请填写手动入口地址或等待源服务器上报公网 IPv4</n-alert>
      <n-alert v-else-if="!selectedRelay.target_address_ready" type="warning">目标代理节点入口地址不可用</n-alert>
      <n-alert v-else-if="relayShareError" type="error">{{ relayShareError }}</n-alert>
      <div v-else-if="relayClientsLoading" class="loading-row"><n-spin size="small" /><span>正在加载客户端节点…</span></div>
      <div v-else-if="selectedRelay.target_type === 'landing' && relayLandingShare" class="relay-client-list">
        <div class="relay-client-share">
          <div class="relay-client-heading">
            <strong>{{ relayLandingShare.landing.name }}</strong>
            <span>{{ landingProtocolLabel(relayLandingShare.landing.protocol) }}</span>
            <n-tag :type="relayLandingShare.network_compatible ? 'success' : 'error'" size="small">{{ relayLandingShare.network_compatible ? '可用' : 'Network 不兼容' }}</n-tag>
          </div>
          <n-alert v-if="relayLandingShare.network_notice" :type="relayLandingShare.network_compatible ? 'warning' : 'error'">{{ relayLandingShare.network_notice }}</n-alert>
          <strong>中转 URI</strong>
          <n-input :value="relayLandingShare.uri" type="textarea" readonly :autosize="{ minRows: 3 }" />
          <div class="modal-actions"><n-button secondary :disabled="!relayLandingShare.network_compatible" @click="showLandingQRCode">二维码</n-button><n-button type="primary" :disabled="!relayLandingShare.network_compatible" @click="copyLandingURI">{{ copiedRelayClientID === 0 ? '链接已复制' : '复制链接' }}</n-button></div>
        </div>
      </div>
      <n-empty v-else-if="relayClients.length === 0" description="目标客户端不可用" />
      <div v-else class="relay-client-list">
        <div v-for="client in relayClients" :key="client.client.id" class="relay-client-share">
          <div class="relay-client-heading">
            <strong>{{ client.client.name }}</strong>
            <span>{{ client.protocol === 'vless' ? 'VLESS' : 'Shadowsocks 2022' }}</span>
            <n-tag :type="relayClientStatusTagType(client)" size="small">{{ relayClientStatusLabel(client) }}</n-tag>
          </div>
          <n-alert v-if="client.network_notice" :type="client.network_compatible ? 'warning' : 'error'">{{ client.network_notice }}</n-alert>
          <strong>中转 URI</strong>
          <n-input :value="client.uri" type="textarea" readonly :autosize="{ minRows: 3 }" />
          <div class="modal-actions"><n-button secondary :disabled="!client.network_compatible" @click="showRelayQRCode(client)">二维码</n-button><n-button type="primary" :disabled="!client.network_compatible" @click="copyRelayClientURI(client)">{{ copiedRelayClientID === client.client.id ? '链接已复制' : '复制链接' }}</n-button></div>
        </div>
      </div>
      <div class="modal-actions"><n-button secondary @click="openEdit(selectedRelay)">编辑</n-button><n-button @click="detailOpen = false">关闭</n-button></div>
    </n-card>
  </n-modal>
  <QRCodeModal :show="qrOpen" :uri="qrURI" :title="qrTitle" :subtitle="qrSubtitle" @update:show="setQRCodeOpen" />
</template>
