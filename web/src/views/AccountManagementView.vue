<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NSpin,
} from 'naive-ui'

import { api } from '../api/client'
import type { AccessUser } from '../types/auth'

const accounts = ref<AccessUser[]>([])
const loading = ref(false)
const deleting = ref(false)
const error = ref('')

async function loadAccounts() {
  loading.value = true
  error.value = ''
  try {
    accounts.value = (await api<{ users: AccessUser[] }>('/api/users')).users
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法加载账号列表'
  } finally {
    loading.value = false
  }
}

async function deleteAccount(account: AccessUser) {
  if (account.role === 'admin') return
  if (!window.confirm(`删除用户“${account.username}”后不可恢复，确定继续吗？`)) return
  if (window.prompt(`请输入用户名“${account.username}”再次确认删除`) !== account.username) {
    error.value = '用户名确认不匹配，已取消删除'
    return
  }
  deleting.value = true
  error.value = ''
  try {
    await api(`/api/admin/users/${account.id}`, { method: 'DELETE' })
    await loadAccounts()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '删除用户失败'
  } finally {
    deleting.value = false
  }
}

function roleLabel(role: AccessUser['role']) {
  return role === 'admin' ? '管理员' : role === 'vip' ? 'VIP' : role === 'subscriber' ? '订阅用户' : '普通用户'
}

onMounted(loadAccounts)
</script>

<template>
  <div class="account-management">
    <n-alert v-if="error" class="page-alert" type="error">{{ error }}</n-alert>
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载账号列表…</span></div>
    <n-card v-else title="账号列表" :bordered="true">
      <n-empty v-if="accounts.length === 0" description="暂无账号" />
      <div v-else class="invitation-list">
        <div v-for="account in accounts" :key="account.id" class="invitation-row">
          <div><strong>{{ account.username }}</strong><span>{{ roleLabel(account.role) }}</span></div>
          <n-button v-if="account.role !== 'admin'" type="error" secondary size="small" :disabled="deleting" @click="deleteAccount(account)">删除用户</n-button>
        </div>
      </div>
    </n-card>
  </div>
</template>
