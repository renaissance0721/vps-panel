import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
const portalSource = await readFile(new URL('../src/views/SubscriberPortalView.vue', import.meta.url), 'utf8')
const managementSource = await readFile(new URL('../src/views/SubscriptionManagementView.vue', import.meta.url), 'utf8')
const relaySource = await readFile(new URL('../src/views/RelaysView.vue', import.meta.url), 'utf8')
const clientSource = await readFile(new URL('../src/components/proxy/ClientList.vue', import.meta.url), 'utf8')

test('订阅用户登录进入独立 Subscriber Portal 且不加载管理端数据', () => {
  assert.match(appSource, /state\.value\.user\?\.role === 'user' \|\| state\.value\.user\?\.role === 'subscriber'[\s\S]*stopServerPolling\(\)[\s\S]*return/)
  assert.match(appSource, /<SubscriberPortalView[^>]*state\.user\?\.role === 'subscriber'/)
  assert.doesNotMatch(portalSource, /<UserPortalView|sidebar|\/api\/(?:servers|proxies|relays|overview)/)
  for (const endpoint of [
    '/api/subscriber/me',
    '/api/subscriber/nodes',
    '/api/subscriber/subscription/regenerate',
    '/api/subscriber/password-change-request',
  ]) {
    assert.match(portalSource, new RegExp(endpoint.replaceAll('/', '\\/')))
  }
})

test('Subscriber Portal 只展示套餐、账号总量、订阅链接与发布节点名称', () => {
  for (const label of ['我的订阅', '下次重置', '到期', '可用节点', '订阅链接', '复制订阅', '重新生成', '申请修改密码']) {
    assert.match(portalSource, new RegExp(label))
  }
  assert.match(portalSource, /subscriber\.plan_name/)
  assert.match(portalSource, /subscriber\.used_bytes/)
  assert.match(portalSource, /subscriber\.traffic_limit_bytes/)
  assert.match(portalSource, /node\.name/)
  assert.doesNotMatch(portalSource, /server_name|proxy_name|client_name|UUID|SS Password|REALITY|Realm|添加中转|自定义落地/)
})

test('订阅管理只在 Admin Sidebar 显示且包含三个 Tab', () => {
  assert.match(appSource, /v-if="state\.user\?\.role === 'admin'"[\s\S]*currentPage === 'subscriptions'[\s\S]*订阅管理/)
  assert.match(appSource, /currentPage === 'subscriptions' && state\.user\?\.role === 'admin'/)
  assert.doesNotMatch(appSource, /role === 'vip'[^\n]*subscriptions/)
  for (const label of ['订阅用户', '套餐', '发布节点']) {
    assert.match(managementSource, new RegExp(`label: '${label}'`))
  }
})

test('订阅管理覆盖用户、套餐和发布节点操作且固定已创建节点拓扑', () => {
  for (const endpoint of [
    '/api/admin/subscription/users',
    '/api/admin/subscription/plans',
    '/api/admin/subscription/nodes',
    '/traffic/reset',
    '/token/regenerate',
  ]) {
    assert.match(managementSource, new RegExp(endpoint.replaceAll('/', '\\/')))
  }
  assert.match(managementSource, /v-if="!editingNode"[\s\S]*中转 Proxy[\s\S]*落地 Proxy/)
  assert.match(managementSource, /创建后不能修改模式、中转 Proxy 或落地 Proxy/)
  assert.match(managementSource, /movePlanNode/)
  assert.match(managementSource, /node_ids: planNodeIDs\.value/)
})

test('普通管理页标记并阻止修改订阅托管 Relay 与 Client', () => {
  assert.match(relaySource, /subscription_published/)
  assert.match(relaySource, /订阅发布/)
  assert.match(relaySource, /v-if="!value\.subscription_published"/)
  assert.match(clientSource, /client\.subscription_managed/)
  assert.match(clientSource, /订阅托管/)
  assert.match(clientSource, /v-if="!client\.subscription_managed"/)
})
