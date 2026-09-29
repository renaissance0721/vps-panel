import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
const accountSource = await readFile(new URL('../src/views/AccountManagementView.vue', import.meta.url), 'utf8')
const carpoolSource = await readFile(new URL('../src/views/CarpoolPanelView.vue', import.meta.url), 'utf8')
const composableSource = await readFile(new URL('../src/composables/useUserManagement.ts', import.meta.url), 'utf8')

test('Admin 左侧同时提供用户管理和拼车面板入口且页面保持 Admin-only', () => {
  assert.match(appSource, /currentPage === 'accounts'/)
  assert.match(appSource, /selectPage\('accounts'\)[\s\S]*用户管理/)
  assert.match(appSource, /currentPage === 'carpool'/)
  assert.match(appSource, /selectPage\('carpool'\)[\s\S]*拼车面板/)
  assert.match(appSource, /AccountManagementView v-if="currentPage === 'accounts' && state\.user\?\.role === 'admin'"/)
  assert.match(appSource, /CarpoolPanelView v-if="currentPage === 'carpool' && state\.user\?\.role === 'admin'"/)
  assert.match(appSource, /v-if="state\.user\?\.role === 'admin'"[\s\S]*selectPage\('accounts'\)/)
  assert.match(appSource, /v-if="state\.user\?\.role === 'admin'"[\s\S]*selectPage\('carpool'\)/)
})

test('顶部页面标题使用 accounts 和 carpool 的统一明确映射', () => {
  assert.match(appSource, /type AdminPage = [^\n]*'accounts'[^\n]*'carpool'/)
  assert.match(appSource, /const pageTitles: Record<AdminPage, string> = \{[\s\S]*accounts: '用户管理',[\s\S]*carpool: '拼车面板'/)
  assert.match(appSource, /<h1>\{\{ pageTitles\[currentPage\] \}\}<\/h1>/)
})

test('独立用户管理页通过 /api/users 显示全部角色账号', () => {
  assert.match(accountSource, /api<\{ users: AccessUser\[\] \}>\('\/api\/users'\)/)
  assert.match(accountSource, /<n-card v-else title="账号列表"/)
  assert.match(accountSource, /v-for="account in accounts"/)
  for (const role of ["role === 'admin'", "role === 'vip'", "role === 'subscriber'"]) {
    assert.ok(accountSource.includes(role))
  }
  for (const label of ['管理员', 'VIP', '普通用户', '订阅用户']) {
    assert.ok(accountSource.includes(label))
  }
})

test('独立用户管理页只允许删除非 Admin 且保留二次确认', () => {
  assert.match(accountSource, /v-if="account\.role !== 'admin'"[\s\S]*@click="deleteAccount\(account\)"/)
  assert.match(accountSource, /window\.confirm\(`删除用户“\$\{account\.username\}”后不可恢复，确定继续吗？`\)/)
  assert.match(accountSource, /window\.prompt\(`请输入用户名“\$\{account\.username\}”再次确认删除`\) !== account\.username/)
  assert.match(accountSource, /`\/api\/admin\/users\/\$\{account\.id\}`[\s\S]*method: 'DELETE'/)
  assert.doesNotMatch(accountSource, /<h1>用户管理<\/h1>/)
})

test('拼车面板不含账号列表且不再加载 /api/users', () => {
  assert.doesNotMatch(carpoolSource, /账号列表/)
  assert.doesNotMatch(carpoolSource, /\/api\/users/)
  assert.doesNotMatch(carpoolSource, /loadAccounts|deleteAccount|roleLabel|const accounts/)
  assert.doesNotMatch(carpoolSource, /<h1>用户管理<\/h1>/)
  assert.match(carpoolSource, /onMounted\(model\.load\)/)
})

test('拼车面板继续通过 /api/admin/users 加载普通用户', () => {
  assert.match(composableSource, /api<\{ users: AccessUser\[\] \}>\('\/api\/admin\/users'\)/)
  assert.match(carpoolSource, /<span>普通用户<\/span>/)
  assert.match(carpoolSource, /@change="model\.selectUser"/)
  assert.match(carpoolSource, /@click="model\.load">刷新/)
})

test('拼车面板保留客户端、用户中转和密码重置申请管理', () => {
  for (const label of ['客户端', '+ 新建客户端', '用户中转', '密码重置申请', '删除中转', '拒绝', '批准']) {
    assert.ok(carpoolSource.includes(label))
  }
  assert.match(carpoolSource, /v-for="node in assignedNodes"/)
  assert.match(composableSource, /\/api\/admin\/users\/\$\{userID\}\/nodes/)
  assert.match(composableSource, /\/api\/admin\/password-change-requests\/\$\{request\.id\}\/\$\{action\}/)
})

test('拼车面板从 User 角度复用真实 Client 且操作后刷新详情', () => {
  assert.doesNotMatch(composableSource, /\/api\/proxies\/\$\{[^}]+\}\/clients/)
  assert.doesNotMatch(composableSource, /user_nodes/)
  assert.match(composableSource, /\/api\/clients\/\$\{node\.client\.id\}/)
  assert.match(composableSource, /将删除该用户在此节点的客户端凭据，并删除依赖此客户端创建的用户中转。是否继续？/)
  for (const operation of ['saveNode', 'removeNode', 'removeRelay', 'reviewPasswordRequest']) {
    assert.match(composableSource, new RegExp(`async function ${operation}\\([\\s\\S]*?await loadDetail\\(\\)`))
  }
})

test('拼车面板保留完整 Client 管理字段和预留端口设置', () => {
  assert.match(carpoolSource, /node\.server_name.*node\.proxy_name/)
  for (const label of ['客户端名称', '流量额度', '流量重置', '到期设置', '付款周期', '用户中转端口数量', '中转端口', '允许 UDP/443', '启用节点']) {
    assert.ok(carpoolSource.includes(label))
  }
  assert.match(composableSource, /const userRelayPortCount = ref\(5\)/)
  assert.match(composableSource, /user_relay_port_count: userRelayPortCount\.value/)
  assert.match(carpoolSource, /v-for="count in 6"/)
  assert.match(carpoolSource, /20000–29999/)
})
