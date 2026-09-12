import assert from 'node:assert/strict'
import test from 'node:test'

import { formatServerExpiration } from '../src/server.ts'

test('服务器列表到期时间只显示日期', () => {
  assert.equal(formatServerExpiration('2026-09-12T12:00:00Z'), '2026-09-12')
})

test('服务器未设置到期时间时显示不限', () => {
  assert.equal(formatServerExpiration(null), '不限')
})
