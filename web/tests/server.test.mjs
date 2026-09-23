import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import {
  agentAPILabel,
  agentCapabilities,
  agentImplementationLabel,
  agentSupportsCapability,
  formatServerExpiration,
} from '../src/server.ts'

test('服务器列表到期时间只显示日期', () => {
  assert.equal(formatServerExpiration('2026-09-12T12:00:00Z'), '2026-09-12')
})

test('服务器未设置到期时间时显示不限', () => {
  assert.equal(formatServerExpiration(null), '不限')
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

test('Agent capability 常量集中定义当前 Phase 2 能力', () => {
  assert.deepEqual(agentCapabilities, {
    proxyVLESSReality: 'proxy.vless.reality',
    proxyVLESSTLSACME: 'proxy.vless.tls.acme',
    proxyVLESSTLSManual: 'proxy.vless.tls.manual',
    proxyShadowsocks: 'proxy.shadowsocks',
    relayRealm: 'relay.realm',
    outboundPreference: 'outbound_preference',
    diagnosticsV1: 'diagnostics_v1',
  })
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
