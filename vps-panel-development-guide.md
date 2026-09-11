# VPS Panel 开发指导与阶段路线图

> 项目：`renaissance0721/vps-panel`  
> 文档定位：长期开发指导文档，作为后续 Codex / 人工开发时的阶段边界、架构约束和验收依据。  
> 当前基线：Phase 1–4、Phase 4.5、Phase 4.6 和 Phase 5A 已完成；下一步进入 Phase 5B。
> 语言：简体中文。  
> 原则：每个 Phase 只实现当前验收条件真正需要的功能，不提前堆未来架构。

---

# 0. 项目最终目标

VPS Panel 的目标不是单纯做一个“探针面板”，而是做一个统一的多 VPS 管理面板。

最终希望实现：

- 多 VPS 统一管理
- 每台 VPS 安装一个统一 Agent
- 服务器状态与系统监控
- 实时上下行速度与累计流量
- 可设置服务器到期时间
- 可设置月流量额度、单向/双向计费方式和每月流量重置时间
- 服务器信息中显示本周期已用流量 / 总流量
- Xray / sing-box / Mihomo 等代理内核管理
- VLESS / Shadowsocks 等代理节点管理
- Proxy 下的 Client 管理
- Realm 端口转发管理
- AccessEndpoint 接入点管理
- 多跳 Chain 管理
- 节点分享、订阅、二维码
- `admin / vip` 两级账号体系
- Server / Proxy / Relay 等业务资源在账号之间全局共享
- ZIP 一键导出、导入、跨 VPS 恢复
- 后续配置、更新、备份、日志等维护能力

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
  │           ├── Xray
  │           ├── sing-box
  │           ├── Mihomo
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
- 安装 Xray
- 修改 sing-box 配置
- 创建 Realm 转发
- 启停服务
- 查询版本

都由这一个 Agent 完成。

---

## 1.3 Agent 不是 SSH 替代器，也不是任意 Shell 网关

Panel 不应该依赖 SSH 自动登录所有 VPS。

正常控制链路：

```text
浏览器
  ↓
Panel
  ↓
WebSocket
  ↓
Agent
  ↓
结构化任务
```

严禁实现类似：

```text
POST /api/agent/exec
{
  "command": "任意 shell"
}
```

不能向 Panel 暴露通用任意命令执行能力。

后续应该实现结构化 Action，例如：

```text
install_core
update_core
restart_core
create_proxy
update_proxy
delete_proxy
create_relay
delete_relay
```

Agent 可以在内部执行必要的系统命令，但 Panel 与 Agent 的协议应是受控、结构化的。

---

## 1.4 账号固定为 admin / vip 两级

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

## 1.5 Server、节点和后续业务资源属于 Panel 全局资源池

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
    ├── CoreInstance
    ├── Proxy
    ├── Client
    ├── Relay
    ├── AccessEndpoint
    └── Chain
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

## 1.6 Panel 必须支持完整 ZIP 备份、导入和跨 VPS 恢复

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
- CoreInstance
- Proxy
- Client
- Relay
- AccessEndpoint
- Chain
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

Server 还需要保存由用户配置的服务器运营信息，包括：

- 到期时间
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

服务器到期时间允许为空，表示“不设置到期时间”。

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

- Server 到期时间
- 月流量重置时间
- 当前流量周期起止时间
- 到期预警天数计算
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

## 2.3 CoreInstance

未来同一台 Server 上可能运行：

```text
Xray
sing-box
Mihomo
```

CoreInstance 表示某个代理核心的实际运行实例。

不要把 Core 和 Proxy 混成同一个概念。

---

## 2.4 Proxy

Proxy 表示真正的代理入站 / 节点配置，例如：

```text
VLESS Reality :443
Shadowsocks :8388
Hysteria2 :8443
```

Proxy 属于某个 CoreInstance，也运行于某台 Server。

---

## 2.5 Client

Client 是 Proxy 下的用户、设备或凭据。

例如：

```text
VLESS Reality
├── PC
├── iPhone
└── Android
```

Client 应属于 Proxy。

Client 不属于 Relay，也不属于 AccessEndpoint。

---

## 2.6 Relay

Relay 表示 Realm 等中转规则。

例如：

```text
JP Server :9502
    ↓
US Home Proxy :443
```

Realm 在“源 Server”上执行。

因此 Relay 必须知道：

- source_server_id
- listen_address
- listen_port
- target

目标不能只记录 `target_server_id`。

因为一台 Server 上以后可能有多个 Proxy / 服务端口。

---

## 2.7 AccessEndpoint

这是后续非常关键的概念。

假设美国落地 Proxy：

```text
10.20.30.40:443
```

然后有：

```text
日本 Realm：
211.x.x.x:9502 → 10.20.30.40:443

香港 Realm：
45.x.x.x:9503 → 10.20.30.40:443
```

这仍然只有 **一个 Proxy**。

只是有多个接入方式：

```text
Proxy
├── Direct Endpoint
├── JP Relay Endpoint
└── HK Relay Endpoint
```

Realm 转发不能复制一个新的 Proxy 数据记录。

否则会出现：

- UUID 重复维护
- Reality 参数重复维护
- Client 重复
- 配置同步困难
- 删除 Relay 时难以判断应该删除什么

所以：

> Realm 创建的是新的 AccessEndpoint，不是新的 Proxy。

---

## 2.8 Chain

Chain 表示未来多跳路径。

例如：

```text
中国用户
  ↓
日本入口
  ↓
西雅图
  ↓
洛杉矶家宽
```

Chain 属于后续高级功能。

当前阶段不要提前实现 Chain 表、Chain Builder 或复杂拓扑编辑器。

---

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

以后 Core / Proxy / Client / Relay / Endpoint / Chain 也沿用同一原则。

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

`agent_enrollments.purpose` 只使用 `initial` 和 `rebind` 两种内部值。统一的 `POST /api/servers/{id}/enrollment` 会自动判断用途：当前存在 Agent，或历史上存在已使用的 Enrollment 时生成 `rebind`；两者都不存在时生成 `initial`，不依赖 Server 当前状态。`initial` 仅允许本机没有 Agent 配置时注册；`rebind` 允许在注册成功后安全替换已有配置。

必须保证：

- 旧 Agent Token 不再可用。
- 新 Enrollment Token 仍然一次性。
- Enrollment Token 只保存 hash。
- 新 Agent Token 只保存 hash。
- 原始 Token 不写日志。
- Server 的到期时间、月流量设置、分组、标签等资料不能因为 Agent 重装而丢失。

---

## 4.6.3 UI

服务器页面保留“正常服务器 / 已移除”入口。两类 Server 都使用同一套详情模态弹窗，并只提供一个 Agent 身份操作：

```text
重新生成 Agent 安装令牌
```

Panel 根据当前 Agent 和已使用 Enrollment 历史自动选择 `initial` 或 `rebind`。正常 Server 和已移除 Server 调用相同 API；已移除 Server 仍保留“彻底删除”操作。生成结果在详情弹窗内显示，原始令牌关闭弹窗后不可再次获取。

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
- Endpoint
- Chain

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
- Endpoint
- Chain

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

# 7. Phase 6A：静态系统信息

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
到期时间
主机名
系统
内核
架构
IPv4
IPv6
Agent 版本
```

服务器到期时间属于用户设置字段，不依赖 Agent 上报。

如果未设置：

```text
到期时间：不限
```

当前阶段只展示和编辑到期时间，不要求自动停机、自动删除服务器或自动禁用节点。

---

# 8. Phase 6B：动态系统指标

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

# 9. Phase 7A：机器网卡流量、月流量额度与重置周期

## 目标

本阶段直接统计 **机器网卡本身的流量**。

不要依赖：

- Xray 流量统计
- sing-box 流量统计
- Mihomo 流量统计
- Realm 流量统计
- Proxy / Client 应用层统计

也就是说，服务器月流量的基础数据直接来自 Linux 网卡 RX / TX 计数器。

---

## 9.1 流量数据源

Linux 初版可以使用：

```text
/proc/net/dev
```

或者等价的：

```text
/sys/class/net/<interface>/statistics/rx_bytes
/sys/class/net/<interface>/statistics/tx_bytes
```

Agent 读取机器实际网卡累计字节数。

最基本语义：

```text
RX = 下载 / 入站
TX = 上传 / 出站
```

必须排除：

```text
lo
```

同时要避免把同一份流量在物理网卡、bridge、veth、tun 等虚拟接口中重复累计。

初版优先统计承载服务器默认路由的实际网络接口，或者使用一个明确且可预测的主网卡选择规则。

不要通过 Xray / Realm 等应用层数据反推服务器总流量。

---

## 9.2 月流量额度

每台 Server 可以单独设置：

```text
月流量总额
```

例如：

```text
500G
1000G
2T
```

数据库内部统一保存字节数，例如：

```text
monthly_traffic_limit_bytes
```

UI 输入时可以允许使用：

```text
G
T
```

等便于用户理解的单位。

未设置额度时，可以显示：

```text
不限
```

但 Agent 仍然可以继续统计实际网卡流量。

---

## 9.3 单向 / 双向统计模式

每台 Server 必须可以选择：

```text
单向
双向
```

内部建议：

```text
single
bidirectional
```

### 单向

初版定义为只计算服务器出口：

```text
已用流量 = TX
```

### 双向

定义为：

```text
已用流量 = RX + TX
```

注意：

单向 / 双向只影响“月流量已用值”的计算方式。

实时速度仍然应该分别显示：

```text
实时下载速度 = RX/s
实时上传速度 = TX/s
```

不要因为服务器设置为单向计费，就隐藏 RX。

---

## 9.4 流量重置时间

每台 Server 可以设置独立的月流量重置时间。

所有重置时间固定按 Panel 默认时区计算：

```text
Asia/Shanghai
```

不使用 Agent VPS 的本地系统时区。

例如：

```text
每月 1 日 00:00
每月 15 日 08:00
每月 25 日 12:00
```

至少支持：

```text
traffic_reset_day
traffic_reset_time
```

流量周期示例：

```text
2026-09-15 08:00
↓
2026-10-15 08:00
```

到达重置时间后：

```text
本周期已用 RX = 0
本周期已用 TX = 0
```

然后从新的网卡基线继续累计。

重置的是 Panel 记录的“本周期流量”，不是去修改 Linux 网卡内核计数器。

---

## 9.5 重启后的累计方式

Linux 网卡累计计数可能因为：

- VPS 重启
- Agent 重启
- 网卡重建
- 接口计数器归零

而发生下降。

因此不能简单把：

```text
当前 /proc/net/dev 数字
```

直接当成“本月已用流量”。

应采用增量累计：

```text
delta_rx = current_rx - previous_rx
delta_tx = current_tx - previous_tx
```

正常情况下把正增量加入当前月周期。

如果发现：

```text
current < previous
```

说明计数器可能重置。

此时：

- 不产生负流量
- 将当前值作为新的基线
- 从之后的增量继续累计

本周期累计值必须持久化，不能因为 Panel 或 Agent 重启就清零。

---

## 9.6 建议持久化状态

实现时可以使用最小字段或单独的小表保存：

```text
cycle_started_at
cycle_rx_bytes
cycle_tx_bytes
last_nic_rx_bytes
last_nic_tx_bytes
last_sample_at
```

具体放在 `servers` 还是单独流量状态表，在实现 Phase 7A 时根据代码现状选择最简单方案。

不要为了这个功能提前建立复杂时序数据库。

---

## 9.7 本周期流量校准

必须允许用户手动校准“当前周期已用流量”。

典型场景：

```text
VPS 商家后台：
已用 183G / 500G

今天才把这台 VPS 接入 VPS Panel
```

如果 Panel 从 0 开始累计，本月数据会长期与商家后台不一致。

因此 Server 详情页提供：

```text
校准本周期流量
```

用户输入：

```text
当前已用：183G
```

Panel 不需要伪造 RX / TX，也不需要修改 Linux 网卡计数器。

推荐内部采用“校准偏移量”：

```text
measured_used
= 根据单向 / 双向规则由 cycle_rx_bytes / cycle_tx_bytes 计算

traffic_adjustment_bytes
= 用户目标已用量 - measured_used

displayed_used
= max(0, measured_used + traffic_adjustment_bytes)
```

这样校准以后，Agent 继续采集新的网卡增量，显示值会自然继续增长。

例如：

```text
校准时：
机器已统计 20G
商家后台显示 183G

adjustment = 163G

之后机器新增 10G
Panel 显示 = 20G + 10G + 163G = 193G
```

校准值只属于当前流量周期。

到达下一次流量重置时间时：

```text
traffic_adjustment_bytes → 0
```

新周期重新从 0 开始。

如果用户在当前周期修改单向 / 双向模式：

- 保留原始 `cycle_rx_bytes` / `cycle_tx_bytes`
- 按新的模式重新计算 `measured_used`
- 保留当前校准偏移量
- UI 提示如需与商家后台完全一致，可再次执行“校准本周期流量”

不要把人工校准值硬塞进 RX 或 TX 字段。

---

## 9.8 已用流量计算

根据 Server 配置：

### 单向

```text
used_bytes = cycle_tx_bytes
```

### 双向

```text
used_bytes = cycle_rx_bytes + cycle_tx_bytes
```

总流量：

```text
total_bytes = monthly_traffic_limit_bytes
```

---

## 9.9 服务器信息显示与流量周期详情

服务器列表中必须直观显示：

```text
100G（已用）/500G（总）
```

不要只显示百分比。

Server 详情页应展示更完整的信息，例如：

```text
月流量：100G（已用）/500G（总）
剩余流量：400G
使用率：20%
统计方式：双向

本周期：
2026-09-15 08:00
→
2026-10-15 08:00

本周期 RX：62G
本周期 TX：38G
校准值：0G
重置时间：每月 15 日 08:00
时区：Asia/Shanghai
```

单向服务器例如：

```text
月流量：37G（已用）/500G（总）
统计方式：单向（TX）
本周期 RX：80G
本周期 TX：37G
```

这里即使单向只按 TX 计费，也仍然展示 RX / TX 原始统计，便于排查。

如果存在人工流量校准：

```text
本周期 RX：12G
本周期 TX：8G
校准值：163G
月流量：183G（已用）/500G（总）
```

如果没有设置月流量上限：

```text
月流量：100G（已用）/不限（总）
剩余流量：不限
使用率：--
```

可以额外显示进度条，但明确的：

```text
已用 / 总量
```

文字必须存在。

流量周期起止时间统一按照：

```text
Asia/Shanghai
```

展示。

---

## 9.10 到期时间显示

服务器信息同时展示：

```text
到期时间：2026-12-31 23:59
```

未设置：

```text
到期时间：不限
```

当前要求只是：

- 可以设置
- 可以修改
- 可以显示

暂时不要求：

- 到期自动删除 Server
- 到期自动关机
- 到期自动停止 Agent
- 到期自动停止 Proxy

到期本身仍然不自动删除或停机，但需要提供 UI 预警。

---

## 9.11 流量与到期预警

为了日常管理方便，Panel 提供默认的视觉预警。

### 流量预警

只有设置了月流量总额的 Server 才进行百分比预警。

默认：

```text
< 80%      正常
>= 80%     黄色提醒
>= 90%     橙色警告
>= 100%    红色 / 已用完
```

这里的百分比使用最终 `displayed_used` 计算，因此人工校准值也必须计入。

当前先使用固定默认阈值，不增加复杂的每 Server 阈值配置系统。

### 到期预警

到期时间统一按 `Asia/Shanghai` 计算。

默认：

```text
> 30 天     正常
<= 30 天    黄色提醒
<= 7 天     红色警告
已过期      红色 / 已过期
```

### 页面表现

服务器列表可以通过：

- Tag
- 文案
- 轻量颜色状态
- 排序 / 筛选

提示风险。

概览页后续可以增加简单汇总，例如：

```text
即将到期：2 台
流量超过 90%：1 台
离线：3 台
```

当前预警只做 Panel 内 UI 提示。

不要在这个阶段加入：

- Telegram Bot
- 邮件通知
- 短信
- Webhook
- 自动停机
- 自动断网

这些以后如有明确需求再单独实现。

---

## 9.12 超额行为

当前只负责统计和显示。

即使：

```text
已用流量 >= 月流量总额
```

也不要自动：

- 断网
- 停止 Agent
- 停止 Xray
- 删除 Proxy
- 修改防火墙

自动限流 / 停机必须等以后有明确需求再设计。

当前可以在 UI 中标记：

```text
已用完
```

或显示警告状态。

---

## 9.13 本阶段验收

至少验证：

1. Server 可以设置到期时间。
2. Server 可以设置月流量总额。
3. Server 可以选择单向 / 双向。
4. Server 可以设置每月流量重置日和时间。
5. Agent 直接读取机器网卡 RX / TX。
6. 不依赖 Xray / Realm / Proxy 统计。
7. 单向模式按 TX 计算月流量。
8. 双向模式按 RX + TX 计算月流量。
9. VPS 重启后不会出现负流量。
10. Agent / Panel 重启后本月累计流量不会丢失。
11. 到达配置的重置时间后开启新月流量周期。
12. 服务器信息显示类似：
    `100G（已用）/500G（总）`
13. 未设置额度时可以显示：
    `100G（已用）/不限（总）`
14. 不因为流量超额自动停服务。
15. 所有到期时间、重置时间、周期起止时间按 `Asia/Shanghai` 计算。
16. 用户可以校准当前周期已用流量。
17. 校准后新增网卡流量继续在校准值基础上累计。
18. 下一周期重置时自动清除当前周期校准偏移量。
19. 详情页显示当前周期起止时间、RX、TX、剩余量和使用率。
20. 流量达到 80% / 90% / 100% 时有对应 UI 预警。
21. 到期 <= 30 天 / <= 7 天 / 已过期时有对应 UI 预警。

---

# 10. Phase 7B：实时上下行速度

## 目标

继续直接使用 Phase 7A 的机器网卡 RX / TX 计数器，根据累计字节差值计算：

```text
RX bytes delta / 时间
TX bytes delta / 时间
```

实时速度也来自机器网卡，不读取 Xray / Realm 的应用层统计。

得到：

```text
实时下载速度
实时上传速度
```

建议：

Agent 本地采样：

```text
约 2 秒
```

向 Panel 上报：

```text
约 5 秒
```

具体数值可以根据实际效果调整。

---

## 9.1 UI

服务器列表可显示：

```text
↓ 12.4 MB/s
↑ 2.3 MB/s
```

详情显示：

```text
实时下载
实时上传
本周期 RX
本周期 TX
月流量：100G（已用）/500G（总）
统计方式：单向 / 双向
重置时间：每月 N 日 HH:mm
到期时间
```

其中：

- `本周期 RX / TX` 用于诊断和透明展示。
- `月流量已用` 根据单向 / 双向规则计算。
- 月流量显示必须保留明确的“已用 / 总”文字格式。

---

## Phase 7C：服务器分组、标签与筛选

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
即将到期
流量 > 90%
```

其中“即将到期”和“流量 > 90%”属于状态筛选，不需要做成永久标签。

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
7. 可以筛选即将到期和高流量 Server。
8. Agent 重装不会丢失分组 / 标签。
9. ZIP 备份包含分组 / 标签。

---

## 到此完成第一阶段服务器管理 MVP

完成 Phase 7C 后：

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
CPU / RAM / Disk
↓
实时上传 / 下载
↓
机器网卡累计 RX / TX
↓
月流量单向 / 双向统计
↓
按配置时间自动重置
↓
服务器到期时间与预警
↓
服务器分组 / 标签 / 筛选
```

这是第一个完整服务器管理 MVP。

在这个节点之后，再开始代理内核。

---

# 11. Phase 8A：Core 基础模型

## 目标

开始支持代理内核。

第一步只定义 CoreInstance 和生命周期。

计划支持：

- Xray
- sing-box
- Mihomo

但不要一次全部实现。

推荐第一个真实实现：

```text
Xray
```

等 Xray 跑通，再接 sing-box。

---

## 10.1 Core 与 Server

关系：

```text
Server
└── Agent
    └── CoreInstance
```

一台 Server 未来可以：

```text
Xray
sing-box
Mihomo
```

但只有一个 Agent。

---

## 10.2 CoreInstance 最小字段

实现时按需求决定，但大致可能需要：

```text
id
server_id
type
version
status
config_path
service_name
created_at
updated_at
```

不要提前创建全部字段。

---

# 12. Phase 8B：Xray 生命周期

实现：

- Install
- Version
- Start
- Stop
- Restart
- Status
- Update
- ValidateConfig

Panel 通过 Agent 结构化 Task 调用。

不要允许 Panel 发任意 shell。

---

# 13. Phase 9A：Proxy 基础

## 目标

先支持一个协议。

建议第一版：

```text
VLESS
```

尤其可以优先支持你常用的：

```text
VLESS Reality
```

---

## 12.1 Proxy 模型

关系：

```text
Server
└── CoreInstance
    └── Proxy
```

Proxy 保存协议配置。

不要把 Client 凭据全部直接堆在 Proxy 主表。

---

# 14. Phase 9B：更多 Proxy 协议

Xray VLESS 稳定后，再逐个增加：

- Shadowsocks
- Trojan
- VMess（如果确实需要）

Hysteria2 等根据 Core 能力再安排。

不要为了“协议大全”一次铺开。

---

# 15. Phase 10：Client

## 目标

支持一个 Proxy 多个 Client。

例如：

```text
VLESS Reality
├── PC
├── Android
└── iPhone
```

Client 以后可能有：

- name
- UUID / password
- enable
- expiry
- traffic limit
- usage

但第一版只做当前必要字段。

---

# 16. Phase 11A：Realm 安装与生命周期

## 目标

统一 Agent 开始支持 Realm。

仍然使用同一个：

```text
vps-panel-agent
```

禁止独立 Realm Agent。

Agent 内部新增 Realm 管理模块即可。

实现：

- Install Realm
- Version
- Start
- Stop
- Restart
- Status

---

# 17. Phase 11B：Relay 规则

创建 Relay：

```text
源 Server
监听地址
监听端口
目标
TCP / UDP
```

目标支持两类：

### 选择已有 Proxy

Panel 自动解析：

```text
Proxy
↓
Server
↓
目标地址
↓
Proxy Port
```

### 手工目标

```text
host
port
```

不要把目标限制成 Server ID。

---

# 18. Phase 12：AccessEndpoint

创建 Relay 成功后，为目标 Proxy 增加接入 Endpoint。

例如：

```text
Proxy: US VLESS
├── Direct
│   └── us.example.com:443
│
├── JP Relay
│   └── jp.example.com:9502
│
└── HK Relay
    └── hk.example.com:9503
```

三条都使用同一 Proxy 和 Client 凭据。

删除 Relay：

```text
删除 Relay
↓
删除对应 Endpoint
```

不得删除 Proxy 或 Client。

---

# 19. Phase 13：Chain

等单级 Realm 稳定以后再做多跳。

例如：

```text
JP → Seattle → LA Home
```

Chain 应描述路径。

最终对用户可以表现为一个逻辑 AccessEndpoint。

不要让订阅系统理解每一级 Realm 的实现细节。

---

# 20. Phase 14：分享与订阅

到这一阶段才生成：

- VLESS URI
- SS URI
- 二维码
- Clash/Mihomo 配置
- sing-box 配置
- 订阅链接

核心生成逻辑：

```text
Client Credentials
+
Proxy Protocol Config
+
AccessEndpoint host/port
=
最终客户端节点
```

这正是为什么：

- Client 必须属于 Proxy
- Relay 不应该复制 Proxy
- Endpoint 必须独立存在

---

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
- servers（包括到期时间、月流量额度、单向/双向模式、重置时间及当前周期累计状态）
- agent_enrollments
- agents
- Agent Token hash
- CoreInstance
- Proxy
- Client
- Relay
- AccessEndpoint
- Chain
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
CoreInstance 数量
Proxy 数量
Client 数量
Relay 数量
AccessEndpoint 数量
Chain 数量
```

对于尚未实现的资源类型可以不显示，不需要为了预览提前创建未来表。

如果 Server 数据已经支持以下信息，预览也应说明这些会一起恢复：

```text
到期时间
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

建议在真正实现迁移 Phase 时加入结构化 Agent Action：

```text
update_panel_url
```

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
8. 已实现的 Proxy / Client / Relay 等关系完整。
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

# 22. Phase 16：完善

后续再考虑：

- Metrics 历史图
- Core 日志
- Proxy 流量
- Client 流量
- Agent 自动更新
- Core 自动更新
- Realm 自动更新
- Panel 地址迁移体验完善
- 操作日志
- 节点排序
- 批量操作

这些不应阻塞前面的主链路。

---

# 23. 通信协议长期原则

Agent 与 Panel 的 WebSocket 后续消息可以逐步增加。

初期：

```json
{
  "type": "heartbeat"
}
```

系统信息：

```json
{
  "type": "system_info",
  "payload": {}
}
```

Metrics：

```json
{
  "type": "metrics",
  "payload": {}
}
```

任务：

```json
{
  "type": "task",
  "id": "...",
  "action": "restart_core",
  "payload": {}
}
```

结果：

```json
{
  "type": "task_result",
  "id": "...",
  "success": true,
  "payload": {}
}
```

但：

> 不要现在一次性定义完整协议。

只在每个 Phase 新增当前真正需要的消息类型。

---

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

禁止创建空的未来表：

```text
chains
endpoints
proxies
clients
cores
tasks
```

除非当前 Phase 已经开始使用它们。

---

# 25. 安全原则

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

## 任务系统

未来远程控制：

> 必须采用白名单 Action。

禁止任意命令执行 API。

---

# 26. 部署原则

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

# 27. 前端原则

当前只使用简体中文。

不要引入 i18n，除非未来明确提出多语言需求。

技术名词可以保留：

- VPS
- Agent
- SQLite
- API
- WebSocket
- Xray
- sing-box
- Mihomo
- Realm
- VLESS
- Shadowsocks

普通 UI 文案使用中文。

后端状态：

```text
pending
online
offline
```

保持英文内部值。

前端映射：

```text
pending → 待注册
online  → 在线
offline → 离线
```

---

# 28. Codex 开发规则

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

不要创建：

```text
agent/core/xray
agent/core/singbox
agent/relay/realm
```

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

不要因为“以后可能有多个 Core”就在 Phase 5 创建：

```go
type Core interface {}
```

真正开始出现多个真实实现时再抽象。

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

# 29. 推荐实际开发顺序

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
静态系统信息
        ↓
Phase 6B
CPU / RAM / Disk / Uptime
        ↓
Phase 7A
机器网卡累计流量 + 月流量额度 / 重置
        ↓
Phase 7B
实时上下行速度
        ↓
Phase 7C
服务器分组 / 标签 / 筛选
        ↓
第一阶段服务器管理 MVP
        ↓
Phase 8A
Core 基础模型
        ↓
Phase 8B
Xray 生命周期
        ↓
Phase 9A
VLESS Proxy
        ↓
Phase 9B
更多协议
        ↓
Phase 10
Client
        ↓
Phase 11A
Realm 生命周期
        ↓
Phase 11B
Relay
        ↓
Phase 12
AccessEndpoint
        ↓
Phase 13
Chain
        ↓
Phase 14
订阅 / 分享
        ↓
Phase 15
完整 ZIP 备份 / 恢复 / 跨 VPS 迁移
        ↓
Phase 16
完善功能
```

---

# 30. 当前下一步

当前先不要继续系统监控。Phase 4.5、Phase 4.6 和 Phase 5A 已完成。

下一步固定为：

```text
Phase 5B：Heartbeat + 自动重连
```

完成并验证以后：

```text
Phase 6A：静态系统信息
```

完整 ZIP 备份 / 导入已经列为固定需求，但实际实现放在核心业务数据模型基本稳定后的 Phase 15，避免当前每新增一张业务表就反复重写备份格式。

---

# 31. Phase 模板

以后每个新 Phase 的 Codex 指令最好按下面结构写：

```text
开始 Phase X。

开始前：
- 阅读 AGENTS.md。
- 阅读与当前功能直接相关的已有代码。
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

# 32. 最终架构图

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
├── Core Management
├── Proxy Management
├── Client Management
├── Relay Management
├── Endpoint Management
├── Chain Management
└── Subscription
   │
   │ WebSocket
   ▼
Server Agent
├── System
├── Network Metrics
├── Task Runner
├── Xray Module
├── sing-box Module
├── Mihomo Module
└── Realm Module
   │
   ▼
Linux Server
```

固定原则再次强调：

> 一台 Server 一个统一 Agent。

> Proxy、Client、Relay、Endpoint 都不是 Agent。

> Realm 转发产生 Endpoint，不复制 Proxy。

> Client 属于 Proxy。

> Relay 的目标最终必须落到具体 Proxy 或 host:port，而不是只指向 Server。

> 第一个初始化账号是唯一 admin；邀请注册账号统一为 vip。

> vip 不能继续邀请注册账号。

> Server / Proxy / Relay 等业务资源属于整个 Panel，admin / vip 读取同一份共享资源，不做资源副本同步。

> 完整备份必须导出为 ZIP，并支持在新 VPS 的 Panel 中直接导入恢复，不要求手工改 ID、外键或 ZIP 内容。

> Panel 默认业务时区固定为 `Asia/Shanghai`；Server 到期、流量重置和周期显示都按上海时区。

> 月流量允许人工校准当前周期已用值，并在下一周期自动清除校准偏移。

> Server 支持轻量分组、标签和筛选。

> 已注册 Server 必须支持安全地重装 Agent / 轮换长期凭据。

> 每个 Phase 只写当前需要的功能。

---

# 33. 修改记录区

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
- [x] Proxy 与 Server 分离。
- [x] Client 属于 Proxy。
- [x] Realm Relay 不复制 Proxy。
- [x] Relay 创建 AccessEndpoint。
- [x] 后续支持 Xray / sing-box / Mihomo。
- [x] 后续支持 Realm 和多跳 Chain。
- [x] 必须支持完整 ZIP 导出与导入恢复。
- [x] 新 VPS 导入备份后，Panel 内部数据与关联关系不需要人工调整。
- [x] 同域名跨 VPS 迁移时，Agent 应使用原长期凭据自动重连。
- [x] Server 可设置到期时间。
- [x] Server 可设置月流量总额。
- [x] 月流量支持单向 / 双向统计模式。
- [x] 单向流量初版按服务器出口 TX 计费。
- [x] 双向流量按 RX + TX 计费。
- [x] 每台 Server 可独立设置每月流量重置日和时间。
- [x] 服务器总流量直接从机器网卡计数，不依赖代理内核或 Realm 流量统计。
- [x] Server 信息必须以类似 `100G（已用）/500G（总）` 的形式显示月流量。
- [x] Panel 默认业务时区固定为 `Asia/Shanghai`。
- [x] Server 到期、流量重置、流量周期和到期预警统一按上海时区。
- [x] 每月重置日不存在时按当月最后一天执行。
- [x] 支持人工校准当前周期已用流量。
- [x] 流量校准使用当前周期偏移量，不修改 Linux 网卡计数器，不伪造 RX / TX。
- [x] 当前周期重置后流量校准偏移自动归零。
- [x] 流量默认在 80% / 90% / 100% 提供 UI 预警。
- [x] 到期默认在 30 天 / 7 天 / 已过期提供 UI 预警。
- [x] Server 详情显示流量周期起止、RX、TX、剩余量、使用率和校准值。
- [x] 已注册 Server 支持重新安装 Agent 和长期凭据轮换。
- [x] Server 支持轻量分组和多个标签。
- [x] 服务器列表支持分组、标签、状态、到期和高流量筛选。
- [x] ZIP 导入必须先显示预览，用户确认后才执行覆盖恢复。

## 待后续决定

- [ ] Phase 6 系统信息最终表结构。
- [ ] Metrics 是否保留历史以及保留周期。
- [ ] 第一个 Proxy 具体支持哪些 VLESS 传输方式。
- [ ] Xray CoreInstance 是否允许同 Server 多实例。
- [ ] Realm 配置采用单进程多规则还是多实例管理。
- [ ] AccessEndpoint 地址选择逻辑。
- [ ] Chain 的最终数据模型。
- [ ] Subscription 输出格式与权限机制。
- [ ] vip 对业务资源的最终增删改边界（当前至少共享查看/使用，不做资源隔离）。
- [ ] Phase 15 Backup ZIP 的最终 manifest 字段。
- [ ] 跨域名迁移时 `update_panel_url` 的确认与安全机制。

这些内容应在真正进入对应 Phase 时再决定，不提前实现。


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
10. 如果 Panel 域名也变化，则后续通过受控的 `update_panel_url` Agent Action 解决，禁止用任意 shell。


## 2026-09-10 新增 Server 到期与月流量要求

新增固定需求：

1. 每台 Server 可以设置独立到期时间。
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
11. 当前流量超额只做状态展示 / 警告，不自动停机、断网或停止代理服务。
12. 当前服务器到期只负责配置与展示，不自动删除、关机或停节点。


## 2026-09-10 补充日常使用便利性要求

本次确认新增：

1. Panel 默认业务时区固定为 `Asia/Shanghai`，当前不做每 Server 独立时区。
2. Server 到期时间、流量重置时间、流量周期起止和到期预警统一使用上海时区。
3. 如果设置每月 29 / 30 / 31 日重置，而当月不存在该日期，则按当月最后一天执行。
4. 支持“校准本周期流量”，用于新接入 VPS 时对齐商家后台已用流量。
5. 流量校准通过当前周期 adjustment 实现，不修改 Linux 网卡计数器，也不把校准值伪造为 RX / TX。
6. 新流量继续在校准后的已用值基础上增长；下一周期重置时校准偏移归零。
7. 月流量默认在 80% / 90% / 100% 给出 Panel 内视觉预警。
8. Server 到期默认在 30 天 / 7 天 / 已过期给出 Panel 内视觉预警。
9. Server 详情展示完整流量周期：周期起止、RX、TX、校准值、已用 / 总量、剩余和使用率。
10. 已注册 Server 支持重新安装 Agent / 重置长期 Agent 凭据，不需要删除并重新创建 Server。
11. Server 支持一个可选分组和多个标签，并提供分组、标签、状态、关键词、即将到期和高流量筛选。
12. ZIP 导入在真正覆盖之前必须显示导入预览和资源数量摘要，用户确认后才恢复。
13. 本次不修改 vip 对 Server / Proxy 等共享业务资源的最终增删改权限边界。
