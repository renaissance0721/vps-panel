import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, reactive, ref } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader
let useServers, useProxyForm, useClientForm, useProxies, api, APIError
const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')

before(async () => {
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { location: { origin: 'https://panel.example.com' }, confirm: () => true } })
  loader = await createServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false },
    optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ useServers } = await loader.ssrLoadModule('/src/composables/useServers.ts'))
  ;({ useProxyForm } = await loader.ssrLoadModule('/src/composables/useProxyForm.ts'))
  ;({ useClientForm } = await loader.ssrLoadModule('/src/composables/useClientForm.ts'))
  ;({ useProxies } = await loader.ssrLoadModule('/src/composables/useProxies.ts'))
  ;({ api, APIError } = await loader.ssrLoadModule('/src/api/client.ts'))
})
after(async () => {
  await loader?.close()
  if (originalWindow) Object.defineProperty(globalThis, 'window', originalWindow)
  else delete globalThis.window
})

function serverRecord(overrides = {}) {
  return {
    id: 7, name: '测试服务器', status: 'pending', visibility: 'public', access_user_ids: [],
    archived_at: null, expires_at: null, outbound_preference: 'auto', monthly_traffic_limit_bytes: null,
    traffic_count_mode: 'single', traffic_reset_day: 1, traffic_reset_time: '00:00',
    traffic_used_bytes: 0, last_seen_at: null, system_info: null, metrics: null,
    created_at: '2026-09-01T00:00:00Z', updated_at: '2026-09-01T00:00:00Z',
    agent_version: '', agent_version_status: 'unregistered', ...overrides,
  }
}

function serverModel() {
  const state = ref({ authenticated: true, user: { id: 1, username: 'admin', role: 'admin' } })
  const error = ref(''), submitting = ref(false)
  const model = useServers(state, ref([]), ref({ status: 'ok', database: 'ok', version: 'v0.20.0' }), submitting, error, async action => {
    submitting.value = true
    try { await action() } finally { submitting.value = false }
  })
  return { model, error, submitting }
}

async function render(path, model, extra = {}) {
  const { default: component } = await loader.ssrLoadModule('/src/' + path)
  return renderToString(createSSRApp(component, { model: reactive(model), ...extra }))
}

function json(value, status = 200) { return new Response(JSON.stringify(value), { status }) }

test('共享 HTTP 客户端保持 Cookie、no-store、JSON、204 和错误语义', async t => {
  let options
  t.mock.method(globalThis, 'fetch', async (_url, init) => { options = init; return json({ ok: true }) })
  assert.deepEqual(await api('/api/servers', { method: 'POST', body: '{}' }), { ok: true })
  assert.equal(options.credentials, 'same-origin')
  assert.equal(options.cache, 'no-store')
  assert.equal(options.headers['Content-Type'], 'application/json')
  globalThis.fetch = async () => new Response(null, { status: 204 })
  assert.equal(await api('/api/servers/7'), undefined)
  globalThis.fetch = async () => json({ error: '无权访问' }, 404)
  await assert.rejects(api('/api/servers/7'), e => e instanceof APIError && e.status === 404 && e.message === '无权访问')
})

test('Server 创建保留私有账号绑定，详情关闭后清除一次性命令', async t => {
  const created = { server: serverRecord(), enrollment_token: 'test-only', enrollment_token_expires_at: '2026-09-18T00:00:00Z', agent_installation_command: 'sh install-agent.sh --token test-only' }
  const calls = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push({ url, init })
    return init?.method === 'POST' ? json(created) : json({ servers: url.includes('?') ? [] : [created.server] })
  })
  const { model, submitting } = serverModel()
  model.serverName.value = '测试服务器'
  model.serverVisibility.value = 'private'
  model.serverAccessUserIDs.value = [2]
  await model.createServerRecord()
  assert.deepEqual(JSON.parse(calls[0].init.body), { name: '测试服务器', visibility: 'private', user_ids: [2, 1] })
  assert.equal(model.serverModalOpen.value, true)
  assert.equal(model.createdServer.value.agent_installation_command, created.agent_installation_command)
  assert.equal(submitting.value, false)
  model.closeServerDetails()
  assert.equal(model.createdServer.value, null)
  assert.equal(model.selectedServer.value, null)
})

test('Server 刷新失去访问权限时关闭关联弹窗，不恢复原始令牌', async t => {
  t.mock.method(globalThis, 'fetch', async () => json({ servers: [] }))
  const { model, error } = serverModel()
  model.viewServer(serverRecord())
  model.createdServer.value = { enrollment_token: 'once' }
  model.accessModalOpen.value = model.trafficModalOpen.value = model.trafficAdjustmentModalOpen.value = true
  await model.loadServers()
  for (const key of ['serverModalOpen', 'accessModalOpen', 'trafficModalOpen', 'trafficAdjustmentModalOpen']) assert.equal(model[key].value, false)
  assert.equal(model.createdServer.value, null)
  assert.equal(model.selectedServer.value, null)
  assert.equal(error.value, '服务器不存在或当前账号无权访问')
})

test('拆分后的 Server 列表与月流量表单实际渲染到期日期和 Modal 内错误', async () => {
  const { model } = serverModel()
  model.servers.value = [serverRecord({ expires_at: '2026-12-31T15:59:59Z' }), serverRecord({ id: 8 })]
  const list = await render('components/server/ServerList.vue', model)
  assert.match(list, /到期时间/)
  assert.match(list, /2026-12-31/)
  assert.match(list, /不限/)
  assert.doesNotMatch(list, /服务器 ID/)
  model.selectedServer.value = model.servers.value[0]
  model.openTrafficModal()
  model.trafficLimitInput.value = -1
  await model.saveTrafficConfig()
  const form = await render('components/server/ServerTrafficForm.vue', model)
  assert.match(form, /月流量额度格式无效/)
  assert.match(form, /type="number"/)
})

test('服务器名称保存后详情保持打开且列表使用新名称', async t => {
  const updated = serverRecord({ name: '新名称' })
  const calls = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push({ url, init })
    if (init?.method === 'PATCH') return json({ server: updated })
    return json({ servers: url.includes('?archived=true') ? [] : [updated] })
  })
  const { model } = serverModel()
  model.viewServer(serverRecord())
  model.openNameModal()
  assert.equal(model.nameInput.value, '测试服务器')
  model.nameInput.value = '  新名称  '
  await model.saveServerName()
  assert.deepEqual(JSON.parse(calls[0].init.body), { name: '新名称' })
  assert.equal(model.selectedServer.value.name, '新名称')
  assert.equal(model.servers.value[0].name, '新名称')
  assert.equal(model.serverModalOpen.value, true)
  assert.equal(model.nameModalOpen.value, false)
  const detail = await render('components/server/ServerDetail.vue', model)
  assert.match(detail, /server-detail-grid/)
  for (const title of ['基本信息', 'Agent', '系统信息', '动态指标', '月流量']) assert.match(detail, new RegExp(title))
})

test('出站优先级按钮高亮当前设置，归档时禁用', async () => {
  const { model } = serverModel()
  for (const [preference, label] of [['auto', '系统默认'], ['prefer_ipv4', '优先 IPv4'], ['prefer_ipv6', '优先 IPv6']]) {
    model.viewServer(serverRecord({ outbound_preference: preference, status: 'offline' }))
    const detail = await render('components/server/ServerDetail.vue', model)
    assert.match(detail, new RegExp(`<button[^>]*type="primary"[^>]*>${label}</button>`))
    assert.match(detail, /设置会保存，待 Agent 下次上线自动应用。/)
  }
  model.viewServer(serverRecord({ archived_at: '2026-09-18T00:00:00Z', outbound_preference: 'prefer_ipv6' }))
  const archived = await render('components/server/ServerDetail.vue', model)
  assert.match(archived, /<button[^>]*disabled[^>]*>优先 IPv6<\/button>/)
})

test('切换出站优先级发送单项 PATCH 并保持详情打开', async t => {
  let current = serverRecord({ status: 'online' })
  const calls = []
  const prompts = []
  const originalConfirm = window.confirm
  let approved = false
  window.confirm = message => { prompts.push(message); return approved }
  t.after(() => { window.confirm = originalConfirm })
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    if (init?.method === 'PATCH') {
      calls.push({ url, body: JSON.parse(init.body) })
      current = serverRecord({ ...current, outbound_preference: calls.at(-1).body.outbound_preference })
      return json({ server: current })
    }
    return json({ servers: url.includes('?archived=true') ? [] : [current] })
  })
  const { model, submitting } = serverModel()
  model.viewServer(current)
  await model.setOutboundPreference(current, 'auto')
  assert.equal(calls.length, 0)
  assert.equal(prompts.length, 0)
  await model.setOutboundPreference(current, 'prefer_ipv4')
  assert.equal(calls.length, 0)
  assert.equal(prompts[0], '切换出站 IP 优先级会重新应用 Xray 配置，现有代理连接可能短暂中断。是否继续？')
  approved = true
  for (const preference of ['prefer_ipv4', 'prefer_ipv6']) {
    await model.setOutboundPreference(model.selectedServer.value, preference)
    assert.deepEqual(calls.at(-1), { url: '/api/servers/7', body: { outbound_preference: preference } })
    assert.equal(model.selectedServer.value.outbound_preference, preference)
    assert.equal(model.servers.value[0].outbound_preference, preference)
    assert.equal(model.serverModalOpen.value, true)
    assert.equal(submitting.value, false)
  }
  const count = calls.length
  await model.setOutboundPreference(model.selectedServer.value, 'prefer_ipv6')
  await model.setOutboundPreference(serverRecord({ archived_at: '2026-09-18T00:00:00Z' }), 'auto')
  assert.equal(calls.length, count)
})

test('Proxy 表单拆分保持 ACME 默认、manual 回填和原始提交字段', async t => {
  const calls = []
  const selected = ref(null), error = ref('')
  t.mock.method(globalThis, 'fetch', async (url, init) => { calls.push({ url, body: JSON.parse(init.body) }); return json({ proxy: { id: 4 } }) })
  let shown
  const form = useProxyForm({ servers: [{ id: 7, name: 'VPS', system_info: null }] }, selected, error, action => action(), async () => {}, async id => { shown = id })
  form.openCreateProxy()
  form.proxyName.value = 'TLS'
  form.proxyServerName.value = 'node.example.com'
  form.proxySecurity.value = 'tls'
  await form.saveProxy()
  assert.equal(calls[0].url, '/api/proxies')
  assert.equal(calls[0].body.tls_mode, 'acme')
  assert.equal(calls[0].body.first_client_name, '默认客户端')
  assert.equal('certificate' in calls[0].body, false)
  assert.equal('private_key' in calls[0].body, false)
  assert.equal(form.proxyFormOpen.value, false)
  assert.equal(shown, 4)
  form.openEditProxy({ id: 4, name: 'legacy', server_id: 7, listen_port: 443, entry_host_mode: 'auto', entry_host: '', enabled: true, protocol: 'vless', config: { security: 'tls', server_name: 'node.example.com', tls_certificate_configured: true } })
  assert.equal(form.proxyTLSMode.value, 'manual')
  form.proxyCertificate.value = 'test-certificate'
  form.proxyPrivateKey.value = 'test-key'
  await form.saveProxy()
  assert.equal(calls[1].url, '/api/proxies/4')
  assert.equal(calls[1].body.certificate, 'test-certificate')
  assert.equal(calls[1].body.private_key, 'test-key')
})

test('Client 表单拆分保持数值额度、周期、到期和协议专属 UDP/443', async t => {
  const calls = []
  t.mock.method(globalThis, 'fetch', async (url, init) => { calls.push({ url, body: JSON.parse(init.body) }); return json({}) })
  const selected = ref({ id: 3, protocol: 'vless' }), error = ref('')
  const form = useClientForm(selected, error, action => action(), async () => {}, async () => {})
  form.openCreateClient()
  form.clientName.value = '手机'
  form.clientTrafficLimit.value = 500
  form.clientTrafficResetMode.value = 'monthly'
  form.clientTrafficResetDay.value = 31
  form.clientExpirationMode.value = 'specified'
  form.clientExpiresAt.value = '2026-12-31T23:59'
  form.clientUDP443.value = true
  await form.saveClient()
  assert.deepEqual(calls[0], { url: '/api/proxies/3/clients', body: { name: '手机', enabled: true, client_udp443: true, traffic_limit: 500, limit_unit: 'G', traffic_reset_mode: 'monthly', traffic_reset_weekday: 1, traffic_reset_day: 31, traffic_reset_time: '00:00', expires_at: '2026-12-31T23:59' } })
  assert.equal(form.clientFormOpen.value, false)
  selected.value.protocol = 'shadowsocks'
  await form.saveClient()
  assert.equal(calls[1].body.client_udp443, false)
})

test('Proxy Client 列表实际渲染不含凭据，两个协议仍复制后端 URI', async t => {
  let model, copied, expectedURI
  await renderToString(createSSRApp({ setup() { model = useProxies({ servers: [] }); return () => null } }))
  t.mock.method(globalThis, 'fetch', async () => json({ share: { uri: expectedURI } }))
  const original = Object.getOwnPropertyDescriptor(globalThis, 'navigator')
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { clipboard: { writeText: async value => { copied = value } } } })
  t.after(() => {
    if (original) Object.defineProperty(globalThis, 'navigator', original)
    else delete globalThis.navigator
  })
  const client = { id: 2, name: '手机', status: 'normal', enabled: true, client_udp443: true, traffic_limit_bytes: null, traffic_reset_mode: 'never', traffic_reset_weekday: 1, traffic_reset_day: 1, traffic_reset_time: '00:00', expires_at: null, metrics: null }
  for (const protocol of ['vless', 'shadowsocks']) {
    model.selectedProxy.value = { id: 3, protocol, clients: [client] }
    const output = await render('components/proxy/ClientList.vue', model)
    assert.match(output, /已用 \/ 总量/)
    assert.match(output, /周期/)
    assert.match(output, /到期时间/)
    assert.match(output, /最近活动/)
    assert.doesNotMatch(output, /UUID|uuid_summary|password/)
    assert.equal(output.includes('UDP/443'), protocol === 'vless')
    expectedURI = protocol === 'vless' ? 'vless://test@node.example.com:443' : 'ss://test@node.example.com:443'
    await model.copyClientURI(client)
    assert.equal(copied, expectedURI)
    assert.equal(model.copiedClientID.value, 2)
  }
})

test('Proxy 客户端二维码点击后读取现有分享 URI，详情复用已加载的 URI，关闭清空状态', async t => {
  let model
  await renderToString(createSSRApp({ setup() { model = useProxies({ servers: [] }); return () => null } }))
  const calls = []
  let share
  t.mock.method(globalThis, 'fetch', async url => {
    calls.push(url)
    return json({ share })
  })
  const client = { id: 2, name: '手机', status: 'normal', enabled: true, client_udp443: false,
    traffic_limit_bytes: null, traffic_reset_mode: 'never', traffic_reset_weekday: 1,
    traffic_reset_day: 1, traffic_reset_time: '00:00', expires_at: null, metrics: null }
  assert.equal(model.qrOpen.value, false)
  assert.equal(model.qrURI.value, '')
  for (const [protocol, uri, subtitle] of [
    ['vless', 'vless://uuid@node.example.com:443?security=reality&fp=chrome#中文%20节点', 'VLESS'],
    ['shadowsocks', 'ss://password@node.example.com:8388#中文%20节点', 'Shadowsocks 2022'],
  ]) {
    share = { client, proxy_name: '日本节点', protocol, uri }
    model.selectedProxy.value = { id: 3, name: '日本节点', protocol, clients: [client] }
    const list = await render('components/proxy/ClientList.vue', model)
    assert.match(list, /<button[^>]*>二维码<\/button>/)
    assert.equal(model.qrOpen.value, false)
    await model.showClientQRCode(client)
    assert.equal(calls.at(-1), '/api/clients/2/share')
    assert.equal(model.qrURI.value, uri)
    assert.equal(model.qrTitle.value, '日本节点 - 手机')
    assert.equal(model.qrSubtitle.value, subtitle)
    assert.equal(model.qrOpen.value, true)
    model.setQRCodeOpen(false)
    assert.equal(model.qrOpen.value, false)
    assert.equal(model.qrURI.value, '')
    assert.equal(model.qrTitle.value, '')
    assert.equal(model.qrSubtitle.value, '')
    model.selectedShare.value = share
    model.clientDetailOpen.value = true
    const detail = await render('components/proxy/ClientDetail.vue', model)
    assert.match(detail, /<button[^>]*>二维码<\/button>/)
    const requestCount = calls.length
    model.showSelectedShareQRCode()
    assert.equal(calls.length, requestCount)
    assert.equal(model.qrURI.value, uri)
    model.setQRCodeOpen(false)
  }
})
