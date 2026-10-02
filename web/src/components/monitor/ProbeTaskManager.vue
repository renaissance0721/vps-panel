<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NInputNumber, NModal, NSelect, NSpin, NSwitch } from 'naive-ui'
import type { ServerRecord } from '../../types/server'
import type { ProbeInput, ProbeTask } from '../../types/monitor'
import { probeTarget, supportsProbe, useProbeTasks } from '../../composables/useProbe'

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
  const unavailable = !!server.archived_at || !!server.decommission_status
  const status = server.archived_at ? '已归档' : server.decommission_status ? '删除中' : server.status !== 'online' ? '当前离线' : ''
  const capability = !supported ? `Agent 不支持 ${form.value.type === 'tcp' ? 'TCPing' : 'ICMP'}` : ''
  const labels = [capability, status].filter(Boolean)
  return { value: server.id, label: `${server.name}${labels.length ? `（${labels.join('，')}）` : ''}`, disabled: !supported || unavailable }
}))
const compatibleServerIDs = computed(() => serverOptions.value.filter(option => !option.disabled).map(option => option.value))
const selectionUnavailable = computed(() => !form.value.default_on && form.value.server_ids.some(id => !compatibleServerIDs.value.includes(id)))
function selectAll() { form.value.server_ids = [...compatibleServerIDs.value] }
function clearSelection() { form.value.server_ids = [] }
function newForm(): ProbeInput { return { name: '', type: 'tcp', target: '', interval_seconds: 60, enabled: true, default_on: true, server_ids: [] } }
function edit(task?: ProbeTask) {
  error.value = ''
  editID.value = task?.id ?? null
  form.value = task ? { name: task.name, type: task.type, target: probeTarget(task), interval_seconds: task.interval_seconds, enabled: task.enabled, default_on: task.default_on, server_ids: [...task.server_ids] } : newForm()
  editing.value = true
}
async function submit() {
  const input = { ...form.value, server_ids: form.value.default_on ? [] : form.value.server_ids }
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
          <td>{{ task.name }}</td><td>{{ task.type.toUpperCase() }}</td><td>{{ probeTarget(task) }}</td><td>{{ task.interval_seconds }} 秒</td>
          <td>{{ task.default_on ? '全部兼容节点' : `${task.server_ids.length} 台` }}</td><td>{{ task.enabled ? '启用' : '停用' }}</td>
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
        <label>目标<n-input v-model:value="form.target" :placeholder="form.type === 'tcp' ? 'example.com:443 / 1.1.1.1:80 / [IPv6]:443' : 'example.com / 1.1.1.1 / 2400:3200::1'" aria-label="探测目标" /></label>
        <label>探测周期<n-input-number v-model:value="form.interval_seconds" :min="5" :max="86400" :precision="0" aria-label="探测周期（秒）" /><n-select v-model:value="form.interval_seconds" :options="intervalOptions" aria-label="常用探测周期" /></label>
        <div class="monitor-probe-server-field">
          <div class="monitor-probe-server-heading"><span>执行服务器</span><span v-if="!form.default_on"><n-button text size="small" @click="selectAll">全选</n-button><n-button text size="small" @click="clearSelection">清空</n-button></span></div>
          <n-select :value="form.default_on ? compatibleServerIDs : form.server_ids" @update:value="form.server_ids = $event" :options="serverOptions" :disabled="form.default_on" multiple filterable :placeholder="form.default_on ? '暂无兼容服务器，后续自动匹配' : '选择执行服务器'" aria-label="执行服务器" />
        </div>
        <n-alert v-if="selectionUnavailable" type="warning">部分已选节点已归档、正在删除或 Agent 不支持当前探测类型，请调整选择。</n-alert>
        <div class="monitor-probe-switch-row"><span>默认应用到新服务器</span><n-switch v-model:value="form.default_on" aria-label="默认应用到新服务器" /></div>
        <p v-if="form.default_on" class="monitor-probe-help">自动应用于当前及后续新增的兼容服务器。</p>
        <div class="monitor-probe-switch-row"><span>启用</span><n-switch v-model:value="form.enabled" aria-label="启用探测" /></div>
        <div class="modal-actions"><n-button :disabled="saving" @click="editing = false">取消</n-button><n-button attr-type="submit" type="primary" :loading="saving" :disabled="selectionUnavailable">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>
</template>
