<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, toRef } from 'vue'
import { NEmpty, NInput, NSelect } from 'naive-ui'
import type { ServerRecord } from '../types/server'
import { metricPercent, useMonitor } from '../composables/useMonitor'
import MonitorCard from '../components/monitor/MonitorCard.vue'

const props = defineProps<{ servers: ServerRecord[] }>()
const emit = defineEmits<{ 'view-server': [server: ServerRecord] }>()
const { speeds } = useMonitor(toRef(props, 'servers'))
const search = ref('')
const status = ref<'all' | 'online' | 'offline'>('all')
const sort = ref<'default' | 'cpu' | 'memory' | 'disk' | 'name'>('default')
const now = ref(Date.now())
const statusOptions = [{ label: '全部', value: 'all' }, { label: '在线', value: 'online' }, { label: '离线', value: 'offline' }]
const sortOptions = [
  { label: '默认顺序', value: 'default' }, { label: 'CPU · 从高到低', value: 'cpu' },
  { label: '内存 · 从高到低', value: 'memory' }, { label: '磁盘 · 从高到低', value: 'disk' },
  { label: '名称', value: 'name' },
]
const counts = computed(() => ({
  online: props.servers.filter((server) => server.status === 'online').length,
  offline: props.servers.filter((server) => server.status === 'offline').length,
  pending: props.servers.filter((server) => server.status === 'pending').length,
}))
function sortValue(server: ServerRecord): number {
  const metrics = server.metrics
  if (!metrics) return -1
  if (sort.value === 'memory') return metricPercent(metrics.memory_used_bytes, metrics.memory_total_bytes) ?? -1
  if (sort.value === 'disk') return metricPercent(metrics.disk_used_bytes, metrics.disk_total_bytes) ?? -1
  return Number.isFinite(metrics.cpu_percent) ? metrics.cpu_percent : -1
}
const visibleServers = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  const result = props.servers.filter((server) => (status.value === 'all' || server.status === status.value)
    && server.name.toLocaleLowerCase().includes(query))
  if (sort.value === 'name') result.sort((a, b) => a.name.localeCompare(b.name, 'zh-CN', { numeric: true }))
  else if (sort.value !== 'default') result.sort((a, b) => sortValue(b) - sortValue(a))
  return result
})

let clockTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => { clockTimer = setInterval(() => { now.value = Date.now() }, 10_000) })
onUnmounted(() => { if (clockTimer !== undefined) clearInterval(clockTimer) })
</script>

<template>
  <div class="monitor-page">
    <p class="monitor-summary">
      <span class="monitor-online-dot" aria-hidden="true"></span>
      <span>共 <strong>{{ servers.length }}</strong> 台服务器，<strong>{{ counts.online }}</strong> 台在线，<strong>{{ counts.offline }}</strong> 台离线<template v-if="counts.pending">，<strong>{{ counts.pending }}</strong> 台待注册</template></span>
    </p>
    <div v-if="servers.length" class="monitor-toolbar">
      <n-input v-model:value="search" class="monitor-search" placeholder="搜索服务器" :input-props="{ 'aria-label': '搜索服务器' }" clearable />
      <label><span>筛选</span><n-select v-model:value="status" :options="statusOptions" aria-label="服务器状态" /></label>
      <label><span>排序</span><n-select v-model:value="sort" :options="sortOptions" aria-label="服务器排序" /></label>
    </div>
    <n-empty v-if="!servers.length" class="monitor-empty" description="暂无服务器">
      <template #extra>请先在「服务器」页面添加服务器并安装 Agent。</template>
    </n-empty>
    <n-empty v-else-if="!visibleServers.length" class="monitor-empty" description="没有匹配的服务器" />
    <div v-else class="monitor-grid">
      <MonitorCard v-for="server in visibleServers" :key="server.id" :server="server" :speed="speeds.get(server.id)" :now="now" @view-server="emit('view-server', $event)" />
    </div>
  </div>
</template>
