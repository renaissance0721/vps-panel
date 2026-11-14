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
  const { bindings, html } = await mount(value => { value.open.value = true; value.creationClient.value = 'codex'; value.creationPlatform.value = 'windows'; value.secret.value = 'temporary-secret' })
  assert.match(html, /AI 对话历史/)
  assert.match(html, /工具执行记录/)
  assert.match(html, /受信任的个人环境/)
  assert.match(html, /复制含令牌的 AI 安装提示词/)
  assert.match(html, /复制带令牌的连接命令/)
  assert.match(html, /含令牌的连接配置/)
  assert.match(html, /temporary-secret/)
  assert.doesNotMatch(bindings.agentPrompt.value, /temporary-secret/)
})
test('MCP 设置仅管理员可见，地址和说明随当前域名生成', async () => {
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  assert.match(app, /v-if="currentPage === 'settings' && state\.user\?\.role === 'admin'" class="settings-page"[\s\S]*<MCPSettingsView \/>/)
  const { bindings, html } = await mount()
  assert.equal(bindings.enabled.value, false)
  assert.equal(bindings.endpoint.value, 'https://panel.example.com/mcp')
  assert.match(html, /AI \/ MCP 集成/)
  assert.equal(bindings.selectedClient.value, null)
  assert.equal(bindings.selectedPlatform.value, 'windows')
  assert.doesNotMatch(html, /原始配置预览/)
  const selected = await mount(value => { value.selectedClient.value = 'codex' })
  assert.match(selected.html, /复制给 AI 的安装提示词/)
  assert.match(selected.html, /复制原始配置命令/)
  assert.match(selected.html, /原始配置预览/)
  assert.match(selected.html, /为 Codex 创建令牌/)
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
    const { bindings } = await mount(); await bindings.load(); bindings.startCreate('codex'); bindings.name.value = 'test'; await bindings.create()
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
    assert.match(clipboard[1], /已有 VPS_PANEL_TOKEN 时继续使用它/)
    assert.match(clipboard[1], /必要时可在工具参数中使用/)
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
    await bindings.changeWrite(true); assert.equal(bindings.writeEnabled.value, true)
    await bindings.changeWrite(false); assert.equal(bindings.writeEnabled.value, false)
    await bindings.copy(bindings.endpoint.value); assert.equal(clipboard.at(-1), bindings.endpoint.value)
    assert.equal(bindings.copyStatus.value, '已复制到剪贴板。')
    navigator.clipboard.writeText = async () => { throw new Error('clipboard unavailable') }
    await bindings.copy(bindings.agentPrompt.value)
    assert.equal(bindings.error.value, '复制失败，请检查浏览器的剪贴板权限。')
    globalThis.fetch = async () => new Response(JSON.stringify({ error: '请求失败' }), { status: 500 })
    await bindings.change(true); assert.equal(bindings.enabled.value, false); assert.equal(bindings.error.value, '请求失败')
  } finally { globalThis.fetch = original; if (originalNavigator) Object.defineProperty(globalThis, 'navigator', originalNavigator); else delete globalThis.navigator }
})
test('读写令牌直接授权管理，保留历史审计并移除审批界面', async () => {
  const original = globalThis.fetch; const requests = []
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options })
    if (url.endsWith('/tokens') && options.method === 'POST') return new Response(JSON.stringify({ token: 'secret' }), { status: 201 })
    if (url.endsWith('/settings')) return new Response(JSON.stringify({ mcp_enabled: true, mcp_write_enabled: true }))
    if (url.endsWith('/tokens')) return new Response(JSON.stringify({ tokens: [] }))
    if (url.endsWith('/change-events')) return new Response(JSON.stringify({ events: [] }))
    return new Response(JSON.stringify({ logs: [] }))
  }
  try {
    const { bindings, html } = await mount(); await bindings.load(); bindings.startCreate()
    assert.equal(bindings.writeEnabled.value, true)
    assert.doesNotMatch(html, /set_server_order/)
    const expanded = await mount(value => { value.showTools.value = true; value.showAdvanced.value = true })
    assert.match(expanded.html, /set_server_order/); assert.match(expanded.html, /历史审批记录/); assert.match(expanded.html, /最近调用/)
    bindings.tokenScope.value = 'read_write'; bindings.name.value = 'writer'; await bindings.create()
    const created = JSON.parse(requests.find(item => item.url.endsWith('/tokens') && item.options.method === 'POST').options.body)
    assert.equal(created.scope, 'read_write'); assert.equal(created.capabilities, undefined)
    assert.ok(!requests.some(item => item.url.includes('/change-plans')))
  } finally { globalThis.fetch = original }
})

test('按创建时的客户端和平台生成一次性配置，关闭后清除秘密', async () => {
  const original = globalThis.fetch
  const originalNavigator = Object.getOwnPropertyDescriptor(globalThis, 'navigator')
  const copied = []; const requests = []
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { clipboard: { writeText: async value => copied.push(value) } } })
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options })
    if (options.method === 'POST') return Response.json({ token: 'one-time-client-secret' })
    if (url.endsWith('/settings')) return Response.json({ mcp_enabled: true })
    if (url.endsWith('/tokens')) return Response.json({ tokens: [] })
    return Response.json({ logs: [], events: [] })
  }
  try {
    for (const [client, label, platform] of [['codex', 'Codex', 'windows'], ['codex', 'Codex', 'unix'], ['claude', 'Claude Code', 'windows'], ['claude', 'Claude Code', 'unix'], ['cursor', 'Cursor', 'windows'], ['cursor', 'Cursor', 'unix']]) {
      const { bindings, html } = await mount(value => { value.loaded.value = true; value.selectedClient.value = client; value.selectedPlatform.value = platform })
      assert.match(html, new RegExp(`为 ${label} 创建令牌`))
      bindings.startCreate(client)
      assert.equal(bindings.name.value, label)
      assert.equal(bindings.creationClient.value, client)
      assert.equal(bindings.creationPlatform.value, platform)
      bindings.tokenScope.value = 'read_write'; bindings.days.value = 180
      const before = Date.now()
      await bindings.create()
      const posted = JSON.parse(requests.filter(item => item.options.method === 'POST').at(-1).options.body)
      assert.equal(posted.name, label); assert.equal(posted.scope, 'read_write')
      assert.ok(Math.abs(Date.parse(posted.expires_at) - before - 180 * 86400000) < 5000)
      assert.equal(copied.length, 0)
      bindings.selectedClient.value = client === 'cursor' ? 'claude' : 'cursor'
      bindings.selectedPlatform.value = platform === 'windows' ? 'unix' : 'windows'
      const preview = bindings.connectionWithToken()
      assert.match(preview, /one-time-client-secret/)
      if (client === 'cursor') assert.equal(JSON.parse(preview).mcpServers['vps-panel'].headers.Authorization, 'Bearer one-time-client-secret')
      else {
        assert.match(preview, client === 'claude' ? /claude mcp add --transport http/ : /codex mcp add vps-panel/)
        assert.match(preview, platform === 'windows' ? /Get-Command/ : /command -v/)
      }
      await bindings.copyConnectionWithToken(); assert.equal(copied.pop(), preview)
      await bindings.copyAgentPromptWithToken(); assert.match(copied.pop(), /one-time-client-secret/)
      assert.doesNotMatch(bindings.connection.value, /one-time-client-secret/)
      bindings.close(); assert.equal(bindings.secret.value, ''); assert.equal(bindings.connectionWithToken(), '')
      assert.equal(bindings.creationClient.value, null); assert.equal(bindings.creationPlatform.value, null)
      bindings.startCreate(client); assert.equal(bindings.secret.value, ''); assert.equal(bindings.connectionWithToken(), '')
      bindings.close()
    }
  } finally { globalThis.fetch = original; if (originalNavigator) Object.defineProperty(globalThis, 'navigator', originalNavigator); else delete globalThis.navigator }
})

test('工具清单与后端注册和只读标注保持一致', async () => {
  const { readdir } = await import('node:fs/promises')
  const directory = new URL('../../panel/internal/api/', import.meta.url)
  const registered = []
  for (const file of await readdir(directory)) {
    if (!file.startsWith('mcp') || !file.endsWith('.go') || file.endsWith('_test.go')) continue
    const source = await readFile(new URL(file, directory), 'utf8')
    for (const match of source.matchAll(/mcp\.AddTool\(ms,\s*(mcpDef|mcpWriteDef|mcpClientTool)\("([^"]+)"/g)) registered.push([match[2], match[1] !== 'mcpDef'])
  }
  const { mcpTools } = await loader.ssrLoadModule('/src/mcpTools.ts')
  assert.deepEqual(mcpTools.map(tool => [tool.name, tool.write]).sort(), registered.sort())
  assert.equal(new Set(mcpTools.map(tool => tool.name)).size, registered.length)
  const { bindings, html } = await mount()
  assert.equal(bindings.readCount + bindings.writeCount, registered.length)
  assert.match(html, new RegExp(`只读 ${bindings.readCount} · 写操作 ${bindings.writeCount}`))
})

test('客户端、令牌及折叠区域呈现正确的状态，窄屏样式可用', async () => {
  const initial = await mount(value => { value.loaded.value = true })
  assert.match(initial.html, /还没有访问令牌/)
  assert.match(initial.html, /aria-expanded="false"/)
  assert.doesNotMatch(initial.html, /原始配置预览/)
  const cursor = await mount(value => { value.loaded.value = true; value.selectedClient.value = 'cursor'; value.tokens.value = [{ id: 1, name: 'mobile', prefix: 'mcp_abc', scope: 'read_write', created_at: 100, expires_at: 101, last_used_at: null, revoked_at: 102 }] })
  assert.match(cursor.html, /配置 Cursor/)
  assert.match(cursor.html, /复制原始 MCP JSON/)
  assert.doesNotMatch(cursor.html, /操作系统 \/ 终端/)
  assert.match(cursor.html, /mcp_abc…/)
  assert.match(cursor.html, /已撤销/)
  assert.match(cursor.html, /最近使用/)
  const vue = await readFile(new URL('../src/views/MCPSettingsView.vue', import.meta.url), 'utf8')
  assert.match(vue, /@media \(max-width: 720px\)/)
  assert.match(vue, /@media \(max-width: 520px\)/)
  assert.match(vue, /grid-template-columns: repeat\(2, minmax\(0, 1fr\)\)/)
})
