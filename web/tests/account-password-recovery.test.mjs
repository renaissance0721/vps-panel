import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'

let loader, AccountMenu, maskEmail
const originalFetch = globalThis.fetch
const originalWindow = globalThis.window
const user = { id: 1, username: 'CaseUser', role: 'admin', created_at: '2026-10-07T00:00:00Z' }
const verified = { email: 'raymond@example.com', verified: true, pending_email: '', available: true }
const json = (body, status = 200) => new Response(JSON.stringify(body), { status })

before(async () => {
  loader = await createServer({
    configFile: false, root: fileURLToPath(new URL('..', import.meta.url)), plugins: [vue()],
    resolve: { alias: { 'naive-ui': fileURLToPath(new URL('./helpers/ui-stubs.mjs', import.meta.url)) } },
    server: { middlewareMode: true, watch: null, hmr: false, ws: false },
    optimizeDeps: { noDiscovery: true, include: [] },
  })
  ;({ default: AccountMenu } = await loader.ssrLoadModule('/src/components/AccountMenu.vue'))
  ;({ maskEmail } = await loader.ssrLoadModule('/src/format.ts'))
})
after(async () => { await loader?.close(); globalThis.fetch = originalFetch; globalThis.window = originalWindow })

async function mount(prepare = () => {}, props = {}) {
  const setup = AccountMenu.setup
  let bindings
  AccountMenu.setup = (properties, context) => {
    bindings = setup(properties, context)
    prepare(bindings)
    return bindings
  }
  try { return { html: await renderToString(createSSRApp(AccountMenu, { user, ...props })), bindings } }
  finally { AccountMenu.setup = setup }
}

async function renderRecovery(bindings, mode = 'email') {
  return (await mount(b => {
    b.resetOpen.value = true
    b.resetMode.value = mode
    for (const key of ['resetEmailStatus', 'resetEmailLoading', 'resetEmailSending', 'resetEmailError', 'resetEmailMessage', 'resetBusy', 'resetError', 'resetStatus']) {
      b[key].value = bindings[key].value
    }
  })).html
}

test('正常改密保留当前密码验证且不增加邮箱步骤', async () => {
  const requests = [], events = []
  globalThis.fetch = async (url, options) => {
    requests.push([url, JSON.parse(options.body)])
    return requests.length === 1 ? json({ error: '当前密码错误' }, 401) : json({ status: 'changed' })
  }
  globalThis.window = { alert: value => events.push(value) }
  const { bindings: b } = await mount(() => {}, { onLogout: () => events.push('logout') })
  b.handleSelect('password')
  b.newPassword.value = b.confirmPassword.value = 'new-password'
  await b.changePassword()
  assert.deepEqual(requests[0], ['/api/account/password', { current_password: '', new_password: 'new-password' }])
  assert.equal(b.formError.value, '当前密码错误')
  assert.equal(b.passwordOpen.value, true)
  assert.equal(events.length, 0)
  b.currentPassword.value = 'current-password'
  await b.changePassword()
  assert.deepEqual(requests[1], ['/api/account/password', { current_password: 'current-password', new_password: 'new-password' }])
  assert.deepEqual(events, ['密码修改成功，请重新登录。', 'logout'])
  const { html } = await mount(b => { b.passwordOpen.value = true })
  assert.match(html, /当前密码/)
  assert.match(html, /确认新密码/)
  assert.doesNotMatch(html, /邮箱找回|发送密码重置邮件|前往邮箱设置/)
})

test('所有四种角色打开恢复窗口时刷新邮箱状态并默认邮箱找回', async () => {
  for (const role of ['admin', 'vip', 'carpool', 'subscriber']) {
    const requests = []
    let resolveStatus
    globalThis.fetch = (url, options) => {
      requests.push([url, options])
      return new Promise(resolve => { resolveStatus = resolve })
    }
    const { bindings: b } = await mount(() => {}, { user: { ...user, role } })
    b.handleSelect('password')
    const opening = b.openPasswordReset()
    assert.equal(b.passwordOpen.value, false)
    assert.equal(b.emailOpen.value, false)
    assert.equal(b.resetOpen.value, true)
    assert.equal(b.resetMode.value, 'email')
    assert.equal(b.resetEmailLoading.value, true)
    assert.equal(b.canSendResetEmail.value, false)
    assert.equal(requests[0][0], '/api/account/email')
    assert.match(await renderRecovery(b), /正在加载邮箱状态/)
    resolveStatus(json(verified)); await opening
    assert.equal(b.canSendResetEmail.value, true)
    const html = await renderRecovery(b)
    assert.match(html, /r\*\*\*@example.com/)
    assert.match(html, /邮箱找回/)
    assert.match(html, /管理员审核/)
    assert.doesNotMatch(html, /raymond@example.com|<input|确认新密码/)
    b.closePasswordReset()
    const reopening = b.openPasswordReset()
    resolveStatus(json({ ...verified, email: 'changed@example.net' })); await reopening
    assert.equal(requests.length, 2)
    assert.match(await renderRecovery(b), /c\*\*\*@example.net/)
  }
})

test('邮箱发送只传当前用户名、独立 loading、防重复、成功后不注销', async () => {
  let finish, sends = 0, request, logouts = 0
  globalThis.fetch = (url, options) => {
    if (url === '/api/account/email') return Promise.resolve(json(verified))
    sends++; request = [url, JSON.parse(options.body)]
    return new Promise(resolve => { finish = resolve })
  }
  const { bindings: b } = await mount(() => {}, { onLogout: () => logouts++ })
  await b.openPasswordReset()
  const sending = b.sendPasswordResetEmail()
  assert.equal(b.resetEmailSending.value, true)
  assert.equal(b.resetBusy.value, false)
  assert.equal(b.busy.value, false)
  await b.sendPasswordResetEmail()
  assert.equal(sends, 1)
  assert.deepEqual(request, ['/api/auth/password-reset/email/request', { identifier: user.username }])
  finish(json({ status: 'accepted' }, 202)); await sending
  assert.equal(b.resetEmailSending.value, false)
  assert.equal(b.resetEmailMessage.value, '密码重置邮件已发送，请前往邮箱查看。链接将在 30 分钟内有效。')
  assert.equal(logouts, 0)
  assert.equal(b.resetOpen.value, true)
})

test('429 映射友好提示，全局不可用和其他错误沿用 API 错误', async () => {
  for (const [status, error, expected] of [
    [429, '服务器限频消息', '请求过于频繁，请稍后再试'],
    [409, '管理员尚未启用邮件服务，请使用管理员审核方式', '管理员尚未启用邮件服务，请使用管理员审核方式'],
    [503, '管理员尚未配置可信 Panel 域名，请使用管理员审核方式', '管理员尚未配置可信 Panel 域名，请使用管理员审核方式'],
    [500, '服务器内部错误', '服务器内部错误'],
  ]) {
    globalThis.fetch = async url => url === '/api/account/email' ? json(verified) : json({ error }, status)
    const { bindings: b } = await mount()
    await b.openPasswordReset(); await b.sendPasswordResetEmail()
    assert.equal(b.resetEmailError.value, expected)
    assert.equal(b.resetEmailMessage.value, '')
    assert.equal(b.resetEmailSending.value, false)
    assert.doesNotMatch(await renderRecovery(b, 'admin'), new RegExp(expected))
  }
})

test('无已验证邮箱或邮件服务不可用时禁用发送，仍可进入邮箱设置或管理员审核', async () => {
  for (const status of [
    { ...verified, email: '', verified: false },
    { ...verified, verified: false, pending_email: 'pending@example.net' },
    { ...verified, email: '' },
    { ...verified, available: false, unavailable_reason: '管理员尚未启用邮件服务' },
    { ...verified, available: false, unavailable_reason: '管理员尚未配置可信 Panel 域名' },
  ]) {
    let calls = 0
    globalThis.fetch = async () => { calls++; return json(status) }
    const { bindings: b } = await mount()
    await b.openPasswordReset(); await b.sendPasswordResetEmail()
    assert.equal(calls, 1)
    assert.equal(b.canSendResetEmail.value, false)
    const html = await renderRecovery(b)
    assert.match(html, /disabled[^>]*>发送密码重置邮件/)
    if (!status.verified || !status.email) {
      assert.match(html, /当前账号尚未绑定并验证邮箱/)
      assert.match(html, /前往邮箱设置/)
      assert.doesNotMatch(html, /r\*\*\*@example.com/)
      b.openEmailSettings()
      assert.equal(b.resetOpen.value, false)
      assert.equal(b.passwordOpen.value, false)
      assert.equal(b.emailOpen.value, true)
      b.emailOpen.value = false
      globalThis.fetch = async () => json(verified)
      await b.openPasswordReset()
      assert.equal(b.canSendResetEmail.value, true)
    } else assert.match(html, new RegExp(status.unavailable_reason))
    const admin = await renderRecovery(b, 'admin')
    assert.match(admin, /确认新密码/)
    assert.match(admin, /提交申请/)
    assert.doesNotMatch(admin, /邮箱找回暂不可用/)
  }
})

test('管理员审核保留 endpoint、6–72 字节校验和确认密码，状态不串到邮箱方式', async () => {
  let finish, sends = 0, request
  globalThis.fetch = (url, options) => {
    if (url === '/api/account/email') return Promise.resolve(json(verified))
    sends++; request = [url, JSON.parse(options.body)]
    return new Promise(resolve => { finish = resolve })
  }
  const { bindings: b } = await mount()
  await b.openPasswordReset()
  b.resetMode.value = 'admin'
  for (const password of ['short', 'x'.repeat(73), '中'.repeat(25)]) {
    b.resetPassword.value = b.resetConfirmPassword.value = password
    await b.requestPasswordReset()
    assert.match(b.resetError.value, /6–72 字节/)
  }
  b.resetPassword.value = 'proposed-password'; b.resetConfirmPassword.value = 'different'
  await b.requestPasswordReset(); assert.match(b.resetError.value, /不一致/)
  assert.equal(sends, 0)
  assert.doesNotMatch(await renderRecovery(b), /6–72 字节|不一致/)
  b.resetConfirmPassword.value = 'proposed-password'
  const submitting = b.requestPasswordReset()
  assert.equal(b.resetBusy.value, true)
  assert.equal(b.resetEmailSending.value, false)
  await b.requestPasswordReset(); assert.equal(sends, 1)
  assert.deepEqual(request, ['/api/account/password-reset-request', { new_password: 'proposed-password' }])
  finish(json({ status: 'accepted' }, 202)); await submitting
  assert.equal(b.resetBusy.value, false)
  assert.equal(b.resetStatus.value, '密码重置申请已提交，等待管理员审核。')
  assert.equal(b.resetPassword.value, '')
  assert.equal(b.resetConfirmPassword.value, '')
  assert.doesNotMatch(await renderRecovery(b), /密码重置申请已提交/)
})

test('关闭取消请求并清空状态，旧请求完成不能覆盖重新打开的窗口', async () => {
  const loads = [], signals = []
  globalThis.fetch = (url, options) => new Promise(resolve => { loads.push(resolve); signals.push(options.signal) })
  const { bindings: b } = await mount()
  const first = b.openPasswordReset()
  b.closePasswordReset()
  assert.equal(signals[0].aborted, true)
  const second = b.openPasswordReset()
  loads[0](json(verified)); await first
  assert.equal(b.resetEmailStatus.value, null)
  assert.equal(b.resetEmailLoading.value, true)
  loads[1](json({ ...verified, email: 'fresh@example.net' })); await second
  assert.equal(b.resetEmailStatus.value.email, 'fresh@example.net')
  let finishSend, sendSignal
  globalThis.fetch = (url, options) => new Promise(resolve => { finishSend = resolve; sendSignal = options.signal })
  const sending = b.sendPasswordResetEmail()
  b.resetError.value = 'old admin error'; b.resetStatus.value = 'old status'; b.resetPassword.value = 'old-password'
  b.openEmailSettings()
  assert.equal(sendSignal.aborted, true)
  finishSend(json({ status: 'accepted' }, 202)); await sending
  for (const key of ['resetEmailError', 'resetEmailMessage', 'resetError', 'resetStatus', 'resetPassword', 'resetConfirmPassword']) assert.equal(b[key].value, '')
  assert.equal(b.resetEmailSending.value, false)
  assert.equal(b.resetOpen.value, false)
  assert.equal(b.emailOpen.value, true)
})

test('邮箱状态加载失败不显示虚假未绑定，可使用管理员审核', async () => {
  globalThis.fetch = async () => json({ error: '无法读取邮箱状态' }, 500)
  const { bindings: b } = await mount()
  await b.openPasswordReset()
  assert.equal(b.resetEmailLoading.value, false)
  assert.equal(b.resetEmailStatus.value, null)
  assert.equal(b.canSendResetEmail.value, false)
  const html = await renderRecovery(b)
  assert.match(html, /无法读取邮箱状态/)
  assert.doesNotMatch(html, /当前账号尚未绑定并验证邮箱/)
  assert.match(await renderRecovery(b, 'admin'), /提交申请/)
})

test('邮箱掩码沿用后端规则，关闭、遮罩和邮箱设置统一清理恢复状态', async () => {
  for (const [value, expected] of [['raymond@example.com', 'r***@example.com'], ['中@example.com', '中***@example.com'], ['😀@example.com', '😀***@example.com'], ['', '***'], ['missing-at', '***'], ['@example.com', '***'], ['r@', '***']]) {
    assert.equal(maskEmail(value), expected)
  }
  const source = await readFile(new URL('../src/components/AccountMenu.vue', import.meta.url), 'utf8')
  assert.match(source, /@update:show="\(show\) => !show && closePasswordReset\(\)"/)
  assert.match(source, /title="忘记当前密码" closable @close="closePasswordReset"/)
  assert.match(source, /onUnmounted\(clearPasswordReset\)/)
  assert.doesNotMatch(source, /props\.user\.role|console\.|\/api\/account\/password-reset\/email/)
})
