<script setup lang="ts">
import { computed } from 'vue'
import { formatPercent } from '../../server'

const props = defineProps<{ label: string; value: number | null; detail?: string }>()
const percentage = computed(() => props.value === null || !Number.isFinite(props.value)
  ? null : Math.min(100, Math.max(0, props.value)))
const level = computed(() => percentage.value !== null && percentage.value >= 85
  ? 'danger' : percentage.value !== null && percentage.value >= 70 ? 'warning' : 'normal')
const text = computed(() => percentage.value === null ? '—' : formatPercent(percentage.value))
</script>

<template>
  <div class="monitor-metric" :class="`monitor-metric-${level}`">
    <div class="monitor-ring" role="img" :aria-label="`${label}：${text}`">
      <svg viewBox="0 0 88 88" aria-hidden="true">
        <circle class="monitor-ring-track" cx="44" cy="44" r="37" />
        <circle class="monitor-ring-value" cx="44" cy="44" r="37" pathLength="100" stroke-dasharray="100" :stroke-dashoffset="100 - (percentage ?? 0)" />
      </svg>
      <strong>{{ text }}</strong>
    </div>
    <span class="monitor-metric-label">{{ label }}</span>
    <small class="monitor-metric-detail" :title="detail">{{ detail || '—' }}</small>
  </div>
</template>
