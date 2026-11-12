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
  ;({ default: View } = await loader.ssrLoadModule('/src/views/RemoteSubscriptionsView.vue'))
})
after(async () => { globalThis.fetch = originalFetch; globalThis.window = originalWindow; await loader?.close() })

async function render(populate = () => {}) {
  const setup = View.setup; let state
  View.setup = (props, context) => { state = setup(props, context); populate(state); return state }
  try { return { html: await renderToString(createSSRApp(View)), state } } finally { View.setup = setup }
}

const source = { id: 1, name: 'Airport', user_agent: 'UA', enabled: true, interval_seconds: 300, last_attempt_at: null, last_success_at: null, last_error: '刷新失败', warnings: ['不支持的节点'], node_count: 1, upload: 10, download: 20, total: 100, expire: null, refreshing: false }
const json = value => new Response(JSON.stringify(value), { status: 200 })

test('机场订阅展示状态、节点数量、错误及操作', async () => {
  const { html } = await render(state => { state.loading.value = false; state.sources.value = [source] })
  for (const text of ['Airport', '节点 1', '刷新失败', '不支持的节点', '查看节点', '立即刷新', '编辑', '删除']) assert.ok(html.includes(text), text)
})

test('编辑保留已保存地址，替换地址和测试使用当前表单', async () => {
  const calls = []
  globalThis.fetch = async (path, options = {}) => {
    calls.push([path, options.method, options.body && JSON.parse(options.body)])
    return json(path.endsWith('/test') ? { preview: { usable: true, nodes: [{ name: 'Node', protocol: 'trojan' }], warnings: [] } } : { sources: [source] })
  }
  const { state } = await render()
  state.openForm(source)
  assert.equal(state.url.value, '')
  state.userAgent.value = 'New UA'
  await state.testSource()
  assert.deepEqual(calls[0], ['/api/remote-subscriptions/1/test', 'POST', { user_agent: 'New UA' }])
  assert.equal(state.preview.value.nodes[0].protocol, 'trojan')
  state.url.value = 'https://example.com/?token=synthetic'
  await state.save()
  assert.deepEqual(calls[1], ['/api/remote-subscriptions/1', 'PUT', { name: 'Airport', url: 'https://example.com/?token=synthetic', user_agent: 'New UA', enabled: true, interval_seconds: 300 }])
  assert.equal(state.url.value, '')
})

test('节点预览和删除使用对应来源接口', async () => {
  const calls = []
  globalThis.window = { confirm: () => true }
  globalThis.fetch = async (path, options = {}) => {
    calls.push([path, options.method])
    return json(path.endsWith('/nodes') ? { nodes: [{ id: 9, name: 'Node', protocol: 'ss', format: 'uri', position: 0 }] } : { sources: [] })
  }
  const { state } = await render()
  await state.showNodes(source)
  assert.equal(state.nodes.value[0].name, 'Node')
  await state.remove(source)
  assert.deepEqual(calls.slice(0, 2), [['/api/remote-subscriptions/1/nodes', undefined], ['/api/remote-subscriptions/1', 'DELETE']])
})
