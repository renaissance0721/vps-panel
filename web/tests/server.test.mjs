import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { formatServerExpiration } from '../src/server.ts'

test('服务器列表到期时间只显示日期', () => {
  assert.equal(formatServerExpiration('2026-09-12T12:00:00Z'), '2026-09-12')
})

test('服务器未设置到期时间时显示不限', () => {
  assert.equal(formatServerExpiration(null), '不限')
})

test('服务器详情展示 Agent 原地升级状态和 bootstrap 命令', async () => {
  const source = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  assert.match(source, /Agent 版本/)
  assert.match(source, /Panel 版本/)
  assert.match(source, /升级状态/)
  assert.match(source, /\/api\/servers\/\$\{value\.id\}\/agent-upgrade/)
  assert.match(source, /\/upgrade-agent\.sh/)
  assert.match(source, /\/upgrade-agent\.sh \| sh/)
  assert.doesNotMatch(source, /\/upgrade-agent\.sh \| bash/)
  assert.match(source, /state\.user\?\.role === 'admin'/)
})
