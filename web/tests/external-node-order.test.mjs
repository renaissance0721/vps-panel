import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, Manager
const originalFetch = globalThis.fetch
before(async () => {
  loader = await createServer({
    configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ default: Manager } = await loader.ssrLoadModule('/src/components/proxy/ExternalNodeManager.vue'))
})
after(async () => { globalThis.fetch = originalFetch; await loader?.close() })

const nodes = () => [3, 2, 1].map(id => ({ id, node_role: 'direct', name: `Node ${id}`, visibility: 'public', protocol: 'vless', host: 'example.com', port: 443, owned_by_me: false }))
async function render() {
  const setup = Manager.setup
  let state
  Manager.setup = (props, context) => {
    state = setup(props, context)
    state.landings.value = nodes(); state.loading.value = false
    return state
  }
  try { return { html: await renderToString(createSSRApp(Manager)), state } }
  finally { Manager.setup = setup }
}

test('公开非本人节点有排序手柄和查看入口，但无编辑删除权限', async () => {
  const { html } = await render()
  assert.match(html, /aria-label="排序"/)
  assert.equal((html.match(/aria-label="拖动外部节点排序"/g) ?? []).length, 3)
  assert.equal((html.match(/draggable="true"/g) ?? []).length, 3)
  assert.doesNotMatch(html, />编辑<|>删除</)
  assert.match(html, />查看</)
})

test('drop 立即乐观移动，串行提交相邻移动后重载；保存中禁止第二次排序', async () => {
  const { state } = await render()
  const calls = []
  let resolveFirst
  globalThis.fetch = async (url, options) => {
    calls.push([url, options.method, options.body])
    if (calls.length === 1) await new Promise(resolve => { resolveFirst = resolve })
    return { ok: true, status: options.method === 'POST' ? 204 : 200, json: async () => ({ landings: [nodes()[2], nodes()[0], nodes()[1]] }) }
  }
  state.draggedID.value = 1
  const save = state.dropExternalNode(3)
  assert.deepEqual(state.landings.value.map(n => n.id), [1, 3, 2])
  assert.equal(state.reorderingID.value, 1)
  assert.equal(calls.length, 1)
  state.draggedID.value = 2
  await state.dropExternalNode(1)
  assert.equal(calls.length, 1)
  resolveFirst(); await save
  assert.deepEqual(calls.map(c => c[0]), ['/api/landings/1/reorder', '/api/landings/1/reorder', '/api/landings'])
  assert.deepEqual(calls.slice(0, 2).map(c => JSON.parse(c[2])), [{ direction: 'up' }, { direction: 'up' }])
  assert.equal(state.reorderingID.value, null)
  assert.equal(state.error.value, '')
})

test('失败后恢复服务端顺序并展示错误，不因 dragover 提前保存', async () => {
  const { state } = await render()
  const calls = []
  globalThis.fetch = async (url, options) => {
    calls.push(url)
    if (options.method === 'POST') return { ok: false, status: 500, json: async () => ({ error: '排序保存失败' }) }
    return { ok: true, status: 200, json: async () => ({ landings: nodes() }) }
  }
  state.draggedID.value = 1
  let prevented = false
  state.dragOver({ preventDefault() { prevented = true } }, 3)
  assert.equal(prevented, true); assert.equal(state.dropTargetID.value, 3); assert.equal(calls.length, 0)
  await state.dropExternalNode(3)
  assert.deepEqual(calls, ['/api/landings/1/reorder', '/api/landings'])
  assert.deepEqual(state.landings.value.map(n => n.id), [3, 2, 1])
  assert.equal(state.error.value, '排序保存失败')
  assert.equal(state.draggedID.value, null); assert.equal(state.dropTargetID.value, null); assert.equal(state.reorderingID.value, null)
})
