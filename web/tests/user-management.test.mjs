import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
const viewSource = await readFile(new URL('../src/views/UserManagementView.vue', import.meta.url), 'utf8')
const composableSource = await readFile(new URL('../src/composables/useUserManagement.ts', import.meta.url), 'utf8')

test('用户管理导航和页面仅对 Admin 开放', () => {
  assert.match(appSource, /v-if="state\.user\?\.role === 'admin'"[\s\S]*用户管理/)
  assert.match(appSource, /currentPage === 'users' && state\.user\?\.role === 'admin'/)
  assert.match(appSource, /type AdminPage = [^\n]*'users'/)
})

test('用户管理从 User 角度复用真实 Client 且原子开通', () => {
  assert.match(composableSource, /\/api\/admin\/users/)
  assert.match(composableSource, /\/api\/admin\/users\/\$\{userID\}\/nodes/)
  assert.doesNotMatch(composableSource, /\/api\/proxies\/\$\{[^}]+\}\/clients/)
  assert.doesNotMatch(composableSource, /user_nodes/)
  assert.match(composableSource, /\/api\/clients\/\$\{node\.client\.id\}/)
  assert.match(composableSource, /method: 'DELETE'/)
  assert.match(composableSource, /将删除该用户在此节点的客户端凭据，并移除基于该节点创建的用户中转。是否继续？/)
})

test('用户切换及节点、中转、密码操作后立即刷新详情', () => {
  assert.match(viewSource, /@change="model\.selectUser"/)
  assert.match(viewSource, /开通节点/)
  assert.match(viewSource, />查看</)
  assert.match(viewSource, />编辑</)
  assert.match(viewSource, />删除</)
  assert.match(viewSource, /删除中转/)
  assert.match(viewSource, />拒绝</)
  assert.match(viewSource, />批准</)
  for (const operation of ['saveNode', 'removeNode', 'removeRelay', 'reviewPasswordRequest']) {
    assert.match(composableSource, new RegExp(`async function ${operation}\\([\\s\\S]*?await loadDetail\\(\\)`))
  }
})

test('用户管理显示 Server 与 Proxy 并提供完整 Client 管理字段', () => {
  assert.match(viewSource, /node\.server_name.*node\.proxy_name/)
  for (const label of ['客户端名称', '流量额度', '流量重置', '到期设置', '付款周期', '允许 UDP\/443', '启用节点']) {
    assert.match(viewSource, new RegExp(label))
  }
  assert.match(viewSource, /relay\.server_name.*relay\.proxy_name/)
  assert.doesNotMatch(viewSource, /source_client_id|assigned_user_id|credential|UUID/)
})
