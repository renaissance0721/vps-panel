# VPS Panel 开发指导与阶段路线图

> 本文是 VPS Panel 的**总开发指导**。
>
> 前端布局、列表信息密度、Modal 交互、Sidebar、Server / Proxy / Realm 页面骨架等 UI 细节，统一参考配套文档：
>
> ```text
> vps-panel-frontend-guide.md
> ```
>
> 两份文档职责固定为：
>
> ```text
> 本 Guide
> → 业务模型、Agent、API、数据库、Phase、协议与安全边界
>
> Frontend Guide
> → 页面布局、列表列信息、容器稳定性、Modal、响应式与前端代码组织
> ```
>
> 如果某个 Phase 涉及前端：
>
> 1. 先以本文确定功能与数据语义。
> 2. 再以 `vps-panel-frontend-guide.md` 确定页面怎么摆、列表显示什么、详情如何打开。
> 3. 前端不得为了方便而改变本文的业务模型。
> 4. 本文也不要重复维护 Frontend Guide 已明确的视觉细节。
>
> 两份文档冲突时：
>
> - **业务 / 协议 / 数据 / Agent / API / 安全语义：以本文为准。**
> - **前端布局 / 容器 / Modal / 列表视觉：以 Frontend Guide 为准。**


> 项目：`renaissance0721/vps-panel`  
> 文档定位：长期开发指导文档，作为后续 Codex / 人工开发时的阶段边界、架构约束和验收依据。  
> 当前基线：Phase 1–4、Phase 4.5、Phase 4.6、Phase 5A–5B、Phase 6A–6B、Phase 7A、Phase 8A 和 Phase 8B 已完成；Phase 7B 暂缓，不阻塞代理主链路。
> 语言：简体中文。  
> 原则：每个 Phase 只实现当前验收条件真正需要的功能，不提前堆未来架构。

---

# 0. 项目最终目标

VPS Panel 的目标不是单纯做一个“探针面板”，而是做一个统一的多 VPS 管理面板。

最终希望实现：

- 多 VPS 统一管理
- 每台 VPS 安装一个统一 Agent
- 服务器状态与系统监控
- 机器网卡累计 RX / TX 与月流量统计
- 可设置服务器到期日期
- 可设置月流量额度、单向/双向计费方式和每月流量重置时间
- 服务器信息中显示本周期已用流量 / 总流量
- 提供统一的 Panel ↔ Agent 节点后端 API，Panel 不直接依赖 Xray / Realm 配置文件格式
- 第一版由统一 Agent 管理 Xray，并支持 VLESS + TCP + XTLS Vision；安全层允许 TLS / REALITY 二选一，两种模式都支持客户端 `xtls-rprx-vision-udp443`；同时支持 Shadowsocks
- Proxy 下提供 Client 管理，并在独立阶段实现每 Client 流量、流量额度、重置周期与到期控制
- 同一个 Agent 管理 Realm 和端口转发规则
- 节点分享、订阅、二维码
- `admin / vip` 两级账号体系
- Server / Proxy / Relay 等业务资源在账号之间全局共享
- ZIP 一键导出、导入、跨 VPS 恢复
- 后续配置、更新、备份、日志等维护能力

当前不把 `CoreInstance`、`AccessEndpoint`、`Chain` 作为第一版固定业务模型。
只有在未来出现明确且无法用现有 Server / Proxy / Relay 表达的真实需求时，再单独增加对应抽象。

但开发顺序必须从基础设施向上逐层推进，不能一次把所有模型和抽象全部写出来。

---

# 1. 固定架构原则

## 1.1 一台 Server 只安装一个统一 Agent

这是整个项目的固定原则。

关系如下：

```text
Panel
  │
  ├── Server A
  │     └── vps-panel-agent
  │           ├── 系统监控
  │           ├── 网络监控
  │           ├── 配置同步
  │           ├── Xray
  │           └── Realm
  │
  └── Server B
        └── vps-panel-agent
```

禁止设计成：

```text
服务器 Agent
节点 Agent
Realm Agent
Xray Agent
```

这些都不应该是独立 Agent。

后续所有功能都建立在“每台 Server 一个 Agent”之上。

---

## 1.2 Agent 属于 Server

核心关系：

```text
Server
└── Agent
```

Agent 是这台 VPS 的统一执行层。

以后：

- 采集 CPU / RAM / Disk
- 采集网络流量
- 同步 Panel 的目标配置（desired state）
- 安装和管理 Xray
- 创建和管理 VLESS / Shadowsocks
- 安装和管理 Realm
- 创建 Realm 转发
- 启停受 Panel 管理的服务
- 查询版本与同步结果

都由这一个 Agent 完成。

第一版只把 Xray 和 Realm 做实。
sing-box / Mihomo 等其他后端不提前建立框架；等未来确实需要第二个真实后端时，再基于同一套 Agent API 增加实现。

---

## 1.3 原创实现与第三方代理面板参考边界（硬性要求）

VPS Panel 的代理功能必须保持**原创实现**。

允许研究或参考 3x-ui、x-ui、Xboard、Marzban、V2Board 生态、其他机场面板 / 节点后端以及 Xray / sing-box 官方项目的：

- 功能清单
- 用户操作流程
- 协议能力
- 配置项含义
- API 职责划分
- 可验证的工程经验
- 某些值得借鉴的高层代码组织思想，例如“renderer / validator / apply / rollback”这种职责拆分

但严禁照抄或近似复刻第三方面板的：

- 源代码或大段代码片段
- 函数 / 类型 / 文件组织的逐项对应实现
- API 路径、请求结构和响应结构的机械复制
- 数据库 schema 的机械复制
- Xray / Realm 配置模板的整段复制
- 前端页面布局、组件结构、文案、按钮顺序和交互细节的像素级复刻
- 注释、错误信息、日志文案
- 安装脚本、systemd unit、目录结构、文件命名
- 第三方面板特有的变量名、tag、remark、header、User-Agent 或其他可识别字符串
- 任何明显能够看出“代码从某个代理面板搬过来”的实现痕迹

如果参考第三方实现，只允许：

```text
理解需求 / 理解协议 / 理解一种可行的职责划分
↓
回到 VPS Panel 自己的数据模型和 Agent API
↓
重新独立设计
↓
独立编写代码
```

不能：

```text
找到 3x-ui / x-ui / 其他面板的实现
↓
改变量名
↓
改目录
↓
直接提交
```

### 外部可见特征

VPS Panel 生成的代理服务、分享链接、订阅配置和协议流量中，不应主动加入任何面板品牌特征。

尤其不得出现第三方面板特征，例如：

```text
3x-ui
x-ui
marzban
xboard
v2board
```

也不要为了标识“由 VPS Panel 管理”而在协议必要字段中主动加入：

```text
vps-panel
panel-managed
```

等外部可识别字符串。

外部输出应尽量只包含：

- 协议真正需要的字段
- 用户自己设置的节点名称 / remark
- 必要的 Server 地址、端口和凭据

本机内部文件路径、systemd unit、日志组件可以使用 VPS Panel 自己的项目命名，因为这些只用于本机运维；但不得复用其他代理面板的命名习惯或目录。

### 官方协议文档优先

实现 Xray / VLESS / Reality / XTLS / Shadowsocks 等协议能力时：

1. 优先依据 Xray 官方文档、官方示例和上游源码行为。
2. 第三方面板只能作为“功能是否有人这样做”的参考。
3. 如果第三方面板实现与上游官方行为冲突，以官方协议 / 上游实现为准。
4. 不为了兼容某个面板而引入其私有格式或历史包袱。

这是一条长期硬性要求，不因开发阶段改变。

---

## 1.4 Panel 与 Agent 使用“目标状态同步”，不是任意 Shell / 通用任务系统

Panel 不应该依赖 SSH 自动登录所有 VPS，也不应该把 Agent 做成远程 Shell。

代理配置的正常控制链路固定为：

```text
浏览器
  ↓
Panel 保存业务数据
  ↓
生成该 Server 的 desired state
  ↓
Agent 通过 REST API 拉取完整目标状态
  ↓
Agent 在本机生成受 Panel 管理的配置
  ↓
校验 → 原子替换 → 重启/重载 → 健康检查
  ↓
Agent 回报同步结果
```

现有 WebSocket 继续用于：

- online / offline
- heartbeat
- system_info
- metrics
- `config_changed` 轻量通知

WebSocket 不承载整份 Xray / Realm 配置，也不建立复杂 RPC / Task 协议。

当 Panel 中某台 Server 的 Proxy / Relay 配置发生变化时：

```text
Panel 保存数据库
↓
desired_state_version + 1
↓
如果 Agent 在线：
  WebSocket 发送 config_changed(version)
↓
Agent 立即 GET /api/agent/config
↓
应用成功或失败
↓
POST /api/agent/config/result
```

如果 WebSocket 暂时断开，Agent 使用低频 REST 轮询兜底，例如每 30 秒检查一次版本。

第一版建议的节点后端 API 保持很小：

```text
GET  /api/agent/config
POST /api/agent/config/result
POST /api/agent/traffic        # 仅在真正开始 Proxy / Client 流量统计时实现
WS   /api/agent/ws             # 复用现有连接
```

`GET /api/agent/config` 返回该 Agent 所属 Server 的完整目标状态，例如：

```json
{
  "version": 17,
  "xray": {
    "enabled": true,
    "proxies": []
  },
  "realm": {
    "enabled": false,
    "relays": []
  }
}
```

这里的“通用”只表示：

> Panel 与 Agent 的 API 不依赖 `/etc/xray/config.json` 或 Realm TOML 的具体文件结构。

不要把它扩张成：

- Plugin SDK
- Generic Driver ABI
- Universal Resource Graph
- 任意 JSON / 任意 Shell 执行器
- 通用任务编排系统

严禁实现：

```text
POST /api/agent/exec
{
  "command": "任意 shell"
}
```

Agent 可以在内部执行必要且写死的系统命令，但 Panel 不向 Agent 发送任意命令字符串。

极少数不能通过 desired state 表达的一次性安全操作（例如未来迁移 Panel URL）可以单独设计专用操作，不因此建立通用 Task Runner。

---

## 1.5 账号固定为 admin / vip 两级

账号模型保持简单，不做复杂 RBAC。

固定规则：

```text
首次初始化创建的第一个账号
→ role = admin
→ 整个 Panel 只有这个账号具有邀请注册权限

admin 生成邀请链接
→ 被邀请者注册
→ role = vip
```

要求：

- `admin` 是首次初始化创建的第一个账号。
- 当前不提供创建第二个 admin 的入口。
- 通过邀请注册的账号统一为 `vip`。
- `vip` 不能创建、查看、撤销新的注册邀请。
- `vip` 访问邀请管理 API 时必须返回无权限，而不是仅在前端隐藏按钮。
- 当前不实现 Owner / SuperAdmin / Operator / Viewer 等更多角色。
- 当前不实现角色自定义、权限矩阵或复杂 RBAC。
- 除非未来明确提出，否则不提供 `vip → admin` 提权功能。
- 为避免失去唯一管理入口，当前不要提供删除唯一 `admin` 的能力。

数据库建议只增加最小字段：

```text
users.role
```

内部值：

```text
admin
vip
```

首次初始化强制写入 `admin`，邀请注册强制写入 `vip`。

---

## 1.6 Server、节点和后续业务资源属于 Panel 全局资源池

当前项目不是多租户 SaaS。

不要给每个账号复制一份 Server / Proxy，也不要用“同步任务”在账号之间复制资源。

正确关系：

```text
Panel
├── admin
├── vip A
├── vip B
└── Shared Resources
    ├── Server
    ├── Proxy
    ├── Client
    ├── Relay
    └── Subscription
```

也就是说：

> Server / Proxy / Relay 等资源属于整个 Panel，而不是某一个 User。

当 admin 添加：

```text
日本服务器
美国落地 Proxy
香港 Realm Relay
```

其他 `vip` 账号读取的也是同一份数据库记录，因此天然同步，不需要创建“同步到 VIP”功能。

固定原则：

- 不给 Server 增加 `owner_user_id`。
- 不给 Proxy 增加账号所有权字段。
- 不为每个 VIP 复制节点。
- 不实现资源副本同步。
- admin 和 vip 至少都可以查看、使用共享业务资源。
- 服务器、节点等业务功能的细粒度修改/删除权限，等真正有需求时再决定；不要现在做复杂权限矩阵。
- 注册邀请、账号级管理以及完整备份/恢复属于 `admin` 专属管理能力。

---

## 1.8 前端实现统一参考 Frontend Guide

只要当前 Phase 涉及：

- Server 页面
- Proxy / 节点页面
- Realm 页面
- 分享 / 订阅页面
- 搜索 / 筛选
- 新增 / 编辑
- 查看详情

就必须同时阅读：

```text
vps-panel-frontend-guide.md
```

硬性边界：

- Sidebar、主内容区、列表容器、Modal 交互不得由每个 Phase 自己重新设计。
- Server / Proxy / Realm 页面必须保持同一套产品骨架。
- “查看详情”统一使用 Modal。
- 新增 / 编辑优先使用 Modal。
- 主列表保持宽、长、稳定，不因详情或编辑而改变页面骨架。
- Proxy / Realm 列表必须按 Frontend Guide 展示核心技术字段。
- 不为了实现某个后端功能顺手重做前端导航、页面布局或视觉体系。

本文只定义“哪些字段存在、字段的业务语义和何时出现”。

Frontend Guide 定义“这些字段如何在 UI 中排列和展示”。

---

## 1.9 Panel 必须支持完整 ZIP 备份、导入和跨 VPS 恢复

长期必须提供：

```text
导出
→ 生成一个 ZIP
→ 在另一台 VPS 安装新的 VPS Panel
→ 选择“导入备份”
→ 上传 ZIP
→ 自动验证并恢复
→ 不要求用户手工修改 ZIP 内文件、ID、关联关系或配置映射
```

这不是简单导出节点链接，而是整个 Panel 的可迁移备份。

恢复后应尽可能还原：

- admin / vip 账号
- Session 之外的持久化认证数据
- Server
- Agent 注册关系和 Agent Token hash
- Proxy
- Client
- Relay
- Subscription 相关配置
- Panel 持久化设置
- 未来真正需要持久化的密钥材料

不应要求用户：

```text
重新创建 Server
重新绑定 Proxy
重新映射 Relay
手工修改数据库 ID
手工调整外键
手工修改 ZIP 内 JSON
```

备份格式需要版本号，以便未来数据库结构升级后仍可做兼容迁移。

---

# 2. 核心概念边界

这些概念后续必须保持清晰，不要混在一起。

## 2.1 Server

Server 代表一台 VPS / 服务器。

例如：

```text
日本优化入口
香港落地
美国住宅
德国 9929
```

Server 是机器，不是节点。

Server 列表、详情 Modal、到期日期、流量信息、分组与标签的前端呈现统一参考：

```text
vps-panel-frontend-guide.md
```

特别注意：

- Server 主列表使用宽表格 / 长列表。
- 查看详情使用 Modal。
- 不因为新增 Server 字段改成卡片瀑布流或单独详情路由。

Server 还需要保存由用户配置的服务器运营信息，包括：

- 到期日期
- 月流量总额
- 流量统计方式
- 每月流量重置时间

建议语义：

```text
expires_at
monthly_traffic_limit_bytes
traffic_count_mode
traffic_reset_day
traffic_reset_time
```

其中：

```text
traffic_count_mode = single
```

表示单向计费，初版按服务器出口流量 `TX` 计入月流量。

```text
traffic_count_mode = bidirectional
```

表示双向计费：

```text
已用流量 = RX + TX
```

这些属于 Server 的用户配置，不属于 Agent 自动探测信息。

服务器到期日期允许为空，表示“不设置到期日期”。日期输入格式为 `YYYY-MM-DD`，按 `Asia/Shanghai` 当天结束后到期。

月流量允许为空或为 0 表示“不限制”，但仍可继续统计实际已用流量。

流量重置时间按“每月第 N 日 HH:mm”配置，具体存储结构在实现时保持最小即可。

### 时间与时区固定规则

Panel 的默认业务时区固定为：

```text
Asia/Shanghai
UTC+8
```

当前阶段不做每台 Server 独立时区，也不做时区切换 UI。

以下时间都按上海时区理解和显示：

- Server 到期日期
- 月流量重置时间
- 当前流量周期起止时间
- 用户在前端输入的日期 / 时间

数据库内部时间戳仍建议统一保存为 UTC / Unix Timestamp，在 Panel 计算周期和展示时转换为 `Asia/Shanghai`，避免系统时区变化影响业务逻辑。

例如用户设置：

```text
每月 15 日 08:00
```

固定表示：

```text
Asia/Shanghai 每月 15 日 08:00
```

不跟随 Agent 所在 VPS 的系统时区。

如果重置日设置为 29 / 30 / 31，而某个月没有该日期，则该月按“当月最后一天的同一时间”执行重置。

例如：

```text
每月 31 日 00:00
```

在 4 月按：

```text
4 月 30 日 00:00
```

执行。

---

## 2.2 Agent

Agent 是安装在 Server 上的统一守护进程。

一台 Server 对应一个 Agent。

---

## 2.3 Proxy

Proxy 表示真正的代理入站 / 节点配置，例如：

```text
VLESS Reality :443
Shadowsocks :8388
```

第一版固定由 Xray 承载 VLESS / Shadowsocks。

VLESS 第一版固定：

```text
protocol = VLESS
transport = TCP
flow = XTLS Vision
```

安全层由用户二选一：

```text
TLS
REALITY
```

因此第一版明确支持两套 VLESS：

```text
VLESS + TCP + TLS + XTLS Vision
VLESS + TCP + REALITY + XTLS Vision
```

不是让 TLS / REALITY 与 XTLS Vision 二选一。

XTLS Vision 在第一版固定开启。

服务端 Xray inbound 的 Client flow 始终使用：

```text
xtls-rprx-vision
```

客户端 / 分享配置允许：

```text
xtls-rprx-vision
xtls-rprx-vision-udp443
```

其中 `xtls-rprx-vision-udp443` 只表示客户端允许 UDP/443 / QUIC 正常通过代理。

它：

- 不是新的服务端协议
- 不要求服务端额外监听 UDP/443
- 不改变服务端 flow
- TLS / REALITY 两种安全模式都可以使用这个客户端选项

服务端始终是：

```text
TCP listener
+
VLESS
+
security = tls | reality
+
flow = xtls-rprx-vision
```

不要把 `xtls-rprx-vision-udp443` 写入服务端 inbound 的 client flow。

Proxy 直接属于 Server：

```text
Server
└── Proxy
```

当前不增加 `CoreInstance` 表。

Xray 在第一版属于 Agent 的本地实现细节，而不是必须暴露给业务层的独立资源。

Proxy 建议只保存少量通用字段：

```text
id
server_id
name
protocol
listen_port
public_host
enabled
config_json
created_at
updated_at
```

其中：

```text
protocol = vless | shadowsocks
```

协议差异字段先放入受后端严格校验的 `config_json`。

例如 VLESS + TCP + TLS + XTLS Vision：

```json
{
  "transport": "tcp",
  "security": "tls",
  "server_flow": "xtls-rprx-vision",
  "client_udp443": false,
  "server_name": "example.com",
  "certificate_ref": "..."
}
```

例如 VLESS + TCP + REALITY + XTLS Vision：

```json
{
  "transport": "tcp",
  "security": "reality",
  "server_flow": "xtls-rprx-vision",
  "client_udp443": false,
  "server_name": "www.microsoft.com",
  "dest": "www.microsoft.com:443",
  "private_key": "...",
  "public_key": "...",
  "short_id": "..."
}
```

这里建议业务字段固定使用：

```text
transport = tcp
security = tls | reality
server_flow = xtls-rprx-vision
client_udp443 = true | false
```

前端只让用户选择：

```text
安全：
○ TLS
○ REALITY

允许 UDP/443 / QUIC：
○ 关闭
○ 开启
```

不要让前端随意填写 `flow` 字符串。

在生成客户端配置时：

```text
client_udp443 = false
→ flow = xtls-rprx-vision

client_udp443 = true
→ flow = xtls-rprx-vision-udp443
```

这样可以避免把客户端专用 `-udp443` 错写进服务端配置。

TLS 与 REALITY 的协议字段必须分支校验：

```text
security = tls
→ 只要求 TLS 分支需要的 SNI / certificate / private key 等字段

security = reality
→ 只要求 REALITY 分支需要的 SNI / dest / key pair / short_id 等字段
```

不要把两套安全层的配置字段混成一份必填表单。

Shadowsocks：

```json
{
  "method": "2022-blake3-aes-128-gcm",
  "password": "..."
}
```

`config_json` 不是让前端随意提交任意 Xray JSON。

Panel 后端仍应按 `protocol`：

- 校验允许字段
- 校验端口
- 校验 UUID / 密钥 / method 等必要值
- 生成规范 desired state

以后如果真的需要 sing-box / Mihomo，再根据真实需求决定是否增加 `backend` 字段或新的数据模型；当前不提前创建 `CoreInstance`。

---

## 2.4 Client

Client 是 Proxy 下的独立用户、设备或凭据。

例如：

```text
VLESS Reality
├── PC
├── iPhone
└── Android
```

固定关系：

```text
Proxy
└── Client
```

Client 不是新的 Proxy，也不是新的 Agent。

同一个 Proxy 可以拥有多个 Client；这些 Client：

- 共享 Proxy 的监听地址、端口、传输层、安全层和服务端参数
- 各自拥有独立凭据
- 各自拥有独立启用状态
- 后续各自统计 Xray per-client 上行 / 下行流量
- 可以设置独立流量额度
- 可以设置独立流量重置周期
- 可以设置独立到期时间

Phase 9A / 9B 如果暂时仍以“一个 Proxy 一份凭据”完成代理主链路，可以先保留当前单凭据实现。

进入 Phase 10A 时，再正式建立 `clients`，并把已有 Proxy 的单凭据安全迁移成默认 Client，必须保留原 UUID / Password 和现有分享行为，不要求用户重新生成节点。

进入 Phase 10B 后：

```text
Server 总流量
→ Linux 网卡累计 RX / TX

Client 流量
→ Xray per-client stats
```

两者是独立统计口径。

不要把 Client 流量反推成 Server 网卡总流量，也不要让 Server 月流量依赖 Xray。

---


## 2.5 Relay

Relay 表示 Realm 等端口转发规则。

例如：

```text
JP Server :9502
    ↓
US Home Proxy :443
```

Relay 直接属于执行 Realm 的源 Server：

```text
Server
└── Relay
```

Relay 最少需要：

- source_server_id
- listen_address
- listen_port
- target_type
- target_proxy_id（可空）
- target_host（可空）
- target_port
- enabled

目标允许：

### 目标为已有 Proxy

Panel 解析目标 Proxy 的 Server / 地址 / 监听端口。

### 手工 host:port

用于转发到 Panel 未管理的服务。

不要把 Relay 目标限制成 `target_server_id`。

---

## 2.6 接入地址与多跳暂不建立独立模型

当前不建立 `AccessEndpoint` 表。

当用户通过 Realm 访问某个 Proxy 时，客户端节点可以在生成分享 / 订阅时直接组合：

```text
Proxy 的协议与凭据
+
Relay 的监听 host / port
=
一个可连接的客户端节点
```

例如：

```text
Proxy:
US VLESS :443

Relay:
JP :9502 → US VLESS :443

分享时可以生成：
direct = us.example.com:443
relay  = jp.example.com:9502
```

两者仍然引用同一个 Proxy / Client 数据，不复制 Proxy。

当前也不建立 `Chain` 表。

多跳只有在单级 Realm 已稳定、且确实出现“必须保存和复用多跳路径”的真实需求时再设计。

---

## 2.7 Agent 管理配置文件的所有权

为了避免 Agent 与用户手工配置互相覆盖，第一版明确：

> Agent 只管理 VPS Panel 自己拥有的配置文件和 systemd 服务。

建议：

```text
/etc/vps-panel/xray/config.json
/etc/vps-panel/realm/config.toml
```

对应服务也使用 Panel 明确管理的 unit，例如：

```text
vps-panel-xray.service
vps-panel-realm.service
```

不要默认去解析、合并或覆盖用户已有的：

```text
/etc/xray/config.json
/usr/local/etc/xray/config.json
其他面板生成的配置
```

如果机器上已经存在未受 VPS Panel 管理的 Xray / Realm，第一版优先：

- 检测冲突
- 明确报错
- 不自动接管
- 不尝试“智能合并”

这样可以显著减少配置损坏和不可预测行为。

---

## 2.8 配置应用必须可验证、可回滚

Agent 每次同步 Xray / Realm 配置都使用统一的安全流程：

```text
收到新的 desired state
↓
生成完整 candidate config
↓
本地语义校验
↓
调用对应程序的配置校验能力（如果有）
↓
保存 previous
↓
原子替换 current
↓
restart / reload
↓
健康检查
├── 成功 → 回报 config sync success
└── 失败 → 恢复 previous → 再启动旧配置 → 回报 failed
```

禁止：

- 在正式配置文件上边读边改
- 使用 `sed` / 字符串拼接修改未知 JSON
- 配置未校验就覆盖
- 重启失败后不回滚
- Panel 在 Agent 真正应用前提前标记成功

同一 Server 上针对同一受管服务的配置应用必须串行执行。


# 3. 当前已完成基线

# 3. 当前已完成基线

## Phase 1：Panel 基础

已完成：

- Go 后端
- SQLite
- Vue 3 + TypeScript + Vite 前端
- 健康检查
- 基础页面
- Release 基础
- Caddy 支持

---

## Phase 2：邀请注册认证基础

当前已完成：

- 首次初始化
- 第一个账号创建
- 初始化完成后关闭初始化入口
- 登录 / 退出
- Session
- HttpOnly Cookie
- HTTPS 下 Secure Cookie
- 一次性邀请
- 邀请 24 小时过期
- 邀请 Token hash 入库
- bcrypt 密码哈希
- 无公开注册

但原先“所有邀请账号与首个管理员权限一致”的设计已经废弃。

新的固定要求已在 Phase 4.5 调整为：

```text
第一个初始化账号 = admin
邀请注册账号 = vip
```

只有唯一 `admin` 可以：

- 创建邀请
- 查看邀请
- 撤销邀请
- 执行完整 Panel 备份 / 恢复
- 执行其他明确的 Panel 级管理操作

`vip`：

- 不能继续邀请其他账号
- 与 admin 共享同一份 Server / Proxy / Relay 等业务资源
- 不是独立租户
- 不拥有资源私有副本

当前仍然不做复杂 RBAC。

---

## Phase 3：Server 管理 + Agent Enrollment

已完成：

- Server CRUD
- Server 状态：
  - pending
  - online
  - offline
- 创建 Server
- 创建一次性 Enrollment Token
- Token 24 小时有效
- Token hash 入库
- 原始 Token 只显示一次
- Agent 安装命令生成
- 删除 Server 清理关联数据

状态语义：

```text
pending = 尚未注册 Agent
offline = 已注册 Agent，但没有有效连接
online  = Agent 当前有效在线
```

---

## Phase 4：Agent 注册与原生安装

已完成：

- Agent Go 二进制
- linux-amd64
- linux-arm64
- GitHub Release Agent artifact
- `/install-agent.sh`
- 自动架构检测
- Agent 一次性注册
- 长期 Agent Token
- Panel 只保存长期 Agent Token hash
- Agent 本地配置
- 配置文件权限 0600
- systemd 安装
- Agent 注册后 Server 从 pending → offline

当前 Agent 启动后会读取配置，并与 Panel 保持经过认证的 WebSocket 长连接。

---

# 4. Phase 4.5：admin / vip 两级账号与共享资源权限

## 目标

在继续 WebSocket 之前，先把账号语义改正确，避免后续 Server、Proxy、Relay API 越来越多以后再返工权限模型。

本阶段只做最小的两级账号：

```text
admin
vip
```

---

## 4.1 数据库

给 `users` 增加：

```text
role
```

允许值：

```text
admin
vip
```

迁移现有数据库时必须兼容旧数据。

迁移策略应保证：

- 现有首次创建的第一个用户成为 `admin`。
- 其他已经存在的邀请注册用户成为 `vip`。
- 不要求用户删除数据库重装 Panel。
- 迁移必须保留原有用户名、密码 hash、Session 和邀请数据。

不要新建复杂权限表。

---

## 4.2 初始化与邀请注册

首次初始化：

```text
第一个账号
→ role = admin
```

之后邀请注册：

```text
admin 创建邀请
↓
新账号注册
↓
role = vip
```

邀请本身不能指定角色。

也就是说不能出现：

```text
邀请为 admin
邀请为 vip
```

所有邀请注册固定得到 `vip`。

---

## 4.3 邀请权限

以下 API / 操作只允许 `admin`：

- 创建邀请
- 查看有效邀请
- 撤销邀请

`vip` 调用时返回：

```text
403 Forbidden
```

不能只依靠前端隐藏按钮。

前端：

- admin 显示邀请管理区域。
- vip 不显示邀请管理区域。
- vip 正常显示共享的服务器和后续节点功能。

---

## 4.4 全局共享资源

本阶段不要给 Server 增加 owner。

现有：

```text
servers
agents
agent_enrollments
```

继续属于整个 Panel。

admin / vip 查询 Server 时读取同一张 `servers` 表。

不要实现：

- user_servers
- server_shares
- owner_user_id
- 资源同步任务
- 每账号独立节点副本

以后 Proxy / Client / Relay / Subscription 也沿用同一原则。

---

## 4.5 账号信息 API

现有认证状态接口应返回当前用户的角色，例如：

```json
{
  "user": {
    "id": 1,
    "username": "admin",
    "role": "admin"
  }
}
```

前端根据 role 控制 Panel 级功能入口。

但后端必须再次做权限校验。

---

## 4.6 本阶段禁止实现

不要实现：

- 多个 admin
- Owner
- SuperAdmin
- Operator
- Viewer
- 自定义角色
- 权限勾选表
- 资源私有化
- Server 分享表
- Proxy 分享表
- WebSocket
- Heartbeat
- Metrics
- Xray
- Realm

---

## 4.7 验收

1. 旧数据库可以直接升级。
2. 首个用户为 admin。
3. 其他已有用户为 vip。
4. admin 可以创建邀请。
5. vip 创建邀请返回 403。
6. vip 看不到邀请管理 UI。
7. admin 和 vip 都能看到相同的 Server 数据。
8. 不存在资源复制或同步逻辑。
9. 原有登录、Server、Agent 注册功能继续正常。
10. `gofmt` / `go test` / `go build` / 前端 build 通过。

完成 Phase 4.5 后停止。

---

# 4.6 Phase 4.6：Server 安全移除与 Agent 重新绑定

> 当前状态：已实现并完成与 Phase 5A WebSocket 的兼容回归。

## 目标

解决 Server 被移除后丢失长期档案，以及 VPS 重装系统、误删 Agent 配置、Agent 长期凭据泄露或损坏以后无法重新接入原 Server 的问题。

固定语义：

```text
Server = 长期业务实体
Agent = 可更换、可轮换凭据的执行端身份
```

普通 `DELETE /api/servers/{id}` 只设置 `archived_at`、撤销 Agent 凭据并隐藏 Server；只有 admin 执行单独的“彻底删除”操作时才物理删除。重新绑定不创建新的 Server，也不复制原 Server 数据。

---

## 4.6.1 行为

安全移除与重新绑定流程：

1. 移除 Server 时写入 `archived_at`，保留 Server ID 和档案。
2. 原 Agent 长期 Token 立即失效，未使用 Enrollment Token 被清理。
3. 如果原 Agent 仍有有效 WebSocket，Panel 主动关闭该连接。
4. admin 可以在正常或已移除 Server 的详情弹窗中生成新的、一次性的 Agent 安装令牌。
5. Enrollment Token 继续绑定原 `server_id`，Server 状态进入：
   ```text
   pending
   ```
6. Panel 返回与首次安装格式相同的 Agent 安装命令，不要求用户提供覆盖参数。
7. Agent 自动检测本机是否已有配置；Panel 仅允许 `rebind` Enrollment 替换旧身份，并在注册成功后使用已 fsync 的临时文件原子替换旧配置。
8. 注册失败或 Panel 不可达时，旧配置保持不变。
9. 新 Agent 使用 Enrollment Token 注册，并得到新的长期 Agent Token。
10. 注册成功后清除 `archived_at`，Server 回到：
   ```text
   offline
   ```
11. WebSocket 建立后再变为 `online`。
12. 只有“彻底删除”才物理删除归档 Server 及关联数据。

保持固定关系：

```text
一个 Server
→ 一个当前有效 Agent 身份
```

不要因为重装 Agent 创建第二份 Server。

---

## 4.6.2 数据处理

由于 `agents.server_id` 是唯一关系，重新绑定时删除旧 Agent 认证记录并创建新记录，不制造同一 Server 的多个有效 Agent。

`agent_enrollments.purpose` 只使用 `initial` 和 `rebind` 两种内部值。新建 Server 时自动生成的首个 Enrollment 固定为 `initial`；管理员主动调用 `POST /api/servers/{id}/enrollment` 时固定生成 `rebind`，不根据 Agent 历史或 Server 状态推断。`initial` 仅允许本机没有 Agent 配置时注册；`rebind` 允许在注册成功后安全替换已有配置。

必须保证：

- 旧 Agent Token 不再可用。
- 新 Enrollment Token 仍然一次性。
- Enrollment Token 只保存 hash。
- 新 Agent Token 只保存 hash。
- 原始 Token 不写日志。
- Server 的到期日期、月流量设置、分组、标签等资料不能因为 Agent 重装而丢失。

---

## 4.6.3 UI

服务器页面保留“正常服务器 / 已移除”入口。两类 Server 都使用同一套详情模态弹窗，并只提供一个 Agent 身份操作：

```text
重新生成 Agent 安装令牌
```

新建 Server 自动生成的首个令牌为 `initial`；管理员在正常或已移除 Server 上主动重新生成的令牌固定为 `rebind`。两类 Server 调用相同 API；已移除 Server 仍保留“彻底删除”操作。生成结果在详情弹窗内显示，原始令牌关闭弹窗后不可再次获取。

点击后必须二次确认，并明确提示：

```text
重新生成后，当前 Agent 凭据将立即失效；彻底删除会永久删除 Server 及全部关联数据。
```

成功后只显示一次：

- Agent 安装令牌
- Agent 安装命令
- Token 过期时间

---

## 4.6.4 权限

这是 Agent 身份安全操作，当前只允许 `admin` 执行。

这不改变其他 Server / Proxy 资源的共享模型，也不在此阶段扩展 vip 的整体资源权限设计。

---

## 4.6.5 验收

1. 普通移除不物理删除 Server，默认列表隐藏它，已移除列表可以查看。
2. 原 Server ID 保持不变。
3. 原 Agent Token 失效。
4. 在线 Agent 连接被主动关闭，断开回调不会恢复归档 Server。
5. 新 Enrollment Token 只能使用一次。
6. 新 Agent 注册后仍绑定原 Server。
7. 注册成功后清除 `archived_at`，状态为 `offline`。
8. 不产生第二个有效 Agent 身份。
9. 重新绑定失败时保留旧配置，成功时以 0600 权限原子替换。
10. 重新绑定和彻底删除仅允许 admin。
11. 只有彻底删除才物理删除 Server。
12. 原有 Server / Agent 注册和 WebSocket 测试继续通过。

完成 Phase 4.6 后停止。

---

# 5. Phase 5A：Agent WebSocket 基础连接

> 当前状态：已实现；补齐 Phase 4.6 后需要重新执行兼容回归。

## 目标

本阶段只解决：

```text
Agent 能否和 Panel 建立一个经过认证的长期 WebSocket 连接
```

以及：

```text
连接成功 → online
连接断开 → offline
```

不做 Heartbeat，不做 Metrics。

---

## 4.1 Panel WebSocket Endpoint

新增：

```text
GET /api/agent/ws
```

该接口：

- 不使用管理员 Session
- 使用 Agent 长期 Token
- 必须完成 Agent 身份验证后再建立有效连接

认证逻辑：

```text
Agent Token
↓
hash
↓
agents.token_hash
↓
找到 Agent
↓
找到 Server
↓
建立 WebSocket
```

Token 不允许输出到日志。

---

## 4.2 Agent 连接

Agent 启动：

```text
读取 /etc/vps-panel-agent/config.json
↓
读取 panel_url
↓
读取 agent_token
↓
连接 /api/agent/ws
```

本阶段连接建立后只保持连接即可。

不发送 CPU/RAM 等数据。

---

## 4.3 Server 在线状态

成功建立经过认证的 WebSocket：

```text
Server → online
```

当前有效连接断开：

```text
Server → offline
```

---

## 4.4 Panel 启动状态恢复

Panel 重启后旧 WebSocket 已全部丢失。

因此 Panel 启动时：

```text
UPDATE servers
SET status = 'offline'
WHERE status = 'online'
```

但必须注意：

- pending 保持 pending
- 已注册 Agent 的服务器可以 offline
- 不要错误把 pending 改成 offline

目标是避免“数据库残留 online，但实际上 Agent 已断开”的假在线状态。

---

## 4.5 安全要求

必须满足：

- Agent Token 明文不入库
- Token 不写日志
- 错误 Token 无法连接
- Enrollment Token 不能当 Agent Token 使用
- 管理员 Cookie 不能代替 Agent Token
- WebSocket 设置合理最大消息大小
- 不接受匿名 Agent

---

## 4.6 本阶段禁止实现

不要实现：

- Heartbeat
- last_seen_at
- 自动重连
- CPU
- RAM
- Disk
- OS
- IP
- Uptime
- RX / TX
- Metrics 表
- 浏览器 WebSocket
- 远程任务
- Xray
- sing-box
- Mihomo
- Realm
- Proxy
- Client

---

## 4.7 验收

需要确认：

1. 无 Agent Token 无法连接。
2. 错误 Agent Token 无法连接。
3. 正确 Token 可以连接。
4. 成功连接后 Server 变成 online。
5. Agent 进程停止后 Server 变成 offline。
6. Panel 重启后不存在假 online。
7. 原有管理员、Server、Agent 注册测试不受影响。
8. `gofmt` 通过。
9. `go test` 通过。
10. `go build` 通过。
11. Agent amd64 / arm64 构建通过。

---

# 6. Phase 5B：Heartbeat 与自动重连

> 当前状态：已实现。Agent 每约 10 秒发送最小 Heartbeat，Panel 记录 `last_seen_at`，断线后按最高 30 秒退避重连；同一 Server 仅保留当前 WebSocket，安装时默认固定到当前 Panel Release，开发版本回退到最新 Release。

## 目标

Phase 5A 解决“能连”。

Phase 5B 解决：

```text
连接能长期稳定工作
```

---

## 5.1 Heartbeat

Agent 每约 10 秒发送：

```json
{
  "type": "heartbeat"
}
```

保持协议最小。

Panel 收到后：

- 确认该连接仍然有效
- 更新 Agent 最后通信时间

可以简单返回 ACK，也可以仅通过 WebSocket ping/pong 维持连接。

不要同时设计复杂的 Event Bus。

---

## 5.2 last_seen_at

在 agents 表增加：

```text
last_seen_at
```

允许 NULL。

作用：

- 显示最后通信时间
- 排查 Agent 是否长时间没有活动
- 后续辅助在线状态判断

不要为 heartbeat 建历史表。

---

## 5.3 自动重连

Agent 连接失败或断开后：

```text
1 秒
2 秒
4 秒
8 秒
16 秒
30 秒
30 秒
...
```

连接成功后退避重置。

临时网络故障不能导致 Agent 直接退出。

---

## 5.4 同一 Agent 新旧连接问题

必须避免这种情况：

```text
旧连接 A
↓
网络抖动
↓
新连接 B 建立
↓
Server online
↓
旧连接 A 稍后触发 Close
↓
错误把 Server 设置 offline
```

解决原则：

Panel 必须知道哪个连接是当前有效连接。

只有当前有效连接关闭时才能：

```text
Server → offline
```

不要为了这个问题引入：

- Redis
- 分布式锁
- 消息队列
- 多节点 Connection Manager

当前 Panel 是单实例，内存状态足够。

---

## 5.5 前端

本阶段前端不需要 WebSocket。

服务器页面可以：

- 手动刷新
- 或 10 秒左右简单轮询 `/api/servers`

显示：

```text
状态：在线
最后通信：刚刚
```

如果为了最后通信时间需要额外接口字段，可以做最小扩展。

不要做实时动画。

---

## 5.6 禁止实现

仍然禁止：

- 系统 Metrics
- 网络 Metrics
- 远程命令
- Core 管理
- Realm
- Proxy
- Client

---

## 5.7 验收

1. Agent 正常 heartbeat。
2. last_seen_at 更新。
3. Panel 与 Agent 临时断线后 Agent 自动重连。
4. 重连后恢复 online。
5. 旧连接关闭不会把新连接误判 offline。
6. Agent 不因普通网络波动退出。
7. 原有测试通过。

---

# 7. Phase 6A：静态系统信息与 Server 到期日期

> 当前状态：已完成。Agent 在 WebSocket 连接和重连成功后上报一次主机名、系统、内核、架构及本机 IP，Panel 按 Server 保存并在详情中展示；Server 详情支持以 `YYYY-MM-DD` 设置、修改、清除和显示到期日期，业务时区固定为 `Asia/Shanghai`。

这一阶段开始做系统监控，但继续拆小。

## 目标

Agent 上报变化频率非常低的系统信息。

建议包括：

- hostname
- OS
- OS version
- kernel
- architecture
- IPv4
- IPv6
- Agent version

这些信息可以：

- WebSocket 建立后立即上报一次
- Agent 重连后重新上报

无需每 5 秒上报。

---

## 6.1 Server 与 Agent 字段归属

建议：

Server 保存用户定义信息：

```text
name
status
```

Agent 或 SystemInfo 保存 Agent 探测信息：

```text
hostname
os
kernel
arch
ip
```

具体表结构在实现 Phase 6A 时根据当前代码最小选择。

不要现在为了“规范”先拆五张表。

---

## 6.2 IP

初版以 Agent 实际可可靠获取的信息为准。

至少支持：

- IPv4
- IPv6

不要在第一版做：

- ASN
- ISP
- GeoIP
- 国旗
- IP 风险评分

这些不是系统监控 MVP 必需。

---

## 6.3 UI

Server 详情至少应能展示：

```text
名称
状态
到期日期
主机名
系统
内核
架构
IPv4
IPv6
Agent 版本
```

服务器到期日期属于用户设置字段，不依赖 Agent 上报。

如果未设置：

```text
到期日期：不限
```

当前阶段只展示和编辑到期日期，不要求自动停机、自动删除服务器或自动禁用节点。

---

# 8. Phase 6B：动态系统指标

> 当前状态：已完成。Agent 约每 5 秒通过现有认证 WebSocket 上报 CPU、RAM、根分区磁盘和 Uptime，Panel 只保存并展示每台 Server 的最新值。

## 目标

加入：

- CPU 使用率
- RAM 使用量 / 总量
- Root Disk 使用量 / 总量
- Uptime

可以通过 WebSocket 定期发送。

建议周期：

```text
5 秒左右
```

不要追求 1 秒高频刷新。

---

## 7.1 数据策略

第一版只需要“当前值”。

不要急着创建大量历史数据。

即：

```text
Panel 内存 / 当前状态
+
必要最新值数据库
```

什么时候需要历史图，再单独设计历史存储。

---

## 7.2 CPU

显示：

```text
CPU 32%
```

不需要初版就做：

- 每核占用
- steal
- iowait
- IRQ
- temperature

---

## 7.3 RAM

显示：

```text
1.2 GB / 2 GB
```

---

## 7.4 Disk

第一版只统计：

```text
/
```

Root filesystem 即可。

不要扫描每个 mount。

---

# 9. Phase 7A：机器网卡累计流量与月流量统计

> 当前状态：已完成。

## 目标

本阶段只解决一件事：

> 让 Panel 能可靠看到一台 Server 的机器网卡累计 RX / TX，并按月流量规则计算“本周期已用 / 总量”。

Agent **不测速**。

明确不实现：

```text
Speedtest
iperf
带宽测试
实时下载速度
实时上传速度
RX/s
TX/s
历史流量曲线
流量时序数据库
```

本阶段也不依赖：

- Xray 流量统计
- sing-box 流量统计
- Mihomo 流量统计
- Realm 流量统计
- Proxy / Client 应用层流量统计

服务器月流量的基础数据只来自 Linux 网卡累计字节计数。

---

## 9.1 数据源

Linux 初版读取机器网卡累计值：

```text
/proc/net/dev
```

或者等价的：

```text
/sys/class/net/<interface>/statistics/rx_bytes
/sys/class/net/<interface>/statistics/tx_bytes
```

语义：

```text
RX = 机器累计入站字节
TX = 机器累计出站字节
```

必须排除：

```text
lo
```

同时不要把物理网卡、bridge、veth、tun 等多层接口重复累计。

第一版选择一个明确、可预测的主网卡即可，优先使用承载默认路由的实际网络接口。

不要为了自动识别所有复杂网络拓扑引入额外框架。

---

## 9.2 复用现有 Metrics WebSocket

当前 Agent 已经通过现有 WebSocket 上报：

```text
CPU
RAM
Disk
Uptime
```

Phase 7A 直接复用现有 `metrics` 消息。

不要新增：

```text
独立 Traffic WebSocket
/api/agent/traffic
新的常驻 goroutine
新的高频 ticker
新的实时速度采样器
```

现有 metrics 消息只增加：

```json
{
  "nic_rx_bytes": 123456789,
  "nic_tx_bytes": 987654321
}
```

Agent 只负责读取并上报**当前累计计数器值**。

Agent 不负责：

- 计算月流量周期
- 计算已用百分比
- 计算实时速率
- 保存流量历史
- 执行流量重置

这些业务逻辑属于 Panel。

---

## 9.3 Server 月流量配置

每台 Server 只增加当前真正需要的配置：

```text
monthly_traffic_limit_bytes
traffic_count_mode
traffic_reset_day
traffic_reset_time
```

其中：

```text
traffic_count_mode = single
```

表示：

```text
本周期已用 = cycle_tx_bytes
```

```text
traffic_count_mode = bidirectional
```

表示：

```text
本周期已用 = cycle_rx_bytes + cycle_tx_bytes
```

月流量上限允许为空或 0，表示：

```text
不限
```

即使不限，Panel 仍继续记录累计 RX / TX。

月流量额度输入使用：

```text
[ 数值 ] [ G ▼ ]
```

单位只支持：

```text
G
T
```

默认单位：

```text
G
```

内部仍然只保存：

```text
monthly_traffic_limit_bytes
```

不要增加 `traffic_unit` / `display_unit` 之类的数据库字段。

换算固定为：

```text
1 G = 1024^3 bytes
1 T = 1024^4 bytes
```

重新打开编辑 Modal 时：

- 能整除 `1T` 的额度优先显示为 `T`
- 其他额度显示为 `G`

单位只是输入 / 展示层，后端真实值始终为 bytes。

---

## 9.4 复用 `server_metrics` 保存最新计数与当前周期状态

当前项目已经有一张：

```text
server_metrics
```

第一版优先继续扩这张表，不为了流量统计新建独立历史表。

建议只增加当前需要的字段：

```text
nic_rx_bytes
nic_tx_bytes

cycle_rx_bytes
cycle_tx_bytes
cycle_started_at
traffic_adjustment_bytes
```

现有 CPU / RAM / Disk / Uptime 字段保持不变。

不要创建：

```text
traffic_samples
traffic_history
network_history
daily_traffic
hourly_traffic
```

这类时序表。

如果实现时发现将周期状态单独放一个小表明显更简单，可以使用一个最小状态表；但默认优先复用现有 `server_metrics`，不要为了“架构更漂亮”拆层。

---

## 9.5 Panel 增量累计

Agent 上报：

```text
current_rx
current_tx
```

Panel 读取上次持久化：

```text
last_rx
last_tx
```

正常情况下：

```text
delta_rx = current_rx - last_rx
delta_tx = current_tx - last_tx

cycle_rx_bytes += delta_rx
cycle_tx_bytes += delta_tx
```

然后保存新的：

```text
last_rx = current_rx
last_tx = current_tx
```

其中数据库字段可以直接复用：

```text
nic_rx_bytes
nic_tx_bytes
```

作为上次已处理的累计计数。

不要让 Agent 维护一套独立的月累计状态。

---

## 9.6 VPS / Agent / 网卡重启

Linux 网卡累计计数可能因为：

- VPS 重启
- 网卡重建
- 接口变化
- 内核计数器归零

而下降。

如果：

```text
current_rx < last_rx
```

或：

```text
current_tx < last_tx
```

对应方向不要产生负流量。

直接：

```text
将 current 作为新的 baseline
本次该方向 delta = 0
```

然后从之后的上报继续累计。

因此：

- Panel 重启后，累计状态不会丢失。
- Agent 重启后，只要 Linux 网卡计数没有归零，下一次上报可以继续计算增量。
- VPS 重启导致计数归零时，不产生负数。

第一版不尝试根据历史日志恢复无法观测到的流量。

---

## 9.7 月流量重置

每台 Server 可以设置：

```text
每月第 N 日 HH:mm
```

统一按：

```text
Asia/Shanghai
```

理解。

例如：

```text
每月 15 日 08:00
```

如果设置：

```text
29 / 30 / 31
```

而某个月不存在该日期，则使用：

```text
当月最后一天的同一时间
```

到达新周期后，Panel 在下一次收到有效 metrics 时：

```text
cycle_rx_bytes = 0
cycle_tx_bytes = 0
cycle_started_at = 新周期开始时间
nic_rx_bytes = 当前 Agent 上报值
nic_tx_bytes = 当前 Agent 上报值
```

也就是：

> 重置 Panel 的周期累计，并把当前 Linux 网卡值作为新 baseline。

不要修改 Linux 内核计数器。

不要为了“准点 00:00 重置”增加 scheduler / cron / background worker。

第一版由下一次 metrics 上报触发周期切换即可。

如果 Panel / Agent 在周期切换点长期离线，跨边界期间无法精确拆分到两个周期的流量；第一版不为此增加复杂补偿系统。

---

## 9.8 本周期流量手动校准

必须允许用户手动设置：

```text
本周期当前已用流量
```

典型场景：

```text
商家后台：
183G / 500G

VPS Panel 今天才开始监控：
20G
```

用户可以在 Server 详情中执行：

```text
校准本周期流量
```

输入目标值，例如：

```text
183 G
```

单位同样只支持：

```text
G
T
```

不要修改：

- Linux 网卡计数器
- `cycle_rx_bytes`
- `cycle_tx_bytes`

使用一个当前周期校准偏移量：

```text
measured_used
= single 时 cycle_tx_bytes
= bidirectional 时 cycle_rx_bytes + cycle_tx_bytes

traffic_adjustment_bytes
= 用户输入的目标已用量 - measured_used

displayed_used
= max(0, measured_used + traffic_adjustment_bytes)
```

例如：

```text
机器已统计 20G
用户校准为 183G

traffic_adjustment_bytes = 163G

之后机器新增 10G
displayed_used = 193G
```

`traffic_adjustment_bytes` 允许为负数。

校准只属于当前流量周期。

进入新周期时：

```text
traffic_adjustment_bytes = 0
```

如果用户切换单向 / 双向统计方式：

- 保留原始 `cycle_rx_bytes / cycle_tx_bytes`
- 根据新模式重新计算 `measured_used`
- 保留当前 adjustment
- UI 提示如果需要与商家后台继续完全一致，可以重新校准

不要把人工校准值硬塞进 RX / TX。

---

## 9.9 UI

本节只规定**流量字段必须展示什么**。

具体页面布局、Server 列表宽度、详情 Modal、操作列、容器稳定性等，统一参考：

```text
vps-panel-frontend-guide.md
```

实现本 Phase 时不要自己重新设计 Server 页面骨架。


服务器列表至少显示：

```text
100G（已用）/500G（总）
```

未设置额度：

```text
100G（已用）/不限（总）
```

Server 详情至少显示：

```text
月流量：100G（已用）/500G（总）
统计方式：单向（TX） / 双向（RX + TX）
本周期 RX：62G
本周期 TX：38G
重置时间：每月 15 日 08:00
本周期开始：2026-09-15 08:00
```

可以显示：

```text
剩余流量
使用率
校准偏移
```

其中使用率同时用于列表中的轻量预警 Tag：

```text
>= 90% 且 < 100%
→ 流量预警

>= 100%
→ 流量已用完
```

Server 详情提供：

```text
校准本周期流量
```

操作入口，使用 Modal，不做行内展开。

不要显示：

```text
实时下载速度
实时上传速度
速度图
历史流量图
```

---

## 9.10 流量预警与超额行为

Server 的流量预警只做 Panel UI 派生状态。

使用：

```text
displayed_used
monthly_traffic_limit_bytes
```

计算使用率。

固定规则：

```text
未设置额度
→ 不显示预警

used < 90%
→ 不显示预警

90% <= used < 100%
→ 流量预警

used >= 100%
→ 流量已用完
```

预警不能修改：

```text
server.status
```

因此可以同时显示：

```text
[在线] [流量预警]
```

或者：

```text
[在线] [流量已用完]
```

不要增加：

```text
traffic_warning
traffic_status
warning_level
```

等持久化状态字段。

即使流量达到或超过上限，也不要自动：

- 断网
- 停止 Agent
- 停止 Xray
- 删除 Proxy
- 修改防火墙
- 限速

当前只做 Panel UI 状态提示，不发送 Telegram / Email / Webhook。

---

## 9.11 本阶段明确不做

以下仍然后移，不阻塞 Phase 7A：

```text
可自定义流量预警阈值
Telegram / Email / Webhook 通知
流量超额自动断网 / 自动停服务 / 自动限速
到期 30 天 / 7 天预警
概览页流量汇总
实时上下行速度
历史流量图
每日 / 每小时流量
Speedtest / iperf
```

当前预警阈值固定为 90% / 100%，不做每台 Server 自定义。

以后确实需要时再单独增加。

底层：

```text
nic_rx_bytes
nic_tx_bytes
cycle_rx_bytes
cycle_tx_bytes
```

已经足够支撑这些未来功能，不需要现在实现。

---

## 9.12 验收

至少验证：

1. Agent 能读取选定主网卡累计 RX / TX。
2. Agent 不进行 Speedtest、iperf 或任何主动带宽测速。
3. Agent 不计算 RX/s、TX/s 或实时下载 / 上传速度。
4. Agent 复用现有 `metrics` WebSocket 消息上报累计 RX / TX。
5. 不新增独立 traffic WebSocket 或高频采样器。
6. Panel 能持久化上次网卡累计计数。
7. Panel 能通过 delta 正确累计本周期 RX / TX。
8. 单向模式按 TX 计算月流量。
9. 双向模式按 RX + TX 计算月流量。
10. 网卡计数器下降时不会产生负流量。
11. Agent / Panel 重启后当前周期累计不会被无条件清零。
12. 到达新周期后，下一次 metrics 上报会开启新的周期并建立新 baseline。
13. 月流量重置不修改 Linux 网卡计数器。
14. Server 可以设置月流量总额。
15. 月流量输入默认单位为 G，并可选择 T。
16. G / T 最终统一转换为 bytes 保存。
17. Server 可以选择单向 / 双向。
18. Server 可以设置每月流量重置日和时间。
19. Server 可以手动校准当前周期已用流量。
20. 校准不会修改原始 RX / TX。
21. 新周期开始时校准偏移自动归零。
22. 服务器列表能显示 `已用 / 总量`。
23. Server 详情能显示本周期 RX / TX。
24. 未设置额度时能显示 `已用 / 不限`。
25. 使用率达到 90% 时显示“流量预警”。
26. 使用率达到或超过 100% 时显示“流量已用完”。
27. 预警不会修改 Server online / offline / pending 状态。
28. 不实现自定义阈值、通知、实时速度、历史图。
29. 不因为流量超额自动停服务。

完成 Phase 7A 后停止。
不要继续服务器分组 / 标签。

---

# 10. Phase 7B：服务器分组、标签与筛选

> 当前状态：暂缓，不阻塞代理主链路。

> 本 Phase 涉及 Server 列表工具栏、筛选器、分组 / 标签显示。
>
> UI 实现必须参考 `vps-panel-frontend-guide.md` 中：
>
> - 页面顶部固定结构
> - Server 列表
> - 搜索、筛选和排序
> - 固定容器与“不能随便动”的实现要求
>
> 不允许为了新增筛选功能改变 Sidebar、主内容宽度、列表容器位置或“详情使用 Modal”的既有规则。


服务器数量增多以后，需要提供轻量组织能力。

每台 Server 可以设置：

```text
分组：一个，可为空
标签：多个，可为空
```

例如：

```text
Server：TY Cloud JP

分组：
日本

标签：
入口
移动
CMIN2
主力
```

或者：

```text
Server：US Home

分组：
美国

标签：
落地
家宽
金融
```

### 设计原则

保持简单。

不要立即实现复杂：

- 多层级文件夹
- 树形资源目录
- ACL
- 标签权限
- 标签自动同步规则

分组只需要一个可选名称。

标签支持多个普通字符串即可。

### 页面

服务器列表支持：

```text
按分组筛选
按标签筛选
按状态筛选
关键词搜索
```

至少能快速找到：

```text
日本 + 入口
美国 + 落地
离线
```

第一版只做分组、标签、在线状态和关键词搜索。

不要为了这个 Phase 顺便加入到期预警、高流量阈值筛选或复杂组合查询。

Server 详情允许编辑分组和标签。

这些字段属于 Panel 的 Server 管理资料，不由 Agent 上报。

### 与共享资源的关系

分组 / 标签属于全局 Server 数据。

admin / vip 看到的是同一份分组和标签，不按用户复制。

当前不借此重新设计 vip 的增删改权限边界。

### 验收

1. Server 可以设置一个可选分组。
2. Server 可以设置多个标签。
3. 可以按分组筛选。
4. 可以按标签筛选。
5. 可以按在线状态筛选。
6. 可以搜索 Server 名称。
7. Agent 重装不会丢失分组 / 标签。
8. ZIP 备份包含分组 / 标签。

---

## 到此完成第一阶段服务器管理 MVP

完成 Phase 7B 后：

```text
添加 Server
↓
安装 Agent
↓
自动注册
↓
在线 / 离线
↓
系统信息
↓
CPU / RAM / Disk / Uptime
↓
机器网卡累计 RX / TX
↓
月流量单向 / 双向统计
↓
按配置时间自动开启新周期
↓
服务器到期日期
↓
服务器分组 / 标签 / 筛选
```

这是第一个完整服务器管理 MVP。

在这个节点之后，再开始代理内核。

---

# 11. Phase 8A：通用 Agent 配置同步 API

> 当前状态：已完成。Panel 与 Agent 已建立带版本的完整 desired state 拉取、同步结果回报、`config_changed` 通知和约 30 秒 REST 兜底链路；当前仍只支持空 Xray / Realm 配置的 no-op apply。

## 目标

在服务器管理 MVP 完成后，先建立一条最小、稳定的 Panel ↔ Agent 配置同步链路。

本阶段只解决：

```text
Panel 如何告诉 Agent：
“这台 Server 现在应该是什么配置”
```

固定采用：

```text
REST = 读取完整 desired state / 回报同步结果
WebSocket = config_changed 轻量通知
```

不要建立通用任务系统。

---

## 11.1 desired state 版本

每台 Server 维护一个简单的配置版本号，例如：

```text
desired_state_version
```

当该 Server 的受管 Proxy / Relay 配置发生实际变化时：

```text
version + 1
```

系统监控、Server 名称、标签等不影响代理配置的修改，不应增加此版本。

---

## 11.2 配置 API

第一版只需要：

```text
GET  /api/agent/config
POST /api/agent/config/result
```

继续使用现有 Agent 长期身份认证。

`GET /api/agent/config` 返回：

```json
{
  "version": 1,
  "xray": {
    "enabled": false,
    "proxies": []
  },
  "realm": {
    "enabled": false,
    "relays": []
  }
}
```

本阶段允许内容还是空配置。

重点是把：

- 身份认证
- version
- 拉取
- 结果回报
- 状态持久化
- 失败信息

这一条链路跑通。

---

## 11.3 WebSocket 通知

现有 `/api/agent/ws` 增加一个非常小的消息：

```json
{
  "type": "config_changed",
  "version": 2
}
```

Agent 收到后立即重新 GET `/api/agent/config`。

不要通过 WebSocket 直接发送整份配置。

如果 WebSocket 不可用：

```text
Agent 每约 30 秒轮询一次 config version
```

作为兜底。

---

## 11.4 同步结果

Agent 应在完成实际应用后回报：

```json
{
  "version": 2,
  "status": "success",
  "message": ""
}
```

失败：

```json
{
  "version": 2,
  "status": "failed",
  "message": "..."
}
```

Panel 至少保存：

- Agent 最后成功同步版本
- 最后同步状态
- 最后错误信息
- 最后同步时间

不要建历史事件表。

---

## 11.5 本阶段不要实现

不要实现：

- Proxy 表
- VLESS
- Shadowsocks
- Xray 安装
- Realm 安装
- Client
- Subscription
- Generic Task Runner
- Plugin SDK
- CoreInstance
- AccessEndpoint
- Chain

完成 Phase 8A 后停止。

---

# 12. Phase 8B：Xray 托管基础与安全配置应用

> 当前状态：已完成。Agent 使用固定的 Xray 官方 Release `v26.3.27` 与 amd64 / arm64 SHA256 校验，在独立受管路径安装并验证 Xray；已实现最小基础配置、candidate 官方校验、同目录原子替换、current / previous、独立 systemd unit、有限健康检查、失败回滚和 enabled / disabled 行为。当前 Panel 仍不创建真实 Proxy。

## 目标

让统一 Agent 能安全托管一份由 VPS Panel 完全拥有的 Xray。

第一版不接管用户已有 Xray 配置。

推荐受管位置：

```text
/etc/vps-panel/xray/config.json
vps-panel-xray.service
```

需要实现：

- 检查受管 Xray 是否已安装
- 必要时安装 Panel 管理的 Xray
- 查询版本
- 生成基础配置
- 配置校验
- 原子替换
- 启动 / 重启
- 健康检查
- 失败回滚

Panel 不发送 shell。

Agent 根据 desired state 自己决定：

```text
是否需要安装
是否需要更新 config
是否需要 restart
```

---

## 12.1 配置安全流程

必须：

```text
render candidate
↓
xray config test / 等价校验
↓
保存 previous
↓
atomic replace
↓
restart
↓
确认 service active
↓
成功
```

任何一步失败：

```text
rollback previous
↓
重新启动旧配置
↓
POST config/result = failed
```

本阶段不需要做 Xray 自动升级。

---

## 12.2 冲突处理

如果发现：

- 443 已被其他服务占用
- 已有非 Panel 管理 Xray
- 配置路径冲突
- systemd unit 冲突

必须明确失败。

第一版不要尝试自动合并第三方配置。

---

## 12.3 实现来源要求

Xray 托管代码必须由 VPS Panel 独立实现。

可以借鉴第三方项目“先生成 candidate、校验、应用、回滚”这类高层工程思想，
但安装、配置渲染、校验、服务管理、错误处理等代码必须重新设计和编写。

不得直接使用 3x-ui / x-ui 等面板的：

- 安装脚本
- Xray 配置模板
- 目录结构
- systemd unit
- API 结构
- helper 函数
- 命名体系

实现依据优先使用 Xray 官方文档 / 官方二进制 / 上游源码行为。

---

# 13. Phase 9A：VLESS + TCP + TLS / REALITY + XTLS Vision

> 本 Phase 的数据语义、协议组合和 Agent 配置以本文为准。
>
> Proxy 列表、入口 IP / 出口 IP / 端口 / 协议 / 传输 / 安全层 / 流控的列展示，以及查看 / 新增 / 编辑 Modal，统一参考：
>
> ```text
> vps-panel-frontend-guide.md
> ```
>
> 重点参考 Frontend Guide 的：
>
> - Proxy / 节点列表
> - Proxy 详情 Modal
> - Proxy 新增 / 编辑 Modal
> - 操作列
> - Modal 固定规范
>
> 不要在本 Phase 另做一套 Proxy 页面布局。


## 目标

在 Phase 8B 的受管 Xray 上实现第一个真实 Proxy。

第一版 VLESS 固定：

```text
协议：VLESS
传输：TCP
Flow：XTLS Vision
```

用户只在安全层二选一：

```text
TLS
REALITY
```

因此第一版必须同时支持：

```text
VLESS + TCP + TLS + XTLS Vision
VLESS + TCP + REALITY + XTLS Vision
```

并且两种安全模式都支持客户端：

```text
xtls-rprx-vision
xtls-rprx-vision-udp443
```

不要把 XTLS Vision 推迟到以后。

---

## 13.1 第一版 UI 边界

新增 VLESS Proxy 时，第一版建议呈现：

```text
协议
VLESS                     固定

传输
TCP                       固定

安全
○ TLS
○ REALITY                 用户二选一

Flow
XTLS Vision               固定

允许 UDP/443 / QUIC
○ 关闭
○ 开启                    用户二选一
```

不要把第一版做成协议组合器。

不要提供：

- Transport 任意下拉组合
- Flow = none
- Flow 自定义字符串
- security 自定义字符串
- 任意 Xray JSON 编辑器

第一版只有两个明确模板：

```text
TLS + XTLS Vision
REALITY + XTLS Vision
```

---

## 13.2 服务端固定语义

两种模式都固定：

```text
protocol = vless
network = tcp
client.flow = xtls-rprx-vision
```

TLS 模式：

```text
security = tls
```

REALITY 模式：

```text
security = reality
```

第一版不做：

- WebSocket transport
- gRPC transport
- HTTPUpgrade
- XHTTP
- VLESS + TCP + none
- VLESS + TCP + TLS + no flow
- VLESS + TCP + REALITY + no flow
- VMess
- Trojan
- 多 transport 自动组合

先把这两套固定组合做稳定。

---

## 13.3 TLS 模式

TLS 分支至少能够管理：

```text
listen address
listen port
UUID / Client credential
server_name / SNI
certificate
private key
fingerprint（客户端输出需要时）
```

证书处理第一版保持最小。

至少要能够让一条 TLS + XTLS Vision Proxy 真正可用，但不要为了本 Phase 顺手实现完整 ACME / DNS Provider / 自动续签平台。

证书来源的具体 UX 在实现本 Phase 时根据现有代码选择最小方案，例如：

```text
用户提供已有证书 / 私钥
```

或者一个同等简单且安全的受管方式。

无论采用哪种方式：

- TLS private key 属于敏感数据
- 不写普通日志
- 不通过不必要的 API 反复返回
- Agent 只写入 VPS Panel 自己管理的文件
- 文件权限必须收紧

不要照抄 3x-ui / x-ui 的证书管理代码。

---

## 13.4 REALITY 模式

REALITY 分支至少能够管理：

```text
listen address
listen port
UUID / Client credential
server_name / SNI
dest / target
private_key
public_key
short_id
fingerprint（用于客户端输出）
```

创建 REALITY Proxy 时：

- private key / public key 必须成对管理
- 不要求用户必须手工提前生成
- 可以由 Agent 或 Panel 中受控代码调用 Xray 官方能力生成
- private key 属于敏感数据
- private key 不写普通日志
- API 不应在无必要时反复返回 private key
- 分享 / 订阅只使用客户端真正需要的 public key 等字段

不要复制 3x-ui / x-ui 的 REALITY 密钥生成代码。

优先使用 Xray 官方能力或官方二进制完成。

---

## 13.5 XTLS Vision 与 UDP/443

服务端 Client flow 在 TLS / REALITY 两种模式下都始终是：

```text
xtls-rprx-vision
```

客户端导出 / 分享时支持：

```text
普通 Vision：
xtls-rprx-vision

允许 UDP/443 / QUIC：
xtls-rprx-vision-udp443
```

映射规则：

```text
允许 UDP/443 / QUIC = 关闭
→ client flow = xtls-rprx-vision

允许 UDP/443 / QUIC = 开启
→ client flow = xtls-rprx-vision-udp443
```

`xtls-rprx-vision-udp443` 是客户端行为。

不要：

- 服务端额外监听 UDP/443
- 把服务端 flow 改成 `xtls-rprx-vision-udp443`
- 把它做成第二个 Proxy 协议
- 因为这个选项额外启动一个 Xray inbound

客户端本地入口 / TUN 是否支持 UDP 由具体客户端负责；VPS Panel 只负责生成正确节点参数。

---

## 13.6 数据库

本阶段创建：

```text
proxies
```

最小字段：

```text
id
server_id
name
protocol
listen_port
enabled
config_json
created_at
updated_at
```

其中：

```text
protocol = vless
```

`public_host` 为可空的公开连接地址字段。

语义：

```text
public_host = 空
→ 分享 / 订阅时使用 Server IP

public_host = jp.example.com
→ 分享 / 订阅时使用 jp.example.com
```

第一版 UI 可以把它显示为：

```text
节点域名（可选）
```

该字段只表示客户端连接地址，不改变：

- Xray 服务端监听地址
- Server 实际 IP
- DNS 记录
- TLS / REALITY 的 SNI / server_name
- REALITY dest / target

Panel 不负责自动创建、修改或验证 DNS 解析，只保存用户填写的已关联域名。

第一版 `config_json` 只保存 VPS Panel 当前支持的：

```text
TCP
TLS | REALITY
XTLS Vision
client_udp443
```

以及对应安全分支真正需要的字段。

不要保存整份用户可编辑的原始 Xray JSON。

Panel 后端必须自己：

- 根据 `security` 分支校验
- 校验 UUID
- 校验端口
- 校验 TLS 必需字段
- 校验 REALITY 必需字段
- 固定 `transport = tcp`
- 固定服务端 `flow = xtls-rprx-vision`
- 生成规范 desired state

然后由 Agent renderer 生成 Xray 配置。

---

## 13.7 desired state

Panel 修改 Proxy 后：

```text
数据库保存
↓
desired_state_version + 1
↓
WS config_changed
↓
Agent GET /api/agent/config
↓
根据该 Server 的所有 Proxy 重新生成完整 Xray candidate config
↓
Xray 官方配置校验
↓
保存 previous
↓
原子替换
↓
restart / reload
↓
health check
├── success → POST config/result success
└── failed  → rollback → POST config/result failed
```

Agent 不对正式 Xray 配置做局部 `sed` 修改。

---

## 13.8 分享 / 客户端参数边界

Phase 9A 可以先保存并验证生成客户端配置所需的数据，但完整订阅 / 二维码 UI 仍放到后续分享阶段。

TLS 模式至少能够正确表达：

```text
address
port
uuid
security = tls
network = tcp
sni
fingerprint
flow
```

REALITY 模式至少能够正确表达：

```text
address
port
uuid
security = reality
network = tcp
sni
public key
short id
fingerprint
flow
```

客户端 `flow` 根据 `client_udp443` 派生。

客户端连接地址统一按：

```text
如果 Proxy.public_host 非空
→ address = public_host

否则
→ address = Server IP
```

例如：

```text
Server IP = 1.2.3.4
public_host = jp.example.com
```

生成的节点连接地址使用：

```text
jp.example.com:端口
```

而不是：

```text
1.2.3.4:端口
```

这里只替换客户端节点链接中的连接 host，不自动修改 SNI、证书域名、REALITY server_name 或其他安全参数。

不要在分享链接 / 配置中加入：

```text
3x-ui
x-ui
marzban
xboard
v2board
vps-panel
panel-managed
```

等非协议必要标识。

节点 remark 默认只使用用户设置的节点名称。

---

## 13.9 原创实现硬性要求

实现本 Phase 时可以研究：

- Xray 官方文档
- Xray 官方示例
- Xray 上游源码
- 其他代理面板提供了哪些功能
- 第三方面板某些可优化的高层代码职责划分

但不得从 3x-ui / x-ui / Marzban / Xboard / V2Board / 其他机场面板：

- 复制 VLESS / TLS / REALITY / XTLS 配置生成函数
- 复制数据结构
- 复制 API
- 复制数据库 schema
- 复制前端表单
- 复制字段命名体系
- 复制安装脚本
- 复制 systemd 配置
- 复制错误处理和日志文案
- 复制配置模板后只改变量名
- 复刻能够明显识别出原面板的 UI / 交互

允许借鉴的是：

```text
功能
协议能力
用户流程
职责划分
可验证的工程思路
```

最终实现必须回到 VPS Panel 自己的：

```text
数据模型
Agent API
目录结构
命名
renderer
validator
apply / rollback
```

重新独立设计和编写。

Code Review 时如果发现实现和某个第三方面板高度同构，应当重写成 VPS Panel 自己的实现。

本 Phase 的协议依据优先来自 Xray 官方行为，而不是某个面板的私有实现。

---

## 13.10 验收

至少验证：

1. 可以在指定 Server 创建 VLESS Proxy。
2. Transport 第一版固定为 TCP。
3. Flow 第一版固定为 XTLS Vision。
4. 用户可以选择 TLS 或 REALITY。
5. TLS 模式可以正常生成并应用 Xray 配置。
6. REALITY 模式可以正常生成并应用 Xray 配置。
7. TLS 与 REALITY 服务端 client flow 都是 `xtls-rprx-vision`。
8. 两种安全模式都能生成普通 Vision 客户端参数。
9. 两种安全模式都能生成 `xtls-rprx-vision-udp443` 客户端参数。
10. 开启 UDP/443 选项不会让服务端新增 UDP/443 listener。
11. UDP/443 选项不会改变服务端 inbound flow。
12. TLS private key 不出现在普通日志。
13. REALITY private key 不出现在普通日志。
14. REALITY private/public key 与 short id 可以正确生成、保存和读取。
15. TLS / REALITY 条件字段不会互相错误要求。
16. 修改 Proxy 后只增加 desired state version，不生成通用 task。
17. Agent 使用完整 candidate config 应用。
18. 配置校验失败不会覆盖当前可用配置。
19. restart / health check 失败会回滚 previous。
20. 分享 / 客户端配置中不出现第三方面板品牌或非必要 Panel 标识。
21. 实现代码无第三方面板复制痕迹。
22. Proxy 可以保存可选 `public_host`。
23. `public_host` 为空时，客户端连接地址回退到 Server IP。
24. `public_host` 非空时，客户端连接地址优先使用该域名。
25. 设置 `public_host` 不会修改 Xray listener、Server IP、SNI / server_name 或 DNS。
26. gofmt / go test / go build / 前端 build 通过。

完成 Phase 9A 后停止。
不要继续 Shadowsocks。

---

# 14. Phase 9B：Shadowsocks Proxy

> Shadowsocks 与 VLESS 共用同一套 Proxy 页面骨架。
>
> UI 继续参考 `vps-panel-frontend-guide.md` 的 Proxy 列表与 Modal 规范。
>
> 对 Shadowsocks 不适用的“传输 / 安全层 / 流控”列统一显示 `--`，不要为了填满表格制造虚假概念。


在第一版 VLESS（TLS / REALITY + XTLS Vision）稳定后，同一套 `proxies` 表增加：

```text
protocol = shadowsocks
```

第一版只支持明确选定的 Shadowsocks 方法。

仍然由同一个 Xray 承载：

```text
Server
└── Agent
    └── Xray
        ├── VLESS
        └── Shadowsocks
```

不要因为增加第二种协议就引入 `CoreInstance` 或插件系统。

---

# 15. Phase 10：Client

> Client 固定属于 Proxy。
>
> Client UI 默认作为 Proxy 详情 / 编辑流程的一部分，继续遵守 `vps-panel-frontend-guide.md` 的固定容器与 Modal 规则。
>
> 可以研究 3x-ui 等第三方面板已经提供了哪些 Client 功能，例如“独立凭据、流量额度、重置周期、到期时间、流量统计”等产品能力；但仍严格遵守本 Guide 的原创实现要求：
>
> - 不复制第三方面板代码
> - 不机械复制数据库 schema
> - 不机械复制 API
> - 不复制其字段命名体系
> - 不复刻其前端布局和交互
>
> 最终实现必须回到 VPS Panel 自己的 Proxy / Client / Agent / desired-state 模型。

## 15.1 Phase 10A：Client 基础管理

### 目标

把：

```text
一个 Proxy = 一份凭据
```

扩展为：

```text
Proxy
├── Client A
├── Client B
└── Client C
```

例如：

```text
VLESS
├── PC
├── Android
└── iPhone
```

Client 第一阶段只解决：

- 独立名称
- 独立凭据
- 用户手动启用 / 禁用
- Client CRUD
- desired state 正确渲染到 Xray

### 数据模型

进入 Phase 10A 时正式创建：

```text
clients
```

最小字段建议：

```text
id
proxy_id
name
credential_json
enabled
created_at
updated_at
```

`credential_json` 只保存该协议当前必要的凭据字段，并由后端严格校验，不允许前端提交任意 Xray JSON。

例如 VLESS：

```text
UUID
```

Shadowsocks 如果当前 Xray 模式确实支持一条 Proxy 下多个独立用户，再按官方能力映射；不要为了统一模型伪造不支持的行为。

### 从单凭据 Proxy 迁移

如果 Phase 9A / 9B 已经存在单凭据 Proxy：

进入 Phase 10A 的 migration 必须：

1. 为已有 Proxy 创建一个默认 Client。
2. 保留原 UUID / Password。
3. 不要求用户重新生成节点。
4. 不改变现有 Proxy 监听地址、端口和安全层。
5. 分享 / 订阅在迁移后继续生成可用配置。
6. migration 必须幂等。

迁移完成后：

> 凭据的业务归属从 Proxy 单凭据过渡到 Client。

Proxy 继续保存：

- 协议
- 监听端口
- transport
- security
- flow
- public_host
- 服务端公共参数

Client 保存：

- 独立 credential
- Client 自身状态

### Xray desired state

Panel 为同一个 Proxy 渲染多个 Client 到同一个 Xray inbound。

不要：

- 为每个 Client 新建一个 Proxy
- 为每个 Client 新建监听端口
- 为每个 Client 新建 Agent
- 为每个 Client 建独立 Xray 进程

### UI

Proxy 详情 Modal 增加：

```text
客户端
```

区域。

列表至少展示：

```text
名称
状态
凭据摘要
操作
```

操作：

```text
查看
编辑
启用 / 禁用
删除
```

敏感凭据默认遮蔽。

查看 / 编辑继续使用 Modal，不行内展开，不为 Client 建一套完全不同的页面骨架。

### Phase 10A 不做

当前不做：

- Client 流量
- Client 流量额度
- Client 周期重置
- Client 到期
- Client 预警
- Client 自动失效
- Client 历史流量图
- 精确连接级在线列表

### Phase 10A 验收

至少验证：

1. 一个 Proxy 可以拥有多个 Client。
2. 每个 VLESS Client 有独立 UUID。
3. 删除一个 Client 不影响同 Proxy 的其他 Client。
4. 禁用一个 Client 后 desired state 不再让该 Client 可用。
5. 不为 Client 创建额外端口或额外 Xray 进程。
6. 已有单凭据 Proxy 能安全迁移为默认 Client。
7. 迁移后原节点凭据保持可用。
8. Proxy 详情 Modal 可以管理 Client。
9. 实现无第三方面板代码 / schema / UI 复制痕迹。

完成 Phase 10A 后停止。
不要继续 Phase 10B。

---

## 15.2 Phase 10B：Client 流量、额度、周期与到期

### 目标

在 Client 基础管理稳定以后，实现：

```text
每 Client 独立流量统计
+
流量额度
+
流量重置周期
+
到期时间
+
流量预警
+
流量耗尽 / 到期后的自动不可用
```

Client 流量来源固定为：

```text
Xray per-client stats
```

不要读取 Linux 网卡来区分不同 Client。

因此统计关系是：

```text
Server 月流量
→ Linux 主网卡 RX / TX

Client 流量
→ Xray per-client uplink / downlink
```

两套数据互不替代。

### Xray 统计标识

每个 Client 必须有一个稳定、唯一、非敏感的内部统计标识，用于把 Xray per-client stats 映射回 Panel Client。

该标识：

- 由 VPS Panel 独立设计
- 不使用第三方面板专有格式
- 不需要出现在分享 URI / 订阅配置中
- 不使用用户 secret 作为日志 / stats key
- Client 修改显示名称后仍能稳定映射

实现时优先依据 Xray 官方 stats / policy 能力。

### Agent 流量采集

Phase 10B 开始后，Agent 才实现本 Guide 预留的：

```text
POST /api/agent/traffic
```

用途仅限：

> 上报 Proxy / Client 应用层累计流量状态。

不要把 Client traffic 塞入机器 `metrics` WebSocket。

Agent：

1. 查询本机受管 Xray 的 per-client cumulative counters。
2. 取得每个 Client 的 uplink / downlink。
3. 按稳定 Client 标识上报 Panel。
4. 不计算额度。
5. 不计算到期。
6. 不决定是否禁用 Client。

轮询不需要高频。

第一版建议：

```text
10 ~ 30 秒
```

保持简单即可。

### Panel 增量累计

不要假设 Xray counter 永远不会重置。

对每个 Client 保存最近一次：

```text
xray_uplink_bytes
xray_downlink_bytes
```

Panel 根据连续上报计算：

```text
delta_up
delta_down
```

然后累计：

```text
cycle_uplink_bytes
cycle_downlink_bytes
```

如果 Xray 重启或 counter 下降：

```text
current < previous
→ 本次 delta = 0
→ current 成为新 baseline
```

不能产生负流量。

### 数据模型

在 `clients` 增加当前需要的配置：

```text
traffic_limit_bytes
traffic_reset_mode
traffic_reset_weekday
traffic_reset_day
traffic_reset_time
expires_at
```

其中：

```text
traffic_limit_bytes = NULL / 0
→ 不限
```

`traffic_reset_mode` 第一版只支持：

```text
never
daily
weekly
monthly
```

第一版不做 hourly。

`traffic_reset_time` 按 `Asia/Shanghai` 理解。

weekly 需要 weekday。

monthly 需要 day。

如果 monthly 设置 29 / 30 / 31，而当月没有该日期：

```text
→ 当月最后一天同一时间
```

Client 到期时间允许：

```text
不限
指定日期 / 时间
```

数据库内部继续保存 UTC / Unix Timestamp，业务解释和展示按 `Asia/Shanghai`。

Client 流量状态建议放在独立的小表：

```text
client_metrics
```

第一版最小字段：

```text
client_id
xray_uplink_bytes
xray_downlink_bytes
cycle_uplink_bytes
cycle_downlink_bytes
cycle_started_at
last_activity_at
updated_at
```

不要创建小时 / 日历史表。

`last_activity_at` 的语义固定为：

> 最近一次检测到该 Client 流量 counter 增长的时间。

它不是“当前连接仍在线”的绝对证明。

前端优先显示：

```text
最近活动
```

不要把它误标成精确在线状态。

### Client 已用流量

第一版 Client 已用量固定为：

```text
used_bytes
= cycle_uplink_bytes + cycle_downlink_bytes
```

Client 不提供 Server 那样的“单向 / 双向计费模式”。

如果未来真实需要，再单独增加。

### Client 流量额度单位

UI 与 Server 流量设置保持一致：

```text
[ 数值 ] [ G ▼ ]
```

单位只允许：

```text
G
T
```

默认：

```text
G
```

内部只保存：

```text
traffic_limit_bytes
```

换算：

```text
1 G = 1024^3 bytes
1 T = 1024^4 bytes
```

不要保存 display unit。

### Client 流量周期

支持：

```text
不重置
每天
每周
每月
```

例如：

```text
每天 00:00
每周一 00:00
每月 1 日 00:00
```

周期切换不要求独立 cron / job queue。

Panel 在下一次有效 Client traffic report 到来时判断是否跨周期。

跨周期时：

```text
cycle_uplink_bytes = 0
cycle_downlink_bytes = 0
cycle_started_at = 新周期
xray baseline = 当前 Xray counter
```

不要修改 Xray 内部 counter。

### 到期与流量耗尽

Client 的用户开关和业务可用状态必须分开。

保存：

```text
enabled
```

只表示：

> 用户是否主动启用这个 Client。

另外派生：

```text
expired
quota_exhausted
effective_enabled
```

规则：

```text
expired
= expires_at 已经过期

quota_exhausted
= 设置了 traffic_limit_bytes
  且 used_bytes >= traffic_limit_bytes

effective_enabled
= enabled
  && !expired
  && !quota_exhausted
```

不要因为到期 / 流量耗尽直接把用户的 `enabled` 永久改成 false。

这样：

- 用户主动关闭的 Client 不会被系统自动打开
- 月度 / 周期流量重置后，quota_exhausted 自动消失
- 如果 Client 仍处于 enabled 且未过期，可自动恢复可用
- 到期 Client 在修改 expiry 前保持不可用

Panel 生成 desired state 时只把 `effective_enabled = true` 的 Client 渲染成可用 Xray user。

当 quota / expiry 导致 effective state 变化时：

```text
desired_state_version + 1
→ config_changed
→ Agent 拉取新 desired state
→ 安全应用 Xray 配置
```

不增加通用 task runner。

### Client 流量预警

固定：

```text
未设置额度
→ 无预警

used < 90%
→ 正常

90% <= used < 100%
→ 流量预警

used >= 100%
→ 流量已用完
```

预警状态由 Panel 派生，不保存冗余 warning 字段。

### Client 到期状态

固定：

```text
未设置 expires_at
→ 不限

未到期
→ 正常

已到期
→ 已到期 / 不可用
```

第一版不做：

- 到期前 30 天通知
- 到期前 7 天通知
- Email
- Telegram
- Webhook

### 手动重置本周期流量

Client 详情提供：

```text
重置本周期流量
```

该操作只重置 Panel 当前 Client 周期累计：

```text
cycle_uplink_bytes = 0
cycle_downlink_bytes = 0
cycle_started_at = now
xray_uplink baseline = 当前已知 counter
xray_downlink baseline = 当前已知 counter
```

不要修改：

- Client 流量额度
- expires_at
- Client credential
- Xray 的全局计数器
- 其他 Client

### UI

Client 继续放在 Proxy 详情体系中，不新增独立大页面作为第一选择。

Client 列表至少清晰展示：

```text
名称
状态
已用 / 总量
周期
到期时间
最近活动
操作
```

示例：

```text
PC       正常       39.7G / 100G   每月   2027-01-01   2 分钟前
iPhone   流量预警   92.1G / 100G   每月   2027-01-01   刚刚
Android  已到期      9.0G / 不限    不重置 2026-09-01   3 天前
```

查看 Client 详情使用 Modal。

详情至少展示：

```text
名称
凭据摘要
用户启用状态
实际可用状态

本周期上行
本周期下行
本周期已用 / 总量
流量周期
下次重置

到期时间
最近活动
```

操作：

```text
编辑
启用 / 禁用
重置本周期流量
删除
```

不要行内展开。

### Phase 10B 明确不做

不要实现：

- Client 小时 / 日历史流量图
- Client 实时上传 / 下载速度
- 自定义 80% / 85% / 90% 阈值
- Email / Telegram / Webhook
- Client 自动限速
- Client IP 数量限制
- 精确连接级在线用户列表
- 多 Panel / 多 Xray 实例流量聚合
- 第三方面板兼容格式

### Phase 10B 验收

至少验证：

1. 同一 Proxy 下多个 Client 的 Xray 流量可以独立识别。
2. Agent 能上报每 Client 累计 uplink / downlink。
3. Xray counter 正常增长时 delta 正确。
4. Xray 重启 / counter 下降时不会产生负流量。
5. Panel / Agent 普通重启不会丢失已累计 Client 周期流量。
6. Client 可以设置 G / T 流量额度。
7. 未设置额度时显示不限。
8. Client 可以选择 never / daily / weekly / monthly 周期。
9. monthly 29 / 30 / 31 日按当月最后一天处理。
10. 到达新周期后周期累计归零，并以当前 Xray counter 作为新 baseline。
11. Client 可以设置或清除到期时间。
12. 使用率达到 90% 显示流量预警。
13. 使用率达到 100% 显示流量已用完。
14. 流量耗尽后 Client 从 effective desired state 中失效。
15. 新周期开始后，如果 Client 用户开关仍启用且未过期，可以恢复可用。
16. Client 到期后自动从 effective desired state 中失效。
17. 修改到期时间后可以恢复可用。
18. 用户手动 `enabled = false` 不会被周期重置自动打开。
19. 手动重置本周期流量不会影响其他 Client。
20. Client 详情和列表遵守 Modal / 固定容器规则。
21. 不实现历史图、实时速度、通知、限速。
22. 实现无 3x-ui / x-ui 等第三方面板代码、schema、API 或 UI 复制痕迹。

完成 Phase 10B 后停止。
不要继续 Realm。

---

# 16. Phase 11：Realm 与 Relay

> Realm / Relay 的业务模型、desired state 和 Agent 应用流程以本文为准。
>
> Realm 列表、入口 IP、监听端口、目标 Host / IP、目标端口、Network、状态、操作列，以及详情 / 新增 / 编辑 Modal，统一参考：
>
> ```text
> vps-panel-frontend-guide.md
> ```
>
> Server / Proxy / Realm 必须保持同一套列表视觉语言和 Modal 交互，不允许 Realm 单独做成另一种页面风格。


## 目标

不再拆成“Realm 生命周期”和“Relay”两个独立大阶段。

同一个 Agent 通过 desired state 同步 Realm：

```text
Server
└── Agent
    └── Realm
        ├── Relay A
        └── Relay B
```

建议受管位置：

```text
/etc/vps-panel/realm/config.toml
vps-panel-realm.service
```

数据库增加最小：

```text
relays
```

创建 Relay 时保存：

```text
源 Server
监听地址
监听端口
目标类型
目标 Proxy 或 host:port
enabled
```

Agent 每次根据该 Server 的全部 Relay：

```text
重新生成完整 Realm config
↓
校验
↓
原子替换
↓
restart / reload
↓
健康检查
↓
失败回滚
```

不创建独立 Realm Agent。

不创建 AccessEndpoint 表。

---

# 17. Phase 12：分享与订阅

> 分享 / 订阅的数据生成规则以本文为准。
>
> `public_host` 在 Proxy 列表中的次级显示、详情 Modal 中的位置、复制按钮和节点信息密度统一参考：
>
> ```text
> vps-panel-frontend-guide.md
> ```
>
> Frontend Guide 只决定展示方式；`public_host` 优先级、Server IP 回退和 Realm 接入地址规则仍以本文为准。


到这一阶段再生成：

- VLESS URI
- SS URI
- 二维码
- Clash / Mihomo 配置
- sing-box 客户端配置
- 订阅链接

生成直连节点：

```text
Client credential
+
Proxy 协议参数
+
连接 host
+
Proxy listen_port
```

如果一个 Proxy 有多个 Client：

> 每个有效 Client 都可以基于同一个 Proxy 生成自己的直连分享配置。

不要继续把 credential 当成 Proxy 的永久单一属性。

其中连接 host 的选择规则固定为：

```text
Proxy.public_host 非空
→ 使用 public_host

Proxy.public_host 为空
→ 使用 Proxy 所在 Server 的 IP
```

例如：

```text
Server IP:
1.2.3.4

Proxy.public_host:
jp.example.com
```

原本客户端地址：

```text
1.2.3.4:443
```

自动生成：

```text
jp.example.com:443
```

这个替换只发生在：

- VLESS URI
- Shadowsocks URI
- 二维码
- Clash / Mihomo 客户端配置
- sing-box 客户端配置
- 订阅输出

不会修改服务端 Xray / Realm 实际监听配置。

Panel 不负责自动修改 DNS。

生成 Realm 接入节点：

```text
同一个 Client credential
+
同一个 Proxy 协议参数
+
Relay listen host / port
```

Realm 接入节点的地址优先使用 Relay 自己的公开接入地址 / 域名（如果未来 Relay 提供该字段），
而不是目标 Proxy 的 `public_host`。

也就是说：

```text
直连节点
→ Proxy.public_host 或 Server IP

Realm 接入节点
→ Relay 的接入 host / port
```

所以：

> Realm 接入方式是由 Proxy + Relay 派生出的客户端地址，不需要单独持久化 AccessEndpoint。

删除 Relay 只会让对应的 Realm 接入方式消失，不删除 Proxy / Client。

当前不做多跳 Chain。

---

# 18. Phase 13：完整 ZIP 备份、恢复与跨 VPS 迁移

# 21. Phase 15：完整 ZIP 备份、恢复与跨 VPS 迁移

## 目标

实现真正可用于迁移整个 Panel 的备份格式。

用户体验：

```text
旧 Panel
→ 设置 / 备份与恢复
→ 导出备份
→ 下载 vps-panel-backup-YYYYMMDD-HHMMSS.zip

新 VPS
→ 安装 VPS Panel
→ 打开初始化 / 恢复入口
→ 选择“导入备份”
→ 上传 ZIP
→ 自动校验
→ 自动恢复
→ 重启 / 重新加载
→ 原有账号、服务器、节点、Relay 等直接出现
```

核心要求：

> 用户不需要手工解压 ZIP，也不需要修改里面任何 ID、数据库、路径或关联关系。

---

## 21.1 导出 ZIP

建议备份格式至少包含：

```text
vps-panel-backup.zip
├── manifest.json
├── data/
│   └── panel.db
└── secrets/
    └── 仅在未来真正存在额外持久化密钥时加入
```

`manifest.json` 至少包含：

```json
{
  "format_version": 1,
  "panel_version": "vX.Y.Z",
  "created_at": "ISO-8601",
  "source_panel_url": "https://panel.example.com"
}
```

不要把 systemd unit、Caddy 软件包、Linux 发行版文件直接当作核心业务数据备份。

---

## 21.2 备份内容

完整备份必须覆盖所有持久化业务关系。

随着项目推进至少包括：

- users
- admin / vip role
- admin invitations 的必要持久化状态
- servers（包括到期日期、月流量额度、单向/双向模式、重置时间及当前周期累计状态）
- agent_enrollments
- agents
- Agent Token hash
- Proxy
- Client（如果已经实现）
- Relay
- Subscription
- Panel 持久化设置
- 未来数据库之外真正必需的加密密钥/secret

一般不需要恢复：

- 当前登录 Session
- 临时缓存
- WebSocket 连接
- 在线状态的内存连接对象
- 临时 Metrics 缓冲

恢复后 Agent 应重新连接并重新形成在线状态。

---

## 21.3 导入行为

导入必须：

1. 检查文件确实为 ZIP。
2. 防止 zip-slip / 路径穿越。
3. 检查压缩包大小和解压后大小上限。
4. 读取 `manifest.json`。
5. 校验 `format_version`。
6. 校验数据库文件存在且可打开。
7. 在临时目录完成所有验证。
8. 恢复前自动备份当前 Panel 数据。
9. 只有全部验证通过后才替换当前持久化数据。
10. 恢复失败时能够回滚到导入前状态。
11. 恢复后执行当前版本必要的数据库 migration。
12. 重新启动或重新加载 Panel。
13. 不要求用户手工处理数据库 ID 和外键关系。

---

## 21.3A 导入前预览与二次确认

导入 ZIP 后不能立即覆盖当前数据。

正确流程：

```text
上传 ZIP
↓
解压到临时目录
↓
完成安全与格式校验
↓
读取 manifest 和数据库摘要
↓
显示“导入预览”
↓
用户确认
↓
才真正恢复
```

预览至少显示：

```text
备份格式版本
Panel 版本
备份创建时间
来源 Panel URL

admin 数量
vip 数量
Server 数量
Proxy 数量
Client 数量（如果已实现）
Relay 数量
Subscription 数量（如果已实现）
```

对于尚未实现的资源类型可以不显示，不需要为了预览提前创建未来表。

如果 Server 数据已经支持以下信息，预览也应说明这些会一起恢复：

```text
到期日期
月流量额度
当前流量周期累计值
流量校准值
分组
标签
```

预览页面不得显示：

- 密码 hash
- Agent Token hash
- Enrollment Token
- Session Token
- Client Secret 明文
- 其他敏感 secret

在已初始化 Panel 上执行恢复时，确认区明确显示：

```text
这将覆盖当前 Panel 的持久化数据。
恢复前会自动创建回滚备份。
```

至少需要一次明确二次确认。

在“尚未初始化的新 Panel”恢复时，也要先显示预览，再确认恢复。

上传但尚未确认的备份只放在临时目录，并设置合理过期清理，不长期保留。

---

## 21.4 导入入口权限

完整导出 / 导入只允许：

```text
admin
```

`vip`：

- 不显示完整备份/恢复入口
- 调用对应 API 返回 403

原因是 ZIP 中包含整个 Panel 的账号、服务器、节点关系以及敏感持久化信息。

---

## 21.5 新 Panel 的恢复入口

为了真正做到跨 VPS 迁移，新安装 Panel 在“尚未完成初始化”状态时，可以提供：

```text
创建新管理员
或
从备份恢复
```

选择“从备份恢复”后：

- 不要求先手动创建一个新 admin。
- 从 ZIP 恢复原来的 admin / vip。
- 恢复完成后直接使用原账号登录。

如果 Panel 已经初始化，也可以在 admin 的“备份与恢复”页面导入，但必须：

- 明确提示会覆盖当前 Panel 持久化数据。
- 要求二次确认。
- 自动生成导入前备份以便回滚。

---

## 21.6 跨 VPS 与 Agent

如果迁移前后 Panel 使用同一个稳定域名：

```text
panel.example.com
```

流程应为：

```text
旧 VPS 导出
↓
新 VPS 导入
↓
DNS 指向新 VPS
↓
Agent 使用原 panel_url 自动重连
```

由于数据库中的 Agent Token hash 已恢复，而 Agent 本地仍保存原长期 Token，因此不需要重新注册 Agent。

这是推荐的零调整迁移方式。

---

## 21.7 Panel URL 变化

需要明确技术边界：

ZIP 可以自动恢复 Panel 内部数据，但 ZIP 本身无法远程修改已经安装在其他 VPS 上的：

```text
/etc/vps-panel-agent/config.json
```

因此如果迁移同时把：

```text
https://old.example.com
```

改为：

```text
https://new.example.com
```

为了继续满足“无需逐台手工调整 Agent”，后续必须提供受控的 Panel 地址迁移能力。

建议在真正实现迁移 Phase 时加入一个**专用的一次性安全操作**：

```text
update_panel_url
```

它是 desired-state 同步之外的少数例外，不因此建立通用 Task Runner。

由旧 Panel 在迁移前向在线 Agent 下发新的 Panel URL，Agent 验证后持久化。

禁止通过任意 shell 完成。

在该能力实现前：

> 完整零手工 Agent 迁移要求迁移前后保持同一个 Panel 公网域名。

这不是 ZIP 格式的限制，而是远程 Agent 必须知道新控制端地址的网络事实。

---

## 21.8 版本兼容

备份 ZIP 必须有独立：

```text
format_version
```

不要只依赖 Panel 软件版本。

规则：

- 新版 Panel 应尽量允许导入旧 backup format。
- 导入后执行 migration。
- 不支持的未来版本必须明确拒绝，不允许“尽量导入”导致半恢复状态。
- 不要求用户自己打开 SQLite 修改 schema。

---

## 21.9 安全要求

备份属于高敏感文件。

必须：

- 只有 admin 可导出。
- 只有 admin 可导入到已初始化 Panel。
- 下载响应禁止缓存。
- 临时 ZIP / 解压目录及时清理。
- 严格限制上传与解压大小。
- 防 zip bomb。
- 防路径穿越。
- 导出时不要额外把日志中的 Token 或明文密码打包进去。
- 不在日志打印备份中的 secret。
- 恢复必须原子化或具备可靠回滚。

---

## 21.10 验收

至少验证：

1. Panel A 创建 admin / vip / Server 等数据。
2. 导出 ZIP。
3. 在干净 VPS 安装 Panel B。
4. 选择从备份恢复。
5. 上传 ZIP。
6. 原 admin / vip 可以直接登录。
7. Server 关系完整。
8. 已实现的 Proxy / Client / Relay / Subscription 等关系完整。
9. ID / 外键无需人工处理。
10. 同域名迁移后 Agent 可使用原长期 Token 重连。
11. 损坏 ZIP 被拒绝且当前数据不受影响。
12. 不兼容 format version 被明确拒绝。
13. vip 无法导入/导出完整备份。
14. 恢复失败可以回滚。
15. 上传 ZIP 后先展示导入预览，不会立即覆盖数据。
16. 预览显示来源、版本和主要资源数量。
17. 预览不泄露任何 Token / password hash / secret。
18. 用户明确确认后才执行恢复。

---

# 19. Phase 14：完善

后续再考虑：

- Metrics 历史图
- Xray / Realm 日志
- Proxy 聚合流量视图（如果 Client 聚合仍不能满足）
- 更精确的 Client 当前在线连接检测
- Agent 自动更新
- Xray 自动更新
- Realm 自动更新
- Panel 地址迁移体验完善
- 操作日志
- 节点排序
- 批量操作

这些不应阻塞前面的主链路。

---

# 20. 通信协议长期原则

通信分成两类，不混在一起：

```text
WebSocket
= 在线状态、heartbeat、system_info、metrics、config_changed 通知

REST
= desired state 拉取、同步结果、以后真正需要的流量上报
```

现有 WebSocket 消息继续逐步增加。

Heartbeat：

```json
{
  "type": "heartbeat"
}
```

系统信息：

```json
{
  "type": "system_info",
  "hostname": "jp-01",
  "os_name": "Debian GNU/Linux",
  "os_version": "12",
  "kernel": "6.1.0-amd64",
  "arch": "amd64",
  "ipv4": ["203.0.113.10"],
  "ipv6": ["2001:db8::10"]
}
```

Metrics：

```json
{
  "type": "metrics",
  "payload": {}
}
```

配置变化通知：

```json
{
  "type": "config_changed",
  "version": 18
}
```

Agent 收到通知后：

```text
GET /api/agent/config
```

不要通过 WebSocket 传完整 Xray / Realm 配置。

不要设计：

```text
task
task_result
restart_core
create_proxy
delete_proxy
create_relay
```

这类通用 RPC 协议。

资源变更统一表现为：

> Panel 中 desired state 发生变化，Agent 拉取完整目标状态并收敛。

只有极少数无法通过目标状态表达的一次性安全操作，才单独增加专用消息，并逐个设计。


# 21. 数据库长期原则

# 24. 数据库长期原则

SQLite 当前足够。

账号与资源归属固定为：

```text
users.role = admin | vip

业务资源 = Panel 全局共享
```

不要给 Server / Proxy 等业务表增加 `owner_user_id` 来制造多租户模型，除非未来明确改变产品定位。


不要因为未来可能扩展就提前更换：

- PostgreSQL
- MySQL
- Redis

除非未来出现明确瓶颈或多实例部署需求。

每个 Phase 只增加当前需要的：

- 表
- 字段
- 索引

禁止创建空的未来表。

当前明确：

- `proxies` 只在 VLESS Phase 真正开始时创建。
- `clients` 在 Phase 10A 正式创建，用于一条 Proxy 下多个独立凭据。
- `client_metrics` 在 Phase 10B 创建，只保存最新 Xray baseline、当前周期累计与最近活动，不做历史时序。
- `relays` 只在 Realm Phase 真正开始时创建。
- 当前不创建 `cores` / `tasks` / `endpoints` / `chains`。

未来出现第二个真实代理后端或真正的多跳需求时，再重新评估数据模型。

---

# 22. 安全原则

## 密码

管理员密码：

```text
bcrypt hash
```

永远不要明文保存。

---

## Token

以下 Token 原始值都不应该在数据库中明文保存：

- Admin Invitation Token
- Agent Enrollment Token
- Agent Long-term Token
- Session Token（如果持久化）

数据库保存：

```text
hash(token)
```

原始值只在必要时返回一次。

---

## Agent

Agent 长期 Token：

- 本机配置文件权限 0600
- 不输出到 journal
- 不暴露在 URL query 中
- 不打印到错误信息
- 不通过管理 UI 再次显示

---

## 配置同步与远程控制

代理配置以 desired state 同步为主。

Agent：

- 只管理 VPS Panel 自己拥有的配置文件 / systemd unit
- 完整生成 candidate config
- 配置校验通过后才替换
- 原子替换
- 重启 / 重载后做健康检查
- 失败自动回滚
- 真正应用完成后才回报 success

禁止任意命令执行 API。

不要为了资源增删改建立通用 Task Runner。

---

# 23. 部署原则

Panel 默认：

```text
GitHub Release
↓
预编译 binary + web/
↓
systemd
```

不以 Docker 为默认。

Agent：

```text
GitHub Release binary
↓
/usr/local/bin/vps-panel-agent
↓
systemd
```

支持：

- amd64
- arm64

用户机器不应该为了运行 Panel / Agent 安装：

- Go
- Node.js
- npm

---

# 24. 前端原则

前端第一目标仍然是：

```text
清晰
稳定
信息密度合理
不阻塞功能开发
```

详细 UI 规范不在本文重复维护，统一参考：

```text
vps-panel-frontend-guide.md
```

本文只保留以下总原则：

- 不提前引入 Vue Router / Pinia / 新 UI Framework。
- 不为了单个 Phase 重做导航和页面骨架。
- Server / Proxy / Realm 使用统一列表风格。
- Sidebar、主内容、列表容器位置保持稳定。
- 查看详情统一使用 Modal。
- 新增 / 编辑优先使用 Modal。
- 只有真实页面开始增长时才做最小组件拆分。
- 不做复杂 Dashboard、地图、过度动画或大型设计系统。
- Komari 只可参考空间利用和信息密度，不复制代码、品牌、配色或页面实现。

如果 Codex 任务涉及任何前端改动：

```text
先读本文相关 Phase
↓
再读 vps-panel-frontend-guide.md
↓
只实现当前 Phase 对应 UI
```

---

# 25. Codex 开发规则

## 第三方代理面板代码参考禁令（硬性）

Codex 在实现 Xray / VLESS / XTLS / Shadowsocks / Realm / Subscription 等功能时：

- 可以阅读第三方开源面板了解功能。
- 可以总结其设计优缺点。
- 可以借鉴抽象层级、错误处理顺序、配置验证思路等高层结构。
- 不得复制源代码。
- 不得逐函数翻译。
- 不得在复制代码后仅修改变量名 / 文件名 / 注释。
- 不得复刻第三方面板 API / 数据库 / 前端表单。
- 不得把第三方面板特征字符串带入 VPS Panel。

如需要确认协议字段，优先查官方文档 / 官方示例 / 上游源码。

如果第三方面板代码是唯一参考来源且无法独立确认行为：

> 先停止该细节实现，记录待确认点；不要直接抄第三方实现。

每次涉及第三方面板研究时，完成报告需要说明：

```text
参考了哪些“功能/行为”
哪些实现是 VPS Panel 独立设计
确认没有复制第三方代码/模板/品牌特征
```


每次给 Codex 一个 Phase 时，必须遵守以下原则。

## 28.1 先读 AGENTS.md

每个任务开头：

```text
开始前阅读根目录 AGENTS.md，并严格遵守最小实现原则。
```

---

## 28.2 当前 Phase 是最高优先级边界

如果某项代码：

> 不直接服务于当前 Phase 的验收条件，就不要写。

---

## 28.3 禁止未来脚手架

不要提前创建未来后端脚手架。

例如：

- Phase 8A 只做 config API 时，不创建 Xray / Realm renderer。
- 真正进入 Xray Phase 时再创建最小 Xray 管理代码。
- 真正进入 Realm Phase 时再创建最小 Realm 管理代码。
- 不提前创建 sing-box / Mihomo adapter。

除非当前 Phase 已经真正开始实现该功能。

---

## 28.4 不做无关重构

例如实现 Heartbeat 时：

不要顺手：

- 重写 Auth
- 换 Router
- 换 ORM
- 改 CSS 框架
- 改部署
- 拆 Repository Layer
- 引入 Event Bus

---

## 28.5 接口只在真实需要时创建

不要因为“以后可能有多个代理后端”就先创建：

```go
type Backend interface {}
```

第一版直接把 Xray 做好。

真正出现第二个真实实现并且重复代码已经明确时，再抽象接口。

---

## 28.6 控制依赖

新增依赖必须说明：

```text
当前功能为什么需要它
```

WebSocket 可以引入成熟轻量库。

普通工具函数不要引入一整套框架。

---

## 28.7 每阶段结束必须停止

Codex 指令最后应明确：

```text
完成当前 Phase 后停止。
不要继续下一阶段。
```

---

# 26. 推荐实际开发顺序

> 从 Phase 7A 开始，只要某个 Phase 涉及 Server / Proxy / Realm / Subscription UI，就同时执行对应的 Frontend Guide 规范。
>
> Frontend Guide 不单独占用一个 Phase，它是所有前端相关 Phase 的横向约束。


从当前版本开始：

```text
Phase 4.5
admin / vip + 全局共享资源
        ↓
Phase 4.6
Agent 重装 / 凭据重置
        ↓
Phase 5A
WebSocket 认证连接
        ↓
Phase 5B
Heartbeat + 自动重连
        ↓
Phase 6A
静态系统信息 + Server 到期日期
        ↓
Phase 6B
CPU / RAM / Disk / Uptime
        ↓
Phase 7A
机器网卡累计流量 + 月流量额度 / 重置
        ↓
Phase 7B
服务器分组 / 标签 / 筛选（暂缓，不阻塞代理主链路）
        ↓
第一阶段服务器管理 MVP
        ↓
Phase 8A
通用 Agent desired-state API
        ↓
Phase 8B
Xray 托管基础 + 安全配置应用
        ↓
Phase 9A
VLESS + TCP + TLS / REALITY + XTLS Vision（含 client udp443）
        ↓
Phase 9B
Shadowsocks Proxy
        ↓
Phase 10A
Client 基础管理 / 多凭据
        ↓
Phase 10B
Client 流量 / 额度 / 周期 / 到期
        ↓
Phase 11
Realm + Relay
        ↓
Phase 12
订阅 / 分享
        ↓
Phase 13
完整 ZIP 备份 / 恢复 / 跨 VPS 迁移
        ↓
Phase 14
完善功能
```

当前路线明确不单独安排：

```text
CoreInstance Phase
AccessEndpoint Phase
Chain Phase
通用 Task System Phase
```

如果以后真实需求出现，再增加，不提前占用当前路线。


# 27. 当前下一步

Phase 4.5、Phase 4.6、Phase 5A、Phase 5B、Phase 6A、Phase 6B、Phase 7A、Phase 8A 和 Phase 8B 已完成。

Phase 7B 服务器分组、标签与筛选暂缓，不阻塞代理主链路。代理主链路下一步为：

```text
Phase 9A：VLESS + TCP + TLS / REALITY + XTLS Vision
```

当前 Phase 8B 只完成 Agent 的 Xray 安全托管能力，仍不能从 Panel 创建真实代理节点；真实节点需在 Phase 9A VLESS 完成后才可用。

完整 ZIP 备份 / 导入已经列为固定需求，但实际实现放在 Proxy / Relay 等核心业务数据模型基本稳定后的 Phase 13，避免当前每新增一张业务表就反复重写备份格式。

---

# 28. Phase 模板

以后每个新 Phase 的 Codex 指令最好按下面结构写：

```text
开始 Phase X。

开始前：
- 阅读 AGENTS.md。
- 阅读当前 Phase。
- 阅读与当前功能直接相关的已有代码。
- 如果本 Phase 修改前端，阅读 `vps-panel-frontend-guide.md`。
- 严格最小实现。

当前已有：
- ...

本阶段目标：
- ...

需要实现：
1. ...
2. ...
3. ...

数据库：
- ...

Agent：
- ...

Panel：
- ...

前端：
- ...
- 如果修改 UI，遵守 `vps-panel-frontend-guide.md`，不要重做页面骨架。

安全：
- ...

明确不要实现：
- ...

测试：
1. ...
2. ...

完成后：
- gofmt
- go test
- go build
- npm build（如果修改前端）

完成 Phase X 后停止。
不要继续 Phase X+1。
```

---

# 29. 最终架构图

```text
Browser
   │
   ▼
Panel
├── Accounts
│   ├── admin
│   └── vip
├── Shared Resources
├── Backup / Restore
├── Server Management
├── Agent Connections
├── Metrics
├── Proxy Management
├── Client Management
├── Relay Management
└── Subscription
   │
   ├── REST: desired state / sync result
   └── WebSocket: heartbeat / metrics / config_changed
              │
              ▼
         Server Agent
         ├── System
         ├── Network Metrics
         ├── Config Sync
         ├── Xray Manager
         └── Realm Manager
              │
              ▼
         Linux Server
```

业务关系：

```text
Server
├── Agent
├── Proxy
│   └── Client
└── Relay
    └── target = Proxy 或 host:port
```

固定原则再次强调：

> 一台 Server 一个统一 Agent。

> 第一版代理后端固定 Xray；VLESS / Shadowsocks 都由同一个 Xray 管理。

> VLESS 第一版固定 `TCP + XTLS Vision`；安全层由用户在 `TLS / REALITY` 中二选一。两种模式的服务端 flow 都使用 `xtls-rprx-vision`，客户端都可以选择 `xtls-rprx-vision-udp443`。

> 所有代理能力必须原创实现；第三方面板只能用于功能研究和高层工程思路参考，不得复制代码、模板、API、Schema、UI 或品牌特征。

> Panel 不直接操作 `/etc/xray/config.json`，只保存业务目标状态。

> Agent 通过 REST 拉取完整 desired state；WebSocket 只负责实时状态和 `config_changed` 通知。

> Agent 只管理 VPS Panel 自己拥有的 Xray / Realm 配置和 systemd unit，不自动接管第三方配置。

> 配置更新必须经过“生成 → 校验 → 原子替换 → 重启/重载 → 健康检查 → 失败回滚”。

> Proxy 直接属于 Server；当前不需要 CoreInstance。

> Realm Relay 不复制 Proxy。

> Realm 接入地址由 Proxy + Relay 在分享/订阅时派生；当前不需要 AccessEndpoint 表。

> Proxy 可以配置可选 `public_host`；直连节点分享时优先使用该域名，未设置时回退到 Server IP。该字段只影响客户端连接地址，不修改服务端监听、SNI、REALITY 参数或 DNS。

> 当前不做 Chain；多跳等出现真实需求以后再设计。

> Client 固定属于 Proxy；Phase 10A 建立多 Client 模型，Phase 10B 使用 Xray per-client stats 实现独立流量、额度、周期和到期控制。

> Server 总流量与 Client 流量是两套独立统计：Server 读取 Linux 网卡；Client 读取 Xray per-client stats。

> Client 的 `enabled` 是用户开关；到期和流量耗尽通过 `effective_enabled` 派生，不永久覆盖用户开关。

> 第一个初始化账号是唯一 admin；邀请注册账号统一为 vip。

> vip 不能继续邀请注册账号。

> Server / Proxy / Relay 等业务资源属于整个 Panel，admin / vip 读取同一份共享资源，不做资源副本同步。

> 完整备份必须导出为 ZIP，并支持在新 VPS 的 Panel 中直接导入恢复，不要求手工改 ID、外键或 ZIP 内容。

> Panel 默认业务时区固定为 `Asia/Shanghai`；Server 到期、流量重置和周期显示都按上海时区。

> Agent 的机器流量功能只读取并上报网卡累计 RX / TX；不执行 Speedtest / iperf，不计算实时 RX/s / TX/s。

> 机器累计流量复用现有 `metrics` WebSocket；第一版不增加独立 traffic API、实时速度采样器或流量历史表。

> Server 支持轻量分组、标签和筛选。

> 已注册 Server 必须支持安全地重装 Agent / 轮换长期凭据。

> 每个 Phase 只写当前需要的功能。

> 所有前端相关 Phase 统一参考 `vps-panel-frontend-guide.md`；Sidebar、宽列表、固定容器、详情 Modal 是跨 Phase 的稳定前端约束。


# 30. 修改记录区

后续如果架构发生明确变化，在这里记录。

## 当前固定决策（勾选表示设计已确定，不代表对应功能已经实现）

- [x] Panel 使用 Go。
- [x] SQLite。
- [x] Vue 3 + TypeScript + Vite。
- [x] Panel 默认 Native + systemd。
- [x] Agent Go 二进制 + systemd。
- [x] amd64 / arm64。
- [x] 邀请注册制账号体系。
- [x] 第一个初始化账号是唯一 admin。
- [x] 邀请注册账号统一为 vip。
- [x] 只有 admin 可以创建/查看/撤销邀请。
- [x] Server / Proxy / Relay 等业务资源为 Panel 全局共享资源。
- [x] 不做按用户复制资源或资源同步副本。
- [x] 一台 Server 一个 Agent。
- [x] Agent 主动连接 Panel。
- [x] 不以 SSH 作为日常控制通道。
- [x] 不开放任意 Shell API。
- [x] Proxy 直接属于 Server。
- [x] Client 属于 Proxy；同一 Proxy 可以拥有多个独立凭据 Client。
- [x] Phase 10A 正式建立 Client，多 Client 共享同一个 Proxy listener / 协议参数，不为每 Client 新建端口或 Xray 进程。
- [x] Phase 10B 的 Client 流量来自 Xray per-client stats，不使用 Linux 网卡区分 Client。
- [x] Client 支持独立流量额度，输入单位默认 G、可选 T，内部统一保存 bytes。
- [x] Client 流量周期第一版支持 never / daily / weekly / monthly，不做 hourly。
- [x] Client 支持独立到期时间。
- [x] Client 流量达到 90% 显示预警，达到 100% 视为 quota_exhausted。
- [x] Client 到期或流量耗尽后从 effective desired state 中失效，但不永久覆盖用户 `enabled` 开关。
- [x] Client 周期重置后，如果用户仍启用且未到期，可自动恢复可用。
- [x] Client 支持手动重置本周期流量，不修改 credential / quota / expiry / 其他 Client。
- [x] Client 第一版只保存当前周期累计，不保存小时 / 日历史流量。
- [x] Realm Relay 不复制 Proxy。
- [x] 第一版代理后端固定为 Xray。
- [x] VLESS / Shadowsocks 第一版都由同一个 Xray 承载。
- [x] VLESS 第一版固定 TCP + XTLS Vision。
- [x] VLESS 第一版安全层允许 TLS / REALITY 二选一。
- [x] 第一版同时支持 `VLESS + TCP + TLS + XTLS Vision` 和 `VLESS + TCP + REALITY + XTLS Vision`。
- [x] TLS / REALITY 两种模式的服务端 flow 都固定为 `xtls-rprx-vision`。
- [x] TLS / REALITY 两种模式的客户端第一版都支持 `xtls-rprx-vision-udp443`，该选项不改变服务端 listener / flow。
- [x] 第三方代理面板只能用于功能和高层代码结构参考，禁止复制代码、模板、API、数据库、UI 或品牌特征。
- [x] 外部分享 / 订阅 / 协议配置不主动加入任何面板品牌标识。
- [x] Panel ↔ Agent 的代理配置采用 desired-state REST 同步。
- [x] WebSocket 只负责 heartbeat / metrics / config_changed 等实时通知，不传整份配置。
- [x] Agent 只管理 VPS Panel 自己拥有的 Xray / Realm 配置。
- [x] 配置应用必须校验、原子替换、健康检查并支持失败回滚。
- [x] 当前不建立 CoreInstance / AccessEndpoint / Chain 作为固定业务模型。
- [x] Realm 接入地址由 Proxy + Relay 在分享/订阅时派生。
- [x] Proxy 支持可选 `public_host`；分享 / 订阅时优先使用该域名，否则回退到 Server IP。
- [x] 前端相关 Phase 统一受 `vps-panel-frontend-guide.md` 约束；Server / Proxy / Realm 使用固定 Sidebar、宽列表和详情 Modal，不允许各 Phase 自行重做页面骨架。
- [x] 必须支持完整 ZIP 导出与导入恢复。
- [x] 新 VPS 导入备份后，Panel 内部数据与关联关系不需要人工调整。
- [x] 同域名跨 VPS 迁移时，Agent 应使用原长期凭据自动重连。
- [x] Server 可设置到期日期。
- [x] Server 可设置月流量总额。
- [x] 月流量支持单向 / 双向统计模式。
- [x] 单向流量初版按服务器出口 TX 计费。
- [x] 双向流量按 RX + TX 计费。
- [x] 每台 Server 可独立设置每月流量重置日和时间。
- [x] 服务器总流量直接从机器网卡计数，不依赖代理内核或 Realm 流量统计。
- [x] Agent 流量功能只读取累计 RX / TX，不进行 Speedtest / iperf 等主动测速。
- [x] 第一版不计算或展示实时 RX/s / TX/s。
- [x] 网卡累计 RX / TX 复用现有 `metrics` WebSocket 上报，不增加独立 Traffic API。
- [x] 第一版不保存流量历史时序，只保存最新网卡 baseline 与当前周期累计值。
- [x] Server 月流量额度输入默认单位为 G、可选 T，内部统一保存 bytes。
- [x] Server 支持手动校准当前周期已用流量，使用 `traffic_adjustment_bytes`，不修改原始 RX / TX。
- [x] Server 使用率达到 90% 显示“流量预警”，达到 100% 显示“流量已用完”；预警只做 UI 派生状态。
- [x] Server 流量超额不自动断网、停 Agent、停 Xray 或限速。
- [x] 第一版不做自定义预警阈值、外部通知或概览流量汇总。
- [x] Server 信息必须以类似 `100G（已用）/500G（总）` 的形式显示月流量。
- [x] Panel 默认业务时区固定为 `Asia/Shanghai`。
- [x] 每月重置日不存在时按当月最后一天执行。
- [x] 已注册 Server 支持重新安装 Agent 和长期凭据轮换。
- [x] Server 支持轻量分组和多个标签。
- [x] ZIP 导入必须先显示预览，用户确认后才执行覆盖恢复。

## 待后续决定

- [ ] Phase 6 系统信息最终表结构。
- [ ] Metrics 是否保留历史以及保留周期。
- [ ] VLESS TLS / REALITY 第一版除已固定的 TCP / XTLS Vision 和安全层二选一外，哪些高级 TLS / REALITY 参数需要开放给 UI。
- [ ] Shadowsocks 第一版支持哪些 method。
- [ ] Realm 配置采用单进程多规则还是其他最小实现。
- [ ] Subscription 输出格式与权限机制。
- [ ] 是否以及何时需要第二个代理后端（sing-box / Mihomo）；有真实需求再决定。
- [ ] 是否以及何时需要多跳模型；有真实需求再决定。
- [ ] vip 对业务资源的最终增删改边界（当前至少共享查看/使用，不做资源隔离）。
- [ ] Phase 13 Backup ZIP 的最终 manifest 字段。
- [ ] 跨域名迁移时 `update_panel_url` 的确认与安全机制。

这些内容应在真正进入对应 Phase 时再决定，不提前实现。


---

## 2026-09-11 VLESS TLS / REALITY + XTLS 第一版与原创实现硬性约束

在简化代理架构基础上追加固定要求：

1. VLESS 第一版固定 `TCP + XTLS Vision`。
2. 安全层由用户在 `TLS / REALITY` 中二选一。
3. 第一版必须同时支持：
   ```text
   VLESS + TCP + TLS + XTLS Vision
   VLESS + TCP + REALITY + XTLS Vision
   ```
4. TLS / REALITY 两种模式的服务端 Client flow 都固定为 `xtls-rprx-vision`。
5. 两种模式的客户端第一版都支持 `xtls-rprx-vision` 与 `xtls-rprx-vision-udp443`。
6. `xtls-rprx-vision-udp443` 仅作为客户端 UDP/443 / QUIC 行为选项，不增加服务端 UDP/443 listener，不改变服务端 flow。
7. 第一版不支持 `Flow = none`，也不做任意 Transport / Security / Flow 组合器。
8. TLS 与 REALITY 使用条件字段分支校验，不把两套参数混成一套必填表单。
9. 代理协议实现优先依据 Xray 官方文档、官方示例和上游源码。
10. 允许研究 3x-ui、x-ui、Xboard、Marzban、V2Board 等代理面板的功能和高层工程思路。
11. 严禁复制或近似复刻第三方面板源代码、配置模板、API、数据库 schema、安装脚本、systemd unit、前端 UI、注释、错误文案和命名体系。
12. 不允许出现“改变量名后提交”的第三方代码搬运。
13. 分享链接、订阅配置和协议必要字段中不主动加入 3x-ui / x-ui / VPS Panel 等面板品牌特征。
14. 第三方实现与官方协议行为冲突时，以官方协议 / 上游实现为准。

---

## 2026-09-11 简化代理管理架构

本次覆盖此前较重的 Core / Endpoint / Chain 设计：

1. 第一版不创建 `CoreInstance`，Proxy 直接属于 Server。
2. 第一版代理后端固定为 Xray，优先支持 VLESS（TCP + TLS / REALITY + XTLS Vision）和 Shadowsocks。
3. Panel 与 Agent 使用通用的 desired-state API：REST 拉配置 / 回报结果，WebSocket 只发 `config_changed` 通知。
4. 不为 Proxy / Relay 增删改建立通用 Task Runner。
5. Agent 每次根据完整 desired state 重新生成 VPS Panel 自己拥有的 Xray / Realm 配置。
6. Xray / Realm 配置必须先校验，再原子替换；重启或健康检查失败必须自动回滚。
7. 第一版不自动接管或合并用户已有的第三方 Xray / Realm 配置。
8. Realm 与 Relay 合并为一个开发阶段；Relay 可指向已有 Proxy 或手工 host:port。
9. 第一版不创建 `AccessEndpoint` 表；Realm 接入节点由 Proxy + Relay 在分享 / 订阅阶段动态组合。
10. 当前不实现 `Chain`；多跳出现真实需求后再设计。
11. sing-box / Mihomo 不作为当前固定路线，只有出现第二个真实后端需求时才增加，并优先复用现有 Agent API，而不是提前建立插件框架。
12. 完整 ZIP 备份调整到 Phase 13；完善功能调整到 Phase 14。

---

## 2026-09-10 新增固定需求

本次新增并覆盖旧设计：

1. 首次初始化创建的第一个账号为唯一 `admin`。
2. 通过邀请注册的账号统一为 `vip`。
3. `vip` 不具有邀请其他账号注册的权限。
4. Server、Proxy、Relay 等业务资源不按账号隔离，属于 Panel 全局共享资源。
5. admin 新增的服务器和节点会直接出现在 vip 的共享资源视图中，不通过复制或同步副本实现。
6. Panel 必须支持完整 ZIP 导出和导入。
7. ZIP 需要能够用于另一台 VPS 上的新 Panel 恢复，内部 ID、外键、节点关系等由程序自动处理，不要求人工修改。
8. 完整导入/导出属于 admin 专属能力。
9. 同一 Panel 域名迁移到新 VPS 时，恢复 Agent Token hash 后，Agent 应能够继续使用原长期 Token 自动重连。
10. 如果 Panel 域名也变化，则后续通过专用的 `update_panel_url` 安全操作解决；这是少数一次性操作，不建立通用 Task Runner，且禁止任意 shell。


## 2026-09-10 新增 Server 到期与月流量要求

新增固定需求：

1. 每台 Server 可以设置独立到期日期。
2. 每台 Server 可以设置月流量总额。
3. 月流量统计模式分为：
   - 单向：按服务器出口 `TX` 统计。
   - 双向：按 `RX + TX` 统计。
4. 每台 Server 可以设置独立的每月流量重置日和具体时间。
5. 月流量直接读取机器网卡计数器，不依赖 Xray、sing-box、Mihomo、Realm、Proxy 或 Client 的应用层流量。
6. 网卡计数采用增量累计，必须正确处理 VPS 重启、Agent 重启以及网卡计数器归零。
7. 本周期累计数据需要持久化，不能因服务重启丢失。
8. 到达流量重置时间时，重置 Panel 记录的本周期累计值并建立新的网卡基线，不修改 Linux 内核网卡计数器。
9. Server 信息中必须显示类似：
   `100G（已用）/500G（总）`
10. 未设置总额时可显示：
    `100G（已用）/不限（总）`
11. 当前流量超额只展示统计结果，不自动停机、断网、限速或停止代理服务。
12. 当前服务器到期只负责配置与展示，不自动删除、关机或停节点。
13. Agent 不进行主动测速，也不计算实时下载 / 上传速度。
14. 机器累计流量复用现有 `metrics` WebSocket 上报，不新增独立 traffic API。
15. 第一版仍不保存流量历史时序；人工校准与固定 90% / 100% UI 预警已在 2026-09-12 重新纳入。


## 2026-09-12 Server 流量额度单位、校准与预警

在 Phase 7A 基础流量统计之上追加：

1. 月流量额度输入采用“数值 + 单位”形式。
2. 单位默认 G，可选 T。
3. 后端只保存 bytes，不保存 display unit。
4. Server 支持手动校准当前周期已用流量。
5. 校准使用 `traffic_adjustment_bytes` 偏移，不修改原始 `cycle_rx_bytes / cycle_tx_bytes`。
6. 新周期开始时自动清除 adjustment。
7. 使用率达到 90% 显示“流量预警”。
8. 使用率达到或超过 100% 显示“流量已用完”。
9. 预警不修改 Server online / offline / pending。
10. 不自动断网、停 Agent、停 Xray、限速。
11. 第一版不支持自定义阈值和外部通知。

---

## 2026-09-12 Client 管理与 per-client 流量设计

正式将 Client 从“可能以后需要”提升为固定后续阶段：

```text
Phase 10A
Client 基础管理 / 多凭据

Phase 10B
Client 流量 / 额度 / 周期 / 到期
```

固定要求：

1. Client 属于 Proxy，一个 Proxy 可以有多个 Client。
2. Client 不新建 Proxy listener、端口、Agent 或 Xray 进程。
3. Phase 10A 将已有单凭据 Proxy 安全迁移为默认 Client，保留原 credential。
4. Phase 10B 使用 Xray 官方 per-client stats 能力统计独立 uplink / downlink。
5. Agent 通过专用 `POST /api/agent/traffic` 上报应用层累计流量；机器 `metrics` WebSocket 仍只负责 Server 网卡统计。
6. Panel 对 Xray cumulative counter 计算 delta，并处理 Xray 重启 / counter 归零。
7. Client 支持独立流量额度，默认单位 G、可选 T。
8. Client 周期第一版支持 never / daily / weekly / monthly；不做 hourly。
9. 周期时间统一按 `Asia/Shanghai`。
10. Client 支持独立到期时间。
11. `enabled` 是用户手动开关；`expired / quota_exhausted / effective_enabled` 为派生状态。
12. 流量耗尽或到期后 Client 从有效 desired state 中失效，不永久修改用户开关。
13. 周期重置后，如果用户仍启用且未到期，可自动恢复。
14. Client 支持手动重置本周期流量。
15. 第一版显示最近活动时间，不把“最近有流量”伪装成精确在线连接状态。
16. 90% 显示流量预警，100% 显示流量已用完。
17. 不做 Client 历史流量图、实时速度、自定义阈值、通知、限速或精确连接级在线检测。
18. 可以参考 3x-ui 等第三方面板的功能清单与用户需求，但禁止复制代码、数据库、API、字段体系、配置模板或 UI。

---

## 2026-09-11 总 Guide 与 Frontend Guide 联动

新增固定文档职责：

1. `vps-panel-development-guide` 继续作为总开发 Guide，负责业务模型、Agent、API、数据库、Phase、协议与安全边界。
2. `vps-panel-frontend-guide.md` 负责 Sidebar、主内容宽度、Server / Proxy / Realm 列表、Modal、搜索筛选、容器稳定性与响应式。
3. 所有涉及前端的 Phase 都必须同时阅读 Frontend Guide。
4. Server / Proxy / Realm 的页面骨架不再在每个 Phase 中重复设计。
5. “查看详情统一 Modal”“主列表宽且稳定”“Sidebar 贴左固定”成为跨 Phase 的前端硬性约束。
6. Proxy Phase 必须参考 Frontend Guide 中入口 IP、出口 IP、端口、协议、传输、安全层、流控等列表字段规范。
7. Realm Phase 必须参考 Frontend Guide 中入口 IP、监听端口、目标 Host / IP、目标端口、Network 等列表字段规范。
8. `public_host` 的业务规则由总 Guide 决定；其列表 / Modal 展示由 Frontend Guide 决定。
9. 两份 Guide 冲突时：业务 / 数据 / 协议 / Agent / API / 安全以总 Guide 为准；视觉布局 / Modal / 表格结构以前端 Guide 为准。

---

## 2026-09-11 Proxy 可选节点域名

新增固定要求：

1. Proxy 增加可选 `public_host` 字段。
2. UI 显示为“节点域名（可选）”或等价简洁文案。
3. 用户填写已关联域名后，分享链接 / 订阅 / 二维码 / 客户端配置中的连接地址自动由 Server IP 替换为该域名。
4. `public_host` 为空时，自动回退到 Server IP。
5. `public_host` 只影响客户端连接 host，不修改 Xray listener、Server IP、TLS / REALITY SNI、REALITY dest / target 或 DNS。
6. Panel 不负责自动创建、修改或验证 DNS 解析。
7. VLESS TLS、VLESS REALITY、Shadowsocks 等直连 Proxy 统一复用同一地址选择逻辑。
8. Realm 接入节点使用 Relay 自己的接入 host / port，不使用目标 Proxy 的 `public_host`。

---

## 2026-09-11 简化机器流量统计要求

> 历史记录：本节当时曾移除人工校准和阈值预警。
>
> 其中“人工校准 / 90% 与 100% UI 预警”已在 2026-09-12 的新决定中重新纳入；实时速度、历史时序和主动测速仍保持不做。

本次覆盖此前较重的实时速度、人工校准和预警设计：

1. Agent 只读取 Linux 主网卡累计 `RX / TX` 字节数。
2. Agent 不执行 Speedtest、iperf 或任何主动带宽测速。
3. 第一版不计算或展示实时下载 / 上传速度，也不计算 `RX/s`、`TX/s`。
4. 累计 RX / TX 直接复用现有 `metrics` WebSocket 消息上报，不新增独立 traffic API / WebSocket。
5. Panel 根据连续累计计数的 delta 维护当前月周期 `cycle_rx_bytes / cycle_tx_bytes`。
6. 网卡计数器下降时视为重启 / 重建，对应方向本次 delta 记 0，并重新建立 baseline。
7. 当前周期状态持久化，Panel / Agent 普通重启不能无条件清零。
8. 月流量单向模式按 TX；双向模式按 RX + TX。
9. 月流量重置按 `Asia/Shanghai` 计算；新周期在下一次 metrics 上报时开启，不增加专用 scheduler。
10. 第一版不建立流量历史时序表、小时 / 日流量表或历史图。
11. 【已被 2026-09-12 部分覆盖】当时不实现人工流量校准和阈值预警；现已重新加入人工校准、固定 90% / 100% UI 预警，但仍不做自定义阈值与概览流量汇总。
12. Server 列表保留明确的 `已用 / 总量`；详情展示本周期 RX / TX、统计方式、重置时间和周期开始时间。
13. 流量超额只展示数据，不自动断网、停 Agent、停 Xray、限速或修改防火墙。
14. 原 Phase 7B“实时上下行速度”删除；原 Phase 7B“服务器分组、标签与筛选”顺延为 Phase 7B。
