<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput } from 'naive-ui'
import { api } from '../api/client'
import { formatTime } from '../format'

type AuditLog = {
  id: number
  created_at: string
  actor_username: string
  action: string
  resource_type: string
  resource_id: number | null
  summary: string
  request_id: string
}

const logs = ref<AuditLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const action = ref('')
const user = ref('')
const loading = ref(false)
const error = ref('')
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

async function load(resetPage = false) {
  if (resetPage) page.value = 1
  loading.value = true
  error.value = ''
  try {
    const query = new URLSearchParams({ page: String(page.value), page_size: String(pageSize) })
    if (action.value.trim()) query.set('action', action.value.trim())
    if (user.value.trim()) query.set('user', user.value.trim())
    const response = await api<{ audit_logs: AuditLog[]; total: number }>(`/api/admin/audit-logs?${query}`)
    logs.value = response.audit_logs
    total.value = response.total
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法加载操作日志'
  } finally {
    loading.value = false
  }
}

function changePage(next: number) {
  page.value = next
  void load()
}

onMounted(() => void load())
</script>

<template>
  <n-card title="操作日志" :bordered="true">
    <n-alert v-if="error" type="error" class="page-alert">{{ error }}</n-alert>
    <form class="audit-filters" @submit.prevent="load(true)">
      <label><span>操作</span><n-input v-model:value="action" placeholder="例如 proxy.create" clearable /></label>
      <label><span>账号</span><n-input v-model:value="user" placeholder="精确用户名" clearable /></label>
      <n-button attr-type="submit" type="primary" :loading="loading">筛选</n-button>
    </form>
    <n-empty v-if="!loading && logs.length === 0" description="暂无操作日志" />
    <div v-else class="table-scroll">
      <table class="data-table">
        <thead><tr><th>时间</th><th>账号</th><th>操作</th><th>资源</th><th>摘要</th></tr></thead>
        <tbody>
          <tr v-for="entry in logs" :key="entry.id">
            <td>{{ formatTime(entry.created_at) }}</td>
            <td>{{ entry.actor_username || '系统' }}</td>
            <td><code>{{ entry.action }}</code></td>
            <td>{{ entry.resource_type }}{{ entry.resource_id ? ` #${entry.resource_id}` : '' }}</td>
            <td>{{ entry.summary || '—' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="audit-pagination">
      <span>第 {{ page }} / {{ pageCount }} 页，共 {{ total }} 条</span>
      <n-button size="small" :disabled="page <= 1 || loading" @click="changePage(page - 1)">上一页</n-button>
      <n-button size="small" :disabled="page >= pageCount || loading" @click="changePage(page + 1)">下一页</n-button>
    </div>
  </n-card>
</template>

<style scoped>
.audit-filters { display: grid; grid-template-columns: minmax(180px, 1fr) minmax(180px, 1fr) auto; gap: 12px; align-items: end; margin-bottom: 18px; }
.audit-filters label { display: grid; gap: 6px; }
.table-scroll { overflow-x: auto; }
.data-table { width: 100%; border-collapse: collapse; }
.data-table th, .data-table td { padding: 10px 12px; border-bottom: 1px solid #e5e7eb; text-align: left; vertical-align: top; }
.data-table th { white-space: nowrap; color: #64748b; font-weight: 600; }
.audit-pagination { display: flex; justify-content: flex-end; align-items: center; gap: 10px; margin-top: 16px; }
@media (max-width: 720px) { .audit-filters { grid-template-columns: 1fr; } .audit-pagination { justify-content: flex-start; flex-wrap: wrap; } }
</style>
