import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, reactive } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, useProxies, ProxyList, ProxyForm, External, Relays
const originalFetch = globalThis.fetch
before(async () => {
  loader = await createServer({
    configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ useProxies } = await loader.ssrLoadModule('/src/composables/useProxies.ts'))
  ;({ default: ProxyList } = await loader.ssrLoadModule('/src/components/proxy/ProxyList.vue'))
  ;({ default: ProxyForm } = await loader.ssrLoadModule('/src/components/proxy/ProxyForm.vue'))
  ;({ default: External } = await loader.ssrLoadModule('/src/components/proxy/ExternalNodeManager.vue'))
  ;({ default: Relays } = await loader.ssrLoadModule('/src/views/RelaysView.vue'))
})
after(async () => { globalThis.fetch = originalFetch; await loader?.close() })

const proxy = (id, node_role = 'direct') => ({ id, node_role, name: `managed-${id}`, server_id: 1, server_name: 'server',
  entry_address: 'node.example.com', entry_host: '', entry_host_mode: 'auto', listen_family: 'ipv4', listen_port: 443,
  protocol: 'vless', enabled: true, config: { security: 'reality', server_name: 'example.com', reality_target: 'example.com:443' } })
const external = (id, node_role = 'direct') => ({ id, node_role, name: `external-${id}`, host: 'example.com', port: 443,
  protocol: 'vless', visibility: 'private', owned_by_me: true })
const relay = (id, subscription_published = false) => ({ id, server_id: 1, subscription_published, name: `relay-${id}`, server_name: 'server',
  entry_address: 'relay.example.com', target_type: 'manual', target_host: 'example.com', target_port: 443,
  target_address_ready: true, target_client_name: '', enabled: true })
async function withState(component, seed, props = {}) {
  const setup = component.setup
  let state
  component.setup = (props, context) => { state = setup(props, context); seed(state); return state }
  try { const html = await renderToString(createSSRApp(component, props)); return { state, html } }
  finally { component.setup = setup }
}
function mockOrder(resource, initial) {
  const stored = initial.map(row => ({ ...row })), calls = []
  globalThis.fetch = async (url, options) => {
    calls.push(url)
    if (options.method === 'POST') {
      const id = Number(url.split('/')[3]), index = stored.findIndex(row => row.id === id)
      const step = JSON.parse(options.body).direction === 'up' ? -1 : 1
      ;[stored[index], stored[index + step]] = [stored[index + step], stored[index]]
      return new Response(null, { status: 204 })
    }
    return new Response(JSON.stringify({ [resource]: stored }), { status: 200 })
  }
  return calls
}

test('受管节点分类内搜索、用途表单和包含隐藏行的排序', async () => {
  const model = useProxies({ servers: [], users: [] })
  const rows = [proxy(3), proxy(4, 'landing'), proxy(2), proxy(5, 'landing'), proxy(1)]
  model.proxies.value = rows.map(row => ({ ...row })); model.loading.value = false
  assert.equal(model.nodeRole.value, 'direct')
  assert.deepEqual(model.filteredProxies.value.map(row => row.id), [3, 2, 1])
  const html = await renderToString(createSSRApp(ProxyList, { model: reactive(model) }))
  assert.match(html, /aria-label="受管节点用途"/)
  assert.match(html, /managed-3/); assert.doesNotMatch(html, /managed-4/)
  model.search.value = 'managed-4'
  assert.equal(model.filteredProxies.value.length, 0)
  model.nodeRole.value = 'landing'
  assert.deepEqual(model.filteredProxies.value.map(row => row.id), [4])
  model.search.value = ''; model.nodeRole.value = 'direct'
  const calls = mockOrder('proxies', rows)
  await model.reorderProxy(model.proxies.value[4], 3)
  assert.equal(calls.length, 5) // Four global neighbours, then reload.
  assert.deepEqual(model.filteredProxies.value.map(row => row.id), [1, 3, 2])
  model.nodeRole.value = 'landing'
  assert.deepEqual(model.filteredProxies.value.map(row => row.id), [4, 5])
  model.openEditProxy(proxy(4, 'landing'))
  assert.equal(model.proxyNodeRole.value, 'landing')
  const form = await renderToString(createSSRApp(ProxyForm, { model: reactive(model) }))
  assert.match(form, /节点用途/); assert.match(form, /value="landing"/)
  model.resetProxyForm(); assert.equal(model.proxyNodeRole.value, 'direct')
})

test('外部节点同卡片分类、表单提交用途，排序不会跨分类', async () => {
  const rows = [external(3), external(4, 'landing'), external(2), external(5, 'landing'), external(1)]
  const { state, html } = await withState(External, state => { state.landings.value = rows.map(row => ({ ...row })); state.loading.value = false })
  assert.equal(state.category.value, 'direct')
  assert.match(html, /aria-label="外部节点用途"/)
  assert.match(html, /external-3/); assert.doesNotMatch(html, /external-4/)
  let calls = mockOrder('landings', rows)
  state.draggedID.value = 1; await state.dropExternalNode(3)
  assert.equal(calls.length, 5)
  assert.deepEqual(state.filteredLandings.value.map(row => row.id), [1, 3, 2])
  state.category.value = 'landing'
  assert.deepEqual(state.filteredLandings.value.map(row => row.id), [4, 5])
  state.draggedID.value = 1; await state.dropExternalNode(4)
  assert.equal(calls.length, 5)
  state.openEdit(rows[1]); assert.equal(state.nodeRole.value, 'landing')
  let payload
  globalThis.fetch = async (url, options) => {
    if (options.method === 'PATCH') payload = JSON.parse(options.body)
    return new Response(JSON.stringify({ landings: rows }), { status: 200 })
  }
  state.nodeRole.value = 'direct'; await state.saveExternalNode()
  assert.equal(payload.node_role, 'direct'); assert.equal(payload.uri, undefined)
  state.openCreate(); assert.equal(state.nodeRole.value, 'direct')
})

test('纯用途编辑不受 Agent 能力或 IPv6 当前状态阻挡，只提交用途', async () => {
  const model = useProxies({ servers: [{ id: 1, agent_implementation: 'third-party-agent', agent_api_version: 1,
    agent_capabilities: [], system_info: { ipv6: [], public_ipv6: '' } }], users: [] })
  const value = { ...proxy(1), listen_family: 'ipv6' }
  model.openEditProxy(value)
  model.proxyNodeRole.value = 'landing'
  assert.equal(model.proxyRoleOnlyUpdate.value, true)
  assert.match(model.proxyCapabilityWarning.value, /不支持/)
  let payload
  globalThis.fetch = async (url, options) => {
    if (options.method === 'PATCH') payload = JSON.parse(options.body)
    return new Response(JSON.stringify(options.method === 'PATCH' ? { proxy: value } : { proxies: [value] }), { status: 200 })
  }
  await model.saveProxy()
  assert.deepEqual(payload, { node_role: 'landing' })
  assert.equal(model.error.value, '')
  model.openEditProxy(value)
  model.proxyPort.value = 8443
  assert.equal(model.proxyRoleOnlyUpdate.value, false)
  payload = undefined
  await model.saveProxy()
  assert.match(model.error.value, /不支持/)
  assert.equal(payload, undefined)
})

test('中转默认本地分类，发布分类仅查看且不提供新增或拖动入口', async () => {
  const rows = [relay(3), relay(4, true), relay(2), relay(5, true), relay(1)]
  const { state, html } = await withState(Relays, state => { state.relays.value = rows.map(row => ({ ...row })); state.loading.value = false }, { servers: [] })
  assert.equal(state.category.value, 'local')
  assert.match(html, /relay-3/); assert.doesNotMatch(html, /relay-4/)
  state.search.value = 'relay-4'; assert.equal(state.filteredRelays.value.length, 0)
  state.category.value = 'published'; assert.deepEqual(state.filteredRelays.value.map(row => row.id), [4])
  state.search.value = ''; state.category.value = 'local'
  const calls = mockOrder('relays', rows)
  await state.reorderRelay(state.relays.value[4], 3)
  assert.equal(calls.length, 5)
  assert.deepEqual(state.filteredRelays.value.map(row => row.id), [1, 3, 2])
  const published = await withState(Relays, state => { state.relays.value = rows; state.loading.value = false; state.category.value = 'published' }, { servers: [] })
  assert.match(published.html, /订阅管理 → 发布节点/)
  assert.match(published.html, /relay-4/); assert.doesNotMatch(published.html, /relay-3/)
  assert.doesNotMatch(published.html, /新增中转|>编辑<|>删除<|>禁用<|aria-label="拖动中转排序"/)
})

test('受管节点配置开关独立于离线和公网 IP 状态，恢复不改手动禁用', async () => {
  const servers = [{ id: 1, status: 'offline', system_info: { public_ipv4: '203.0.113.1' } }]
  const model = useProxies({ servers, users: [] })
  model.proxies.value = [proxy(1)]; model.loading.value = false
  const render = () => renderToString(createSSRApp(ProxyList, { model: reactive(model) }))
  let html = await render()
  assert.match(html, />启用<.*服务器离线/s)
  assert.equal(model.proxies.value[0].enabled, true)
  servers[0].status = 'online'; servers[0].system_info.public_ipv4 = ''
  html = await render()
  assert.match(html, />启用<.*入口地址不可用/s)
  servers[0].system_info.public_ipv4 = '203.0.113.1'
  model.proxies.value[0].enabled = false
  html = await render()
  assert.match(html, />禁用<.*服务器在线 · 地址可用/s)
  assert.equal(model.proxies.value[0].enabled, false)
})

test('本地和发布中转目标不可用时仍显示配置启用，地址恢复自动更新提示', async () => {
  const servers = [{ id: 1, status: 'online', system_info: { public_ipv4: '203.0.113.1' } },
    { id: 2, status: 'online', system_info: { public_ipv4: '' } }]
  const target = { ...proxy(1), server_id: 2 }
  const local = { ...relay(1), target_type: 'proxy', target_proxy_id: 1, target_address_ready: false }
  for (const published of [false, true]) {
    const { state, html } = await withState(Relays, state => {
      state.relays.value = [{ ...local, subscription_published: published }]
      state.proxies.value = [target]; state.loading.value = false; state.category.value = published ? 'published' : 'local'
    }, { servers })
    assert.match(html, />启用<.*目标：入口地址不可用/s)
    servers[1].system_info.public_ipv4 = '203.0.113.2'
    assert.equal(state.relayAvailability(local), '服务器在线 · 地址可用')
    assert.equal(state.relays.value[0].enabled, true)
    servers[1].status = 'offline'
    assert.match(state.relayAvailability(local), /目标：服务器离线/)
    servers[1].status = 'online'; servers[1].system_info.public_ipv4 = ''
  }
})
