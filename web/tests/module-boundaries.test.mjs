import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, nextTick, reactive, ref } from 'vue'
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
    id: 7, name: '测试服务器', bound_domain_ipv4: '', bound_domain_ipv6: '', status: 'pending', visibility: 'public', access_user_ids: [],
    owner_user_id: 1, owner_username: 'admin',
    archived_at: null, expires_at: null, renewal_period_months: null, auto_renew: false,
    decommissioning_at: null, decommission_status: '', decommission_error: '',
    outbound_preference: 'auto', block_china_inbound: false,
    desired_state_version: 1, monthly_traffic_limit_bytes: null,
    traffic_count_mode: 'single', traffic_reset_day: 1, traffic_reset_time: '00:00',
    traffic_used_bytes: 0, last_seen_at: null, system_info: null, metrics: null,
    created_at: '2026-09-01T00:00:00Z', updated_at: '2026-09-01T00:00:00Z',
    agent_implementation: '', agent_version: '', agent_api_version: 0, agent_capabilities: [],
    agent_version_status: 'unregistered', agent_applied_config_version: 0,
    agent_config_sync_status: '', agent_config_sync_error: '', agent_config_synced_at: null,
    ...overrides,
  }
}

function serverModel(users = ref([])) {
  const state = ref({ authenticated: true, user: { id: 1, username: 'admin', role: 'admin' } })
  const error = ref(''), submitting = ref(false)
  const model = useServers(state, users, ref({ status: 'ok', database: 'ok', version: 'v0.20.0' }), submitting, error, async action => {
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

test('Server 私有访问仅渲染管理账号，保持当前账号及独立所有者选项', async () => {
  const users = ref([
    { id: 2, username: 'bob', role: 'vip' },
    { id: 3, username: 'dave', role: 'user' },
    { id: 1, username: 'alice', role: 'admin' },
    { id: 4, username: 'eve', role: 'subscriber' },
  ])
  const { model } = serverModel(users)
  assert.deepEqual(model.serverAccessUsers.value.map(user => user.id), [1, 2])
  assert.deepEqual(model.orderedUsers.value.map(user => user.id), [1, 2, 3, 4])
  assert.deepEqual(users.value.map(user => user.id), [2, 3, 1, 4])
  model.openCreateServerModal()
  model.createServerVisibility.value = 'private'
  model.createServerAccessUserIDs.value = [2]
  model.ensureCreateCurrentUser()
  assert.deepEqual(model.createServerAccessUserIDs.value, [2, 1])
  model.viewServer(serverRecord({ visibility: 'private', access_user_ids: [1, 2] }))
  model.openBasicInfoModal()
  model.accessUserIDs.value = [2]
  model.ensureAccessCurrentUser()
  assert.deepEqual(model.accessUserIDs.value, [2, 1])

  for (const path of ['components/server/ServerForm.vue', 'components/server/ServerBasicInfoForm.vue']) {
    const html = await render(path, model)
    assert.match(html, /公开（所有管理账号）/)
    const access = html.match(/<fieldset\b[^>]*class="server-access-users"[\s\S]*?<\/fieldset>/)?.[0]
    assert.ok(access, 'private access fieldset is rendered')
    assert.match(access, /允许访问的管理账号/)
    assert.match(access, /alice/)
    assert.match(access, /bob/)
    assert.doesNotMatch(access, /dave|eve/)
    assert.equal((access.match(/type="checkbox"/g) ?? []).length, 2)
    const current = access.match(/<input[^>]*value="1"[^>]*>/)?.[0]
    assert.match(current, /checked/)
    assert.match(current, /disabled/)
    assert.ok(access.indexOf('alice') < access.indexOf('bob'))
    if (path.includes('ServerBasicInfoForm')) {
      const owner = html.match(/<select[^>]*>[\s\S]*?<\/select>/)?.[0]
      assert.match(owner, /dave/)
      assert.match(owner, /eve/)
    }
  }
  users.value.push({ id: 5, username: 'carol', role: 'vip' })
  assert.deepEqual(model.serverAccessUsers.value.map(user => user.id), [1, 2, 5])
})

function bulkUpgradeServer(id, overrides = {}) {
  return serverRecord({
    id,
    name: `服务器 ${id}`,
    status: 'online',
    agent_can_self_upgrade: true,
    agent_version: 'v0.19.0',
    agent_version_status: 'upgrade_available',
    ...overrides,
  })
}

function bulkUpgradeItem(server, overrides = {}) {
  return {
    serverId: server.id,
    serverName: server.name,
    fromVersion: server.agent_version,
    targetVersion: 'v0.20.0',
    status: 'upgrading',
    error: '',
    startedAt: 100,
    ...overrides,
  }
}

function flushPromises() {
  return new Promise(resolve => setTimeout(resolve, 0))
}

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

test('Server 创建一次提交管理字段，成功后打开安装命令并重置 Modal', async t => {
  const created = { server: serverRecord(), enrollment_token: 'test-only', enrollment_token_expires_at: '2026-09-18T00:00:00Z', agent_installation_command: 'sh install-agent.sh --token test-only' }
  const calls = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push({ url, init })
    return init?.method === 'POST' ? json(created) : json({ servers: url.includes('?') ? [] : [created.server] })
  })
  const { model, submitting } = serverModel()
  model.openCreateServerModal()
  model.createServerName.value = '测试服务器'
  model.createServerBoundDomainIPv4.value = 'node.example.com'
  model.createServerVisibility.value = 'private'
  model.createServerAccessUserIDs.value = [2]
  model.createServerExpiration.value = '2027-04-03'
  model.createServerRenewalPeriod.value = 12
  model.createServerAutoRenew.value = true
  model.createServerTrafficLimit.value = '500'
  model.createServerTrafficUnit.value = 'G'
  model.createServerTrafficCountMode.value = 'bidirectional'
  model.createServerTrafficResetDay.value = 31
  model.createServerTrafficResetTime.value = '08:30'
  await model.createServerRecord()
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    name: '测试服务器', bound_domain_ipv4: 'node.example.com', bound_domain_ipv6: '', visibility: 'private', user_ids: [2, 1],
    expires_at: '2027-04-03', renewal_period_months: 12, auto_renew: true,
    monthly_traffic_limit_bytes: 500 * 1024 ** 3, traffic_count_mode: 'bidirectional',
    traffic_reset_day: 31, traffic_reset_time: '08:30',
  })
  assert.equal(model.createServerModalOpen.value, false)
  assert.equal(model.serverModalOpen.value, true)
  assert.equal(model.createdServer.value.agent_installation_command, created.agent_installation_command)
  assert.equal(model.createServerName.value, '')
  assert.equal(model.createServerBoundDomainIPv4.value, '')
  assert.equal(model.createServerTrafficLimit.value, '')
  assert.equal(submitting.value, false)
  model.closeServerDetails()
  assert.equal(model.createdServer.value, null)
  assert.equal(model.selectedServer.value, null)
})

test('Server 新增入口只在正常列表标题显示，Modal 字段完整且每次打开重置', async () => {
  const { model } = serverModel(ref([
    { id: 1, username: 'alice', role: 'admin' },
    { id: 2, username: 'bob', role: 'vip' },
  ]))
  let page = await render('views/ServersView.vue', model, { active: true })
  assert.equal((page.match(/新增服务器/g) ?? []).length, 1)
  assert.doesNotMatch(page, /基本信息[\s\S]*访问控制[\s\S]*到期与续费/)
  model.serverListMode.value = 'archived'
  await nextTick()
  page = await render('views/ServersView.vue', model, { active: true })
  assert.doesNotMatch(page, /新增服务器/)

  model.serverListMode.value = 'active'
  model.openCreateServerModal()
  assert.equal(model.createServerModalOpen.value, true)
  page = await render('components/server/ServerForm.vue', model)
  for (const label of ['名称', '访问范围', '到期日期', '续费周期', '自动续费', '月流量额度', '流量统计方式', '重置日期', '重置时间']) {
    assert.match(page, new RegExp(label))
  }
  assert.match(page, /disabled[^>]*role="switch"|role="switch"[^>]*disabled/)
  model.createServerName.value = '残留名称'
  model.createServerExpiration.value = '2027-04-03'
  model.createServerRenewalPeriod.value = 12
  model.createServerAutoRenew.value = true
  model.createServerExpiration.value = ''
  await nextTick()
  assert.equal(model.createServerAutoRenew.value, false)
  model.closeCreateServerModal()
  model.openCreateServerModal()
  assert.equal(model.createServerName.value, '')
  assert.equal(model.createServerVisibility.value, 'public')
  assert.equal(model.createServerTrafficResetDay.value, 1)
  assert.equal(model.createServerTrafficResetTime.value, '00:00')
})

test('Server 刷新失去访问权限时关闭关联弹窗，不恢复原始令牌', async t => {
  t.mock.method(globalThis, 'fetch', async () => json({ servers: [] }))
  const { model, error } = serverModel()
  model.viewServer(serverRecord())
  model.createdServer.value = { enrollment_token: 'once' }
  model.basicInfoModalOpen.value = model.trafficModalOpen.value = model.trafficAdjustmentModalOpen.value = true
  await model.loadServers()
  for (const key of ['serverModalOpen', 'basicInfoModalOpen', 'trafficModalOpen', 'trafficAdjustmentModalOpen']) assert.equal(model[key].value, false)
  assert.equal(model.createdServer.value, null)
  assert.equal(model.selectedServer.value, null)
  assert.equal(error.value, '服务器不存在或当前账号无权访问')
})

test('拆分后的 Server 列表与月流量表单实际渲染到期日期和 Modal 内错误', async () => {
  const { model } = serverModel()
  model.servers.value = [
    serverRecord({ bound_domain_ipv4: 'core.example.com', expires_at: '2026-12-31T15:59:59Z', owner_username: 'refrain' }),
    serverRecord({ id: 8, owner_user_id: null, owner_username: '' }),
  ]
  const list = await render('components/server/ServerList.vue', model)
  assert.match(list, /到期时间/)
  assert.match(list, /所有者/)
  assert.match(list, /refrain/)
  assert.match(list, /core\.example\.com/)
  assert.equal((list.match(/server-bound-domain/g) ?? []).length, 1)
  assert.doesNotMatch(list, /未设置/)
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

test('Server 统一基本信息表单初始化、保存续费设置并保持列表摘要紧凑', async t => {
  const initial = serverRecord({
    expires_at: '2026-10-31T15:59:59Z', renewal_period_months: 1, auto_renew: false,
  })
  const updated = { ...initial, auto_renew: true }
  const calls = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push({ url, init })
    if (init?.method === 'PATCH') return json({ server: updated })
    return json({ servers: url.includes('?archived=true') ? [] : [updated] })
  })
  const { model } = serverModel()
  model.servers.value = [initial]
  model.viewServer(initial)
  model.openBasicInfoModal()
  assert.equal(model.expirationInput.value, '2026-10-31')
  assert.equal(model.renewalPeriodInput.value, 1)
  assert.equal(model.autoRenewInput.value, false)

  let form = await render('components/server/ServerBasicInfoForm.vue', model)
  for (const label of ['不设置', '月付', '季付', '半年付', '年付', '两年付', '三年付']) assert.match(form, new RegExp(label))
  assert.match(form, /自动续费/)
  assert.match(form, /不会向 VPS 商家付款/)

  model.autoRenewInput.value = true
  await model.saveBasicInfo()
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    expires_at: '2026-10-31', renewal_period_months: 1, auto_renew: true,
  })

  model.servers.value = [updated]
  const list = await render('components/server/ServerList.vue', model)
  assert.match(list, /2026-10-31/)
  assert.match(list, /月付[\s\S]*?· 自动续费/)
  assert.equal((list.match(/<th(?:\s|>)/g) ?? []).length, 7)

  model.viewServer(serverRecord())
  model.openBasicInfoModal()
  form = await render('components/server/ServerBasicInfoForm.vue', model)
  assert.match(form, /<button[^>]*disabled[^>]*role="switch"/)
})

test('Server 绑定域名在基本信息中独立 PATCH 并立即刷新详情', async t => {
  const initial = serverRecord({ bound_domain_ipv4: 'old.example.com', bound_domain_ipv6: '' })
  const updated = { ...initial, bound_domain_ipv4: 'new.example.com' }
  const calls = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    if (init?.method === 'PATCH') {
      calls.push({ url, body: JSON.parse(init.body) })
      return json({ server: updated })
    }
    return json({ servers: url.includes('?archived=true') ? [] : [updated] })
  })
  const { model } = serverModel()
  model.servers.value = [initial]
  model.viewServer(initial)
  model.openBasicInfoModal()
  assert.equal(model.boundDomainIPv4Input.value, 'old.example.com')
  model.boundDomainIPv4Input.value = ' new.example.com '
  await model.saveBasicInfo()
  assert.deepEqual(calls, [{ url: '/api/servers/7', body: { bound_domain_ipv4: 'new.example.com', bound_domain_ipv6: '' } }])
  assert.equal(model.selectedServer.value.bound_domain_ipv4, 'new.example.com')
  assert.equal(model.serverModalOpen.value, true)
})

test('已上报但无公网 IPv6 的 Server 拒绝新增 IPv6 绑定域名', async t => {
  const current = serverRecord({
    system_info: { hostname: 'host', ipv4: [], ipv6: ['fd00::1'], public_ipv4: '198.51.100.7', public_ipv6: '' },
  })
  const fetch = t.mock.method(globalThis, 'fetch', async () => { throw new Error('不应发送请求') })
  const { model } = serverModel()
  model.servers.value = [current]
  model.viewServer(current)
  model.openBasicInfoModal()
  model.nameInput.value = '不应提前修改'
  model.boundDomainIPv6Input.value = 'v6.example.com'
  await model.saveBasicInfo()
  assert.equal(fetch.mock.calls.length, 0)
  assert.match(model.basicInfoFormError.value, /未检测到可用公网 IPv6/)
})

test('Server 详情基本信息只读且归档状态不显示统一修改入口', async () => {
  const active = serverRecord({
    expires_at: '2026-10-31T15:59:59Z', renewal_period_months: 1, auto_renew: false,
  })
  const { model } = serverModel()
  model.servers.value = [active]
  model.viewServer(active)
  let detail = await render('components/server/ServerDetail.vue', model)
  assert.match(detail, /自动续费/)
  assert.match(detail, /已关闭/)
  assert.match(detail, /> 修改 <\/button>/)
  assert.doesNotMatch(detail, /aria-label="修改(?:名称|所有者|访问范围|到期日期)"/)

  const archived = { ...active, archived_at: '2026-11-01T00:00:00Z' }
  model.viewServer(archived)
  detail = await render('components/server/ServerDetail.vue', model)
  assert.match(detail, /续费周期/)
  assert.doesNotMatch(detail, /openBasicInfoModal/)
})

test('批量升级入口仅管理员可见，开发版本入口禁用且不能启动', async t => {
  const { model } = serverModel()
  model.servers.value = [bulkUpgradeServer(7)]
  let output = await render('views/ServersView.vue', model, { active: true })
  assert.match(output, /一键升级 Agent（1）/)

  model.state.value.user.role = 'vip'
  output = await render('views/ServersView.vue', model, { active: true })
  assert.doesNotMatch(output, /一键升级 Agent/)

  model.state.value.user.role = 'admin'
  model.health.value = { status: 'ok', database: 'ok', version: 'dev' }
  output = await render('views/ServersView.vue', model, { active: true })
  const devButton = output.match(/<button[^>]*disabled[^>]*>[\s\S]*?开发版本不可批量升级[\s\S]*?<\/button>/)?.[0] ?? ''
  assert.match(devButton, /disabled/)
  t.mock.method(globalThis, 'fetch', async () => { throw new Error('开发版不应发送请求') })
  await model.startBulkAgentUpgrade()
  assert.equal(model.bulkUpgradeItems.value.length, 0)
})

test('批量升级固定最多并发三台，already_current 成功且单台 POST 失败不终止队列', async t => {
  const servers = [7, 8, 9, 10, 11].map(bulkUpgradeServer)
  const pending = new Map()
  const postIDs = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    if (init?.method === 'POST') {
      const id = Number(url.match(/servers\/(\d+)/)?.[1])
      postIDs.push(id)
      return await new Promise(resolve => pending.set(id, resolve))
    }
    return json({ servers: url.includes('?archived=true') ? [] : servers })
  })
  const { model } = serverModel()
  await model.startBulkAgentUpgrade()
  assert.deepEqual(postIDs, [7, 8, 9])
  assert.equal(model.bulkUpgradeItems.value.filter(item => item.status === 'starting').length, 3)
  assert.equal(model.bulkUpgradeItems.value.filter(item => item.status === 'waiting').length, 2)

  pending.get(8)(json({ error: 'Agent 已离线' }, 409))
  pending.get(9)(json({ status: 'already_current', version: 'v0.20.0' }))
  await flushPromises()
  assert.deepEqual(postIDs, [7, 8, 9, 10, 11])
  assert.equal(model.bulkUpgradeItems.value.find(item => item.serverId === 8).status, 'failed')
  assert.equal(model.bulkUpgradeItems.value.find(item => item.serverId === 8).error, 'Agent 已离线')
  assert.equal(model.bulkUpgradeItems.value.find(item => item.serverId === 9).status, 'success')
  assert.equal(model.bulkUpgradeItems.value.filter(item => ['starting', 'upgrading'].includes(item.status)).length, 3)

  for (const id of [7, 10, 11]) pending.get(id)(json({ status: 'upgrading', version: 'v0.20.0' }, 202))
  await flushPromises()
  assert.equal(model.bulkUpgradeItems.value.filter(item => item.status === 'upgrading').length, 3)
  model.resetSession()
})

test('批量轮询根据服务端真实状态确认成功、失败、消失和超时，升级中离线继续等待', () => {
  const { model } = serverModel()
  const server = bulkUpgradeServer(7, { status: 'offline', agent_upgrade_status: 'upgrading' })
  model.bulkUpgradePhase.value = 'running'
  model.bulkUpgradeItems.value = [bulkUpgradeItem(server)]
  model.servers.value = [server]
  model.reconcileBulkAgentUpgrade(1_000)
  assert.equal(model.bulkUpgradeItems.value[0].status, 'upgrading')

  model.servers.value = [bulkUpgradeServer(7, {
    agent_version: 'v0.20.0',
    agent_version_status: 'up_to_date',
  })]
  model.reconcileBulkAgentUpgrade(2_000)
  assert.equal(model.bulkUpgradeItems.value[0].status, 'success')
  assert.equal(model.bulkUpgradePhase.value, 'done')

  model.resetBulkAgentUpgrade()
  const failed = bulkUpgradeServer(8, {
    name: '德国',
    agent_upgrade_status: 'failed',
    agent_upgrade_error: 'download Agent upgrade: context deadline exceeded',
  })
  model.bulkUpgradePhase.value = 'running'
  model.bulkUpgradeItems.value = [bulkUpgradeItem(failed)]
  model.servers.value = [failed]
  model.reconcileBulkAgentUpgrade(2_000)
  assert.equal(model.bulkUpgradeItems.value[0].status, 'failed')
  assert.equal(model.bulkUpgradeItems.value[0].error, 'download Agent upgrade: context deadline exceeded')

  model.resetBulkAgentUpgrade()
  model.bulkUpgradePhase.value = 'running'
  model.bulkUpgradeItems.value = [bulkUpgradeItem(bulkUpgradeServer(9, { name: '已删除' }))]
  model.servers.value = []
  model.reconcileBulkAgentUpgrade(2_000)
  assert.equal(model.bulkUpgradeItems.value[0].error, '服务器已不存在或当前账号无权访问')

  model.resetBulkAgentUpgrade()
  const slow = bulkUpgradeServer(10, { status: 'offline', agent_upgrade_status: 'upgrading' })
  model.bulkUpgradePhase.value = 'running'
  model.bulkUpgradeItems.value = [bulkUpgradeItem(slow)]
  model.servers.value = [slow]
  model.reconcileBulkAgentUpgrade(180_100)
  assert.equal(model.bulkUpgradeItems.value[0].status, 'timeout')
  assert.equal(model.bulkUpgradeItems.value[0].error, '等待 Agent 升级完成超时')
  assert.equal(model.bulkUpgradePhase.value, 'done')
})

test('批量升级 Modal 展示实时计数和失败原因，运行中关闭继续而完成后关闭重置', async () => {
  const { model } = serverModel()
  model.bulkUpgradeModalOpen.value = true
  model.bulkUpgradePhase.value = 'running'
  model.bulkUpgradeTargetVersion.value = 'v0.20.0'
  model.bulkUpgradeItems.value = [
    bulkUpgradeItem(bulkUpgradeServer(7, { name: '东京' }), { status: 'success' }),
    bulkUpgradeItem(bulkUpgradeServer(8, { name: '德国' }), { status: 'failed', error: 'download Agent upgrade: context deadline exceeded' }),
    bulkUpgradeItem(bulkUpgradeServer(9, { name: '伦敦' })),
    bulkUpgradeItem(bulkUpgradeServer(10, { name: '香港' }), { status: 'waiting', startedAt: undefined }),
  ]
  let output = await render('components/server/AgentBulkUpgradeModal.vue', model)
  for (const text of ['目标版本', '1 / 4 已成功升级', '已处理 2 / 4', '成功 1', '失败 1', '升级中 1', '等待 1', '德国', 'download Agent upgrade: context deadline exceeded', '关闭窗口（升级继续）']) {
    assert.match(output, new RegExp(text))
  }

  model.closeBulkAgentUpgrade()
  assert.equal(model.bulkUpgradeModalOpen.value, false)
  assert.equal(model.bulkUpgradeItems.value.length, 4)

  model.bulkUpgradeModalOpen.value = true
  model.bulkUpgradePhase.value = 'done'
  output = await render('components/server/AgentBulkUpgradeModal.vue', model)
  assert.match(output, /批量升级完成/)
  assert.match(output, /未升级成功/)
  assert.match(output, /德国/)
  model.closeBulkAgentUpgrade()
  assert.equal(model.bulkUpgradeItems.value.length, 0)
  assert.equal(model.bulkUpgradePhase.value, 'confirm')
})

test('服务器统一基本信息表单按现有接口保存全部可编辑字段', async t => {
  let current = serverRecord()
  const patchCalls = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    if (init?.method === 'PATCH') {
      const body = JSON.parse(init.body)
      patchCalls.push({ url, body })
      if (url.endsWith('/access')) {
        current = { ...current, visibility: body.visibility, access_user_ids: body.user_ids }
        return json({ access: { visibility: body.visibility, user_ids: body.user_ids } })
      }
      if ('name' in body) current = { ...current, name: body.name }
      if ('owner_user_id' in body) current = { ...current, owner_user_id: body.owner_user_id, owner_username: 'member' }
      if ('expires_at' in body) {
        current = {
          ...current,
          expires_at: `${body.expires_at}T15:59:59Z`,
          renewal_period_months: body.renewal_period_months,
          auto_renew: body.auto_renew,
        }
      }
      return json({ server: current })
    }
    return json({ servers: url.includes('?archived=true') ? [] : [current] })
  })
  const { model } = serverModel()
  model.users.value = [
    { id: 1, username: 'admin', role: 'admin' },
    { id: 2, username: 'member', role: 'vip' },
  ]
  model.viewServer(current)
  model.openBasicInfoModal()
  model.nameInput.value = '新名称'
  model.ownerUserID.value = 2
  model.accessVisibility.value = 'private'
  model.accessUserIDs.value = [1, 2]
  model.expirationInput.value = '2027-01-31'
  model.renewalPeriodInput.value = 3
  model.autoRenewInput.value = true

  await model.saveBasicInfo()

  assert.deepEqual(patchCalls.map(({ url, body }) => ({ path: url, body })), [
    { path: '/api/servers/7', body: { name: '新名称' } },
    { path: '/api/servers/7', body: { owner_user_id: 2 } },
    { path: '/api/servers/7', body: { expires_at: '2027-01-31', renewal_period_months: 3, auto_renew: true } },
    { path: '/api/servers/7/access', body: { visibility: 'private', user_ids: [1, 2] } },
  ])
  assert.equal(model.basicInfoModalOpen.value, false)
  assert.equal(model.selectedServer.value.name, '新名称')
  assert.equal(model.selectedServer.value.owner_username, 'member')
  assert.deepEqual(model.selectedServer.value.access_user_ids, [1, 2])
  assert.equal(model.selectedServer.value.auto_renew, true)
})

test('服务器统一基本信息保存名称后详情保持打开且列表使用新名称', async t => {
  const updated = serverRecord({ name: '新名称' })
  const calls = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push({ url, init })
    if (init?.method === 'PATCH') return json({ server: updated })
    return json({ servers: url.includes('?archived=true') ? [] : [updated] })
  })
  const { model } = serverModel()
  model.viewServer(serverRecord())
  model.openBasicInfoModal()
  assert.equal(model.nameInput.value, '测试服务器')
  model.nameInput.value = '  新名称  '
  await model.saveBasicInfo()
  assert.deepEqual(JSON.parse(calls[0].init.body), { name: '新名称' })
  assert.equal(model.selectedServer.value.name, '新名称')
  assert.equal(model.servers.value[0].name, '新名称')
  assert.equal(model.serverModalOpen.value, true)
  assert.equal(model.basicInfoModalOpen.value, false)
  const detail = await render('components/server/ServerDetail.vue', model)
  assert.match(detail, /server-detail-grid/)
  for (const title of ['基本信息', 'Agent', '月流量']) assert.match(detail, new RegExp(title))
  for (const title of ['系统信息', '动态指标']) assert.doesNotMatch(detail, new RegExp(title))
})

test('服务器统一基本信息表单可选择所有者或无所有者并立即刷新详情', async t => {
  const users = [
    { id: 1, username: 'admin', role: 'admin' },
    { id: 2, username: 'member', role: 'vip' },
  ]
  let current = serverRecord()
  const patchBodies = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    if (init?.method === 'PATCH') {
      const body = JSON.parse(init.body)
      patchBodies.push(body)
      current = {
        ...current,
        owner_user_id: body.owner_user_id,
        owner_username: body.owner_user_id === 2 ? 'member' : '',
      }
      return json({ server: current })
    }
    return json({ servers: url.includes('?archived=true') ? [] : [current] })
  })
  const { model } = serverModel()
  model.users.value = users
  model.viewServer(current)
  let detail = await render('components/server/ServerDetail.vue', model)
  assert.match(detail, /admin/)
  assert.equal((detail.match(/> 修改 <\/button>/g) ?? []).length, 1)

  model.openBasicInfoModal()
  assert.equal(model.basicInfoModalOpen.value, true)
  assert.equal(model.ownerUserID.value, 1)
  const form = await render('components/server/ServerBasicInfoForm.vue', model)
  assert.match(form, /修改基本信息/)
  assert.match(form, /无所有者/)
  assert.match(form, /admin（管理员）/)
  assert.match(form, /member（VIP用户）/)

  model.ownerUserID.value = 2
  await model.saveBasicInfo()
  assert.deepEqual(patchBodies[0], { owner_user_id: 2 })
  assert.equal(model.selectedServer.value.owner_username, 'member')
  assert.equal(model.servers.value[0].owner_username, 'member')
  assert.equal(model.basicInfoModalOpen.value, false)

  model.openBasicInfoModal()
  model.ownerUserID.value = 0
  await model.saveBasicInfo()
  assert.deepEqual(patchBodies[1], { owner_user_id: null })
  assert.equal(model.selectedServer.value.owner_user_id, null)
  assert.equal(model.selectedServer.value.owner_username, '')
  detail = await render('components/server/ServerDetail.vue', model)
  assert.match(detail, /所有者/)
  assert.match(detail, /—/)
})

test('服务器详情展示中国 IP 入站限制状态并允许历史开启状态关闭', async () => {
  const { model } = serverModel()
  const supported = {
    agent_implementation: 'vps-panel-agent', agent_version: 'v0.31.0', agent_api_version: 1,
    agent_capabilities: ['firewall.cn_block'], agent_version_status: 'up_to_date',
  }
  for (const [overrides, label] of [
    [{ ...supported, status: 'online', block_china_inbound: true, desired_state_version: 2, agent_applied_config_version: 2, agent_config_sync_status: 'success' }, '已生效'],
    [{ ...supported, status: 'online', block_china_inbound: false, desired_state_version: 2, agent_applied_config_version: 2, agent_config_sync_status: 'success' }, '已关闭'],
    [{ ...supported, status: 'online', block_china_inbound: true, desired_state_version: 3, agent_applied_config_version: 2, agent_config_sync_status: 'pending' }, '应用中'],
    [{ ...supported, status: 'offline', block_china_inbound: true, desired_state_version: 3, agent_applied_config_version: 2, agent_config_sync_status: 'pending' }, '等待 Agent 上线'],
  ]) {
    model.viewServer(serverRecord(overrides))
    const output = await render('components/server/ServerDetail.vue', model)
    assert.match(output, new RegExp(label))
    assert.match(output, /中国 IP 入站限制/)
    assert.match(output, /Proxy 和 Relay/)
    assert.match(output, /APNIC/)
    assert.match(output, /SSH 和其他服务不受影响/)
  }

  model.viewServer(serverRecord({ ...supported, status: 'online', block_china_inbound: true, agent_config_sync_status: 'failed', agent_config_sync_error: 'managed China inbound firewall requires nftables' }))
  const failed = await render('components/server/ServerDetail.vue', model)
  assert.match(failed, /配置应用失败/)
  assert.match(failed, /managed China inbound firewall requires nftables/)

  model.viewServer(serverRecord())
  const unsupportedOff = await render('components/server/ServerDetail.vue', model)
  const disabledSwitch = [...unsupportedOff.matchAll(/<button[^>]*role="switch"[^>]*>/g)].at(-1)?.[0] ?? ''
  assert.match(disabledSwitch, /disabled/)
  assert.match(unsupportedOff, /未启用/)

  model.viewServer(serverRecord({ ...supported, status: 'online', agent_config_sync_status: 'success', agent_applied_config_version: 1 }))
  const supportedOff = await render('components/server/ServerDetail.vue', model)
  const supportedSwitch = [...supportedOff.matchAll(/<button[^>]*role="switch"[^>]*>/g)].at(-1)?.[0] ?? ''
  assert.doesNotMatch(supportedSwitch, /disabled/)

  model.viewServer(serverRecord({ block_china_inbound: true, agent_version_status: 'unknown' }))
  const unsupportedOn = await render('components/server/ServerDetail.vue', model)
  const enabledSwitch = [...unsupportedOn.matchAll(/<button[^>]*role="switch"[^>]*>/g)].at(-1)?.[0] ?? ''
  assert.doesNotMatch(enabledSwitch, /disabled/)
  assert.match(unsupportedOn, /无法确认/)
  assert.match(unsupportedOn, /仍然生效/)

  model.viewServer(serverRecord({ ...supported, archived_at: '2026-09-24T00:00:00Z' }))
  const archived = await render('components/server/ServerDetail.vue', model)
  const archivedSwitch = archived.match(/<button[^>]*role="switch"[^>]*>/)?.[0] ?? ''
  assert.match(archivedSwitch, /disabled/)
  assert.match(archived, /只读/)
})

test('服务器一键诊断请求固定 API 并按模块展示结构化结果', async t => {
  const calls = []
  const report = {
    server_id: 7,
    started_at: '2026-09-22T05:00:00Z',
    duration_ms: 42,
    checks: [
      { code: 'agent.connected', status: 'pass', detail: 'Agent 在线并已响应诊断请求' },
      { code: 'config.version', status: 'pass', detail: 'Desired v1 / Applied v1' },
      { code: 'xray.listener', status: 'pass', label: 'US VLESS', endpoint: '127.0.0.1:443', protocol: 'tcp' },
      { code: 'relay.target_tcp', status: 'fail', label: 'SG Relay', endpoint: '203.0.113.8:8443', protocol: 'tcp', detail: 'timeout' },
      { code: 'panel.entry_tcp', status: 'pass', label: 'US VLESS', endpoint: 'node.example.com:443', latency_ms: 42 },
      { code: 'protocol.end_to_end', status: 'skipped', detail: '未执行协议握手' },
    ],
  }
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push({ url, init })
    return json(report)
  })
  const { model } = serverModel()
  const server = serverRecord({ status: 'online' })
  model.viewServer(server)
  await model.openDiagnostics(server)
  assert.equal(calls[0].url, '/api/servers/7/diagnostics')
  assert.equal(calls[0].init.method, 'POST')
  assert.deepEqual(model.diagnosticReport.value, report)
  const detail = await render('components/server/ServerDetail.vue', model)
  for (const label of ['一键诊断', '配置同步', 'Xray', '中转目标', 'Panel → 入口', '协议端到端', '重新诊断']) {
    assert.match(detail, new RegExp(label))
  }
  assert.match(detail, /TCP 检查只表示指定网络位置可以建立连接/)
  assert.match(detail, /US VLESS/)
  assert.match(detail, /node\.example\.com:443/)
})

test('旧 Agent 的诊断不支持错误显示在诊断 Drawer 内', async t => {
  t.mock.method(globalThis, 'fetch', async () => json({ error: '当前 Agent 不支持一键诊断，请升级 Agent。' }, 409))
  const { model } = serverModel()
  const server = serverRecord({ status: 'online' })
  model.viewServer(server)
  await model.openDiagnostics(server)
  assert.equal(model.diagnosticOpen.value, true)
  assert.equal(model.diagnosticError.value, '当前 Agent 不支持一键诊断，请升级 Agent。')
  const detail = await render('components/server/ServerDetail.vue', model)
  assert.match(detail, /当前 Agent 不支持一键诊断，请升级 Agent。/)
  assert.match(detail, /开始诊断/)
})

test('出站优先级按钮高亮当前设置，归档时禁用', async () => {
  const { model } = serverModel()
  for (const [preference, label] of [['auto', '系统默认'], ['prefer_ipv4', '优先 IPv4'], ['prefer_ipv6', '优先 IPv6']]) {
    model.viewServer(serverRecord({ outbound_preference: preference, status: 'offline' }))
    const detail = await render('components/server/ServerDetail.vue', model)
    assert.match(detail, new RegExp(`<button[^>]*type="primary"[^>]*>${label}</button>`))
    assert.match(detail, /设置会保存，待 Agent 下次上线自动应用。/)
  }
  model.viewServer(serverRecord({
    status: 'offline',
    agent_implementation: 'third-party-agent',
    agent_api_version: 1,
    agent_capabilities: [],
  }))
  const unsupported = await render('components/server/ServerDetail.vue', model)
  assert.match(unsupported, /当前 Agent 不支持出站 IPv4 \/ IPv6 偏好。/)
  assert.doesNotMatch(unsupported, /设置会保存，待 Agent 下次上线自动应用。/)
  const autoButton = unsupported.match(/<button[^>]*>系统默认<\/button>/)?.[0] ?? ''
  assert.doesNotMatch(autoButton, /disabled/)

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
  form.openEditProxy({ id: 4, name: 'legacy', server_id: 7, listen_family: 'ipv4', listen_port: 443, entry_host_mode: 'auto', entry_host: '', enabled: true, protocol: 'vless', config: { security: 'tls', server_name: 'node.example.com', tls_certificate_configured: true } })
  assert.equal(form.proxyTLSMode.value, 'manual')
  form.proxyCertificate.value = 'test-certificate'
  form.proxyPrivateKey.value = 'test-key'
  await form.saveProxy()
  assert.equal(calls[1].url, '/api/proxies/4')
  assert.equal(calls[1].body.certificate, 'test-certificate')
  assert.equal(calls[1].body.private_key, 'test-key')
})

test('Proxy 入口选择将绑定域名映射为现有 manual 请求并按服务器刷新', async t => {
  const calls = []
  const servers = [
    { id: 7, name: 'A', bound_domain_ipv4: 'a.example.com', bound_domain_ipv6: '', system_info: null },
    { id: 8, name: 'B', bound_domain_ipv4: '', bound_domain_ipv6: '', system_info: null },
  ]
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push({ url, body: JSON.parse(init.body) })
    return json({ proxy: { id: 4 } })
  })
  const form = useProxyForm({ servers }, ref(null), ref(''), action => action(), async () => {}, async () => {})
  form.openCreateProxy()
  assert.equal(form.proxyEntryHostMode.value, 'bound')
  form.proxyName.value = 'Bound'
  form.proxyServerName.value = 'sni.example.com'
  await form.saveProxy()
  assert.equal(calls[0].body.entry_host_mode, 'manual')
  assert.equal(calls[0].body.entry_host, 'a.example.com')

  form.openCreateProxy()
  form.proxyEntryHostMode.value = 'manual'
  form.proxyEntryHost.value = 'custom.example.com'
  form.proxyServerID.value = 8
  form.onProxyServerChange()
  assert.equal(form.proxyEntryHostMode.value, 'auto')
  assert.equal(form.proxyEntryHost.value, '')
  form.proxyName.value = 'Auto'
  form.proxyServerName.value = 'sni.example.com'
  await form.saveProxy()
  assert.equal(calls[1].body.entry_host_mode, 'auto')
  assert.equal(calls[1].body.entry_host, '')

  form.openEditProxy({ id: 4, name: 'Bound', server_id: 7, listen_family: 'ipv4', listen_port: 443, entry_host_mode: 'manual', entry_host: 'a.example.com', enabled: true, protocol: 'vless', config: { security: 'tls', server_name: 'sni.example.com', tls_certificate_configured: true } })
  assert.equal(form.proxyEntryHostMode.value, 'bound')
  form.openEditProxy({ id: 5, name: 'Custom', server_id: 7, listen_family: 'ipv4', listen_port: 443, entry_host_mode: 'manual', entry_host: 'custom.example.com', enabled: true, protocol: 'vless', config: { security: 'tls', server_name: 'sni.example.com', tls_certificate_configured: true } })
  assert.equal(form.proxyEntryHostMode.value, 'manual')
})

test('Proxy Family 切换使用对应域名，无 IPv6 的服务器自动回退', async t => {
  const calls = []
  const error = ref('')
  const servers = [
    { id: 7, name: 'A', bound_domain_ipv4: 'a4.example.com', bound_domain_ipv6: 'a6.example.com', system_info: { public_ipv4: '198.51.100.7', public_ipv6: '2606:4700:4700::1111' } },
    { id: 8, name: 'B', bound_domain_ipv4: 'b4.example.com', bound_domain_ipv6: '', system_info: { public_ipv4: '198.51.100.8', public_ipv6: '' } },
  ]
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    calls.push({ url, body: JSON.parse(init.body) })
    return json({ proxy: { id: 6 } })
  })
  const form = useProxyForm({ servers }, ref(null), error, action => action(), async () => {}, async () => {})
  form.openCreateProxy()
  assert.equal(form.proxyListenFamily.value, 'ipv4')
  form.proxyListenFamily.value = 'ipv6'
  form.onProxyFamilyChange()
  assert.equal(form.proxyEntryHostMode.value, 'bound')
  assert.equal(form.selectedServerBoundDomain.value, 'a6.example.com')
  form.proxyName.value = 'IPv6'
  form.proxyServerName.value = 'sni.example.com'
  await form.saveProxy()
  assert.equal(calls[0].body.listen_family, 'ipv6')
  assert.equal(calls[0].body.entry_host, 'a6.example.com')

  form.openCreateProxy()
  form.proxyListenFamily.value = 'ipv6'
  form.onProxyFamilyChange()
  form.proxyServerID.value = 8
  form.onProxyServerChange()
  assert.equal(form.proxyListenFamily.value, 'ipv4')
  assert.equal(form.proxyEntryHostMode.value, 'bound')
  assert.equal(form.selectedServerBoundDomain.value, 'b4.example.com')
  assert.match(error.value, /已切换为 IPv4/)
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
