# VPS Panel Development Guide

本文描述当前 main 分支的主要架构和长期开发约束。若文档与实现冲突，以当前代码、schema / migration、API 和 UI 的实际行为为准。

安装与日常使用见 [README](README.md)，AI 执行规则见 [AGENTS](AGENTS.md)，协议字段与兼容性见 [Agent API v1](docs/agent-api-v1.md)。

## 1. 项目目标与原则

项目将服务器管理、代理、中转、订阅和轻量探针放在一个可独立部署的系统中。
采用 Go 单体 Panel、SQLite 和一个统一 Agent，优先保证简单、可运行、可验证。

Panel 保存业务状态，Agent 执行目标 VPS 操作。受管服务通过 desired state 同步，不提供任意远程 shell。
现有功能按实际需求演进；只有真实重复或多个实际实现出现时，才引入必要抽象。

## 2. 技术栈

| 部分 | 当前实现 |
| --- | --- |
| Panel / Agent | Go；同一个 Go module，独立命令入口 |
| HTTP / WebSocket | 标准库 `net/http`、`github.com/coder/websocket` |
| 数据库 | SQLite，`modernc.org/sqlite` 驱动 |
| Web | Vue 3、TypeScript、Vite、Naive UI |
| 图表 / 分享 | ECharts、qrcode |
| 受管服务 | Xray、Realm；acme.sh 用于自动 TLS |
| 网络探测 | TCP connect、pro-bing ICMP |
| 通知 | Panel 直接调用 Telegram Bot API |
| 部署 | 原生 systemd；Agent 也支持 OpenRC；可选 Docker Compose |

Go 工具链要求见 [go.mod](panel/go.mod)。前端依赖及实际命令见 [package.json](web/package.json)，安装使用锁文件。
Release workflow 使用 Node.js 22；本地使用该系列最新维护版本，避免旧 Node 不支持 Vite 或测试中的 TypeScript 导入。
受管内核版本和校验值维护在 Agent 源码中，不在长期文档复制版本号。

## 3. 仓库结构

| 路径 | 职责 |
| --- | --- |
| [panel/cmd/panel](panel/cmd/panel) | Panel 启动、后台维护任务、备份恢复与健康检查 |
| [panel/cmd/agent](panel/cmd/agent) | Agent 注册、连接、采集、探测、受管服务与自身生命周期 |
| [panel/internal/api](panel/internal/api) | 路由、认证门禁、DTO、安装脚本与业务入口 |
| [panel/internal/agentcontrol](panel/internal/agentcontrol) | Agent 身份、连接、能力、配置同步与升级状态 |
| [panel/internal/server](panel/internal/server) | Server 元数据、访问权限、指标、周期流量和生命周期 |
| [panel/internal/proxy](panel/internal/proxy) / [relay](panel/internal/relay) / [landing](panel/internal/landing) | 代理、Client、中转、外部节点 |
| [panel/internal/subscription](panel/internal/subscription) | 个人订阅、套餐分发、订阅用户、路由与输出 |
| [panel/internal/monitor](panel/internal/monitor) / [notification](panel/internal/notification) | 探测任务与历史、Telegram 通知 |
| [panel/internal/auth](panel/internal/auth) / [listorder](panel/internal/listorder) | 用户与会话、按用户保存的列表顺序 |
| [panel/internal/database](panel/internal/database) / [backup](panel/internal/backup) | schema、迁移、数据库备份与恢复 |
| [web/src](web/src) | 页面、组件、composables、类型与共享样式 |
| [scripts](scripts) / [deploy](deploy) | 原生 Panel 安装与管理、可选 Compose 部署 |

业务通常沿 API Handler → 现有领域 Service → SQLite 展开，不要求为简单功能增加额外层级。

## 4. 核心领域模型

| 模型 | 含义与边界 |
| --- | --- |
| Server | 一台受管理 VPS 的业务记录；保存访问权限、归属、流量配置及 Agent 状态 |
| Agent | Server 的执行端身份和连接；使用独立 Agent Token 认证 |
| Proxy | Server 上的 Xray 入站及协议配置，不等于一个用户凭据 |
| Client | Proxy 下的凭据、配额与生命周期，可分配给普通用户或由订阅系统管理 |
| Relay | Server 上的 Realm 转发规则，引用目标 Proxy / Landing 或手动地址 |
| Landing | Panel 保存的外部节点，不会因此在远端安装 Agent |
| Subscription | 将可用节点、路由和配置模板组织成用户可获取的订阅 |
| Probe Task | 独立网络探测任务，与代理配置的版本和生效流程分开 |

`created_by_user_id` / `created_by_role` 表示创建来源，`owner_user_id` 表示可调整的归属。
归属、访问权限、创建来源各有用途，不能相互替代；订阅分发等业务还会检查创建来源。

## 5. Panel / Agent 架构

Panel 负责认证、业务校验、持久化、Web API、订阅输出、探测历史和通知。
Agent 负责目标机上的系统采集、探测、Xray / Realm、受管防火墙、诊断和升级。
Web 提供管理与监控界面，不承担最终权限判断。

配置变更在数据库事务内更新业务记录和 Server 的 desired-state version，并记录配置操作。
Panel 通过已有 WebSocket 通知在线 Agent；Agent 获取完整目标配置，执行后回报对应版本的结果。
周期拉取用于恢复漏掉的通知，重连也重新同步。保存成功和目标机应用成功是不同状态，UI 展示 pending / success / failed。

Agent 对受管配置先生成候选内容、校验并替换，失败时保留或尝试恢复可用配置。
不同受管服务的操作不构成跨服务原子事务，不能把某一服务的回滚描述为整台 VPS 的完整恢复。

Server 正常移除进入 decommission 流程，要求 Agent 明确支持清理和自卸载。
Agent 清理受管资源并准备自卸载后回报成功，Panel 才归档记录并撤销 Agent 凭据。
强制移除仅处理 Panel 侧记录，不能承诺远端已卸载；永久删除与归档也应区分。

## 6. Agent API 与 capability

一次性 Enrollment Token 用于初次注册或受控重新绑定；成功后 Agent 使用长期 Agent Token 访问 HTTP 与 WebSocket。
实现身份、软件版本和协议版本是三个独立概念，不能用版本字符串证明官方身份。

API v1 通过显式 capabilities 表达支持的功能。一般代理能力与 Legacy 的兼容规则，和探测、受管清理等要求显式声明的能力，并不完全相同。
调用现有 capability 判断函数，遵循协议为具体功能定义的规则，避免自行扩大 Legacy 兜底。

在线诊断、探测下发和结果接收使用当前连接的能力；探测历史展示可使用最后声明的能力，任务分配不依赖 capability。
官方自升级对已标识 API v1 Agent 要求官方 implementation 和 `self_upgrade`；第三方实现不能仅凭版本号进入官方升级流程。
升级成功由目标版本 Agent 重新连接确认，失败通过专用 HTTP 结果接口回报。

未知的 WebSocket 消息类型保持可忽略兼容。Panel 下行复用连接写锁，Agent 上行复用主写循环。
状态写入需确认仍是当前连接，避免被替换的旧连接覆盖新状态。

注册字段、认证头、能力列表、desired state、结果与升级接口、Legacy 行为均见 [Agent API v1](docs/agent-api-v1.md)。
本指南不复制其 JSON 定义。

## 7. Server 与 Metrics

Server 管理包含基本信息、分组、归属、访问范围、续期、流量配置和生命周期。
Server 的公开 / 私有访问范围只针对 `admin` / `vip`：公开对所有管理账号可见，私有通过 `server_access` 授权指定管理账号。
写入时拒绝 `user` / `subscriber`，读取授权和判断私有访问时也检查账号角色，历史无效授权由数据迁移清理。
普通用户和订阅用户继续使用各自门户、Client 分配和订阅模型，不通过 `server_access` 授权。
管理员也受 Server 访问检查约束，不能把 admin 理解为自动绕过所有资源 ACL。

Agent 连接时报告系统信息，并持续报告 CPU、内存、磁盘、uptime 和 NIC 累计计数。
Panel 保存最新指标与更新时间；Server 在线状态与当前 Agent 连接相关，启动时会清理旧在线状态。

前端网络速度根据相邻有效 NIC 样本计算，重复时间戳不生成新速度；离线、计数回退或重启时重新建立基线。
缺失指标不当作零使用率。当前没有 CPU / RAM / Disk 的历史时序库。

续期信息属于 Panel 业务元数据。自动续期在 Panel 启动和周期维护时处理，不直接执行支付或购买。
日期计算使用既有上海时区和月末规则，修改时复用 [renewal.go](panel/internal/server/renewal.go)。

## 8. Monitor / Probe

[MonitorView](web/src/views/MonitorView.vue) 复用 App 中现有 `ServerRecord` 列表及轮询。
[useMonitor](web/src/composables/useMonitor.ts) 计算资源比例和网络速度；详情使用独立的 `MonitorServerDetail`，不嵌回服务器管理详情。

管理员管理 Probe Task，手动选择当前服务器，并可设置为以后新增 Server 自动继承。
`monitor_probe_servers` 是当前分配关系的唯一来源；`default_on` 不参与 Desired / Ingest / History 的运行时匹配，也不会自动作用于当前未分配的服务器。
Server 创建事务在 pending 阶段写入所有默认任务的分配；关闭默认开关保留已有分配。默认任务总数与每台 Server 的实际分配数分别最多 64，均包含停用任务。
升级迁移会一次性把旧默认任务对未归档、未处于 decommission 状态的服务器的关系写入关联表，保留手动分配及历史记录。
Agent capability 只决定已分配任务能否下发，不决定 assignment 是否存在。
TCP 与 ICMP 分别由 `probe.tcp` / `probe.icmp` 声明；不能因 Agent 名称或版本推断支持。
任务通过现有 WebSocket 下发独立的完整 desired task list，不进入 Xray / Realm desired state。

Agent 执行 TCP connect 或 ICMP Echo，Panel 验证当前连接、任务状态、分配范围及能力后保存结果。
历史查询沿用管理角色与 Server ACL；前端支持 1h / 6h / 24h 延迟曲线和相应失败率，SQLite 定期清理过期探测记录。
权限错误等本机问题不应被展示成 ICMP 丢包；失败和报告间断保留图表空隙。

精确超时、任务上限、结果枚举、窗口统计及 API 字段见 [网络探测协议](docs/agent-api-v1.md#network-probes)。

## 9. Proxy / Xray

Proxy 保存服务端入站配置，Client 保存其下独立凭据。当前支持 VLESS over TCP 的 TLS / REALITY，以及 Shadowsocks 2022。
配置校验、分享参数和实际下发应使用相同业务来源，避免 UI、分享链接与 Agent 配置出现三套规则。

TLS 可以使用手动证书，或由 Agent 通过受管 acme.sh 申请 Let's Encrypt HTTP-01 证书并续期。
自动 TLS 要求域名指向目标 Agent VPS、TCP 80 可达；证书申请、候选证书应用和续期由 Agent 处理。
Panel 自身 HTTPS 由部署层的 Caddy 管理，两者不是同一证书流程。

REALITY 的公钥、私钥和客户端展示参数分开处理。普通列表 DTO 不应返回私钥或完整客户端凭据。
需要分享信息时，通过已有受权限控制的接口获取。

Agent 管理专属的 Xray 二进制、配置、服务与防火墙规则；不接管任意既有 Xray 安装。
禁用最后一个 Proxy 与删除最后一条 Proxy 记录不同：前者停用，后者会请求清理受管运行时。
已记录非空 Agent 版本时，删除最后一条记录要求显式清理能力；版本为空时当前入口跳过此检查，不能据此认定远端支持清理。

## 10. Relay / Realm

Relay 描述入口 Server 的监听与目标，支持 TCP、UDP、TCP+UDP。
目标可以是已有 Proxy、Landing 或手动 host / port；引用更新时按现有逻辑同步所涉及 Server 的 desired state。

分享链接的入口地址来自 Relay，协议及认证参数来自目标节点，不能因中转地址改变而丢失目标 TLS / REALITY 参数。
监听端口与 Proxy 共用现有端口预留检查，避免跨模块冲突。

普通用户的个人中转使用分配给自己的 Client 和受限来源；订阅发布产生的受管 Relay 由订阅业务维护。
这些资源不能绕过其归属流程直接修改。最后一条 Relay 的删除沿用与 Proxy 相同的清理能力检查。

Agent 在专属目录和服务中管理 Realm；清理只针对本项目拥有的运行时和防火墙资源。

## 11. Subscription / Client / Landing

### Client

Client 有独立凭据、启停状态、流量配额和到期时间，实际可用性由业务状态共同决定。
普通 Client 可分配给 `user`；订阅系统创建的 Client 具有专门映射与生命周期，不应通过普通管理入口绕过约束。
Client 分享链接及二维码包含连接凭据，应按敏感信息处理。

### Landing

Landing 解析外部 VLESS / Shadowsocks 分享链接，保存协议、地址和必要参数。
当前解析支持有明确边界，例如 VLESS 传输限制及不支持 Shadowsocks plugin；输入按现有解析器校验，不能原样下发任意配置。

私有节点仅所有者访问；公开节点可被有管理权限的用户使用，修改仍限所有者。
这里的公开可见不等于任何登录角色都获得管理 API 权限。Landing 不创建远端服务，也不增加远端流量采集。

### 订阅

管理用户的个人订阅按 owner 隔离，组织可访问的节点来源、节点实例及路由绑定。
管理员的订阅分发维护发布节点、套餐、subscriber 资料及相应 Client / Relay；subscriber 通过独立门户访问自己的资源。

模板、路由配置及绑定由现有 subscription 模块校验并输出 Mihomo 配置。
引用节点的权限、停用、到期、流量限制和删除影响，都应通过该模块已有生成与协调逻辑处理。
公开订阅地址以不可猜测的 Token 授权；Token 重置后旧链接失效，不应记录到日志。

## 12. Traffic

Server 流量来自 NIC 累计计数的增量，和 Xray Client 流量是两套不同口径。
前者覆盖 VPS 网络活动，后者用于代理凭据及订阅用户统计，不能互相替代或重复累计。

Server 周期流量支持单向或双向计算、限额、周期重置和手动校准。
单向使用发送量，双向使用接收量加发送量；展示和通知复用正式 `TrafficUsedBytes` 计算，包含周期调整值与边界处理。
计数回退、Agent / VPS 重启和周期切换应沿用既有基线及增量逻辑。

Agent 从受管 Xray 的统计获取 Client 数据，通过认证 HTTP 回报。
Panel 累计使用量并协调 Client 配额、到期及订阅用量，必要时更新 desired state。
流量上限是业务控制，不能视为运营商计费的精确替代。

## 13. Notification

[notification](panel/internal/notification) 目前只发送 Telegram 的 Server 离线、恢复、流量阈值通知。
设置入口位于探针页面，仅管理员可读取配置、修改和测试；读取结果不返回 Bot Token 原文。

Panel watcher 使用当前在线连接和正式 Server 流量数据判断事件，跳过不适用的服务器。
离线需要经过宽限期；恢复通知以实际成功发送过离线通知为前提，避免把尚未通知的短暂断连作为恢复事件。

配置及每台 Server 的离线 / 流量通知状态保存在 SQLite，流量提醒按周期和阈值去重。
异步发送器将 Telegram 网络请求与 Agent 消息处理分开；有界队列和有限重试不构成可靠消息队列，也不承诺消息必达。
重启后结合重建的连接状态重新判断，不直接按陈旧的在线记录群发离线消息。

目前没有 CPU、内存、磁盘或 Probe 延迟阈值通知，也没有其他通知渠道。
具体发送重试和错误处理维护在源码与测试中，不在本指南重复参数。

## 14. Auth / Access Control

[auth](panel/internal/auth) 负责初始化、账号、邀请、密码和会话；API Handler 执行角色及资源访问校验。

| 角色 | 主要边界 |
| --- | --- |
| `admin` | 系统设置、账号、邀请、通知、探测任务、备份、订阅分发等；管理资源仍检查相应 ACL |
| `vip` | 管理可访问的 Server / Proxy / Relay / Landing 和自己的个人订阅 |
| `user` | 普通用户门户、分配的 Client 节点和授权的个人中转 |
| `subscriber` | 独立订阅门户和自己的套餐资源 |

`requireManager` 对应 admin / vip；普通用户和订阅用户有分别受限的路由，不应仅用“已登录”代替这些门禁。
Server 的公开可见性不赋予普通用户管理权限。具体敏感操作还会额外要求 admin。

首次初始化只创建管理员。管理员可生成一次性、有效期 24 小时的邀请，角色为 vip / user / subscriber，默认 vip。
用户名去除首尾空白后使用 3–64 个 ASCII 字母、数字、点、下划线或连字符，数据库比较区分大小写。
密码按 UTF-8 字节长度校验为 6–72 字节，使用 bcrypt；已有 Session 和密码变更 / 重置流程需一起考虑。

隐藏按钮只是 UI 行为。新增读写 API、分享、导出或订阅来源时，后端必须独立校验对应角色与资源权限。

## 15. Database / Migration / Backup

SQLite 使用单连接、外键和 WAL。schema 定义见 [schema.go](panel/internal/database/schema.go)，版本迁移入口见 [versioned_migrations.go](panel/internal/database/versioned_migrations.go)。
旧库兼容转换由现有迁移代码衔接；版本号以源码为准，不在文档维护迁移历史清单。

数据库改动必须同时覆盖：

- 空库初始化后的最终 schema。
- 已有数据库的版本升级路径。
- 迁移测试中的数据保留、外键、唯一性、默认值和重复启动行为。

仅修改 `CREATE TABLE IF NOT EXISTS` 无法升级已有表。重建表时需验证外键引用、索引及自增序列等原有约束。
不要用删除用户数据库来替代 migration。

管理员备份导出通过 SQLite 快照生成 ZIP，并包含 manifest、校验信息及相关部署配置。
导入验证归档和数据库后暂存，Panel 重启时替换数据库；迁移成功后提交，否则尝试回滚旧库。
备份域名必须与当前 Panel 匹配，不能把它当作自动重写所有 Agent 地址的工具。

备份包含凭据与业务数据，应按敏感文件保管；它不备份各 Agent VPS 的整个文件系统。
原生安装器的升级快照与用户导入 / 导出备份用途不同，修改其一不能假定另一个自动覆盖。

## 16. Frontend

[App.vue](web/src/App.vue) 使用 Vue 状态切换页面，当前没有 Vue Router 或 Pinia。
管理入口包括概览、探针、服务器、代理、中转、订阅、账号、拼车和审计等页面，按角色显示；user / subscriber 使用各自门户。

页面逻辑优先放在现有 views / composables，局部展示放在对应领域组件。
API 访问复用 [api/client.ts](web/src/api/client.ts)，类型与后端 DTO 保持一致，格式化函数和 CSS 变量沿用现有实现。
Naive UI 提供表单、表格、对话框等基础 UI；响应式布局在共享样式和组件中维护。

[drag.ts](web/src/drag.ts) 统一拖拽预览、边缘自动滚动和清理；[reorder.ts](web/src/reorder.ts) 提供顺序移动、持久化和失败恢复。
Server / Proxy / Relay / Landing / 用户列表复用后端 listorder，顺序是当前用户的偏好，不应递增 Agent 配置版本。
个人订阅节点和路由编辑器的顺序属于其自身配置数组，不能机械改用全局列表顺序表。

轮询和拖拽需要协调，避免刷新覆盖正在调整的顺序。组件卸载应清理监听器、计时器及未完成请求。
Monitor 继续共享 Server 数据源，探测任务和通知配置使用各自 API，不再创建另一套 Server 状态缓存。

## 17. Deployment / Release

原生安装与 `vp` 操作见 [README](README.md)，不在此重复安装命令。
Panel 安装器需要 systemd；Agent 安装器根据 systemd / OpenRC 选择服务配置。

同机运行时的核心隔离：

| 内容 | 原生默认位置 |
| --- | --- |
| Panel 程序 / 数据 | `/opt/vps-panel/panel` / `/var/lib/vps-panel/panel` |
| Panel 环境配置 | `/etc/vps-panel/panel/environment` |
| Agent 程序 / 凭据 | `/opt/vps-panel/agent` / `/etc/vps-panel-agent/config.json` |
| 受管 Xray / Realm | 各自专属目录与服务，由 Agent 管理 |

Panel 更新、卸载和历史布局迁移不得跨越其资源边界删除同机 Agent 或受管内核。
Agent 清理也不得删除非本项目拥有的服务、目录和防火墙规则。

Panel 本地运行读取 `PANEL_LISTEN_ADDR`、`PANEL_DATA_DIR`、`PANEL_WEB_DIR`；默认分别为 `127.0.0.1:8080`、`data`、`../web/dist`。
生产域名模式使用 Caddy 反向代理；IP 模式和 Compose 的对外端口不同，以 README 的对应安装方式为准。

Release workflow 对 `v*` tag 构建 Linux amd64 / arm64 的 Panel 和 Agent，版本由构建参数注入。
Panel 压缩包带构建后的 Web；`SHA256SUMS` 当前覆盖 Agent 文件。手动 workflow dispatch 可构建，但发布 Release 的步骤以 tag 为条件。
发布验证应对应 tag 的实际 commit 和上传产物，不能只看本地代码或工作流已启动。

## 18. Security

认证凭据、分享密钥、私钥及备份内容均为敏感数据。
普通列表、审计记录、错误响应和日志应只暴露完成该操作所需的信息。
Telegram Token 保存在数据库中，读取 API 的遮蔽不等于数据库加密。

Panel 与 Agent 的公网连接推荐 HTTPS / WSS，网络层加密不替代 Token 与后端授权。
capability 只描述支持能力，不是额外的授权凭据，也不能证明第三方 Agent 的可信程度。

Agent 执行的操作限定为已定义的配置、探测、诊断和生命周期流程，不提供任意命令执行接口。
路径、端口、下载、归档和外部输入校验应复用已有边界；日志中的上游错误也要检查是否含凭据。

## 19. Testing

以下命令在相应目录执行。根据改动范围选择相关测试；核心 backend、schema 或协议修改运行完整 Go 测试。

Go（从仓库根目录进入 `panel`；第一条格式化实际改动文件）：

```bash
cd panel
gofmt -w path/to/changed.go
go test ./...
go vet ./...
go build ./...
```

`path/to/changed.go` 是待替换的路径，不是仓库内固定文件。
若只验证局部改动，可将 `go test ./...` 换为实际受影响的 package，并覆盖调用方的关键行为。

Frontend（从仓库根目录进入 `web`）：

```bash
cd web
npm ci
npm test
npm run build
```

`npm run build` 包含 `vue-tsc --noEmit` 和 Vite 构建。项目没有 `npm run lint` 脚本。
测试重点是权限、状态转换、解析、配置生成、流量边界、迁移和真实交互，不为简单 getter 堆覆盖率。

纯文档修改检查 Markdown 渲染、相对链接、代码块、命令与真实行为的一致性，不必运行无关的昂贵业务测试。
运行不了的验证需要注明环境原因，不能以“预计通过”替代实际结果。

## 20. 开发工作流

先从入口路由、领域 Service、schema 与对应页面找到真实行为，再搜索可复用实现。
确认当前任务所需的最少文件，按现有结构修改，补齐受影响的关键验证。

涉及协议或 capability 时同步 [Agent API v1](docs/agent-api-v1.md)；涉及用户安装、支持范围和可见功能时同步 README。
架构变动更新本文，AI 的稳定执行约束维护在 AGENTS，开发历史留在 Git / Release。

提交前检查 diff 范围、凭据泄露、未使用代码及文档链接，报告实际完成内容、主要文件、验证和已知限制。
具体执行要求以 [AGENTS.md](AGENTS.md) 为准。
