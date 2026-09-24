import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import {
  agentAPILabel,
  agentCapabilities,
  agentDeclaresCapability,
  agentImplementationLabel,
  agentSupportsCapability,
  canBulkUpgradeAgent,
  chinaInboundApplyState,
  chinaInboundConfigNeedsPolling,
  chinaInboundSupported,
  formatServerExpiration,
} from '../src/server.ts'

test('服务器列表到期时间只显示日期', () => {
  assert.equal(formatServerExpiration('2026-09-12T12:00:00Z'), '2026-09-12')
})

test('服务器未设置到期时间时显示不限', () => {
  assert.equal(formatServerExpiration(null), '不限')
})

test('批量 Agent 升级候选只使用服务端能力判断和当前升级状态', () => {
  const eligible = {
    status: 'online',
    agent_can_self_upgrade: true,
    agent_version_status: 'upgrade_available',
  }
  assert.equal(canBulkUpgradeAgent(eligible), true)
  assert.equal(canBulkUpgradeAgent({ ...eligible, status: 'offline' }), false)
  assert.equal(canBulkUpgradeAgent({ ...eligible, agent_can_self_upgrade: false }), false)
  assert.equal(canBulkUpgradeAgent({ ...eligible, agent_version_status: 'up_to_date' }), false)
  assert.equal(canBulkUpgradeAgent({ ...eligible, agent_version_status: 'agent_newer' }), false)
  assert.equal(canBulkUpgradeAgent({ ...eligible, agent_upgrade_status: 'upgrading' }), false)
  assert.equal(canBulkUpgradeAgent({ ...eligible, agent_upgrade_status: 'failed' }), true)
})

test('服务器详情展示 Agent 原地升级状态和 bootstrap 命令', async () => {
  const source = (await Promise.all(["composables/useServers.ts","components/server/ServerDetail.vue"].map(path => readFile(new URL('../src/' + path, import.meta.url), 'utf8')))).join('\n')
  assert.match(source, /Agent 版本/)
  assert.match(source, /Panel 版本/)
  assert.match(source, /升级状态/)
  assert.match(source, /\/api\/servers\/\$\{value\.id\}\/agent-upgrade/)
  assert.match(source, /\/upgrade-agent\.sh/)
  assert.match(source, /\/upgrade-agent\.sh \| sh/)
  assert.doesNotMatch(source, /\/upgrade-agent\.sh \| bash/)
  assert.match(source, /state\?\.user\?\.role === 'admin'/)
})

test('高于 Panel 的 Agent 不能展示或触发降级按钮', async () => {
  const source = (await Promise.all(["composables/useServers.ts","components/server/ServerDetail.vue"].map(path => readFile(new URL('../src/' + path, import.meta.url), 'utf8')))).join('\n')
  assert.match(source, /selectedServer\.agent_version_status === 'upgrade_available'/)
  assert.match(source, /value\.agent_version_status !== 'upgrade_available'/)
  assert.match(source, /selectedServer\.agent_version_status === 'agent_newer'/)
  assert.match(source, /请先升级 Panel；不支持自动降级 Agent/)
  assert.doesNotMatch(source, /selectedServer\.agent_version !== panelReleaseVersion/)
})

test('Agent 身份显示区分官方、BoardRay 和 Legacy', () => {
  assert.equal(agentImplementationLabel('vps-panel-agent'), 'VPS Panel Agent')
  assert.equal(agentImplementationLabel('io.github.matthewlu070111.boardray'), 'BoardRay')
  assert.equal(agentImplementationLabel(''), '旧版 / 未声明')
  assert.equal(agentImplementationLabel('example.agent'), 'example.agent')
  assert.equal(agentAPILabel(0), 'Legacy')
  assert.equal(agentAPILabel(1), 'v1')
})

test('Agent 升级 UI 使用服务端安全判断并展示 metadata', async () => {
  const source = (await Promise.all(["composables/useServers.ts", "components/server/ServerDetail.vue"].map(path => readFile(new URL('../src/' + path, import.meta.url), 'utf8')))).join('\n')
  assert.match(source, /agent_can_self_upgrade/)
  assert.match(source, /Agent 类型/)
  assert.match(source, /Agent API/)
  assert.match(source, /agent_capabilities/)
  assert.doesNotMatch(source, /agent_implementation === 'vps-panel-agent'/)
})

test('Agent capability helper 保持 Legacy 兼容并严格限制 API v1', () => {
  assert.equal(agentSupportsCapability({
    agent_implementation: '', agent_api_version: 0, agent_capabilities: [],
  }, agentCapabilities.relayRealm), true)
  assert.equal(agentSupportsCapability({
    agent_implementation: 'third-party-agent', agent_api_version: 1, agent_capabilities: ['diagnostics_v1'],
  }, agentCapabilities.diagnosticsV1), true)
  assert.equal(agentSupportsCapability({
    agent_implementation: 'third-party-agent', agent_api_version: 1, agent_capabilities: ['metrics'],
  }, agentCapabilities.diagnosticsV1), false)
})

test('中国入站防火墙 capability 只接受 API v1 显式声明', () => {
  assert.equal(agentDeclaresCapability({
    agent_implementation: '', agent_api_version: 0, agent_capabilities: [],
  }, agentCapabilities.firewallCNBlock), false)
  assert.equal(agentDeclaresCapability({
    agent_implementation: 'vps-panel-agent', agent_api_version: 1, agent_capabilities: ['firewall.cn_block'],
  }, agentCapabilities.firewallCNBlock), true)
  assert.equal(agentDeclaresCapability({
    agent_implementation: 'third-party-agent', agent_api_version: 1, agent_capabilities: ['metrics'],
  }, agentCapabilities.firewallCNBlock), false)
})

test('Agent capability 常量集中定义当前 Phase 2 能力', () => {
  assert.deepEqual(agentCapabilities, {
    proxyVLESSReality: 'proxy.vless.reality',
    proxyVLESSTLSACME: 'proxy.vless.tls.acme',
    proxyVLESSTLSManual: 'proxy.vless.tls.manual',
    proxyShadowsocks: 'proxy.shadowsocks',
    relayRealm: 'relay.realm',
    outboundPreference: 'outbound_preference',
    diagnosticsV1: 'diagnostics_v1',
    firewallCNBlock: 'firewall.cn_block',
  })
})

test('中国 IP 入站限制按 desired/apply 状态和 capability 准确展示', () => {
  const server = overrides => ({
    status: 'online', archived_at: null, block_china_inbound: true,
    desired_state_version: 21, agent_applied_config_version: 20,
    agent_config_sync_status: 'pending', agent_config_sync_error: '', agent_config_synced_at: null,
    agent_implementation: 'vps-panel-agent', agent_api_version: 1,
    agent_capabilities: ['firewall.cn_block'], agent_version_status: 'up_to_date',
    ...overrides,
  })
  assert.equal(chinaInboundApplyState(server({ agent_applied_config_version: 21, agent_config_sync_status: 'success' })).label, '已生效')
  assert.equal(chinaInboundApplyState(server({ block_china_inbound: false, agent_applied_config_version: 21, agent_config_sync_status: 'success' })).label, '已关闭')
  assert.equal(chinaInboundApplyState(server({})).label, '应用中')
  assert.equal(chinaInboundApplyState(server({ block_china_inbound: false })).label, '关闭中')
  assert.equal(chinaInboundApplyState(server({ status: 'offline' })).label, '等待 Agent 上线')
  assert.equal(chinaInboundApplyState(server({ status: 'offline', block_china_inbound: false })).label, '等待 Agent 上线关闭')
  assert.equal(chinaInboundApplyState(server({ agent_config_sync_status: 'failed' })).label, '配置应用失败')

  const legacyDisabled = server({
    block_china_inbound: false, agent_implementation: '', agent_api_version: 0,
    agent_capabilities: [], agent_version_status: 'unknown',
  })
  assert.equal(chinaInboundSupported(legacyDisabled), false)
  assert.equal(chinaInboundApplyState(legacyDisabled).label, '当前 Agent 不支持')
  assert.equal(chinaInboundApplyState({ ...legacyDisabled, block_china_inbound: true }).label, '无法确认')
  const missingCapability = server({ block_china_inbound: false, agent_capabilities: [] })
  assert.equal(chinaInboundSupported(missingCapability), false)
  assert.equal(chinaInboundApplyState(missingCapability).label, '当前 Agent 不支持')
  assert.equal(chinaInboundApplyState(server({ archived_at: '2026-09-24T00:00:00Z' })).label, '只读')

  assert.equal(chinaInboundConfigNeedsPolling(server({})), true)
  assert.equal(chinaInboundConfigNeedsPolling(server({ block_china_inbound: false })), true)
  assert.equal(chinaInboundConfigNeedsPolling(server({ agent_applied_config_version: 21, agent_config_sync_status: 'success' })), false)
  assert.equal(chinaInboundConfigNeedsPolling(server({ agent_config_sync_status: 'failed' })), false)
  assert.equal(chinaInboundConfigNeedsPolling(server({ status: 'offline' })), false)
})

test('中国 IP 入站开关只出现在详情并保留原 PATCH API', async () => {
  const list = await readFile(new URL('../src/components/server/ServerList.vue', import.meta.url), 'utf8')
  const detail = await readFile(new URL('../src/components/server/ServerDetail.vue', import.meta.url), 'utf8')
  const composable = await readFile(new URL('../src/composables/useServers.ts', import.meta.url), 'utf8')
  const types = await readFile(new URL('../src/types/server.ts', import.meta.url), 'utf8')
  assert.match(types, /block_china_inbound: boolean/)
  assert.doesNotMatch(list, /禁止中国 IP 入站|NSwitch|setBlockChinaInbound/)
  for (const column of ['排序', '名称', '状态', '本周期流量', '到期时间', '操作']) assert.match(list, new RegExp(column))
  assert.match(detail, /中国 IP 入站限制/)
  assert.match(detail, /Proxy 和 Relay/)
  assert.match(detail, /IPv4 和 IPv6/)
  assert.match(detail, /APNIC/)
  assert.match(detail, /SSH 和其他服务不受影响/)
  assert.match(detail, /!server\.block_china_inbound && !chinaInboundSupported\(server\)/)
  assert.match(composable, /JSON\.stringify\(\{ block_china_inbound: enabled \}\)/)
  const requestIndex = composable.indexOf("await api<{ server: ServerRecord }>(`/api/servers/${server.id}`")
  const updateIndex = composable.indexOf('servers.value = servers.value.map', requestIndex)
  assert.ok(requestIndex >= 0 && updateIndex > requestIndex, '成功响应前不应乐观更新开关状态')
})

test('中国 IP 入站配置轮询只读取当前 Server 并在各终止条件清理', async () => {
  const source = await readFile(new URL('../src/composables/useServers.ts', import.meta.url), 'utf8')
  assert.match(source, /chinaInboundConfigPollMs = 2_000/)
  assert.match(source, /chinaInboundConfigPollTimeoutMs = 30_000/)
  assert.match(source, /api<\{ server: ServerRecord \}>\(`\/api\/servers\/\$\{serverID\}`\)/)
  assert.match(source, /startChinaInboundConfigPolling\(response\.server\)/)
  assert.match(source, /!chinaInboundConfigNeedsPolling\(response\.server\)/)
  assert.match(source, /function closeServerDetails\(\)[\s\S]*?stopChinaInboundConfigPolling\(\)/)
  assert.match(source, /function resetSession\(\)[\s\S]*?stopChinaInboundConfigPolling\(\)/)
  assert.match(source, /onScopeDispose\(\(\) => \{[\s\S]*?stopChinaInboundConfigPolling\(\)/)
})

test('服务器详情按 capability 控制诊断和出站偏好且始终允许系统默认', async () => {
  const source = await readFile(new URL('../src/components/server/ServerDetail.vue', import.meta.url), 'utf8')
  assert.match(source, /agentSupportsCapability\(selectedServer\.value, agentCapabilities\.diagnosticsV1\)/)
  assert.match(source, /agentSupportsCapability\(selectedServer\.value, agentCapabilities\.outboundPreference\)/)
  assert.match(source, /当前 Agent 不支持一键诊断/)
  assert.match(source, /当前 Agent 不支持出站 IPv4 \/ IPv6 偏好/)
  assert.match(source, /<small v-if="!outboundPreferenceSupported"[^>]*>/)
  assert.match(source, /<small v-else-if="!selectedServer\.archived_at && selectedServer\.status !== 'online'"[^>]*>/)
  const autoButton = source.match(/<n-button[^>]+setOutboundPreference\(selectedServer, 'auto'\)[^>]*>/)?.[0] ?? ''
  assert.doesNotMatch(autoButton, /outboundPreferenceSupported/)
})
