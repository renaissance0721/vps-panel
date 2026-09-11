import assert from 'node:assert/strict'
import test from 'node:test'

import {
  GIBIBYTE,
  TEBIBYTE,
  formatTrafficLimitInput,
  parseTrafficLimit,
  trafficWarningLevel,
} from '../src/traffic.ts'

test('月流量额度默认使用 G', () => {
  assert.deepEqual(formatTrafficLimitInput(null), { value: '', unit: 'G' })
})

test('G 和 T 正确转换为字节', () => {
  assert.equal(parseTrafficLimit('500', 'G'), 500 * GIBIBYTE)
  assert.equal(parseTrafficLimit('1', 'T'), TEBIBYTE)
  assert.equal(parseTrafficLimit('2', 'T'), 2 * TEBIBYTE)
})

test('已有额度优先使用整 T，否则使用 G', () => {
  assert.deepEqual(formatTrafficLimitInput(2 * TEBIBYTE), { value: '2', unit: 'T' })
  assert.deepEqual(formatTrafficLimitInput(500 * GIBIBYTE), { value: '500', unit: 'G' })
})

test('流量预警覆盖固定边界', () => {
  const limit = 500 * GIBIBYTE
  assert.equal(trafficWarningLevel(38 * GIBIBYTE, null), null)
  assert.equal(trafficWarningLevel(320 * GIBIBYTE, limit), null)
  assert.equal(trafficWarningLevel(450 * GIBIBYTE, limit), 'warning')
  assert.equal(trafficWarningLevel(460 * GIBIBYTE, limit), 'warning')
  assert.equal(trafficWarningLevel(500 * GIBIBYTE, limit), 'exhausted')
  assert.equal(trafficWarningLevel(620 * GIBIBYTE, limit), 'exhausted')
})

test('流量预警不会改变服务器状态', () => {
  for (const status of ['pending', 'online', 'offline']) {
    const server = { status }
    assert.equal(trafficWarningLevel(460 * GIBIBYTE, 500 * GIBIBYTE), 'warning')
    assert.equal(server.status, status)
  }
})
