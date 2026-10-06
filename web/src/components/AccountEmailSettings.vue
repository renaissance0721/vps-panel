<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NAlert, NButton, NInput, NSpin, NTag } from 'naive-ui'
import { api } from '../api/client'

type EmailStatus = {
  email: string
  verified: boolean
  pending_email: string
  available: boolean
  unavailable_reason?: string
}

const status = ref<EmailStatus | null>(null)
const loading = ref(true)
const sending = ref(false)
const resending = ref(false)
const editing = ref(false)
const resendOpen = ref(false)
const email = ref('')
const currentPassword = ref('')
const resendPassword = ref('')
const error = ref('')
const message = ref('')

async function loadStatus() {
  status.value = await api<EmailStatus>('/api/account/email')
}

function openEditor() {
  email.value = status.value?.pending_email || ''
  currentPassword.value = ''
  resendOpen.value = false
  editing.value = true
  error.value = ''
  message.value = ''
}

async function sendVerification() {
  if (sending.value || resending.value) return
  sending.value = true
  error.value = ''
  message.value = ''
  try {
    await api('/api/account/email/request', {
      method: 'POST',
      body: JSON.stringify({ email: email.value, current_password: currentPassword.value }),
    })
    message.value = '验证邮件已发送，请在 30 分钟内完成验证。'
    currentPassword.value = ''
    editing.value = false
    await loadStatus()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '验证邮件发送失败'
  } finally {
    sending.value = false
  }
}

async function resendVerification() {
  if (sending.value || resending.value) return
  resending.value = true
  error.value = ''
  message.value = ''
  try {
    await api('/api/account/email/resend', {
      method: 'POST',
      body: JSON.stringify({ current_password: resendPassword.value }),
    })
    message.value = '验证邮件已重新发送，请在 30 分钟内完成验证。'
    resendPassword.value = ''
    resendOpen.value = false
    await loadStatus()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '验证邮件重新发送失败'
  } finally {
    resending.value = false
  }
}

onMounted(async () => {
  try {
    await loadStatus()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法加载邮箱状态'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <section class="account-email-settings">
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载邮箱状态…</span></div>
    <template v-else>
      <n-alert v-if="error" type="error">{{ error }}</n-alert>
      <n-alert v-if="message" type="success">{{ message }}</n-alert>
      <n-alert v-if="status && !status.available" type="warning">
        邮箱功能暂不可用：{{ status.unavailable_reason || '管理员尚未启用邮件服务' }}
      </n-alert>

      <dl v-if="status" class="user-details account-email-details">
        <div>
          <dt>当前邮箱</dt>
          <dd>{{ status.email || '未绑定' }} <n-tag v-if="status.verified" size="small" type="success">已验证</n-tag></dd>
        </div>
        <div v-if="status.pending_email">
          <dt>待验证新邮箱</dt>
          <dd>{{ status.pending_email }} <n-tag size="small" type="warning">待验证</n-tag></dd>
        </div>
      </dl>

      <form v-if="editing" class="auth-form" @submit.prevent="sendVerification">
        <label><span>邮箱地址</span><n-input v-model:value="email" :input-props="{ autocomplete: 'email' }" /></label>
        <label><span>当前密码</span><n-input v-model:value="currentPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'current-password' }" /></label>
        <div class="modal-actions"><n-button @click="editing = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="sending">发送验证邮件</n-button></div>
      </form>

      <form v-else-if="resendOpen" class="auth-form" @submit.prevent="resendVerification">
        <n-alert type="info">重新发送前，请再次输入当前密码。</n-alert>
        <label><span>当前密码</span><n-input v-model:value="resendPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'current-password' }" /></label>
        <div class="modal-actions"><n-button @click="resendOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="resending">重新发送验证邮件</n-button></div>
      </form>

      <div v-else-if="status?.available" class="modal-actions">
        <n-button v-if="status.pending_email" secondary @click="resendOpen = true">重新发送</n-button>
        <n-button type="primary" @click="openEditor">{{ status.email || status.pending_email ? '更换邮箱' : '绑定邮箱' }}</n-button>
      </div>
    </template>
  </section>
</template>

<style scoped>
.account-email-settings {
  display: grid;
  gap: 14px;
}
.account-email-details {
  margin: 0;
}
.account-email-details dd {
  overflow-wrap: anywhere;
}
</style>
