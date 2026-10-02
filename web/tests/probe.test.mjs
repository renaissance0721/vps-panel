import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, effectScope } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, useProbeHistory, useProbeTasks, latencySeries, supportsProbe, Detail, View, Manager
const originalFetch = globalThis.fetch
before(async () => {
  loader = await createServer({
    configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ useProbeHistory, useProbeTasks, latencySeries, supportsProbe } = await loader.ssrLoadModule('/src/composables/useProbe.ts'))
  ;({ default: Detail } = await loader.ssrLoadModule('/src/components/monitor/MonitorServerDetail.vue'))
  ;({ default: View } = await loader.ssrLoadModule('/src/views/MonitorView.vue'))
  ;({ default: Manager } = await loader.ssrLoadModule('/src/components/monitor/ProbeTaskManager.vue'))
})
after(async () => { globalThis.fetch = originalFetch; await loader?.close() })

function server(overrides = {}) {
  return { id: 1, name: 'Tokyo', status: 'online', agent_capabilities: ['probe.tcp'], system_info: { hostname: 'host', os_name: 'Debian', os_version: '12', kernel: '6.1', arch: 'amd64', ipv4: ['192.0.2.1'], ipv6: ['2001:db8::1'], public_ipv4: '198.51.100.1' }, metrics: { memory_total_bytes: 1024, disk_total_bytes: 4096, uptime_seconds: 60 }, ...overrides }
}
function task(overrides = {}) {
  return { id: 1, name: 'Tokyo TCP', type: 'tcp', target: 'example.com', port: 443, interval_seconds: 60, enabled: true, server_ids: [1], created_at: '', updated_at: '', latest_latency_ms: 42, latest_outcome: 'success', failure_rate: 0.2, ...overrides }
}
function history(tasks = [task()], samples = []) { return { tasks, samples, from: '2026-10-02T00:00:00Z', to: '2026-10-02T06:00:00Z' } }
function response(value, status = 200) { return { ok: status < 400, status, json: async () => value } }
async function render(component, props, configure = () => {}) {
  const original = component.setup
  let bindings
  component.setup = (props, context) => { bindings = original(props, context); configure(bindings); return bindings }
  try { return { html: await renderToString(createSSRApp(component, props)), bindings } }
  finally { component.setup = original }
}

test('探针卡片只打开独立详情，按现有 ServerRecord 显示基础信息，管理入口仅 admin 可见', async () => {
  const record = server({ agent_capabilities: [] })
  const { bindings, html } = await render(View, { servers: [record], role: 'admin' })
  assert.match(html, /延迟探测/)
  bindings.viewMonitor(record)
  assert.equal(bindings.monitorDetailOpen.value, true)
  assert.equal(bindings.selectedMonitorServer.value.id, 1)
  const detail = await render(Detail, { server: record, show: true })
  for (const label of ['基础信息', '主机名', '操作系统', '系统版本', '内核', '架构', '内存总量', '磁盘总量', 'IPv4', 'IPv6', '公网 IPv4', '运行时间', '网络延迟']) assert.ok(detail.html.includes(label), label)
  for (const forbidden of ['所有者', '访问范围', '到期', '续费', 'Agent 类型', 'Agent 版本', 'Agent API', 'Agent 升级', '一键诊断', '出站', '防火墙', '删除服务器', '安装令牌', '流量配置']) assert.ok(!detail.html.includes(forbidden), forbidden)
  assert.match(detail.html, /当前 Agent 不支持延迟探测/)
  assert.match(detail.html, /1 小时/); assert.match(detail.html, /6 小时/); assert.match(detail.html, /24 小时/)
  assert.equal(detail.bindings.hours.value, 6)
  const missing = await render(Detail, { server: server({ system_info: null, metrics: null, agent_capabilities: [] }), show: true })
  assert.doesNotMatch(missing.html, /undefined|NaN|Infinity/)
  assert.ok(missing.html.includes('—'))
  const vip = await render(View, { servers: [], role: 'vip' })
  assert.doesNotMatch(vip.html, /延迟探测/)
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  const monitorTag = app.match(/<MonitorView\b[^>]*>/)[0]
  assert.doesNotMatch(monitorTag, /view-server|serverState\.viewServer/)
})

test('延迟请求 loading/error/empty、范围切换与晚返回隔离，关闭后不恢复数据', async () => {
  const pending = []
  globalThis.fetch = (url, options) => new Promise((resolve, reject) => pending.push({ url, options, resolve, reject }))
  const scope = effectScope()
  const state = scope.run(() => useProbeHistory())
  try {
    const old = state.load(1, 6)
    assert.equal(state.loading.value, true)
    assert.equal(pending[0].url, '/api/monitor/servers/1/latency?hours=6')
    const recent = state.load(1, 1)
    assert.equal(pending[0].options.signal.aborted, true)
    pending[1].resolve(response(history([task({ name: 'new' })])))
    await recent
    pending[0].resolve(response(history([task({ name: 'stale' })])))
    await old
    assert.equal(state.history.value.tasks[0].name, 'new')
    assert.equal(state.loading.value, false)
    const failed = state.load(2, 24)
    pending[2].resolve(response({ error: '无权限' }, 404))
    await failed
    assert.equal(state.error.value, '无权限')
    assert.equal(state.history.value, null)
    const empty = state.load(2, 6)
    pending[3].resolve(response(history([], []))); await empty
    assert.equal(state.history.value.tasks.length, 0)
    const afterClose = state.load(2, 1)
    state.reset()
    pending[4].resolve(response(history())); await afterClose
    assert.equal(state.history.value, null)
    assert.equal(state.loading.value, false)
  } finally { scope.stop() }
})

test('网络延迟区域渲染加载、错误、无任务和无样本状态', async () => {
  globalThis.fetch = async () => response(history())
  for (const [configure, expected] of [
    [b => { b.reset(); b.loading.value = true }, /正在加载延迟历史/],
    [b => { b.reset(); b.error.value = '测试错误' }, /测试错误/],
    [b => { b.reset(); b.history.value = history([]) }, /尚未分配延迟探测任务/],
    [b => { b.reset(); b.history.value = history([task({ type: 'icmp', latest_latency_ms: null, latest_outcome: 'permission_error', failure_rate: null })]) }, /所选时段暂无延迟数据/],
  ]) {
    const { html } = await render(Detail, { server: server(), show: true }, configure)
    assert.match(html, expected)
  }
  assert.equal(supportsProbe(server(), 'tcp'), true)
  assert.equal(supportsProbe(server(), 'icmp'), false)
  assert.equal(supportsProbe(server({ agent_capabilities: [], agent_version: 'v100.0.0', agent_implementation: 'vps-panel-agent' }), 'tcp'), false)
})

test('多 series 保留零延迟，timeout、无数据和离线缺口均断线', () => {
  const samples = [
    { task_id: 1, timestamp: '2026-10-02T00:00:00Z', outcome: 'success', latency_ms: 42 },
    { task_id: 1, timestamp: '2026-10-02T00:01:00Z', outcome: 'timeout', latency_ms: 0 },
    { task_id: 1, timestamp: '2026-10-02T00:02:00Z', outcome: 'success', latency_ms: 0 },
    { task_id: 1, timestamp: '2026-10-02T00:10:00Z', outcome: 'success', latency_ms: 40 },
    { task_id: 2, timestamp: '2026-10-02T00:01:00Z', outcome: 'permission_error', latency_ms: null },
  ]
  const series = latencySeries(history([task(), task({ id: 2, type: 'icmp' })], samples))
  assert.equal(series.length, 2)
  assert.equal(series[0].connectNulls, false)
  assert.deepEqual(series[0].data.map(point => point[1]), [42, null, 0, null, 40])
  assert.deepEqual(series[1].data.map(point => point[1]), [null])
  assert.equal(series[0].data[3][0], Date.parse('2026-10-02T00:03:00Z'))
  assert.equal(samples.length, 5)
})

test('任务管理 CRUD 请求、默认值、ICMP 端口清空及不支持节点提示', async () => {
  const requests = []
  globalThis.fetch = async (url, options) => {
    requests.push({ url, options })
    if (!options.method) return response({ tasks: [task()] })
    if (options.method === 'DELETE') return response(null, 204)
    return response({ task: task({ ...JSON.parse(options.body) }) })
  }
  const state = useProbeTasks()
  await state.load()
  assert.equal(state.tasks.value.length, 1)
  const input = { name: 'new', type: 'tcp', target: 'example.com', port: 443, interval_seconds: 60, enabled: true, server_ids: [1] }
  assert.equal(await state.save(null, input), true)
  assert.equal(requests.at(-1).options.method, 'POST')
  assert.equal(await state.save(1, { ...input, name: 'updated' }), true)
  assert.equal(state.tasks.value[0].name, 'updated')
  assert.equal(requests.at(-1).options.method, 'PATCH')
  assert.equal(await state.remove(1), true)
  assert.equal(state.tasks.value.length, 0)
  assert.equal(requests.at(-1).options.method, 'DELETE')
  const { html, bindings } = await render(Manager, { show: true, servers: [server(), server({ id: 2, name: 'Legacy', agent_capabilities: [] })] }, b => { b.edit(); b.tasks.value = [task()] })
  for (const text of ['新建任务', '名称', '类型', '目标', '周期', '执行节点数', '状态', '编辑', '删除', '保存']) assert.ok(html.includes(text), text)
  assert.equal(bindings.form.value.interval_seconds, 60)
  assert.equal(bindings.serverOptions.value[1].disabled, true)
  assert.match(bindings.serverOptions.value[1].label, /不支持 TCP/)
  bindings.edit(task())
  bindings.form.value.type = 'icmp'
  await bindings.submit()
  assert.equal(JSON.parse(requests.at(-1).options.body).port, null)
  globalThis.fetch = async () => response({ error: '保存失败' }, 400)
  assert.equal(await state.save(null, input), false)
  assert.equal(state.error.value, '保存失败')
})
