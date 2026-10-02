import { getCurrentScope, onScopeDispose, ref } from 'vue'
import { api } from '../api/client'
import type { ServerRecord } from '../types/server'
import type { LatencyHistory, LatencyHours, ProbeInput, ProbeOutcome, ProbeTask, ProbeType } from '../types/monitor'

export function supportsProbe(server: ServerRecord, type: ProbeType): boolean {
  return server.agent_capabilities?.includes(`probe.${type}`) ?? false
}

export function probeOutcomeLabel(outcome: ProbeOutcome | ''): string {
  return { success: '成功', timeout: '超时', dns_error: 'DNS 解析失败', connect_error: '连接失败', permission_error: '无 ICMP / 网络权限', cancelled: '已取消', '': '暂无数据' }[outcome]
}

export function useProbeHistory() {
  const history = ref<LatencyHistory | null>(null)
  const loading = ref(false)
  const error = ref('')
  let sequence = 0
  let controller: AbortController | undefined

  function reset() {
    sequence++
    controller?.abort()
    history.value = null
    loading.value = false
    error.value = ''
  }

  async function load(serverID: number, hours: LatencyHours) {
    const request = ++sequence
    controller?.abort()
    controller = new AbortController()
    loading.value = true
    error.value = ''
    history.value = null
    try {
      const value = await api<LatencyHistory>(`/api/monitor/servers/${serverID}/latency?hours=${hours}`, { signal: controller.signal })
      if (request === sequence) history.value = value
    } catch (reason) {
      if (request === sequence) error.value = reason instanceof Error ? reason.message : '无法加载延迟历史'
    } finally {
      if (request === sequence) loading.value = false
    }
  }
  if (getCurrentScope()) onScopeDispose(reset)
  return { history, loading, error, load, reset }
}

export function useProbeTasks() {
  const tasks = ref<ProbeTask[]>([])
  const loading = ref(false)
  const saving = ref(false)
  const error = ref('')
  async function load() {
    loading.value = true
    error.value = ''
    try { tasks.value = (await api<{ tasks: ProbeTask[] }>('/api/monitor/probes')).tasks }
    catch (reason) { error.value = reason instanceof Error ? reason.message : '无法加载探测任务' }
    finally { loading.value = false }
  }
  async function save(id: number | null, input: ProbeInput): Promise<boolean> {
    saving.value = true
    error.value = ''
    try {
      const { task } = await api<{ task: ProbeTask }>(id === null ? '/api/monitor/probes' : `/api/monitor/probes/${id}`, { method: id === null ? 'POST' : 'PATCH', body: JSON.stringify(input) })
      const index = tasks.value.findIndex(item => item.id === task.id)
      if (index < 0) tasks.value.push(task)
      else tasks.value[index] = task
      return true
    } catch (reason) { error.value = reason instanceof Error ? reason.message : '无法保存探测任务'; return false }
    finally { saving.value = false }
  }
  async function remove(id: number): Promise<boolean> {
    saving.value = true
    error.value = ''
    try {
      await api(`/api/monitor/probes/${id}`, { method: 'DELETE' })
      tasks.value = tasks.value.filter(task => task.id !== id)
      return true
    } catch (reason) { error.value = reason instanceof Error ? reason.message : '无法删除探测任务'; return false }
    finally { saving.value = false }
  }
  return { tasks, loading, saving, error, load, save, remove }
}

// Explicit nulls break failures; inserted nulls break periods without Agent reports.
export function latencySeries(history: LatencyHistory) {
  const samplesByTask = new Map<number, typeof history.samples>()
  for (const sample of history.samples) {
    if (!samplesByTask.has(sample.task_id)) samplesByTask.set(sample.task_id, [])
    samplesByTask.get(sample.task_id)!.push(sample)
  }
  return history.tasks.map(task => {
    const samples = (samplesByTask.get(task.id) ?? []).slice().sort((a, b) => Date.parse(a.timestamp) - Date.parse(b.timestamp))
    const data: [number, number | null][] = []
    const interval = task.interval_seconds * 1000
    let previous: number | undefined
    for (const sample of samples) {
      const time = Date.parse(sample.timestamp)
      if (!Number.isFinite(time)) continue
      if (previous !== undefined && time - previous > interval * 1.5) data.push([previous + interval, null])
      const latency = sample.outcome === 'success' && sample.latency_ms !== null && Number.isFinite(sample.latency_ms) && sample.latency_ms >= 0 ? sample.latency_ms : null
      data.push([time, latency])
      previous = time
    }
    return { id: String(task.id), name: `${task.name} ${task.type.toUpperCase()} #${task.id}`, type: 'line' as const, connectNulls: false, showSymbol: true, symbolSize: 4, smooth: false, data }
  })
}
