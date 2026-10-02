import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, effectScope, nextTick, ref } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { useMonitor, metricPercent } from '../src/composables/useMonitor.ts'

// Test fixtures only: the application always receives real ServerRecord[] from App.
function server(id, seconds, rx = 1000, tx = 2000, overrides = {}) {
  return {
    id, name: `Server ${id}`, status: 'online', last_seen_at: null, system_info: { os_name: 'Debian' },
    metrics: {
      updated_at: new Date(seconds * 1000).toISOString(), nic_rx_bytes: rx, nic_tx_bytes: tx,
      uptime_seconds: seconds, cpu_percent: 23.4, memory_used_bytes: 256, memory_total_bytes: 1024,
      disk_used_bytes: 512, disk_total_bytes: 1024, cycle_rx_bytes: 4096, cycle_tx_bytes: 8192,
    },
    ...overrides,
  }
}

test('网速按上报时间而非轮询次数计算，并隔离服务器、保留原始数据', async () => {
  const scope = effectScope()
  const records = ref([server(1, 100), server(2, 100)])
  const { speeds } = scope.run(() => useMonitor(records))
  try {
    assert.deepEqual(speeds.value.get(1), { rxSpeed: null, txSpeed: null })
    const incoming = [server(1, 110, 2024, 4048), server(2, 105, 1512, 2512)]
    const original = structuredClone(incoming)
    records.value = incoming
    await nextTick()
    assert.deepEqual(speeds.value.get(1), { rxSpeed: 102.4, txSpeed: 204.8 })
    assert.deepEqual(speeds.value.get(2), { rxSpeed: 102.4, txSpeed: 102.4 })
    records.value = structuredClone(incoming)
    await nextTick()
    assert.deepEqual(speeds.value.get(1), { rxSpeed: 102.4, txSpeed: 204.8 })
    assert.deepEqual(incoming, original)
    records.value = [server(1, 120, 3048, 6096)]
    await nextTick()
    assert.equal(speeds.value.has(2), false)
    assert.deepEqual(speeds.value.get(1), { rxSpeed: 102.4, txSpeed: 204.8 })
  } finally { scope.stop() }
})

test('计数器归零、重启、倒退时间与离线重连不会产生负数或跨断线网速', async () => {
  const scope = effectScope()
  const records = ref([server(1, 100)])
  const { speeds } = scope.run(() => useMonitor(records))
  const missing = { rxSpeed: null, txSpeed: null }
  async function sample(value, expected = missing) {
    records.value = [value]
    await nextTick()
    assert.deepEqual(speeds.value.get(1), expected)
  }
  try {
    await sample(server(1, 110, 10, 20))
    await sample(server(1, 120, 110, 220), { rxSpeed: 10, txSpeed: 20 })
    const restarted = server(1, 130, 210, 420)
    restarted.metrics.uptime_seconds = 1
    await sample(restarted)
    await sample(server(1, 125, 310, 620))
    await sample(server(1, 140, 410, 820, { status: 'offline' }))
    await sample(server(1, 150, 510, 1020))
    await sample(server(1, 160, 610, 1220), { rxSpeed: 10, txSpeed: 20 })
    await sample(server(1, 170, 710, 1420, { metrics: null }))
    await sample(server(1, 180, 810, 1620))
    const invalid = server(1, 190)
    invalid.metrics.updated_at = 'invalid'
    await sample(invalid)
  } finally { scope.stop() }
})

test('比例处理零容量、缺失数据及边界', () => {
  assert.equal(metricPercent(0, 1024), 0)
  assert.equal(metricPercent(256, 1024), 25)
  assert.equal(metricPercent(2048, 1024), 100)
  for (const values of [[0, 0], [undefined, 1024], [NaN, 100], [-1, 100]]) {
    assert.equal(metricPercent(...values), null)
  }
})

let loader, MonitorCard, MonitorView, MetricRing
before(async () => {
  loader = await createServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false },
    optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ default: MonitorCard } = await loader.ssrLoadModule('/src/components/monitor/MonitorCard.vue'))
  ;({ default: MonitorView } = await loader.ssrLoadModule('/src/views/MonitorView.vue'))
  ;({ default: MetricRing } = await loader.ssrLoadModule('/src/components/monitor/MetricRing.vue'))
})
after(async () => { await loader?.close() })

test('卡片渲染周期流量、网速和离线旧指标；缺失 metrics 安全显示', async () => {
  const value = server(1, 100)
  const online = await renderToString(createSSRApp(MonitorCard, { server: value, speed: { txSpeed: 1024, rxSpeed: 2048 } }))
  assert.match(online, /↑ 1 KiB\/s/)
  assert.match(online, /↓ 2 KiB\/s/)
  assert.match(online, /↑ 8 KiB/)
  assert.match(online, /↓ 4 KiB/)
  assert.doesNotMatch(online, /最后更新|最后在线|<footer/)
  for (const label of ['Server 1', '在线', 'Debian', 'CPU', 'RAM', 'Disk', '网络', '本周期流量']) assert.ok(online.includes(label), label)
  const offline = await renderToString(createSSRApp(MonitorCard, { server: { ...value, status: 'offline' }, speed: { txSpeed: 1024, rxSpeed: 2048 } }))
  assert.match(offline, /指标为最后上报值/)
  assert.match(offline, /23.4%/)
  assert.doesNotMatch(offline, /最后更新|最后在线/)
  assert.doesNotMatch(offline, /KiB\/s/)
  const pending = await renderToString(createSSRApp(MonitorCard, { server: { ...value, status: 'pending', metrics: null, system_info: null } }))
  assert.match(pending, /待注册/)
  assert.doesNotMatch(pending, /NaN|Infinity|undefined/)
})

test('圆环阈值、缺失和超出范围数据渲染正确', async () => {
  for (const [value, level, text] of [[69, 'normal', '69%'], [70, 'warning', '70%'], [84, 'warning', '84%'], [85, 'danger', '85%'], [120, 'danger', '100%'], [null, 'normal', '—']]) {
    const html = await renderToString(createSSRApp(MetricRing, { value, label: 'CPU' }))
    assert.ok(html.includes(`monitor-metric-${level}`))
    assert.ok(html.includes(text))
  }
})

test('搜索、状态筛选和指标排序保留原始服务器顺序与待注册统计', async () => {
  const values = [server(1, 100, 0, 0, { name: 'Zulu' }), server(2, 100, 0, 0, { name: 'Alpha', status: 'offline' }), server(3, 100, 0, 0, { name: 'Pending', status: 'pending', metrics: null })]
  values[1].metrics.memory_used_bytes = 768
  const original = MonitorView.setup
  let bindings
  MonitorView.setup = (props, context) => { bindings = original(props, context); return bindings }
  try {
    const html = await renderToString(createSSRApp(MonitorView, { servers: values }))
    assert.match(html, /台待注册/)
    assert.deepEqual(bindings.visibleServers.value.map(value => value.id), [1, 2, 3])
    bindings.sort.value = 'memory'
    assert.deepEqual(bindings.visibleServers.value.map(value => value.id), [2, 1, 3])
    bindings.search.value = ' ALP '
    assert.deepEqual(bindings.visibleServers.value.map(value => value.id), [2])
    bindings.status.value = 'online'
    assert.equal(bindings.visibleServers.value.length, 0)
    bindings.search.value = ''
    assert.deepEqual(bindings.visibleServers.value.map(value => value.id), [1])
    assert.deepEqual(values.map(value => value.id), [1, 2, 3])
    const empty = await renderToString(createSSRApp(MonitorView, { servers: [] }))
    assert.match(empty, /暂无服务器/)
  } finally { MonitorView.setup = original }
})
