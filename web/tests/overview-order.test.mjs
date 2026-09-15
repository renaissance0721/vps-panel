import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
const proxies = await readFile(new URL('../src/ProxiesView.vue', import.meta.url), 'utf8')
const relays = await readFile(new URL('../src/RelaysView.vue', import.meta.url), 'utf8')
const css = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')

test('概览展示账号安全摘要及权限范围内的服务器和代理节点数量', () => {
  assert.match(app, /type Overview = \{[\s\S]*?server_count: number[\s\S]*?proxy_count: number[\s\S]*?users: \{ username: string; role: 'admin' \| 'vip' \}\[\]/)
  assert.match(app, /api<Overview>\('\/api\/overview'\)/)
  assert.match(app, /loadHealth\(\), loadServers\(\), loadUsers\(\), loadOverview\(\)/)
  assert.match(app, /if \(page === 'overview'\) \{[\s\S]*?loadOverview\(\)/)
  assert.match(app, /<n-card title="已注册账号"[\s\S]*?overview\?\.users\.length/)
  assert.match(app, /v-for="account in overview\?\.users \?\? \[\]"[\s\S]*?account\.username[\s\S]*?account\.role/)
  assert.match(app, /<n-card title="服务器"[\s\S]*?overview\?\.server_count/)
  assert.match(app, /<n-card title="代理节点"[\s\S]*?overview\?\.proxy_count/)
  assert.doesNotMatch(app, /account\.(?:id|password_hash|token)/)
  assert.match(css, /\.overview-summary-grid\s*\{[\s\S]*?repeat\(3, minmax\(0, 1fr\)\)/)
  assert.match(css, /\.overview-users\s*\{[\s\S]*?max-height:[\s\S]*?overflow-y: auto/)
  assert.match(css, /@media \(max-width: 720px\)[\s\S]*?\.overview-summary-grid\s*\{\s*grid-template-columns: 1fr/)
  assert.match(css, /\.dashboard-grid\s*\{[\s\S]*?repeat\(auto-fit,/)
})

test('服务器正常和已移除列表分别提供窄排序列和边界禁用', () => {
  assert.equal((app.match(/<th class="reorder-cell" aria-label="排序"><\/th>/g) ?? []).length, 2)
  assert.match(app, /aria-label="上移服务器"[^>]*servers\[0\]\?\.id === value\.id/)
  assert.match(app, /aria-label="下移服务器"[^>]*servers\[servers\.length - 1\]\?\.id === value\.id/)
  assert.match(app, /aria-label="上移已移除服务器"[^>]*archivedServers\[0\]\?\.id === value\.id/)
  assert.match(app, /aria-label="下移已移除服务器"[^>]*archivedServers\[archivedServers\.length - 1\]\?\.id === value\.id/)
  assert.match(app, /\/api\/servers\/\$\{value\.id\}\/reorder/)
  assert.match(app, /await loadServers\(\)[\s\S]*?serverReorderingID\.value = null/)
})

test('代理节点和中转排序禁用搜索结果、边界和正在提交的箭头', () => {
  for (const [source, kind, list, upper, lower] of [
    [proxies, 'proxies', 'proxies', '上移代理节点', '下移代理节点'],
    [relays, 'relays', 'relays', '上移中转', '下移中转'],
  ]) {
    assert.match(source, new RegExp(`/api/${kind}/\\$\\{value\\.id\\}/reorder`))
    assert.match(source, /if \(reorderingID\.value !== null \|\| search\.value\.trim\(\)\) return/)
    assert.match(source, /清除搜索后可调整顺序/)
    assert.match(source, new RegExp(`aria-label="${upper}"[^>]*reorderingID !== null[^>]*search\\.trim\\(\\)[^>]*${list}\\[0\\]\\?\\.id === value\\.id`))
    assert.match(source, new RegExp(`aria-label="${lower}"[^>]*${list}\\[${list}\\.length - 1\\]\\?\\.id === value\\.id`))
    assert.match(source, /await load(?:Proxies|Relays)\(\)/)
    assert.match(source, /<th class="reorder-cell" aria-label="排序"><\/th>/)
  }
  assert.match(css, /\.server-table \.reorder-cell\s*\{[\s\S]*?width: 50px/)
  assert.match(css, /\.reorder-controls \.n-button\s*\{[\s\S]*?width: 20px/)
})
