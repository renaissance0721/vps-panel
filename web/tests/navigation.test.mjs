import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { createNavigation } from '../src/navigation.ts'

function browserAt(path) {
  let entries = [new URL(path, 'https://panel.example')]
  let index = 0
  const listeners = new Map()
  const browser = {
    get location() { return entries[index] },
    history: {
      state: null,
      pushState(_state, _title, url) {
        entries = entries.slice(0, index + 1)
        entries.push(new URL(url, entries[index]))
        index++
      },
      replaceState(_state, _title, url) { entries[index] = new URL(url, entries[index]) },
    },
    addEventListener(name, fn) { listeners.set(name, fn) },
    removeEventListener(name, fn) { if (listeners.get(name) === fn) listeners.delete(name) },
    move(delta) {
      index += delta
      assert.ok(index >= 0 && index < entries.length)
      listeners.get('popstate')?.()
      listeners.get('hashchange')?.()
    },
    get count() { return entries.length },
    get listenerCount() { return listeners.size },
  }
  return browser
}

function setup(path, role) {
  const browser = browserAt(path)
  let route
  const navigation = createNavigation(browser, (next) => { route = next })
  if (role) navigation.authenticate(role)
  return { browser, navigation, get route() { return route } }
}

const pages = ['overview', 'monitor', 'servers', 'proxies', 'relays', 'subscriptions', 'accounts', 'carpool', 'settings', 'audit']
const tabs = ['personal', 'users', 'plans', 'nodes', 'configuration']

test('直接访问和刷新每个主页面及订阅标签时恢复路由', () => {
  for (const page of pages) {
    const app = setup(`/#/${page}`, 'admin')
    assert.equal(app.route.page, page)
    const refreshed = setup(app.browser.location.href, 'admin')
    assert.deepEqual(refreshed.route, app.route)
  }
  for (const tab of tabs) {
    const app = setup(`/#/subscriptions?tab=${tab}`, 'admin')
    assert.deepEqual(app.route, { page: 'subscriptions', tab })
    assert.deepEqual(setup(app.browser.location.href, 'admin').route, app.route)
  }
})

test('页面与标签导航支持前进后退，重复选择不增加历史记录', () => {
  const app = setup('/#/overview', 'admin')
  app.navigation.navigate({ page: 'servers', tab: 'personal' })
  app.navigation.navigate({ page: 'subscriptions', tab: 'personal' })
  app.navigation.navigate({ page: 'subscriptions', tab: 'configuration' })
  app.navigation.navigate({ page: 'subscriptions', tab: 'configuration' })
  assert.equal(app.browser.count, 4)
  app.browser.move(-1)
  assert.deepEqual(app.route, { page: 'subscriptions', tab: 'personal' })
  app.browser.move(-1)
  assert.equal(app.route.page, 'servers')
  app.browser.move(1)
  app.browser.move(1)
  assert.deepEqual(app.route, { page: 'subscriptions', tab: 'configuration' })
  assert.equal(app.browser.count, 4)
  app.navigation.dispose()
  assert.equal(app.browser.listenerCount, 0)
})

test('无效页面和标签回退，VIP 不能访问管理员页面或标签', () => {
  for (const hash of ['#/unknown', '#/servers/extra', '#wrong']) {
    const app = setup('/' + hash, 'admin')
    assert.equal(app.route.page, 'overview')
    assert.equal(app.browser.location.hash, '#/overview')
  }
  for (const tab of ['missing', ...tabs.slice(1)]) {
    const app = setup(`/#/subscriptions?tab=${tab}`, tab === 'missing' ? 'admin' : 'vip')
    assert.equal(app.route.tab, 'personal')
    assert.equal(app.browser.location.hash, '#/subscriptions?tab=personal')
  }
  for (const page of ['accounts', 'carpool', 'settings', 'audit']) {
    const app = setup(`/#/${page}`, 'vip')
    assert.equal(app.route.page, 'overview')
    app.navigation.navigate({ page, tab: 'personal' })
    assert.equal(app.route.page, 'overview')
  }
})

test('登录前保留请求，认证后才应用；登出清除当前管理路由', () => {
  const app = setup('/#/subscriptions?tab=nodes')
  assert.equal(app.route, undefined)
  app.navigation.authenticate(undefined)
  assert.equal(app.browser.location.hash, '#/subscriptions?tab=nodes')
  app.navigation.authenticate('admin')
  assert.equal(app.route.tab, 'nodes')
  app.navigation.logout()
  assert.deepEqual(app.route, { page: 'overview', tab: 'personal' })
  assert.equal(app.browser.location.hash, '#/overview')
  app.navigation.authenticate('vip')
  assert.equal(app.route.page, 'overview')
  const restricted = setup('/#/audit')
  restricted.navigation.authenticate('vip')
  assert.equal(restricted.route.page, 'overview')
})

test('邀请与邮件路径及专用门户不被管理导航改写', () => {
  for (const path of ['/register?token=example', '/verify-email?token=example', '/reset-password?token=example']) {
    const app = setup(path)
    const original = app.browser.location.href
    app.navigation.authenticate(undefined)
    app.navigation.authenticate('admin')
    assert.equal(app.browser.location.href, original)
    assert.equal(app.route, undefined)
  }
  for (const role of ['carpool', 'subscriber']) {
    const app = setup('/#/settings', role)
    assert.equal(app.route, undefined)
    app.navigation.navigate({ page: 'servers', tab: 'personal' })
    assert.equal(app.browser.location.hash, '#/settings')
    app.navigation.logout()
    assert.equal(app.browser.location.hash, '#/overview')
  }
})

test('认证流程和订阅组件接入同一导航状态', async () => {
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  const subscription = await readFile(new URL('../src/views/SubscriptionManagementView.vue', import.meta.url), 'utf8')
  assert.match(app, /navigation\.authenticate\(state\.value\.authenticated \? state\.value\.user\?\.role : undefined\)/)
  for (const action of ['initialize', 'login', 'register']) {
    assert.match(app, new RegExp(`async function ${action}\\(\\)[\\s\\S]*?await loadState\\(\\)`))
  }
  assert.match(app, /async function logout\(\)[\s\S]*?navigation\.logout\(\)/)
  assert.match(app, /:current-tab="currentSubscriptionTab"[\s\S]*?@select-tab="selectSubscriptionTab"/)
  assert.match(subscription, /emit\('selectTab', tab\.id\)/)
  assert.match(app, /isVerifyEmailPage\.value \|\| isResetPasswordPage\.value/)
  assert.match(app, /window\.history\.replaceState\(\{\}, '', '\/'\)/)
})
