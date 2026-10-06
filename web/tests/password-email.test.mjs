import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, App, ResetPassword
const originalWindow = globalThis.window
const originalFetch = globalThis.fetch
const anonymous = { requires_initialization: false, authenticated: false }
before(async () => {
  globalThis.window = { location: { pathname: '/', search: '', origin: 'https://panel.example.com' }, history: { replaceState() {} } }
  loader = await createServer({
    configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false },
    optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ default: App } = await loader.ssrLoadModule('/src/App.vue'))
  ;({ default: ResetPassword } = await loader.ssrLoadModule('/src/views/ResetPasswordView.vue'))
})
after(async () => { await loader?.close(); globalThis.window = originalWindow; globalThis.fetch = originalFetch })

async function mount(component, props = {}, prepare = () => {}) {
  const setup = component.setup
  let bindings
  component.setup = (properties, context) => {
    bindings = setup(properties, context)
    prepare(bindings)
    return bindings
  }
  try { return { html: await renderToString(createSSRApp(component, props)), bindings } }
  finally { component.setup = setup }
}

const json = (body, status = 200) => new Response(JSON.stringify(body), { status })

test('登录标签仅改登录入口，初始化与邀请仍使用用户名', async () => {
  const source = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  assert.match(source, /@submit.prevent="login"[\s\S]*用户名或邮箱/)
  assert.match(source, /placeholder="用户名 \/ 已验证邮箱"/)
  assert.match(source, /@submit.prevent="initialize"[\s\S]*管理员用户名/)
  assert.match(source, /@submit.prevent="register"[\s\S]*<span>用户名<\/span>/)
  assert.match(source, /isVerifyEmailPage.value \|\| isResetPasswordPage.value/)
  assert.match(source, /<ResetPasswordView v-else-if="isResetPasswordPage"/)
})

test('实际登录提交兼容 username 字段，用户名与邮箱均可输入', async () => {
  for (const identifier of ['CaseUser', ' TEST@Example.COM ']) {
    const requests = []
    globalThis.fetch = async (url, options = {}) => { requests.push([url, options.body && JSON.parse(options.body)]); return json(url === '/api/auth/state' ? anonymous : { user: {} }) }
    const { html, bindings } = await mount(App, {}, b => { b.state.value = anonymous; b.loading.value = false })
    assert.match(html, /用户名或邮箱/)
    bindings.username.value = identifier
    bindings.password.value = 'current-password'
    await bindings.login()
    assert.deepEqual(requests[0], ['/api/auth/login', { username: identifier, password: 'current-password' }])
    assert.equal(bindings.password.value, '')
  }
})

test('邮箱找回默认显示，不收集新密码；管理员审核保留原表单', async () => {
  const prepare = mode => b => { b.state.value = anonymous; b.loading.value = false; b.passwordResetOpen.value = true; b.passwordResetMode.value = mode }
  const { html: email, bindings } = await mount(App, {}, prepare('email'))
  assert.match(email, /邮箱找回/)
  assert.match(email, /管理员审核/)
  assert.match(email, /发送密码重置邮件/)
  assert.doesNotMatch(email, /确认新密码/)
  bindings.passwordResetMode.value = 'admin'
  bindings.openPasswordReset()
  assert.equal(bindings.passwordResetMode.value, 'email')
  const { html: admin } = await mount(App, {}, prepare('admin'))
  assert.match(admin, /确认新密码/)
  assert.match(admin, /提交管理员审核/)
  assert.match(admin, /管理员审核通过后/)
})

test('实际邮箱找回泛化成功、loading、防重复提交与全局不可用提示', async () => {
  let complete, calls = 0, body
  globalThis.fetch = async (url, options) => {
    calls++; body = JSON.parse(options.body)
    assert.equal(url, '/api/auth/password-reset/email/request')
    return new Promise(resolve => { complete = resolve })
  }
  const { bindings } = await mount(App, {}, b => { b.state.value = anonymous; b.loading.value = false })
  bindings.passwordResetUsername.value = 'member@example.com'
  const sending = bindings.requestPasswordReset()
  assert.equal(bindings.passwordResetBusy.value, true)
  await bindings.requestPasswordReset()
  assert.equal(calls, 1)
  assert.deepEqual(body, { identifier: 'member@example.com' })
  complete(json({ status: 'accepted' }, 202)); await sending
  assert.equal(bindings.passwordResetBusy.value, false)
  assert.match(bindings.passwordResetStatus.value, /如果该账号存在且已绑定验证邮箱/)
  globalThis.fetch = async () => json({ error: '管理员尚未启用邮件服务，请使用管理员审核方式' }, 409)
  await bindings.requestPasswordReset()
  assert.equal(bindings.passwordResetStatus.value, '')
  assert.match(bindings.passwordResetError.value, /管理员审核/)
})

test('实际管理员审核提交仍使用旧 endpoint 与字段', async () => {
  let request
  globalThis.fetch = async (url, options) => { request = [url, JSON.parse(options.body)]; return json({ status: 'accepted' }, 202) }
  const { bindings } = await mount(App, {}, b => { b.state.value = anonymous; b.loading.value = false; b.passwordResetMode.value = 'admin' })
  bindings.passwordResetUsername.value = 'member'
  bindings.passwordResetPassword.value = 'proposed-password'
  bindings.passwordResetConfirm.value = 'different'
  await bindings.requestPasswordReset()
  assert.equal(request, undefined)
  bindings.passwordResetConfirm.value = 'proposed-password'
  await bindings.requestPasswordReset()
  assert.deepEqual(request, ['/api/auth/password-reset-request', { username: 'member', new_password: 'proposed-password' }])
  assert.match(bindings.passwordResetStatus.value, /请等待管理员审核/)
})

test('实际重置页面立即清除 URL，保留内存 token，校验字节及确认密码', async () => {
  const events = []
  globalThis.window.history.replaceState = (...args) => events.push(args)
  let calls = 0
  globalThis.fetch = async () => { calls++; return json({ status: 'reset' }) }
  const { bindings, html } = await mount(ResetPassword, { token: 'secret-reset-token' })
  bindings.prepareResetPage()
  assert.deepEqual(events, [[{}, '', '/reset-password']])
  assert.equal(bindings.token.value, 'secret-reset-token')
  assert.doesNotMatch(html, /secret-reset-token/)
  for (const invalid of ['short', 'x'.repeat(73), '中'.repeat(25)]) {
    bindings.password.value = invalid; bindings.confirmPassword.value = invalid
    await bindings.resetPassword()
    assert.match(bindings.error.value, /6–72 字节/)
  }
  bindings.password.value = 'new-password'; bindings.confirmPassword.value = 'wrong'
  await bindings.resetPassword()
  assert.match(bindings.error.value, /不一致/)
  assert.equal(calls, 0)
})

test('实际重置成功后不自动登录，失效链接统一报错且保持 loading', async () => {
  for (const success of [true, false]) {
    let complete, calls = 0
    globalThis.fetch = async (url, options) => {
      calls++
      assert.equal(url, '/api/auth/password-reset/email/confirm')
      assert.deepEqual(JSON.parse(options.body), { token: 'secret-reset-token', new_password: '新'.repeat(24) })
      return new Promise(resolve => { complete = resolve })
    }
    const { bindings } = await mount(ResetPassword, { token: 'secret-reset-token' })
    bindings.password.value = bindings.confirmPassword.value = '新'.repeat(24)
    const resetting = bindings.resetPassword()
    assert.equal(bindings.busy.value, true)
    await bindings.resetPassword(); assert.equal(calls, 1)
    complete(json(success ? { status: 'reset' } : { error: '密码重置链接无效或已过期' }, success ? 200 : 400))
    await resetting
    assert.equal(bindings.busy.value, false)
    assert.equal(bindings.completed.value, success)
    if (success) { assert.equal(bindings.token.value, ''); assert.equal(bindings.password.value, '') }
    else assert.match(bindings.error.value, /密码重置链接无效或已过期/)
    const { html } = await mount(ResetPassword, { token: 'secret-reset-token' }, b => { b.completed.value = success; b.error.value = bindings.error.value })
    assert.match(html, success ? /密码重置成功，请重新登录/ : /密码重置链接无效或已过期/)
    assert.match(html, /返回登录/)
    assert.doesNotMatch(html, /secret-reset-token/)
  }
})
