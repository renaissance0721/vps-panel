import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createRenderer, createSSRApp, effectScope, nextTick, reactive, ssrContextKey } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, useProbeHistory, useProbeTasks, useServerProbeAssignments, latencySeries, supportsProbe, probeTarget, Detail, View, Manager, AssignmentEditor
const originalFetch = globalThis.fetch
before(async () => {
  loader = await createServer({
    configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ useProbeHistory, useProbeTasks, useServerProbeAssignments, latencySeries, supportsProbe, probeTarget } = await loader.ssrLoadModule('/src/composables/useProbe.ts'))
  ;({ default: Detail } = await loader.ssrLoadModule('/src/components/monitor/MonitorServerDetail.vue'))
  ;({ default: View } = await loader.ssrLoadModule('/src/views/MonitorView.vue'))
  ;({ default: Manager } = await loader.ssrLoadModule('/src/components/monitor/ProbeTaskManager.vue'))
  ;({ default: AssignmentEditor } = await loader.ssrLoadModule('/src/components/monitor/MonitorProbeAssignmentEditor.vue'))
})
after(async () => { globalThis.fetch = originalFetch; await loader?.close() })

function server(overrides = {}) {
  return { id: 1, name: 'Tokyo', status: 'online', agent_capabilities: ['probe.tcp'], system_info: { hostname: 'host', os_name: 'Debian', os_version: '12', kernel: '6.1', arch: 'amd64', ipv4: ['192.0.2.1'], ipv6: ['2001:db8::1'], public_ipv4: '198.51.100.1', public_ipv6: '2606:4700:4700::1111' }, metrics: { memory_total_bytes: 1024, disk_total_bytes: 4096, uptime_seconds: 60 }, ...overrides }
}
function task(overrides = {}) {
  return { id: 1, name: 'Tokyo TCP', type: 'tcp', target: 'example.com', port: 443, interval_seconds: 60, enabled: true, default_on: false, server_ids: [1], created_at: '', updated_at: '', latest_latency_ms: 42, latest_outcome: 'success', failure_rate: 0.2, ...overrides }
}
function history(tasks = [task()], samples = [], hours = 1) { return { range_hours: hours, tasks, samples, from: '2026-10-02T00:00:00Z', to: '2026-10-02T06:00:00Z' } }
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
  for (const label of ['基础信息', '主机名', '操作系统', '系统版本', '内核', '架构', '内存总量', '磁盘总量', 'IPv4', 'IPv6', '公网 IPv4', '公网 IPv6', '运行时间', '网络延迟']) assert.ok(detail.html.includes(label), label)
  for (const forbidden of ['所有者', '访问范围', '到期', '续费', 'Agent 类型', 'Agent 版本', 'Agent API', 'Agent 升级', '一键诊断', '出站', '防火墙', '删除服务器', '安装令牌', '流量配置']) assert.ok(!detail.html.includes(forbidden), forbidden)
  assert.match(detail.html, /当前 Agent 不支持延迟探测/)
  assert.match(detail.html, /1 小时/); assert.match(detail.html, /6 小时/); assert.match(detail.html, /24 小时/)
  assert.equal(detail.bindings.hours.value, 1)
  assert.match(detail.html, /aria-pressed="true"[^>]*>1 小时/)
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
    [b => { b.reset(); b.history.value = history([task({ type: 'icmp', latest_latency_ms: null, latest_outcome: '', failure_rate: null })]) }, /该时间范围内暂无延迟数据/],
  ]) {
    const { html } = await render(Detail, { server: server(), show: true }, configure)
    assert.match(html, expected)
    assert.doesNotMatch(html, /网络延迟历史折线图/)
  }
  assert.equal(supportsProbe(server(), 'tcp'), true)
  assert.equal(supportsProbe(server(), 'icmp'), false)
  assert.equal(supportsProbe(server({ agent_capabilities: [], agent_version: 'v100.0.0', agent_implementation: 'vps-panel-agent' }), 'tcp'), false)
})

test('详情默认请求并高亮 1 小时，切换和刷新使用当前范围，重开恢复默认', async () => {
  const pending = []
  globalThis.fetch = (url, options) => new Promise(resolve => pending.push({ url, options, resolve }))
  const props = reactive({ show: true, server: server() })
  // Mount the real setup/watchers in a component scope; no browser or chart is needed here.
  let b
  const renderer = createRenderer({ createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null })
  const app = renderer.createApp({ setup() { b = Detail.setup(props, { expose() {} }); return () => null } })
  app.provide(ssrContextKey, {})
  app.mount({})
  try {
    assert.equal(b.hours.value, 1)
    assert.equal(pending[0].url, '/api/monitor/servers/1/latency?hours=1')
    for (const [range, rate] of [[6, 50], [24, 75]]) {
      const previous = pending.at(-1)
      b.hours.value = range
      await nextTick()
      assert.equal(previous.options.signal.aborted, true)
      assert.equal(b.loading.value, true)
      assert.equal(b.history.value, null)
      const current = pending.at(-1)
      assert.equal(current.url, `/api/monitor/servers/1/latency?hours=${range}`)
      current.resolve(response(history([task({ failure_rate: rate })], [], range)))
      await new Promise(resolve => setImmediate(resolve))
      previous.resolve(response(history([task({ failure_rate: 99 })])))
      await new Promise(resolve => setImmediate(resolve))
      assert.equal(b.history.value.range_hours, range)
      assert.equal(b.history.value.tasks[0].failure_rate, rate)
    }
    b.refresh()
    assert.equal(pending.at(-1).url, '/api/monitor/servers/1/latency?hours=24')
    props.show = false
    await nextTick()
    assert.equal(b.history.value, null)
    assert.equal(pending.at(-1).options.signal.aborted, true)
    props.show = true
    await nextTick()
    assert.equal(b.hours.value, 1)
    assert.equal(pending.at(-1).url, '/api/monitor/servers/1/latency?hours=1')
  } finally { app.unmount() }
})

test('摘要在图表上方，以延迟和简短比率同排展示，数值随范围更新但不重复范围文案', async () => {
  globalThis.fetch = async () => response(history())
  for (const [hours, rate] of [[1, 0], [6, 50], [24, 75]]) {
    const tasks = [task({ name: '上海电信', latest_latency_ms: 33.2, failure_rate: rate }), task({ id: 2, name: 'Cloudflare', type: 'icmp', port: null, latest_latency_ms: 42, failure_rate: rate })]
    const samples = tasks.map(t => ({ task_id: t.id, timestamp: '2026-10-02T00:01:00Z', outcome: 'success', latency_ms: t.latest_latency_ms }))
    const { html } = await render(Detail, { server: server(), show: true }, b => { b.reset(); b.hours.value = hours; b.history.value = history(tasks, samples, hours) })
    const summaryHTML = html.match(/<ul class="monitor-probe-summaries">([\s\S]*?)<\/ul>/)[1]
    assert.doesNotMatch(summaryHTML, /最近|小时|失败率|丢包率|24h/)
    assert.ok(summaryHTML.includes(`<span class="monitor-probe-rate">${rate.toFixed(1)}%失败</span>`))
    assert.ok(summaryHTML.includes(`<span class="monitor-probe-rate">${rate.toFixed(1)}%丢包</span>`))
    assert.match(summaryHTML, /<div class="monitor-probe-metrics"><span class="monitor-probe-latest"[^>]*>33\.2 ms<\/span><span class="monitor-probe-rate">[^<]+<\/span><\/div>/)
    assert.match(summaryHTML, /<div class="monitor-probe-metrics"><span class="monitor-probe-latest"[^>]*>42\.0 ms<\/span><span class="monitor-probe-rate">[^<]+<\/span><\/div>/)
    assert.match(html, new RegExp(`aria-pressed="true"[^>]*>${hours} 小时`))
    assert.ok(html.indexOf('aria-label="探测摘要"') < html.indexOf('aria-label="延迟图表"'))
    assert.ok(html.indexOf('上海电信 TCP') < html.indexOf('网络延迟历史折线图'))
    assert.deepEqual(latencySeries(history(tasks, samples)).map(s => s.name), ['上海电信 TCP', 'Cloudflare ICMP'])
  }
  const empty = await render(Detail, { server: server(), show: true }, b => { b.reset(); b.history.value = history([task({ latest_latency_ms: null, latest_outcome: '', failure_rate: null })]) })
  assert.match(empty.html, /暂无数据/)
  assert.match(empty.html, /—失败/)
  assert.match(empty.html, /该时间范围内暂无延迟数据/)
  assert.doesNotMatch(empty.html, /0\.0 ms|0\.0%|网络延迟历史折线图/)
})

test('超时及失败摘要保持中文状态与对应比率同排，灰色小字不换行', async () => {
  globalThis.fetch = async () => response(history())
  for (const [outcome, label] of [['timeout', '超时'], ['dns_error', 'DNS 解析失败'], ['connect_error', '连接失败']]) {
    const tasks = [
      task({ name: '上海联通', latest_latency_ms: null, latest_outcome: outcome, failure_rate: 20 }),
      task({ id: 2, name: 'ICMP 节点', type: 'icmp', latest_latency_ms: null, latest_outcome: outcome, failure_rate: 3.2 }),
    ]
    const { html } = await render(Detail, { server: server(), show: true }, b => { b.reset(); b.history.value = history(tasks) })
    assert.match(html, new RegExp(`<div class="monitor-probe-metrics"><span class="monitor-probe-latest"[^>]*>${label}</span><span class="monitor-probe-rate">20.0%失败</span></div>`))
    assert.match(html, new RegExp(`<div class="monitor-probe-metrics"><span class="monitor-probe-latest"[^>]*>${label}</span><span class="monitor-probe-rate">3.2%丢包</span></div>`))
    assert.doesNotMatch(html, /最近|(?:1|6|24)小时(?:失败率|丢包率)/)
  }
  const css = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')
  const metricsCSS = css.match(/\.monitor-probe-metrics\s*\{([^}]+)\}/)[1]
  for (const rule of ['display: flex', 'flex-wrap: nowrap', 'gap: 10px', 'color: var(--color-text-secondary)', 'font-size: 12px', 'font-weight: 400', 'white-space: nowrap']) assert.ok(metricsCSS.includes(rule), rule)
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

test('任务管理 CRUD 请求使用 endpoint，不发送独立端口', async () => {
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
  const input = { name: 'new', type: 'tcp', target: 'example.com:443', interval_seconds: 60, enabled: true, default_on: false, server_ids: [1] }
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
  assert.equal(bindings.serverOptions.value[1].disabled, false)
  assert.match(bindings.serverOptions.value[1].label, /等待 Agent 支持 TCPing/)
  bindings.edit(task())
  assert.equal(bindings.form.value.target, 'example.com:443')
  await bindings.submit()
  assert.equal(JSON.parse(requests.at(-1).options.body).target, 'example.com:443')
  assert.equal('port' in JSON.parse(requests.at(-1).options.body), false)
  bindings.form.value.type = 'icmp'
  bindings.form.value.target = '2400:3200::1'
  await bindings.submit()
  assert.equal(JSON.parse(requests.at(-1).options.body).target, '2400:3200::1')
  assert.equal('port' in JSON.parse(requests.at(-1).options.body), false)
  globalThis.fetch = async () => response({ error: '保存失败' }, 400)
  assert.equal(await state.save(null, input), false)
  assert.equal(state.error.value, '保存失败')
})

test('TCP endpoint 显示与编辑包含 IPv6 方括号，表单没有独立端口输入', async () => {
  assert.equal(probeTarget(task()), 'example.com:443')
  assert.equal(probeTarget(task({ target: '1.1.1.1', port: 80 })), '1.1.1.1:80')
  assert.equal(probeTarget(task({ target: '2400:3200::1' })), '[2400:3200::1]:443')
  assert.equal(probeTarget(task({ type: 'icmp', target: '2400:3200::1', port: null })), '2400:3200::1')
  const { html, bindings } = await render(Manager, { show: true, servers: [] }, b => {
    b.tasks.value = [task(), task({ id: 2, target: '2400:3200::1' })]
    b.edit(task({ target: '2400:3200::1' }))
  })
  assert.equal(bindings.form.value.target, '[2400:3200::1]:443')
  assert.match(html, /example.com:443/)
  assert.match(html, /\[2400:3200::1\]:443/)
  assert.match(html, /example.com:443 \/ 1.1.1.1:80 \/ \[IPv6\]:443/)
  assert.doesNotMatch(html, /TCP 端口|<label>端口/)
  assert.equal('port' in bindings.form.value, false)
})

test('全选与清空作用于可用节点，能力仅提示等待下发', async () => {
  const servers = [
    server(), server({ id: 2, status: 'offline' }),
    server({ id: 3, archived_at: '2026-10-02T00:00:00Z' }),
    server({ id: 4, decommission_status: 'pending' }),
    server({ id: 5, agent_capabilities: ['probe.icmp'] }),
    server({ id: 6, agent_capabilities: [], status: 'offline' }),
  ]
  const { bindings, html } = await render(Manager, { show: true, servers }, b => { b.edit(); b.form.value.default_on = false })
  assert.match(html, /全选/); assert.match(html, /清空/)
  bindings.selectAll()
  assert.deepEqual(bindings.form.value.server_ids, [1, 2, 5, 6])
  assert.equal(bindings.selectionUnavailable.value, false)
  assert.match(bindings.serverOptions.value[1].label, /当前离线/)
  assert.doesNotMatch(bindings.serverOptions.value[1].label, /不支持/)
  assert.equal(bindings.serverOptions.value[1].disabled, false)
  assert.match(bindings.serverOptions.value[5].label, /等待 Agent 支持 TCPing.*当前离线/)
  bindings.clearSelection()
  assert.deepEqual(bindings.form.value.server_ids, [])
  bindings.form.value.type = 'icmp'
  bindings.selectAll()
  assert.deepEqual(bindings.form.value.server_ids, [1, 2, 5, 6])
  bindings.form.value.type = 'tcp'
  assert.equal(bindings.selectionUnavailable.value, false)
  bindings.form.value.server_ids = [3]
  assert.equal(bindings.selectionUnavailable.value, true)
})

test('默认开关仅控制未来继承，当前选择始终可编辑并原样提交', async () => {
  const { bindings, html } = await render(Manager, { show: true, servers: [server(), server({ id: 2, status: 'offline' })] }, b => {
    b.edit(); b.tasks.value = [task({ default_on: true, server_ids: [] }), task({ id: 2 })]
  })
  assert.equal(bindings.form.value.default_on, true)
  assert.equal(bindings.form.value.enabled, true)
  assert.match(html, /0 台 · 新服务器自动加入/)
  assert.doesNotMatch(html, /全部兼容节点/)
  assert.deepEqual(bindings.form.value.server_ids, [])
  assert.match(html, /1 台/)
  assert.match(html, /开启后，未来新增的服务器会自动加入此探测任务；当前已有服务器仍以以上选择为准。/)
  assert.doesNotMatch(html, /<select[^>]*disabled[^>]*aria-label="执行服务器"/)
  assert.match(html, /全选/); assert.match(html, /清空/)
  for (const name of ['默认应用到以后新增的服务器', '启用']) {
    assert.ok(html.includes(`<div class="monitor-probe-switch-row"><span>${name}</span>`))
  }
  const css = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')
  assert.match(css, /\.monitor-probe-switch-row\s*\{[^}]*display: flex;[^}]*align-items: center;[^}]*justify-content: space-between;/)
  let input
  globalThis.fetch = async (_, options) => { input = JSON.parse(options.body); return response({ task: task({ default_on: true, server_ids: [] }) }) }
  bindings.form.value.name = 'default'
  bindings.form.value.target = 'example.com:443'
  bindings.form.value.server_ids = [1]
  await bindings.submit()
  assert.equal(input.default_on, true)
  assert.deepEqual(input.server_ids, [1])
  bindings.form.value.default_on = false
  assert.deepEqual(bindings.form.value.server_ids, [1])
  bindings.form.value.default_on = true
  assert.deepEqual(bindings.form.value.server_ids, [1])
  bindings.selectAll()
  assert.deepEqual(bindings.form.value.server_ids, [1, 2])
  bindings.clearSelection()
  assert.deepEqual(bindings.form.value.server_ids, [])
  bindings.edit(task({ default_on: false }))
  assert.equal(bindings.form.value.default_on, false)
})

test('编辑默认任务保留未声明能力的继承节点并显示真实分配数', async () => {
  const { bindings, html } = await render(Manager, { show: true, servers: [server(), server({ id: 2, status: 'pending', agent_capabilities: [] })] }, b => {
    const value = task({ default_on: true, server_ids: [1, 2] })
    b.tasks.value = [value]; b.edit(value)
  })
  assert.match(html, /2 台 · 新服务器自动加入/)
  assert.deepEqual(bindings.form.value.server_ids, [1, 2])
  assert.equal(bindings.selectionUnavailable.value, false)
  assert.equal(bindings.serverOptions.value[1].disabled, false)
  bindings.form.value.default_on = false
  assert.deepEqual(bindings.form.value.server_ids, [1, 2])
})

test('服务器详情仅管理员显示编辑监测，离线与未知能力不阻止入口', async () => {
  globalThis.fetch = () => new Promise(() => {})
  for (const role of ['admin', 'vip']) {
    const { html } = await render(Detail, { server: server({ status: 'offline', agent_capabilities: [] }), role, show: true })
    assert.equal(html.includes('编辑监测'), role === 'admin')
    assert.match(html, /1 小时/); assert.match(html, /6 小时/); assert.match(html, /24 小时/)
  }
  const { html } = await render(Detail, { server: server({ decommission_status: 'pending' }), role: 'admin', show: true })
  assert.match(html, /<button[^>]*disabled[^>]*>编辑监测/)
})

function mountAssignmentEditor(props) {
  let bindings
  const events = []
  const renderer = createRenderer({ createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null })
  const app = renderer.createApp({ setup() { bindings = AssignmentEditor.setup(props, { expose() {}, emit: (...args) => events.push(args) }); return () => null } })
  app.provide(ssrContextKey, {})
  app.mount({})
  return { bindings, events, unmount: () => app.unmount() }
}
const flushAssignments = () => new Promise(resolve => setImmediate(resolve))

test('监测编辑加载当前勾选，单次 PUT 后重新 GET，并通知详情刷新当前时间范围', async () => {
  const requests = []
  const initial = [task({ assigned: true }), task({ id: 2, name: '广州联通', type: 'icmp', port: null, assigned: false, enabled: false })]
  globalThis.fetch = async (url, options) => {
    requests.push({ url, options })
    if (options.method === 'PUT') return response(undefined, 204)
    return response({ tasks: initial })
  }
  const scope = mountAssignmentEditor(reactive({ show: true, server: server({ status: 'offline' }) }))
  try {
    await flushAssignments()
    const b = scope.bindings
    assert.deepEqual(b.selectedIDs.value, [1])
    b.toggle(1, false); b.toggle(2, true)
    await b.submit()
    assert.deepEqual(requests.map(r => r.options.method ?? 'GET'), ['GET', 'PUT', 'GET'])
    assert.ok(requests.every(r => r.url === '/api/monitor/servers/1/probe-assignments'))
    assert.deepEqual(JSON.parse(requests[1].options.body), { task_ids: [2] })
    assert.deepEqual(scope.events, [['saved', 1], ['update:show', false]])
  } finally { scope.unmount() }
  requests.length = 0
  globalThis.fetch = async (url, options) => { requests.push({ url, options }); return response(history()) }
  const { bindings } = await render(Detail, { show: true, server: server(), role: 'admin' })
  bindings.hours.value = 24
  bindings.assignmentsSaved(1)
  assert.equal(requests.at(-1).url, '/api/monitor/servers/1/latency?hours=24')
  const count = requests.length
  bindings.assignmentsSaved(2)
  assert.equal(requests.length, count)
})

test('编辑列表显示全局停用、能力不足、离线提示，并支持搜索和独立选择', async () => {
  globalThis.fetch = () => new Promise(() => {})
  const values = [task({ name: '上海电信', assigned: true }), task({ id: 2, name: '广州联通', type: 'icmp', port: null, assigned: false, enabled: false, target: 'carrier.example.net' })]
  const { html, bindings: b } = await render(AssignmentEditor, { show: true, server: server({ status: 'offline' }) }, b => {
    b.tasks.value = values; b.loading.value = false; b.loaded.value = true; b.selectedIDs.value = [1]
  })
  assert.match(html, /aria-label="监测 上海电信"[^>]*checked/)
  const unassigned = html.match(/<input[^>]*aria-label="监测 广州联通"[^>]*>/)[0]
  assert.doesNotMatch(unassigned, /checked|disabled/)
  for (const text of ['全局停用', '当前 Agent 暂不支持 ICMP，支持后自动生效', '服务器当前离线', '下次连接后生效', 'TCP · example.com:443 · 60 秒']) assert.ok(html.includes(text), text)
  b.search.value = 'carrier.example'
  assert.deepEqual(b.visibleTasks.value.map(t => t.id), [2])
  b.selectAll()
  assert.deepEqual(b.selectedIDs.value, [1, 2])
  assert.equal(values[1].enabled, false)
  b.clearSelection()
  assert.deepEqual(b.selectedIDs.value, [])
  b.search.value = '上海'
  b.selectAll()
  assert.deepEqual(b.selectedIDs.value, [1])
  b.select(Array.from({ length: 64 }, (_, i) => i + 1))
  b.toggle(65, true)
  assert.equal(b.selectedIDs.value.length, 64)
  assert.match(b.error.value, /最多选择 64/)
  const source = await readFile(new URL('../src/components/monitor/MonitorProbeAssignmentEditor.vue', import.meta.url), 'utf8')
  assert.match(source, /max-height: 50vh; overflow-y: auto/)
  assert.doesNotMatch(source, /window\.location|setTimeout|default_on|server_ids/)
})

test('无任务、加载失败和保存失败保留可理解状态，空分配允许保存', async () => {
  globalThis.fetch = async () => response({ tasks: [] })
  const props = reactive({ show: true, server: server() })
  const scope = mountAssignmentEditor(props)
  try {
    await flushAssignments()
    assert.equal(scope.bindings.loaded.value, true)
    let input
    globalThis.fetch = async (_, options) => {
      if (options.method === 'PUT') { input = JSON.parse(options.body); return response(undefined, 204) }
      return response({ tasks: [] })
    }
    await scope.bindings.submit()
    assert.deepEqual(input, { task_ids: [] })
  } finally { scope.unmount() }
  globalThis.fetch = () => new Promise(() => {})
  const empty = await render(AssignmentEditor, { show: true, server: server() }, b => { b.loaded.value = true; b.loading.value = false })
  assert.match(empty.html, /暂无延迟探测任务/)
  assert.match(empty.html, /请先在延迟探测管理中创建任务/)
  globalThis.fetch = async () => response({ tasks: [task({ assigned: true })] })
  const failed = mountAssignmentEditor(reactive({ show: true, server: server() }))
  try {
    await flushAssignments()
    globalThis.fetch = async () => response({ error: '保存失败' }, 400)
    await failed.bindings.submit()
    assert.equal(failed.bindings.error.value, '保存失败')
    assert.deepEqual(failed.bindings.selectedIDs.value, [1])
    assert.deepEqual(failed.events, [])
    globalThis.fetch = async (_, options) => options.method === 'PUT' ? response(undefined, 204) : response({ error: '读取失败' }, 500)
    await failed.bindings.submit()
    assert.match(failed.bindings.error.value, /设置已保存，但重新加载失败/)
    assert.deepEqual(failed.events, [['saved', 1]])
  } finally { failed.unmount() }
})

test('服务器 A 到 B 及关闭重开隔离晚返回的读取和保存结果', async () => {
  const pending = []
  globalThis.fetch = (url, options) => new Promise(resolve => pending.push({ url, options, resolve }))
  const props = reactive({ show: true, server: server() })
  const scope = mountAssignmentEditor(props)
  try {
    const a = pending[0]
    props.server = server({ id: 2, name: 'B' })
    await nextTick()
    const b = pending.at(-1)
    assert.equal(a.options.signal.aborted, true)
    b.resolve(response({ tasks: [task({ id: 2, assigned: true })] }))
    await flushAssignments()
    a.resolve(response({ tasks: [task({ assigned: true })] }))
    await flushAssignments()
    assert.deepEqual(scope.bindings.selectedIDs.value, [2])
    assert.deepEqual(scope.bindings.tasks.value.map(t => t.id), [2])
    const saving = scope.bindings.submit()
    const put = pending.at(-1)
    assert.equal(put.options.method, 'PUT')
    props.show = false
    await nextTick()
    props.server = server(); props.show = true
    await nextTick()
    pending.at(-1).resolve(response({ tasks: [task({ id: 3, assigned: true })] }))
    await flushAssignments()
    put.resolve(response(undefined, 204))
    await saving
    assert.deepEqual(scope.events, [])
    assert.deepEqual(scope.bindings.selectedIDs.value, [3])
    assert.equal(scope.bindings.saving.value, false)
  } finally { scope.unmount() }
})

test('分配 composable 关闭后忽略晚返回错误，且禁止未加载或重复保存', async () => {
  const scope = effectScope()
  const state = scope.run(() => useServerProbeAssignments())
  const pending = []
  globalThis.fetch = (url, options) => new Promise((resolve, reject) => pending.push({ url, options, resolve, reject }))
  try {
    assert.equal(await state.save(1, []), false)
    const load = state.load(1)
    state.reset()
    pending[0].reject(new Error('old error'))
    await load
    assert.equal(state.error.value, '')
    const next = state.load(2)
    pending.at(-1).resolve(response({ tasks: [] }))
    await next
    const saving = state.save(2, [])
    assert.equal(await state.save(2, []), false)
    scope.stop()
    pending.at(-1).resolve(response(undefined, 204))
    assert.equal(await saving, false)
    assert.equal(state.saving.value, false)
  } finally { scope.stop() }
})
