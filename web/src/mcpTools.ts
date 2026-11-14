// Matches the MCP tool registrations and read-only annotations in panel/internal/api.
export const mcpTools = [
  {
    "name": "get_proxy_client",
    "description": "Get accessible Client details without credentials.",
    "write": false,
    "domain": "代理与客户端"
  },
  {
    "name": "create_proxy_client",
    "description": "Create a Client using the Panel's validation; Agent synchronization is asynchronous.",
    "write": true,
    "domain": "代理与客户端"
  },
  {
    "name": "update_proxy_client",
    "description": "Update Client fields; omit unchanged values, and use null to clear expiration or traffic_limit.",
    "write": true,
    "domain": "代理与客户端"
  },
  {
    "name": "delete_proxy_client",
    "description": "Delete a Client using existing dependent relay cleanup; expected_name must match.",
    "write": true,
    "domain": "代理与客户端"
  },
  {
    "name": "reset_proxy_client_traffic",
    "description": "Reset one Client's traffic; expected_name must match.",
    "write": true,
    "domain": "代理与客户端"
  },
  {
    "name": "assign_proxy_client",
    "description": "Assign or unassign a Client; expected_name must match and unassigning may remove dependent user relays.",
    "write": true,
    "domain": "代理与客户端"
  },
  {
    "name": "set_client_relay_port_count",
    "description": "Set the Client's reserved user relay port count, subject to current restrictions.",
    "write": true,
    "domain": "代理与客户端"
  },
  {
    "name": "get_proxy_client_share",
    "description": "Explicitly retrieve a Client's credential-bearing share URI; requires read_write and write switch.",
    "write": false,
    "domain": "代理与客户端"
  },
  {
    "name": "get_landing",
    "description": "Get an accessible external landing's metadata without its URI.",
    "write": false,
    "domain": "中转与外部节点"
  },
  {
    "name": "create_landing",
    "description": "Create an external landing using the Panel's URI validation; never returns its URI.",
    "write": true,
    "domain": "中转与外部节点"
  },
  {
    "name": "update_landing",
    "description": "Update an owned landing; dependent Relay synchronization is asynchronous.",
    "write": true,
    "domain": "中转与外部节点"
  },
  {
    "name": "delete_landing",
    "description": "Delete an owned landing subject to dependency rules; expected_name must match.",
    "write": true,
    "domain": "中转与外部节点"
  },
  {
    "name": "get_landing_share",
    "description": "Explicitly retrieve a landing's credential-bearing URI; requires read_write and write switch.",
    "write": false,
    "domain": "中转与外部节点"
  },
  {
    "name": "set_proxy_order",
    "description": "Set the complete visible proxy order for the current user; dry_run validates only.",
    "write": true,
    "domain": "排序"
  },
  {
    "name": "set_relay_order",
    "description": "Set the complete visible relay order for the current user; dry_run validates only.",
    "write": true,
    "domain": "排序"
  },
  {
    "name": "set_landing_order",
    "description": "Set the complete visible external landing order for the current user; dry_run validates only.",
    "write": true,
    "domain": "排序"
  },
  {
    "name": "set_user_order",
    "description": "Set the complete administrator-visible user order; dry_run validates only.",
    "write": true,
    "domain": "排序"
  },
  {
    "name": "set_personal_subscription_order",
    "description": "Set the order of all your personal subscriptions; dry_run validates only.",
    "write": true,
    "domain": "排序"
  },
  {
    "name": "set_published_node_order",
    "description": "Set the complete administrator-visible subscription published node order; dry_run validates only.",
    "write": true,
    "domain": "排序"
  },
  {
    "name": "list_landings",
    "description": "List accessible external landing metadata without connection credentials.",
    "write": false,
    "domain": "列表"
  },
  {
    "name": "list_users",
    "description": "List administrator-visible user IDs, names and roles without credentials.",
    "write": false,
    "domain": "列表"
  },
  {
    "name": "list_personal_subscriptions",
    "description": "List your personal subscription IDs and summaries without secrets.",
    "write": false,
    "domain": "列表"
  },
  {
    "name": "list_published_nodes",
    "description": "List subscription published node metadata without credentials.",
    "write": false,
    "domain": "列表"
  },
  {
    "name": "list_proxy_clients",
    "description": "List client identifiers on a visible proxy without credentials.",
    "write": false,
    "domain": "代理与客户端"
  },
  {
    "name": "create_proxy",
    "description": "Create a proxy using existing validation and capability checks. Agent sync is asynchronous.",
    "write": true,
    "domain": "代理与客户端"
  },
  {
    "name": "update_proxy",
    "description": "Update an existing proxy; changes use the same validation as the Panel.",
    "write": true,
    "domain": "代理与客户端"
  },
  {
    "name": "set_proxy_enabled",
    "description": "Enable or disable one proxy; Agent sync is asynchronous.",
    "write": true,
    "domain": "代理与客户端"
  },
  {
    "name": "delete_proxy",
    "description": "Delete one proxy subject to existing reference checks. expected_name must match the current name.",
    "write": true,
    "domain": "代理与客户端"
  },
  {
    "name": "create_relay",
    "description": "Create a relay with the Panel's target access and validation rules.",
    "write": true,
    "domain": "中转与外部节点"
  },
  {
    "name": "update_relay",
    "description": "Update one ordinary relay; managed relays remain protected.",
    "write": true,
    "domain": "中转与外部节点"
  },
  {
    "name": "set_relay_enabled",
    "description": "Enable or disable one ordinary relay; Agent sync is asynchronous.",
    "write": true,
    "domain": "中转与外部节点"
  },
  {
    "name": "delete_relay",
    "description": "Delete one ordinary relay. expected_name must match the current name.",
    "write": true,
    "domain": "中转与外部节点"
  },
  {
    "name": "get_routing_ini",
    "description": "Export an existing routing preset as supported INI and return its revision.",
    "write": false,
    "domain": "路由"
  },
  {
    "name": "validate_routing_ini",
    "description": "Validate routing INI without saving or fetching rule URLs.",
    "write": false,
    "domain": "路由"
  },
  {
    "name": "preview_routing_ini",
    "description": "Preview semantic changes and binding conflicts without saving; optionally render against an owned personal subscription.",
    "write": false,
    "domain": "路由"
  },
  {
    "name": "create_routing_from_ini",
    "description": "Create a routing preset from validated INI.",
    "write": true,
    "domain": "路由"
  },
  {
    "name": "update_routing_from_ini",
    "description": "Update an existing preset using its current revision; existing bound groups cannot be removed.",
    "write": true,
    "domain": "路由"
  },
  {
    "name": "get_agent_status",
    "description": "Get current Agent metadata, upgrade and configuration synchronization status.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "create_agent_enrollment",
    "description": "Create sensitive one-time Agent enrollment command for one server using the configured public Panel domain.",
    "write": true,
    "domain": "服务器与概览"
  },
  {
    "name": "upgrade_server_agent",
    "description": "Request an official Agent upgrade; upgrading is not completion. Recheck get_agent_status.",
    "write": true,
    "domain": "服务器与概览"
  },
  {
    "name": "get_server_access",
    "description": "Get a visible Server's visibility and authorized user IDs.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "update_server_access",
    "description": "Update one Server's visibility and complete authorized user list.",
    "write": true,
    "domain": "服务器与概览"
  },
  {
    "name": "get_server_traffic",
    "description": "Get a Server's traffic accounting and metric freshness.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "set_server_traffic_adjustment",
    "description": "Calibrate displayed server usage to target_used_bytes; this is not the monthly traffic limit.",
    "write": true,
    "domain": "服务器与概览"
  },
  {
    "name": "clear_server_traffic_adjustment",
    "description": "Clear a Server's traffic adjustment without resetting Agent counters.",
    "write": true,
    "domain": "服务器与概览"
  },
  {
    "name": "set_server_order",
    "description": "Set the complete top-to-bottom order of all visible active servers for the current user. dry_run validates without saving.",
    "write": true,
    "domain": "服务器与概览"
  },
  {
    "name": "create_server",
    "description": "Create a server record. Enrollment credentials are not returned; use the Panel to enroll its Agent.",
    "write": true,
    "domain": "服务器与概览"
  },
  {
    "name": "update_server",
    "description": "Update one server setting group (name, network, domains, expiry, traffic, or outbound). dry_run previews renaming only.",
    "write": true,
    "domain": "服务器与概览"
  },
  {
    "name": "get_published_node",
    "description": "Get an accessible subscription published node.",
    "write": false,
    "domain": "发布节点"
  },
  {
    "name": "create_published_node",
    "description": "Create a published node with direct or relay mode; Agent synchronization is asynchronous.",
    "write": true,
    "domain": "发布节点"
  },
  {
    "name": "update_published_node",
    "description": "Update a published node's name, enabled state, entry settings, multiplier or plan_ids; Agent sync is asynchronous.",
    "write": true,
    "domain": "发布节点"
  },
  {
    "name": "delete_published_node",
    "description": "Delete a published node subject to plan dependencies; expected_name must match.",
    "write": true,
    "domain": "发布节点"
  },
  {
    "name": "list_subscription_relay_servers",
    "description": "List relay-capable servers accessible to the current user.",
    "write": false,
    "domain": "发布节点"
  },
  {
    "name": "get_personal_subscription",
    "description": "Get your personal subscription details without subscription credentials.",
    "write": false,
    "domain": "个人订阅"
  },
  {
    "name": "create_personal_subscription",
    "description": "Create a personal subscription; no subscription URL is returned.",
    "write": true,
    "domain": "个人订阅"
  },
  {
    "name": "update_personal_subscription",
    "description": "Update personal subscription fields; changes may contain name, subscription_title, enabled, client_name, routing_preset_id, mihomo_template_id, shadowrocket_template_id. Omit unchanged fields; null clears template IDs.",
    "write": true,
    "domain": "个人订阅"
  },
  {
    "name": "delete_personal_subscription",
    "description": "Delete your personal subscription; expected_name must match.",
    "write": true,
    "domain": "个人订阅"
  },
  {
    "name": "list_personal_subscription_sources",
    "description": "List accessible personal subscription sources for a client name.",
    "write": false,
    "domain": "个人订阅"
  },
  {
    "name": "set_personal_subscription_nodes",
    "description": "Replace one personal subscription's complete ordered nodes list.",
    "write": true,
    "domain": "个人订阅"
  },
  {
    "name": "set_personal_subscription_routing_bindings",
    "description": "Replace routing bindings for one personal subscription.",
    "write": true,
    "domain": "个人订阅"
  },
  {
    "name": "preview_personal_subscription_mihomo",
    "description": "Explicitly render the actual personal Mihomo configuration; output may contain credentials and requires read_write.",
    "write": false,
    "domain": "个人订阅"
  },
  {
    "name": "preview_personal_subscription_shadowrocket",
    "description": "Explicitly render the actual personal Shadowrocket configuration; output may contain credentials and requires read_write.",
    "write": false,
    "domain": "个人订阅"
  },
  {
    "name": "regenerate_personal_subscription_token",
    "description": "Invalidate and regenerate a personal subscription token; returns the sensitive new token and requires expected_name.",
    "write": true,
    "domain": "个人订阅"
  },
  {
    "name": "list_subscription_plans",
    "description": "List shared subscription plans with stable pagination.",
    "write": false,
    "domain": "共享订阅"
  },
  {
    "name": "get_subscription_plan",
    "description": "Get one shared subscription plan and its published nodes.",
    "write": false,
    "domain": "共享订阅"
  },
  {
    "name": "create_subscription_plan",
    "description": "Create a shared plan using existing validation.",
    "write": true,
    "domain": "共享订阅"
  },
  {
    "name": "update_subscription_plan",
    "description": "Update a shared plan; changes accepts name, subscription_title, enabled, traffic_limit_bytes, routing_preset_id and template IDs. This may reconcile subscriber clients; Agent sync is asynchronous.",
    "write": true,
    "domain": "共享订阅"
  },
  {
    "name": "delete_subscription_plan",
    "description": "Delete a shared plan subject to dependency rules; expected_name must match.",
    "write": true,
    "domain": "共享订阅"
  },
  {
    "name": "set_subscription_plan_nodes",
    "description": "Replace a shared plan's ordered published node IDs; this may reconcile subscriber clients.",
    "write": true,
    "domain": "共享订阅"
  },
  {
    "name": "set_subscription_plan_routing_bindings",
    "description": "Replace routing bindings for one shared plan.",
    "write": true,
    "domain": "共享订阅"
  },
  {
    "name": "list_routing_presets",
    "description": "List routing presets in stable order.",
    "write": false,
    "domain": "路由"
  },
  {
    "name": "get_routing_preset",
    "description": "Get the complete routing preset before editing groups or rules.",
    "write": false,
    "domain": "路由"
  },
  {
    "name": "create_routing_preset",
    "description": "Create a routing preset using the existing schema and validation.",
    "write": true,
    "domain": "路由"
  },
  {
    "name": "update_routing_preset",
    "description": "Update name, enabled, groups, rule_providers or rules in changes. Arrays replace the complete field: read and preserve other entries first.",
    "write": true,
    "domain": "路由"
  },
  {
    "name": "delete_routing_preset",
    "description": "Delete an unreferenced non-default preset; expected_name must match.",
    "write": true,
    "domain": "路由"
  },
  {
    "name": "list_subscription_templates",
    "description": "List template metadata without template content.",
    "write": false,
    "domain": "订阅模板"
  },
  {
    "name": "get_subscription_template",
    "description": "Get one template including its content for editing; content may contain sensitive configuration.",
    "write": false,
    "domain": "订阅模板"
  },
  {
    "name": "create_subscription_template",
    "description": "Create a validated Mihomo or Shadowrocket template.",
    "write": true,
    "domain": "订阅模板"
  },
  {
    "name": "update_subscription_template",
    "description": "Update a template's name, enabled state or content using changes; type cannot change.",
    "write": true,
    "domain": "订阅模板"
  },
  {
    "name": "delete_subscription_template",
    "description": "Delete an unreferenced template; expected_name must match.",
    "write": true,
    "domain": "订阅模板"
  },
  {
    "name": "get_builtin_mihomo_configuration",
    "description": "Get the built-in Mihomo base configuration.",
    "write": false,
    "domain": "订阅模板"
  },
  {
    "name": "get_builtin_shadowrocket_configuration",
    "description": "Get the built-in Shadowrocket base configuration.",
    "write": false,
    "domain": "订阅模板"
  },
  {
    "name": "get_effective_mihomo_configuration",
    "description": "Get the effective Mihomo base configuration for an optional template ID.",
    "write": false,
    "domain": "订阅模板"
  },
  {
    "name": "list_subscription_users",
    "description": "List subscriber accounts without subscription credentials.",
    "write": false,
    "domain": "订阅用户"
  },
  {
    "name": "get_subscription_user",
    "description": "Get subscription user details without tokens or subscription URLs.",
    "write": false,
    "domain": "订阅用户"
  },
  {
    "name": "update_subscription_user",
    "description": "Update plan_id, enabled, expires_at, traffic reset and billing fields; subscriber Client sync is asynchronous.",
    "write": true,
    "domain": "订阅用户"
  },
  {
    "name": "reset_subscription_user_traffic",
    "description": "Reset subscriber traffic; expected_name must match the username.",
    "write": true,
    "domain": "订阅用户"
  },
  {
    "name": "regenerate_subscription_user_token",
    "description": "Regenerate a subscriber token; returns a sensitive token once, and requires expected_name.",
    "write": true,
    "domain": "订阅用户"
  },
  {
    "name": "preview_subscription_user_mihomo",
    "description": "Render sensitive subscriber configuration; this can reconcile Clients and notify Agents.",
    "write": true,
    "domain": "订阅用户"
  },
  {
    "name": "list_carpool_nodes",
    "description": "Summarize carpool assignments for administrators.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "get_latency_history",
    "description": "Summarize probe history for 1, 6 or 24 hours.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "check_relay_health",
    "description": "Analyze saved configuration; does not perform network probes.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "list_servers",
    "description": "List visible servers, optionally filtering online or offline.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "get_server_details",
    "description": "Get visible server details by ID.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "get_server_metrics",
    "description": "Get latest server metrics and freshness.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "list_proxies",
    "description": "List visible proxy metadata without credentials.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "list_relays",
    "description": "List visible relay metadata without private targets.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "list_subscriptions",
    "description": "List personal and administrator-managed subscriber summaries without credentials.",
    "write": false,
    "domain": "服务器与概览"
  },
  {
    "name": "get_panel_info",
    "description": "Identify the Panel and current authorization.",
    "write": false,
    "domain": "服务器与概览"
  }
] as const
