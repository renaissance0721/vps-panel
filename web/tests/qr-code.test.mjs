import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import QRCode from 'qrcode'

let loader, QRCodeModal, RelaysView

before(async () => {
  loader = await createServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false },
    optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ default: QRCodeModal } = await loader.ssrLoadModule('/src/components/share/QRCodeModal.vue'))
  ;({ default: RelaysView } = await loader.ssrLoadModule('/src/views/RelaysView.vue'))
})

after(async () => { await loader?.close() })

async function renderWithBindings(component, props, prepare = () => {}) {
  const original = component.setup
  let bindings
  component.setup = (value, context) => {
    bindings = original(value, context)
    prepare(bindings)
    return bindings
  }
  try {
    return { html: await renderToString(createSSRApp(component, props)), bindings }
  } finally {
    component.setup = original
  }
}

test('二维码弹窗默认关闭且不提前生成', async t => {
  const generate = t.mock.method(QRCode, 'toDataURL', async () => 'data:image/png;base64,test')
  const { html, bindings } = await renderWithBindings(QRCodeModal, {
    show: false, uri: 'vless://secret', title: '节点',
  })
  assert.equal(generate.mock.calls.length, 0)
  assert.equal(bindings.image.value, '')
  assert.equal(html.includes('vless://secret'), false)
})

test('VLESS REALITY、TLS 和 Shadowsocks URI 原样传给本地二维码库', async t => {
  const actualToDataURL = QRCode.toDataURL.bind(QRCode)
  const generate = t.mock.method(QRCode, 'toDataURL', async () => 'data:image/png;base64,test')
  const uris = [
    `vless://uuid@relay.example.com:443?security=reality&pbk=${'a'.repeat(400)}&fp=chrome#中文%20节点`,
    'vless://uuid@node.example.com:443?security=tls&sni=www.example.com#中文节点',
    'ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@node.example.com:8388#中文%20节点',
  ]
  for (const uri of uris) {
    const { bindings } = await renderWithBindings(QRCodeModal, { show: true, uri, title: '节点', subtitle: '协议' })
    await Promise.resolve()
    assert.equal(bindings.image.value, 'data:image/png;base64,test')
  }
  assert.deepEqual(generate.mock.calls.map(call => call.arguments[0]), uris)
  for (const call of generate.mock.calls) {
    assert.deepEqual(call.arguments[1], { errorCorrectionLevel: 'M', margin: 2, width: 280 })
  }
  for (const uri of uris) {
    const png = await actualToDataURL(uri, { errorCorrectionLevel: 'M', margin: 2, width: 280 })
    assert.match(png, /^data:image\/png;base64,/)
  }
})

test('关闭二维码弹窗立即清空图像且丢弃未完成的生成结果', async t => {
  let resolve
  t.mock.method(QRCode, 'toDataURL', () => new Promise(done => { resolve = done }))
  const { bindings } = await renderWithBindings(QRCodeModal, {
    show: true, uri: 'ss://secret@node.example.com:8388', title: '节点',
  })
  assert.equal(bindings.image.value, '')
  bindings.setShow(false)
  resolve('data:image/png;base64,late')
  await Promise.resolve()
  assert.equal(bindings.image.value, '')
  assert.equal(bindings.error.value, '')
})

test('二维码生成失败显示局部错误', async t => {
  t.mock.method(QRCode, 'toDataURL', () => { throw new Error('test failure') })
  const { html, bindings } = await renderWithBindings(QRCodeModal, {
    show: true, uri: 'vless://secret', title: '节点',
  })
  assert.equal(bindings.error.value, '二维码生成失败')
  assert.match(html, /二维码生成失败/)
  assert.equal(bindings.image.value, '')
})

function relay(overrides = {}) {
  return {
    id: 1, name: '中转', server_name: '源服务器', entry_host_mode: 'manual', entry_address: 'relay.example.com',
    listen_address: '0.0.0.0', listen_port: 9502, target_type: 'proxy', target_client_id: 8,
    target_proxy_name: '目标代理', target_host: 'node.example.com', target_port: 443,
    target_address_ready: true, network: 'tcp', enabled: true, created_at: '2026-09-18T00:00:00Z',
    updated_at: '2026-09-18T00:00:00Z', ...overrides,
  }
}

function relayShare(compatible = true) {
  return {
    client: { id: 8, name: '手机', status: 'normal', effective_enabled: true },
    protocol: 'vless', uri: 'vless://uuid@relay.example.com:9502#中文%20节点',
    network_compatible: compatible,
  }
}

test('Relay 二维码直接使用已加载的 URI，不再请求 API；不兼容或手动目标无法打开', async t => {
  const fetch = t.mock.method(globalThis, 'fetch', async () => { throw new Error('unexpected request') })
  const compatible = relayShare()
  const { html, bindings } = await renderWithBindings(RelaysView, { servers: [] }, model => {
    model.selectedRelay.value = relay()
    model.detailOpen.value = true
    model.relayClients.value = [compatible]
  })
  assert.match(html, /<button[^>]*>二维码<\/button>/)
  assert.equal(bindings.qrOpen.value, false)
  bindings.showRelayQRCode(compatible)
  assert.equal(fetch.mock.calls.length, 0)
  assert.equal(bindings.qrURI.value, compatible.uri)
  assert.equal(bindings.qrTitle.value, '中转 - 手机')
  assert.equal(bindings.qrSubtitle.value, 'VLESS · 中转')
  bindings.setQRCodeOpen(false)
  assert.equal(bindings.qrURI.value, '')
  assert.equal(bindings.qrOpen.value, false)
  bindings.showRelayQRCode(relayShare(false))
  assert.equal(bindings.qrOpen.value, false)
  bindings.selectedRelay.value = relay({ target_type: 'manual' })
  bindings.showRelayQRCode(compatible)
  assert.equal(bindings.qrOpen.value, false)
  const incompatible = await renderWithBindings(RelaysView, { servers: [] }, model => {
    model.selectedRelay.value = relay()
    model.detailOpen.value = true
    model.relayClients.value = [relayShare(false)]
  })
  assert.match(incompatible.html, /<button[^>]*disabled[^>]*>二维码<\/button>/)
  const manual = await renderWithBindings(RelaysView, { servers: [] }, model => {
    model.selectedRelay.value = relay({ target_type: 'manual', target_client_id: null })
    model.detailOpen.value = true
  })
  assert.match(manual.html, /手动目标不支持自动生成客户端节点链接/)
  assert.doesNotMatch(manual.html, />二维码<\/button>/)
})
