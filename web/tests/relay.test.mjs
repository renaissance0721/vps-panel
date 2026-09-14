import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { relayNetworkLabel, relayTargetLabel } from '../src/relay.ts'

test('Relay Network 和目标显示正确', () => {
  assert.equal(relayNetworkLabel('tcp'), 'TCP')
  assert.equal(relayNetworkLabel('udp'), 'UDP')
  assert.equal(relayNetworkLabel('tcp,udp'), 'TCP + UDP')
  assert.equal(relayTargetLabel({
    target_type: 'manual', target_proxy_name: '', target_host: '2001:db8::1',
    target_port: 443, target_address_ready: true,
  }), '[2001:db8::1]:443')
  assert.equal(relayTargetLabel({
    target_type: 'proxy', target_proxy_name: '日本节点', target_host: '203.0.113.10',
    target_port: 8443, target_address_ready: true,
  }), '日本节点 · 203.0.113.10:8443')
})

test('中转导航和 CRUD 页面保持 Modal 交互', async () => {
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  const view = await readFile(new URL('../src/RelaysView.vue', import.meta.url), 'utf8')
  assert.match(app, /selectPage\('relays'\)/)
  assert.match(app, />\s*中转\s*<\/button>/)
  assert.match(app, /<RelaysView v-if="currentPage === 'relays'"/)
  assert.match(view, /<th>名称<\/th><th>服务器<\/th><th>入口 IP<\/th><th>监听端口<\/th><th>目标<\/th><th>Network<\/th><th>状态<\/th><th>操作<\/th>/)
  assert.match(view, /server_public_ipv4 \|\| '未检测'/)
  assert.match(view, /value="tcp">TCP/)
  assert.match(view, /value="udp">UDP/)
  assert.match(view, /value="tcp,udp">TCP \+ UDP/)
  assert.match(view, /targetType === 'proxy'/)
  assert.match(view, /targetHost/)
  assert.match(view, /showRelay\(value\.id\)/)
  assert.match(view, /openEdit\(value\)/)
  assert.match(view, /toggleRelay\(value\)/)
  assert.match(view, /removeRelay\(value\)/)
  assert.match(view, /<n-modal v-model:show="formOpen"/)
  assert.match(view, /<n-modal v-if="selectedRelay" v-model:show="detailOpen"/)
})

test('中转 Modal 保持小屏可用且列表只在自身容器横向滚动', async () => {
  const source = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')
  assert.match(source, /\.relay-form-card,\s*\.relay-detail-card\s*{[^}]*width:\s*min\(640px, calc\(100vw - 32px\)\)/s)
  assert.match(source, /\.relay-table\s*{[^}]*min-width:\s*1040px/s)
  assert.match(source, /@media \(max-width: 720px\)[\s\S]*\.relay-toolbar\s*{[^}]*grid-template-columns:\s*1fr/s)
})
