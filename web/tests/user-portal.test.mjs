import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
const portalSource = await readFile(new URL('../src/views/UserPortalView.vue', import.meta.url), 'utf8')
const clientFormSource = await readFile(new URL('../src/components/proxy/ClientForm.vue', import.meta.url), 'utf8')
const serverDetailSource = await readFile(new URL('../src/components/server/ServerDetail.vue', import.meta.url), 'utf8')
const relayViewSource = await readFile(new URL('../src/views/RelaysView.vue', import.meta.url), 'utf8')

test('普通 user 使用独立门户且登录初始化不加载管理数据', () => {
  assert.match(appSource, /state\.value\.user\?\.role === 'user'[\s\S]*stopServerPolling\(\)[\s\S]*return/)
  assert.match(appSource, /<UserPortalView[^>]*state\.user\?\.role === 'user'/)
  assert.doesNotMatch(portalSource, /\/api\/(?:users|servers|overview|proxies|clients|landings)(?:[/'"`])/)
  for (const endpoint of ['/api/me/nodes', '/api/me/relay-sources', '/api/me/relays', '/api/me/password-change-request']) {
    assert.match(portalSource, new RegExp(endpoint.replaceAll('/', '\\/')))
  }
})

test('普通用户门户按需请求分享并只展示轻量节点与中转表单', () => {
  assert.match(portalSource, /\/api\/me\/nodes\/\$\{node\.client_id\}\/share/)
  assert.match(portalSource, /source_client_id: sourceClientID\.value/)
  assert.match(portalSource, /source\.server_name.*source\.proxy_name/)
  assert.match(portalSource, /复制链接/)
  assert.match(portalSource, /二维码/)
  assert.match(portalSource, /落地公网 IP/)
  assert.doesNotMatch(portalSource, /REALITY Public Key|desired state|listen_address|server_id/)
})

test('用户门户各区域独立加载且管理员中转页标记用户中转', () => {
  assert.match(portalSource, /Promise\.allSettled\(\[loadNodes\(\), loadRelaySources\(\), loadRelays\(\), loadPasswordRequest\(\)\]\)/)
  for (const loader of ['loadNodes', 'loadRelaySources', 'loadRelays', 'loadPasswordRequest']) {
    assert.match(portalSource, new RegExp(`async function ${loader}\\(`))
  }
  assert.match(clientFormSource, /普通用户/)
  assert.match(clientFormSource, /付款周期/)
  assert.doesNotMatch(serverDetailSource, /普通用户中转池|user-relay-pool/)
  assert.match(relayViewSource, /用户中转/)
  assert.match(relayViewSource, /owner_username/)
})
