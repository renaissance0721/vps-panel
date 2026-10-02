<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { NAlert, NButton, NCard, NInput, NInputNumber, NModal, NSelect, NSpin, NSwitch } from 'naive-ui'
import { api } from '../../api/client'

defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [value: boolean] }>()
type Settings = {
  telegram_token_set: boolean; telegram_chat_id: string; online_enabled: boolean; offline_grace_minutes: number
  recovery_enabled: boolean; traffic_enabled: boolean; traffic_threshold_percent: number; traffic_step_percent: number; traffic_full_enabled: boolean
}
type TestResult = { id: string; status: 'pending' | 'success' | 'error'; error?: string }
const form = ref<Settings>({ telegram_token_set: false, telegram_chat_id: '', online_enabled: true, offline_grace_minutes: 3,
  recovery_enabled: true, traffic_enabled: true, traffic_threshold_percent: 80, traffic_step_percent: 10, traffic_full_enabled: true })
const botToken = ref('')
const clearToken = ref(false)
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const testing = ref(false)
const error = ref('')
const testError = ref('')
const testSuccess = ref(false)
const stepOptions = [5, 10, 20].map(value => ({ label: `${value}%`, value }))
const controller = new AbortController()
let pollTimer: ReturnType<typeof setTimeout> | undefined

async function load() {
  loading.value = true
  error.value = ''
  try { form.value = await api<Settings>('/api/notifications/settings', { signal: controller.signal }); loaded.value = true }
  catch (err) { if (!controller.signal.aborted) error.value = err instanceof Error ? err.message : '加载通知设置失败' }
  finally { loading.value = false }
}
async function save() {
  saving.value = true
  error.value = ''
  try {
    await api<Settings>('/api/notifications/settings', { method: 'PUT', signal: controller.signal,
      body: JSON.stringify({ ...form.value, telegram_bot_token: botToken.value, clear_telegram_token: clearToken.value }) })
    botToken.value = ''
    emit('update:show', false)
  } catch (err) { if (!controller.signal.aborted) error.value = err instanceof Error ? err.message : '保存通知设置失败' }
  finally { saving.value = false }
}
async function pollTest(id: string) {
  try {
    const result = await api<TestResult>(`/api/notifications/test/${encodeURIComponent(id)}`, { signal: controller.signal })
    if (result.status === 'pending') { pollTimer = setTimeout(() => { void pollTest(id) }, 1000); return }
    testing.value = false
    testSuccess.value = result.status === 'success'
    testError.value = result.status === 'error' ? result.error || '测试发送失败' : ''
  } catch (err) {
    testing.value = false
    if (!controller.signal.aborted) testError.value = err instanceof Error ? err.message : '查询测试结果失败'
  }
}
async function sendTest() {
  testing.value = true
  testError.value = ''
  testSuccess.value = false
  try {
    const result = await api<TestResult>('/api/notifications/test', { method: 'POST', signal: controller.signal,
      body: JSON.stringify({ telegram_bot_token: botToken.value, telegram_chat_id: form.value.telegram_chat_id }) })
    await pollTest(result.id)
  } catch (err) {
    testing.value = false
    if (!controller.signal.aborted) testError.value = err instanceof Error ? err.message : '测试发送失败'
  }
}
onMounted(load)
onUnmounted(() => { controller.abort(); clearTimeout(pollTimer); botToken.value = '' })
</script>

<template>
  <n-modal :show="show" @update:show="emit('update:show', $event)">
    <n-card class="client-form-card notification-settings" title="通知设置" closable @close="emit('update:show', false)">
      <n-alert v-if="error" type="error">{{ error }}</n-alert>
      <n-spin v-if="loading" description="正在加载通知设置…" />
      <n-button v-else-if="!loaded" @click="load">重新加载</n-button>
      <form v-else class="notification-form" @submit.prevent="save">
        <h3>Telegram</h3>
        <label>Bot Token<n-input v-model:value="botToken" type="password" show-password-on="click" :maxlength="256" :disabled="clearToken"
          :placeholder="form.telegram_token_set ? '已配置，留空保留原 Token' : '输入 Bot Token'" :input-props="{ autocomplete: 'new-password' }" aria-label="Bot Token" /></label>
        <div v-if="form.telegram_token_set" class="notification-switch-row"><span>清除已保存的 Token</span><n-switch v-model:value="clearToken" @update:value="botToken = ''" aria-label="清除已保存的 Token" /></div>
        <label>Chat ID<n-input v-model:value="form.telegram_chat_id" :maxlength="32" placeholder="个人或群组数字 ID，例如 -1001234567890" aria-label="Chat ID" /></label>
        <p class="notification-help">请先向 Bot 发送消息，或将 Bot 加入群组并授予发送权限。测试使用当前填写的内容，Token 留空则使用已保存的值。</p>
        <n-button :loading="testing" :disabled="saving || clearToken" @click="sendTest">发送测试消息</n-button>
        <n-alert v-if="testSuccess" type="success">测试消息已发送，请在 Telegram 中查看。</n-alert>
        <n-alert v-if="testError" type="error">{{ testError }}</n-alert>
        <h3>在线状态</h3>
        <div class="notification-switch-row"><span>离线通知</span><n-switch v-model:value="form.online_enabled" aria-label="离线通知" /></div>
        <label>离线宽限（分钟）<n-input-number v-model:value="form.offline_grace_minutes" :min="1" :max="60" :precision="0" :disabled="!form.online_enabled" aria-label="离线宽限（分钟）" /></label>
        <div class="notification-switch-row"><span>恢复通知</span><n-switch v-model:value="form.recovery_enabled" :disabled="!form.online_enabled" aria-label="恢复通知" /></div>
        <h3>流量</h3>
        <div class="notification-switch-row"><span>流量通知</span><n-switch v-model:value="form.traffic_enabled" aria-label="流量通知" /></div>
        <label>提醒起点（%）<n-input-number v-model:value="form.traffic_threshold_percent" :min="50" :max="100" :precision="0" :disabled="!form.traffic_enabled" aria-label="提醒起点（%）" /></label>
        <label>后续每增加<n-select v-model:value="form.traffic_step_percent" :options="stepOptions" :disabled="!form.traffic_enabled" aria-label="流量提醒步长" /></label>
        <div class="notification-switch-row"><span>100% 用尽提醒</span><n-switch v-model:value="form.traffic_full_enabled" :disabled="!form.traffic_enabled" aria-label="100% 用尽提醒" /></div>
        <div class="modal-actions"><n-button :disabled="saving" @click="emit('update:show', false)">取消</n-button><n-button attr-type="submit" type="primary" :loading="saving">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>
</template>

<style scoped>
.notification-settings { width: min(560px, calc(100vw - 32px)); max-height: calc(100vh - 48px); overflow-y: auto; }
.notification-form { display: grid; gap: 14px; }
.notification-form h3 { margin: 10px 0 0; }
.notification-form label { display: grid; gap: 6px; }
.notification-switch-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.notification-help { margin: 0; color: var(--color-text-secondary); font-size: 13px; }
</style>
