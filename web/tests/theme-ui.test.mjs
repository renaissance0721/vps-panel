import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const source = async (path) => readFile(new URL(`../src/${path}`, import.meta.url), 'utf8')

test('全局主题使用白色与淡蓝色 token 并统一柔和圆角', async () => {
  const [app, style] = await Promise.all([source('App.vue'), source('style.css')])
  assert.match(app, /primaryColor: '#4f9fe8'/)
  assert.match(app, /<n-config-provider :theme-overrides="themeOverrides">/)
  for (const token of ['--color-primary', '--color-primary-soft', '--color-surface', '--radius-control', '--radius-card']) {
    assert.match(style, new RegExp(token))
  }
  assert.match(style, /\.sidebar\s*\{[\s\S]*?background: transparent;[\s\S]*?\}/)
  assert.match(style, /\.sidebar-nav\s*\{[\s\S]*linear-gradient[\s\S]*backdrop-filter: blur\(14px\)/)
  assert.match(style, /\.settings-input[\s\S]*border-radius: var\(--radius-control\)/)
  assert.doesNotMatch(style, /#168a55|#18a058|#237b4b|#eef8f2|#f2faf5|#c9e7d5/)
})

test('Sidebar 和 Portal 共用液态玻璃品牌语言', async () => {
  const [app, userPortal, subscriberPortal, style] = await Promise.all([
    source('App.vue'),
    source('views/UserPortalView.vue'),
    source('views/SubscriberPortalView.vue'),
    source('style.css'),
  ])
  assert.match(app, /class="app-brand app-brand--sidebar">夕凪云/)
  assert.match(userPortal, /class="app-brand app-brand--portal">夕凪云/)
  assert.match(subscriberPortal, /class="app-brand app-brand--portal">夕凪云/)
  assert.match(style, /\.app-brand\s*\{[\s\S]*?border: 1px solid[\s\S]*?\}/)
  assert.match(style, /\.app-brand\s*\{[\s\S]*?background: linear-gradient[\s\S]*?\}/)
  assert.match(style, /\.app-brand\s*\{[\s\S]*?box-shadow:[\s\S]*?\}/)
  assert.match(style, /\.app-brand\s*\{[\s\S]*?backdrop-filter: blur\(10px\)[\s\S]*?\}/)
  assert.match(style, /\.app-brand--portal\s*\{[\s\S]*font-size: 22px/)
  assert.match(style, /\.app-brand--sidebar\s*\{[\s\S]*font-size: 18px/)
  assert.doesNotMatch(style, /\.portal-brand|\.sidebar-brand/)
})

test('Sidebar hover 与 active 使用不同层级的玻璃背景', async () => {
  const style = await source('style.css')
  assert.match(style, /\.sidebar-nav button:hover\s*\{[\s\S]*border-color:[\s\S]*linear-gradient/)
  assert.match(style, /\.sidebar-nav button\.active,[\s\S]*\.sidebar-nav button\.active:hover\s*\{[\s\S]*linear-gradient[\s\S]*box-shadow:/)
})

test('Sidebar 品牌与居中菜单使用相互独立的玻璃容器', async () => {
  const [app, style] = await Promise.all([source('App.vue'), source('style.css')])
  const sidebar = app.slice(app.indexOf('<aside class="sidebar"'), app.indexOf('</aside>'))
  assert.ok(sidebar.indexOf('app-brand--sidebar') < sidebar.indexOf('<nav class="sidebar-nav"'))
  assert.match(style, /\.sidebar-nav\s*\{[\s\S]*?margin: auto 0;[\s\S]*?padding: 10px;[\s\S]*?border: 1px solid/)
  assert.match(style, /\.app-brand--sidebar\s*\{[\s\S]*?margin: 0 8px;/)
})

test('布尔型选项统一使用 NSwitch，真正的账号和节点多选保留 checkbox', async () => {
  const booleanFiles = [
    'components/proxy/ClientForm.vue',
    'components/proxy/ProxyForm.vue',
    'views/RelaysView.vue',
    'views/CarpoolPanelView.vue',
    'views/SubscriptionManagementView.vue',
  ]
  for (const path of booleanFiles) {
    const value = await source(path)
    assert.match(value, /NSwitch/)
  }
  const client = await source('components/proxy/ClientForm.vue')
  const proxy = await source('components/proxy/ProxyForm.vue')
  const relay = await source('views/RelaysView.vue')
  assert.doesNotMatch(client, /type="checkbox"|n-checkbox/i)
  assert.doesNotMatch(proxy, /type="checkbox"|n-checkbox/i)
  assert.doesNotMatch(relay, /type="checkbox"|n-checkbox/i)

  const [serverForm, basicInfo, subscriptions] = await Promise.all([
    source('components/server/ServerForm.vue'),
    source('components/server/ServerBasicInfoForm.vue'),
    source('views/SubscriptionManagementView.vue'),
  ])
  assert.match(serverForm, /允许访问的账号[\s\S]*type="checkbox"/)
  assert.match(basicInfo, /允许访问的账号[\s\S]*type="checkbox"/)
  assert.match(subscriptions, /包含节点[\s\S]*type="checkbox"/)
})
