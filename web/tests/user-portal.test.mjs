import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
const portalSource = await readFile(new URL('../src/views/UserPortalView.vue', import.meta.url), 'utf8')
const clientFormSource = await readFile(new URL('../src/components/proxy/ClientForm.vue', import.meta.url), 'utf8')
const serverDetailSource = await readFile(new URL('../src/components/server/ServerDetail.vue', import.meta.url), 'utf8')

test('普通 user 使用独立门户且登录初始化不加载管理数据', () => {
  assert.match(appSource, /state\.value\.user\?\.role === 'user'[\s\S]*stopServerPolling\(\)[\s\S]*return/)
  assert.match(appSource, /<UserPortalView[^>]*state\.user\?\.role === 'user'/)
  assert.doesNotMatch(portalSource, /\/api\/(?:users|servers|overview|proxies|clients|landings)(?:[/'"`])/)
  for (const endpoint of ['/api/me/nodes', '/api/me/relay-sources', '/api/me/relays', '/api/me/password-change-request']) {
    assert.match(portalSource, new RegExp(endpoint.replaceAll('/', '\\/')))
  }
})

test('普通用户门户按需请求分享并只展示轻量节点与中转表单', () => {
  assert.match(portalSource, /\/api\/me\/nodes\/\$\{node\.id\}\/share/)
  assert.match(portalSource, /复制链接/)
  assert.match(portalSource, /二维码/)
  assert.match(portalSource, /落地公网 IP/)
  assert.doesNotMatch(portalSource, /REALITY Public Key|desired state|listen_address|server_id/)
})

test('管理员界面提供 Client 分配和 Server 用户中转池设置', () => {
  assert.match(clientFormSource, /普通用户/)
  assert.match(clientFormSource, /付款周期/)
  assert.match(serverDetailSource, /普通用户中转池/)
  assert.match(serverDetailSource, /0\.0\.0\.0/)
  assert.match(serverDetailSource, /IPv6（::）/)
})
