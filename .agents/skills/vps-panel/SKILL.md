---
name: vps-panel
description: Manage and troubleshoot a connected VPS Panel using its MCP business tools, including servers, proxies, relays, subscriptions, and monitoring.
---

# VPS Panel MCP

Use the connected `vps-panel` MCP server for the user's Panel. Discover its currently available tools and input schemas; different installations may run different versions. The Panel administrator enables MCP and creates a read or read_write token in AI / MCP settings. The endpoint is the Panel origin plus `/mcp`. Codex can use the configured bearer-token environment variable; never put its value in instructions, logs, source files, or a chat response.

## Find the right resource

Use `get_panel_info` to identify the connected Panel and `list_servers`, `list_proxies`, or `list_relays` to resolve names to IDs. Ask when multiple resources match. `list_proxy_clients` returns client IDs required for proxy-targeted relays without returning credentials. Server ACLs apply even to administrators; an absent item may be inaccessible rather than nonexistent.

## Operations

- Server: `get_server_details`, `get_server_metrics`, and `get_latency_history` inspect a server; `create_server` creates a record and `update_server` changes supported settings. `update_server` supports `dry_run` for a rename only. `set_server_order` accepts all visible active `server_ids` in final top-to-bottom order and optionally `dry_run: true`. Agent enrollment credentials are not returned by `create_server`; use the Panel's existing enrollment flow if needed.
- Proxy: `create_proxy`, `update_proxy`, `set_proxy_enabled`, and `delete_proxy` use the Panel's validation. Look up the proxy first; deletion requires its current `expected_name` to guard against targeting the wrong ID.
- Relay: `check_relay_health` analyzes saved configuration, not live network reachability. Use `create_relay`, `update_relay`, `set_relay_enabled`, and `delete_relay` for ordinary managed relays. A proxy target may need both proxy and client IDs. Subscription-managed or user-owned relays have separate lifecycle restrictions.
- Subscription and carpool: `list_subscriptions` summarizes the current administrator's personal subscriptions and managed subscriber accounts; `list_carpool_nodes` summarizes assignments. Do not assume write tools exist for these domains.
- Monitor: combine server metrics and latency history with Panel status. Missing metrics are not healthy zero values. No arbitrary network probe, SSH, or shell tool is implied.

## P0 management

Discover available tools on the connected Panel first; older deployments may not have these capabilities. Resolve targets using `list_servers`, `list_proxies`, `list_proxy_clients`, `list_landings`, and `list_users`, then verify IDs and ownership before changing anything. Do not use browser automation as a fallback.

- Clients: use `get_proxy_client` and `create_proxy_client` to inspect or create clients under an existing proxy; call creation separately for each requested client. Use `update_proxy_client` for name, enabled state, UDP 443, expiration, and traffic settings: omit unchanged fields, use `null` to clear `expires_at` or `traffic_limit`. `assign_proxy_client` assigns an eligible carpool user or unassigns with `user_id: null`; reassignment can remove dependent user relays. `set_client_relay_port_count`, `reset_proxy_client_traffic`, and `delete_proxy_client` obey existing limits and dependency rules. Assignment, reset and deletion require the current `expected_name`. Check Client and Agent sync status after changes.
- External landings: create from the user's URI with `create_landing`; use `get_landing`, `update_landing`, and `delete_landing` for owned resources. Use `list_relays` and the supported `update_relay` to connect an accessible landing. Endpoint changes may require asynchronous Agent sync; query the related resources afterward.
- Sharing: `get_proxy_client_share` and `get_landing_share` explicitly expose sensitive connection URIs and require a read_write token and enabled write switch. Retrieve only when the user requests sharing, avoid unnecessary disclosure, and never put credentials into project files or logs.
- Agents: use `get_agent_status` to inspect implementation, capability, version, and synchronization; `create_agent_enrollment` creates a sensitive one-time installation command using the configured public domain. `upgrade_server_agent` may return `upgrading` or `already_current`; poll `get_agent_status` for the later outcome and never call an accepted request a completed upgrade. Error details may be redacted.
- Server settings: `get_server_access` and `update_server_access` inspect or replace visibility and the full authorized user list. `get_server_traffic`, `set_server_traffic_adjustment` with `target_used_bytes`, and `clear_server_traffic_adjustment` work on displayed server usage, not monthly limits or Client traffic. Compare before and after, and check `metrics_status` for stale data.

For an explicit authorized management request, call the appropriate supported write tool directly. Use `dry_run` when the user wants a preview or when it materially clarifies an operation; do not claim an unsupported operation was previewed. For deletion or other consequential actions, establish the intended target and obtain any confirmation required by the user's instructions and environment. Do not create approval plans or ask for an unrelated webpage approval.

For ordering, page through the relevant list until all visible items are collected. Pair `list_servers` with `set_server_order` (`server_ids`), `list_proxies` with `set_proxy_order` (`proxy_ids`), `list_relays` with `set_relay_order` (`relay_ids`), `list_landings` with `set_landing_order` (`landing_ids`), `list_users` with `set_user_order` (`user_ids`), `list_personal_subscriptions` with `set_personal_subscription_order` (`subscription_ids`), and `list_published_nodes` with `set_published_node_order` (`node_ids`). Submit every ID exactly once; optionally set `dry_run: true` to preview. Reread all pages and compare IDs after saving. If the tool or write permission is unavailable, state that no sorting was performed.

Apply only the user's requested criteria using available metadata, such as name, type, state, geography, provider and relay relationships. For relay topology, match `server_id` to its source and `target_proxy_id` to the proxy's `server_id`; use names only when unambiguous. Separate configured relationships from confirmed network health, do not guess direct-only server roles from names, and explain uncertain or dual roles and tradeoffs.

Inspect structured results: `saved` reports Panel persistence, while `agent_sync` reports the latest known server configuration status and versions. `pending`, `unknown`, and `failed` are not remote success; even a successful Agent configuration result does not establish end-to-end network reachability. If an operation reports an uncertain outcome, query current state before retrying to avoid duplicates.

If a write is rejected, check the user's current role and resource access, token scope, expiry/revocation, and the separate MCP write switch. Do not bypass these checks or request a raw token in the conversation. Old Phase 2A read_write tokens are downgraded to read during upgrade to avoid broadening their original limited permission; an administrator may create a new read_write token.
