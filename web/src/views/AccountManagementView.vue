<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NSpin,
} from 'naive-ui'

import { api } from '../api/client'
import { userRoleLabel } from '../format'
import { beginDragPreview, endDragPreview } from '../drag'
import { moveRow, persistMove } from '../reorder'
import type { AccessUser } from '../types/auth'

const accounts = ref<AccessUser[]>([])
const loading = ref(false)
const deleting = ref(false)
const error = ref('')
const draggedID = ref<number | null>(null)
const dropTargetID = ref<number | null>(null)
const reorderingID = ref<number | null>(null)

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
  if (account.role === 'admin' || deleting.value || reorderingID.value !== null) return
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

function startDrag(event: DragEvent, id: number) {
  if (deleting.value || reorderingID.value !== null || !event.dataTransfer) return
  const source = (event.currentTarget as HTMLElement | null)?.closest('.invitation-row') as HTMLElement | null
  if (!source || !beginDragPreview(event, source, String(id))) return
  draggedID.value = id
}

function endDrag() {
  endDragPreview()
  draggedID.value = null
  dropTargetID.value = null
}

function dragOver(event: DragEvent, id: number) {
  if (draggedID.value === null || draggedID.value === id || deleting.value || reorderingID.value !== null) return
  event.preventDefault()
  dropTargetID.value = id
}

async function dropAccount(id: number) {
  const sourceID = draggedID.value
  endDrag()
  if (sourceID === null || deleting.value || reorderingID.value !== null) return
  const move = moveRow(accounts.value, sourceID, id)
  if (!move) return
  reorderingID.value = sourceID
  error.value = ''
  try {
    await persistMove(move, (direction) => api(`/api/users/${sourceID}/reorder`, {
      method: 'POST', body: JSON.stringify({ direction }),
    }), loadAccounts)
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '调整用户顺序失败'
  } finally {
    reorderingID.value = null
  }
}

onMounted(loadAccounts)
onUnmounted(() => {
  if (draggedID.value !== null) endDrag()
})
</script>

<template>
  <div class="account-management">
    <n-alert v-if="error" class="page-alert" type="error">{{ error }}</n-alert>
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载账号列表…</span></div>
    <n-card v-else title="账号列表" :bordered="true">
      <n-empty v-if="accounts.length === 0" description="暂无账号" />
      <div v-else class="invitation-list">
        <div v-for="account in accounts" :key="account.id" class="invitation-row account-row"
          :class="{ 'row-dragging': draggedID === account.id, 'row-drop-target': dropTargetID === account.id }"
          @dragover="dragOver($event, account.id)" @dragleave="dropTargetID === account.id && (dropTargetID = null)" @drop.prevent="dropAccount(account.id)">
          <span class="drag-handle" :class="{ 'drag-handle--disabled': deleting || reorderingID !== null }" :draggable="!deleting && reorderingID === null"
            title="拖动排序" aria-label="拖动用户排序" @dragstart="startDrag($event, account.id)" @dragend="endDrag"><span></span><span></span><span></span></span>
          <div class="account-row-info"><strong>{{ account.username }}</strong><span>{{ userRoleLabel(account.role) }}</span></div>
          <n-button v-if="account.role !== 'admin'" type="error" secondary size="small" :disabled="deleting || reorderingID !== null" @click="deleteAccount(account)">删除用户</n-button>
        </div>
      </div>
    </n-card>
  </div>
</template>

<style scoped>
.account-row {
  flex-direction: row;
  align-items: center;
}
.account-row-info {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
}
</style>
