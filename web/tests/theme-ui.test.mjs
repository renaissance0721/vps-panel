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
  assert.match(style, /\.sidebar\s*\{[\s\S]*linear-gradient[\s\S]*#edf6ff/)
  assert.match(style, /\.settings-input[\s\S]*border-radius: var\(--radius-control\)/)
  assert.doesNotMatch(style, /#168a55|#18a058|#237b4b|#eef8f2|#f2faf5|#c9e7d5/)
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
