<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NModal, NProgress, NSpin } from 'naive-ui'
import { api } from '../api/client'
import { formatTime } from '../format'
import { formatClientTrafficBytes } from '../proxy'
import type { User } from '../types/auth'
import QRCodeModal from '../components/share/QRCodeModal.vue'

type UserNode = {
  client_id: number
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
type PasswordRequest = { id: number; status: 'pending' | 'approved' | 'rejected'; created_at: string; reviewed_at: string | null }
type NodeShare = { uri: string; protocol: 'vless' | 'shadowsocks'; server_name: string; proxy_name: string }
type RelayShare = { uri: string; protocol: 'vless' | 'shadowsocks'; name: string }

const props = defineProps<{ user: User }>()
const emit = defineEmits<{ logout: [] }>()
const nodes = ref<UserNode[]>([])
const relaySources = ref<RelaySource[]>([])
const relays = ref<UserRelay[]>([])
const passwordRequest = ref<PasswordRequest | null>(null)
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const copiedNodeID = ref<number | null>(null)
const copiedRelayID = ref<number | null>(null)
const qrOpen = ref(false)
const qrURI = ref('')
const qrTitle = ref('')
const qrSubtitle = ref('')
const passwordModalOpen = ref(false)
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const relayModalOpen = ref(false)
const relayName = ref('')
const relayMode = ref<'assigned_node' | 'custom'>('assigned_node')
const sourceClientID = ref(0)
const targetClientID = ref(0)
const relayTargetIP = ref('')
const relayTargetPort = ref<number | null>(null)
const editingRelayID = ref<number | null>(null)

const pendingPasswordRequest = computed(() => passwordRequest.value?.status === 'pending')
const targetSources = computed(() => relaySources.value.filter((source) => source.client_id !== sourceClientID.value))

async function loadPortal() {
  const results = await Promise.allSettled([loadNodes(), loadRelaySources(), loadRelays(), loadPasswordRequest()])
  const failed = results.find((result) => result.status === 'rejected')
  if (failed?.status === 'rejected') {
    error.value = failed.reason instanceof Error ? failed.reason.message : '部分用户门户数据加载失败'
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

async function loadPasswordRequest() {
  passwordRequest.value = (await api<{ request: PasswordRequest | null }>('/api/me/password-change-request')).request
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

async function submitPasswordRequest() {
  if (newPassword.value.length < 10 || newPassword.value.length > 72) {
    error.value = '新密码长度需为 10–72 字节'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    error.value = '两次输入的新密码不一致'
    return
  }
  await run(async () => {
    const response = await api<{ request: PasswordRequest }>('/api/me/password-change-request', {
      method: 'POST',
      body: JSON.stringify({ current_password: currentPassword.value, new_password: newPassword.value }),
    })
    passwordRequest.value = response.request
    currentPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
    passwordModalOpen.value = false
  })
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
      <div><strong>VPS Panel</strong><span>{{ props.user.username }}</span></div>
      <n-button secondary :loading="busy" @click="emit('logout')">退出登录</n-button>
    </header>
    <n-alert v-if="error" type="error" class="page-alert">{{ error }}</n-alert>
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载…</span></div>
    <template v-else>
      <section>
        <h1>我的节点</h1>
        <n-empty v-if="nodes.length === 0" description="管理员尚未分配节点" />
        <div v-else class="user-card-grid">
          <n-card v-for="node in nodes" :key="node.client_id" :title="node.server_name" :bordered="true">
            <template #header-extra><span :class="['user-status', { online: node.effective_enabled }]">● {{ nodeStatus(node) }}</span></template>
            <p class="secondary-text">{{ node.proxy_name }}</p>
            <n-progress type="line" :percentage="nodeUsagePercent(node)" :show-indicator="false" />
            <strong>{{ formatClientTrafficBytes(node.traffic_used_bytes) }} / {{ node.traffic_limit_bytes ? formatClientTrafficBytes(node.traffic_limit_bytes) : '不限' }}</strong>
            <dl class="user-details"><div><dt>到期</dt><dd>{{ node.expires_at ? formatTime(node.expires_at) : '不限' }}</dd></div><div><dt>付款周期</dt><dd>{{ billingLabel(node.billing_period_months) }}</dd></div></dl>
            <div class="modal-actions"><n-button secondary :disabled="busy" @click="copyNode(node)">{{ copiedNodeID === node.client_id ? '已复制' : '复制链接' }}</n-button><n-button type="primary" :disabled="busy" @click="showNodeQR(node)">二维码</n-button></div>
          </n-card>
        </div>
      </section>
      <section>
        <div class="section-heading"><h1>我的中转</h1><n-button type="primary" :disabled="relaySources.length === 0 || busy" @click="openRelayModal">添加中转</n-button></div>
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
      <section><h1>账号</h1><n-card :bordered="true"><dl class="user-details"><div><dt>用户名</dt><dd>{{ props.user.username }}</dd></div><div><dt>密码修改</dt><dd>{{ pendingPasswordRequest ? '等待管理员审核' : passwordRequest?.status === 'approved' ? '最近申请已批准' : passwordRequest?.status === 'rejected' ? '最近申请已拒绝' : '无待处理申请' }}</dd></div></dl><n-button type="primary" :disabled="pendingPasswordRequest || busy" @click="passwordModalOpen = true">申请修改密码</n-button></n-card></section>
    </template>
  </main>

  <n-modal v-model:show="passwordModalOpen"><n-card class="client-form-card" title="申请修改密码" closable @close="passwordModalOpen = false"><form class="auth-form" @submit.prevent="submitPasswordRequest"><label><span>当前密码</span><n-input v-model:value="currentPassword" type="password" show-password-on="click" /></label><label><span>新密码</span><n-input v-model:value="newPassword" type="password" show-password-on="click" /></label><label><span>确认新密码</span><n-input v-model:value="confirmPassword" type="password" show-password-on="click" /></label><div class="modal-actions"><n-button @click="passwordModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">提交申请</n-button></div></form></n-card></n-modal>
  <n-modal v-model:show="relayModalOpen"><n-card class="client-form-card" :title="editingRelayID === null ? '添加中转' : '修改落地'" closable @close="relayModalOpen = false"><form class="auth-form" @submit.prevent="saveRelay"><template v-if="editingRelayID === null"><label><span>名称</span><n-input v-model:value="relayName" maxlength="100" /></label><label><span>入口节点</span><select v-model.number="sourceClientID" class="settings-input" @change="onSourceChange"><option v-for="source in relaySources" :key="source.client_id" :value="source.client_id">{{ source.server_name }} · {{ source.proxy_name }}</option></select></label><fieldset class="relay-mode-field"><legend>落地方式</legend><label><input v-model="relayMode" type="radio" value="assigned_node" /> 使用已有节点</label><label><input v-model="relayMode" type="radio" value="custom" /> 自定义落地</label></fieldset><label v-if="relayMode === 'assigned_node'"><span>落地节点</span><select v-model.number="targetClientID" class="settings-input"><option v-for="source in targetSources" :key="source.client_id" :value="source.client_id">{{ source.server_name }} · {{ source.proxy_name }}</option></select></label></template><template v-if="relayMode === 'custom'"><label><span>落地公网 IP</span><n-input v-model:value="relayTargetIP" placeholder="1.2.3.4" /></label><label><span>落地端口</span><input v-model.number="relayTargetPort" class="settings-input" type="number" min="1" max="65535" /></label></template><div class="modal-actions"><n-button @click="relayModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">保存</n-button></div></form></n-card></n-modal>
  <QRCodeModal :show="qrOpen" :uri="qrURI" :title="qrTitle" :subtitle="qrSubtitle" @update:show="qrOpen = $event" />
</template>
