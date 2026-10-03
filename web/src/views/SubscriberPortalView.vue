<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NModal, NProgress, NSpin, NTag } from 'naive-ui'
import { api } from '../api/client'
import QRCodeModal from '../components/share/QRCodeModal.vue'
import { formatTime } from '../format'
import { formatClientTrafficBytes } from '../proxy'
import type { User } from '../types/auth'
import AccountMenu from '../components/AccountMenu.vue'

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
  subscription_base64_url: string
  subscription_mihomo_url: string
  subscription_shadowrocket_url: string
  subscription_auto_url: string
  subscription_title: string
}
const props = defineProps<{ user: User }>()
const emit = defineEmits<{ logout: []; userUpdated: [user: User] }>()
const subscriber = ref<Subscriber | null>(null)
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const copied = ref<'base64' | 'mihomo' | 'shadowrocket' | ''>('')
const importModalOpen = ref(false)
const qrOpen = ref(false)
const hasPlan = computed(() => (subscriber.value?.plan_name ?? '').trim() !== '')

const usagePercent = computed(() => {
  const value = subscriber.value
	if (value?.traffic_limit_bytes === null || value?.traffic_limit_bytes === undefined) return 0
	if (value.traffic_limit_bytes === 0) return 100
  return Math.min(100, Math.round(value.used_bytes / value.traffic_limit_bytes * 100))
})
const statusLabels: Record<string, string> = {
  normal: '正常', unconfigured: '管理员尚未开通共享订阅', disabled: '账号已停用',
  plan_disabled: '共享订阅暂停 / 不可用', expired: '已到期', exhausted: '流量已用完',
}

async function loadPortal() {
  const me = await api<{ subscriber: Subscriber }>('/api/subscriber/me')
  subscriber.value = me.subscriber
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

async function copySubscription(format: 'base64' | 'mihomo' | 'shadowrocket', value: string) {
  if (!value) return
  await run(async () => {
    await navigator.clipboard.writeText(value)
    copied.value = format
  })
}

async function regenerateSubscription() {
  if (!window.confirm('重新生成后，旧订阅链接会立即失效。确定继续吗？')) return
  await run(async () => {
    const value = await api<{
      subscription_url: string
      subscription_base64_url: string
      subscription_mihomo_url: string
      subscription_shadowrocket_url: string
      subscription_auto_url: string
    }>('/api/subscriber/subscription/regenerate', { method: 'POST' })
    if (subscriber.value) Object.assign(subscriber.value, value)
    copied.value = ''
  })
}

function showSubscriptionQRCode() {
  importModalOpen.value = false
  qrOpen.value = true
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
      <strong class="app-brand app-brand--portal">夕凪云</strong>
      <AccountMenu :user="props.user" @updated="emit('userUpdated', $event)" @logout="emit('logout')" />
    </header>
    <n-alert v-if="error" type="error" class="page-alert">{{ error }}</n-alert>
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载订阅…</span></div>
    <template v-else-if="subscriber">
      <section v-if="!hasPlan" class="subscriber-empty-state">
        <h1>尚未开通共享订阅</h1>
        <p>当前账号尚未开通任何共享订阅</p>
      </section>
      <section v-else>
        <h2 class="portal-section-title">共享订阅信息</h2>
        <n-card :bordered="true">
          <template #header><strong>{{ subscriber.plan_name }}</strong></template>
          <template #header-extra><n-tag :type="subscriber.active ? 'success' : 'warning'">{{ statusLabels[subscriber.status] ?? subscriber.status }}</n-tag></template>
          <n-progress v-if="subscriber.traffic_limit_bytes !== null" type="line" :percentage="usagePercent" />
          <strong>{{ formatClientTrafficBytes(subscriber.used_bytes) }} / {{ subscriber.traffic_limit_bytes !== null ? formatClientTrafficBytes(subscriber.traffic_limit_bytes) : '不限流量' }}</strong>
          <dl class="user-details">
            <div><dt>下次重置</dt><dd>{{ subscriber.next_reset_at ? formatTime(subscriber.next_reset_at) : '不重置' }}</dd></div>
            <div><dt>到期</dt><dd>{{ subscriber.expires_at ? formatTime(subscriber.expires_at) : '不限' }}</dd></div>
            <div><dt>付款周期</dt><dd>{{ billingLabel(subscriber.billing_period_months) }}</dd></div>
            <div><dt>可用节点</dt><dd>{{ subscriber.enabled_node_count }}</dd></div>
          </dl>
          <div class="subscriber-plan-actions"><n-button type="primary" :disabled="busy" @click="importModalOpen = true">导入订阅</n-button></div>
        </n-card>
      </section>
    </template>
  </main>

  <n-modal v-if="hasPlan" v-model:show="importModalOpen">
    <n-card class="client-form-card subscription-import-card" title="导入订阅" closable @close="importModalOpen = false">
      <div class="subscription-import-list">
        <section class="subscription-import-option">
          <div><strong>Base64 通用订阅</strong><p>适用于支持 VLESS / Shadowsocks URI Base64 订阅的客户端</p></div>
          <n-button secondary :disabled="busy" @click="copySubscription('base64', subscriber?.subscription_base64_url || '')">{{ copied === 'base64' ? '已复制' : '复制地址' }}</n-button>
        </section>
        <section class="subscription-import-option">
          <div><strong>Clash / Mihomo</strong><p>适用于 Clash Verge Rev、Mihomo、FlClash 等</p></div>
          <n-button secondary :disabled="busy" @click="copySubscription('mihomo', subscriber?.subscription_mihomo_url || '')">{{ copied === 'mihomo' ? '已复制' : '复制地址' }}</n-button>
        </section>
        <section class="subscription-import-option">
          <div><strong>Shadowrocket</strong><p>完整 .conf 配置，请在 Shadowrocket 的配置页面从 URL 导入</p></div>
          <n-button secondary :disabled="busy" @click="copySubscription('shadowrocket', subscriber?.subscription_shadowrocket_url || '')">{{ copied === 'shadowrocket' ? '已复制' : '复制地址' }}</n-button>
        </section>
        <section class="subscription-import-option">
          <div><strong>扫描二维码订阅</strong><p>扫描后自动适配常见订阅客户端</p></div>
          <n-button secondary :disabled="busy" @click="showSubscriptionQRCode">显示二维码</n-button>
        </section>
      </div>
      <div class="modal-actions"><n-button type="error" secondary :disabled="busy" @click="regenerateSubscription">重新生成订阅</n-button><n-button @click="importModalOpen = false">关闭</n-button></div>
    </n-card>
  </n-modal>

  <QRCodeModal
    v-if="hasPlan"
    :show="qrOpen"
    :uri="subscriber?.subscription_auto_url || ''"
    :title="subscriber?.subscription_title || subscriber?.plan_name || '订阅'"
    modal-title="扫描二维码订阅"
    instruction="使用支持订阅二维码的客户端扫描导入。"
    @update:show="qrOpen = $event"
  />
</template>
