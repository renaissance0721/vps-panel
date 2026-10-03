<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NModal, NSpin } from 'naive-ui'
import type { ServerRecord } from '../../types/server'
import type { LatencyHours } from '../../types/monitor'
import { formatBytes, formatUptime, statusLabel } from '../../server'
import { probeColors, probeLabel, probeOutcomeLabel, supportsProbe, useProbeHistory } from '../../composables/useProbe'

const LatencyChart = defineAsyncComponent(() => import('./LatencyChart.vue'))
const props = defineProps<{ server: ServerRecord; show: boolean }>()
const emit = defineEmits<{ 'update:show': [value: boolean] }>()
const hours = ref<LatencyHours>(1)
const rangeOptions: LatencyHours[] = [1, 6, 24]
const supported = computed(() => supportsProbe(props.server, 'tcp') || supportsProbe(props.server, 'icmp'))
const { history, loading, error, load, reset } = useProbeHistory()
const summaries = computed(() => {
  const tasks = history.value?.tasks ?? []
  return tasks.map(task => ({
    id: task.id,
    title: probeLabel(task, tasks),
    primary: task.latest_latency_ms === null ? probeOutcomeLabel(task.latest_outcome) : `${task.latest_latency_ms.toFixed(1)} ms`,
    secondary: `${task.failure_rate === null ? '—' : `${task.failure_rate.toFixed(1)}%`}${task.type === 'icmp' ? '丢包' : '失败'}`,
  }))
})
const fields = computed(() => {
  const info = props.server.system_info
  const metrics = props.server.metrics
  const bytes = (value: number | undefined) => value === undefined || !Number.isFinite(value) || value < 0 ? '—' : formatBytes(value)
  return [
    ['主机名', info?.hostname || '—'], ['操作系统', info?.os_name || '—'], ['系统版本', info?.os_version || '—'],
    ['内核', info?.kernel || '—'], ['架构', info?.arch || '—'], ['内存总量', bytes(metrics?.memory_total_bytes)],
    ['磁盘总量', bytes(metrics?.disk_total_bytes)], ['IPv4', info?.ipv4?.join(' · ') || '—'],
    ['IPv6', info?.ipv6?.join(' · ') || '—'], ['公网 IPv4', info?.public_ipv4 || '—'],
    ['公网 IPv6', info?.public_ipv6 || '—'],
    ['运行时间', metrics?.uptime_seconds !== undefined && Number.isFinite(metrics.uptime_seconds) && metrics.uptime_seconds >= 0 ? formatUptime(metrics.uptime_seconds) : '—'],
  ]
})
function refresh() { if (props.show && supported.value) void load(props.server.id, hours.value) }
watch([() => props.server.id, () => props.show], () => { if (props.show) hours.value = 1 })
watch([() => props.server.id, () => props.show, hours, supported, () => props.server.agent_capabilities?.join(',')], () => {
  if (!props.show || !supported.value) reset()
  else refresh()
}, { immediate: true })
</script>

<template>
  <n-modal :show="show" @update:show="emit('update:show', $event)">
    <n-card class="monitor-detail-modal" :title="server.name" closable @close="emit('update:show', false)">
      <template #header-extra><span class="monitor-status" :class="`monitor-status-${server.status}`">{{ statusLabel(server.status) }}</span></template>
      <section aria-label="基础信息">
        <h3>基础信息</h3>
        <dl class="monitor-basic-info"><div v-for="[label, value] in fields" :key="label"><dt>{{ label }}</dt><dd>{{ value }}</dd></div></dl>
      </section>
      <section class="monitor-latency-section" aria-label="网络延迟">
        <h3>网络延迟</h3>
        <div class="monitor-range-actions">
          <div role="group" aria-label="延迟时间范围"><n-button v-for="range in rangeOptions" :key="range" size="small" :type="hours === range ? 'primary' : 'default'" :aria-pressed="hours === range" @click="hours = range">{{ range }} 小时</n-button></div>
          <n-button size="small" :disabled="!supported || loading" @click="refresh">刷新</n-button>
        </div>
        <n-empty v-if="!supported" description="当前 Agent 不支持延迟探测" />
        <n-spin v-else-if="loading" description="正在加载延迟历史…" class="monitor-history-loading" />
        <n-alert v-else-if="error" type="error" title="无法加载延迟历史">{{ error }} <n-button size="small" @click="refresh">重试</n-button></n-alert>
        <template v-else-if="history">
          <div class="monitor-probe-overview" aria-label="探测摘要">
            <n-empty v-if="!history.tasks.length" description="尚未分配延迟探测任务" />
            <ul v-else class="monitor-probe-summaries"><li v-for="(summary, index) in summaries" :key="summary.id" :style="{ '--probe-color': probeColors[index % probeColors.length] }">
              <strong :title="summary.title">{{ summary.title }}</strong>
              <div class="monitor-probe-metrics">
                <span class="monitor-probe-latest" :title="summary.primary">{{ summary.primary }}</span>
                <span class="monitor-probe-rate">{{ summary.secondary }}</span>
              </div>
            </li></ul>
          </div>
          <div class="monitor-chart-panel" aria-label="延迟图表">
            <n-empty v-if="!history.tasks.length || !history.samples.length" description="该时间范围内暂无延迟数据" />
            <LatencyChart v-else :history="history" />
          </div>
        </template>
      </section>
    </n-card>
  </n-modal>
</template>
