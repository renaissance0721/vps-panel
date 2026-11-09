import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

test('VPS Panel skill metadata and documented tools match the MCP server', async () => {
  const skill = await readFile(new URL('../../.agents/skills/vps-panel/SKILL.md', import.meta.url), 'utf8')
  assert.match(skill, /^---\s*\nname: vps-panel\ndescription: .+\n---\s*\n/)
  assert.doesNotMatch(skill, /vpsmcp_[A-Za-z0-9_-]{20,}/)
  assert.doesNotMatch(skill, /\b(?:prepare_change|approve_change|apply_change)\b/)
  const files = await Promise.all(['mcp_tools.go', 'mcp_server_write.go', 'mcp_proxy_tools.go', 'mcp_relay_write.go', 'mcp_order.go', 'mcp_order_lists.go', 'mcp_client_p0.go', 'mcp_landing_p0.go', 'mcp_server_p0.go', 'mcp_subscription_personal.go', 'mcp_subscription_plans.go', 'mcp_subscription_nodes.go', 'mcp_subscription_users.go', 'mcp_subscription_routing.go', 'mcp_subscription_templates.go'].map(file => readFile(new URL(`../../panel/internal/api/${file}`, import.meta.url), 'utf8')))
  const registered = new Set([...files.join('\n').matchAll(/mcp(?:WriteDef|Def|ClientTool)\("([a-z_]+)"/g)].map(match => match[1]))
  for (const name of ['list_servers', 'get_panel_info', 'list_proxies', 'list_relays', 'list_proxy_clients', 'list_landings', 'list_users', 'list_personal_subscriptions', 'list_published_nodes', 'set_server_order', 'set_proxy_order', 'set_relay_order', 'set_landing_order', 'set_user_order', 'set_personal_subscription_order', 'set_published_node_order', 'update_server', 'create_server', 'create_proxy', 'update_proxy', 'set_proxy_enabled', 'delete_proxy', 'create_relay', 'update_relay', 'set_relay_enabled', 'delete_relay', 'get_proxy_client', 'create_proxy_client', 'update_proxy_client', 'delete_proxy_client', 'reset_proxy_client_traffic', 'assign_proxy_client', 'set_client_relay_port_count', 'get_proxy_client_share', 'get_landing', 'create_landing', 'update_landing', 'delete_landing', 'get_landing_share', 'get_agent_status', 'create_agent_enrollment', 'upgrade_server_agent', 'get_server_access', 'update_server_access', 'get_server_traffic', 'set_server_traffic_adjustment', 'clear_server_traffic_adjustment']) {
    assert.ok(registered.has(name), `tool ${name} is not registered`)
    assert.ok(skill.includes(`\`${name}\``), `skill does not describe ${name}`)
  }
  for (const name of ['get_personal_subscription', 'create_personal_subscription', 'update_personal_subscription', 'delete_personal_subscription', 'list_personal_subscription_sources', 'set_personal_subscription_nodes', 'set_personal_subscription_routing_bindings', 'preview_personal_subscription_mihomo', 'preview_personal_subscription_shadowrocket', 'regenerate_personal_subscription_token', 'list_subscription_plans', 'get_subscription_plan', 'create_subscription_plan', 'update_subscription_plan', 'delete_subscription_plan', 'set_subscription_plan_nodes', 'set_subscription_plan_routing_bindings', 'get_published_node', 'create_published_node', 'update_published_node', 'delete_published_node', 'list_subscription_relay_servers', 'list_subscription_users', 'get_subscription_user', 'update_subscription_user', 'reset_subscription_user_traffic', 'regenerate_subscription_user_token', 'preview_subscription_user_mihomo', 'list_routing_presets', 'get_routing_preset', 'create_routing_preset', 'update_routing_preset', 'delete_routing_preset', 'list_subscription_templates', 'get_subscription_template', 'create_subscription_template', 'update_subscription_template', 'delete_subscription_template', 'get_builtin_mihomo_configuration', 'get_builtin_shadowrocket_configuration', 'get_effective_mihomo_configuration']) {
    assert.ok(registered.has(name), `tool ${name} is not registered`)
    assert.ok(skill.includes(`\`${name}\``), `skill does not describe ${name}`)
  }
})
