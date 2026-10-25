<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NCheckbox, NEmpty, NInput, NModal, NSpin, NTag } from 'naive-ui'
import type { ServerRecord } from '../../types/server'
import { probeTarget, supportsProbe, useServerProbeAssignments } from '../../composables/useProbe'

const props = defineProps<{ server: ServerRecord; show: boolean }>()
const emit = defineEmits<{ 'update:show': [value: boolean]; saved: [serverID: number] }>()
const { tasks, loading, loaded, saving, error, load, save, reset } = useServerProbeAssignments()
const search = ref('')
const selectedIDs = ref<number[]>([])
let session = 0
const unavailable = computed(() => !!props.server.archived_at || !!props.server.decommission_status)
const visibleTasks = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return tasks.value.filter(task => `${task.name} ${probeTarget(task)}`.toLocaleLowerCase().includes(query))
})

async function reload() {
  const id = props.server.id
  const current = session
  if (await load(id) && current === session && props.show && props.server.id === id) {
    selectedIDs.value = tasks.value.filter(task => task.assigned).map(task => task.id)
  }
}
watch([() => props.server.id, () => props.show], () => {
  session++
  reset()
  search.value = ''
  selectedIDs.value = []
  if (props.show) void reload()
}, { immediate: true, flush: 'sync' })
onScopeDispose(() => { session++ })

function select(ids: number[]) {
  if (ids.length > 64) {
    error.value = '每台服务器最多选择 64 个探测任务'
    return
  }
  error.value = ''
  selectedIDs.value = ids
}
function toggle(id: number, checked: boolean) {
  select(checked ? [...new Set([...selectedIDs.value, id])] : selectedIDs.value.filter(value => value !== id))
}
function selectAll() { select([...new Set([...selectedIDs.value, ...visibleTasks.value.map(task => task.id)])]) }
function clearSelection() { select([]) }
async function submit() {
  if (unavailable.value || !loaded.value || loading.value || saving.value) return
  const id = props.server.id
  const current = session
  if (!await save(id, [...selectedIDs.value]) || current !== session) return
  const refreshed = await load(id)
  if (current !== session || !props.show || props.server.id !== id) return
  // A committed save still refreshes history if reloading the editor fails.
  emit('saved', id)
  if (refreshed) emit('update:show', false)
  else error.value = `监测设置已保存，但重新加载失败：${error.value}`
}
</script>

<template>
  <n-modal :show="show" :mask-closable="!saving" :close-on-esc="!saving" @update:show="emit('update:show', $event)">
    <n-card class="monitor-assignment-modal" :title="`编辑延迟监测 · ${server.name}`" :closable="!saving" @close="emit('update:show', false)">
      <form @submit.prevent="submit">
        <p class="monitor-assignment-help">选择这台服务器需要执行的延迟探测任务。</p>
        <n-alert v-if="unavailable" type="warning">服务器已归档或正在退役，不能修改监测设置。</n-alert>
        <n-alert v-else-if="server.status !== 'online'" type="info">服务器当前离线，设置将在 Agent 下次连接后生效。</n-alert>
        <n-alert v-if="error" type="error" class="monitor-probe-error">{{ error }}<n-button v-if="!loaded && !loading" size="small" attr-type="button" @click="reload">重试</n-button></n-alert>
        <n-spin v-if="loading" description="正在加载监测设置…" class="monitor-history-loading" />
        <template v-else-if="loaded">
          <n-input v-model:value="search" placeholder="搜索地区、运营商或目标" aria-label="搜索地区、运营商或目标" clearable :disabled="saving" />
          <div class="monitor-assignment-toolbar">
            <span>已选择 {{ selectedIDs.length }} / 64</span>
            <div><n-button size="small" attr-type="button" :disabled="saving || unavailable" title="选择当前搜索结果" @click="selectAll">全选</n-button><n-button size="small" attr-type="button" :disabled="saving || unavailable" @click="clearSelection">清空</n-button></div>
          </div>
          <n-empty v-if="!tasks.length" description="暂无延迟探测任务"><template #extra>请先在延迟探测管理中创建任务。</template></n-empty>
          <n-empty v-else-if="!visibleTasks.length" description="没有匹配的探测任务" />
          <ul v-else class="monitor-assignment-list">
            <li v-for="task in visibleTasks" :key="task.id">
              <n-checkbox :checked="selectedIDs.includes(task.id)" :aria-label="`监测 ${task.name}`" :disabled="saving || unavailable" @update:checked="toggle(task.id, $event)" />
              <div class="monitor-assignment-task">
                <div class="monitor-assignment-name"><strong>{{ task.name }}</strong><n-tag v-if="!task.enabled" size="small">全局停用</n-tag></div>
                <small>{{ task.type.toUpperCase() }} · {{ probeTarget(task) }} · {{ task.interval_seconds }} 秒</small>
                <small v-if="!supportsProbe(server, task.type)" class="monitor-assignment-capability">当前 Agent 暂不支持 {{ task.type.toUpperCase() }}，支持后自动生效。</small>
              </div>
            </li>
          </ul>
        </template>
        <div class="modal-actions"><n-button attr-type="button" :disabled="saving" @click="emit('update:show', false)">取消</n-button><n-button attr-type="submit" type="primary" :loading="saving" :disabled="!loaded || loading || saving || unavailable">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>
</template>

<style scoped>
.monitor-assignment-modal { width: min(640px, calc(100vw - 32px)); max-height: calc(100dvh - 32px); overflow-y: auto; }
.monitor-assignment-help { margin-top: 0; color: var(--color-text-secondary); }
.monitor-assignment-modal :deep(.n-alert) { margin-bottom: 12px; }
.monitor-assignment-toolbar, .monitor-assignment-toolbar > div, .monitor-assignment-name { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.monitor-assignment-toolbar { justify-content: space-between; margin: 12px 0; }
.monitor-assignment-toolbar > span { font-size: 12px; color: var(--color-text-secondary); }
.monitor-assignment-list { list-style: none; margin: 0; padding: 0; max-height: 50vh; overflow-y: auto; }
.monitor-assignment-list li { display: flex; align-items: flex-start; gap: 12px; padding: 12px 0; border-bottom: 1px solid var(--color-border); }
.monitor-assignment-task { min-width: 0; }
.monitor-assignment-task small { display: block; margin-top: 4px; overflow-wrap: anywhere; color: var(--color-text-secondary); }
.monitor-assignment-task .monitor-assignment-capability { color: var(--color-warning); }
.monitor-assignment-name strong { overflow-wrap: anywhere; }
</style>
