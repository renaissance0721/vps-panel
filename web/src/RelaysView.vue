<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NModal, NSpin, NTag } from 'naive-ui'
import {
  relayNetworkLabel,
  relayTargetLabel,
  type RelayNetwork,
  type RelayTargetType,
} from './relay'

type ServerOption = {
  id: number
  name: string
  system_info: { public_ipv4: string } | null
}

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
  target_type: RelayTargetType
  target_proxy_id: number | null
  target_proxy_name: string
  target_host: string
  target_port: number
  target_address_ready: boolean
  network: RelayNetwork
  enabled: boolean
  created_at: string
  updated_at: string
}

const props = defineProps<{ servers: ServerOption[] }>()

const relays = ref<RelayRecord[]>([])
const proxies = ref<ProxyOption[]>([])
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const search = ref('')
const formOpen = ref(false)
const formMode = ref<'create' | 'edit'>('create')
const editingID = ref<number | null>(null)
const detailOpen = ref(false)
const selectedRelay = ref<RelayRecord | null>(null)

const name = ref('')
const serverID = ref<number | null>(null)
const listenPort = ref(9502)
const network = ref<RelayNetwork>('tcp')
const targetType = ref<RelayTargetType>('proxy')
const targetProxyID = ref<number | null>(null)
const targetHost = ref('')
const targetPort = ref(443)
const enabled = ref(true)

const filteredRelays = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  if (!keyword) return relays.value
  return relays.value.filter((value) =>
    [value.name, value.server_name, value.server_public_ipv4, relayTargetLabel(value)]
      .some((field) => field.toLowerCase().includes(keyword)),
  )
})

async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    cache: 'no-store',
    credentials: 'same-origin',
    ...options,
    headers: options?.body
      ? { 'Content-Type': 'application/json', ...options.headers }
      : options?.headers,
  })
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { error?: string } | null
    throw new Error(body?.error ?? `请求失败（${response.status}）`)
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

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

async function loadRelays() {
  const response = await api<{ relays: RelayRecord[] }>('/api/relays')
  relays.value = response.relays
}

async function loadProxies() {
  const response = await api<{ proxies: ProxyOption[] }>('/api/proxies')
  proxies.value = response.proxies
}

function resetForm() {
  editingID.value = null
  name.value = ''
  serverID.value = props.servers[0]?.id ?? null
  listenPort.value = 9502
  network.value = 'tcp'
  targetType.value = proxies.value.length === 0 ? 'manual' : 'proxy'
  targetProxyID.value = proxies.value[0]?.id ?? null
  targetHost.value = ''
  targetPort.value = 443
  enabled.value = true
}

function openCreate() {
  resetForm()
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
  listenPort.value = value.listen_port
  network.value = value.network
  targetType.value = value.target_type
  targetProxyID.value = value.target_proxy_id
  targetHost.value = value.target_type === 'manual' ? value.target_host : ''
  targetPort.value = value.target_type === 'manual' ? value.target_port : 443
  enabled.value = value.enabled
  formOpen.value = true
}

async function saveRelay() {
  await run(async () => {
    const payload = {
      ...(formMode.value === 'create' ? { server_id: serverID.value } : {}),
      name: name.value,
      ...(formMode.value === 'create' ? { listen_address: '0.0.0.0' } : {}),
      listen_port: listenPort.value,
      network: network.value,
      target_type: targetType.value,
      ...(targetType.value === 'proxy'
        ? { target_proxy_id: targetProxyID.value }
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
    if (selectedRelay.value?.id === response.relay.id) selectedRelay.value = response.relay
  })
}

async function showRelay(id: number) {
  await run(async () => {
    const response = await api<{ relay: RelayRecord }>(`/api/relays/${id}`)
    selectedRelay.value = response.relay
    detailOpen.value = true
  })
}

async function toggleRelay(value: RelayRecord) {
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
      selectedRelay.value = null
      detailOpen.value = false
    }
    await loadRelays()
  })
}

function formatTime(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

watch(targetType, (value) => {
  if (value === 'proxy' && targetProxyID.value === null) {
    targetProxyID.value = proxies.value[0]?.id ?? null
  }
})

onMounted(async () => {
  try {
    await Promise.all([loadRelays(), loadProxies()])
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法加载中转规则'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <n-alert v-if="error" class="page-alert" type="error" closable @close="error = ''">
    {{ error }}
  </n-alert>

  <div class="relay-toolbar">
    <n-input v-model:value="search" clearable placeholder="搜索名称、服务器、入口 IP 或目标" />
    <n-button type="primary" :disabled="props.servers.length === 0" @click="openCreate">
      新增中转
    </n-button>
  </div>

  <n-card :bordered="true">
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载中转规则…</span></div>
    <n-empty v-else-if="filteredRelays.length === 0" description="当前没有中转规则" />
    <div v-else class="server-table-wrap">
      <table class="server-table relay-table">
        <thead><tr><th>名称</th><th>服务器</th><th>入口 IP</th><th>监听端口</th><th>目标</th><th>Network</th><th>状态</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="value in filteredRelays" :key="value.id">
            <td>{{ value.name }}</td>
            <td>{{ value.server_name }}</td>
            <td>{{ value.server_public_ipv4 || '未检测' }}</td>
            <td>{{ value.listen_port }}</td>
            <td>{{ relayTargetLabel(value) }}</td>
            <td>{{ relayNetworkLabel(value.network) }}</td>
            <td><n-tag :type="value.enabled ? 'success' : 'default'" size="small">{{ value.enabled ? '启用' : '禁用' }}</n-tag></td>
            <td class="server-actions">
              <n-button size="small" secondary @click="showRelay(value.id)">查看</n-button>
              <n-button size="small" secondary @click="openEdit(value)">编辑</n-button>
              <n-button size="small" secondary @click="toggleRelay(value)">{{ value.enabled ? '禁用' : '启用' }}</n-button>
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
            <option v-for="server in props.servers" :key="server.id" :value="server.id">{{ server.name }}</option>
          </select>
        </label>
        <label><span>监听端口</span><input v-model.number="listenPort" class="settings-input" type="number" min="1" max="65535" /></label>
        <label>
          <span>Network</span>
          <select v-model="network" class="settings-input"><option value="tcp">TCP</option><option value="udp">UDP</option><option value="tcp,udp">TCP + UDP</option></select>
        </label>
        <label>
          <span>目标类型</span>
          <select v-model="targetType" class="settings-input"><option value="proxy">Panel Proxy</option><option value="manual">手动地址</option></select>
        </label>
        <label v-if="targetType === 'proxy'">
          <span>目标 Proxy</span>
          <select v-model.number="targetProxyID" class="settings-input">
            <option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">
              {{ proxy.name }} · {{ proxy.server_name }} · {{ proxy.entry_address || '入口未检测' }}:{{ proxy.listen_port }} · {{ proxy.protocol === 'vless' ? 'VLESS' : 'Shadowsocks' }}
            </option>
          </select>
        </label>
        <template v-else>
          <label><span>目标 Host / IP</span><n-input v-model:value="targetHost" placeholder="例如：node.example.com 或 2001:db8::1" /></label>
          <label><span>目标端口</span><input v-model.number="targetPort" class="settings-input" type="number" min="1" max="65535" /></label>
        </template>
        <label class="checkbox-row"><input v-model="enabled" type="checkbox" /><span>启用中转</span></label>
        <div class="modal-actions"><n-button @click="formOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>

  <n-modal v-if="selectedRelay" v-model:show="detailOpen">
    <n-card class="relay-detail-card" title="中转详情" :bordered="false" closable @close="detailOpen = false">
      <dl class="server-details">
        <div><dt>名称</dt><dd>{{ selectedRelay.name }}</dd></div><div><dt>服务器</dt><dd>{{ selectedRelay.server_name }}</dd></div>
        <div><dt>入口 IP</dt><dd>{{ selectedRelay.server_public_ipv4 || '未检测' }}</dd></div><div><dt>监听地址</dt><dd>{{ selectedRelay.listen_address }}:{{ selectedRelay.listen_port }}</dd></div>
        <div><dt>目标类型</dt><dd>{{ selectedRelay.target_type === 'proxy' ? 'Panel Proxy' : '手动地址' }}</dd></div><div><dt>目标</dt><dd>{{ relayTargetLabel(selectedRelay) }}</dd></div>
        <div><dt>Network</dt><dd>{{ relayNetworkLabel(selectedRelay.network) }}</dd></div><div><dt>状态</dt><dd>{{ selectedRelay.enabled ? '启用' : '禁用' }}</dd></div>
        <div><dt>创建时间</dt><dd>{{ formatTime(selectedRelay.created_at) }}</dd></div><div><dt>更新时间</dt><dd>{{ formatTime(selectedRelay.updated_at) }}</dd></div>
      </dl>
      <div class="modal-actions"><n-button secondary @click="openEdit(selectedRelay)">编辑</n-button><n-button @click="detailOpen = false">关闭</n-button></div>
    </n-card>
  </n-modal>
</template>
