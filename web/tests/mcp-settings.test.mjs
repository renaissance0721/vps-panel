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
async function mount(prepare = () => {}) {
  const original = View.setup; let bindings
  View.setup = (props, context) => { bindings = original(props, context); prepare(bindings); return bindings }
  try { const html = await renderToString(createSSRApp(View)); return { bindings, html } } finally { View.setup = original }
}
test('首次展示弹窗明确提示 AI 对话历史风险并保留原始命令入口', async () => {
  const { bindings, html } = await mount(value => { value.open.value = true; value.secret.value = 'temporary-secret' })
  assert.match(html, /AI 对话历史/)
  assert.match(html, /受信任的个人环境/)
  assert.match(html, /复制含令牌的 AI 安装提示词/)
  assert.match(html, /复制带令牌的原始配置命令/)
  assert.doesNotMatch(bindings.agentPrompt.value, /temporary-secret/)
})
test('MCP 设置仅管理员可见，地址和说明随当前域名生成', async () => {
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  assert.match(app, /MCPSettingsView v-if="currentPage === 'settings' && state\.user\?\.role === 'admin'"/)
  const { bindings, html } = await mount()
  assert.equal(bindings.enabled.value, false)
  assert.equal(bindings.endpoint.value, 'https://panel.example.com/mcp')
  assert.match(html, /AI \/ MCP 集成/)
  assert.equal(bindings.selectedClient.value, 'codex')
  assert.equal(bindings.selectedPlatform.value, 'windows')
  assert.match(html, /复制给 AI 的安装提示词/)
  assert.match(html, /复制原始配置命令/)
  assert.match(bindings.connection.value, /--bearer-token-env-var VPS_PANEL_MCP_TOKEN/)
  assert.notEqual(bindings.agentPrompt.value, bindings.connection.value)
  assert.ok(bindings.agentPrompt.value.includes(bindings.connection.value))
  bindings.selectedClient.value = 'claude'
  bindings.selectedPlatform.value = 'unix'
  assert.match(bindings.connection.value, /claude mcp add --transport http/)
  assert.match(bindings.agentPrompt.value, /Claude Code/)
  assert.match(bindings.agentPrompt.value, /macOS \/ Linux Bash/)
  bindings.selectedClient.value = 'cursor'
  assert.equal(JSON.parse(bindings.connection.value).mcpServers['vps-panel'].headers.Authorization, 'Bearer ${env:VPS_PANEL_MCP_TOKEN}')
  assert.match(bindings.agentPrompt.value, /合并到 Cursor/)
})
test('令牌只显示一次并可撤销；失败显示错误', async () => {
  const original = globalThis.fetch; const requests = []
  const originalNavigator = Object.getOwnPropertyDescriptor(globalThis, 'navigator')
  const clipboard = []
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { clipboard: { writeText: async value => { clipboard.push(value) } } } })
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options })
    if (options.method === 'DELETE') return new Response(null, { status: 204 })
    if (options.method === 'POST') return new Response(JSON.stringify({ token: 'one-time-secret' }), { status: 201 })
    if (options.method === 'PATCH') return new Response(options.body)
    if (url.endsWith('/settings')) return new Response(JSON.stringify({ mcp_enabled: false }))
    if (url.endsWith('/tokens')) return new Response(JSON.stringify({ tokens: [] }))
    return new Response(JSON.stringify({ logs: [] }))
  }
  try {
    const { bindings } = await mount(); await bindings.load(); bindings.name.value = 'test'; await bindings.create()
    assert.equal(bindings.secret.value, 'one-time-secret')
    assert.equal(clipboard.length, 0)
    assert.doesNotMatch(bindings.connection.value, /one-time-secret/)
    assert.doesNotMatch(bindings.agentPrompt.value, /one-time-secret/)
    await bindings.copy(bindings.agentPrompt.value)
    assert.doesNotMatch(clipboard[0], /one-time-secret/)
    assert.match(clipboard[0], /get_panel_info/)
    await bindings.copyAgentPromptWithToken()
    assert.match(clipboard[1], /one-time-secret/)
    assert.match(clipboard[1], /list_servers/)
    await bindings.copyConnectionWithToken()
    assert.match(clipboard[2], /one-time-secret/)
    assert.doesNotMatch(clipboard[2], /get_panel_info/)
    const posted = JSON.parse(requests.find(item => item.options.method === 'POST').options.body)
    assert.equal(posted.scope, 'read')
    bindings.close(); assert.equal(bindings.secret.value, '')
    await bindings.copyConnectionWithToken(); await bindings.copyAgentPromptWithToken(); assert.equal(clipboard.length, 3)
    const refreshed = await mount(); assert.equal(refreshed.bindings.secret.value, '')
    assert.doesNotMatch(refreshed.bindings.agentPrompt.value, /one-time-secret/)
    await bindings.revoke(7); assert.ok(requests.some(item => item.url.endsWith('/tokens/7') && item.options.method === 'DELETE'))
    await bindings.change(true); assert.equal(bindings.enabled.value, true)
    await bindings.change(false); assert.equal(bindings.enabled.value, false)
    navigator.clipboard.writeText = async () => { throw new Error('clipboard unavailable') }
    await bindings.copy(bindings.agentPrompt.value)
    assert.equal(bindings.error.value, '复制失败，请检查浏览器的剪贴板权限。')
    globalThis.fetch = async () => new Response(JSON.stringify({ error: '请求失败' }), { status: 500 })
    await bindings.change(true); assert.equal(bindings.enabled.value, false); assert.equal(bindings.error.value, '请求失败')
  } finally { globalThis.fetch = original; if (originalNavigator) Object.defineProperty(globalThis, 'navigator', originalNavigator); else delete globalThis.navigator }
})
