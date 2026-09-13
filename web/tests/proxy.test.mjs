import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import {
  clientTrafficUsedBytes,
  formatClientTrafficBytes,
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

test('Shadowsocks 客户端不显示 VLESS UUID 与 UDP443 字段', () => {
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
})

test('Proxy 表单包含协议条件分支、只读方法和分享 URI 复制', async () => {
  const source = await readFile(new URL('../src/ProxiesView.vue', import.meta.url), 'utf8')
  assert.match(source, /v-model="proxyProtocol"/)
  assert.match(source, /proxyProtocol === 'vless'/)
  assert.match(source, /:disabled="proxyFormMode === 'edit'"/)
  assert.match(source, /selectedShare\.protocol === 'vless'/)
  assert.match(source, /copyValue\('uri', selectedShare\.uri\)/)
  assert.match(source, /<th>已用流量<\/th><th>最近活动<\/th>/)
  assert.match(source, /本周期上行/)
  assert.match(source, /本周期下行/)
  assert.match(source, /本周期已用/)
  assert.match(source, /last_activity_at \? formatTime/)
})
