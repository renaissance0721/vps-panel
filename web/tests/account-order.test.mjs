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

test('个人订阅卡片提供 Shadowrocket 链接与配置预览，既有操作保持可用', async () => {
  const { html } = await render(Subscriptions, state => {
    state.loading.value = false
    state.personalSubscriptions.value = [{ id: 1, name: 'iPhone', enabled: true, client_name: 'refrain', nodes: [], routing_preset_name: '个人自用' }]
  }, { role: 'vip' })
  for (const label of ['复制链接', '复制 Shadowrocket URL', '预览 Shadowrocket', '二维码', '编辑', '删除']) assert.match(html, new RegExp(`>${label}<`))
  assert.doesNotMatch(html, />预览<|重置链接|个人订阅 Mihomo Preview/)
  const source = await readFile(new URL('../src/views/SubscriptionManagementView.vue', import.meta.url), 'utf8')
  assert.doesNotMatch(source, /personalPreviewOpen|personalPreviewYAML|regeneratePersonalToken/)
  assert.match(source, /\/api\/personal-subscriptions\/\$\{value.id\}\/shadowrocket-preview/)
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

test('Shadowrocket 预览读取 conf 并显示，接口错误保留可读原因', async () => {
  const { state } = await render(Subscriptions, () => {}, { role: 'vip' })
  const calls = []
  globalThis.fetch = async (url) => {
    calls.push(url)
    return { ok: true, status: 200, json: async () => ({ conf: '[Rule]\nFINAL,DIRECT\n' }) }
  }
  await state.previewPersonalShadowrocket({ id: 8 })
  assert.deepEqual(calls, ['/api/personal-subscriptions/8/shadowrocket-preview'])
  assert.equal(state.shadowrocketPreviewOpen.value, true)
  assert.equal(state.shadowrocketPreviewConf.value, '[Rule]\nFINAL,DIRECT\n')
  state.shadowrocketPreviewOpen.value = false
  globalThis.fetch = async () => ({ ok: false, status: 422, json: async () => ({ error: '当前规则无法转换为 Shadowrocket 格式' }) })
  await state.previewPersonalShadowrocket({ id: 8 })
  assert.equal(state.shadowrocketPreviewOpen.value, false)
  assert.equal(state.error.value, '当前规则无法转换为 Shadowrocket 格式')
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
