<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { NAlert, NButton, NCard, NInput, NInputNumber, NSelect, NSpin, NSwitch } from 'naive-ui'
import { api } from '../api/client'

type Security = 'tls' | 'starttls' | 'none'
type MailSettings = {
  enabled: boolean
  host: string
  port: number
  security: Security
  username: string
  password_configured: boolean
  from_address: string
  from_name: string
  reply_to: string
}

const defaults: MailSettings = {
  enabled: false,
  host: '',
  port: 587,
  security: 'starttls',
  username: '',
  password_configured: false,
  from_address: '',
  from_name: 'VPS Panel',
  reply_to: '',
}
const form = ref<MailSettings>({ ...defaults })
const password = ref('')
const testRecipient = ref('')
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const testing = ref(false)
const error = ref('')
const success = ref('')
const testError = ref('')
const testSuccess = ref('')
const controller = new AbortController()
const securityOptions = [
  { label: 'TLS（通常为 465）', value: 'tls' },
  { label: 'STARTTLS（通常为 587）', value: 'starttls' },
  { label: '无 TLS（不推荐）', value: 'none' },
]

function requestBody() {
  return {
    enabled: form.value.enabled,
    host: form.value.host,
    port: form.value.port,
    security: form.value.security,
    username: form.value.username,
    password: password.value,
    from_address: form.value.from_address,
    from_name: form.value.from_name,
    reply_to: form.value.reply_to,
  }
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    form.value = await api<MailSettings>('/api/admin/settings/mail', { signal: controller.signal })
    password.value = ''
    loaded.value = true
  } catch (reason) {
    if (!controller.signal.aborted) error.value = reason instanceof Error ? reason.message : '加载邮件设置失败'
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  error.value = ''
  success.value = ''
  try {
    form.value = await api<MailSettings>('/api/admin/settings/mail', {
      method: 'PUT',
      signal: controller.signal,
      body: JSON.stringify(requestBody()),
    })
    password.value = ''
    success.value = '邮件服务设置已保存。'
  } catch (reason) {
    if (!controller.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存邮件设置失败'
  } finally {
    saving.value = false
  }
}

async function sendTest() {
  testing.value = true
  testError.value = ''
  testSuccess.value = ''
  try {
    await api('/api/admin/settings/mail/test', {
      method: 'POST',
      signal: controller.signal,
      body: JSON.stringify({ ...requestBody(), to: testRecipient.value }),
    })
    testSuccess.value = '测试邮件已发送，请检查收件箱。'
  } catch (reason) {
    if (!controller.signal.aborted) testError.value = reason instanceof Error ? reason.message : '测试邮件发送失败'
  } finally {
    testing.value = false
  }
}

onMounted(load)
onUnmounted(() => {
  controller.abort()
  password.value = ''
})
</script>

<template>
  <n-card title="邮件服务" :bordered="true" class="mail-settings-card">
    <n-alert v-if="error" type="error" class="mail-alert">{{ error }}</n-alert>
    <n-alert v-if="success" type="success" class="mail-alert">{{ success }}</n-alert>
    <n-spin v-if="loading" description="正在加载邮件设置…" />
    <n-button v-else-if="!loaded" @click="load">重新加载</n-button>
    <form v-else class="mail-settings-form" @submit.prevent="save">
      <div class="mail-switch-row">
        <div>
          <strong>启用邮件服务</strong>
          <p>停用后会保留配置；管理员仍可发送测试邮件。</p>
        </div>
        <n-switch v-model:value="form.enabled" aria-label="启用邮件服务" />
      </div>

      <div class="mail-form-grid">
        <label class="mail-wide-field">
          <span>SMTP 服务器</span>
          <n-input v-model:value="form.host" :maxlength="253" placeholder="smtp.example.com" aria-label="SMTP 服务器" />
        </label>
        <label>
          <span>端口</span>
          <n-input-number v-model:value="form.port" :min="1" :max="65535" :precision="0" aria-label="SMTP 端口" />
        </label>
        <label>
          <span>加密方式</span>
          <n-select v-model:value="form.security" :options="securityOptions" aria-label="加密方式" />
        </label>
        <n-alert v-if="form.security === 'none'" type="warning" class="mail-wide-field">
          不推荐。认证信息及邮件内容可能以明文传输。
        </n-alert>
        <label>
          <span>SMTP 用户名</span>
          <n-input v-model:value="form.username" :maxlength="320" placeholder="noreply@example.com" aria-label="SMTP 用户名" />
        </label>
        <label>
          <span>SMTP 密码</span>
          <n-input
            v-model:value="password"
            type="password"
            show-password-on="click"
            :maxlength="1024"
            :placeholder="form.password_configured ? '已配置，留空表示保持不变' : '未配置'"
            :input-props="{ autocomplete: 'new-password' }"
            aria-label="SMTP 密码"
          />
          <small v-if="form.password_configured">已配置，留空表示保持不变。</small>
        </label>
        <label>
          <span>发件地址</span>
          <n-input v-model:value="form.from_address" :maxlength="320" placeholder="noreply@example.com" aria-label="发件地址" />
        </label>
        <label>
          <span>发件人名称</span>
          <n-input v-model:value="form.from_name" :maxlength="100" placeholder="VPS Panel" aria-label="发件人名称" />
        </label>
        <label class="mail-wide-field">
          <span>Reply-To（可选）</span>
          <n-input v-model:value="form.reply_to" :maxlength="320" placeholder="support@example.com" aria-label="Reply-To" />
        </label>
      </div>

      <section class="mail-test-section">
        <h3>测试邮件</h3>
        <p>使用当前表单内容直接测试，不会先保存设置。密码留空时会使用已保存的密码。</p>
        <div class="mail-test-row">
          <n-input v-model:value="testRecipient" :maxlength="320" placeholder="test@example.com" aria-label="测试收件地址" />
          <n-button attr-type="button" :loading="testing" :disabled="testing || saving" @click="sendTest">发送测试邮件</n-button>
        </div>
        <n-alert v-if="testSuccess" type="success">{{ testSuccess }}</n-alert>
        <n-alert v-if="testError" type="error">{{ testError }}</n-alert>
      </section>

      <div class="mail-actions">
        <n-button type="primary" attr-type="submit" :loading="saving" :disabled="saving || testing">保存</n-button>
      </div>
    </form>
  </n-card>
</template>

<style scoped>
.mail-settings-card { max-width: 860px; }
.mail-alert { margin-bottom: 14px; }
.mail-settings-form { display: grid; gap: 22px; }
.mail-switch-row { display: flex; justify-content: space-between; align-items: center; gap: 20px; }
.mail-switch-row p, .mail-test-section p { margin: 4px 0 0; color: var(--color-text-secondary); font-size: 13px; }
.mail-form-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 16px; }
.mail-form-grid label { display: grid; gap: 7px; }
.mail-form-grid small { color: var(--color-text-secondary); }
.mail-wide-field { grid-column: 1 / -1; }
.mail-test-section { display: grid; gap: 12px; padding-top: 18px; border-top: 1px solid var(--color-border); }
.mail-test-section h3 { margin: 0; }
.mail-test-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 12px; }
.mail-actions { display: flex; justify-content: flex-end; }
@media (max-width: 720px) {
  .mail-form-grid, .mail-test-row { grid-template-columns: 1fr; }
  .mail-wide-field { grid-column: auto; }
  .mail-test-row .n-button, .mail-actions .n-button { width: 100%; }
}
</style>
