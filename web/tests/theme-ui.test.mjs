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
    source('views/CarpoolPortalView.vue'),
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
  assert.match(style, /\.sidebar-nav button::before\s*\{[\s\S]*linear-gradient[\s\S]*opacity: 0;[\s\S]*transition: opacity/)
  assert.match(style, /\.sidebar-nav button:hover\s*\{[\s\S]*border-color:[\s\S]*box-shadow:[\s\S]*translateX\(1px\)/)
  assert.match(style, /\.sidebar-nav button\.active::before,[\s\S]*\.sidebar-nav button\.active:hover::before\s*\{[\s\S]*opacity: 1/)
})

test('Sidebar 品牌与居中菜单使用相互独立的玻璃容器', async () => {
  const [app, style] = await Promise.all([source('App.vue'), source('style.css')])
  const sidebar = app.slice(app.indexOf('<aside class="sidebar"'), app.indexOf('</aside>'))
  assert.ok(sidebar.indexOf('app-brand--sidebar') < sidebar.indexOf('<nav class="sidebar-nav"'))
  assert.match(style, /\.sidebar-nav\s*\{[\s\S]*?margin: auto 0;[\s\S]*?padding: 10px;[\s\S]*?border: 1px solid/)
  assert.match(style, /\.app-brand--sidebar\s*\{[\s\S]*?margin: 0 8px;/)
})

test('主要交互使用统一克制动效并尊重减少动画设置', async () => {
  const [app, style] = await Promise.all([source('App.vue'), source('style.css')])
  for (const token of ['--motion-fast: 160ms', '--motion-base: 220ms', '--ease-standard', '--ease-out']) {
    assert.match(style, new RegExp(token))
  }
  assert.match(app, /<transition name="page-fade" mode="out-in" appear>[\s\S]*:key="currentPage"[\s\S]*class="admin-page-content"/)
  assert.match(style, /\.page-fade-enter-from\s*\{[\s\S]*opacity: 0;[\s\S]*translateY\(4px\)/)
  assert.match(style, /\.n-button:not\([\s\S]*:hover\s*\{[\s\S]*translateY\(-1px\)/)
  assert.match(style, /\.n-button:not\([\s\S]*:active,[\s\S]*scale\(0\.98\)/)
  assert.match(style, /\.overview-page \.n-card:hover\s*\{[\s\S]*border-color:[\s\S]*translateY\(-1px\)/)
  assert.match(style, /\.n-modal\.fade-in-scale-up-transition-enter-from,[\s\S]*scale\(0\.98\)/)
  assert.match(style, /\.n-modal-mask\.fade-in-transition-enter-active,[\s\S]*transition: opacity var\(--motion-base\)/)
  assert.match(style, /\.n-switch__rail\s*\{[\s\S]*background-color var\(--motion-base\)/)
  assert.match(style, /\.n-switch__button\s*\{[\s\S]*left var\(--motion-base\)/)
  assert.match(style, /\.server-table tbody tr:not\(\.row-dragging\):hover\s*\{[\s\S]*background-color:/)
  assert.match(style, /\.app-brand:hover\s*\{[\s\S]*translateY\(-1px\)/)
  assert.match(style, /@media \(prefers-reduced-motion: reduce\)[\s\S]*animation-iteration-count: 1 !important;[\s\S]*transition: none !important;/)
  assert.doesNotMatch(style, /animation-iteration-count:\s*infinite/)
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
  assert.match(serverForm, /允许访问的管理账号[\s\S]*type="checkbox"/)
  assert.match(basicInfo, /允许访问的管理账号[\s\S]*type="checkbox"/)
  assert.match(subscriptions, /包含节点[\s\S]*type="checkbox"/)
})
