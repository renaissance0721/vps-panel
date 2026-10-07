<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { NAlert, NButton, NCard, NDropdown, NInput, NModal, NSpin, type DropdownOption } from 'naive-ui'
import { api, APIError } from '../api/client'
import { maskEmail } from '../format'
import type { EmailStatus, User } from '../types/auth'
import AccountEmailSettings from './AccountEmailSettings.vue'

const props = defineProps<{ user: User }>()
const emit = defineEmits<{ updated: [user: User]; logout: [] }>()

const renameOpen = ref(false)
const passwordOpen = ref(false)
const emailOpen = ref(false)
const resetOpen = ref(false)
const busy = ref(false)
const formError = ref('')
const formStatus = ref('')
const newUsername = ref('')
const renamePassword = ref('')
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const resetPassword = ref('')
const resetConfirmPassword = ref('')
const resetMode = ref<'email' | 'admin'>('email')
const resetEmailStatus = ref<EmailStatus | null>(null)
const resetEmailLoading = ref(false)
const resetEmailSending = ref(false)
const resetEmailError = ref('')
const resetEmailMessage = ref('')
const resetBusy = ref(false)
const resetError = ref('')
const resetStatus = ref('')
let resetRequest: AbortController | undefined

const hasVerifiedResetEmail = computed(() => Boolean(resetEmailStatus.value?.verified && resetEmailStatus.value.email))
const canSendResetEmail = computed(() => hasVerifiedResetEmail.value && resetEmailStatus.value?.available && !resetEmailLoading.value)

const menuOptions = computed<DropdownOption[]>(() => [
  { label: props.user.username, key: 'username', disabled: true },
  { type: 'divider', key: 'divider' },
  { label: '更改用户名', key: 'rename' },
  { label: '更改密码', key: 'password' },
  { label: '邮箱设置', key: 'email' },
  { label: '退出登录', key: 'logout' },
])

function handleSelect(key: string | number) {
  if (key === 'rename') {
    formError.value = ''
    formStatus.value = ''
    newUsername.value = props.user.username
    renamePassword.value = ''
    renameOpen.value = true
  } else if (key === 'password') {
    formError.value = ''
    formStatus.value = ''
    currentPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
    passwordOpen.value = true
  } else if (key === 'email') {
    emailOpen.value = true
  } else if (key === 'logout') {
    emit('logout')
  }
}

async function renameAccount() {
  const username = newUsername.value.trim()
  if (!/^[A-Za-z0-9_.-]{3,64}$/.test(username)) {
    formError.value = '用户名需为 3–64 位字母、数字、点、下划线或连字符'
    return
  }
  busy.value = true
  formError.value = ''
  try {
    const response = await api<{ user: User }>('/api/account/username', {
      method: 'PATCH',
      body: JSON.stringify({ username, current_password: renamePassword.value }),
    })
    emit('updated', response.user)
    renameOpen.value = false
    renamePassword.value = ''
  } catch (reason) {
    formError.value = reason instanceof Error ? reason.message : '用户名修改失败'
  } finally {
    busy.value = false
  }
}

async function changePassword() {
  const passwordBytes = new TextEncoder().encode(newPassword.value).length
  if (passwordBytes < 6 || passwordBytes > 72) {
    formError.value = '新密码长度需为 6–72 字节'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    formError.value = '两次输入的新密码不一致'
    return
  }
  busy.value = true
  formError.value = ''
  formStatus.value = ''
  try {
    await api('/api/account/password', {
      method: 'POST',
      body: JSON.stringify({ current_password: currentPassword.value, new_password: newPassword.value }),
    })
    window.alert('密码修改成功，请重新登录。')
    currentPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
    passwordOpen.value = false
    emit('logout')
  } catch (reason) {
    formError.value = reason instanceof Error ? reason.message : '密码修改失败'
  } finally {
    busy.value = false
  }
}

function clearPasswordReset() {
  resetRequest?.abort()
  resetRequest = undefined
  resetMode.value = 'email'
  resetEmailStatus.value = null
  resetEmailLoading.value = false
  resetEmailSending.value = false
  resetEmailError.value = ''
  resetEmailMessage.value = ''
  resetBusy.value = false
  resetError.value = ''
  resetStatus.value = ''
  resetPassword.value = ''
  resetConfirmPassword.value = ''
}

onUnmounted(clearPasswordReset)

function closePasswordReset() {
  resetOpen.value = false
  clearPasswordReset()
}

async function openPasswordReset() {
  if (busy.value) return
  passwordOpen.value = false
  emailOpen.value = false
  formError.value = ''
  formStatus.value = ''
  clearPasswordReset()
  const request = new AbortController()
  resetRequest = request
  resetOpen.value = true
  resetEmailLoading.value = true
  try {
    const status = await api<EmailStatus>('/api/account/email', { signal: request.signal })
    if (resetRequest === request) resetEmailStatus.value = status
  } catch (reason) {
    if (resetRequest === request) resetEmailError.value = reason instanceof Error ? reason.message : '无法加载邮箱状态'
  } finally {
    if (resetRequest === request) resetEmailLoading.value = false
  }
}

function openEmailSettings() {
  closePasswordReset()
  passwordOpen.value = false
  emailOpen.value = true
}

async function sendPasswordResetEmail() {
  const request = resetRequest
  if (!request || !canSendResetEmail.value || resetEmailSending.value) return
  resetEmailSending.value = true
  resetEmailError.value = ''
  resetEmailMessage.value = ''
  try {
    await api('/api/auth/password-reset/email/request', {
      method: 'POST',
      body: JSON.stringify({ identifier: props.user.username }),
      signal: request.signal,
    })
    if (resetRequest === request) resetEmailMessage.value = '密码重置邮件已发送，请前往邮箱查看。链接将在 30 分钟内有效。'
  } catch (reason) {
    if (resetRequest === request) {
      resetEmailError.value = reason instanceof APIError && reason.status === 429
        ? '请求过于频繁，请稍后再试'
        : reason instanceof Error ? reason.message : '密码重置邮件申请失败'
    }
  } finally {
    if (resetRequest === request) resetEmailSending.value = false
  }
}

async function requestPasswordReset() {
  const request = resetRequest
  if (!request || resetBusy.value) return
  const passwordBytes = new TextEncoder().encode(resetPassword.value).length
  if (passwordBytes < 6 || passwordBytes > 72) {
    resetError.value = '新密码长度需为 6–72 字节'
    return
  }
  if (resetPassword.value !== resetConfirmPassword.value) {
    resetError.value = '两次输入的新密码不一致'
    return
  }
  resetBusy.value = true
  resetError.value = ''
  resetStatus.value = ''
  try {
    await api('/api/account/password-reset-request', {
      method: 'POST',
      body: JSON.stringify({ new_password: resetPassword.value }),
      signal: request.signal,
    })
    if (resetRequest === request) {
      resetStatus.value = '密码重置申请已提交，等待管理员审核。'
      resetPassword.value = ''
      resetConfirmPassword.value = ''
    }
  } catch (reason) {
    if (resetRequest === request) resetError.value = reason instanceof Error ? reason.message : '密码重置申请提交失败'
  } finally {
    if (resetRequest === request) resetBusy.value = false
  }
}
</script>

<template>
  <div class="account-menu">
    <n-dropdown trigger="click" :options="menuOptions" @select="handleSelect">
      <n-button class="account-avatar-button" circle secondary aria-label="账户菜单">
        <span aria-hidden="true">👤</span>
      </n-button>
    </n-dropdown>
  </div>

  <n-modal v-model:show="renameOpen">
    <n-card class="account-modal-card" title="更改用户名" closable @close="renameOpen = false">
      <form class="auth-form" @submit.prevent="renameAccount">
        <n-alert v-if="formError" type="error">{{ formError }}</n-alert>
        <label><span>新用户名</span><n-input v-model:value="newUsername" maxlength="64" :input-props="{ autocomplete: 'username' }" /></label>
        <label><span>当前密码</span><n-input v-model:value="renamePassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'current-password' }" /></label>
        <div class="modal-actions"><n-button @click="renameOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>

  <n-modal v-model:show="passwordOpen">
    <n-card class="account-modal-card" title="更改密码" closable @close="passwordOpen = false">
      <form class="auth-form" @submit.prevent="changePassword">
        <n-alert v-if="formError" type="error">{{ formError }}</n-alert>
        <n-alert v-if="formStatus" type="success">{{ formStatus }}</n-alert>
        <label><span>当前密码</span><n-input v-model:value="currentPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'current-password' }" /></label>
        <label><span>新密码</span><n-input v-model:value="newPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" /></label>
        <label><span>确认新密码</span><n-input v-model:value="confirmPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" /></label>
        <n-button text type="primary" attr-type="button" :disabled="busy" @click="openPasswordReset">忘记当前密码？</n-button>
        <div class="modal-actions"><n-button @click="passwordOpen = false">关闭</n-button><n-button type="primary" attr-type="submit" :loading="busy">修改密码</n-button></div>
      </form>
    </n-card>
  </n-modal>

  <n-modal :show="resetOpen" @update:show="(show) => !show && closePasswordReset()">
    <n-card class="account-modal-card" title="忘记当前密码" closable @close="closePasswordReset">
      <div class="modal-actions">
        <n-button :type="resetMode === 'email' ? 'primary' : 'default'" :disabled="resetEmailSending || resetBusy" @click="resetMode = 'email'">邮箱找回</n-button>
        <n-button :type="resetMode === 'admin' ? 'primary' : 'default'" :disabled="resetEmailSending || resetBusy" @click="resetMode = 'admin'">管理员审核</n-button>
      </div>
      <form v-if="resetMode === 'email'" class="auth-form" @submit.prevent="sendPasswordResetEmail">
        <div v-if="resetEmailLoading" class="loading-row"><n-spin size="small" /><span>正在加载邮箱状态…</span></div>
        <template v-else>
          <n-alert v-if="resetEmailError" type="error">{{ resetEmailError }}</n-alert>
          <n-alert v-if="resetEmailMessage" type="success">{{ resetEmailMessage }}</n-alert>
          <n-alert v-if="resetEmailStatus && !resetEmailStatus.available" type="warning">邮箱找回暂不可用：{{ resetEmailStatus.unavailable_reason || '管理员尚未启用邮件服务' }}。可使用管理员审核方式。</n-alert>
          <template v-if="hasVerifiedResetEmail">
            <p>已验证邮箱：<span class="recovery-email">{{ maskEmail(resetEmailStatus?.email || '') }}</span></p>
            <n-alert type="info">密码重置邮件将发送到当前账号已验证的邮箱。</n-alert>
          </template>
          <template v-else-if="resetEmailStatus">
            <n-alert type="info">当前账号尚未绑定并验证邮箱，无法使用邮箱找回密码。</n-alert>
            <n-button attr-type="button" @click="openEmailSettings">前往邮箱设置</n-button>
          </template>
          <div class="modal-actions"><n-button @click="closePasswordReset">关闭</n-button><n-button type="primary" attr-type="submit" :disabled="!canSendResetEmail" :loading="resetEmailSending">发送密码重置邮件</n-button></div>
        </template>
      </form>
      <form v-else class="auth-form" @submit.prevent="requestPasswordReset">
        <n-alert type="info">忘记当前密码时可提交重置申请。管理员审核通过后，新密码才会生效。</n-alert>
        <n-alert v-if="resetError" type="error">{{ resetError }}</n-alert>
        <n-alert v-if="resetStatus" type="success">{{ resetStatus }}</n-alert>
        <label><span>新密码</span><n-input v-model:value="resetPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" /></label>
        <label><span>确认新密码</span><n-input v-model:value="resetConfirmPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" /></label>
        <div class="modal-actions"><n-button @click="closePasswordReset">关闭</n-button><n-button type="primary" attr-type="submit" :loading="resetBusy">提交申请</n-button></div>
      </form>
    </n-card>
  </n-modal>

  <n-modal v-model:show="emailOpen">
    <n-card class="account-modal-card" title="邮箱设置" closable @close="emailOpen = false">
      <AccountEmailSettings v-if="emailOpen" />
    </n-card>
  </n-modal>
</template>

<style scoped>
.recovery-email {
  overflow-wrap: anywhere;
}
</style>
