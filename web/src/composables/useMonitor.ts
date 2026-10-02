import { ref, watch, type Ref } from 'vue'
import type { ServerRecord } from '../types/server'

export type MonitorSpeed = { rxSpeed: number | null; txSpeed: number | null }
type NICSample = { time: number; rx: number; tx: number; uptime: number }

export function useMonitor(servers: Ref<ServerRecord[]>) {
  const speeds = ref(new Map<number, MonitorSpeed>())
  const previous = new Map<number, NICSample>()

  watch(servers, (values) => {
    const next = new Map<number, MonitorSpeed>()
    const present = new Set(values.map((server) => server.id))
    for (const id of previous.keys()) {
      if (!present.has(id)) previous.delete(id)
    }
    for (const server of values) {
      let speed: MonitorSpeed = { rxSpeed: null, txSpeed: null }
      const metrics = server.metrics
      const time = Date.parse(metrics?.updated_at ?? '')
      if (server.status !== 'online' || !metrics || !Number.isFinite(time)
        || !Number.isFinite(metrics.nic_rx_bytes) || metrics.nic_rx_bytes < 0
        || !Number.isFinite(metrics.nic_tx_bytes) || metrics.nic_tx_bytes < 0) {
        previous.delete(server.id)
        next.set(server.id, speed)
        continue
      }
      const sample = { time, rx: metrics.nic_rx_bytes, tx: metrics.nic_tx_bytes, uptime: metrics.uptime_seconds }
      const last = previous.get(server.id)
      if (last && time === last.time) {
        // A repeated poll of the same report is not a new NIC sample.
        speed = speeds.value.get(server.id) ?? speed
      } else {
        if (last && time > last.time && sample.rx >= last.rx && sample.tx >= last.tx && sample.uptime >= last.uptime) {
          const elapsed = (time - last.time) / 1000
          speed = { rxSpeed: (sample.rx - last.rx) / elapsed, txSpeed: (sample.tx - last.tx) / elapsed }
        }
        previous.set(server.id, sample)
      }
      next.set(server.id, speed)
    }
    speeds.value = next
  }, { immediate: true })

  return { speeds }
}

export function metricPercent(used: number | undefined, total: number | undefined): number | null {
  if (used === undefined || total === undefined || !Number.isFinite(used) || !Number.isFinite(total) || used < 0 || total <= 0) return null
  return Math.min(100, (used / total) * 100)
}

export function monitorRelativeTime(value: string | null | undefined, now: number): string {
  const time = Date.parse(value ?? '')
  if (!Number.isFinite(time)) return '—'
  const seconds = Math.max(0, Math.floor((now - time) / 1000))
  if (seconds < 5) return '刚刚'
  if (seconds < 60) return `${seconds} 秒前`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  return `${Math.floor(seconds / 86400)} 天前`
}
