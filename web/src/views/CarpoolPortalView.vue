<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NModal, NProgress, NRadio, NRadioGroup, NSpin } from 'naive-ui'
import { api } from '../api/client'
import { formatTime } from '../format'
import { formatClientTrafficBytes } from '../proxy'
import type { User } from '../types/auth'
import QRCodeModal from '../components/share/QRCodeModal.vue'
import AccountMenu from '../components/AccountMenu.vue'

type UserNode = {
  client_id: number
  client_name: string
  server_name: string
  proxy_name: string
  protocol: 'vless' | 'shadowsocks'
  status: string
  enabled: boolean
  effective_enabled: boolean
  traffic_used_bytes: number
  traffic_limit_bytes: number | null
  expires_at: string | null
  billing_period_months: 1 | 3 | 6 | 12 | null
  next_reset_at: string | null
}

type RelaySource = {
  client_id: number
  server_name: string
  proxy_name: string
  protocol: 'vless' | 'shadowsocks'
  effective_enabled: boolean
}
type RelayNode = { server_name: string; proxy_name: string }
type UserRelay = {
  id: number
  name: string
  mode: 'assigned_node' | 'custom'
  source: RelayNode
  target?: RelayNode
  target_ip?: string
  target_port?: number
  entry_address: string
  enabled: boolean
}
type NodeShare = { uri: string; protocol: 'vless' | 'shadowsocks'; server_name: string; proxy_name: string }
type RelayShare = { uri: string; protocol: 'vless' | 'shadowsocks'; name: string }

const props = defineProps<{ user: User }>()
const emit = defineEmits<{ logout: []; userUpdated: [user: User] }>()
const nodes = ref<UserNode[]>([])
const relaySources = ref<RelaySource[]>([])
const relays = ref<UserRelay[]>([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const copiedNodeID = ref<number | null>(null)
const copiedRelayID = ref<number | null>(null)
const qrOpen = ref(false)
const qrURI = ref('')
const qrTitle = ref('')
const qrSubtitle = ref('')
const relayModalOpen = ref(false)
const relayName = ref('')
const relayMode = ref<'assigned_node' | 'custom'>('assigned_node')
const sourceClientID = ref(0)
const targetClientID = ref(0)
const relayTargetIP = ref('')
const relayTargetPort = ref<number | null>(null)
const editingRelayID = ref<number | null>(null)
const nodeModalOpen = ref(false)
const editingNode = ref<UserNode | null>(null)
const nodeName = ref('')

const targetSources = computed(() => relaySources.value.filter((source) => source.client_id !== sourceClientID.value))

async function loadPortal() {
  const results = await Promise.allSettled([loadNodes(), loadRelaySources(), loadRelays()])
  const failed = results.find((result) => result.status === 'rejected')
  if (failed?.status === 'rejected') {
    error.value = failed.reason instanceof Error ? failed.reason.message : '部分拼车门户数据加载失败'
  }
}

async function loadNodes() {
  nodes.value = (await api<{ nodes: UserNode[] }>('/api/me/nodes')).nodes
}

async function loadRelaySources() {
  relaySources.value = (await api<{ sources: RelaySource[] }>('/api/me/relay-sources')).sources
}

async function loadRelays() {
  relays.value = (await api<{ relays: UserRelay[] }>('/api/me/relays')).relays
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

async function getNodeShare(node: UserNode) {
  return (await api<{ share: NodeShare }>(`/api/me/nodes/${node.client_id}/share`)).share
}

async function getRelayShare(relay: UserRelay) {
  return (await api<{ share: RelayShare }>(`/api/me/relays/${relay.id}/share`)).share
}

async function copyNode(node: UserNode) {
  await run(async () => {
    const share = await getNodeShare(node)
    await navigator.clipboard.writeText(share.uri)
    copiedNodeID.value = node.client_id
  })
}

async function showNodeQR(node: UserNode) {
  await run(async () => {
    const share = await getNodeShare(node)
    qrURI.value = share.uri
    qrTitle.value = `${share.server_name} · ${share.proxy_name}`
    qrSubtitle.value = share.protocol === 'vless' ? 'VLESS' : 'Shadowsocks 2022'
    qrOpen.value = true
  })
}

function openNodeEdit(node: UserNode) {
  editingNode.value = node
  nodeName.value = node.client_name
  nodeModalOpen.value = true
}

async function saveNode() {
  const node = editingNode.value
  if (!node) return
  await run(async () => {
    await api(`/api/me/nodes/${node.client_id}`, {
      method: 'PATCH',
      body: JSON.stringify({ name: nodeName.value.trim() }),
    })
    nodeModalOpen.value = false
    await loadNodes()
  })
}

async function copyRelay(relay: UserRelay) {
  await run(async () => {
    const share = await getRelayShare(relay)
    await navigator.clipboard.writeText(share.uri)
    copiedRelayID.value = relay.id
  })
}

async function showRelayQR(relay: UserRelay) {
  await run(async () => {
    const share = await getRelayShare(relay)
    qrURI.value = share.uri
    qrTitle.value = share.name
    qrSubtitle.value = share.protocol === 'vless' ? 'VLESS · 中转' : 'Shadowsocks 2022 · 中转'
    qrOpen.value = true
  })
}

function nodeUsagePercent(node: UserNode) {
  if (!node.traffic_limit_bytes || node.traffic_limit_bytes <= 0) return 0
  return Math.min(100, Math.round(node.traffic_used_bytes / node.traffic_limit_bytes * 100))
}

function nodeStatus(node: UserNode) {
  const labels: Record<string, string> = { normal: '可用', warning: '流量预警', exhausted: '流量已用完', expired: '已到期', disabled: '已停用' }
  return labels[node.status] ?? node.status
}

function billingLabel(months: UserNode['billing_period_months']) {
  return ({ 1: '月付', 3: '季付', 6: '半年', 12: '年付' } as Record<number, string>)[months ?? 0] ?? '未设置'
}

function openRelayModal() {
  editingRelayID.value = null
  relayName.value = ''
  relayMode.value = 'assigned_node'
  sourceClientID.value = relaySources.value[0]?.client_id ?? 0
  targetClientID.value = relaySources.value.find((source) => source.client_id !== sourceClientID.value)?.client_id ?? 0
  relayTargetIP.value = ''
  relayTargetPort.value = null
  relayModalOpen.value = true
}

function onSourceChange() {
  if (targetClientID.value === sourceClientID.value || !targetSources.value.some((source) => source.client_id === targetClientID.value)) {
    targetClientID.value = targetSources.value[0]?.client_id ?? 0
  }
}

function editCustomRelay(relay: UserRelay) {
  editingRelayID.value = relay.id
  relayName.value = relay.name
  relayMode.value = 'custom'
  relayTargetIP.value = relay.target_ip ?? ''
  relayTargetPort.value = relay.target_port ?? null
  relayModalOpen.value = true
}

async function saveRelay() {
  await run(async () => {
    if (editingRelayID.value !== null) {
      await api(`/api/me/relays/${editingRelayID.value}`, {
        method: 'PATCH',
        body: JSON.stringify({ target_ip: relayTargetIP.value, target_port: relayTargetPort.value }),
      })
    } else {
      await api('/api/me/relays', {
        method: 'POST',
        body: JSON.stringify({
          source_client_id: sourceClientID.value,
          name: relayName.value,
          mode: relayMode.value,
          ...(relayMode.value === 'assigned_node'
            ? { target_client_id: targetClientID.value }
            : { target_ip: relayTargetIP.value, target_port: relayTargetPort.value }),
        }),
      })
    }
    relayModalOpen.value = false
    await loadRelays()
  })
}

async function deleteRelay(relay: UserRelay) {
  if (!window.confirm(`确定删除中转“${relay.name}”吗？`)) return
  await run(async () => {
    await api(`/api/me/relays/${relay.id}`, { method: 'DELETE' })
    await loadRelays()
  })
}

onMounted(async () => {
  await loadPortal()
  loading.value = false
})
</script>

<template>
  <main class="user-portal">
    <header class="user-portal-header">
      <strong class="app-brand app-brand--portal">夕凪云</strong>
      <AccountMenu :user="props.user" @updated="emit('userUpdated', $event)" @logout="emit('logout')" />
    </header>
    <n-alert v-if="error" type="error" class="page-alert">{{ error }}</n-alert>
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载…</span></div>
    <template v-else>
      <section>
        <h2 class="portal-section-title">我的节点</h2>
        <n-empty v-if="nodes.length === 0" description="管理员尚未分配节点" />
        <div v-else class="user-card-grid">
          <n-card v-for="node in nodes" :key="node.client_id" :title="node.server_name" :bordered="true">
            <template #header-extra><span :class="['user-status', { online: node.effective_enabled }]">● {{ nodeStatus(node) }}</span></template>
            <p class="secondary-text">{{ node.proxy_name }}</p>
            <n-progress type="line" :percentage="nodeUsagePercent(node)" :show-indicator="false" />
            <strong>{{ formatClientTrafficBytes(node.traffic_used_bytes) }} / {{ node.traffic_limit_bytes ? formatClientTrafficBytes(node.traffic_limit_bytes) : '不限' }}</strong>
            <dl class="user-details"><div><dt>到期</dt><dd>{{ node.expires_at ? formatTime(node.expires_at) : '不限' }}</dd></div><div><dt>付款周期</dt><dd>{{ billingLabel(node.billing_period_months) }}</dd></div></dl>
            <div class="modal-actions"><n-button secondary :disabled="busy" @click="openNodeEdit(node)">编辑</n-button><n-button secondary :disabled="busy" @click="copyNode(node)">{{ copiedNodeID === node.client_id ? '已复制' : '复制链接' }}</n-button><n-button type="primary" :disabled="busy" @click="showNodeQR(node)">二维码</n-button></div>
          </n-card>
        </div>
      </section>
      <section>
        <div class="section-heading"><h2 class="portal-section-title">我的中转</h2><n-button type="primary" :disabled="relaySources.length === 0 || busy" @click="openRelayModal">添加中转</n-button></div>
        <n-empty v-if="relays.length === 0" description="暂无中转" />
        <div v-else class="user-card-grid">
          <n-card v-for="relay in relays" :key="relay.id" :title="relay.name" :bordered="true">
            <p>{{ relay.source.server_name }} · {{ relay.source.proxy_name }}</p>
            <p class="secondary-text">→ {{ relay.mode === 'assigned_node' && relay.target ? `${relay.target.server_name} · ${relay.target.proxy_name}` : '自定义落地' }}</p>
            <p v-if="relay.mode === 'custom'">落地：{{ relay.target_ip }}:{{ relay.target_port }}</p>
            <p>入口：<strong>{{ relay.entry_address }}</strong></p>
            <div class="modal-actions"><n-button secondary @click="copyRelay(relay)">{{ copiedRelayID === relay.id ? '已复制' : '复制链接' }}</n-button><n-button type="primary" secondary @click="showRelayQR(relay)">二维码</n-button><n-button v-if="relay.mode === 'custom'" secondary @click="editCustomRelay(relay)">修改落地</n-button><n-button type="error" secondary :disabled="busy" @click="deleteRelay(relay)">删除</n-button></div>
          </n-card>
        </div>
      </section>
    </template>
  </main>

  <n-modal v-model:show="nodeModalOpen"><n-card class="client-form-card" title="编辑节点" closable @close="nodeModalOpen = false"><form class="auth-form" @submit.prevent="saveNode"><div class="fixed-fields"><span>{{ editingNode?.server_name }}</span><span>{{ editingNode?.proxy_name }}</span></div><label><span>客户端名称</span><n-input v-model:value="nodeName" maxlength="100" /></label><div class="modal-actions"><n-button @click="nodeModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">保存</n-button></div></form></n-card></n-modal>
  <n-modal v-model:show="relayModalOpen"><n-card class="client-form-card" :title="editingRelayID === null ? '添加中转' : '修改落地'" closable @close="relayModalOpen = false"><form class="auth-form" @submit.prevent="saveRelay"><template v-if="editingRelayID === null"><label><span>名称</span><n-input v-model:value="relayName" maxlength="100" /></label><label><span>入口节点</span><select v-model.number="sourceClientID" class="settings-input" @change="onSourceChange"><option v-for="source in relaySources" :key="source.client_id" :value="source.client_id">{{ source.server_name }} · {{ source.proxy_name }}</option></select></label><fieldset class="relay-mode-field"><legend>落地方式</legend><n-radio-group v-model:value="relayMode"><div class="relay-mode-options"><n-radio value="assigned_node">使用已有节点</n-radio><n-radio value="custom">自定义落地</n-radio></div></n-radio-group></fieldset><label v-if="relayMode === 'assigned_node'"><span>落地节点</span><select v-model.number="targetClientID" class="settings-input"><option v-for="source in targetSources" :key="source.client_id" :value="source.client_id">{{ source.server_name }} · {{ source.proxy_name }}</option></select></label></template><template v-if="relayMode === 'custom'"><label><span>落地公网 IP</span><n-input v-model:value="relayTargetIP" placeholder="1.2.3.4" /></label><label><span>落地端口</span><input v-model.number="relayTargetPort" class="settings-input" type="number" min="1" max="65535" /></label></template><div class="modal-actions"><n-button @click="relayModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">保存</n-button></div></form></n-card></n-modal>
  <QRCodeModal :show="qrOpen" :uri="qrURI" :title="qrTitle" :subtitle="qrSubtitle" @update:show="qrOpen = $event" />
</template>
