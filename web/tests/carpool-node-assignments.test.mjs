import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, View, Portal, useUserManagement
const originalFetch = globalThis.fetch
before(async () => {
  loader = await createServer({ configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] } })
  ;({ default: View } = await loader.ssrLoadModule('/src/views/CarpoolPanelView.vue'))
  ;({ default: Portal } = await loader.ssrLoadModule('/src/views/CarpoolPortalView.vue'))
  ;({ useUserManagement } = await loader.ssrLoadModule('/src/composables/useUserManagement.ts'))
})
after(async () => { globalThis.fetch = originalFetch; await loader?.close() })
const sources = [
  { source_type: 'proxy', source_id: 12, display_name: 'Tokyo · Reality', server_name: 'Tokyo', proxy_name: 'Reality', protocol: 'vless', client: null },
  { source_type: 'relay', source_id: 35, display_name: 'Transit', server_name: 'Frankfurt', proxy_name: 'Tokyo · Reality', protocol: 'vless', client: null },
]
const detail = () => ({ user: { id: 2, username: 'Alice', role: 'carpool' }, nodes: [], available_sources: sources, relays: [], password_request: null })

test('添加节点窗口实际渲染分组和中转入口与目标', async () => {
  const setup = View.setup
  View.setup = (props, ctx) => { const state = setup(props, ctx); state.users.value = [detail().user]; state.detail.value = detail(); state.loading.value = false; state.model.openCreate(); return state }
  try {
    const html = await renderToString(createSSRApp(View))
    for (const expected of ['直连节点', '中转节点', '[直连]', '[中转]', 'Frankfurt', 'Tokyo', 'Reality']) assert.ok(html.includes(expected), expected)
  } finally { View.setup = setup }
})

test('选择中转或直连时按来源类型提交，并独立于已有客户端判断候选', async () => {
  const calls = []
  globalThis.fetch = async (url, options) => { calls.push({ url, options }); return { ok: true, status: 200, json: async () => detail() } }
  const m = useUserManagement(); m.selectedUserID.value = 2; m.detail.value = detail()
  for (const source of sources) {
    m.openCreate(); m.selectedNodeSource.value = `${source.source_type}:${source.source_id}`; m.selectCreateNode(); await m.saveNode()
    const request = calls.findLast(call => call.options.method === 'POST')
    const body = JSON.parse(request.options.body)
    assert.equal(body.source_type, source.source_type); assert.equal(body.source_id, source.source_id)
  }
})


test('门户实际渲染共享 Client 的两个入口，复制请求使用 assignment ID', async () => {
  const nodes = [
    { id: 100, client_id: 7, source_type: 'proxy', source_id: 12, display_name: 'Tokyo', server_name: 'Tokyo', proxy_name: 'Reality', protocol: 'vless', status: 'normal', effective_enabled: true, traffic_used_bytes: 0, traffic_limit_bytes: null, expires_at: null, billing_period_months: null },
    { id: 101, client_id: 7, source_type: 'relay', source_id: 35, display_name: 'Transit', server_name: 'Frankfurt', proxy_name: 'Tokyo · Reality', protocol: 'vless', status: 'relay_disabled', effective_enabled: false, traffic_used_bytes: 0, traffic_limit_bytes: null, expires_at: null, billing_period_months: null },
  ]
  let state; const setup = Portal.setup
  Portal.setup = (props, ctx) => { state = setup(props, ctx); state.nodes.value = nodes; state.loading.value = false; return state }
  try {
    const html = await renderToString(createSSRApp(Portal, { user: detail().user }))
    assert.ok(html.includes('Tokyo')); assert.ok(html.includes('Transit')); assert.ok(html.includes('中转已停用'))
    let requested = ''; let copied = ''
    globalThis.fetch = async url => { requested = url; return { ok: true, status: 200, json: async () => ({ share: { uri: 'vless://example', protocol: 'vless' } }) } }
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'navigator')
    Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { clipboard: { writeText: async text => { copied = text } } } })
    try { await state.copyNode(nodes[1]); assert.equal(requested, '/api/me/nodes/101/share'); assert.equal(copied, 'vless://example') }
    finally { if (descriptor) Object.defineProperty(globalThis, 'navigator', descriptor); else delete globalThis.navigator }
  } finally { Portal.setup = setup }
})
