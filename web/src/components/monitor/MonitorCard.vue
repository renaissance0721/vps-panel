<script setup lang="ts">
import { computed } from 'vue'
import type { ServerRecord } from '../../types/server'
import { formatBytes, formatUptime, statusLabel } from '../../server'
import { metricPercent, type MonitorSpeed } from '../../composables/useMonitor'
import MetricRing from './MetricRing.vue'

const props = defineProps<{ server: ServerRecord; speed?: MonitorSpeed }>()
const emit = defineEmits<{ 'view-server': [server: ServerRecord] }>()
const metrics = computed(() => props.server.metrics)
const online = computed(() => props.server.status === 'online')

function bytes(value: number | undefined | null): string {
  return value === undefined || value === null || !Number.isFinite(value) || value < 0 ? '—' : formatBytes(value)
}

function speedText(value: number | undefined | null): string {
  return !online.value || value === undefined || value === null ? '—' : `${bytes(value)}/s`
}
</script>

<template>
  <article class="monitor-card" :class="{ 'monitor-card-offline': !online }">
    <header class="monitor-card-header">
      <button class="monitor-server-link" type="button" :title="server.name" :aria-label="`查看服务器详情：${server.name}`" @click="emit('view-server', server)">{{ server.name }}</button>
      <span class="monitor-status" :class="`monitor-status-${server.status}`">{{ statusLabel(server.status) }}</span>
    </header>
    <p class="monitor-card-subtitle" :title="server.system_info?.os_name">
      <span>{{ server.system_info?.os_name || '—' }}</span>
      <span>· {{ metrics ? formatUptime(metrics.uptime_seconds) : '—' }}</span>
    </p>
    <div class="monitor-metrics">
      <MetricRing label="CPU" :value="metrics?.cpu_percent ?? null" detail="使用率" />
      <MetricRing label="RAM" :value="metricPercent(metrics?.memory_used_bytes, metrics?.memory_total_bytes)" :detail="metrics ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}` : '—'" />
      <MetricRing label="Disk" :value="metricPercent(metrics?.disk_used_bytes, metrics?.disk_total_bytes)" :detail="metrics ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}` : '—'" />
    </div>
    <div class="monitor-network">
      <div class="monitor-network-row">
        <span>网络</span>
        <span class="monitor-upload" title="上传速度">↑ {{ speedText(speed?.txSpeed) }}</span>
        <span class="monitor-download" title="下载速度">↓ {{ speedText(speed?.rxSpeed) }}</span>
      </div>
      <div class="monitor-network-row">
        <span>本周期流量</span>
        <span title="本周期上传">↑ {{ bytes(metrics?.cycle_tx_bytes) }}</span>
        <span title="本周期下载">↓ {{ bytes(metrics?.cycle_rx_bytes) }}</span>
      </div>
    </div>
    <footer v-if="!online && metrics" class="monitor-card-footer">指标为最后上报值</footer>
  </article>
</template>
