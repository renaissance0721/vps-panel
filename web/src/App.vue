<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NConfigProvider,
  NEmpty,
  NInput,
  NSpin,
  NTag,
} from 'naive-ui'

type User = {
  id: number
  username: string
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
  created_at: string
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
const selectedServer = ref<ServerRecord | null>(null)
const createdServer = ref<CreatedServer | null>(null)
const serverName = ref('')
const currentPage = ref<'overview' | 'servers'>('overview')
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const generatedLink = ref('')
const copied = ref(false)
const copiedEnrollment = ref<'token' | 'command' | ''>('')

const username = ref('')
const password = ref('')
const confirmPassword = ref('')

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
    await Promise.all([loadHealth(), loadInvitations(), loadServers()])
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
  const response = await api<{ servers: ServerRecord[] }>('/api/servers')
  servers.value = response.servers
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
    state.value = {
      requires_initialization: false,
      authenticated: false,
    }
    health.value = null
    invitations.value = []
    servers.value = []
    selectedServer.value = null
    createdServer.value = null
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
    serverName.value = ''
    copiedEnrollment.value = ''
    await loadServers()
  })
}

async function viewServer(id: number) {
  await submit(async () => {
    const response = await api<{ server: ServerRecord }>(`/api/servers/${id}`)
    selectedServer.value = response.server
  })
}

async function deleteServer(value: ServerRecord) {
  if (!window.confirm(`确定删除服务器“${value.name}”吗？`)) return
  await submit(async () => {
    await api(`/api/servers/${value.id}`, { method: 'DELETE' })
    if (selectedServer.value?.id === value.id) selectedServer.value = null
    if (createdServer.value?.server.id === value.id) createdServer.value = null
    await loadServers()
  })
}

async function copyEnrollment(value: string, kind: 'token' | 'command') {
  try {
    await navigator.clipboard.writeText(value)
    copiedEnrollment.value = kind
  } catch {
    error.value = '无法自动复制，请手动复制内容'
  }
}

function statusType(status: ServerRecord['status']): 'warning' | 'success' | 'default' {
  if (status === 'online') return 'success'
  if (status === 'pending') return 'warning'
  return 'default'
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

onMounted(async () => {
  try {
    await loadState()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法连接 Panel API'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <n-config-provider>
    <main class="page-shell" :class="{ 'admin-shell': state?.authenticated }">
      <n-card v-if="loading" class="auth-card" :bordered="true">
        <div class="loading-row">
          <n-spin size="small" />
          <span>正在连接 Panel…</span>
        </div>
      </n-card>

      <n-card v-else-if="!state" class="auth-card" :bordered="true">
        <n-alert title="Panel 暂不可用" type="error">{{ error }}</n-alert>
      </n-card>

      <n-card
        v-else-if="isInvitationPage && !state.authenticated"
        class="auth-card"
        :bordered="true"
      >
        <p class="eyebrow">ADMIN INVITATION</p>
        <h1>创建管理员账户</h1>
        <p class="description">此邀请仅可使用一次，并将在创建后 24 小时过期。</p>
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
        <p class="eyebrow">FIRST-TIME SETUP</p>
        <h1>初始化 VPS Panel</h1>
        <p class="description">创建首个管理员。完成后，此入口将永久关闭。</p>
        <n-alert v-if="error" class="form-alert" type="error">{{ error }}</n-alert>
        <form class="auth-form" @submit.prevent="initialize">
          <label>
            <span>管理员用户名</span>
            <n-input
              v-model:value="username"
              :input-props="{ autocomplete: 'username' }"
              placeholder="例如 admin"
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
        <p class="eyebrow">VPS MANAGEMENT</p>
        <h1>登录 VPS Panel</h1>
        <p class="description">仅管理员可以访问管理页面。</p>
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
            <p class="eyebrow">VPS MANAGEMENT</p>
            <h1>VPS Panel</h1>
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
                Servers
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
                  <span>Backend</span>
                  <strong>{{ health?.status === 'ok' ? 'Healthy' : 'Unavailable' }}</strong>
                </div>
                <div>
                  <span>SQLite</span>
                  <strong>{{ health?.database === 'ok' ? 'Connected' : 'Unavailable' }}</strong>
                </div>
              </div>
            </n-card>

            <n-card title="邀请管理员" :bordered="true">
              <p class="card-copy">生成 24 小时有效的一次性注册链接。所有管理员权限相同。</p>
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

          <n-card title="有效邀请" :bordered="true">
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
          <div class="server-grid">
            <n-card title="新增服务器" :bordered="true">
              <p class="card-copy">创建后将生成一个 24 小时有效的 Enrollment Token。</p>
              <form class="server-form" @submit.prevent="createServerRecord">
                <n-input
                  v-model:value="serverName"
                  maxlength="100"
                  placeholder="例如 JP Native 01"
                />
                <n-button type="primary" attr-type="submit" :loading="submitting">
                  新增服务器
                </n-button>
              </form>
            </n-card>

            <n-card v-if="selectedServer" title="服务器详情" :bordered="true">
              <dl class="server-details">
                <div><dt>Name</dt><dd>{{ selectedServer.name }}</dd></div>
                <div><dt>Status</dt><dd>{{ selectedServer.status }}</dd></div>
                <div><dt>Created At</dt><dd>{{ formatTime(selectedServer.created_at) }}</dd></div>
                <div><dt>Updated At</dt><dd>{{ formatTime(selectedServer.updated_at) }}</dd></div>
              </dl>
            </n-card>
          </div>

          <n-card
            v-if="createdServer"
            class="enrollment-card"
            title="保存 Enrollment Token"
            :bordered="true"
          >
            <n-alert type="warning" title="This token is shown only once.">
              Agent support will be added in the next phase. 下方命令目前仅展示未来安装格式，暂不可执行。
            </n-alert>
            <dl class="server-details enrollment-summary">
              <div><dt>Server Name</dt><dd>{{ createdServer.server.name }}</dd></div>
              <div><dt>Status</dt><dd>Pending</dd></div>
              <div>
                <dt>Expires At</dt>
                <dd>{{ formatTime(createdServer.enrollment_token_expires_at) }}</dd>
              </div>
            </dl>
            <div class="secret-field">
              <strong>Enrollment Token</strong>
              <n-input :value="createdServer.enrollment_token" readonly />
              <n-button
                secondary
                @click="copyEnrollment(createdServer.enrollment_token, 'token')"
              >
                {{ copiedEnrollment === 'token' ? '已复制' : '复制 Token' }}
              </n-button>
            </div>
            <div class="secret-field">
              <strong>Agent Installation Command</strong>
              <n-input
                :value="createdServer.agent_installation_command"
                type="textarea"
                readonly
                :autosize="{ minRows: 3 }"
              />
              <n-button
                secondary
                @click="copyEnrollment(createdServer.agent_installation_command, 'command')"
              >
                {{ copiedEnrollment === 'command' ? '已复制' : '复制命令' }}
              </n-button>
            </div>
          </n-card>

          <n-card title="Servers" :bordered="true">
            <n-empty v-if="servers.length === 0" description="当前没有服务器" />
            <div v-else class="server-table-wrap">
              <table class="server-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Status</th>
                    <th>Created At</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="value in servers" :key="value.id">
                    <td>{{ value.name }}</td>
                    <td>
                      <n-tag :type="statusType(value.status)" size="small">
                        {{ value.status }}
                      </n-tag>
                    </td>
                    <td>{{ formatTime(value.created_at) }}</td>
                    <td class="server-actions">
                      <n-button
                        size="small"
                        secondary
                        :disabled="submitting"
                        @click="viewServer(value.id)"
                      >
                        查看
                      </n-button>
                      <n-button
                        size="small"
                        type="error"
                        secondary
                        :disabled="submitting"
                        @click="deleteServer(value)"
                      >
                        删除
                      </n-button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </n-card>
        </template>

        <p class="phase-note">v0.3 · Phase 3</p>
      </section>
    </main>
  </n-config-provider>
</template>
