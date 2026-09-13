import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import {
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

test('Proxy 表单包含协议条件分支、只读方法和分享 URI 复制', async () => {
  const source = await readFile(new URL('../src/ProxiesView.vue', import.meta.url), 'utf8')
  assert.match(source, /v-model="proxyProtocol"/)
  assert.match(source, /proxyProtocol === 'vless'/)
  assert.match(source, /:disabled="proxyFormMode === 'edit'"/)
  assert.match(source, /selectedShare\.protocol === 'vless'/)
  assert.match(source, /copyValue\('uri', selectedShare\.uri\)/)
})
