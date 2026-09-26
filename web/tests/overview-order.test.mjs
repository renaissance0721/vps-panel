import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { adminFirst } from '../src/adminFirst.ts'
import { moveRow, persistMove } from '../src/reorder.ts'
import { proxyAddressLines } from '../src/proxy.ts'

const source = async (path) => readFile(new URL(`../src/${path}`, import.meta.url), 'utf8')

test('admin 在概览和服务器私有访问列表优先，组内顺序稳定', async () => {
  const users = [{ role: 'vip', id: 1 }, { role: 'admin', id: 2 }, { role: 'vip', id: 3 }, { role: 'admin', id: 4 }]
  assert.deepEqual(adminFirst(users).map((user) => user.id), [2, 4, 1, 3])
  assert.deepEqual(users.map((user) => user.id), [1, 2, 3, 4])
  assert.match(await source('views/OverviewView.vue'), /v-for="account in orderedUsers"/)
  assert.match(await source('composables/useServers.ts'), /adminFirst\(users\.value\)/)
  for (const path of ['components/server/ServerForm.vue', 'components/server/ServerAccessForm.vue']) {
    assert.match(await source(path), /v-for="user in orderedUsers"/)
  }
  assert.match(await source('components/server/ServerForm.vue'), /user\.id === state\?\.user\?\.id/)
})

test('概览卡片说明位于计数之前', async () => {
  const view = await source('views/OverviewView.vue')
  for (const [title, field] of [['服务器', 'server_count'], ['代理节点', 'proxy_count']]) {
    const card = view.slice(view.indexOf(`<n-card title="${title}"`))
    assert.ok(card.indexOf('overview-summary-caption') < card.indexOf(field))
  }
})

test('拖动行按目标索引移动并给出正确方向和步数', () => {
  const rows = [1, 2, 3, 4].map((id) => ({ id }))
  assert.deepEqual(moveRow(rows, 4, 2), { direction: 'up', steps: 2 })
  assert.deepEqual(rows.map((row) => row.id), [1, 4, 2, 3])
  assert.deepEqual(moveRow(rows, 1, 3), { direction: 'down', steps: 3 })
  assert.deepEqual(rows.map((row) => row.id), [4, 2, 3, 1])
  assert.equal(moveRow(rows, 2, 2), null)
})

test('拖放请求串行执行，失败时仍重新加载服务端顺序', async () => {
  const calls = []
  await persistMove({ direction: 'up', steps: 3 }, async (direction) => { calls.push(direction) }, async () => { calls.push('reload') })
  assert.deepEqual(calls, ['up', 'up', 'up', 'reload'])
  const failed = []
  await assert.rejects(persistMove({ direction: 'down', steps: 3 }, async () => {
    failed.push('step')
    if (failed.length === 2) throw Error('failed')
  }, async () => { failed.push('reload') }), /failed/)
  assert.deepEqual(failed, ['step', 'step', 'reload'])
})

test('服务器、代理和中转只从 handle 拖动，串行提交并在失败后重载', async () => {
  const files = [
    ['composables/useServers.ts', 'components/server/ServerList.vue', 'servers', 'loadServers'],
    ['composables/useProxies.ts', 'components/proxy/ProxyList.vue', 'proxies', 'loadProxies'],
    ['views/RelaysView.vue', 'views/RelaysView.vue', 'relays', 'loadRelays'],
  ]
  for (const [logicPath, viewPath, resource, reload] of files) {
    const logic = await source(logicPath)
    const view = await source(viewPath)
    assert.match(logic, /moveRow\(/)
    assert.match(logic, new RegExp(`persistMove\\(move,[\\s\\S]*?/api/${resource}/`))
    assert.match(logic, new RegExp(`persistMove\\(move,[\\s\\S]*?${reload},`))
    assert.match(view, /class="drag-handle"/)
    assert.match(view, /@dragstart="startDrag/)
    assert.match(view, /@drop\.prevent="drop/)
    assert.doesNotMatch(view, /aria-label="(?:上移|下移)/)
  }
  const server = await source('components/server/ServerList.vue')
  assert.match(server, /dropServer\(value\.id, false\)/)
  assert.match(server, /dropServer\(value\.id, true\)/)
  for (const path of ['components/proxy/ProxyList.vue', 'views/RelaysView.vue']) {
    const view = await source(path)
    assert.match(view, /清除搜索后可调整顺序/)
    assert.match(view, /!search\.trim\(\)/)
  }
})

test('代理 IP 和入口地址展示去重且处理缺失值', async () => {
  assert.deepEqual(proxyAddressLines({ server_public_ipv4: '1.2.3.4', entry_address: 'host.example' }), ['1.2.3.4', 'host.example'])
  assert.deepEqual(proxyAddressLines({ server_public_ipv4: '1.2.3.4', entry_address: '' }), ['1.2.3.4'])
  assert.deepEqual(proxyAddressLines({ server_public_ipv4: '1.2.3.4', entry_address: '1.2.3.4' }), ['1.2.3.4'])
  assert.deepEqual(proxyAddressLines({ server_public_ipv4: '', entry_address: '' }), ['未检测'])
  const view = await source('components/proxy/ProxyList.vue')
  assert.match(view, /<th>IP \/ 地址<\/th>/)
  assert.match(view, /proxyAddressLines\(value\)/)
  assert.doesNotMatch(view, /<th>出口 IP<\/th>/)
})

test('服务器名称与所有者弹窗和详情分区', async () => {
  const detail = await source('components/server/ServerDetail.vue')
  const form = await source('components/server/ServerNameForm.vue')
  const ownerForm = await source('components/server/ServerOwnerForm.vue')
  const logic = await source('composables/useServers.ts')
  const css = await source('style.css')
  assert.match(detail, /class="server-detail-grid"/)
  for (const title of ['基本信息', 'Agent', '系统信息', '动态指标']) assert.match(detail, new RegExp(`<h3[^>]*>${title}<\\/h3>`))
  assert.match(detail, /<ServerTraffic :model="model"/)
  assert.match(detail, /@click="openNameModal"/)
  assert.match(detail, /@click="openOwnerModal"/)
  assert.match(form, /v-model:value="nameInput" maxlength="100"/)
  assert.match(form, /@submit\.prevent="saveServerName"/)
  assert.match(ownerForm, /v-model\.number="ownerUserID"/)
  assert.match(ownerForm, /无所有者/)
  assert.match(ownerForm, /@submit\.prevent="saveServerOwner"/)
  assert.match(logic, /body: JSON\.stringify\(\{ name \}\)/)
  assert.match(logic, /body: JSON\.stringify\(\{ owner_user_id: selectedOwnerID \}\)/)
  assert.match(css, /\.server-modal-card\s*\{[^}]*width: min\(960px, calc\(100vw - 48px\)\)/)
  assert.match(css, /\.server-detail-grid\s*\{[^}]*grid-template-columns: repeat\(2, minmax\(0, 1fr\)\)/)
  assert.match(css, /@media \(max-width: 720px\)[\s\S]*?\.server-detail-grid \{ grid-template-columns: 1fr; \}/)
})
