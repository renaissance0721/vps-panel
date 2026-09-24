# Agent API v1

Agent API v1 adds explicit implementation identity, protocol version, and capability metadata to the existing VPS Panel Agent protocol. It does not replace Agent Token authentication and does not add new URLs.

## Identity and versions

- `implementation` identifies the software, for example `vps-panel-agent` or `io.github.matthewlu070111.boardray`.
- `version` is that implementation's release version, for example `v0.4.2`.
- `api_version` is the Panel/Agent protocol version. This document defines version `1`.

Implementations use lowercase letters, digits, `.`, `_`, and `-`, with a maximum length of 128 characters. Capabilities use the same character set, have a maximum length of 64 characters, and are normalized by deduplicating and sorting them. At most 64 capabilities may be declared.

## Registration

`POST /api/agent/register` keeps its existing URL and fields and accepts three additional fields:

```json
{
  "enrollment_token": "...",
  "agent_version": "v0.4.2",
  "existing_config": false,
  "agent_implementation": "io.github.matthewlu070111.boardray",
  "agent_api_version": 1,
  "agent_capabilities": ["diagnostics_v1", "metrics"]
}
```

The response still supplies `agent_id`, `server_id`, and the long-lived `agent_token`. The one-time enrollment token is only used for registration.

## Authentication and WebSocket headers

Authenticated Agent HTTP requests and the WebSocket handshake use `Authorization: Bearer <agent_token>`. Identity metadata is informational and never replaces this token.

`GET /api/agent/ws` uses these headers:

- `X-VPS-Panel-Agent-Implementation`
- `X-VPS-Panel-Agent-Version`
- `X-VPS-Panel-Agent-API`
- `X-VPS-Panel-Agent-Capabilities`, as a comma-separated list

The implementation must match the value saved at registration. A legacy record with no implementation may be populated by its first valid identified WebSocket connection. API versions other than `0` (Legacy) and `1` are rejected.

## Existing protocol operations

- Desired state: `GET /api/agent/config` returns the desired-state `version`, the Server-level `block_china_inbound` flag, plus Xray and Realm configuration. `config_changed` tells an online Agent to fetch a newer version.
- Config result: `POST /api/agent/config/result` reports the applied desired-state version and `success` or `failed` status.
- Heartbeat: WebSocket `heartbeat` refreshes liveness.
- System information: WebSocket `system_info` reports hostname, OS, kernel, architecture, addresses, and public IPv4.
- Metrics: WebSocket `metrics` reports CPU, memory, disk, uptime, and network counters.
- Client traffic: `POST /api/agent/traffic` reports per-client uplink and downlink counters.
- Diagnostics: an Agent declaring `diagnostics_v1` can receive `diagnostic_request` and reply with `diagnostic_result`. The Panel uses capabilities from the current online connection, not stale stored metadata.

An Agent must ignore unknown WebSocket message types so that the protocol can gain additive messages without breaking older implementations.

## Capabilities

Known capabilities currently are:

```text
proxy.vless.tls.acme
proxy.vless.tls.manual
proxy.vless.reality
proxy.shadowsocks
relay.realm
outbound_preference
metrics
client_traffic
diagnostics_v1
self_upgrade
firewall.cn_block
```

Unknown but syntactically valid capabilities are retained. For an explicitly identified API v1 Agent, the Panel uses declared capabilities to control Proxy and Relay creation or enablement, IPv4/IPv6 outbound preference, and diagnostics UI availability. Disabling, deleting, and restoring outbound preference to `auto` remain available. Legacy Agents keep the previous compatibility behavior because their capabilities are unknown rather than empty.

`firewall.cn_block` is intentionally stricter: an Agent must identify as API v1 and explicitly declare the capability before the Panel allows the Server setting to change from disabled to enabled. Legacy Agents are not assumed to support it. Disabling the setting remains available.

Official automatic upgrade is available to an identified v1 Agent only when `implementation` is `vps-panel-agent` and `self_upgrade` is declared. A third-party Agent never receives the official `agent_upgrade` message, regardless of its version string.

## Legacy compatibility and versioning

An Agent that omits the three new registration fields and the implementation/API WebSocket headers is treated as Legacy (`implementation=""`, `api_version=0`). Existing Legacy behavior, including version comparison and the prior upgrade flow, remains available as a deployment transition. A version string is never proof that an Agent is official.

Additive fields, capabilities, and ignorable WebSocket message types are non-breaking changes within Agent API v1. Removing or changing required fields, authentication, message semantics, or previously defined behavior is breaking and requires a new Agent API version.
