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
  NModal,
  type GlobalThemeOverrides,
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
import UserPortalView from './views/UserPortalView.vue'
import SubscriberPortalView from './views/SubscriberPortalView.vue'
import AccountManagementView from './views/AccountManagementView.vue'
import CarpoolPanelView from './views/CarpoolPanelView.vue'
import SubscriptionManagementView from './views/SubscriptionManagementView.vue'
import AccountMenu from './components/AccountMenu.vue'

const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#4f9fe8',
    primaryColorHover: '#69afea',
    primaryColorPressed: '#3789d2',
    primaryColorSuppl: '#4f9fe8',
    bodyColor: '#f5f9fe',
    cardColor: '#ffffff',
    modalColor: '#ffffff',
    popoverColor: '#ffffff',
    borderRadius: '9px',
    borderRadiusSmall: '7px',
  },
}
const state = ref<AuthState | null>(null)
const health = ref<Health | null>(null)
const users = ref<AccessUser[]>([])
const sidebarOpen = ref(false)
type AdminPage = 'overview' | 'servers' | 'proxies' | 'relays' | 'subscriptions' | 'accounts' | 'carpool'
const pageTitles: Record<AdminPage, string> = {
  overview: '概览',
  servers: '服务器',
  proxies: '代理节点',
  relays: '中转',
  subscriptions: '订阅管理',
  accounts: '用户管理',
  carpool: '拼车面板',
}
const currentPage = ref<AdminPage>('overview')
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const username = ref('')
const password = ref('')
const confirmPassword = ref('')
const invitationRole = ref<'vip' | 'user' | 'subscriber' | null>(null)
const passwordResetOpen = ref(false)
const passwordResetUsername = ref('')
const passwordResetPassword = ref('')
const passwordResetConfirm = ref('')
const passwordResetError = ref('')
const passwordResetStatus = ref('')
const passwordResetBusy = ref(false)

const serverState = useServers(state, users, health, submitting, error, submit)
const serverView = reactive(serverState)
const { servers, loadServers, serverReorderingID } = serverState
const overviewState = useOverview(state, health, submitting, error, submit)
const overviewView = reactive(overviewState)
const { loadOverview, loadInvitations, loadPasswordChangeRequests, loadUserRelays } = overviewState

let serverPollTimer: number | undefined

const invitationToken = new URLSearchParams(window.location.search).get('token') ?? ''

const isInvitationPage = computed(
  () => window.location.pathname === '/register' && invitationToken !== '',
)

async function loadState() {
  state.value = await api<AuthState>('/api/auth/state')
  if (!state.value.authenticated && isInvitationPage.value) {
    const response = await api<{ invitation: { role: 'vip' | 'user' | 'subscriber' } }>(
      `/api/auth/invitation?token=${encodeURIComponent(invitationToken)}`,
    )
    invitationRole.value = response.invitation.role
  }
  if (state.value.authenticated) {
	if (state.value.user?.role === 'user' || state.value.user?.role === 'subscriber') {
	  stopServerPolling()
	  return
	}
    const requests = [loadHealth(), loadServers(), loadUsers(), loadOverview()]
    if (state.value.user?.role === 'admin') {
	  requests.push(loadInvitations(), loadPasswordChangeRequests(), loadUserRelays())
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

function openPasswordReset() {
  passwordResetUsername.value = username.value
  passwordResetPassword.value = ''
  passwordResetConfirm.value = ''
  passwordResetError.value = ''
  passwordResetStatus.value = ''
  passwordResetOpen.value = true
}

async function requestPasswordReset() {
  const passwordBytes = new TextEncoder().encode(passwordResetPassword.value).length
  if (passwordBytes < 10 || passwordBytes > 72) {
    passwordResetError.value = '新密码长度需为 10–72 字节'
    return
  }
  if (passwordResetPassword.value !== passwordResetConfirm.value) {
    passwordResetError.value = '两次输入的新密码不一致'
    return
  }
  passwordResetBusy.value = true
  passwordResetError.value = ''
  passwordResetStatus.value = ''
  try {
    await api('/api/auth/password-reset-request', {
      method: 'POST',
      body: JSON.stringify({
        username: passwordResetUsername.value,
        new_password: passwordResetPassword.value,
      }),
    })
    passwordResetStatus.value = '如果该账号存在，重置申请已提交，请等待管理员审核。'
    passwordResetPassword.value = ''
    passwordResetConfirm.value = ''
  } catch (reason) {
    passwordResetError.value = reason instanceof Error ? reason.message : '密码重置申请提交失败'
  } finally {
    passwordResetBusy.value = false
  }
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

function updateCurrentUser(user: NonNullable<AuthState['user']>) {
  if (state.value) state.value.user = user
}

function selectPage(page: AdminPage) {
  currentPage.value = page
  sidebarOpen.value = false
  if (page === 'overview') {
    const requests: Promise<void>[] = [loadOverview()]
    if (state.value?.user?.role === 'admin') requests.push(loadUserRelays(), loadPasswordChangeRequests())
    void Promise.all(requests).catch((reason) => {
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
<n-config-provider :theme-overrides="themeOverrides">
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
        <h1>创建受邀账户</h1>
        <p class="description">此邀请将创建{{ invitationRole === 'vip' ? ' VIP' : invitationRole === 'subscriber' ? '订阅用户' : '普通用户' }}账号，仅可使用一次。</p>
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
        <h1>初始化夕凪云</h1>
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
        <p class="eyebrow">夕凪云</p>
        <h1>登录管理面板</h1>
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
          <n-button text type="primary" attr-type="button" @click="openPasswordReset">忘记密码？</n-button>
        </form>
      </n-card>

      <UserPortalView v-else-if="state.user?.role === 'user'" :user="state.user" @user-updated="updateCurrentUser" @logout="logout" />
      <SubscriberPortalView v-else-if="state.user?.role === 'subscriber'" :user="state.user" @user-updated="updateCurrentUser" @logout="logout" />

      <div v-else class="app-layout">
        <aside class="sidebar" :class="{ 'is-open': sidebarOpen }">
          <strong class="app-brand app-brand--sidebar">夕凪云</strong>
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
            <button
              v-if="state.user?.role === 'admin'"
              type="button"
              :class="{ active: currentPage === 'subscriptions' }"
              @click="selectPage('subscriptions')"
            >
              订阅管理
            </button>
            <button
              v-if="state.user?.role === 'admin'"
              type="button"
              :class="{ active: currentPage === 'accounts' }"
              @click="selectPage('accounts')"
            >
              用户管理
            </button>
            <button
              v-if="state.user?.role === 'admin'"
              type="button"
              :class="{ active: currentPage === 'carpool' }"
              @click="selectPage('carpool')"
            >
              拼车面板
            </button>
          </nav>
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
            <strong class="app-brand app-brand--sidebar">夕凪云</strong>
          </header>
          <div class="admin-page">
            <header class="page-heading">
              <h1>{{ pageTitles[currentPage] }}</h1>
              <AccountMenu v-if="state.user" :user="state.user" @updated="updateCurrentUser" @logout="logout" />
            </header>

            <n-alert v-if="error" class="page-alert" type="error">{{ error }}</n-alert>

        <OverviewView v-if="currentPage === 'overview'" :model="overviewView" />
        <ProxiesView v-if="currentPage === 'proxies'" :servers="servers" :users="users" :role="state.user?.role" />
        <RelaysView v-if="currentPage === 'relays'" :servers="servers" />
        <SubscriptionManagementView v-if="currentPage === 'subscriptions' && state.user?.role === 'admin'" />
        <AccountManagementView v-if="currentPage === 'accounts' && state.user?.role === 'admin'" />
        <CarpoolPanelView v-if="currentPage === 'carpool' && state.user?.role === 'admin'" />
        <ServersView :active="currentPage === 'servers'" :model="serverView" />


          </div>
        </main>
      </div>
    </div>
    <n-modal v-model:show="passwordResetOpen">
      <n-card class="account-modal-card" title="申请重置密码" closable @close="passwordResetOpen = false">
        <form class="auth-form" @submit.prevent="requestPasswordReset">
          <n-alert type="info">提交后当前密码不会立即改变。管理员审核通过后，新密码才会生效。为保护账号信息，系统不会确认用户名是否存在。</n-alert>
          <n-alert v-if="passwordResetError" type="error">{{ passwordResetError }}</n-alert>
          <n-alert v-if="passwordResetStatus" type="success">{{ passwordResetStatus }}</n-alert>
          <label><span>用户名</span><n-input v-model:value="passwordResetUsername" :input-props="{ autocomplete: 'username' }" /></label>
          <label><span>新密码</span><n-input v-model:value="passwordResetPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" /></label>
          <label><span>确认新密码</span><n-input v-model:value="passwordResetConfirm" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" /></label>
          <div class="modal-actions"><n-button @click="passwordResetOpen = false">关闭</n-button><n-button type="primary" attr-type="submit" :loading="passwordResetBusy">提交申请</n-button></div>
        </form>
      </n-card>
    </n-modal>
  </n-config-provider>
</template>
