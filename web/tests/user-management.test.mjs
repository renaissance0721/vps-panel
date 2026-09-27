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
  assert.match(composableSource, /将删除该用户在此节点的客户端凭据，并删除依赖此客户端创建的用户中转。是否继续？/)
})

test('用户切换及节点、中转、密码操作后立即刷新详情', () => {
  assert.match(viewSource, /@change="model\.selectUser"/)
  assert.match(viewSource, /\+ 新建客户端/)
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
  assert.match(viewSource, /relay\.source\.server_name.*relay\.source\.proxy_name/)
  assert.match(viewSource, /自定义落地/)
  assert.doesNotMatch(viewSource, /relay\.target_(?:ip|port)/)
  assert.doesNotMatch(viewSource, /source_client_id|assigned_user_id|credential|UUID/)
})

test('用户管理主区域只展示已开通节点，未开通节点只进入新建弹窗', () => {
  assert.match(viewSource, /v-for="node in assignedNodes"/)
  assert.doesNotMatch(viewSource, /v-for="node in detail\.nodes"/)
  assert.match(viewSource, /v-if="assignedNodes\.length === 0" description="暂无已开通客户端"/)
  assert.match(viewSource, /v-if="formMode === 'create'"[\s\S]*v-for="node in availableNodes"/)
  assert.match(viewSource, /node\.server_name.*node\.proxy_name/)
  assert.match(composableSource, /function openCreate\(\)[\s\S]*availableNodes\.value\[0\]/)
  assert.doesNotMatch(viewSource, /overview-summary-grid/)
})
