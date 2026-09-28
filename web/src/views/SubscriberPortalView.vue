<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NModal, NProgress, NSpin, NTag } from 'naive-ui'
import { api } from '../api/client'
import { formatTime } from '../format'
import { formatClientTrafficBytes } from '../proxy'
import type { User } from '../types/auth'

type Subscriber = {
  username: string
  plan_name: string
  enabled: boolean
  expires_at: string | null
  enabled_node_count: number
  traffic_limit_bytes: number | null
  used_bytes: number
  cycle_started_at: string
  next_reset_at: string | null
  billing_period_months: number | null
  active: boolean
  status: string
  subscription_url: string
}
type PublishedNode = { id: number; name: string; mode: 'direct' | 'relay'; enabled: boolean }
type PasswordRequest = { id: number; status: 'pending' | 'approved' | 'rejected'; created_at: string; reviewed_at: string | null }

const props = defineProps<{ user: User }>()
const emit = defineEmits<{ logout: [] }>()
const subscriber = ref<Subscriber | null>(null)
const nodes = ref<PublishedNode[]>([])
const passwordRequest = ref<PasswordRequest | null>(null)
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const copied = ref(false)
const passwordModalOpen = ref(false)
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')

const usagePercent = computed(() => {
  const value = subscriber.value
	if (value?.traffic_limit_bytes === null || value?.traffic_limit_bytes === undefined) return 0
	if (value.traffic_limit_bytes === 0) return 100
  return Math.min(100, Math.round(value.used_bytes / value.traffic_limit_bytes * 100))
})
const pendingPasswordRequest = computed(() => passwordRequest.value?.status === 'pending')

const statusLabels: Record<string, string> = {
  normal: '正常', unconfigured: '管理员尚未开通套餐', disabled: '账号已停用',
  plan_disabled: '套餐暂停 / 不可用', expired: '已到期', exhausted: '流量已用完',
}

async function loadPortal() {
  const [me, nodeResult, requestResult] = await Promise.all([
    api<{ subscriber: Subscriber }>('/api/subscriber/me'),
    api<{ nodes: PublishedNode[] }>('/api/subscriber/nodes'),
    api<{ request: PasswordRequest | null }>('/api/subscriber/password-change-request'),
  ])
  subscriber.value = me.subscriber
  nodes.value = nodeResult.nodes
  passwordRequest.value = requestResult.request
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

async function copySubscription() {
  const value = subscriber.value?.subscription_url
  if (!value) return
  await run(async () => {
    await navigator.clipboard.writeText(value)
    copied.value = true
  })
}

async function regenerateSubscription() {
  if (!window.confirm('重新生成后，旧订阅链接会立即失效。确定继续吗？')) return
  await run(async () => {
    const value = await api<{ subscription_url: string }>('/api/subscriber/subscription/regenerate', { method: 'POST' })
    if (subscriber.value) subscriber.value.subscription_url = value.subscription_url
    copied.value = false
  })
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
    const response = await api<{ request: PasswordRequest }>('/api/subscriber/password-change-request', {
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

function billingLabel(months: number | null) {
  return ({ 1: '月付', 3: '季付', 6: '半年付', 12: '年付' } as Record<number, string>)[months ?? 0] ?? '未设置'
}

onMounted(async () => {
  try {
    await loadPortal()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法加载订阅信息'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <main class="user-portal subscriber-portal">
    <header class="user-portal-header">
      <div><strong>VPS Panel</strong><span>{{ props.user.username }}</span></div>
      <n-button secondary :loading="busy" @click="emit('logout')">退出登录</n-button>
    </header>
    <n-alert v-if="error" type="error" class="page-alert">{{ error }}</n-alert>
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载订阅…</span></div>
    <template v-else-if="subscriber">
      <section>
        <h1>我的订阅</h1>
        <n-card :bordered="true">
          <template #header><strong>{{ subscriber.plan_name || '尚未开通套餐' }}</strong></template>
          <template #header-extra><n-tag :type="subscriber.active ? 'success' : 'warning'">{{ statusLabels[subscriber.status] ?? subscriber.status }}</n-tag></template>
          <n-progress v-if="subscriber.traffic_limit_bytes !== null" type="line" :percentage="usagePercent" />
          <strong>{{ formatClientTrafficBytes(subscriber.used_bytes) }} / {{ subscriber.traffic_limit_bytes !== null ? formatClientTrafficBytes(subscriber.traffic_limit_bytes) : '不限流量' }}</strong>
          <dl class="user-details">
            <div><dt>下次重置</dt><dd>{{ subscriber.next_reset_at ? formatTime(subscriber.next_reset_at) : '不重置' }}</dd></div>
            <div><dt>到期</dt><dd>{{ subscriber.expires_at ? formatTime(subscriber.expires_at) : '不限' }}</dd></div>
            <div><dt>付款周期</dt><dd>{{ billingLabel(subscriber.billing_period_months) }}</dd></div>
            <div><dt>可用节点</dt><dd>{{ subscriber.enabled_node_count }}</dd></div>
          </dl>
        </n-card>
      </section>
      <section>
        <h1>订阅链接</h1>
        <n-card :bordered="true">
          <n-input :value="subscriber.subscription_url" readonly />
          <div class="modal-actions">
            <n-button type="primary" :disabled="busy" @click="copySubscription">{{ copied ? '已复制' : '复制订阅' }}</n-button>
            <n-button secondary :disabled="busy" @click="regenerateSubscription">重新生成</n-button>
          </div>
        </n-card>
      </section>
      <section>
        <h1>可用节点</h1>
        <n-empty v-if="nodes.length === 0" description="暂无可用节点" />
        <n-card v-else :bordered="true">
          <div class="subscriber-node-list"><div v-for="node in nodes" :key="node.id">{{ node.name }}</div></div>
        </n-card>
      </section>
      <section>
        <h1>账号</h1>
        <n-card :bordered="true">
          <dl class="user-details">
            <div><dt>用户名</dt><dd>{{ subscriber.username }}</dd></div>
            <div><dt>密码修改</dt><dd>{{ pendingPasswordRequest ? '等待管理员审核' : passwordRequest?.status === 'approved' ? '最近申请已批准' : passwordRequest?.status === 'rejected' ? '最近申请已拒绝' : '无待处理申请' }}</dd></div>
          </dl>
          <n-button type="primary" :disabled="pendingPasswordRequest || busy" @click="passwordModalOpen = true">申请修改密码</n-button>
        </n-card>
      </section>
    </template>
  </main>

  <n-modal v-model:show="passwordModalOpen">
    <n-card class="client-form-card" title="申请修改密码" closable @close="passwordModalOpen = false">
      <form class="auth-form" @submit.prevent="submitPasswordRequest">
        <label><span>当前密码</span><n-input v-model:value="currentPassword" type="password" show-password-on="click" /></label>
        <label><span>新密码</span><n-input v-model:value="newPassword" type="password" show-password-on="click" /></label>
        <label><span>确认新密码</span><n-input v-model:value="confirmPassword" type="password" show-password-on="click" /></label>
        <div class="modal-actions"><n-button @click="passwordModalOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">提交申请</n-button></div>
      </form>
    </n-card>
  </n-modal>
</template>
