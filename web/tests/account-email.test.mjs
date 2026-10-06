import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, EmailSettings, VerifyEmail
before(async () => {
  loader = await createServer({
    configFile: false,
    root: fileURLToPath(new URL('..', import.meta.url)),
    plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false },
    optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ default: EmailSettings } = await loader.ssrLoadModule('/src/components/AccountEmailSettings.vue'))
  ;({ default: VerifyEmail } = await loader.ssrLoadModule('/src/views/VerifyEmailView.vue'))
})
after(async () => { await loader?.close() })

async function mountComponent(component, props = {}, prepare = () => {}) {
  const original = component.setup
  let bindings
  component.setup = (properties, context) => {
    bindings = original(properties, context)
    prepare(bindings)
    return bindings
  }
  try {
    const html = await renderToString(createSSRApp(component, props))
    return { bindings, html }
  } finally {
    component.setup = original
  }
}

const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
const menuSource = await readFile(new URL('../src/components/AccountMenu.vue', import.meta.url), 'utf8')
const emailSource = await readFile(new URL('../src/components/AccountEmailSettings.vue', import.meta.url), 'utf8')
const verifySource = await readFile(new URL('../src/views/VerifyEmailView.vue', import.meta.url), 'utf8')
const carpoolSource = await readFile(new URL('../src/views/CarpoolPortalView.vue', import.meta.url), 'utf8')
const subscriberSource = await readFile(new URL('../src/views/SubscriberPortalView.vue', import.meta.url), 'utf8')
const authTypesSource = await readFile(new URL('../src/types/auth.ts', import.meta.url), 'utf8')

async function vueAndTypeScriptSources(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const sources = []
  for (const entry of entries) {
    const location = new URL(`${entry.name}${entry.isDirectory() ? '/' : ''}`, directory)
    if (entry.isDirectory()) sources.push(...await vueAndTypeScriptSources(location))
    else if (/\.(?:ts|vue)$/.test(entry.name)) sources.push([location.pathname, await readFile(location, 'utf8')])
  }
  return sources
}

test('四种角色通过同一个账户菜单访问共享邮箱设置', () => {
  assert.match(authTypesSource, /role: 'admin' \| 'vip' \| 'carpool' \| 'subscriber'/)
  assert.match(menuSource, /label: '邮箱设置', key: 'email'/)
  assert.match(menuSource, /import AccountEmailSettings from '.\/AccountEmailSettings\.vue'/)
  assert.match(menuSource, /<AccountEmailSettings v-if="emailOpen"/)
  assert.match(appSource, /<AccountMenu v-if="state\.user"/)
  assert.match(carpoolSource, /<AccountMenu :user="props\.user"/)
  assert.match(subscriberSource, /<AccountMenu :user="props\.user"/)
})

test('邮箱设置覆盖未绑定、待验证、已验证、密码确认、loading 与错误状态', () => {
  for (const text of ['未绑定', '待验证', '已验证', '当前密码', '绑定邮箱', '更换邮箱', '重新发送']) {
    assert.match(emailSource, new RegExp(text))
  }
  assert.match(emailSource, /\/api\/account\/email\/request[\s\S]*current_password/)
  assert.match(emailSource, /\/api\/account\/email\/resend[\s\S]*current_password/)
  assert.match(emailSource, /:loading="sending"/)
  assert.match(emailSource, /:loading="resending"/)
  assert.match(emailSource, /v-if="error" type="error"/)
  assert.match(emailSource, /邮箱功能暂不可用/)
  assert.match(emailSource, /验证邮件已发送，请在 30 分钟内完成验证。/)
})

test('公共验证页清除 query、不显示 token，并处理成功与失效', () => {
  assert.match(appSource, /window\.location\.pathname === '\/verify-email'/)
  assert.match(verifySource, /window\.history\.replaceState\(\{\}, '', '\/verify-email'\)/)
  assert.match(verifySource, /\/api\/auth\/email\/verify/)
  assert.match(verifySource, /邮箱验证成功/)
  assert.match(verifySource, /验证链接无效或已过期/)
  const template = verifySource.split('<template>')[1] ?? ''
  assert.doesNotMatch(template, /\btoken\b|\{\{\s*token\s*\}\}/)
})

test('前端不再保留 user 角色或 UserPortalView', async () => {
  const sources = await vueAndTypeScriptSources(new URL('../src/', import.meta.url))
  for (const [name, source] of sources) {
    assert.doesNotMatch(source, /RoleUser|requireUser|UserPortalView|role\s*===\s*['"]user['"]|role\s*:\s*['"]user['"]/, name)
  }
  const viewNames = await readdir(new URL('../src/views/', import.meta.url))
  assert.ok(viewNames.includes('CarpoolPortalView.vue'))
  assert.ok(!viewNames.includes('UserPortalView.vue'))
})

test('实际渲染未绑定、首次 pending、已验证、更换 pending 和 SMTP 关闭状态', async () => {
  for (const [status, expected] of [
    [{ email: '', verified: false, pending_email: '', available: true }, ['未绑定', '绑定邮箱']],
    [{ email: '', verified: false, pending_email: 'first@example.com', available: true }, ['未绑定', 'first@example.com', '待验证', '重新发送']],
    [{ email: 'old@example.com', verified: true, pending_email: '', available: true }, ['old@example.com', '已验证', '更换邮箱']],
    [{ email: 'old@example.com', verified: true, pending_email: 'new@example.com', available: true }, ['old@example.com', 'new@example.com', '已验证', '待验证']],
    [{ email: '', verified: false, pending_email: '', available: false, unavailable_reason: '管理员尚未启用邮件服务' }, ['未绑定', '邮箱功能暂不可用', '管理员尚未启用邮件服务']],
  ]) {
    const { html } = await mountComponent(EmailSettings, {}, bindings => {
      bindings.loading.value = false
      bindings.status.value = status
    })
    for (const value of expected) assert.ok(html.includes(value), value)
    if (!status.available) assert.doesNotMatch(html, /绑定邮箱|更换邮箱|重新发送/)
  }
})

test('发送与重发传递当前密码、显示独立 loading、阻止重复提交并显示真实错误', async () => {
  const originalFetch = globalThis.fetch
  const requests = []
  let completeRequest
  globalThis.fetch = async (url, options = {}) => {
    requests.push({ url, body: options.body && JSON.parse(options.body) })
    if (url === '/api/account/email') return new Response(JSON.stringify({
      email: '', verified: false, pending_email: 'new@example.com', available: true,
    }))
    return new Promise(resolve => { completeRequest = resolve })
  }
  try {
    const { bindings } = await mountComponent(EmailSettings)
    bindings.email.value = 'new@example.com'
    bindings.currentPassword.value = 'current-password'
    const sending = bindings.sendVerification()
    assert.equal(bindings.sending.value, true)
    assert.equal(bindings.resending.value, false)
    await bindings.sendVerification()
    assert.equal(requests.length, 1)
    assert.deepEqual(requests[0].body, { email: 'new@example.com', current_password: 'current-password' })
    completeRequest(new Response(JSON.stringify({ status: 'sent' })))
    await sending
    assert.equal(bindings.sending.value, false)
    assert.equal(bindings.currentPassword.value, '')
    assert.equal(bindings.status.value.pending_email, 'new@example.com')
    assert.match(bindings.message.value, /验证邮件已发送/)

    bindings.resendPassword.value = 'current-password'
    const resending = bindings.resendVerification()
    assert.equal(bindings.resending.value, true)
    assert.equal(bindings.sending.value, false)
    assert.deepEqual(requests.at(-1).body, { current_password: 'current-password' })
    completeRequest(new Response(JSON.stringify({ error: '验证邮件发送过于频繁，请稍后重试' }), { status: 429 }))
    await resending
    assert.equal(bindings.resending.value, false)
    assert.equal(bindings.message.value, '')
    assert.equal(bindings.error.value, '验证邮件发送过于频繁，请稍后重试')
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('实际公共验证成功或失效，先清除 token query 且无需认证请求', async () => {
  const originalFetch = globalThis.fetch
  const originalWindow = globalThis.window
  const events = []
  globalThis.window = { history: { replaceState: (...args) => events.push(['replace', ...args]) } }
  try {
    for (const success of [true, false]) {
      events.length = 0
      globalThis.fetch = async (url, options) => {
        events.push(['fetch', url])
        assert.equal(url, '/api/auth/email/verify')
        assert.deepEqual(JSON.parse(options.body), { token: 'verification-token' })
        return new Response(JSON.stringify(success ? { status: 'verified' } : { error: '验证链接无效或已过期' }), { status: success ? 200 : 400 })
      }
      const { bindings } = await mountComponent(VerifyEmail, { token: 'verification-token' })
      await bindings.verifyEmail()
      assert.equal(events[0][0], 'replace')
      assert.equal(events[0][3], '/verify-email')
      assert.equal(bindings.loading.value, false)
      assert.equal(bindings.verified.value, success)
      assert.equal(bindings.error.value, success ? '' : '验证链接无效或已过期')
      const { html } = await mountComponent(VerifyEmail, { token: 'verification-token' }, value => {
        value.loading.value = false
        value.verified.value = success
        value.error.value = bindings.error.value
      })
      assert.match(html, success ? /邮箱验证成功/ : /验证链接无效或已过期/)
      assert.doesNotMatch(html, /verification-token/)
    }
  } finally {
    globalThis.fetch = originalFetch
    globalThis.window = originalWindow
  }
})
