import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, Accounts, Subscriptions, BindingEditor, drag
const originalFetch = globalThis.fetch
before(async () => {
  loader = await createServer({
    configFile: false, root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [vue(), {
      name: 'observe-shared-drag-calls',
      transform(_, id) {
        if (!id.replaceAll('\\', '/').endsWith('/src/drag.ts')) return
        return `export const calls = [];
          export function beginDragPreview(...args) { calls.push(['begin', ...args]); return true }
          export function endDragPreview() { calls.push(['end']) }`
      },
    }],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ default: Accounts } = await loader.ssrLoadModule('/src/views/AccountManagementView.vue'))
  ;({ default: Subscriptions } = await loader.ssrLoadModule('/src/views/SubscriptionManagementView.vue'))
  ;({ default: BindingEditor } = await loader.ssrLoadModule('/src/components/subscription/RoutingBindingEditor.vue'))
  drag = await loader.ssrLoadModule('/src/drag.ts')
})
after(async () => { globalThis.fetch = originalFetch; await loader?.close() })

const accounts = () => [
  { id: 1, username: 'admin', role: 'admin' },
  { id: 2, username: 'refrain', role: 'vip' },
  { id: 3, username: 'Refrain', role: 'subscriber' },
]
const personalSubscriptions = () => [1, 2, 3].map(id => ({
  id, name: `Personal ${id}`, enabled: true, client_name: `client-${id}`, nodes: [], routing_preset_name: '默认分流',
  mihomo_template_name: '', shadowrocket_template_name: '',
}))
const publishedNodes = () => [1, 2, 3].map(id => ({
  id, name: `Published ${id}`, enabled: true, mode: id === 2 ? 'relay' : 'direct', traffic_multiplier: 1,
  entry_address: 'node.example.com', entry_port: 443, target_server_name: 'Target', target_proxy_name: `Proxy ${id}`,
  source_server_name: 'Source',
}))
async function render(component, populate, props = {}) {
  if (component === Subscriptions) props = { currentTab: 'personal', ...props }
  const setup = component.setup
  let state
  component.setup = (props, context) => {
    state = setup(props, context)
    populate(state)
    return state
  }
  try { return { html: await renderToString(createSSRApp(component, props)), state } }
  finally { component.setup = setup }
}
const renderAccounts = () => render(Accounts, state => { state.accounts.value = accounts(); state.loading.value = false })

test('每个用户包括管理员均有共享拖拽手柄，管理员没有删除按钮', async () => {
  const { html } = await renderAccounts()
  assert.equal((html.match(/aria-label="拖动用户排序"/g) ?? []).length, 3)
  assert.equal((html.match(/draggable="true"/g) ?? []).length, 3)
  assert.equal((html.match(/>删除用户</g) ?? []).length, 2)
  assert.doesNotMatch(html.slice(html.indexOf('<strong>admin'), html.indexOf('<strong>refrain')), />删除用户</)
})

test('拖动使用共享 begin/end 基础设施，管理员 drop 乐观更新并逐步保存', async () => {
  const { state } = await renderAccounts()
  drag.calls.length = 0
  const source = { id: 'account-row' }
  const event = { dataTransfer: {}, currentTarget: { closest(selector) { assert.equal(selector, '.invitation-row'); return source } } }
  state.startDrag(event, 1)
  assert.deepEqual(drag.calls, [['begin', event, source, '1']])
  assert.equal(state.draggedID.value, 1)
  const calls = []
  let resolveFirst
  globalThis.fetch = async (url, options) => {
    calls.push([url, options.method, options.body])
    if (calls.length === 1) await new Promise(resolve => { resolveFirst = resolve })
    return { ok: true, status: options.method === 'POST' ? 204 : 200, json: async () => ({ users: [accounts()[1], accounts()[2], accounts()[0]] }) }
  }
  const save = state.dropAccount(3)
  assert.deepEqual(state.accounts.value.map(a => a.id), [2, 3, 1])
  assert.deepEqual(drag.calls.at(-1), ['end'])
  assert.equal(state.reorderingID.value, 1)
  state.draggedID.value = 2
  await state.dropAccount(1)
  assert.equal(calls.length, 1)
  resolveFirst(); await save
  assert.deepEqual(calls.map(c => c[0]), ['/api/users/1/reorder', '/api/users/1/reorder', '/api/users'])
  assert.deepEqual(calls.slice(0, 2).map(c => JSON.parse(c[2])), [{ direction: 'down' }, { direction: 'down' }])
  assert.equal(state.reorderingID.value, null)
  assert.equal(state.error.value, '')
})

test('dragover 只提示目标，失败后恢复持久化顺序并显示错误', async () => {
  const { state } = await renderAccounts()
  const calls = []
  globalThis.fetch = async (url, options) => {
    calls.push(url)
    if (options.method === 'POST') return { ok: false, status: 500, json: async () => ({ error: '排序保存失败' }) }
    return { ok: true, status: 200, json: async () => ({ users: accounts() }) }
  }
  state.draggedID.value = 3
  let prevented = false
  state.dragOver({ preventDefault() { prevented = true } }, 1)
  assert.ok(prevented)
  assert.equal(state.dropTargetID.value, 1)
  assert.equal(calls.length, 0)
  await state.dropAccount(1)
  assert.deepEqual(calls, ['/api/users/3/reorder', '/api/users'])
  assert.deepEqual(state.accounts.value.map(a => a.id), [1, 2, 3])
  assert.equal(state.error.value, '排序保存失败')
  assert.equal(state.draggedID.value, null)
  assert.equal(state.dropTargetID.value, null)
  assert.equal(state.reorderingID.value, null)
})

test('个人订阅网格拖动后乐观排序、逐步持久化并刷新', async () => {
  const { html, state } = await render(Subscriptions, state => {
    state.loading.value = false
    state.personalSubscriptions.value = personalSubscriptions()
  }, { role: 'vip' })
  assert.equal((html.match(/aria-label="拖动个人订阅排序"/g) ?? []).length, 3)
  assert.match(html, /personal-subscription-grid/)
  const calls = []
  globalThis.fetch = async (url, options = {}) => {
    calls.push([url, options.method, options.body])
    if (options.method === 'POST') return { ok: true, status: 204 }
    return { ok: true, status: 200, json: async () => ({ personal_subscriptions: [personalSubscriptions()[2], personalSubscriptions()[0], personalSubscriptions()[1]] }) }
  }
  state.draggedPersonalSubscriptionID.value = 3
  await state.dropPersonalSubscription(1)
  assert.deepEqual(calls.map(call => call[0]), [
    '/api/personal-subscriptions/3/reorder',
    '/api/personal-subscriptions/3/reorder',
    '/api/personal-subscriptions',
  ])
  assert.deepEqual(calls.slice(0, 2).map(call => JSON.parse(call[2])), [{ direction: 'up' }, { direction: 'up' }])
  assert.deepEqual(state.personalSubscriptions.value.map(value => value.id), [3, 1, 2])
  assert.equal(state.reorderingPersonalSubscriptionID.value, null)
})

test('发布节点条状卡片拖动持久化，失败时刷新回服务端顺序', async () => {
  const { html, state } = await render(Subscriptions, state => {
    state.loading.value = false
    state.nodes.value = publishedNodes()
  }, { role: 'admin', currentTab: 'nodes' })
  assert.equal((html.match(/aria-label="拖动发布节点排序"/g) ?? []).length, 3)
  assert.match(html, /published-node-list/)
  assert.match(html, /中转 \+ 落地/)
  const calls = []
  globalThis.fetch = async (url, options = {}) => {
    calls.push(url)
    if (options.method === 'POST') return { ok: false, status: 500, json: async () => ({ error: '发布节点排序保存失败' }) }
    return { ok: true, status: 200, json: async () => ({ nodes: publishedNodes() }) }
  }
  state.draggedPublishedNodeID.value = 3
  await state.dropPublishedNode(1)
  assert.deepEqual(calls, ['/api/admin/subscription/nodes/3/reorder', '/api/admin/subscription/nodes'])
  assert.deepEqual(state.nodes.value.map(value => value.id), [1, 2, 3])
  assert.equal(state.error.value, '发布节点排序保存失败')
  assert.equal(state.reorderingPublishedNodeID.value, null)
})

test('个人订阅卡片保留四个主要操作，编辑器提供配置预览', async () => {
  const { html } = await render(Subscriptions, state => {
    state.loading.value = false
    state.personalSubscriptions.value = [{ id: 1, name: 'iPhone', enabled: true, client_name: 'refrain', nodes: [], routing_preset_name: '个人自用' }]
  }, { role: 'vip' })
  for (const label of ['复制链接', '二维码', '编辑', '删除']) assert.match(html, new RegExp(`>${label}<`))
  assert.doesNotMatch(html, /复制 Shadowrocket URL|预览 Shadowrocket|>预览<|重置链接|个人订阅 Mihomo Preview/)
  const source = await readFile(new URL('../src/views/SubscriptionManagementView.vue', import.meta.url), 'utf8')
  assert.match(source, /personalPreviewOpen/)
  assert.match(source, /previewPersonal\('mihomo'\)/)
  assert.match(source, /previewPersonal\('shadowrocket'\)/)
  assert.doesNotMatch(source, /regeneratePersonalToken|regenerateUserToken/)
})

test('策略组节点按勾选和拖动的顺序更新，重新打开时回填顺序', async () => {
  const bindings = {}
  const props = {
    groups: [{ key: 'main', name: '默认组', include_all: true }],
    nodes: [{ id: 1, name: 'HK-03' }, { id: 2, name: 'JP-01' }, { id: 3, name: 'US-02' }],
    modelValue: bindings,
    'onUpdate:modelValue': value => Object.assign(bindings, value),
  }
  const { state } = await render(BindingEditor, state => state.toggleGroup('main'), props)
  state.toggleNode('main', 3, true)
  state.toggleNode('main', 2, true)
  state.toggleNode('main', 1, true)
  assert.deepEqual(bindings.main, [3, 2, 1])
  state.startDrag({ currentTarget: { closest: () => ({}) } }, 'main', 1)
  state.drop('main', 3)
  assert.deepEqual(bindings.main, [1, 3, 2])
  state.toggleNode('main', 2, false)
  assert.deepEqual(bindings.main, [1, 3])
  const { html } = await render(BindingEditor, state => state.toggleGroup('main'), { ...props, modelValue: structuredClone(bindings) })
  assert.match(html.slice(0, html.indexOf('type="checkbox"')), /已选节点顺序[\s\S]*>HK-03<[\s\S]*>US-02</)
})

test('个人订阅来源可独立折叠，搜索清除后恢复展开状态并沿用原有保存流程', async t => {
  t.after(() => { globalThis.fetch = originalFetch })
  const sources = [
    ['proxy', 1, 'Proxy 测试', '🇯🇵'],
    ['relay', 1, '本地中转测试', 'via Proxy'],
    ['landing', 1, '住宅落地测试', 'NYC edge'],
    ['remote_source', 1, '机场订阅测试', '全部缓存'],
    ['remote_node', 1, '纽约机场节点', 'NYC edge'],
    ['remote_node', 2, '东京机场节点', 'Tokyo edge'],
  ].map(([source_type, source_id, name, detail]) => ({
    source_type, source_id, name, detail, default_name: name,
    status: 'ready', status_detail: '可用', requires_client: false,
  }))
  const existing = {
    id: 11, source_type: 'relay', source_id: 1, source_name: '本地中转测试', source_detail: 'via Proxy',
    custom_name: '已选中转', display_name: '已选中转', enabled: true, position: 1, entry_host: 'relay.example.test', entry_port: 443,
    dialer_proxy: '', status: 'ready', status_detail: '可用', requires_client: false,
  }
  const subscription = {
    ...personalSubscriptions()[0], subscription_title: '手机', routing_preset_id: 1,
    nodes: [existing], routing_bindings: { grp_main: [11] },
  }
  const preset = { id: 1, name: '默认分流', enabled: true, groups: [{ key: 'grp_main', name: '默认组' }] }
  const lists = {
    '/api/personal-subscriptions': { personal_subscriptions: [subscription] },
    '/api/admin/subscription/routing-presets': { routing_presets: [preset] },
    '/api/admin/subscription/templates': { templates: [] },
  }
  let candidates = sources
  const writes = []
  globalThis.fetch = async (url, options) => {
    if (options.method) {
      writes.push({ url, method: options.method, body: JSON.parse(options.body) })
      if (url.endsWith('/nodes')) {
        const added = { ...existing, id: 12, position: 2, source_type: 'proxy', source_id: 1, custom_name: '', display_name: sources[0].name }
        return Response.json({ personal_subscription: { ...subscription, nodes: [existing, added] } })
      }
      return Response.json({})
    }
    if (url.includes('/sources?')) return Response.json({ sources: candidates })
    if (url === '/api/personal-subscriptions/1') return Response.json({ personal_subscription: subscription })
    if (url === '/api/personal-subscriptions/2') return Response.json({ personal_subscription: { ...subscription, id: 2, nodes: [] } })
    assert.ok(lists[url], `unexpected request: ${url}`)
    return Response.json(lists[url])
  }
  const { state } = await render(Subscriptions, current => {
    current.loading.value = false
    current.routingPresets.value = [preset]
  }, { role: 'vip' })
  const pickerHTML = async () => {
    const { html } = await render(Subscriptions, current => {
      current.loading.value = false
      for (const key of ['personalModalOpen', 'editingPersonal', 'personalSourceAdderExpanded', 'personalSourceSearch',
        'personalSources', 'expandedPersonalSourceTypes', 'personalNodes']) current[key].value = state[key].value
    }, { role: 'vip' })
    return html.match(/<section[^>]+class="subscription-node-picker personal-source-adder"[^>]*>[\s\S]+?<\/section>/)?.[0] ?? ''
  }
  function assertGroups(html, expected) {
    const headers = [...html.matchAll(/aria-expanded="(true|false)" aria-controls="([^"]+)"[^>]*>[\s\S]*?<strong[^>]*>([^<]+)<\/strong>\s*<span class="personal-source-group-count"[^>]*>(\d+) 个<\/span>/g)]
    assert.deepEqual(headers.map(([, expanded, , name, count]) => [name, Number(count), expanded === 'true']), expected)
    for (const [, , id] of headers) assert.ok(html.includes(`id="${id}"`))
  }

  await state.openEditPersonal(subscription)
  assert.equal(await pickerHTML(), '')
  await state.togglePersonalSourceAdder()
  const initial = [
    ['受管 Proxy 节点', 1, false], ['本地中转', 1, false], ['外部 Landing 节点', 1, false],
    ['机场订阅（全部节点）', 1, false], ['机场订阅节点', 2, false],
  ]
  let html = await pickerHTML()
  assertGroups(html, initial)
  assert.doesNotMatch(html, /Proxy 测试|本地中转测试|纽约机场节点/)

  state.togglePersonalSourceGroup('proxy')
  state.togglePersonalSourceGroup('relay')
  html = await pickerHTML()
  assertGroups(html, initial.map(([name, count], index) => [name, count, index < 2]))
  assert.match(html, /Proxy 测试/)
  assert.match(html, /本地中转测试/)
  state.togglePersonalSourceGroup('relay')
  assert.doesNotMatch(await pickerHTML(), /本地中转测试/)

  state.personalSourceSearch.value = '  nyc EDGE '
  html = await pickerHTML()
  assertGroups(html, [['外部 Landing 节点', 1, true], ['机场订阅节点', 1, true]])
  assert.match(html, /纽约机场节点/)
  assert.doesNotMatch(html, /东京机场节点/)
  state.personalSourceSearch.value = 'missing'
  html = await pickerHTML()
  assert.equal([...html.matchAll(/没有匹配的节点来源/g)].length, 1)
  assertGroups(html, [])

  state.personalSourceSearch.value = ''
  const restored = initial.map(([name, count], index) => [name, count, index === 0])
  assertGroups(await pickerHTML(), restored)
  assert.deepEqual(state.personalNodes.value.map(node => node.id), [11])
  state.addPersonalSource(sources[0])
  candidates = sources.filter(source => source.name !== '东京机场节点')
  await state.loadPersonalSources()
  assertGroups(await pickerHTML(), restored.map(([name, count, expanded], index) => [name, index === 4 ? 1 : count, expanded]))
  assert.equal(state.personalNodes.value[0].entry_host, existing.entry_host)
  assert.equal(state.personalTitle.value, subscription.subscription_title)

  await state.savePersonal()
  assert.deepEqual(writes.map(({ url, method }) => [url, method]), [
    ['/api/personal-subscriptions/1', 'PATCH'],
    ['/api/personal-subscriptions/1/nodes', 'PUT'],
    ['/api/personal-subscriptions/1/routing-bindings', 'PUT'],
  ])
  assert.deepEqual(writes[1].body.nodes.map(node => [node.id, node.source_type, node.source_id]), [[11, 'relay', 1], [null, 'proxy', 1]])
  assert.deepEqual(writes[1].body.nodes.map(node => node.custom_name), ['已选中转', ''])
  assert.deepEqual(writes[2].body, { routing_bindings: { grp_main: [11] } })
  assert.equal(state.personalModalOpen.value, false)
  assert.equal(state.personalFormError.value, '')

  await state.openEditPersonal({ ...subscription, id: 2 })
  assert.equal(await pickerHTML(), '')
  await state.togglePersonalSourceAdder()
  assertGroups(await pickerHTML(), initial.map(([name]) => [name, 1, false]))
})

test('批量添加留空覆盖名，编辑和重开分别显示有效名称与覆盖值', async t => {
  t.after(() => { globalThis.fetch = originalFetch })
  const sources = [3, 4].map(source_id => ({ source_type: 'landing', source_id, name: 'Japan', default_name: 'Japan' }))
  const group = { id: 1, client_name: 'owner', routing_preset_id: 1, routing_bindings: {}, nodes: [
    { id: 31, source_type: 'landing', source_id: 3, source_name: 'Japan', custom_name: '', display_name: 'Japan' },
    { id: 32, source_type: 'landing', source_id: 4, source_name: 'Japan', custom_name: 'Custom', display_name: 'Custom' },
  ] }
  globalThis.fetch = async url => Response.json(url.includes('/sources?') ? { sources } : { personal_subscription: group })
  const { state } = await render(Subscriptions, current => {
    current.loading.value = false
    current.personalClientName.value = 'owner'
  }, { role: 'vip' })
  await state.addAllPersonalSources()
  assert.deepEqual(state.personalNodes.value.map(node => node.custom_name), ['', ''])
  assert.deepEqual([...state.personalEffectiveNames.value.values()], ['Japan', 'Japan [2]'])
  await state.openEditPersonal(group)
  assert.deepEqual(state.personalNodes.value.map(node => node.custom_name), ['', 'Custom'])
  assert.equal(state.personalEffectiveNames.value.get(32), 'Custom')
  state.personalNodes.value[1].custom_name = ''
  assert.equal(state.personalEffectiveNames.value.get(32), 'Japan [2]')
  const { html } = await render(Subscriptions, current => {
    current.loading.value = false
    current.personalModalOpen.value = true
    current.personalNodes.value = state.personalNodes.value
    current.expandedPersonalNodeIDs.value = new Set([32])
  }, { role: 'vip' })
  assert.match(html, /<strong[^>]*>Japan \[2\]<\/strong>/)
  assert.match(html.match(/<input[^>]*placeholder="留空使用原节点名称"[^>]*>/)?.[0] ?? '', /\bvalue(?:=""|(?=[\s>]))/)
  assert.match(html, /当前生效名称：Japan \[2\]/)
})

test('个人订阅可添加远程整源和单节点，并展示预览错误', async t => {
  t.after(() => { globalThis.fetch = originalFetch })
  const sources = [
    { source_type: 'remote_source', source_id: 1, name: '机场', detail: '全部缓存', default_name: '机场', status: 'ready', status_detail: '使用本地缓存', requires_client: false },
    { source_type: 'remote_node', source_id: 2, name: '东京', detail: '机场 · trojan', default_name: '东京', status: 'ready', status_detail: '使用本地缓存', requires_client: false },
  ]
  let shadowrocket = { ok: false, status: 422, json: async () => ({ error: '远程节点参数不兼容' }) }
  const editor = state => {
    state.loading.value = false
    state.personalModalOpen.value = true
    state.personalClientName.value = 'owner'
    state.editingPersonal.value = { id: 5 }
  }
  globalThis.fetch = async url => url.includes('/sources?')
    ? { ok: true, status: 200, json: async () => ({ sources }) }
    : url.includes('/mihomo-preview')
      ? { ok: true, status: 200, json: async () => ({ yaml: 'proxies: []' }) }
      : shadowrocket
  const { state } = await render(Subscriptions, editor, { role: 'vip' })
  await state.loadPersonalSources()
  for (const source of state.personalSources.value) state.addPersonalSource(source)
  assert.deepEqual(state.personalNodes.value.map(node => node.source_type), ['remote_source', 'remote_node'])
  await state.previewPersonal('mihomo')
  assert.equal(state.personalPreviewContent.value, 'proxies: []')
  assert.equal(state.personalPreviewOpen.value, true)
  state.personalPreviewOpen.value = false
  state.personalFormError.value = '表单校验错误'
  const attempt = state.previewPersonal('shadowrocket')
  assert.equal(state.personalPreviewLoading.value, 'shadowrocket')
  await attempt
  assert.equal(state.personalPreviewError.value, '远程节点参数不兼容')
  assert.equal(state.personalFormError.value, '表单校验错误')
  assert.equal(state.personalPreviewLoading.value, null)
  assert.equal(state.personalPreviewOpen.value, false)
  const { html } = await render(Subscriptions, current => {
    editor(current)
    current.personalPreviewError.value = state.personalPreviewError.value
  }, { role: 'vip' })
  assert.match(html, /预览 Shadowrocket<\/button><\/div>\s*<section[^>]*role="alert"[^>]*>(?:<!--.*?-->)*远程节点参数不兼容/)

  shadowrocket = { ok: true, status: 200, json: async () => ({ conf: '  ' }) }
  await state.previewPersonal('shadowrocket')
  assert.match(state.personalPreviewError.value, /服务器未返回配置内容/)
  assert.equal(state.personalPreviewOpen.value, false)

  shadowrocket = { ok: true, status: 200, json: async () => ({ conf: '[Proxy]', chain_warning: '落地节点会直接连接服务器' }) }
  await state.previewPersonal('shadowrocket')
  assert.equal(state.personalPreviewContent.value, '[Proxy]')
  assert.equal(state.personalPreviewOpen.value, true)
  assert.equal(state.personalPreviewWarning.value, '落地节点会直接连接服务器')
  assert.equal(state.personalPreviewError.value, '')
  assert.equal(state.personalPreviewLoading.value, null)
})

test('保存 Shadowrocket 默认策略时按节点身份转换临时 ID，绑定失败后可以重试', async t => {
  t.after(() => { globalThis.fetch = originalFetch })
  const source = { source_type: 'landing', source_id: 4, source_name: 'Tokyo', custom_name: '', enabled: true }
  const nodes = [{ ...source, id: -2 }, { ...source, id: -1 }]
  const persisted = [
    { ...nodes[0], id: 51, position: 1, display_name: 'Tokyo' },
    { ...nodes[1], id: 52, position: 2, display_name: 'Tokyo [2]' },
  ]
  const group = {
    id: 5, client_name: 'owner', routing_preset_id: 1, nodes: persisted,
    shadowrocket_defaults: { grp_removed: { type: 'builtin', policy: 'REJECT' } },
  }
  const preset = { id: 1, enabled: true, groups: [{ key: 'grp_ai', name: 'AI' }, { key: 'grp_video', name: 'YouTube' }] }
  const { state } = await render(Subscriptions, current => {
    current.loading.value = false
    current.personalModalOpen.value = true
    current.editingPersonal.value = group
    current.personalClientName.value = 'owner'
    current.personalName.value = 'iPhone'
    current.personalRoutingPresetID.value = 1
    current.routingPresets.value = [preset]
    current.personalNodes.value = nodes
    current.personalRoutingBindings.value = { grp_ai: [-1, -2] }
  }, { role: 'vip' })
  const requests = []
  globalThis.fetch = async (url, options) => {
    if (url.endsWith('/nodes')) {
      assert.deepEqual(JSON.parse(options.body).nodes.map(node => node.custom_name), ['', ''])
      return Response.json({ personal_subscription: group })
    }
    if (url.endsWith('/routing-bindings')) {
      const body = JSON.parse(options.body)
      requests.push(body)
      if (requests.length === 1) return Response.json({ error: '临时保存失败' }, { status: 503 })
      const defaults = Object.fromEntries(Object.entries(body.shadowrocket_defaults).filter(([, target]) => target !== null))
      return Response.json({ personal_subscription: { ...group, shadowrocket_defaults: defaults } })
    }
    if (options.method === 'PATCH') return Response.json({ personal_subscription: group })
    if (url.endsWith('/routing-presets')) return Response.json({ routing_presets: [preset] })
    if (url.endsWith('/templates')) return Response.json({ templates: [] })
    return Response.json({ personal_subscriptions: [group] })
  }
  state.changePersonalShadowrocketDefault('grp_ai', { type: 'node', node_id: -1 })
  state.changePersonalShadowrocketDefault('grp_video', { type: 'builtin', policy: 'DIRECT' })
  state.changePersonalShadowrocketDefault('grp_removed', null)
  await state.savePersonal()
  assert.equal(state.personalModalOpen.value, true)
  assert.match(state.personalFormError.value, /临时保存失败/)
  assert.equal(state.personalShadowrocketDefaults.value.grp_ai.node_id, 52)
  await state.savePersonal()
  assert.deepEqual(requests, Array.from({ length: 2 }, () => ({
    routing_bindings: { grp_ai: [52, 51], grp_video: [] },
    shadowrocket_defaults: { grp_ai: { type: 'node', node_id: 52 }, grp_video: { type: 'builtin', policy: 'DIRECT' }, grp_removed: null },
  })))
  assert.equal(state.personalModalOpen.value, false)
  assert.deepEqual(state.personalShadowrocketChanges.value, {})
})

test('个人和共享模板选择按客户端类型隔离，停用模板仅保留当前引用', async () => {
  const { state } = await render(Subscriptions, state => {
    state.templates.value = [
      { id: 1, type: 'mihomo', enabled: true },
      { id: 2, type: 'shadowrocket', enabled: true },
      { id: 3, type: 'mihomo', enabled: false },
      { id: 4, type: 'shadowrocket', enabled: false },
      { id: 5, type: 'shadowrocket', enabled: false },
    ]
  }, { role: 'admin' })
  const ids = name => state[name].value.map(value => value.id)
  for (const name of ['selectablePersonalTemplates', 'selectablePlanMihomoTemplates']) assert.deepEqual(ids(name), [1])
  for (const name of ['selectablePersonalShadowrocketTemplates', 'selectablePlanShadowrocketTemplates']) assert.deepEqual(ids(name), [2])
  state.editingPersonal.value = { mihomo_template_id: 3, shadowrocket_template_id: 4 }
  state.editingPlan.value = { mihomo_template_id: 1, shadowrocket_template_id: 5 }
  assert.deepEqual(ids('selectablePersonalTemplates'), [1, 3])
  assert.deepEqual(ids('selectablePersonalShadowrocketTemplates'), [2, 4])
  assert.deepEqual(ids('selectablePlanMihomoTemplates'), [1])
  assert.deepEqual(ids('selectablePlanShadowrocketTemplates'), [2, 5])
})

test('切换模板类型提供对应初始内容，编辑模板保留类型和原文', async () => {
  const conf = '[General]\ndns-server = system\n[Proxy]\n{{PROXIES}}'
  const { state } = await render(Subscriptions, state => {
    state.builtinShadowrocket.value = { name: 'Shadowrocket', conf }
  }, { role: 'admin' })
  state.openCreateTemplate()
  assert.equal(state.templateType.value, 'mihomo')
  assert.match(state.templateContent.value, /dns:/)
  state.templateType.value = 'shadowrocket'
  state.resetTemplateContent()
  assert.equal(state.templateContent.value, conf)
  state.openEditTemplate({ id: 9, name: '自定义', enabled: false, type: 'shadowrocket', content: conf + '\n# preserved' })
  assert.equal(state.templateType.value, 'shadowrocket')
  assert.equal(state.templateContent.value, conf + '\n# preserved')
  assert.equal(state.templateEnabled.value, false)
})

test('复制订阅弹窗默认 Auto，四种格式复制对应 URL，重新打开重置状态', async t => {
  const navigatorDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'navigator')
  const writeText = t.mock.fn(async () => {})
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { clipboard: { writeText } } })
  t.after(() => { if (navigatorDescriptor) Object.defineProperty(globalThis, 'navigator', navigatorDescriptor); else delete globalThis.navigator })
  const { state } = await render(Subscriptions, () => {}, { role: 'vip' })
  const value = { id: 8, subscription_auto_url: 'https://panel.example/sub/one/auto',
    subscription_mihomo_url: 'https://panel.example/sub/one/mihomo', subscription_shadowrocket_url: 'https://panel.example/sub/one/shadowrocket',
    subscription_base64_url: 'https://panel.example/sub/one/base64' }
  state.openSubscriptionLinkModal(value)
  assert.equal(state.subscriptionLinkModalOpen.value, true)
  assert.equal(state.subscriptionLinkFormat.value, 'auto')
  assert.equal(state.subscriptionLinkCopied.value, false)
  assert.equal(writeText.mock.calls.length, 0)
  for (const format of ['auto', 'mihomo', 'shadowrocket', 'base64']) {
    state.subscriptionLinkFormat.value = format
    await state.copySubscriptionLink()
    assert.equal(writeText.mock.calls.at(-1).arguments[0], value[`subscription_${format}_url`])
    assert.equal(state.subscriptionLinkCopied.value, true)
  }
  state.subscriptionLinkModalOpen.value = false
  state.openSubscriptionLinkModal({ ...value, id: 9, subscription_auto_url: 'https://panel.example/sub/two/auto' })
  assert.equal(state.subscriptionLinkFormat.value, 'auto')
  assert.equal(state.subscriptionLinkCopied.value, false)
  await state.copySubscriptionLink()
  assert.equal(writeText.mock.calls.at(-1).arguments[0], 'https://panel.example/sub/two/auto')
  writeText.mock.mockImplementation(async () => { throw new Error('Permission denied') })
  await state.copySubscriptionLink()
  assert.equal(state.subscriptionLinkCopied.value, false)
  assert.match(state.subscriptionLinkError.value, /复制失败/)
  state.openSubscriptionLinkModal(value)
  assert.equal(state.subscriptionLinkError.value, '')
})

test('模板编辑器只显示当前客户端的格式帮助', async () => {
  for (const type of ['mihomo', 'shadowrocket']) {
    const { html } = await render(Subscriptions, state => {
      state.openCreateTemplate()
      state.templateType.value = type
    }, { role: 'admin' })
    if (type === 'mihomo') {
      assert.match(html, /不能包含 proxies、proxy-groups、rule-providers 或 rules/)
      assert.doesNotMatch(html, /保留 \[General\]/)
    } else {
      assert.match(html, /保留 \[General\]/)
      assert.match(html, /\{\{PROXIES\}\}/)
      assert.doesNotMatch(html, /不能包含 proxies、proxy-groups、rule-providers 或 rules/)
    }
  }
})

const subscriberDetail = () => ({
  user_id: 42, username: 'Revenant', plan_id: 9, enabled: true, active: true, status: 'normal',
  traffic_limit_bytes: 100 * 2 ** 30, used_bytes: 6 * 2 ** 30, cycle_started_at: '2026-10-14T03:48:00Z',
  traffic_reset_mode: 'monthly', traffic_reset_day: 14, traffic_reset_time: '00:00',
  node_usage: [
    { published_node_id: 8, name: 'Japan', traffic_multiplier: 0.5, used_bytes: 2 * 2 ** 30 },
    { published_node_id: 3, name: 'Hong Kong', traffic_multiplier: 2, used_bytes: 3 * 2 ** 30 },
  ],
  subscription_auto_url: 'https://panel.example/sub/test-only/auto',
  subscription_base64_url: 'https://panel.example/sub/test-only',
  subscription_mihomo_url: 'https://panel.example/sub/test-only/mihomo',
  subscription_shadowrocket_url: 'https://panel.example/sub/test-only/shadowrocket',
  password_request: { id: 7, status: 'pending' },
})

test('订阅用户使用紧凑卡片显示摘要，不新增拖拽且沿用详情入口', async t => {
  const value = { ...subscriberDetail(), enabled_node_count: 3, expires_at: null, plan_name: 'Friendship' }
  const { html, state } = await render(Subscriptions, state => {
    state.loading.value = false
    state.users.value = [value, { ...value, user_id: 43, username: 'Second' }]
  }, { role: 'admin', currentTab: 'users' })
  assert.equal((html.match(/class="subscription-user-card subscription-overview-card"/g) ?? []).length, 2)
  assert.match(html, /Revenant/); assert.match(html, /Second/)
  for (const label of ['正常', 'Friendship', '已用 / 总量', '6 G / 100 G', '到期时间', '节点数量', '3 个', '查看 / 编辑']) assert.ok(html.includes(label))
  assert.doesNotMatch(html, /<table|draggable=|drag-handle/)
  t.after(() => { globalThis.fetch = originalFetch })
  let requested
  globalThis.fetch = async url => { requested = url; return { ok: true, status: 200, json: async () => ({ user: value }) } }
  await state.openUser(value)
  assert.equal(requested, '/api/admin/subscription/users/42')
  assert.equal(state.userModalOpen.value, true)
  assert.equal(state.selectedUser.value.username, 'Revenant')
})

test('共享订阅卡片把额度和节点数合并摘要，保留编辑删除入口', async () => {
  const { html } = await render(Subscriptions, state => {
    state.loading.value = false
    state.plans.value = [1, 2].map(id => ({ id, name: `Shared ${id}`, enabled: true, traffic_limit_bytes: 100 * 2 ** 30, nodes: [{ id }] }))
  }, { role: 'admin', currentTab: 'plans' })
  assert.match(html, /class="subscription-plan-grid"/)
  assert.equal((html.match(/class="subscription-plan-card subscription-overview-card"/g) ?? []).length, 2)
  assert.equal((html.match(/class="subscription-plan-summary"/g) ?? []).length, 2)
  assert.ok(html.includes('100 G')); assert.ok(html.includes('1 个'))
  assert.equal((html.match(/>编辑</g) ?? []).length, 2)
  assert.equal((html.match(/>删除</g) ?? []).length, 2)
})

test('订阅用户详情按服务端顺序展示已计费流量，历史差额独立显示且不重复乘倍率', async () => {
  const { html, state } = await render(Subscriptions, state => {
    state.loading.value = false
    state.selectedUser.value = subscriberDetail()
    state.userModalOpen.value = true
    state.userResetMode.value = 'monthly'
  }, { role: 'admin' })
  assert.equal(state.historicalUserUsage.value, 2 ** 30)
  assert.ok(html.indexOf('Japan') < html.indexOf('Hong Kong'))
  const markup = html.replace(/ data-v-[\da-z]+/g, '')
  assert.match(markup, /Japan<\/td><td>0.5×<\/td><td>2 G/)
  assert.match(markup, /Hong Kong<\/td><td>2×<\/td><td>3 G/)
  assert.match(markup, /历史 \/ 已移除节点<\/td><td>—<\/td><td>1 G/)
  assert.match(html, /密码重置申请等待审核/)
  assert.match(html, /<details[^>]*subscriber-node-usage/)
  assert.doesNotMatch(html, /<details[^>]*\bopen\b|Subscription URL|预览 Mihomo|重置订阅链接|https:\/\/panel.example/)
  state.selectedUser.value.used_bytes = 0
  state.selectedUser.value.node_usage = []
  assert.equal(state.historicalUserUsage.value, 0)
})

test('订阅用户复用个人订阅四格式复制和二维码数据，不拼接公开路径', async t => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'navigator')
  const writeText = t.mock.fn(async () => {})
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { clipboard: { writeText } } })
  t.after(() => { if (descriptor) Object.defineProperty(globalThis, 'navigator', descriptor); else delete globalThis.navigator })
  const { state } = await render(Subscriptions, () => {}, { role: 'admin' })
  const value = subscriberDetail()
  const links = state.subscriberLinks(value)
  state.openSubscriptionLinkModal(links)
  assert.equal(state.subscriptionLinkFormat.value, 'auto')
  for (const format of ['auto', 'base64', 'mihomo', 'shadowrocket']) {
    state.subscriptionLinkFormat.value = format
    await state.copySubscriptionLink()
    assert.equal(writeText.mock.calls.at(-1).arguments[0], value[`subscription_${format}_url`])
  }
  state.showSubscriptionQR(links)
  assert.equal(state.subscriptionQROpen.value, true)
  assert.deepEqual(state.subscriptionQR.value, { name: value.username, ...Object.fromEntries(
    ['auto', 'base64', 'mihomo', 'shadowrocket'].map(format => [`subscription_${format}_url`, value[`subscription_${format}_url`]])) })
})

test('重置本周期流量需要确认，同时更新总量和节点明细并保留密码审核', async t => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const confirm = t.mock.fn(() => false)
  Object.defineProperty(globalThis, 'window', { configurable: true, value: { confirm } })
  t.after(() => { if (descriptor) Object.defineProperty(globalThis, 'window', descriptor); else delete globalThis.window; globalThis.fetch = originalFetch })
  const { state } = await render(Subscriptions, state => { state.selectedUser.value = subscriberDetail() }, { role: 'admin' })
  const requests = []
  globalThis.fetch = async (url, options = {}) => {
    requests.push([url, options.method])
    const reset = { ...subscriberDetail(), used_bytes: 0, node_usage: [] }
    delete reset.password_request
    return { ok: true, status: 200, json: async () => url.endsWith('/traffic/reset') ? { user: reset } : {
      personal_subscriptions: [], routing_presets: [], templates: [], users: [], plans: [], nodes: [], proxies: [], servers: [],
    } }
  }
  await state.resetUserTraffic()
  assert.equal(requests.length, 0)
  assert.equal(confirm.mock.calls[0].arguments[0], '确定重置 Revenant 的本周期流量吗？')
  confirm.mock.mockImplementation(() => true)
  await state.resetUserTraffic()
  assert.deepEqual(requests[0], ['/api/admin/subscription/users/42/traffic/reset', 'POST'])
  assert.equal(state.selectedUser.value.used_bytes, 0)
  assert.deepEqual(state.selectedUser.value.node_usage, [])
  assert.equal(state.historicalUserUsage.value, 0)
  assert.equal(state.selectedUser.value.password_request.status, 'pending')
})

test('新增规则源默认 text，编辑和预览使用数据库内容且保留自定义 YAML', async () => {
  const providers = [
    { name: 'OpenAI', type: 'http', behavior: 'classical', format: 'text', interval: 86400,
      url: 'https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Surge/OpenAI/OpenAI.list' },
    { name: 'Custom', type: 'http', behavior: 'classical', format: 'yaml', interval: 3600, url: 'https://example.com/custom.yaml' },
  ]
  const preset = { id: 42, name: '数据库中的默认分流', is_default: true, enabled: true, groups: [], rules: ['RULE-SET,OpenAI,DIRECT'], rule_providers: providers }
  const { html, state } = await render(Subscriptions, state => {
    state.routingPresets.value = [preset]
    state.planRoutingPresetID.value = 42
    state.viewSelectedPlanRouting()
  }, { role: 'admin' })
  assert.deepEqual(state.routingPreviewProviders.value, providers)
  assert.deepEqual(state.routingPreviewRules.value, preset.rules)
  assert.match(html, /http · classical · text · 86400 秒/)
  assert.ok(html.includes(providers[0].url))
  assert.match(html, /Shadowrocket 不支持直接使用 Mihomo YAML Rule Provider/)
  state.openEditRoutingPreset(preset)
  assert.deepEqual(state.routingProviders.value, providers)
  state.addRoutingProvider()
  assert.deepEqual(state.routingProviders.value.at(-1), { name: '', url: '', type: 'http', behavior: 'classical', format: 'text', interval: 86400 })
  assert.equal(providers.length, 2)
  assert.equal(state.routingProviders.value[1].format, 'yaml')
  assert.equal(state.routingProviders.value[1].url, providers[1].url)
})

test('复制分流方案直接创建并刷新，失败时保留原列表并显示现有错误', async t => {
  const previousFetch = globalThis.fetch
  t.after(() => { globalThis.fetch = previousFetch })
  const preset = {
    id: 42, name: '个人自用', is_default: false, enabled: true,
    groups: [{ key: 'grp_original', name: '代理', type: 'select', proxies: ['DIRECT'], include_all: true }],
    rule_providers: [{ name: 'OpenAI', type: 'http', behavior: 'classical', format: 'text', interval: 86400, url: 'https://example.com/openai.list' }],
    rules: ['RULE-SET,OpenAI,代理', 'MATCH,代理'],
  }
  const { state } = await render(Subscriptions, state => {
    state.routingPresets.value = [preset]
  }, { role: 'admin' })
  const requests = []
  let copied
  const response = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body })
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options })
    if (url === '/api/admin/subscription/routing-presets' && options.method === 'POST') {
      const body = JSON.parse(options.body)
      copied = { id: 43, is_default: false, ...body, groups: body.groups.map(group => ({ ...group, key: 'grp_copy' })) }
      return response({ routing_preset: copied }, 201)
    }
    const bodies = {
      '/api/personal-subscriptions': { personal_subscriptions: [] },
      '/api/admin/subscription/routing-presets': { routing_presets: [preset, copied] },
      '/api/admin/subscription/templates': { templates: [] },
      '/api/admin/subscription/users': { users: [] },
      '/api/admin/subscription/plans': { plans: [] },
      '/api/admin/subscription/nodes': { nodes: [] },
      '/api/admin/distributable-proxies': { proxies: [] },
      '/api/admin/subscription/relay-servers': { servers: [] },
      '/api/admin/subscription/builtin-mihomo': { name: '内置默认', yaml: 'dns:\n  enable: true' },
      '/api/admin/subscription/builtin-shadowrocket': { name: '内置默认', conf: '[General]\n[Proxy]\n{{PROXIES}}\n[Proxy Group]\n{{PROXY_GROUPS}}\n[Rule]\n{{RULES}}' },
    }
    return response(bodies[url])
  }

  await state.copyRoutingPreset(preset)

  const requestBody = JSON.parse(requests[0].options.body)
  assert.equal(requests[0].url, '/api/admin/subscription/routing-presets')
  assert.equal(requests[0].options.method, 'POST')
  assert.equal(requestBody.name, '个人自用 - 副本')
  assert.equal('id' in requestBody, false)
  assert.equal('is_default' in requestBody, false)
  assert.deepEqual(state.routingPresets.value.map(value => value.name), ['个人自用', '个人自用 - 副本'])
  assert.equal(state.routingModalOpen.value, false)
  state.openEditRoutingPreset(state.routingPresets.value[1])
  assert.equal(state.routingName.value, '个人自用 - 副本')
  assert.deepEqual(state.routingGroups.value, copied.groups)
  assert.deepEqual(state.routingProviders.value, copied.rule_providers)
  assert.equal(state.routingRulesText.value, copied.rules.join('\n'))

  const beforeFailure = JSON.parse(JSON.stringify(state.routingPresets.value))
  state.routingModalOpen.value = false
  globalThis.fetch = async () => response({ error: '创建副本失败' }, 500)
  await state.copyRoutingPreset(preset)
  assert.deepEqual(state.routingPresets.value, beforeFailure)
  assert.equal(state.routingModalOpen.value, false)
  assert.equal(state.error.value, '创建副本失败')
})
