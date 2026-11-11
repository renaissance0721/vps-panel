import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, View
const originalFetch = globalThis.fetch
const originalWindow = globalThis.window
before(async () => {
  loader = await createServer({ configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()], resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } }, server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] } })
  ;({ default: View } = await loader.ssrLoadModule('/src/views/SubscriptionManagementView.vue'))
})
after(async () => { globalThis.fetch = originalFetch; globalThis.window = originalWindow; await loader?.close() })
async function mount() {
  const setup = View.setup; let state
  View.setup = (props, context) => { state = setup(props, context); return state }
  try { await renderToString(createSSRApp(View)); return state } finally { View.setup = setup }
}
const json = value => new Response(JSON.stringify(value), { status: 200 })

test('INI 草稿导入、模式切换、预览和保存使用同一份内容', async () => {
  const calls = []
  let confirms = 0
  globalThis.window = { confirm: () => { confirms++; return true } }
  globalThis.fetch = async (url, options = {}) => {
    calls.push([url, options])
    if (url.endsWith('/routing-ini/preview')) return json({ result: { diagnostics: [], warnings: [] }, changes: { groups_added: ['Group'], groups_removed: [], groups_changed: [], providers_added: [], providers_removed: [], rules_before: [], rules_after: ['MATCH,Group'], invalid_binding_groups: [] } })
    if (url.endsWith('/routing-ini')) return json({ saved: true, id: 1 })
    if (url.endsWith('/personal-subscriptions')) return json({ personal_subscriptions: [] })
    if (url.endsWith('/routing-presets')) return json({ routing_presets: [] })
    if (url.endsWith('/templates')) return json({ templates: [] })
    throw new Error(`unexpected URL ${url}`)
  }
  const state = await mount()
  state.openCreateRoutingPreset()
  state.routingName.value = 'Test'
  await state.switchRoutingEditor('ini')
  const source = '[custom]\ncustom_proxy_group=Group`select`[]DIRECT\nruleset=Group,[]FINAL\n'
  await state.importRoutingINI({ target: { files: [{ size: source.length, arrayBuffer: async () => new TextEncoder().encode(source) }], value: 'routing.ini' } })
  await state.switchRoutingEditor('visual')
  await state.switchRoutingEditor('ini')
  assert.equal(state.routingINI.value, source)
  assert.ok(confirms >= 1)
  await state.previewRoutingINI()
  assert.equal(state.routingINIPreview.value.changes.groups_added[0], 'Group')
  await state.saveRoutingINI()
  const submitted = calls.find(([url]) => url.endsWith('/routing-ini'))
  assert.equal(JSON.parse(submitted[1].body).source, source)
  assert.equal(state.routingModalOpen.value, false)
})

test('校验错误阻止保存，过大的文件不覆盖草稿', async () => {
  globalThis.window = { confirm: () => true }
  globalThis.fetch = async () => json({ result: { diagnostics: [{ line: 2, directive: 'invalid', message: '不支持' }], warnings: [] }, changes: { groups_added: [], groups_removed: [], groups_changed: [], providers_added: [], providers_removed: [], rules_before: [], rules_after: [], invalid_binding_groups: [] } })
  const state = await mount(); state.openCreateRoutingPreset(); state.routingName.value = 'Test'; await state.switchRoutingEditor('ini')
  const before = state.routingINI.value
  await state.importRoutingINI({ target: { files: [{ size: 10241, text: async () => 'bad' }], value: 'large.ini' } })
  assert.equal(state.routingINI.value, before)
  await state.importRoutingINI({ target: { files: [{ size: 1, arrayBuffer: async () => new Uint8Array([255]) }], value: 'invalid.ini' } })
  assert.equal(state.routingINI.value, before)
  assert.match(state.routingFormError.value, /UTF-8/)
  await state.previewRoutingINI(); await state.saveRoutingINI()
  assert.match(state.routingFormError.value, /验证/)
  assert.equal(state.routingModalOpen.value, true)
})

test('INI 导出包含当前草稿并释放下载 URL', async () => {
  const oldDocument = globalThis.document
  const oldCreate = URL.createObjectURL
  const oldRevoke = URL.revokeObjectURL
  let exported, revoked, clicked = false
  try {
    globalThis.document = { createElement: () => ({ click() { clicked = true } }) }
    URL.createObjectURL = blob => { exported = blob; return 'blob:test' }
    URL.revokeObjectURL = value => { revoked = value }
    const state = await mount(); state.openCreateRoutingPreset(); state.routingINI.value = '[custom]\n# draft\n'; state.exportRoutingINI()
    assert.equal(await exported.text(), '[custom]\n# draft\n')
    assert.equal(revoked, 'blob:test')
    assert.equal(clicked, true)
  } finally { globalThis.document = oldDocument; URL.createObjectURL = oldCreate; URL.revokeObjectURL = oldRevoke }
})
