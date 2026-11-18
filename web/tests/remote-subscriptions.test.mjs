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

test('手动来源可无地址创建并导入文件，显示结果和解析警告', async () => {
  const manual = { ...source, mode: 'manual', name: 'Manual', warnings: ['发现重名节点'], node_count: 2 }
  const calls = []
  globalThis.fetch = async (path, options = {}) => {
    calls.push([path, options])
    if (path.endsWith('/import')) return json({ source: manual })
    if (options.method === 'POST') return json({ source: manual })
    return json({ sources: [manual] })
  }
  const { state } = await render()
  state.openForm(); state.url.value = 'https://example.com/?token=synthetic'; state.userAgent.value = 'Previous'; state.mode.value = 'manual'; state.name.value = 'Manual'
  await state.save()
  assert.deepEqual(JSON.parse(calls[0][1].body), { name: 'Manual', url: '', user_agent: '', enabled: true, interval_seconds: 3600, mode: 'manual' })
  const file = new File(['trojan://synthetic@example.com:443#Node'], 'manual.txt', { type: 'text/plain' })
  const input = { files: [file], value: 'selected' }
  await state.importFile(manual, { target: input })
  const upload = calls.find(([path]) => path.endsWith('/import'))
  assert.equal(upload[0], '/api/remote-subscriptions/1/import')
  assert.equal(await upload[1].body.get('file').text(), await file.text())
  assert.equal(input.value, '')
  assert.match(state.importNotice.value, /导入 2 个节点/)
  assert.deepEqual(state.sources.value[0].warnings, ['发现重名节点'])
  const { html } = await render(state => { state.loading.value = false; state.sources.value = [manual] })
  assert.match(html, /手动导入来源/)
  assert.match(html, /导入文件/)
  assert.doesNotMatch(html, /立即刷新/)
})

test('文件导入拒绝空文件、超大文件并显示服务端错误', async () => {
  let calls = 0
  globalThis.fetch = async () => { calls++; return new Response(JSON.stringify({ error: '文件内容无效或没有可用节点' }), { status: 400 }) }
  const { state } = await render()
  for (const file of [new File([], 'empty.txt'), new File([new Uint8Array(4 * 1024 * 1024 + 1)], 'large.txt')]) {
    await state.importFile(source, { target: { files: [file], value: 'selected' } })
    assert.match(state.error.value, /非空且不超过/)
  }
  assert.equal(calls, 0)
  await state.importFile(source, { target: { files: [new File(['bad'], 'bad.txt')], value: 'selected' } })
  assert.equal(calls, 1)
  assert.equal(state.error.value, '文件内容无效或没有可用节点')
})
