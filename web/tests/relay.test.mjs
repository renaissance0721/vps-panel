import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { relayEndpointLabel, relayNetworkLabel, relayTargetLabel } from '../src/relay.ts'

test('Relay Network 和目标显示正确', () => {
  assert.equal(relayNetworkLabel('tcp'), 'TCP')
  assert.equal(relayNetworkLabel('udp'), 'UDP')
  assert.equal(relayNetworkLabel('tcp,udp'), 'TCP + UDP')
  assert.equal(relayEndpointLabel('relay.example.com', 9502), 'relay.example.com:9502')
  assert.equal(relayEndpointLabel('2001:db8::1', 9502), '[2001:db8::1]:9502')
  assert.equal(relayEndpointLabel('', 9502), '入口地址不可用')
  assert.equal(relayTargetLabel({
    target_type: 'manual', target_proxy_name: '', target_host: '2001:db8::1',
    target_port: 443, target_address_ready: true,
  }), '[2001:db8::1]:443')
  assert.equal(relayTargetLabel({
    target_type: 'proxy', target_proxy_name: '日本节点', target_host: '203.0.113.10',
    target_port: 8443, target_address_ready: true,
  }), '日本节点 · 8443')
  assert.equal(relayTargetLabel({
    target_type: 'landing', target_proxy_name: '', target_landing_name: 'US Home',
    target_host: '2001:db8::20', target_port: 443, target_address_ready: true,
  }), 'US Home · [2001:db8::20]:443')
})

test('中转支持管理和选择已导入落地并按协议推荐 Network', async () => {
  const view = await readFile(new URL('../src/views/RelaysView.vue', import.meta.url), 'utf8')
  const manager = await readFile(new URL('../src/components/relay/LandingManagerModal.vue', import.meta.url), 'utf8')
  assert.match(view, />管理落地</)
  assert.match(view, /value="proxy">Panel Proxy/)
  assert.match(view, /value="landing">已导入落地/)
  assert.match(view, /value="manual">手动地址/)
  assert.match(view, /target_landing_id: targetLandingID\.value/)
  assert.match(view, /selected\.protocol === 'vless' \? 'tcp' : 'tcp,udp'/)
  assert.match(view, /\/api\/relays\/\$\{value\.id\}\/landing-share/)
  assert.match(view, /relayLandingShare\.uri/)
  assert.match(view, /showLandingQRCode/)
  assert.match(view, /copyLandingURI/)
  assert.match(manager, /visibility = ref<LandingVisibility>\('private'\)/)
  assert.match(manager, /公开后，其他 Panel 用户可以使用此落地创建中转，并获取可实际连接的中转节点链接。/)
  assert.match(manager, /value\.owned_by_me/)
  assert.doesNotMatch(manager, /value\.uri/)
  assert.doesNotMatch(manager, /landing\.uri/)
})

test('中转导航和 CRUD 页面保持 Modal 交互', async () => {
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  const view = await readFile(new URL('../src/views/RelaysView.vue', import.meta.url), 'utf8')
  assert.match(app, /selectPage\('relays'\)/)
  assert.match(app, />\s*中转\s*<\/button>/)
  assert.match(app, /<RelaysView v-if="currentPage === 'relays'"/)
  assert.match(view, /<th>名称<\/th><th>服务器<\/th><th>入口地址<\/th><th>监听端口<\/th><th>目标<\/th><th>客户端<\/th><th>Network<\/th><th>状态<\/th><th>操作<\/th>/)
  assert.match(view, /value\.target_client_name \|\| '—'/)
  assert.match(view, /value\.entry_address \|\| '入口地址不可用'/)
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

test('中转入口表单和派生客户端链接保持单一 canonical URI 流程', async () => {
  const view = await readFile(new URL('../src/views/RelaysView.vue', import.meta.url), 'utf8')
  assert.match(view, /v-model="entryHostMode"/)
  assert.match(view, /value="auto">自动检测公网 IPv4/)
  assert.match(view, /value="manual">手动填写/)
  assert.match(view, /entryHostMode === 'manual'/)
  assert.match(view, /selectedServerPublicIPv4 \|\| '未检测到'/)
  assert.match(view, /entry_host_mode: entryHostMode\.value/)
  assert.match(view, /entry_host: entryHost\.value/)
  assert.match(view, /\/api\/relays\/\$\{value\.id\}\/clients/)
  assert.match(view, /copyRelayClientURI\(client\)/)
  assert.match(view, /navigator\.clipboard\.writeText\(value\.uri\)/)
  assert.match(view, /手动目标不支持自动生成客户端节点链接/)
  assert.match(view, /入口地址不可用，请填写手动入口地址或等待源服务器上报公网 IPv4/)
  assert.match(view, /当前中转 Network 与该 Proxy 不兼容|client\.network_notice/)
  assert.match(view, /:disabled="!client\.network_compatible"/)
  assert.match(view, /clientStatusLabel\(value\.client\.status\)/)
})

test('中转 Modal 保持小屏可用且列表只在自身容器横向滚动', async () => {
  const source = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')
  assert.match(source, /\.relay-form-card\s*{[^}]*width:\s*min\(640px, calc\(100vw - 32px\)\)/s)
  assert.match(source, /\.relay-detail-card\s*{[^}]*width:\s*min\(960px, calc\(100vw - 48px\)\)/s)
  assert.match(source, /\.relay-table\s*{[^}]*min-width:\s*1040px/s)
  assert.match(source, /@media \(max-width: 720px\)[\s\S]*\.relay-toolbar\s*{[^}]*grid-template-columns:\s*1fr/s)
  assert.match(source, /@media \(max-width: 720px\)[\s\S]*\.relay-detail-card\s*{[^}]*width:\s*calc\(100vw - 24px\)/)
})

test('Relay 源服务器和重新启用受 relay.realm 控制但禁用删除保持可用', async () => {
  const view = await readFile(new URL('../src/views/RelaysView.vue', import.meta.url), 'utf8')
  assert.match(view, /agentSupportsCapability\(server, agentCapabilities\.relayRealm\)/)
  assert.doesNotMatch(view, /'relay\.realm'/)
  assert.match(view, /:disabled="!serverSupportsRealm\(server\.id\)"/)
  assert.match(view, /:disabled="!value\.enabled && !serverSupportsRealm\(value\.server_id\)"/)
  assert.match(view, /enabled && !selectedServerSupportsRealm/)
  assert.match(view, /当前 Agent 不支持 Realm 中转/)
  assert.match(view, /removeRelay\(value\)/)
})
