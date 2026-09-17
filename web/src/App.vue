<script setup lang="ts">
import {
  computed,
  onMounted,
  onUnmounted,
  reactive,
  ref,
} from 'vue'
import {
  NConfigProvider,
  NCard,
  NSpin,
  NAlert,
  NInput,
  NButton,
} from 'naive-ui'
import {
  api,
  APIError,
} from './api/client'
import type {
  AuthState,
  AccessUser,
} from './types/auth'
import type {
  Health,
} from './types/overview'
import {
  useServers,
} from './composables/useServers'
import {
  useOverview,
} from './composables/useOverview'
import OverviewView from './views/OverviewView.vue'
import ServersView from './views/ServersView.vue'
import ProxiesView from './views/ProxiesView.vue'
import RelaysView from './views/RelaysView.vue'
const state = ref<AuthState | null>(null)
const health = ref<Health | null>(null)
const users = ref<AccessUser[]>([])
const sidebarOpen = ref(false)
const currentPage = ref<'overview' | 'servers' | 'proxies' | 'relays'>('overview')
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const username = ref('')
const password = ref('')
const confirmPassword = ref('')

const serverState = useServers(state, users, health, submitting, error, submit)
const serverView = reactive(serverState)
const { servers, loadServers, serverReorderingID } = serverState
const overviewState = useOverview(state, health, submitting, error, submit)
const overviewView = reactive(overviewState)
const { loadOverview, loadInvitations } = overviewState

let serverPollTimer: number | undefined

const invitationToken = new URLSearchParams(window.location.search).get('token') ?? ''

const isInvitationPage = computed(
  () => window.location.pathname === '/register' && invitationToken !== '',
)

async function loadState() {
  state.value = await api<AuthState>('/api/auth/state')
  if (state.value.authenticated) {
    const requests = [loadHealth(), loadServers(), loadUsers(), loadOverview()]
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

async function loadUsers() {
  const response = await api<{ users: AccessUser[] }>('/api/users')
  users.value = response.users
}

function startServerPolling() {
  stopServerPolling()
  serverPollTimer = window.setInterval(() => {
    if (!state.value?.authenticated || submitting.value || serverReorderingID.value !== null) return
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
    serverState.resetSession()
    overviewState.resetSession()
    users.value = []
    sidebarOpen.value = false
    currentPage.value = 'overview'
  })
}

async function submit(action: () => Promise<void>) {
  submitting.value = true
  error.value = ''
  try {
    await action()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '操作失败'
    if (reason instanceof APIError && reason.status === 404) {
      await serverState.handleMissingServer()
    }
  } finally {
    submitting.value = false
  }
}

function clearCredentials() {
  username.value = ''
  password.value = ''
  confirmPassword.value = ''
}

function selectPage(page: 'overview' | 'servers' | 'proxies' | 'relays') {
  currentPage.value = page
  sidebarOpen.value = false
  if (page === 'overview') {
    void loadOverview().catch((reason) => {
      error.value = reason instanceof Error ? reason.message : '无法加载概览'
    })
  }
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
    <div class="page-shell" :class="{ 'admin-shell': state?.authenticated }">
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

      <div v-else class="app-layout">
        <aside class="sidebar" :class="{ 'is-open': sidebarOpen }">
          <strong class="sidebar-brand">VPS Panel</strong>
          <nav class="sidebar-nav" aria-label="管理导航">
            <button
              type="button"
              :class="{ active: currentPage === 'overview' }"
              @click="selectPage('overview')"
            >
              概览
            </button>
            <button
              type="button"
              :class="{ active: currentPage === 'servers' }"
              @click="selectPage('servers')"
            >
              服务器
            </button>
            <button
              type="button"
              :class="{ active: currentPage === 'proxies' }"
              @click="selectPage('proxies')"
            >
              代理节点
            </button>
            <button
              type="button"
              :class="{ active: currentPage === 'relays' }"
              @click="selectPage('relays')"
            >
              中转
            </button>
          </nav>
          <div class="sidebar-account">
            <span>{{ state.user?.username }}</span>
            <n-button secondary block :loading="submitting" @click="logout">退出登录</n-button>
          </div>
        </aside>
        <button
          v-if="sidebarOpen"
          class="sidebar-backdrop"
          type="button"
          aria-label="关闭菜单"
          @click="sidebarOpen = false"
        />

        <main class="admin-main">
          <header class="mobile-header">
            <button type="button" aria-label="打开菜单" @click="sidebarOpen = true">☰</button>
            <strong>VPS Panel</strong>
          </header>
          <div class="admin-page">
            <header class="page-heading">
              <h1>{{ currentPage === 'overview' ? '概览' : currentPage === 'servers' ? '服务器' : currentPage === 'proxies' ? '代理节点' : '中转' }}</h1>
            </header>

            <n-alert v-if="error" class="page-alert" type="error">{{ error }}</n-alert>

        <OverviewView v-if="currentPage === 'overview'" :model="overviewView" />
        <ProxiesView v-if="currentPage === 'proxies'" :servers="servers" />
        <RelaysView v-if="currentPage === 'relays'" :servers="servers" />
        <ServersView :active="currentPage === 'servers'" :model="serverView" />


          </div>
        </main>
      </div>
    </div>
  </n-config-provider>
</template>
