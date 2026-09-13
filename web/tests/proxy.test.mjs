import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import {
  CLIENT_GIBIBYTE,
  CLIENT_TEBIBYTE,
  clientStatusLabel,
  clientStatusTagType,
  clientTrafficCycleLabel,
  clientTrafficUsagePercentLabel,
  clientTrafficUsageLabel,
  clientTrafficUsedBytes,
  formatClientTrafficLimitInput,
  formatClientTrafficBytes,
  formatClientExpiration,
  formatClientExpirationInput,
  parseClientTrafficLimit,
  proxyListProtocolFields,
  shadowsocksMethods,
  showsVLESSClientFields,
} from '../src/proxy.ts'

test('代理列表按协议显示字段', () => {
  assert.deepEqual(proxyListProtocolFields('vless', 'reality'), {
    protocol: 'VLESS', transport: 'TCP', security: 'REALITY', flow: 'XTLS Vision',
  })
  assert.deepEqual(proxyListProtocolFields('shadowsocks'), {
    protocol: 'Shadowsocks', transport: '--', security: '--', flow: '--',
  })
})

test('Shadowsocks 创建方法固定为支持的 SS2022 AES 方法', () => {
  assert.deepEqual(shadowsocksMethods, [
    '2022-blake3-aes-128-gcm',
    '2022-blake3-aes-256-gcm',
  ])
})

test('UDP/443 客户端字段仅用于 VLESS', () => {
  assert.equal(showsVLESSClientFields('vless'), true)
  assert.equal(showsVLESSClientFields('shadowsocks'), false)
})

test('客户端流量显示本周期已用且兼容无 metrics', () => {
  const metrics = {
    cycle_uplink_bytes: 10,
    cycle_downlink_bytes: 20,
    used_bytes: 30,
    cycle_started_at: null,
    last_activity_at: null,
    updated_at: null,
  }
  assert.equal(clientTrafficUsedBytes(metrics), 30)
  assert.equal(clientTrafficUsedBytes(null), 0)
  assert.equal(formatClientTrafficBytes(0), '0 B')
  assert.equal(formatClientTrafficBytes(39.7 * 1024 ** 3), '39.7 G')
  assert.equal(clientTrafficUsageLabel(metrics, 100 * CLIENT_GIBIBYTE), '30 B / 100 G')
  assert.equal(clientTrafficUsageLabel(null, null), '0 B / 不限')
})

test('客户端流量额度支持 G、T、数字输入和不限', () => {
  assert.equal(parseClientTrafficLimit(100, 'G'), 100 * CLIENT_GIBIBYTE)
  assert.equal(parseClientTrafficLimit('1.5', 'T'), 1.5 * CLIENT_TEBIBYTE)
  assert.equal(parseClientTrafficLimit('', 'G'), null)
  assert.equal(parseClientTrafficLimit(0, 'T'), null)
  assert.equal(parseClientTrafficLimit('-1', 'G'), undefined)
  assert.equal(parseClientTrafficLimit('1e2', 'G'), undefined)
  assert.deepEqual(formatClientTrafficLimitInput(2 * CLIENT_TEBIBYTE), { value: 2, unit: 'T' })
})

test('客户端流量周期显示正确', () => {
  assert.equal(clientTrafficCycleLabel('never', 1, 1, '00:00'), '不重置')
  assert.equal(clientTrafficCycleLabel('daily', 1, 1, '03:30'), '每日 03:30')
  assert.equal(clientTrafficCycleLabel('weekly', 7, 1, '00:00'), '每周日 00:00')
  assert.equal(clientTrafficCycleLabel('monthly', 1, 31, '23:59'), '每月 31 日 23:59')
})

test('客户端生命周期状态、使用率和上海时区到期时间显示正确', () => {
  assert.equal(clientStatusLabel('normal'), '正常')
  assert.equal(clientStatusLabel('warning'), '流量预警')
  assert.equal(clientStatusLabel('exhausted'), '流量已用完')
  assert.equal(clientStatusLabel('expired'), '已到期')
  assert.equal(clientStatusLabel('disabled'), '用户禁用')
  assert.equal(clientStatusTagType('normal'), 'success')
  assert.equal(clientStatusTagType('warning'), 'warning')
  assert.equal(clientStatusTagType('exhausted'), 'error')
  assert.equal(clientTrafficUsagePercentLabel({ used_bytes: 900 }, 1000), '90%')
  assert.equal(clientTrafficUsagePercentLabel(null, null), '不限')
  assert.equal(formatClientExpiration(null), '不限')
  assert.equal(formatClientExpiration('2026-12-31T16:00:00Z'), '2027-01-01 00:00')
  assert.equal(formatClientExpirationInput('2026-12-31T16:00:00Z'), '2027-01-01T00:00')
})

test('Proxy 详情隐藏 Client UUID 并保留两种协议的分享 URI 复制', async () => {
  const source = await readFile(new URL('../src/ProxiesView.vue', import.meta.url), 'utf8')
  assert.match(source, /v-model="proxyProtocol"/)
  assert.match(source, /proxyProtocol === 'vless'/)
  assert.match(source, /:disabled="proxyFormMode === 'edit'"/)
  assert.match(source, /selectedShare\.protocol === 'vless'/)
	assert.match(source, /copyClientURI\(client\)/)
	assert.match(source, /copyShareURI\(selectedShare\.uri\)/)
	assert.match(source, /selectedShare\.protocol === 'vless' \? 'VLESS' : 'Shadowsocks'/)
  assert.match(source, /<th>已用 \/ 总量<\/th><th>周期<\/th><th>到期时间<\/th><th>最近活动<\/th>/)
	assert.match(source, /showsVLESSClientFields\(selectedProxy\.protocol\).*UDP\/443/)
	assert.doesNotMatch(source, /<th[^>]*>UUID<\/th>/)
	assert.doesNotMatch(source, /uuid_summary/)
	assert.doesNotMatch(source, /selectedShare\.client\.uuid/)
	assert.doesNotMatch(source, /复制 UUID/)
  assert.match(source, /本周期上行/)
  assert.match(source, /本周期下行/)
  assert.match(source, /本周期已用/)
  assert.match(source, /clientTrafficResetMode === 'weekly'/)
  assert.match(source, /clientTrafficResetMode === 'monthly'/)
  assert.match(source, /resetClientTraffic/)
  assert.match(source, /clientStatusLabel\(client\.status\)/)
  assert.match(source, /用户启用/)
  assert.match(source, /实际可用/)
  assert.match(source, /clientExpirationMode === 'specified'/)
  assert.match(source, /type="datetime-local"/)
  assert.match(source, /expires_at: clientExpirationMode\.value/)
  assert.match(source, /last_activity_at \? formatTime/)
  assert.doesNotMatch(source, /Reality Public Key|REALITY Public Key|reality_public_key|reality_short_id/)
})

test('代理节点详情使用加宽卡片且表单宽度保持不变', async () => {
	const source = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')
	assert.match(source, /\.proxy-detail-card\s*{[^}]*width:\s*min\(1180px, calc\(100vw - 48px\)\)/s)
	assert.match(source, /\.proxy-form-card\s*{[^}]*width:\s*min\(760px, calc\(100vw - 32px\)\)/s)
	assert.match(source, /@media \(max-width: 720px\)[\s\S]*\.proxy-detail-card\s*{[^}]*width:\s*calc\(100vw - 24px\)/)
})
