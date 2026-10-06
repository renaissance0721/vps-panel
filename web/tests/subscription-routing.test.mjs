import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import {
  MAX_ROUTING_PRESET_NAME_RUNES,
  createRoutingPresetCopyPayload,
  routingPresetCopyName,
} from '../src/subscriptionRouting.ts'

test('分流方案副本深复制全部配置且不携带记录身份', () => {
  const original = {
    id: 7,
    name: '个人自用',
    enabled: false,
    is_default: false,
    groups: [
      { key: 'grp_proxy', name: '代理', type: 'select', proxies: ['DIRECT', 'REJECT'], include_all: true },
      { key: 'grp_ai', name: 'AI', type: 'select', proxies: ['代理'], include_all: false },
    ],
    rule_providers: [
      { name: 'OpenAI', url: 'https://example.com/openai.list', type: 'http', behavior: 'classical', format: 'text', interval: 86400 },
      { name: 'Lan', url: 'https://example.com/lan.list', type: 'http', behavior: 'classical', format: 'text', interval: 3600 },
    ],
    rules: ['RULE-SET,OpenAI,AI', 'RULE-SET,Lan,DIRECT', 'MATCH,代理'],
  }

  const copy = createRoutingPresetCopyPayload(original, [original.name])

  assert.deepEqual(copy, {
    name: '个人自用 - 副本',
    enabled: original.enabled,
    groups: original.groups,
    rule_providers: original.rule_providers,
    rules: original.rules,
  })
  assert.equal('id' in copy, false)
  assert.equal('is_default' in copy, false)
  assert.notStrictEqual(copy.groups, original.groups)
  assert.notStrictEqual(copy.groups[0], original.groups[0])
  assert.notStrictEqual(copy.groups[0].proxies, original.groups[0].proxies)
  assert.notStrictEqual(copy.rule_providers, original.rule_providers)
  assert.notStrictEqual(copy.rule_providers[0], original.rule_providers[0])
  assert.notStrictEqual(copy.rules, original.rules)

  copy.groups[0].name = '已修改'
  copy.groups[0].proxies.push('AI')
  copy.rule_providers[0].url = 'https://example.com/changed.list'
  copy.rules.push('DOMAIN,example.com,DIRECT')
  assert.equal(original.groups[0].name, '代理')
  assert.deepEqual(original.groups[0].proxies, ['DIRECT', 'REJECT'])
  assert.equal(original.rule_providers[0].url, 'https://example.com/openai.list')
  assert.deepEqual(original.rules, ['RULE-SET,OpenAI,AI', 'RULE-SET,Lan,DIRECT', 'MATCH,代理'])
})

test('分流方案副本名称依次选择第一个未使用名称', () => {
  const names = ['个人自用']
  for (const expected of ['个人自用 - 副本', '个人自用 - 副本 2', '个人自用 - 副本 3']) {
    const name = routingPresetCopyName('个人自用', names)
    assert.equal(name, expected)
    names.push(name)
  }
})

test('分流方案副本名称按 Unicode 字符安全截断到后端上限', () => {
  const original = `${'长'.repeat(96)}😀😀😀😀`
  const first = routingPresetCopyName(original, [original])
  const second = routingPresetCopyName(original, [original, first])

  assert.equal(Array.from(first).length, MAX_ROUTING_PRESET_NAME_RUNES)
  assert.equal(Array.from(second).length, MAX_ROUTING_PRESET_NAME_RUNES)
  assert.match(first, / - 副本$/)
  assert.match(second, / - 副本 2$/)
  assert.doesNotMatch(first, /\uFFFD/)
  assert.doesNotMatch(second, /\uFFFD/)
})

test('分流方案操作使用独立横向按钮组，中转列表仅隐藏 Network 列', async () => {
  const [management, relays] = await Promise.all([
    readFile(new URL('../src/views/SubscriptionManagementView.vue', import.meta.url), 'utf8'),
    readFile(new URL('../src/views/RelaysView.vue', import.meta.url), 'utf8'),
  ])
  const routingList = management.slice(management.indexOf('v-for="value in routingPresets"'), management.indexOf('</n-card>', management.indexOf('v-for="value in routingPresets"')))
  assert.match(routingList, /class="routing-preset-actions"[\s\S]*>编辑<[\s\S]*>复制<[\s\S]*>删除</)
  assert.match(management, /\.routing-preset-actions\s*\{[^}]*display:\s*flex;[^}]*flex-wrap:\s*nowrap;[^}]*align-items:\s*center;[^}]*gap:\s*8px;/s)
  assert.doesNotMatch(routingList, /class="modal-actions"/)

  const relayList = relays.slice(relays.indexOf('<table class="server-table relay-table">'), relays.indexOf('</table>'))
  assert.match(relayList, /<th>名称<\/th><th>服务器<\/th><th>入口地址<\/th><th>监听端口<\/th><th>目标<\/th><th>客户端<\/th><th>状态<\/th><th>操作<\/th>/)
  assert.doesNotMatch(relayList, /<th>Network<\/th>|relayNetworkLabel\(value\.network\)/)
  assert.match(relays, /<span>Network<\/span>[\s\S]*v-model="network"/)
  assert.match(relays, /relayNetworkLabel\(selectedRelay\.network\)/)
})
