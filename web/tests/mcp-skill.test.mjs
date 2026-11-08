import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

test('VPS Panel skill metadata and documented tools match the MCP server', async () => {
  const skill = await readFile(new URL('../../.agents/skills/vps-panel/SKILL.md', import.meta.url), 'utf8')
  assert.match(skill, /^---\s*\nname: vps-panel\ndescription: .+\n---\s*\n/)
  assert.doesNotMatch(skill, /vpsmcp_[A-Za-z0-9_-]{20,}/)
  assert.doesNotMatch(skill, /\b(?:prepare_change|approve_change|apply_change)\b/)
  const files = await Promise.all(['mcp_tools.go', 'mcp_server_write.go', 'mcp_proxy_tools.go', 'mcp_relay_write.go', 'mcp_order.go', 'mcp_order_lists.go'].map(file => readFile(new URL(`../../panel/internal/api/${file}`, import.meta.url), 'utf8')))
  const registered = new Set([...files.join('\n').matchAll(/mcp(?:Write)?Def\("([a-z_]+)"/g)].map(match => match[1]))
  for (const name of ['list_servers', 'get_panel_info', 'list_proxies', 'list_relays', 'list_proxy_clients', 'list_landings', 'list_users', 'list_personal_subscriptions', 'list_published_nodes', 'set_server_order', 'set_proxy_order', 'set_relay_order', 'set_landing_order', 'set_user_order', 'set_personal_subscription_order', 'set_published_node_order', 'update_server', 'create_server', 'create_proxy', 'update_proxy', 'set_proxy_enabled', 'delete_proxy', 'create_relay', 'update_relay', 'set_relay_enabled', 'delete_relay']) {
    assert.ok(registered.has(name), `tool ${name} is not registered`)
    assert.ok(skill.includes(`\`${name}\``), `skill does not describe ${name}`)
  }
})
