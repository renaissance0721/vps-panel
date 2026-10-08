import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, Accounts, Subscriptions, drag
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
    state.currentTab.value = 'nodes'
    state.nodes.value = publishedNodes()
  }, { role: 'admin' })
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

test('个人订阅卡片保留四个主要操作并移除独立 Shadowrocket 操作', async () => {
  const { html } = await render(Subscriptions, state => {
    state.loading.value = false
    state.personalSubscriptions.value = [{ id: 1, name: 'iPhone', enabled: true, client_name: 'refrain', nodes: [], routing_preset_name: '个人自用' }]
  }, { role: 'vip' })
  for (const label of ['复制链接', '二维码', '编辑', '删除']) assert.match(html, new RegExp(`>${label}<`))
  assert.doesNotMatch(html, /复制 Shadowrocket URL|预览 Shadowrocket|>预览<|重置链接|个人订阅 Mihomo Preview/)
  const source = await readFile(new URL('../src/views/SubscriptionManagementView.vue', import.meta.url), 'utf8')
  assert.doesNotMatch(source, /personalPreviewOpen|personalPreviewYAML|regeneratePersonalToken/)
  assert.doesNotMatch(source, /shadowrocket-preview/)
  assert.match(source, /\/api\/admin\/subscription\/users\/\$\{value.user_id\}\/mihomo-preview/)
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
  state.openPersonalLinkModal(value)
  assert.equal(state.personalLinkModalOpen.value, true)
  assert.equal(state.personalLinkFormat.value, 'auto')
  assert.equal(state.personalLinkCopied.value, false)
  assert.equal(writeText.mock.calls.length, 0)
  for (const format of ['auto', 'mihomo', 'shadowrocket', 'base64']) {
    state.personalLinkFormat.value = format
    await state.copyPersonalLink()
    assert.equal(writeText.mock.calls.at(-1).arguments[0], value[`subscription_${format}_url`])
    assert.equal(state.personalLinkCopied.value, true)
  }
  state.personalLinkModalOpen.value = false
  state.openPersonalLinkModal({ ...value, id: 9, subscription_auto_url: 'https://panel.example/sub/two/auto' })
  assert.equal(state.personalLinkFormat.value, 'auto')
  assert.equal(state.personalLinkCopied.value, false)
  await state.copyPersonalLink()
  assert.equal(writeText.mock.calls.at(-1).arguments[0], 'https://panel.example/sub/two/auto')
  writeText.mock.mockImplementation(async () => { throw new Error('Permission denied') })
  await state.copyPersonalLink()
  assert.equal(state.personalLinkCopied.value, false)
  assert.match(state.personalLinkError.value, /复制失败/)
  state.openPersonalLinkModal(value)
  assert.equal(state.personalLinkError.value, '')
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
