import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
let loader, View
before(async () => {
  globalThis.window = { location: { origin: 'https://panel.example.com' } }
  loader = await createServer({ configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()], resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } }, server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] } })
  ;({ default: View } = await loader.ssrLoadModule('/src/views/MCPSettingsView.vue'))
})
after(async () => { await loader?.close(); delete globalThis.window })
async function mount() {
  const original = View.setup; let bindings
  View.setup = (props, context) => { bindings = original(props, context); return bindings }
  try { const html = await renderToString(createSSRApp(View)); return { bindings, html } } finally { View.setup = original }
}
test('MCP 设置仅管理员可见，地址和说明随当前域名生成', async () => {
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  assert.match(app, /MCPSettingsView v-if="currentPage === 'settings' && state\.user\?\.role === 'admin'"/)
  const { bindings, html } = await mount()
  assert.equal(bindings.enabled.value, false)
  assert.equal(bindings.endpoint.value, 'https://panel.example.com/mcp')
  assert.match(html, /AI \/ MCP 集成/)
  assert.match(bindings.command.value, /--bearer-token-env-var VPS_PANEL_MCP_TOKEN/)
})
test('令牌只显示一次并可撤销；失败显示错误', async () => {
  const original = globalThis.fetch; const requests = []
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options })
    if (options.method === 'DELETE') return new Response(null, { status: 204 })
    if (options.method === 'POST') return new Response(JSON.stringify({ token: 'one-time-secret' }), { status: 201 })
    if (url.endsWith('/settings')) return new Response(JSON.stringify({ mcp_enabled: false }))
    if (url.endsWith('/tokens')) return new Response(JSON.stringify({ tokens: [] }))
    return new Response(JSON.stringify({ logs: [] }))
  }
  try {
    const { bindings } = await mount(); await bindings.load(); bindings.name.value = 'test'; await bindings.create()
    assert.equal(bindings.secret.value, 'one-time-secret')
    const posted = JSON.parse(requests.find(item => item.options.method === 'POST').options.body)
    assert.equal(posted.scope, 'read')
    bindings.close(); assert.equal(bindings.secret.value, '')
    await bindings.revoke(7); assert.ok(requests.some(item => item.url.endsWith('/tokens/7') && item.options.method === 'DELETE'))
    globalThis.fetch = async () => new Response(JSON.stringify({ error: '请求失败' }), { status: 500 })
    await bindings.change(true); assert.equal(bindings.enabled.value, false); assert.equal(bindings.error.value, '请求失败')
  } finally { globalThis.fetch = original }
})
