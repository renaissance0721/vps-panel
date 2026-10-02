import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, NotificationSettings, MonitorView
const defaults = { telegram_token_set: false, telegram_chat_id: '', online_enabled: true, offline_grace_minutes: 3,
  recovery_enabled: true, traffic_enabled: true, traffic_threshold_percent: 80, traffic_step_percent: 10, traffic_full_enabled: true }
before(async () => {
  loader = await createServer({ configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true, include: [] } })
  ;({ default: NotificationSettings } = await loader.ssrLoadModule('/src/components/monitor/NotificationSettings.vue'))
  ;({ default: MonitorView } = await loader.ssrLoadModule('/src/views/MonitorView.vue'))
})
after(async () => { await loader?.close() })

async function mountSettings(prepare = () => {}) {
  const original = NotificationSettings.setup
  let bindings
  NotificationSettings.setup = (props, context) => { bindings = original(props, context); prepare(bindings); return bindings }
  try {
    const html = await renderToString(createSSRApp(NotificationSettings, { show: true }))
    return { bindings, html }
  } finally { NotificationSettings.setup = original }
}

test('通知入口只向管理员显示，并在延迟探测旁；所有非管理员均不可见', async () => {
  for (const role of ['admin', 'vip', 'user', 'subscriber', undefined]) {
    const html = await renderToString(createSSRApp(MonitorView, { role, servers: [] }))
    if (role === 'admin') assert.match(html, /延迟探测[\s\S]+通知设置/)
    else assert.doesNotMatch(html, /通知设置/)
  }
})

test('默认值、密码输入、已配置提示和开关布局正确', async () => {
  const { bindings, html } = await mountSettings(b => { b.loading.value = false; b.loaded.value = true; b.form.value.telegram_token_set = true })
  assert.equal(bindings.form.value.offline_grace_minutes, 3)
  assert.equal(bindings.form.value.traffic_threshold_percent, 80)
  assert.equal(bindings.form.value.traffic_step_percent, 10)
  assert.match(html, /type="password"/)
  assert.match(html, /已配置，留空保留原 Token/)
  assert.match(html, /在线状态/)
  assert.match(html, /100% 用尽提醒/)
  const source = await readFile(new URL('../src/components/monitor/NotificationSettings.vue', import.meta.url), 'utf8')
  assert.match(source, /\.notification-switch-row\s*\{[^}]*display: flex;[^}]*align-items: center;[^}]*justify-content: space-between;/)
})

test('保存空 Token 保留旧凭据；测试当前未保存内容并正确显示成功和中文错误', async () => {
  const originalFetch = globalThis.fetch
  const requests = []
  let testStatus = 'success'
  globalThis.fetch = async (url, options) => {
    requests.push({ url, options, body: options.body ? JSON.parse(options.body) : undefined })
    if (url === '/api/notifications/test') return new Response(JSON.stringify({ id: 'test-id', status: 'pending' }), { status: 202 })
    if (url.endsWith('/test/test-id')) return new Response(JSON.stringify({ id: 'test-id', status: testStatus, error: testStatus === 'error' ? 'Telegram Bot Token 无效' : undefined }))
    return new Response(JSON.stringify({ ...defaults, telegram_token_set: true, telegram_chat_id: '123' }))
  }
  try {
    const { bindings } = await mountSettings()
    await bindings.load()
    assert.equal(bindings.botToken.value, '')
    assert.equal(bindings.form.value.telegram_token_set, true)
    await bindings.save()
    const saved = requests.find(r => r.options.method === 'PUT').body
    assert.equal(saved.telegram_bot_token, '')
    assert.equal(saved.clear_telegram_token, false)
    requests.length = 0
    bindings.form.value.telegram_chat_id = '-100999'
    bindings.botToken.value = '456:UNSAVED'
    await bindings.sendTest()
    assert.equal(bindings.testSuccess.value, true)
    assert.equal(bindings.testing.value, false)
    assert.equal(bindings.testError.value, '')
    assert.deepEqual(requests[0].body, { telegram_bot_token: '456:UNSAVED', telegram_chat_id: '-100999' })
    assert.equal(requests.filter(r => r.options.method === 'PUT').length, 0)
    testStatus = 'error'
    bindings.botToken.value = ''
    await bindings.sendTest()
    assert.equal(bindings.testSuccess.value, false)
    assert.equal(bindings.testError.value, 'Telegram Bot Token 无效')
    assert.equal(requests.at(-2).body.telegram_bot_token, '')
  } finally { globalThis.fetch = originalFetch }
})

test('加载失败不会开放默认表单覆盖已有配置；测试入队失败会显示错误', async () => {
  const originalFetch = globalThis.fetch
  globalThis.fetch = async () => new Response(JSON.stringify({ error: '通知队列已满，请稍后再试' }), { status: 503 })
  try {
    const { bindings } = await mountSettings()
    await bindings.load()
    assert.equal(bindings.loaded.value, false)
    assert.equal(bindings.loading.value, false)
    await bindings.sendTest()
    assert.equal(bindings.testSuccess.value, false)
    assert.equal(bindings.testing.value, false)
    assert.match(bindings.testError.value, /通知队列已满/)
  } finally { globalThis.fetch = originalFetch }
})
