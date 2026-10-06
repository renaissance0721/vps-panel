<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NSpin } from 'naive-ui'
import { api } from '../api/client'

const props = defineProps<{ token: string }>()
const loading = ref(true)
const verified = ref(false)
const error = ref('')

async function verifyEmail() {
  const token = props.token
  window.history.replaceState({}, '', '/verify-email')
  if (!token) {
    error.value = '验证链接无效或已过期。'
    loading.value = false
    return
  }
  try {
    await api('/api/auth/email/verify', {
      method: 'POST',
      body: JSON.stringify({ token }),
    })
    verified.value = true
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '验证链接无效或已过期。'
  } finally {
    loading.value = false
  }
}

onMounted(verifyEmail)
</script>

<template>
  <n-card class="auth-card" :bordered="true">
    <p class="eyebrow">账号邮箱</p>
    <h1>邮箱验证</h1>
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在验证邮箱…</span></div>
    <n-alert v-else-if="verified" type="success" title="邮箱验证成功">
      你现在可以关闭此页面或返回登录页。
    </n-alert>
    <n-alert v-else type="error" title="验证失败">{{ error || '验证链接无效或已过期。' }}</n-alert>
    <n-button v-if="!loading" tag="a" href="/" type="primary" block>返回登录页</n-button>
  </n-card>
</template>
