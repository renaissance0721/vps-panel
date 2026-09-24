# VPS Panel DEV.md

> 项目：`renaissance0721/vps-panel`
>
> 文档定位：VPS Panel 的**统一开发指南**。本文合并并替代原来的“总开发指导”和“前端 UI Guide”，作为后续人工开发、Codex 任务拆解、架构边界和验收的单一参考。
>
> 生成基线：`main @ 1138866a790003a66e4bffb780c331ea7dc34d87`（2026-09-23）。
>
> 若本文与当前代码发生冲突：
>
> 1. **当前实际运行行为以代码为准**；
> 2. **开发原则以根目录 `AGENTS.md` 为最高约束**；
> 3. 若是有意改变产品语义，应先改实现，再同步更新本文；
> 4. 不允许为了“让代码符合旧文档”而恢复已经被当前实现淘汰的结构。

---

# 0. 核心原则

VPS Panel 的长期目标是一个**简单、统一、可迁移、可验证**的多 VPS 管理面板，而不是机场销售系统，也不是远程 Shell 平台。

固定原则：

- 一台 Server 对应一个统一 Agent。
- Panel 保存业务状态，Agent 负责目标机上的执行。
- Panel ↔ Agent 以 **desired state + 结果回报** 为主，不下发任意 Shell。
- 第一代理后端固定为 Xray；中转后端固定为 Realm。
- Server / Proxy / Client / Relay 是当前核心业务模型，不提前引入 `CoreInstance`、`Chain`、通用 Driver、插件系统等抽象。
- 账号保持 `admin / vip` 两级，不做复杂 RBAC。
- SQLite 是当前唯一数据库，不为未来数据库提前抽象 Repository Provider。
- 前端继续使用 Vue 3 + TypeScript + Naive UI；不提前引入 Vue Router、Pinia、新 UI Framework 或通用 Schema Form。
- 所有任务遵守 `AGENTS.md`：**简单、可运行、可验证、最小 diff**。

当存在两种方案：

```text
A：直接、代码少、满足当前真实需求
B：高度抽象、扩展性强、代码多、主要服务未来需求
```

默认选择 A。

---

# 1. 当前产品边界

当前已经具备：

- admin / vip 邀请制认证
- Server public / private 访问控制
- Server 归档、彻底删除、Agent 重新绑定
- Agent 一次性 Enrollment + 长期 Token
- Agent WebSocket 在线状态、Heartbeat、静态系统信息、动态指标
- Agent 单台原地升级，以及管理员批量升级所有当前可升级的官方 Agent
- Agent API v1 identity metadata 与 capability 持久化
- Proxy / Relay / 出站偏好 capability enforcement 与 diagnostics UI 控制
- Server 到期日期
- Server 月流量统计、额度、重置周期、手动校准
- Panel ↔ Agent desired-state 配置同步
- Xray 托管
- VLESS + TCP + TLS / REALITY + XTLS Vision
- TLS 手动证书 + ACME 自动证书
- Shadowsocks 2022 multi-user
- Client 独立凭据、启停、流量、额度、周期、到期
- Realm 托管
- Relay TCP / UDP / TCP+UDP 中转
- Relay 绑定目标 Proxy / 手动地址
- Relay 可选择目标 Proxy 的某个 Client 用于派生分享 URI
- VLESS / Shadowsocks Client 分享 URI
- Proxy / Relay 二维码，本地浏览器生成
- Server Xray 出站 IPv4 / IPv6 偏好
- Server 级禁止中国 IP 访问受管 Proxy / Relay 入站
- 整站 ZIP 备份 / 恢复
- 一键诊断
- 账号级列表顺序持久化与拖拽排序

当前**没有**：

- Clash / Mihomo / sing-box 批量订阅
- 通用 Subscription URL
- 多跳 Chain
- 通用插件系统
- 通用 Agent Task Runner
- 任意命令执行 API
- 多角色 RBAC
- 历史 Metrics 图表
- 完整通知系统
- 自动 Panel URL 迁移

旧文档中的 Phase 编号只保留历史参考价值。当前继续开发时，应以**当前代码 + 本文“未实现 / 下一步”**为边界，不再机械按照旧 Phase 数字推进。

---

# 2. 当前仓库结构

后端是 Go 模块化单体：

```text
panel/
├── cmd/
│   ├── panel/                  Panel 进程入口
│   └── agent/                  官方 Agent
│       ├── xray*.go            Xray 生命周期 / renderer / apply
│       ├── realm*.go           Realm 生命周期 / renderer / apply
│       ├── china_firewall.go   中国 IP 入站限制
│       ├── acme.go             ACME
│       ├── client_traffic.go   Client 流量上报
│       ├── diagnostics.go      Agent 侧诊断
│       ├── metrics.go          系统指标
│       ├── system_info.go      静态系统信息
│       └── upgrade*.go         Agent 自升级
│
└── internal/
    ├── api/                    HTTP / WebSocket API 编排层
    ├── auth/                   用户、Session、邀请
    ├── server/                 Server 业务
    ├── agentcontrol/           Enrollment、注册、连接、配置同步、升级、诊断请求
    ├── proxy/                  Proxy / Client / 分享 / 流量
    ├── relay/                  Relay 业务与 desired state
    ├── backup/                 ZIP 备份与恢复
    ├── diagnostic/             诊断协议模型和校验
    ├── listorder/              账号级列表顺序
    ├── database/               SQLite schema / migration
    ├── token/                  Token helper
    └── version/                Release 版本比较
```

前端当前结构：

```text
web/src/
├── App.vue
├── views/
│   ├── OverviewView.vue
│   ├── ServersView.vue
│   ├── ProxiesView.vue
│   └── RelaysView.vue
├── components/
│   ├── server/
│   ├── proxy/
│   └── share/
├── composables/
│   ├── useOverview.ts
│   ├── useServers.ts
│   ├── useProxies.ts
│   ├── useProxyForm.ts
│   └── useClientForm.ts
├── types/
├── api/client.ts
├── server.ts
├── proxy.ts
├── relay.ts
├── traffic.ts
└── style.css
```

当前不需要为了“更标准”继续拆出 Controller / Repository / UseCase / Adapter / Store 等层级。

---

# 3. 核心业务模型

## 3.1 Server

Server 代表一台真实 VPS / 主机。

关系：

```text
Server
├── Agent
├── Proxy
│   └── Client
└── Relay
```

Server 不是 Proxy，也不是节点凭据。

当前 Server 的主要语义：

```text
id
name
status = pending | online | offline
visibility = public | private
outbound_preference = auto | prefer_ipv4 | prefer_ipv6
block_china_inbound = 0 | 1
desired_state_version
archived_at
expires_at
monthly_traffic_limit_bytes
traffic_count_mode = single | bidirectional
traffic_reset_day
traffic_reset_time
created_at
updated_at
```

Server 动态信息由 Agent 上报，不由用户手工编辑：

- hostname
- OS / version
- kernel
- arch
- IPv4 / IPv6
- public IPv4
- CPU
- RAM
- root disk
- uptime
- NIC RX / TX

### Server 时间语义

业务时间固定按：

```text
Asia/Shanghai
UTC+8
```

用于：

- Server 到期日期
- 月流量重置
- Client 流量周期
- Client 到期时间

数据库时间戳继续使用 UTC / Unix Timestamp。

如果月重置日为 29 / 30 / 31，而当月不存在该日期，使用该月最后一天同一时间。

---

## 3.2 Agent

Agent 是 Server 的统一执行层。

当前官方实现：

```text
vps-panel-agent
```

职责：

- 注册并保存长期身份
- 建立认证 WebSocket
- Heartbeat
- system_info
- metrics
- public IPv4 探测
- REST desired state 拉取
- 配置 apply 结果回报
- Client 流量上报
- Xray 管理
- Realm 管理
- 受管 Proxy / Relay 入站的中国 IP 限制
- ACME
- 一键诊断
- 官方 Agent 自升级

固定原则：

> 一台 Server 不拆成“监控 Agent / Xray Agent / Realm Agent / Relay Agent”。

以后即使支持第三方 Agent，也仍然是一台 Server 一个当前控制 Agent，只是 Agent implementation 可以不同。

---

## 3.3 Proxy

Proxy 表示一个真实代理入站。

当前协议：

```text
vless
shadowsocks
```

共同字段：

```text
server_id
name
protocol
listen_port
entry_host_mode = auto | manual
entry_host
enabled
config_json
```

`config_json` 是 Panel 后端严格控制的内部协议配置，不允许浏览器提交任意 Xray JSON。

### 入口地址

```text
entry_host_mode = auto
→ 使用 Server.public_ipv4

entry_host_mode = manual
→ 使用 entry_host
```

只影响客户端连接地址 / 分享 URI。

不会自动改变：

- TLS SNI
- REALITY server_name
- REALITY target
- 证书域名
- 服务端监听地址

---

## 3.4 Client

Client 是 Proxy 下的独立设备 / 用户 / 凭据。

```text
Proxy
├── PC
├── iPhone
└── Android
```

Client 不是新 Proxy，不拥有独立端口，也不运行独立 Xray。

当前 Client 支持：

- 独立凭据
- enabled
- VLESS `client_udp443`
- 流量统计
- 流量额度
- daily / weekly / monthly / never 周期
- 手动重置本周期流量
- 到期时间
- 派生实际可用状态
- 分享 URI
- QR

实际可用状态：

```text
effective_enabled
= enabled
  && !expired
  && !quota_exhausted
```

达到额度 / 到期后，Client 从 Xray desired state 中失效，但凭据不删除；周期重置或配置恢复后可继续使用原凭据。

普通 Client API / UI 不单独展示：

- VLESS UUID
- Shadowsocks password
- REALITY private key
- TLS private key

需要分享时，后端生成最终 URI。

---

## 3.5 Relay

业务层统一叫：

```text
Relay
```

UI 中文名称：

```text
中转
```

Realm 是 Agent 本地执行 Relay 的实现，不是用户层业务资源名称。

Relay 当前支持：

```text
target_type = proxy | manual
network = tcp | udp | tcp,udp
entry_host_mode = auto | manual
```

目标为 Proxy：

- 数据库存 `target_proxy_id`
- desired state 生成时实时解析目标 Proxy 地址与端口

目标为手动地址：

- 保存 Host/IP + Port

### `target_client_id` 的正确语义

当前 Relay 可以额外保存：

```text
target_client_id
```

它只用于：

- UI 显示“目标客户端”
- 派生对应 Client 的中转分享 URI / QR

它**不进入 Realm L4 转发语义**。

也就是说：

> 选中 Client 不是 Relay 的独占认证规则。

只要目标 Proxy 上其他凭据仍有效，它们在网络层仍可能通过这个 Relay 访问目标 Proxy。

删除 Client 时 `target_client_id` 使用 `ON DELETE SET NULL`，旧 Relay 仍然有效。

---

## 3.6 账号级排序

Server、Proxy、Relay 顺序是**账号偏好**，不是资源状态。

数据库：

```text
user_server_order
user_proxy_order
user_relay_order
```

排序：

- 不修改 desired state
- 不通知 Agent
- 不改变其他账号排序
- 搜索状态下 Proxy / Relay 不允许拖拽，避免“可见子集相邻关系”歧义

---

# 4. 账号、权限与资源可见性

## 4.1 角色

只保留：

```text
admin
vip
```

规则：

- 首次初始化创建唯一 admin。
- admin 可创建 24 小时一次性邀请。
- 邀请注册用户统一为 vip。
- vip 不能操作 admin invitation / 整站备份 / Agent 官方升级 / 永久删除 Server 等 admin-only 能力。
- 不做 Owner / SuperAdmin / Operator / Viewer。
- 不做自定义权限矩阵。

## 4.2 Server public / private

```text
public
→ 所有已登录账号可访问

private
→ 只有 server_access 中的账号可访问
```

重要：

> admin 不自动绕过 private Server。

角色权限和资源权限是两套独立判断。

资源继承：

```text
Server
├── Proxy
│   └── Client
└── Relay
```

- Proxy / Client 完全继承所属 Server。
- Relay 需要用户能访问源 Server。
- Relay 目标是 Proxy 时，还必须能访问目标 Proxy 所属 Server。
- 无权限资源对用户 API 尽量按不存在处理，避免 IDOR / 资源枚举。

Server access：

- 只影响用户 API / UI
- 不进入 Agent desired state
- 不递增 desired_state_version

---

# 5. Panel ↔ Agent 通信

通信分成两类：

```text
WebSocket
→ 在线状态、Heartbeat、system_info、metrics、轻量通知、诊断

REST
→ 注册、desired state、apply 结果、Client 流量、升级失败回报
```

## 5.1 当前 Agent HTTP API

现有 URL 保持稳定：

```text
POST /api/agent/register
GET  /api/agent/config
POST /api/agent/config/result
POST /api/agent/traffic
POST /api/agent/upgrade/result
GET  /api/agent/ws
```

不要为了“版本化”随意改成：

```text
/api/v1/agent/...
```

除非未来发生真正 breaking protocol change。

## 5.2 当前 WebSocket Header

当前 Agent 发送：

```text
Authorization: Bearer <agent_token>
X-VPS-Panel-Agent-Implementation: <implementation>
X-VPS-Panel-Agent-Version: <version>
X-VPS-Panel-Agent-API: 1
X-VPS-Panel-Agent-Capabilities: diagnostics_v1
```

Agent 注册与 WebSocket 会保存并刷新 `implementation`、`version`、`api_version` 和完整 capability list。明确声明 API v1 的 Agent 会按 capability 限制 Proxy、Relay 和出站偏好；诊断执行仍以当前在线连接的 `diagnostics_v1` 为最终依据。Legacy Agent（`implementation=""`、`api_version=0`）保持兼容模式，但全新的 `firewall.cn_block` 必须由 API v1 Agent 显式声明，Legacy 不自动视为支持。

## 5.3 当前消息类型

Panel → Agent：

```text
config_changed
agent_upgrade
diagnostic_request
```

Agent → Panel：

```text
heartbeat
system_info
metrics
diagnostic_result
```

双方对：

- 非 JSON
- 非文本
- `type` 为空
- 已知类型但 payload 非法

可以视为协议错误。

对于**合法 JSON + 非空未知 type**：

> 当前官方 Panel 与官方 Agent 都应忽略并继续连接。

这条规则用于前向兼容。

## 5.4 WebSocket 不承载完整配置

WebSocket `config_changed` 只通知：

```json
{
  "type": "config_changed",
  "version": 17
}
```

Agent 收到后重新：

```text
GET /api/agent/config
```

完整配置始终走 REST。

---

# 6. Desired State 与配置同步

固定控制链路：

```text
浏览器修改业务资源
↓
Panel 保存 SQLite
↓
对应 Server desired_state_version + 1
↓
如 Agent 在线，WS 发送 config_changed(version)
↓
Agent GET /api/agent/config
↓
Agent 生成候选配置
↓
校验
↓
原子替换
↓
重启 / reload
↓
健康检查
↓
成功：POST /api/agent/config/result success
失败：回滚并 POST failed
```

Agent 另有约 30 秒 REST 轮询兜底，所以 WebSocket 丢通知不会永久失步。

删除 Proxy / Client / Relay 同样遵守 desired-state 语义：

> Panel 数据库删除成功只表示“目标状态已改变”；真正远端运行态清理，以 Agent apply 结果为准。

禁止：

- 任意 Shell API
- 通用 Task Runner
- 在 WebSocket 中发送整份 Xray / Realm 配置
- Panel 直接写远端 `/etc/xray/config.json`

---

# 7. Xray 管理

当前 Agent 固定管理官方 Xray：

```text
v26.3.27
```

当前受管路径：

```text
/opt/vps-panel/xray/xray
/opt/vps-panel/xray/.managed-by-vps-panel
/etc/vps-panel/xray/config.json
/etc/vps-panel/xray/config.previous.json
/etc/systemd/system/vps-panel-xray.service   # systemd
```

Agent 只管理自己的受管路径。

如果没有 managed marker 而目标路径已有第三方内容，必须拒绝接管，不允许覆盖用户已有 Xray。

配置 apply 必须：

1. 生成完整 candidate。
2. 用真实 Xray binary 校验。
3. 保存 previous。
4. 原子替换 current。
5. 重启服务。
6. 检查 listener / 运行状态。
7. 失败恢复 previous。

Xray 和 Realm 的 rollback 独立，不互相覆盖。

---

# 8. VLESS

当前固定服务端语义：

```text
protocol = VLESS
transport = TCP
flow = xtls-rprx-vision
security = tls | reality
```

支持：

```text
VLESS + TCP + TLS + XTLS Vision
VLESS + TCP + REALITY + XTLS Vision
```

## 8.1 Client UDP/443

`client_udp443` 是 Client 分享参数，不是服务端入站模式。

服务端所有 Client flow 仍固定：

```text
xtls-rprx-vision
```

客户端 URI：

```text
client_udp443 = false
→ xtls-rprx-vision

client_udp443 = true
→ xtls-rprx-vision-udp443
```

不要把 `-udp443` 写进服务端 Xray Client flow。

## 8.2 TLS

TLS 支持：

```text
acme
manual
```

### 自动 ACME

当前：

- Let's Encrypt
- HTTP-01 standalone
- ECC P-256
- 每约 12 小时检查
- 到期前约 30 天尝试续期
- 申请时临时处理 TCP 80 防火墙

不支持：

- DNS-01
- wildcard
- 自定义 CA

自动模式私钥不进入 Panel 数据库和 desired state。

受管路径：

```text
/opt/vps-panel/acme/acme.sh
/var/lib/vps-panel/acme/
/etc/vps-panel/xray/certs/<domain>/fullchain.pem
/etc/vps-panel/xray/certs/<domain>/private.key
```

删除 Proxy 不自动删除证书，避免影响同域名其他节点。

### 手动 TLS

手动模式允许 Panel 保存所需 PEM 并下发给 Agent。

敏感 TLS private key 不应出现在普通详情 API / UI / 日志中。

## 8.3 REALITY

REALITY 私钥由 Panel 业务层管理后进入 desired state 给官方 Agent；普通分享只输出客户端所需 public key，不输出 private key。

当前分享 URI 应复用项目已有 canonical generator，不在 UI 重新拼协议 URL。

---

# 9. Shadowsocks

当前支持 Shadowsocks 2022：

```text
2022-blake3-aes-128-gcm
2022-blake3-aes-256-gcm
```

特点：

- multi-user
- TCP + UDP
- Client 独立 password
- Agent 维护 TCP / UDP 防火墙规则
- 分享使用 SIP002 URI

Shadowsocks 不适用：

- TLS
- REALITY
- XTLS Flow
- VLESS `client_udp443`

UI 对不适用字段显示 `--`，不要制造虚假概念。

---

# 10. Xray 出站 IPv4 / IPv6 偏好

Server 当前可设置：

```text
auto
prefer_ipv4
prefer_ipv6
```

官方 Agent 通过 Xray Freedom outbound 的：

```text
streamSettings.sockopt.domainStrategy
```

实现：

```text
auto
→ 不强行写策略

prefer_ipv4
→ UseIPv4v6

prefer_ipv6
→ UseIPv6v4
```

固定边界：

- 不改 `/etc/gai.conf`
- 不改系统 route
- 不改 sysctl
- 不改系统 DNS
- 不影响 Realm

这是 Xray 受管配置的一部分，修改会递增 desired state version。

---

# 11. Realm / Relay

Agent 固定使用官方 Realm：

```text
v2.9.4
```

当前受管路径：

```text
/opt/vps-panel/realm/realm
/opt/vps-panel/realm/.managed-by-vps-panel
/etc/vps-panel/realm/config.toml
/etc/vps-panel/realm/config.previous.toml
/etc/systemd/system/vps-panel-realm.service   # systemd
/etc/init.d/vps-panel-realm                   # Alpine OpenRC
```

同一 Server 的全部 enabled Relay 合并为一个 Realm 进程、多 endpoint 配置。

支持：

```text
TCP
UDP
TCP + UDP
```

Relay target 为 Proxy 时：

- 实际 target host 根据目标 Proxy 的 entry mode 动态解析
- 实际 target port 使用目标 Proxy listen_port
- Proxy 地址 / 端口改变时，所有引用它的 Relay source Server 必须 bump desired state
- 目标 Server public IPv4 改变时，使用 auto 地址的 Relay 也必须 bump

Realm apply：

- candidate 校验
- 原子替换
- restart
- TCP listener probe
- UDP local socket probe
- 独立防火墙 owner
- 失败回滚

禁用最后一个 Relay：

- 停止 / disable Realm
- 清理 current / previous config
- 清理 Realm-owned 防火墙规则
- 保留 binary / marker / service 文件供再次启用

## 11.1 禁止中国 IP 入站

Server 的 `block_china_inbound` 只限制 desired state 中 VPS Panel 管理的 VLESS、Shadowsocks 和 Realm listener 端口。Agent 从 APNIC delegated 数据提取 CN（不含 HK / MO / TW）的 IPv4 / IPv6 前缀，缓存到 `/var/lib/vps-panel/agent/firewall/cn-prefixes.json`，并使用独立拥有的 `inet vps_panel_cn_block` nftables 表和 interval set 应用规则。

该功能不扫描、不检测也不修改 SSH 或其他系统服务端口，不 flush 系统 ruleset，也不接管用户、UFW 或 Docker 的规则。启用必须由 API v1 Agent 显式声明 `firewall.cn_block`；Legacy Agent 不自动视为支持。

---

# 12. 分享与二维码

## 12.1 Client 直连 URI

连接地址：

```text
Proxy.entry_host_mode = manual
→ entry_host

Proxy.entry_host_mode = auto
→ Server.public_ipv4
```

每个 Client 独立生成自己的 URI。

当前支持：

- VLESS URI
- Shadowsocks SIP002 URI
- 复制
- 浏览器本地 QR

QR 必须使用本地前端库生成。

禁止把含凭据 URI 发送给第三方二维码服务。

## 12.2 Relay 派生 URI

Relay 选择一个 `target_client_id` 后：

```text
目标 Client 原凭据
+
目标 Proxy 协议参数
+
Relay 客户端入口地址 / 端口
=
Relay 分享 URI
```

只替换客户端连接 endpoint，不改变协议密钥和目标 Proxy 业务配置。

当前未实现：

- 通用订阅 URL
- Clash / Mihomo 批量订阅
- sing-box 批量配置
- 订阅权限系统
- 多节点订阅选择器

后续做订阅时必须复用现有 canonical share generator，不重写 VLESS / SS 参数语义。

---

# 13. Server / Client 流量

## 13.1 Server 流量

Server 总流量来自 Linux 网卡累计计数：

```text
NIC RX / TX
```

不是 Xray Client 流量之和。

模式：

```text
single
→ 当前按 TX 计费

bidirectional
→ RX + TX
```

支持：

- 月额度
- 每月重置日 / 时间
- 90% warning
- 100% exhausted
- 手动校准

手动校准通过 adjustment 表达，不篡改 Agent 原始 NIC counter。

## 13.2 Client 流量

Client 流量来自 Xray per-client stats。

Agent 使用稳定非敏感 stats ID，例如内部 `vp-client-*`，不把 UUID / password 当统计标识。

Agent 约每 15 秒走：

```text
POST /api/agent/traffic
```

Panel 保存：

- Xray baseline
- cycle uplink
- cycle downlink
- last activity

Server 流量和 Client 流量是两套独立口径，不互相反推。

---

# 14. 一键诊断

入口：

```text
POST /api/servers/{id}/diagnostics
```

当前要求 Agent 在线且当前 WebSocket 声明：

```text
diagnostics_v1
```

## 14.1 Panel 侧检查

包括：

- Agent 在线状态
- desired / applied config version
- 最近一次 config sync 状态
- 从 Panel 所在网络探测节点公网 TCP 入口

## 14.2 Agent 侧检查

包括：

- Xray service
- 当前 Xray config 文件状态 / 语法
- Xray TCP listener
- Realm service
- Realm TCP listener
- Realm UDP local socket
- Relay DNS
- Relay target TCP
- TLS 证书存在、有效期、域名匹配

## 14.3 明确不表示

当前诊断**不是**完整协议健康检查。

不做：

- VLESS 完整认证握手
- Shadowsocks 完整协议握手
- 远端 UDP 可达性
- 自动修复
- 修改系统状态

因此：

> TCP connect 成功只表示指定网络位置可以建立 TCP 连接，不等于代理协议一定可用。

## 14.4 UI 例外

资源查看仍统一使用 Modal。

“一键诊断”当前使用右侧 Drawer，是专门的临时诊断结果面板，不视为资源详情页；不要因此把 Server / Proxy / Relay 详情改成 Drawer。

---

# 15. 备份与恢复

当前整站备份已经实现，不再属于未来 Phase。

入口：admin only。

```text
GET  /api/admin/backup/export
POST /api/admin/backup/import
```

## 15.1 导出

当前使用 SQLite：

```text
VACUUM INTO
```

生成一致性快照，包含已提交 WAL 数据。

ZIP 当前包含：

```text
manifest.json
data/panel.db
deployment/environment          # 有则加入
deployment/vps-panel.caddy      # 有则加入
SHA256SUMS
```

备份格式：

```text
format = vps-panel-backup
format_version = 1
```

备份文件是高敏感文件，因为 SQLite 快照包含完整业务数据与 token hash / Client credential 等持久化内容。

## 15.2 导入

当前实现：

1. admin 选择 ZIP。
2. UI 明确覆盖警告。
3. 用户输入 `RESTORE` 二次确认。
4. Panel 校验 ZIP 路径、大小、格式、SHA256、SQLite integrity / foreign key、核心 schema。
5. 备份版本不得高于当前 Panel。
6. **备份 `panel_domain` 必须与当前 `PANEL_DOMAIN` 相同。**
7. 导入只先写入 `/var/lib/vps-panel/panel/restore/` pending state。
8. Panel 重启后在 `database.Open()` 前替换数据库。
9. 当前数据库先保存 rollback copy。
10. 新数据库打开 / migration 失败则回滚。

## 15.3 当前恢复边界

当前实现不是旧文档规划中的“未初始化页面直接恢复”。

现状：

- 需要先进入一个已初始化 Panel
- 只允许 admin 调用导入
- 没有独立的导入预览页面
- deployment 文件会进入 ZIP，但当前恢复核心是 SQLite 数据库
- Panel URL / 域名不同不会自动迁移 Agent

跨 VPS 推荐：

```text
保持原 Panel 域名
↓
新 VPS 安装 Panel
↓
把当前 Panel 域名切为原域名
↓
导入备份
↓
DNS 指向新 VPS
↓
Agent 使用原 panel_url + 原 token 自动重连
```

未来若实现域名迁移，应设计**专用 Panel URL 迁移消息**，不要建立任意 Task Runner。

---

# 16. Agent 生命周期

## 16.1 Enrollment

Server 创建后生成一次性 Enrollment Token。

类型：

```text
initial
rebind
```

当前规则：

- Token 只显示一次。
- 数据库只保存 hash。
- 新 Enrollment 会使旧未使用 Enrollment 失效。
- 已有 Agent 时重新生成 Enrollment 会撤销旧 Agent 凭据并关闭在线连接。
- 有效 Enrollment 可以直接覆盖目标 VPS 上已有 Agent 配置，不需要 `--force`。
- 注册成功后才原子替换本地 config。

## 16.2 Agent 本地配置

```text
/etc/vps-panel-agent/config.json
```

包含：

```text
panel_url
server_id
agent_id
agent_token
```

权限必须保持：

```text
0600
```

## 16.3 Agent 自动升级

正式版本 Panel 可以要求官方 Agent 升级到同版本。

管理员批量升级由前端编排：固定最多同时升级 3 台，重复调用现有单 Agent 升级接口并通过 Server 列表轮询确认结果。批量进度不是新的后端 Task Runner，也不单独持久化。

流程：

- WS `agent_upgrade`
- 下载固定 tag 二进制
- SHA256SUMS 校验
- `version` 校验
- 原子替换
- 服务重启
- 保留旧 binary 直到升级后稳定连接
- 失败回滚

禁止自动降级。

旧 Agent 可使用：

```text
/upgrade-agent.sh
```

bootstrap 保留原 Agent config，不重新注册。

---

# 17. 第三方 Agent：当前状态与边界

当前 Panel 已正式识别并持久化：

```text
implementation
version
api_version
capabilities
```

明确声明 API v1 的 Agent 按 capability 控制 Proxy / Relay 创建与启用、IPv4 / IPv6 出站偏好和 diagnostics UI。真正执行诊断时仍使用当前在线 `Connection.Capabilities`。

Legacy Agent 使用兼容模式：

```text
implementation = ""
api_version = 0
```

其 capability 视为 unknown，不因空 capability list 禁止现有功能。

## 17.1 当前最小 Agent API v1

当前已实现字段：

```text
implementation
version
api_version
capabilities
```

含义：

```text
implementation
→ 这是谁，例如 vps-panel-agent / BoardRay

version
→ 这个实现自己的版本

api_version
→ 它兼容哪一版 VPS Panel Agent 协议

capabilities
→ 它真正支持哪些当前功能
```

capability 与真实 Panel 功能直接对应，例如：

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
```

不要只写过粗的：

```text
xray.vless
xray.tls
```

否则无法区分“TLS ACME 支持但手动 TLS 不支持”等真实差异。

## 17.2 当前实现边界

除非单独开启对应任务，否则不要：

- 预先建 Plugin SDK
- 建 Agent Provider/Factory 层
- 建通用 Driver ABI
- 改现有 Agent API URL
- 静默过滤 Agent 不支持的 desired state

当前已实现 Agent identity、官方自动升级安全边界，以及 Proxy / Relay / 出站偏好 / diagnostics UI 的 capability 控制；不会静默过滤、降级或修改已有 desired state。

---

# 18. 数据库

当前 SQLite 核心表：

```text
users
admin_invitations
sessions
servers
server_access
user_server_order
agent_enrollments
agents
server_system_info
server_metrics
proxies
user_proxy_order
clients
client_metrics
relays
user_relay_order
```

## 18.1 Migration 原则

每次 schema 变更：

- 更新 `schema.go` 作为当前完整 schema
- 在 `migrations.go` 增加兼容旧数据库的 migration
- migration 必须幂等
- 不能要求用户手改 SQLite
- 不能因为新增字段重建整张表，除非 SQLite 约束确实要求且有可靠数据迁移
- 外键和旧数据必须保留

## 18.2 JSON 字段

当前 `config_json` / `credential_json`：

- 是受后端严格控制的内部结构
- decode 时应拒绝未知字段
- 不允许前端提交任意 Xray / Realm JSON
- 不应成为通用 extension bag

## 18.3 SQLite 约束

保持：

- foreign keys
- unique
- CHECK
- transaction

不要因为“Go 已经校验”就移除数据库关键约束。

---

# 19. 前端产品骨架

本节已把原 Frontend Guide 合并进统一文档。

## 19.1 技术栈

固定：

```text
Vue 3
TypeScript
Naive UI
Vite
```

当前额外前端依赖：

```text
qrcode
```

不要为了页面开发新增：

- Vue Router
- Pinia
- 新 UI Framework
- 通用 Table Engine
- Schema Form Engine
- 大型 Design System

除非出现明确、当前的重复问题。

## 19.2 导航

当前 `App.vue` 使用轻量：

```text
currentPage
```

切换：

```text
概览
服务器
代理节点
中转
```

没有 Router。

不要仅为了“URL 更标准”引入 Router。

## 19.3 Desktop 布局

Sidebar 当前固定：

```text
width: 192px
position: fixed
left: 0
top: 0
height: 100vh
```

主内容：

```text
margin-left: 192px
width: calc(100% - 192px)
```

原则：

- Sidebar 稳定，不随页面变化。
- 主内容尽量使用横向空间。
- 不套窄 `max-width` 居中容器。
- 资源列表优先用宽表格。
- 不把每个资源做成大卡片瀑布流。

## 19.4 Mobile

当前：

```text
<= 720px
```

Sidebar 变为滑出菜单，并显示 backdrop / mobile header。

移动端允许表格横向滚动。

不要为了移动端重新设计完全不同的数据语义。

---

# 20. 资源页面 UI 规则

## 20.1 Server

当前正常服务器主列表保持紧凑：

```text
排序
名称 + public/private
状态
禁止中国 IP 入站
本周期流量
到期时间
操作
```

已移除列表：

```text
排序
名称
移除时间
创建时间
操作
```

Server 的详细 IP、系统信息、Agent 版本、资源使用等放在详情 Modal，不要求全部塞进主列表。

当前详情 Modal 包含：

- 基本信息
- access 范围
- 到期日期
- Agent / system info
- CPU / RAM / Disk / Uptime
- 流量设置
- Xray 出站偏好
- Agent 升级
- Enrollment / rebind
- admin 的彻底删除
- 一键诊断入口

不要把详情改成独立详情路由。

## 20.2 Proxy

当前主列表列固定为：

```text
排序
名称
服务器
IP / 地址
端口
协议
传输
安全层
流控
状态
操作
```

VLESS：

```text
协议：VLESS
传输：TCP
安全：TLS / REALITY
流控：XTLS Vision
```

Shadowsocks 不适用字段显示 `--`。

Client 不做独立主导航页，继续属于 Proxy 详情。

Proxy 详情：

- 公共 Proxy 参数
- Client 紧凑列表
- Client 查看 / 编辑 / 启停 / 删除
- 复制链接
- QR

## 20.3 Relay / 中转

当前主列表：

```text
排序
名称
服务器
入口地址
监听端口
目标
客户端
Network
状态
操作
```

目标为 Proxy 时，“目标”只显示：

```text
Proxy 名称 · Port
```

不重复显示目标 IP。

“客户端”列显示：

```text
目标 Client 名称
```

无选择则：

```text
—
```

Relay 详情继续用 Modal。

## 20.4 详情交互

默认：

```text
Server 详情 → Modal
Proxy 详情  → Modal
Client 详情 → Modal
Relay 详情  → Modal
```

禁止：

- 主表行内展开
- 每个资源新开独立详情路由
- 右侧 Drawer 挤压列表

例外：

```text
服务器一键诊断 → Drawer
```

这是临时诊断面板，不是资源详情页。

---

# 21. 前端表格、搜索、排序和 Modal

## 21.1 表格

统一：

- 表头轻量
- 行高稳定
- 文本优先单行
- 长字段使用次级文本 / overflow
- 状态用轻量 Tag
- 操作放最右
- 操作按钮用小尺寸
- 不给每行套 Card

技术字段如 endpoint / latency 可使用等宽字体。

## 21.2 搜索

当前：

- Proxy 有搜索
- Relay 有搜索
- Server 当前没有统一搜索框

不要为了“所有页面看起来一样”强行抽一套通用 Query Builder。

真正需要 Server 搜索时再加。

## 21.3 拖拽排序

当前使用 HTML5 Drag & Drop，并复用已有 reorder API。

原则：

- 排序偏好按账号保存
- 不改业务资源状态
- 不改 desired state
- 不触发 Agent
- 搜索过滤时避免含糊排序

## 21.4 Modal 尺寸

当前实现按业务复杂度区分，而不是强制统一宽度。

大致：

```text
Server detail      ~960px
Proxy form         ~760px
Proxy detail       ~1180px
Relay form         ~640px
Relay detail       ~960px
Client form/detail ~560px
```

允许随内容微调，但不要为了加一个字段改成全屏工作台。

---

# 22. 视觉原则

整体：

```text
浅背景
轻边框
稳定绿色主色
高信息密度
少量状态 Tag
```

可以参考 Komari 一类监控面板的：

- 空间利用
- 信息密度
- 固定 Sidebar
- 宽列表思路

禁止复制：

- Logo
- 品牌
- 原 CSS
- 原配色数值
- 原组件结构
- 原文案 / 按钮顺序
- 像素级页面布局

避免：

- 大渐变
- 玻璃拟态
- 大面积阴影
- 超大圆角
- 大动画
- 每个区块都套 Card

---

# 23. 部署与目录边界

## 23.1 Panel

Panel 默认原生安装，不以 Docker 为主路径。

当前支持：

```text
Debian / Ubuntu
systemd
amd64 / arm64
```

目录：

```text
/opt/vps-panel/panel/
├── vps-panel
├── web/
└── .vps-panel-install

/var/lib/vps-panel/panel/
/etc/vps-panel/panel/environment
/etc/systemd/system/vps-panel.service
```

Panel 以：

```text
User=vps-panel
Group=vps-panel
```

运行。

## 23.2 Agent

支持：

```text
Debian / Ubuntu + systemd
Alpine + OpenRC
amd64 / arm64
```

目录：

```text
/opt/vps-panel/agent/vps-panel-agent
/etc/vps-panel-agent/config.json
/etc/systemd/system/vps-panel-agent.service
/etc/init.d/vps-panel-agent
```

## 23.3 Panel / Agent 同机

当前目录已经分离，允许同一 VPS 同时安装 Panel 和 Agent。

共享根：

```text
/opt/vps-panel
/var/lib/vps-panel
/etc/vps-panel
```

开发原则：

- 共享父目录应允许需要的服务遍历。
- 子目录保持各自严格权限。
- 不对共享根做递归 `chmod -R` / `chown -R`。
- 安装 / 升级 / 卸载只能操作自己拥有的子目录。
- Panel 卸载不能删除 Agent / Xray / Realm / ACME。
- Agent 操作不能覆盖 Panel 子目录。
- 对受管路径继续拒绝 symlink。

---

# 24. Panel 域名与 Caddy

Panel 两种入口：

```text
无域名
→ 0.0.0.0:8080

有域名
→ Panel 127.0.0.1:8080
→ Caddy HTTPS reverse proxy
```

配置：

```text
/etc/vps-panel/panel/environment
/etc/caddy/vps-panel.caddy
```

`vp domain` 负责域名调整。

未来 Panel URL 迁移不能靠备份 ZIP 猜测远端 Agent 新地址；如实现，应设计专用迁移协议。

---

# 25. 安全

## 25.1 Password

用户密码只保存强哈希，当前认证实现继续使用项目现有方案。

不记录明文密码。

## 25.2 Token

以下原始 Token 数据库只保存 hash：

- Session Token
- Admin Invitation Token
- Agent Enrollment Token
- Agent Long-term Token

原始值只在必要时显示一次。

## 25.3 Agent secret

Agent Token：

- 本地 config 0600
- 不放 URL query
- 不写日志
- 不写错误信息
- 管理 UI 不重复展示

## 25.4 Proxy secret

以下不应出现在普通 UI / 日志：

- VLESS UUID
- Shadowsocks password
- REALITY private key
- TLS private key

前端复制最终 URI，而不是复制裸 secret。

## 25.5 备份

ZIP 包必须视为敏感文件：

- admin only
- no-store
- 上传 / 解压大小限制
- zip-slip 防护
- symlink / 非普通文件拒绝
- SHA256
- SQLite integrity / FK 校验
- 临时文件清理
- 原子 / 可回滚恢复

## 25.6 远程控制

禁止增加：

```text
POST /api/agent/exec
```

或任何等价的任意 shell / command runner。

少数无法用 desired state 表达的操作必须是**专用、结构化、可校验**协议。

---

# 26. 第三方项目参考边界

VPS Panel 的实现必须保持独立。

允许研究第三方项目的：

- 功能清单
- 用户流程
- 协议字段含义
- API 职责
- 配置验证顺序
- renderer / validator / apply / rollback 这种高层职责划分

禁止：

- 复制源代码
- 逐函数翻译
- 改变量名后复用
- 机械复制 API path / JSON schema
- 机械复制数据库 schema
- 复制 Xray / Realm 模板
- 复制安装脚本
- 复制 systemd unit
- 复制 UI 组件结构 / 文案 / CSS
- 带入第三方面板特征字符串

协议实现优先参考：

```text
Xray 官方文档
Xray 官方示例
上游源码行为
Realm 官方发布 / 文档
```

第三方面板只能用于理解“有人实现过什么需求”，不能成为代码来源。

---

# 27. API 设计原则

用户 API 与 Agent API 当前都保持简单。

当前用户态主要路由：

```text
/api/auth/*
/api/admin/invitations
/api/admin/backup/*
/api/users
/api/overview
/api/servers
/api/proxies
/api/clients
/api/relays
```

规则：

- Handler 做权限 / DTO / 编排。
- 业务状态修改尽量放现有 `server` / `proxy` / `relay` service。
- 不为一个简单 handler 引入多层 UseCase / Repository。
- API 需要区分的错误才定义 typed error。
- 删除 / 禁用通常必须保持可用，即使当前 Agent 不在线或能力不足。

---

# 28. 测试与验证

## 28.1 Go

涉及 Go 代码时至少运行与任务相关的测试。

完整验证推荐：

```bash
cd panel
gofmt -w .
go test ./...
go test -count=1 ./...
go vet ./...
go build ./cmd/panel
go build ./cmd/agent
```

如果 `gofmt -w .` 不符合本地使用方式，可对实际修改的 `.go` 文件执行 gofmt，但不得跳过格式化。

## 28.2 Frontend

当前 `web/package.json` 只有：

```text
test
build
```

其中 `build` 已包含：

```text
vue-tsc --noEmit
+
vite build
```

推荐：

```bash
cd web
npm test
npm run build
```

不要在提示词里要求不存在的：

```text
npm run typecheck
```

除非 package.json 后续真的增加该 script。

## 28.3 Shell

修改安装 / 升级脚本时：

```bash
bash -n <script>
```

并执行现有与布局 / 安装相关的测试脚本。

## 28.4 测试重点

优先测试：

- auth / 权限 / IDOR
- Token 生命周期
- migration
- Agent 注册 / rebind
- WS 连接替换 / 断线
- desired state
- config rollback
- Xray / Realm renderer
- traffic baseline / reset
- share URI
- backup safety
- diagnostics bounds

不为了覆盖率给简单 getter 写大量无价值测试。

---

# 29. Codex 任务规则

每次任务：

1. 先读根目录 `AGENTS.md`。
2. 阅读与当前任务直接相关的现有代码。
3. 阅读本文对应章节。
4. 明确最少修改哪些文件。
5. 搜索是否已有 helper / model / validation 可复用。
6. 完成当前任务。
7. 测试。
8. 停止。

禁止：

- 顺手做下一阶段
- 顺手重构无关模块
- 顺手换前端框架
- 顺手换数据库
- 为未来建大量空目录 / interface
- 为“统一”改所有 API
- 为“扩展性”建 Plugin Manager

完成报告只需要：

```text
### 完成
### 修改文件
### Migration（如有）
### API / 行为变化（如有）
### 验证
### 明确未实现
```

---

# 30. 当前实现状态表

| 能力 | 状态 | 当前说明 |
| --- | --- | --- |
| admin / vip | 已实现 | 唯一初始 admin + 邀请 vip |
| Server public/private | 已实现 | admin 不绕过 private |
| Server archive / rebind | 已实现 | Enrollment 可覆盖已有 Agent 配置 |
| Agent WS / heartbeat | 已实现 | 10s 心跳，自动重连 |
| system_info | 已实现 | 静态信息 + public IPv4 |
| metrics | 已实现 | CPU/RAM/Disk/Uptime/NIC |
| Server 月流量 | 已实现 | single / bidirectional / reset / adjustment |
| desired state | 已实现 | REST + `config_changed` |
| Xray | 已实现 | v26.3.27 |
| VLESS TLS | 已实现 | ACME + manual |
| VLESS REALITY | 已实现 | XTLS Vision |
| Shadowsocks 2022 | 已实现 | 128/256 method，多 Client |
| Client traffic/quota/expiry | 已实现 | 周期、预警、耗尽、恢复 |
| Realm / Relay | 已实现 | v2.9.4，TCP/UDP |
| Relay target Client | 已实现 | 分享元数据，不是 L4 独占认证 |
| QR | 已实现 | 浏览器本地生成 |
| Subscription | 未实现 | 无订阅 URL / Clash / sing-box 批量输出 |
| outbound preference | 已实现 | auto / IPv4 / IPv6 |
| 禁止中国 IP 入站 | 已实现 | APNIC CN prefix + nftables set，仅受管 Proxy / Relay listener |
| ZIP backup / restore | 已实现 | admin-only，同域名校验 |
| 一键诊断 | 已实现 | `diagnostics_v1` |
| Panel URL 自动迁移 | 未实现 | 仍需同域名迁移或人工改 Agent |
| Agent API v1 identity | 已实现 | implementation / version / api_version / capabilities |
| 第三方 Agent capability enforcement | 已实现 | Proxy / Relay / 出站偏好 / diagnostics UI；Legacy 兼容 |
| 历史指标 | 未实现 | 不阻塞主链路 |
| 通知系统 | 未实现 | 不阻塞主链路 |
| 分组 / 标签 | 暂缓 | 不阻塞主链路 |

---

# 31. 建议的下一步开发顺序

以下记录当前完成状态和后续规划。

## 31.1 Agent API v1 身份元数据（已实现）

目标：

- 区分官方 Agent 与第三方 Agent
- Agent 上报真实自身版本
- 独立声明 `api_version`
- capability 持久化
- 防止官方 updater 覆盖第三方 Agent

当前已完成 identity metadata、capability 持久化及官方 updater 安全边界。

## 31.2 capability-aware 操作限制（已实现）

当前已完成：

- 创建 / 启用 Proxy 前校验能力
- 创建 / 启用 Relay 前校验 `relay.realm`
- outbound preference 按能力提示
- 删除 / 禁用始终允许
- legacy Agent 保持兼容

## 31.3 Subscription

复用已有：

```text
Proxy
Client
share URI
Relay-derived URI
QR
```

增加真正需要的：

- subscription URL
- 选择哪些 Client / Relay endpoint 进入订阅
- Clash / Mihomo 输出
- sing-box 输出

不要创建新的 Credential 模型。

## 31.4 Panel URL 迁移

设计专用：

```text
panel_url_changed / update_panel_url
```

Agent：

- 校验 URL
- 原子更新 `/etc/vps-panel-agent/config.json`
- reconnect
- 保留失败回滚

不要使用 shell。

## 31.5 后续可选

真正有需要时再做：

- Server 分组 / 标签
- Metrics 历史图
- 事件通知 / Webhook / Telegram / Email
- Proxy / Relay 批量操作
- 配置历史 / rollback UI
- 拓扑图
- 外部只读节点

---

# 32. 新任务模板

后续给 Codex 的任务可以直接使用：

```text
请修改 renaissance0721/vps-panel。

开始前：
1. 阅读根目录 AGENTS.md。
2. 阅读 DEV.md 中与本任务相关章节。
3. 阅读当前 main 中直接相关代码。
4. 使用完成需求所需的最小 diff。

当前行为：
- ...

本任务目标：
- ...

后端：
- ...

数据库：
- ...

Agent：
- ...

前端：
- 保持现有 Sidebar / 主内容 / 列表 / Modal 产品骨架。
- 不引入 Vue Router / Pinia / 新 UI Framework。
- ...

安全：
- ...

明确不做：
- ...

测试：
- 相关 Go tests
- go vet / build（如相关）
- npm test / npm run build（如改前端）
- bash -n（如改脚本）

完成后只报告：
完成 / 修改文件 / migration / API 行为 / 验证 / 未实现。

不要 tag。
不要 Release。
不要 version bump，除非本任务明确要求。
```

---

# 33. 最终架构图

```text
Browser
   │
   ▼
Panel
├── Auth
│   ├── admin
│   └── vip
├── Server
│   ├── access
│   ├── traffic
│   ├── ordering
│   └── diagnostics
├── Agent Control
│   ├── enrollment
│   ├── registration
│   ├── WebSocket
│   ├── desired state
│   └── upgrade
├── Proxy
│   ├── VLESS TLS / REALITY
│   ├── Shadowsocks 2022
│   └── Client
├── Relay
├── Share / QR
└── Backup / Restore
       │
       ├── REST: config / result / traffic
       └── WebSocket: heartbeat / metrics / notifications / diagnostics
                    │
                    ▼
                Server Agent
                ├── System Info
                ├── Metrics
                ├── Xray
                ├── ACME
                ├── Realm
                ├── China Inbound Firewall
                └── Diagnostics
                    │
                    ▼
                  Linux
```

业务关系：

```text
Server
├── Agent
├── Proxy
│   └── Client
└── Relay
    ├── target = Proxy | host:port
    └── target_client_id = 分享元数据（可空）
```

---

# 34. 不可随意改变的固定决策

- [x] 一台 Server 一个统一 Agent。
- [x] Panel 不通过 SSH / 任意 Shell 管理 VPS。
- [x] 配置以 desired state 同步。
- [x] WebSocket 负责实时状态与轻量通知，REST 负责完整配置与结果。
- [x] SQLite 单体。
- [x] admin / vip 两级。
- [x] admin 不绕过 private Server。
- [x] Server / Proxy / Client / Relay 是当前核心模型。
- [x] VLESS 固定 TCP + XTLS Vision，TLS / REALITY 二选一。
- [x] `client_udp443` 是 Client 分享选项，不是服务端 flow。
- [x] Shadowsocks 使用同一个 Xray / Proxy / Client 模型。
- [x] Realm 是 Relay 的本地实现，不额外创建 Realm 业务资源表。
- [x] Relay 选择 Client 只影响分享，不提供独占认证。
- [x] Proxy / Relay 分享 URI 由后端 canonical generator 生成。
- [x] 二维码在浏览器本地生成。
- [x] 备份是完整 SQLite 快照，属于高敏感文件。
- [x] Panel / Agent / Xray / Realm / ACME 使用分离的受管目录。
- [x] 前端资源详情默认使用 Modal；诊断 Drawer 是明确例外。
- [x] 当前不引入 Router / Pinia / 插件系统 / 通用 Task Runner。
- [x] 第三方项目只能参考行为和高层思路，不能复制实现。

---

# 35. 更新本文的规则

每次发生以下变化，应同步修改 `DEV.md`：

- 核心数据模型变化
- 新 Agent API 行为
- Xray / Realm 受管目录变化
- 认证 / 权限语义变化
- 备份格式变化
- 支持协议变化
- 前端主导航 / 页面骨架变化
- 新增真正长期固定的安全规则

普通 bugfix、文案、局部样式调整无需记录为“架构变化”。

不要继续维护两份互相引用、容易漂移的“总 Guide + Frontend Guide”。以后只维护：

```text
AGENTS.md
→ 开发行为约束

DEV.md
→ 产品 / 架构 / UI / 协议统一开发指南

README.md
→ 面向用户的安装、使用、当前功能说明
```
