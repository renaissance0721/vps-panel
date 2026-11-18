import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

const originalFetch = globalThis.fetch
const originalWindow = globalThis.window
let loader, View
const endpoint = '/api/admin/settings/reverse-proxy'
const config = { trusted_proxy_ips: [], source: 'default', max_trusted_proxy_ips: 32 }
const observed = { peer_ip: '192.0.2.10', peer_loopback: false, peer_trusted: false, backend_tls: false, forwarded_status: 'https', https_accepted: false }
const json = (body, status = 200) => new Response(JSON.stringify(body), { status })

before(async () => {
  loader = await createServer({
    configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ default: View } = await loader.ssrLoadModule('/src/views/ReverseProxySettingsView.vue'))
})
after(async () => { globalThis.fetch = originalFetch; globalThis.window = originalWindow; await loader?.close() })

async function render(populate = () => {}, protocol = 'https:') {
  globalThis.window = { location: { protocol } }
  const setup = View.setup
  let state
  View.setup = (props, context) => { state = setup(props, context); populate(state); return state }
  try {
    const html = await renderToString(createSSRApp(View))
    return { state, html }
  }
  finally { View.setup = setup }
}

test('管理员设置显示实际对端、分开两段协议，并如实标记 WSS 未验证', async () => {
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  assert.match(app, /v-if="currentPage === 'settings' && state\.user\?\.role === 'admin'" class="settings-page">\s*<ReverseProxySettingsView/)
  const { html } = await render(state => {
    state.applySettings(config)
    state.loading.value = false
    state.diagnostics.value = observed
  })
  for (const label of ['网络与安全', '反向代理', '浏览器访问协议', 'Panel 后端接收协议', '192.0.2.10', '未信任', '添加当前来源 IP', '未验证', 'Docker 网关']) {
    assert.ok(html.includes(label), label)
  }
})

test('当前对端须手动加入并确认，保存后重新诊断，删除写入显式空列表', async () => {
  let saved = config
  const calls = []
  globalThis.fetch = async (path, options) => {
    calls.push([path, options.method, options.body && JSON.parse(options.body)])
    if (path === endpoint + '/diagnostics') {
      const trusted = saved.trusted_proxy_ips.includes(observed.peer_ip)
      return json({ ...observed, peer_trusted: trusted, https_accepted: trusted })
    }
    if (options.method === 'PUT') saved = { ...config, source: 'database', ...JSON.parse(options.body) }
    return json(saved)
  }
  const { state } = await render()
  await state.load()
  await state.check()
  assert.equal(state.canAddCurrent.value, true)
  assert.deepEqual(state.addresses.value, [])
  assert.ok(state.addIP(state.diagnostics.value.peer_ip))
  await state.save()
  assert.equal(calls.length, 2)
  state.confirmed.value = true
  await state.save()
  assert.deepEqual(calls.slice(2), [
    [endpoint, 'PUT', { trusted_proxy_ips: [observed.peer_ip] }],
    [endpoint + '/diagnostics', undefined, undefined],
  ])
  assert.match(state.success.value, /生效/)
  assert.equal(state.diagnostics.value.https_accepted, true)
  state.removeIP(observed.peer_ip)
  await state.save()
  assert.deepEqual(calls.at(-2), [endpoint, 'PUT', { trusted_proxy_ips: [] }])
  assert.equal(state.diagnostics.value.https_accepted, false)
})

test('拒绝不合规地址及归一化后重复地址，保存失败保留编辑内容', async () => {
  const { state } = await render(state => { state.applySettings(config) })
  for (const invalid of ['example.test', '10.0.0.0/8', '*', '0.0.0.0', 'ff02::1', '192.0.2.1:8080']) {
    assert.equal(state.addIP(invalid), false)
    assert.match(state.error.value, /单个有效/)
  }
  assert.ok(state.addIP('::ffff:192.0.2.10'))
  assert.equal(state.addIP('192.0.2.10'), false)
  assert.match(state.error.value, /已在列表中/)
  assert.ok(state.addIP('2001:0DB8:0::1'))
  assert.equal(state.addIP('2001:db8::1'), false)
  assert.equal(state.normalizeIP('ff::1'), 'ff::1')
  state.confirmed.value = true
  globalThis.fetch = async () => json({ error: '保存失败' }, 500)
  await state.save()
  assert.equal(state.error.value, '保存失败')
  assert.deepEqual(state.addresses.value, ['192.0.2.10', '2001:db8::1'])
  assert.equal(state.settings.value.source, 'default')
})

test('明文浏览器、缺失或冲突协议头都不会提供当前地址快捷信任', async () => {
  const { state: http } = await render(state => { state.diagnostics.value = observed }, 'http:')
  assert.equal(http.canAddCurrent.value, false)
  assert.match(http.diagnosticAdvice.value, /HTTPS 域名/)
  const { state } = await render()
  state.diagnostics.value = { ...observed, forwarded_status: 'invalid' }
  assert.equal(state.canAddCurrent.value, false)
  assert.match(state.diagnosticAdvice.value, /清除 Forwarded/)
  state.diagnostics.value = { ...observed, forwarded_status: 'missing' }
  assert.equal(state.canAddCurrent.value, false)
  assert.match(state.diagnosticAdvice.value, /覆盖 X-Forwarded-Proto/)
})
