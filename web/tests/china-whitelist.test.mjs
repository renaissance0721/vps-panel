import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, View
const server = { id: 1, block_china_inbound: true, agent_implementation: 'official', agent_api_version: 1, agent_capabilities: ['firewall.cn_block', 'firewall.cn_whitelist'] }
before(async () => {
  loader = await createServer({ configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()], resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } }, server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] } })
  ;({ default: View } = await loader.ssrLoadModule('/src/components/server/ChinaInboundWhitelist.vue'))
})
after(async () => { await loader?.close() })
async function mount(prepare = () => {}) {
  const original = View.setup; let bindings
  View.setup = (props, context) => { bindings = original(props, context); prepare(bindings); return bindings }
  try { return { html: await renderToString(createSSRApp({ render: () => h(View, { server, readonly: false }) })), bindings } } finally { View.setup = original }
}

test('白名单地址验证、归一化和重复检查', async () => {
  const old = globalThis.fetch
  globalThis.fetch = async () => Response.json({ entries: [] })
  try {
    const { bindings } = await mount()
    assert.equal(bindings.addressKey('203.0.113.10'), bindings.addressKey('203.0.113.10/32'))
    assert.equal(bindings.addressKey('2001:db8::1/64'), bindings.addressKey('2001:0db8::/64'))
    assert.equal(bindings.addressKey('2001:db8::192.0.2.1'), bindings.addressKey('2001:db8::c000:201/128'))
    for (const value of ['0.0.0.0/0', '::/0', '1.2.3.4/33', '999.1.1.1', '::ffff:192.0.2.1']) assert.equal(bindings.addressKey(value), null)
    bindings.entries.value = [{ address: '203.0.113.10/32', remark: '' }]
    bindings.address.value = '203.0.113.10'; bindings.submitEntry()
    assert.match(bindings.error.value, /已存在/)
  } finally { globalThis.fetch = old }
})

test('白名单保存、错误和不支持状态', async () => {
  const old = globalThis.fetch; const requests = []
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, options })
    if (options.method === 'PUT') return Response.json({ entries: [{ address: '203.0.113.10/32', remark: 'relay' }], server })
    return Response.json({ entries: [] })
  }
  try {
    const { bindings, html } = await mount()
    assert.match(html, /SSH/)
    await bindings.save([{ address: '203.0.113.10', remark: 'relay' }])
    assert.equal(bindings.entries.value[0].address, '203.0.113.10/32')
    assert.match(bindings.message.value, /已保存/)
    assert.equal(requests.find(item => item.options.method === 'PUT').url, '/api/servers/1/china-inbound-whitelist')
    globalThis.fetch = async () => new Response(JSON.stringify({ error: '保存失败' }), { status: 500 })
    await bindings.save([])
    assert.match(bindings.error.value, /保存失败/)
  } finally { globalThis.fetch = old }
})
