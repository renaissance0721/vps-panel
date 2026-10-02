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

test('个人订阅卡片只保留复制、二维码、编辑和删除，其他预览不受影响', async () => {
  const { html } = await render(Subscriptions, state => {
    state.loading.value = false
    state.personalSubscriptions.value = [{ id: 1, name: 'iPhone', enabled: true, client_name: 'refrain', nodes: [], routing_preset_name: '个人自用' }]
  }, { role: 'vip' })
  for (const label of ['复制链接', '二维码', '编辑', '删除']) assert.match(html, new RegExp(`>${label}<`))
  assert.doesNotMatch(html, />预览<|重置链接|个人订阅 Mihomo Preview/)
  const source = await readFile(new URL('../src/views/SubscriptionManagementView.vue', import.meta.url), 'utf8')
  assert.doesNotMatch(source, /personalPreviewOpen|personalPreviewYAML|previewPersonal|regeneratePersonalToken/)
  assert.match(source, /\/api\/admin\/subscription\/users\/\$\{value.user_id\}\/mihomo-preview/)
})
