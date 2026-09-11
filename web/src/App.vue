<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NConfigProvider,
  NEmpty,
  NInput,
  NModal,
  NSpin,
  NTag,
} from 'naive-ui'

type User = {
  id: number
  username: string
  role: 'admin' | 'vip'
  created_at: string
}

type AuthState = {
  requires_initialization: boolean
  authenticated: boolean
  user?: User
}

type Health = {
  status: string
  database: string
}

type Invitation = {
  id: number
  created_by: number
  created_by_username: string
  expires_at: string
  created_at: string
  token?: string
}

type ServerRecord = {
  id: number
  name: string
  status: 'pending' | 'online' | 'offline'
  archived_at?: string
  expires_at: string | null
  last_seen_at: string | null
  system_info: ServerSystemInfo | null
  metrics: ServerMetrics | null
  created_at: string
  updated_at: string
}

type ServerSystemInfo = {
  hostname: string
  os_name: string
  os_version: string
  kernel: string
  arch: string
  ipv4: string[]
  ipv6: string[]
  agent_version: string
}

type ServerMetrics = {
  cpu_percent: number
  memory_used_bytes: number
  memory_total_bytes: number
  disk_used_bytes: number
  disk_total_bytes: number
  uptime_seconds: number
  updated_at: string
}

type CreatedServer = {
  server: ServerRecord
  enrollment_token: string
  enrollment_token_expires_at: string
  agent_installation_command: string
}

const state = ref<AuthState | null>(null)
const health = ref<Health | null>(null)
const invitations = ref<Invitation[]>([])
const servers = ref<ServerRecord[]>([])
const archivedServers = ref<ServerRecord[]>([])
const selectedServer = ref<ServerRecord | null>(null)
const createdServer = ref<CreatedServer | null>(null)
const serverModalOpen = ref(false)
const serverName = ref('')
const currentPage = ref<'overview' | 'servers'>('overview')
const serverListMode = ref<'active' | 'archived'>('active')
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const generatedLink = ref('')
const copied = ref(false)
const copiedCommand = ref(false)
const expirationEditing = ref(false)
const expirationInput = ref('')

const username = ref('')
const password = ref('')
const confirmPassword = ref('')

let serverLoadPromise: Promise<void> | null = null
let serverPollTimer: number | undefined

const invitationToken = new URLSearchParams(window.location.search).get('token') ?? ''
const isInvitationPage = computed(
  () => window.location.pathname === '/register' && invitationToken !== '',
)
const isHealthy = computed(
  () => health.value?.status === 'ok' && health.value.database === 'ok',
)

async function api<T>(url: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, {
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

  if (response.status === 204) {
    return undefined as T
  }
  return (await response.json()) as T
}

async function loadState() {
  state.value = await api<AuthState>('/api/auth/state')
  if (state.value.authenticated) {
    const requests = [loadHealth(), loadServers()]
    if (state.value.user?.role === 'admin') {
      requests.push(loadInvitations())
    }
    await Promise.all(requests)
    startServerPolling()
  }
}

async function loadHealth() {
  health.value = await api<Health>('/api/health')
}

async function loadInvitations() {
  const response = await api<{ invitations: Invitation[] }>('/api/admin/invitations')
  invitations.value = response.invitations
}

async function loadServers() {
  if (serverLoadPromise) return serverLoadPromise
  serverLoadPromise = (async () => {
    const [activeResponse, archivedResponse] = await Promise.all([
      api<{ servers: ServerRecord[] }>('/api/servers'),
      api<{ servers: ServerRecord[] }>('/api/servers?archived=true'),
    ])
    if (!state.value?.authenticated) return
    servers.value = activeResponse.servers
    archivedServers.value = archivedResponse.servers
    if (selectedServer.value) {
      selectedServer.value = [...servers.value, ...archivedServers.value].find(
        (value) => value.id === selectedServer.value?.id,
      ) ?? null
    }
  })()
  try {
    await serverLoadPromise
  } finally {
    serverLoadPromise = null
  }
}

function startServerPolling() {
  stopServerPolling()
  serverPollTimer = window.setInterval(() => {
    if (!state.value?.authenticated || submitting.value) return
    void loadServers().catch(() => undefined)
  }, 5_000)
}

function stopServerPolling() {
  if (serverPollTimer !== undefined) {
    window.clearInterval(serverPollTimer)
    serverPollTimer = undefined
  }
}

function validatePasswords(): boolean {
  if (password.value.length < 10) {
    error.value = '密码至少需要 10 个字符'
    return false
  }
  if (password.value !== confirmPassword.value) {
    error.value = '两次输入的密码不一致'
    return false
  }
  return true
}

async function initialize() {
  if (!validatePasswords()) return
  await submit(async () => {
    await api('/api/auth/initialize', {
      method: 'POST',
      body: JSON.stringify({ username: username.value, password: password.value }),
    })
    clearCredentials()
    await loadState()
  })
}

async function login() {
  await submit(async () => {
    await api('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: username.value, password: password.value }),
    })
    clearCredentials()
    await loadState()
  })
}

async function register() {
  if (!validatePasswords()) return
  await submit(async () => {
    await api('/api/auth/register', {
      method: 'POST',
      body: JSON.stringify({
        token: invitationToken,
        username: username.value,
        password: password.value,
      }),
    })
    clearCredentials()
    window.history.replaceState({}, '', '/')
    await loadState()
  })
}

async function logout() {
  await submit(async () => {
    await api('/api/auth/logout', { method: 'POST' })
    stopServerPolling()
    state.value = {
      requires_initialization: false,
      authenticated: false,
    }
    health.value = null
    invitations.value = []
    servers.value = []
    archivedServers.value = []
    selectedServer.value = null
    createdServer.value = null
    serverModalOpen.value = false
    expirationEditing.value = false
    expirationInput.value = ''
    generatedLink.value = ''
    currentPage.value = 'overview'
  })
}

async function createInvitation() {
  await submit(async () => {
    const invitation = await api<Invitation>('/api/admin/invitations', { method: 'POST' })
    generatedLink.value = `${window.location.origin}/register?token=${encodeURIComponent(invitation.token ?? '')}`
    copied.value = false
    await loadInvitations()
  })
}

async function revokeInvitation(id: number) {
  await submit(async () => {
    await api(`/api/admin/invitations/${id}`, { method: 'DELETE' })
    await loadInvitations()
  })
}

async function copyInvitation() {
  try {
    await navigator.clipboard.writeText(generatedLink.value)
    copied.value = true
  } catch {
    error.value = '无法自动复制，请手动复制邀请链接'
  }
}

async function createServerRecord() {
  if (serverName.value.trim() === '') {
    error.value = '请输入服务器名称'
    return
  }
  await submit(async () => {
    createdServer.value = await api<CreatedServer>('/api/servers', {
      method: 'POST',
      body: JSON.stringify({ name: serverName.value }),
    })
    selectedServer.value = createdServer.value.server
    serverModalOpen.value = true
    serverName.value = ''
    copiedCommand.value = false
    expirationEditing.value = false
    await loadServers()
  })
}

function viewServer(value: ServerRecord) {
  selectedServer.value = value
  createdServer.value = null
  copiedCommand.value = false
  expirationEditing.value = false
  expirationInput.value = ''
  serverModalOpen.value = true
}

async function archiveServer(value: ServerRecord) {
  if (
    !window.confirm(
      `确定移除服务器“${value.name}”吗？\n\n移除后将从服务器列表隐藏，并立即撤销当前 Agent 凭据，但服务器资料和历史数据会保留。之后可以重新生成 Agent 安装令牌恢复。`,
    )
  )
    return
  await submit(async () => {
    await api(`/api/servers/${value.id}`, { method: 'DELETE' })
    if (selectedServer.value?.id === value.id) selectedServer.value = null
    if (createdServer.value?.server.id === value.id) createdServer.value = null
    await loadServers()
  })
}

async function regenerateEnrollment(value: ServerRecord) {
  if (
    !window.confirm(
      '重新生成后，当前 Agent 凭据将立即失效；如果服务器在线，现有 Agent 连接也会断开。服务器资料和历史数据不会删除。是否继续？',
    )
  )
    return
  await submit(async () => {
    createdServer.value = await api<CreatedServer>(`/api/servers/${value.id}/enrollment`, {
      method: 'POST',
    })
    selectedServer.value = createdServer.value.server
    copiedCommand.value = false
    expirationEditing.value = false
    await loadServers()
  })
}

function closeServerDetails() {
  selectedServer.value = null
  createdServer.value = null
  copiedCommand.value = false
  expirationEditing.value = false
  expirationInput.value = ''
}

function startExpirationEdit() {
  if (!selectedServer.value || selectedServer.value.archived_at) return
  expirationInput.value = selectedServer.value.expires_at
    ? formatExpiration(selectedServer.value.expires_at)
    : ''
  expirationEditing.value = true
}

function cancelExpirationEdit() {
  expirationEditing.value = false
  expirationInput.value = ''
}

async function saveExpiration() {
  const value = expirationInput.value.trim()
  if (!/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/.test(value)) {
    error.value = '请输入格式为 YYYY-MM-DD HH:mm 的到期时间'
    return
  }
  await updateExpiration(value)
}

async function clearExpiration() {
  await updateExpiration(null)
}

async function updateExpiration(expiresAt: string | null) {
  if (!selectedServer.value) return
  const serverID = selectedServer.value.id
  await submit(async () => {
    const response = await api<{ server: ServerRecord }>(`/api/servers/${serverID}`, {
      method: 'PATCH',
      body: JSON.stringify({ expires_at: expiresAt }),
    })
    selectedServer.value = response.server
    expirationEditing.value = false
    expirationInput.value = ''
    await loadServers()
  })
}

async function permanentlyDeleteServer(value: ServerRecord) {
  if (
    !window.confirm(
      `确定彻底删除服务器“${value.name}”吗？\n\n这将永久删除该服务器及全部历史数据，无法通过重新安装 Agent 恢复。`,
    )
  )
    return
  await submit(async () => {
    await api(`/api/servers/${value.id}/permanent`, { method: 'DELETE' })
    serverModalOpen.value = false
    await loadServers()
  })
}

async function copyAgentCommand(value: string) {
  try {
    await navigator.clipboard.writeText(value)
    copiedCommand.value = true
  } catch {
    error.value = '无法自动复制，请手动复制内容'
  }
}

function statusType(status: ServerRecord['status']): 'warning' | 'success' | 'default' {
  if (status === 'online') return 'success'
  if (status === 'pending') return 'warning'
  return 'default'
}

function statusLabel(status: ServerRecord['status']): string {
  if (status === 'pending') return '待注册'
  if (status === 'online') return '在线'
  return '离线'
}

async function submit(action: () => Promise<void>) {
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

function clearCredentials() {
  username.value = ''
  password.value = ''
  confirmPassword.value = ''
}

function formatTime(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

function formatExpiration(value: string) {
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  })
  const parts = Object.fromEntries(
    formatter.formatToParts(new Date(value)).map((part) => [part.type, part.value]),
  )
  return `${parts.year}-${parts.month}-${parts.day} ${parts.hour}:${parts.minute}`
}

function formatPercent(value: number) {
  return `${value.toFixed(1).replace(/\.0$/, '')}%`
}

function formatBytes(value: number) {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let unitIndex = 0
  let scaled = value
  while (scaled >= 1024 && unitIndex < units.length - 1) {
    scaled /= 1024
    unitIndex++
  }
  const digits = unitIndex > 0 && scaled < 10 ? 1 : 0
  return `${scaled.toFixed(digits).replace(/\.0$/, '')} ${units[unitIndex]}`
}

function formatUptime(value: number) {
  const days = Math.floor(value / 86_400)
  const hours = Math.floor((value % 86_400) / 3_600)
  const minutes = Math.floor((value % 3_600) / 60)
  const seconds = Math.floor(value % 60)
  if (days > 0) return `${days} 天${hours > 0 ? ` ${hours} 小时` : ''}`
  if (hours > 0) return `${hours} 小时${minutes > 0 ? ` ${minutes} 分钟` : ''}`
  if (minutes > 0) return `${minutes} 分钟`
  return `${seconds} 秒`
}

onMounted(async () => {
  try {
    await loadState()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法连接管理面板 API'
  } finally {
    loading.value = false
  }
})

onUnmounted(stopServerPolling)
</script>

<template>
  <n-config-provider>
    <main class="page-shell" :class="{ 'admin-shell': state?.authenticated }">
      <n-card v-if="loading" class="auth-card" :bordered="true">
        <div class="loading-row">
          <n-spin size="small" />
          <span>正在连接管理面板…</span>
        </div>
      </n-card>

      <n-card v-else-if="!state" class="auth-card" :bordered="true">
        <n-alert title="管理面板暂不可用" type="error">{{ error }}</n-alert>
      </n-card>

      <n-card
        v-else-if="isInvitationPage && !state.authenticated"
        class="auth-card"
        :bordered="true"
      >
        <p class="eyebrow">账号邀请</p>
        <h1>创建 VIP 账户</h1>
        <p class="description">此邀请仅可使用一次，注册后账号权限为 VIP。</p>
        <n-alert v-if="error" class="form-alert" type="error">{{ error }}</n-alert>
        <form class="auth-form" @submit.prevent="register">
          <label>
            <span>用户名</span>
            <n-input
              v-model:value="username"
              :input-props="{ autocomplete: 'username' }"
              placeholder="3–64 位字符"
            />
          </label>
          <label>
            <span>密码</span>
            <n-input
              v-model:value="password"
              type="password"
              show-password-on="click"
              :input-props="{ autocomplete: 'new-password' }"
              placeholder="至少 10 个字符"
            />
          </label>
          <label>
            <span>确认密码</span>
            <n-input
              v-model:value="confirmPassword"
              type="password"
              show-password-on="click"
              :input-props="{ autocomplete: 'new-password' }"
            />
          </label>
          <n-button type="primary" attr-type="submit" block :loading="submitting">
            接受邀请
          </n-button>
        </form>
      </n-card>

      <n-card v-else-if="state.requires_initialization" class="auth-card" :bordered="true">
        <p class="eyebrow">首次初始化</p>
        <h1>初始化 VPS 管理面板</h1>
        <p class="description">创建首个管理员。完成后，此入口将永久关闭。</p>
        <n-alert v-if="error" class="form-alert" type="error">{{ error }}</n-alert>
        <form class="auth-form" @submit.prevent="initialize">
          <label>
            <span>管理员用户名</span>
            <n-input
              v-model:value="username"
              :input-props="{ autocomplete: 'username' }"
              placeholder="例如：管理员"
            />
          </label>
          <label>
            <span>密码</span>
            <n-input
              v-model:value="password"
              type="password"
              show-password-on="click"
              :input-props="{ autocomplete: 'new-password' }"
              placeholder="至少 10 个字符"
            />
          </label>
          <label>
            <span>确认密码</span>
            <n-input
              v-model:value="confirmPassword"
              type="password"
              show-password-on="click"
              :input-props="{ autocomplete: 'new-password' }"
            />
          </label>
          <n-button type="primary" attr-type="submit" block :loading="submitting">
            创建管理员并进入面板
          </n-button>
        </form>
      </n-card>

      <n-card v-else-if="!state.authenticated" class="auth-card" :bordered="true">
        <p class="eyebrow">VPS 管理</p>
        <h1>登录 VPS 管理面板</h1>
        <p class="description">请使用已注册账号登录。</p>
        <n-alert v-if="error" class="form-alert" type="error">{{ error }}</n-alert>
        <form class="auth-form" @submit.prevent="login">
          <label>
            <span>用户名</span>
            <n-input v-model:value="username" :input-props="{ autocomplete: 'username' }" />
          </label>
          <label>
            <span>密码</span>
            <n-input
              v-model:value="password"
              type="password"
              show-password-on="click"
              :input-props="{ autocomplete: 'current-password' }"
            />
          </label>
          <n-button type="primary" attr-type="submit" block :loading="submitting">
            登录
          </n-button>
        </form>
      </n-card>

      <section v-else class="admin-page">
        <header class="admin-header">
          <div class="brand-block">
            <p class="eyebrow">VPS 管理</p>
            <h1>VPS 管理面板</h1>
            <nav class="admin-nav" aria-label="管理导航">
              <n-button
                size="small"
                :type="currentPage === 'overview' ? 'primary' : 'default'"
                :secondary="currentPage === 'overview'"
                @click="currentPage = 'overview'"
              >
                概览
              </n-button>
              <n-button
                size="small"
                :type="currentPage === 'servers' ? 'primary' : 'default'"
                :secondary="currentPage === 'servers'"
                @click="currentPage = 'servers'"
              >
                服务器
              </n-button>
            </nav>
          </div>
          <div class="account-actions">
            <span>{{ state.user?.username }}</span>
            <n-button secondary :loading="submitting" @click="logout">退出登录</n-button>
          </div>
        </header>

        <n-alert v-if="error" class="page-alert" type="error">{{ error }}</n-alert>

        <template v-if="currentPage === 'overview'">
          <div class="dashboard-grid">
            <n-card title="运行状态" :bordered="true">
              <template #header-extra>
                <n-tag v-if="isHealthy" type="success" round>运行正常</n-tag>
              </template>
              <div class="health-grid">
                <div>
                  <span>后端服务</span>
                  <strong>{{ health?.status === 'ok' ? '正常' : '不可用' }}</strong>
                </div>
                <div>
                  <span>SQLite</span>
                  <strong>{{ health?.database === 'ok' ? '已连接' : '不可用' }}</strong>
                </div>
              </div>
            </n-card>

            <n-card v-if="state.user?.role === 'admin'" title="邀请 VIP 账号" :bordered="true">
              <p class="card-copy">生成 24 小时有效的一次性 VIP 注册链接。</p>
              <n-button type="primary" :loading="submitting" @click="createInvitation">
                生成邀请链接
              </n-button>
              <div v-if="generatedLink" class="generated-link">
                <strong>请立即保存，此链接不会再次显示</strong>
                <n-input :value="generatedLink" readonly />
                <n-button secondary @click="copyInvitation">
                  {{ copied ? '已复制' : '复制链接' }}
                </n-button>
              </div>
            </n-card>
          </div>

          <n-card v-if="state.user?.role === 'admin'" title="有效邀请" :bordered="true">
            <n-empty v-if="invitations.length === 0" description="当前没有未过期的邀请" />
            <div v-else class="invitation-list">
              <div v-for="invitation in invitations" :key="invitation.id" class="invitation-row">
                <div>
                  <strong>邀请 #{{ invitation.id }}</strong>
                  <span>
                    {{ invitation.created_by_username }} 创建 ·
                    {{ formatTime(invitation.expires_at) }} 过期
                  </span>
                </div>
                <n-button
                  type="error"
                  secondary
                  size="small"
                  :disabled="submitting"
                  @click="revokeInvitation(invitation.id)"
                >
                  撤销
                </n-button>
              </div>
            </div>
          </n-card>
        </template>

        <template v-else>
          <div class="admin-nav server-list-nav" aria-label="服务器列表">
            <n-button
              size="small"
              :type="serverListMode === 'active' ? 'primary' : 'default'"
              :secondary="serverListMode === 'active'"
              @click="serverListMode = 'active'"
            >
              正常服务器
            </n-button>
            <n-button
              size="small"
              :type="serverListMode === 'archived' ? 'primary' : 'default'"
              :secondary="serverListMode === 'archived'"
              @click="serverListMode = 'archived'"
            >
              已移除
            </n-button>
          </div>

          <div v-if="serverListMode === 'active'" class="server-create">
            <n-card title="新增服务器" :bordered="true">
              <p class="card-copy">创建后将生成一个 24 小时有效的 Agent 安装令牌。</p>
              <form class="server-form" @submit.prevent="createServerRecord">
                <n-input
                  v-model:value="serverName"
                  maxlength="100"
                  placeholder="例如：日本服务器 01"
                />
                <n-button type="primary" attr-type="submit" :loading="submitting">
                  新增服务器
                </n-button>
              </form>
            </n-card>
          </div>

          <n-card v-if="serverListMode === 'active'" title="正常服务器" :bordered="true">
            <n-empty v-if="servers.length === 0" description="当前没有服务器" />
            <div v-else class="server-table-wrap">
              <table class="server-table">
                <thead>
                  <tr>
                    <th>名称</th>
                    <th>状态</th>
                    <th>创建时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="value in servers" :key="value.id">
                    <td>{{ value.name }}</td>
                    <td>
                      <n-tag :type="statusType(value.status)" size="small">
                        {{ statusLabel(value.status) }}
                      </n-tag>
                    </td>
                    <td>{{ formatTime(value.created_at) }}</td>
                    <td class="server-actions">
                      <n-button
                        size="small"
                        secondary
                        :disabled="submitting"
                        @click="viewServer(value)"
                      >
                        查看
                      </n-button>
                      <n-button
                        size="small"
                        type="error"
                        secondary
                        :disabled="submitting"
                        @click="archiveServer(value)"
                      >
                        移除
                      </n-button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </n-card>

          <n-card v-if="serverListMode === 'archived'" title="已移除服务器" :bordered="true">
            <n-empty v-if="archivedServers.length === 0" description="当前没有已移除服务器" />
            <div v-else class="server-table-wrap">
              <table class="server-table">
                <thead>
                  <tr>
                    <th>名称</th>
                    <th>移除时间</th>
                    <th>创建时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="value in archivedServers" :key="value.id">
                    <td>{{ value.name }}</td>
                    <td>{{ value.archived_at ? formatTime(value.archived_at) : '—' }}</td>
                    <td>{{ formatTime(value.created_at) }}</td>
                    <td class="server-actions">
                      <n-button
                        size="small"
                        secondary
                        :disabled="submitting"
                        @click="viewServer(value)"
                      >
                        查看
                      </n-button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </n-card>
        </template>

        <n-modal
          v-model:show="serverModalOpen"
          :mask-closable="true"
          @after-leave="closeServerDetails"
        >
          <n-card
            v-if="selectedServer"
            class="server-modal-card"
            title="服务器详情"
            :bordered="false"
            closable
            @close="serverModalOpen = false"
          >
            <dl class="server-details">
              <div><dt>名称</dt><dd>{{ selectedServer.name }}</dd></div>
              <div><dt>状态</dt><dd>{{ statusLabel(selectedServer.status) }}</dd></div>
              <div>
                <dt>到期时间</dt>
                <dd class="expiration-field">
                  <template v-if="!expirationEditing">
                    <span>{{ selectedServer.expires_at ? formatExpiration(selectedServer.expires_at) : '不限' }}</span>
                    <n-button
                      v-if="!selectedServer.archived_at"
                      size="tiny"
                      text
                      :disabled="submitting"
                      @click="startExpirationEdit"
                    >
                      修改
                    </n-button>
                  </template>
                  <template v-else>
                    <n-input
                      v-model:value="expirationInput"
                      placeholder="2026-12-31 23:59"
                      :disabled="submitting"
                    />
                    <small>格式：YYYY-MM-DD HH:mm（Asia/Shanghai）</small>
                    <div class="expiration-actions">
                      <n-button size="small" type="primary" :loading="submitting" @click="saveExpiration">
                        保存
                      </n-button>
                      <n-button
                        v-if="selectedServer.expires_at"
                        size="small"
                        secondary
                        :disabled="submitting"
                        @click="clearExpiration"
                      >
                        设为不限
                      </n-button>
                      <n-button size="small" text :disabled="submitting" @click="cancelExpirationEdit">
                        取消
                      </n-button>
                    </div>
                  </template>
                </dd>
              </div>
              <div>
                <dt>最后通信</dt>
                <dd>{{ selectedServer.last_seen_at ? formatTime(selectedServer.last_seen_at) : '—' }}</dd>
              </div>
              <div><dt>创建时间</dt><dd>{{ formatTime(selectedServer.created_at) }}</dd></div>
              <div><dt>更新时间</dt><dd>{{ formatTime(selectedServer.updated_at) }}</dd></div>
              <div v-if="selectedServer.archived_at">
                <dt>移除时间</dt><dd>{{ formatTime(selectedServer.archived_at) }}</dd>
              </div>
            </dl>

            <h3 class="system-info-title">系统信息</h3>
            <n-empty
              v-if="!selectedServer.system_info"
              size="small"
              description="暂无系统信息"
            />
            <dl v-else class="server-details">
              <div><dt>主机名</dt><dd>{{ selectedServer.system_info.hostname || '—' }}</dd></div>
              <div><dt>系统</dt><dd>{{ selectedServer.system_info.os_name || '—' }}</dd></div>
              <div><dt>系统版本</dt><dd>{{ selectedServer.system_info.os_version || '—' }}</dd></div>
              <div><dt>内核</dt><dd>{{ selectedServer.system_info.kernel || '—' }}</dd></div>
              <div><dt>架构</dt><dd>{{ selectedServer.system_info.arch || '—' }}</dd></div>
              <div>
                <dt>IPv4</dt>
                <dd class="address-list">
                  <span v-if="selectedServer.system_info.ipv4.length === 0">—</span>
                  <span v-for="address in selectedServer.system_info.ipv4" :key="address">{{ address }}</span>
                </dd>
              </div>
              <div>
                <dt>IPv6</dt>
                <dd class="address-list">
                  <span v-if="selectedServer.system_info.ipv6.length === 0">—</span>
                  <span v-for="address in selectedServer.system_info.ipv6" :key="address">{{ address }}</span>
                </dd>
              </div>
              <div><dt>Agent 版本</dt><dd>{{ selectedServer.system_info.agent_version || '—' }}</dd></div>
            </dl>

            <h3 class="system-info-title">动态指标</h3>
            <n-empty
              v-if="!selectedServer.metrics"
              size="small"
              description="暂无动态指标"
            />
            <dl v-else class="server-details">
              <div><dt>CPU</dt><dd>{{ formatPercent(selectedServer.metrics.cpu_percent) }}</dd></div>
              <div>
                <dt>内存</dt>
                <dd>
                  {{ formatBytes(selectedServer.metrics.memory_used_bytes) }} /
                  {{ formatBytes(selectedServer.metrics.memory_total_bytes) }}
                </dd>
              </div>
              <div>
                <dt>根分区磁盘</dt>
                <dd>
                  {{ formatBytes(selectedServer.metrics.disk_used_bytes) }} /
                  {{ formatBytes(selectedServer.metrics.disk_total_bytes) }}
                </dd>
              </div>
              <div><dt>运行时间</dt><dd>{{ formatUptime(selectedServer.metrics.uptime_seconds) }}</dd></div>
            </dl>

            <div v-if="state.user?.role === 'admin'" class="server-modal-actions">
              <n-button
                type="primary"
                secondary
                :loading="submitting"
                @click="regenerateEnrollment(selectedServer)"
              >
                重新生成 Agent 安装令牌
              </n-button>
              <n-button
                v-if="selectedServer.archived_at"
                type="error"
                secondary
                :disabled="submitting"
                @click="permanentlyDeleteServer(selectedServer)"
              >
                彻底删除
              </n-button>
            </div>

            <div v-if="createdServer" class="modal-enrollment">
              <n-alert
                type="warning"
                title="Agent 安装命令仅显示一次，请立即保存。"
              >
                请在目标 Debian/Ubuntu VPS 上以 root 用户执行下方安装命令。
              </n-alert>
              <dl class="server-details enrollment-summary">
                <div>
                  <dt>过期时间</dt>
                  <dd>{{ formatTime(createdServer.enrollment_token_expires_at) }}</dd>
                </div>
              </dl>
              <div class="secret-field">
                <strong>Agent 安装命令</strong>
                <n-input
                  :value="createdServer.agent_installation_command"
                  type="textarea"
                  readonly
                  :autosize="{ minRows: 3 }"
                />
                <n-button
                  secondary
                  @click="copyAgentCommand(createdServer.agent_installation_command)"
                >
                  {{ copiedCommand ? '已复制' : '复制命令' }}
                </n-button>
              </div>
            </div>
          </n-card>
        </n-modal>
      </section>
    </main>
  </n-config-provider>
</template>
