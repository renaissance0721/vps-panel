import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, effectScope, reactive, ref } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, Detail, Form, useServers
before(async () => {
  loader = await createServer({
    configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ default: Detail } = await loader.ssrLoadModule('/src/components/server/ServerDetail.vue'))
  ;({ default: Form } = await loader.ssrLoadModule('/src/components/server/ServerBasicInfoForm.vue'))
  ;({ useServers } = await loader.ssrLoadModule('/src/composables/useServers.ts'))
})
after(async () => { await loader?.close() })

function server(overrides = {}) {
  return {
    id: 1, name: 'metadata', bandwidth_mbps: null, network_stack: null,
    bound_domain_ipv4: '', bound_domain_ipv6: '', owner_user_id: null, owner_username: '',
    status: 'online', visibility: 'public', access_user_ids: [], outbound_preference: 'auto', block_china_inbound: false,
    desired_state_version: 1, decommission_status: '', expires_at: null, renewal_period_months: null, auto_renew: false,
    last_seen_at: null, created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
    system_info: { ipv4: ['198.51.100.1'], ipv6: ['2606:4700::1111'], public_ipv4: '198.51.100.1', public_ipv6: '2606:4700::1111' },
    metrics: null, monthly_traffic_limit_bytes: null, traffic_count_mode: 'single', traffic_used_bytes: 0, traffic_reset_day: 1, traffic_reset_time: '00:00',
    agent_implementation: 'third-party', agent_api_version: 1, agent_version_status: 'up_to_date',
    agent_capabilities: ['outbound_preference', 'firewall.cn_block'], agent_applied_config_version: 1, agent_config_sync_status: 'success', agent_config_sync_error: '',
    ...overrides,
  }
}

function setup(t, record = server()) {
  const scope = effectScope()
  t.after(() => scope.stop())
  const model = reactive(scope.run(() => useServers(ref({ authenticated: true, user: { id: 1, role: 'admin' } }), ref([]), ref(null), ref(false), ref(''), async action => action())))
  model.selectedServer = record
  model.servers = [record]
  model.serverModalOpen = true
  return model
}

const render = (component, model) => renderToString(createSSRApp(component, { model }))
const buttons = html => [...html.matchAll(/<button\b([^>]*)>([^<]*)<\/button>/g)].map(([, attributes, label]) => ({ attributes, label: label.trim() }))

test('基本信息带宽/网络栈展示与真实 IPv6 状态独立，出入站使用同一宽区域', async t => {
  const model = setup(t)
  let html = await render(Detail, model)
  assert.match(html, /<dt>带宽<\/dt><dd>未设置<\/dd>/)
  assert.match(html, /<dt>网络栈<\/dt><dd>未设置<\/dd>/)
  for (const [value, label] of [['ipv4', 'IPv4 单栈'], ['ipv6', 'IPv6 单栈'], ['dual', 'IPv4 + IPv6 双栈']]) {
    model.selectedServer.network_stack = value
    model.selectedServer.bandwidth_mbps = 500
    html = await render(Detail, model)
    assert.ok(html.includes(label))
    assert.match(html, /<dt>带宽<\/dt><dd>500 Mbps<\/dd>/)
    assert.match(html, /<dt>IPv6 状态<\/dt><dd>公网 2606:4700::1111<\/dd>/)
  }
  const sections = html.split(/<section class="server-detail-section[^>]*>/).slice(1)
  assert.equal(sections.filter(section => section.includes('出入站管理')).length, 1)
  assert.ok(!sections[0].includes('当前出站'))
  const management = sections.find(section => section.includes('出入站管理'))
  for (const label of ['出站', '当前出站策略', '系统默认', '优先 IPv4', '优先 IPv6', '入站', '中国 IP 入站限制', 'SSH 和其他服务不受影响']) assert.ok(management.includes(label), label)
  assert.doesNotMatch(html, /<h3[^>]*>中国 IP 入站限制<\/h3>/)
})

test('基本信息表单回填、保存、清空与后端错误保留编辑', async t => {
  const model = setup(t)
  let stored = server()
  const requests = []
  t.mock.method(globalThis, 'fetch', async (url, options = {}) => {
    if (options.method === 'PATCH') {
      const body = JSON.parse(options.body)
      requests.push({ url, body })
      stored = { ...stored, ...body }
      return new Response(JSON.stringify({ server: stored }))
    }
    return new Response(JSON.stringify({ servers: url.includes('archived') ? [] : [stored] }))
  })
  model.openBasicInfoModal()
  assert.equal(model.bandwidthMbpsInput, null)
  assert.equal(model.networkStackInput, null)
  const html = await render(Form, model)
  for (const label of ['带宽大小', '填写 VPS 套餐标称网络带宽', '网络栈', 'IPv4 单栈', 'IPv6 单栈', 'IPv4 + IPv6 双栈']) assert.ok(html.includes(label), label)
  for (const stack of ['ipv4', 'ipv6', 'dual', null]) {
    model.openBasicInfoModal()
    model.bandwidthMbpsInput = stack === null ? null : 500
    model.networkStackInput = stack
    await model.saveBasicInfo()
    assert.equal(model.basicInfoFormError, '')
    assert.equal(model.selectedServer.network_stack, stack)
    assert.equal(model.selectedServer.bandwidth_mbps, stack === null ? null : 500)
    assert.equal(model.selectedServer.system_info.public_ipv6, '2606:4700::1111')
    assert.equal(model.basicInfoModalOpen, false)
  }
  assert.equal(requests.length, 4)
  assert.deepEqual(requests[0], { url: '/api/servers/1', body: { bandwidth_mbps: 500, network_stack: 'ipv4' } })
  assert.deepEqual(requests.at(-1).body, { bandwidth_mbps: null, network_stack: null })
  model.openBasicInfoModal()
  for (const value of [0, -1, 1.5, 10000001]) {
    model.bandwidthMbpsInput = value
    await model.saveBasicInfo()
    assert.match(model.basicInfoFormError, /带宽必须/)
  }
  assert.equal(requests.length, 4)
  t.mock.method(globalThis, 'fetch', async (_, options) => options.method === 'PATCH'
    ? new Response(JSON.stringify({ error: '保存失败' }), { status: 400 })
    : new Response(JSON.stringify({ servers: [stored] })))
  model.bandwidthMbpsInput = 500
  await model.saveBasicInfo()
  assert.equal(model.basicInfoFormError, '保存失败')
  assert.equal(model.basicInfoModalOpen, true)
})

test('出入站 capability、offline、apply/failure 与只读门禁保留', async t => {
  const model = setup(t)
  for (const [overrides, message] of [
    [{ status: 'offline', agent_config_sync_status: 'pending' }, '设置会保存，待 Agent 下次上线自动应用。'],
    [{ agent_capabilities: [] }, '当前 Agent 不支持出站 IPv4 / IPv6 偏好。'],
    [{ block_china_inbound: true, agent_config_sync_status: 'pending' }, '应用中'],
    [{ block_china_inbound: true }, '已生效'],
    [{ block_china_inbound: false }, '已关闭'],
    [{ block_china_inbound: true, agent_config_sync_status: 'failed', agent_config_sync_error: 'firewall apply failed' }, 'firewall apply failed'],
    [{ block_china_inbound: true, agent_capabilities: [] }, '无法确认'],
  ]) {
    model.selectedServer = server(overrides)
    const html = await render(Detail, model)
    assert.ok(html.includes(message), message)
    if (overrides.agent_capabilities?.length === 0) {
      for (const button of buttons(html).filter(button => /优先 IPv[46]/.test(button.label))) assert.match(button.attributes, /disabled/)
      if (!overrides.block_china_inbound) assert.match(html, /<button[^>]*disabled[^>]*role="switch"/)
    }
  }
  for (const overrides of [{ archived_at: '2026-10-01' }, { decommission_status: 'pending' }, { decommission_status: 'failed' }]) {
    model.selectedServer = server(overrides)
    const html = await render(Detail, model)
    for (const button of buttons(html).filter(button => ['系统默认', '优先 IPv4', '优先 IPv6'].includes(button.label))) assert.match(button.attributes, /disabled/)
    assert.match(html, /<button[^>]*disabled[^>]*role="switch"/)
  }
})

test('三个出站策略和中国入站开关沿用当前 PATCH payload', async t => {
  const model = setup(t, server({ status: 'offline' }))
  const previousWindow = globalThis.window
  globalThis.window = { confirm: () => true, location: { origin: 'http://localhost' } }
  t.after(() => { globalThis.window = previousWindow })
  let stored = { ...model.selectedServer }
  const requests = []
  t.mock.method(globalThis, 'fetch', async (url, options = {}) => {
    if (options.method === 'PATCH') {
      const body = JSON.parse(options.body)
      requests.push({ url, body })
      stored = { ...stored, ...body }
      return new Response(JSON.stringify({ server: stored }))
    }
    return new Response(JSON.stringify({ servers: url.includes('archived') ? [] : [stored] }))
  })
  for (const value of ['prefer_ipv4', 'prefer_ipv6', 'auto']) await model.setOutboundPreference(model.selectedServer, value)
  await model.setBlockChinaInbound(model.selectedServer, true)
  await model.setBlockChinaInbound(model.selectedServer, false)
  assert.deepEqual(requests.map(value => value.body), [
    { outbound_preference: 'prefer_ipv4' }, { outbound_preference: 'prefer_ipv6' }, { outbound_preference: 'auto' },
    { block_china_inbound: true }, { block_china_inbound: false },
  ])
  assert.ok(requests.every(value => value.url === '/api/servers/1'))
})
