<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NInputNumber, NModal, NSelect, NSpin, NSwitch } from 'naive-ui'
import type { ServerRecord } from '../../types/server'
import type { ProbeInput, ProbeTask } from '../../types/monitor'
import { supportsProbe, useProbeTasks } from '../../composables/useProbe'

const props = defineProps<{ servers: ServerRecord[]; show: boolean }>()
const emit = defineEmits<{ 'update:show': [value: boolean] }>()
const { tasks, loading, saving, error, load, save, remove } = useProbeTasks()
const editing = ref(false)
const editID = ref<number | null>(null)
const deleting = ref<number | null>(null)
const form = ref<ProbeInput>(newForm())
const typeOptions = [{ label: 'TCP', value: 'tcp' }, { label: 'ICMP', value: 'icmp' }]
const intervalOptions = [30, 60, 120, 300].map(value => ({ label: `${value} 秒`, value }))
const serverOptions = computed(() => props.servers.map(server => {
  const supported = supportsProbe(server, form.value.type)
  const unavailable = server.status !== 'online' || !!server.archived_at || !!server.decommission_status
  return { value: server.id, label: `${server.name}${!supported ? `（不支持 ${form.value.type.toUpperCase()}）` : unavailable ? '（离线或不可用）' : ''}`, disabled: !supported || unavailable }
}))
const selectionUnavailable = computed(() => form.value.server_ids.some(id => !serverOptions.value.some(option => option.value === id && !option.disabled)))
function newForm(): ProbeInput { return { name: '', type: 'tcp', target: '', port: 443, interval_seconds: 60, enabled: true, server_ids: [] } }
function edit(task?: ProbeTask) {
  error.value = ''
  editID.value = task?.id ?? null
  form.value = task ? { name: task.name, type: task.type, target: task.target, port: task.port, interval_seconds: task.interval_seconds, enabled: task.enabled, server_ids: [...task.server_ids] } : newForm()
  editing.value = true
}
async function submit() {
  const input = { ...form.value, port: form.value.type === 'icmp' ? null : form.value.port }
  if (await save(editID.value, input)) editing.value = false
}
async function confirmDelete() { if (deleting.value !== null && await remove(deleting.value)) deleting.value = null }
onMounted(load)
</script>

<template>
  <n-modal :show="show" @update:show="emit('update:show', $event)">
    <n-card class="monitor-detail-modal" title="延迟探测" closable @close="emit('update:show', false)">
      <div class="section-heading"><span>每个节点最多 64 个任务</span><n-button :disabled="saving" @click="edit()">+ 新建任务</n-button></div>
      <n-alert v-if="error" type="error" class="monitor-probe-error">{{ error }}</n-alert>
      <n-spin v-if="loading" description="正在加载探测任务…" />
      <n-empty v-else-if="!tasks.length" description="暂无延迟探测任务" />
      <div v-else class="monitor-probe-table-wrapper"><table class="monitor-probe-table">
        <thead><tr><th>名称</th><th>类型</th><th>目标</th><th>周期</th><th>执行节点数</th><th>状态</th><th>操作</th></tr></thead>
        <tbody><tr v-for="task in tasks" :key="task.id">
          <td>{{ task.name }}</td><td>{{ task.type.toUpperCase() }}</td><td>{{ task.target }}<template v-if="task.port !== null"> · {{ task.port }}</template></td><td>{{ task.interval_seconds }} 秒</td>
          <td>{{ task.server_ids.length }}</td><td>{{ task.enabled ? '启用' : '停用' }}</td>
          <td><n-button size="small" :disabled="saving" @click="edit(task)">编辑</n-button> <n-button size="small" :disabled="saving" @click="deleting = task.id">删除</n-button></td>
        </tr></tbody>
      </table></div>
      <n-alert v-if="deleting !== null" type="warning" class="monitor-probe-error" title="删除探测任务及其延迟历史？">
        <n-button :loading="saving" @click="confirmDelete">确认删除</n-button> <n-button :disabled="saving" @click="deleting = null">取消</n-button>
      </n-alert>
      <form v-if="editing" class="monitor-probe-form" @submit.prevent="submit">
        <h3>{{ editID === null ? '新建任务' : '编辑任务' }}</h3>
        <label>名称<n-input v-model:value="form.name" :maxlength="100" placeholder="上海电信" aria-label="探测名称" /></label>
        <label>类型<n-select v-model:value="form.type" :options="typeOptions" aria-label="探测类型" /></label>
        <label>目标<n-input v-model:value="form.target" placeholder="example.com / IPv4 / IPv6" aria-label="探测目标" /></label>
        <label v-if="form.type === 'tcp'">端口<n-input-number v-model:value="form.port" :min="1" :max="65535" :precision="0" aria-label="TCP 端口" /></label>
        <label>探测周期<n-input-number v-model:value="form.interval_seconds" :min="5" :max="86400" :precision="0" aria-label="探测周期（秒）" /><n-select v-model:value="form.interval_seconds" :options="intervalOptions" aria-label="常用探测周期" /></label>
        <label>执行服务器<n-select v-model:value="form.server_ids" :options="serverOptions" multiple filterable placeholder="选择执行服务器" aria-label="执行服务器" /></label>
        <n-alert v-if="selectionUnavailable" type="warning">部分已选节点离线或不支持当前探测类型，请移除这些节点或等待其上线后再保存。</n-alert>
        <label>启用<n-switch v-model:value="form.enabled" aria-label="启用探测" /></label>
        <div class="modal-actions"><n-button :disabled="saving" @click="editing = false">取消</n-button><n-button attr-type="submit" type="primary" :loading="saving" :disabled="selectionUnavailable">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>
</template>
