import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, MailSettingsView
const settings = {
  enabled: true,
  host: 'smtp.example.com',
  port: 587,
  security: 'starttls',
  username: 'noreply@example.com',
  password_configured: true,
  from_address: 'noreply@example.com',
  from_name: 'VPS Panel',
  reply_to: '',
}

before(async () => {
  loader = await createServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false },
    optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ default: MailSettingsView } = await loader.ssrLoadModule('/src/views/MailSettingsView.vue'))
})
after(async () => { await loader?.close() })

async function mountSettings(prepare = () => {}) {
  const original = MailSettingsView.setup
  let bindings
  MailSettingsView.setup = (props, context) => {
    bindings = original(props, context)
    prepare(bindings)
    return bindings
  }
  try {
    const html = await renderToString(createSSRApp(MailSettingsView))
    return { bindings, html }
  } finally {
    MailSettingsView.setup = original
  }
}

test('系统设置入口和邮件页面仅按管理员条件渲染', async () => {
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  assert.match(app, /v-if="state\.user\?\.role === 'admin'"[\s\S]*currentPage === 'settings'[\s\S]*系统设置/)
  assert.match(app, /MailSettingsView v-if="currentPage === 'settings' && state\.user\?\.role === 'admin'"/)
})

test('邮件设置显示密码保留提示、三种安全模式和无 TLS 警告', async () => {
  const { bindings, html } = await mountSettings(value => {
    value.loading.value = false
    value.loaded.value = true
    value.form.value = { ...settings, security: 'none' }
  })
  assert.equal(bindings.password.value, '')
  assert.deepEqual(bindings.securityOptions.map(option => option.value), ['tls', 'starttls', 'none'])
  assert.match(html, /已配置，留空表示保持不变/)
  assert.match(html, /认证信息及邮件内容可能以明文传输/)
  assert.match(html, /发送测试邮件/)
})

test('加载不回填密码，保存空密码保留凭据，测试使用未保存表单', async () => {
  const originalFetch = globalThis.fetch
  const requests = []
  globalThis.fetch = async (url, options = {}) => {
    const body = options.body ? JSON.parse(options.body) : undefined
    requests.push({ url, options, body })
    if (url.endsWith('/test')) return new Response(JSON.stringify({ status: 'sent' }))
    return new Response(JSON.stringify(settings))
  }
  try {
    const { bindings } = await mountSettings()
    await bindings.load()
    assert.equal(bindings.password.value, '')
    assert.equal(bindings.form.value.password_configured, true)
    await bindings.save()
    const saved = requests.find(request => request.options.method === 'PUT')
    assert.equal(saved.body.password, '')
    bindings.form.value.host = 'unsaved.example.com'
    bindings.testRecipient.value = 'test@example.com'
    await bindings.sendTest()
    const tested = requests.find(request => request.url.endsWith('/test'))
    assert.equal(tested.body.host, 'unsaved.example.com')
    assert.equal(tested.body.password, '')
    assert.equal(tested.body.to, 'test@example.com')
    assert.equal(bindings.testSuccess.value, '测试邮件已发送，请检查收件箱。')
    assert.equal(bindings.testing.value, false)
    assert.equal(requests.filter(request => request.options.method === 'PUT').length, 1)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('保存和测试使用独立 loading，并显示服务端测试错误', async () => {
  const originalFetch = globalThis.fetch
  globalThis.fetch = async url => {
    if (url.endsWith('/test')) return new Response(JSON.stringify({ error: 'SMTP AUTH 认证失败' }), { status: 502 })
    return new Response(JSON.stringify(settings))
  }
  try {
    const { bindings } = await mountSettings()
    await bindings.load()
    await bindings.sendTest()
    assert.equal(bindings.testing.value, false)
    assert.equal(bindings.saving.value, false)
    assert.equal(bindings.testSuccess.value, '')
    assert.equal(bindings.testError.value, 'SMTP AUTH 认证失败')
  } finally {
    globalThis.fetch = originalFetch
  }
})
