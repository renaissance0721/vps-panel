<script setup lang="ts">
import { computed, ref } from 'vue'
import { NAlert, NButton, NCard, NDropdown, NInput, NModal, type DropdownOption } from 'naive-ui'
import { api } from '../api/client'
import type { User } from '../types/auth'

const props = defineProps<{ user: User }>()
const emit = defineEmits<{ updated: [user: User]; logout: [] }>()

const renameOpen = ref(false)
const passwordOpen = ref(false)
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

const menuOptions = computed<DropdownOption[]>(() => [
  { label: props.user.username, key: 'username', disabled: true },
  { type: 'divider', key: 'divider' },
  { label: '更改用户名', key: 'rename' },
  { label: '更改密码', key: 'password' },
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
  if (passwordBytes < 10 || passwordBytes > 72) {
    formError.value = '新密码长度需为 10–72 字节'
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

function openPasswordReset() {
  passwordOpen.value = false
  formError.value = ''
  formStatus.value = ''
  resetPassword.value = ''
  resetConfirmPassword.value = ''
  resetOpen.value = true
}

async function requestPasswordReset() {
  const passwordBytes = new TextEncoder().encode(resetPassword.value).length
  if (passwordBytes < 10 || passwordBytes > 72) {
    formError.value = '新密码长度需为 10–72 字节'
    return
  }
  if (resetPassword.value !== resetConfirmPassword.value) {
    formError.value = '两次输入的新密码不一致'
    return
  }
  busy.value = true
  formError.value = ''
  formStatus.value = ''
  try {
    await api('/api/account/password-reset-request', {
      method: 'POST',
      body: JSON.stringify({ new_password: resetPassword.value }),
    })
    formStatus.value = '密码重置申请已提交，等待管理员审核。'
    resetPassword.value = ''
    resetConfirmPassword.value = ''
  } catch (reason) {
    formError.value = reason instanceof Error ? reason.message : '密码重置申请提交失败'
  } finally {
    busy.value = false
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
        <n-button text type="primary" attr-type="button" @click="openPasswordReset">忘记当前密码？</n-button>
        <div class="modal-actions"><n-button @click="passwordOpen = false">关闭</n-button><n-button type="primary" attr-type="submit" :loading="busy">修改密码</n-button></div>
      </form>
    </n-card>
  </n-modal>

  <n-modal v-model:show="resetOpen">
    <n-card class="account-modal-card" title="申请重置密码" closable @close="resetOpen = false">
      <form class="auth-form" @submit.prevent="requestPasswordReset">
        <n-alert type="info">忘记当前密码时可提交重置申请。管理员审核通过后，新密码才会生效。</n-alert>
        <n-alert v-if="formError" type="error">{{ formError }}</n-alert>
        <n-alert v-if="formStatus" type="success">{{ formStatus }}</n-alert>
        <label><span>新密码</span><n-input v-model:value="resetPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" /></label>
        <label><span>确认新密码</span><n-input v-model:value="resetConfirmPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" /></label>
        <div class="modal-actions"><n-button @click="resetOpen = false">关闭</n-button><n-button type="primary" attr-type="submit" :loading="busy">提交申请</n-button></div>
      </form>
    </n-card>
  </n-modal>
</template>
