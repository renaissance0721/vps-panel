<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NInput } from 'naive-ui'
import { api } from '../api/client'

const props = defineProps<{ token: string }>()
const token = ref(props.token)
const password = ref('')
const confirmPassword = ref('')
const busy = ref(false)
const completed = ref(false)
const error = ref('')

function prepareResetPage() {
  window.history.replaceState({}, '', '/reset-password')
  if (!token.value) error.value = '密码重置链接无效或已过期'
}

onMounted(prepareResetPage)

async function resetPassword() {
  if (busy.value || completed.value) return
  if (!token.value) {
    error.value = '密码重置链接无效或已过期'
    return
  }
  const length = new TextEncoder().encode(password.value).length
  if (length < 6 || length > 72) {
    error.value = '新密码长度需为 6–72 字节'
    return
  }
  if (password.value !== confirmPassword.value) {
    error.value = '两次输入的新密码不一致'
    return
  }
  busy.value = true
  error.value = ''
  try {
    await api('/api/auth/password-reset/email/confirm', {
      method: 'POST',
      body: JSON.stringify({ token: token.value, new_password: password.value }),
    })
    completed.value = true
    token.value = ''
    password.value = ''
    confirmPassword.value = ''
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '密码重置失败'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <n-card class="auth-card" :bordered="true">
    <p class="eyebrow">账号恢复</p>
    <h1>设置新密码</h1>
    <n-alert v-if="completed" type="success" title="密码重置成功">密码重置成功，请重新登录。</n-alert>
    <template v-else>
      <n-alert v-if="error" class="form-alert" type="error">{{ error }}</n-alert>
      <form v-if="token" class="auth-form" @submit.prevent="resetPassword">
        <label><span>新密码</span><n-input v-model:value="password" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" placeholder="6–72 字节" /></label>
        <label><span>确认新密码</span><n-input v-model:value="confirmPassword" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" /></label>
        <n-button type="primary" attr-type="submit" block :loading="busy">重置密码</n-button>
      </form>
    </template>
    <n-button tag="a" href="/" block>返回登录</n-button>
  </n-card>
</template>
