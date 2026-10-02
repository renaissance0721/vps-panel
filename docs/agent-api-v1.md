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
probe.tcp
probe.icmp
```

Unknown but syntactically valid capabilities are retained. For an explicitly identified API v1 Agent, the Panel uses declared capabilities to control Proxy and Relay creation or enablement, IPv4/IPv6 outbound preference, and diagnostics UI availability. Disabling, deleting, and restoring outbound preference to `auto` remain available. Legacy Agents keep the previous compatibility behavior because their capabilities are unknown rather than empty.

`firewall.cn_block` is intentionally stricter: an Agent must identify as API v1 and explicitly declare the capability before the Panel allows the Server setting to change from disabled to enabled. Legacy Agents are not assumed to support it. Disabling the setting remains available.

Official automatic upgrade is available to an identified v1 Agent only when `implementation` is `vps-panel-agent` and `self_upgrade` is declared. A third-party Agent never receives the official `agent_upgrade` message, regardless of its version string.

## Network probes

The official Agent declares `probe.tcp` and `probe.icmp` independently. Probe assignment,
delivery, and ingestion require explicit capabilities from the current WebSocket connection.
No implementation name or release version implies support. Offline history uses the last
explicit capability report. A TCP-only Agent receives no ICMP tasks.

Probe tasks are separate from the Xray/Realm desired state. On connection and on task or
assignment changes, the Panel sends the complete enabled task list for that Agent:

```json
{"type":"probe_tasks","version":7,"tasks":[{"id":1,"name":"Tokyo","type":"tcp","target":"example.com","port":443,"interval_seconds":60}]}
```

`version` increases within a connection and resets on reconnection; the Agent starts a new
manager for each connection. An empty list stops all probes. The Agent retains unchanged
workers/timers, cancels deleted or changed workers, and starts added/changed workers.
The Panel serializes all notifications through the connection's existing write mutex.
The Agent sends results through its existing main writer loop. A failed delivery closes the
socket so that reconnection restores the complete desired list; there is no command replay.

Both ends enforce at most **64 assigned tasks per server** and an interval of **5–86400
seconds** (default 60). TCP targets are IP literals or hostnames with a separate port
(1–65535); ICMP port must be null. URLs, bracketed IPv6 and host:port strings are rejected.

TCP resolves DNS with a 2-second bound before measuring latency. It makes one concurrent
wave of connections to at most three distinct resolved addresses, including IPv4 and IPv6,
under a shared 900 ms deadline. The first successful handshake is measured from that
address's connect start, then closed without application data. There are no retries after
timeout. If every address fails, a timeout is distinguished from connection refusal.

ICMP sends one Echo with a 1-second timeout using pro-bing raw ICMP sockets (IPv4/IPv6).
RTT is the received Echo RTT. The official root service can use raw sockets; a restricted
deployment needs ICMP socket permissions (for example CAP_NET_RAW on Linux). Permission
failure is reported explicitly and is not counted as packet loss. No privileges are granted
automatically by the probe manager.

```json
{"type":"probe_result","task_id":1,"outcome":"success","latency_ms":43.2}
{"type":"probe_result","task_id":1,"outcome":"timeout"}
```

Outcomes are `success`, `timeout`, `dns_error`, `connect_error`, `permission_error`, and
`cancelled`. Success requires a finite, nonnegative `latency_ms`. Other outcomes store NULL;
negative failure sentinels are never stored. Cancelled/replaced workers are not reported.
The Panel timestamps receipt, derives server identity from authentication, and verifies the
current connection, enabled task, assignment, capability and result values before insertion.
Late results for removed/disabled tasks are discarded without breaking the Agent connection.

Admin-only task management:

- `GET /api/monitor/probes` → `{ "tasks": [...] }`
- `POST /api/monitor/probes` → `201 { "task": ... }`
- `PATCH /api/monitor/probes/{id}` → `{ "task": ... }` (omitted fields retained)
- `DELETE /api/monitor/probes/{id}` → `204`

Task fields are `name`, `type`, `target`, `port`, `interval_seconds`, `enabled`, `server_ids`.
Assignment requires a currently connected, capable, non-archived/non-decommissioning node.
Existing assignments survive disconnection; editing an assignment with offline nodes requires
removing those nodes or waiting for reconnection.

`GET /api/monitor/servers/{server_id}/latency?hours=6` follows the existing manager and
server access checks. Only 1, 6 and 24 hours are accepted (default 6). The response includes
`tasks`, `samples`, `from` and `to`; each task includes `interval_seconds`, `latest_outcome`,
`latest_latency_ms` and a 24-hour `failure_rate` percentage (null with no eligible samples).
The latest outcome is used even when unsuccessful, so old success latency does not mask
failure. TCP failure rate counts DNS/connection failures and timeouts versus successes;
ICMP loss counts timeouts versus successful Echoes only. Permission errors and cancellation
are excluded from both rates; ICMP DNS failures are excluded as well.

Schema migration 11 adds `monitor_probe_tasks`, `monitor_probe_servers`, and
`monitor_probe_records`. Task/server deletion cascades to assignments and history; server
archival also clears them. The Panel cleans records older than seven days at startup and
hourly, using an indexed timestamp. There are no rollups. The chart keeps failures as null
points and inserts a gap when consecutive reports are more than 1.5 task intervals apart.

## Legacy compatibility and versioning

An Agent that omits the three new registration fields and the implementation/API WebSocket headers is treated as Legacy (`implementation=""`, `api_version=0`). Existing Legacy behavior, including version comparison and the prior upgrade flow, remains available as a deployment transition. A version string is never proof that an Agent is official.

Additive fields, capabilities, and ignorable WebSocket message types are non-breaking changes within Agent API v1. Removing or changing required fields, authentication, message semantics, or previously defined behavior is breaking and requires a new Agent API version.
