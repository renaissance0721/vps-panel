<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { init, use, type EChartsType } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { LatencyHistory } from '../../types/monitor'
import { latencySeries, probeColors } from '../../composables/useProbe'

use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])
const props = defineProps<{ history: LatencyHistory }>()
const container = ref<HTMLDivElement | null>(null)
let chart: EChartsType | undefined
let observer: ResizeObserver | undefined
function update() {
  chart?.setOption({
    animation: false,
    color: probeColors,
    grid: { left: 12, right: 16, top: 68, bottom: 16, containLabel: true },
    tooltip: {
      trigger: 'axis', renderMode: 'richText', confine: true,
      backgroundColor: '#fff', borderColor: '#e1e8f0', padding: 12,
      textStyle: { color: '#34445a', fontSize: 12 },
      axisPointer: { type: 'line', lineStyle: { color: '#9aafc3', type: 'dashed' } },
      valueFormatter: (value: unknown) => typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(1)} ms` : '—',
    },
    legend: { type: 'scroll', top: 4, left: 8, right: 8, itemWidth: 18, itemHeight: 8, textStyle: { color: '#607089', fontSize: 12 } },
    xAxis: { type: 'time', min: Date.parse(props.history.from), max: Date.parse(props.history.to), axisLabel: { hideOverlap: true, color: '#718096' }, axisLine: { lineStyle: { color: '#dce5ef' } }, axisTick: { show: false } },
    yAxis: { type: 'value', name: 'ms', min: 0, splitNumber: 4, nameTextStyle: { color: '#718096' }, axisLabel: { color: '#718096' }, splitLine: { lineStyle: { color: '#edf1f6', type: 'dashed' } } },
    series: latencySeries(props.history),
  }, { notMerge: true })
}
onMounted(() => {
  if (!container.value) return
  chart = init(container.value)
  observer = new ResizeObserver(() => chart?.resize())
  observer.observe(container.value)
  update()
})
watch(() => props.history, update)
onUnmounted(() => { observer?.disconnect(); chart?.dispose(); chart = undefined })
</script>

<template><div ref="container" class="monitor-latency-chart" role="img" aria-label="网络延迟历史折线图，横轴为时间，纵轴为毫秒" /></template>
