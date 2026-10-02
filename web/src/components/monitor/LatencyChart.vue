<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { init, use, type EChartsType } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { LatencyHistory } from '../../types/monitor'
import { latencySeries } from '../../composables/useProbe'

use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])
const props = defineProps<{ history: LatencyHistory }>()
const container = ref<HTMLDivElement | null>(null)
let chart: EChartsType | undefined
let observer: ResizeObserver | undefined
function update() {
  chart?.setOption({
    animation: false,
    color: ['#4b8acb', '#46a68b', '#d3a14c', '#9c79bc', '#ce7180'],
    grid: { left: 56, right: 20, top: 24, bottom: 90 },
    tooltip: { trigger: 'axis', renderMode: 'richText', valueFormatter: (value: unknown) => typeof value === 'number' ? `${value.toFixed(1)} ms` : '—' },
    legend: { type: 'scroll', bottom: 0, textStyle: { color: '#607089' } },
    xAxis: { type: 'time', min: Date.parse(props.history.from), max: Date.parse(props.history.to), axisLabel: { hideOverlap: true } },
    yAxis: { type: 'value', name: '延迟 ms', min: 0 },
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
