# VPS Panel

VPS Panel 是一个轻量的多 VPS 管理面板，集服务器管理、代理节点、中转、订阅和轻量探针于一体。

Panel 与 Agent 通过认证 WebSocket 保持连接，统一管理 VPS 状态、Xray / Realm 配置、流量和网络探测。

## ✨ 功能

| 功能 | 当前支持 |
| --- | --- |
| 服务器 | 系统信息、在线状态、分组、访问权限、到期与续期信息 |
| 轻量探针 | CPU / RAM / Disk、网络速度、周期流量；TCPing / ICMP Ping 与 1h / 6h / 24h 延迟历史 |
| Telegram | Server 离线、恢复和流量阈值通知 |
| 代理节点 | 受管 Xray；VLESS over TCP、TLS、REALITY、Shadowsocks 2022 |
| 中转 | 受管 Realm；TCP、UDP、TCP+UDP；目标可选代理节点、外部节点或手动地址 |
| 外部节点 | 导入 VLESS / Shadowsocks 分享链接，管理 Landing 节点 |
| Client 与流量 | 独立凭据、流量配额、到期控制、分享链接与二维码；Server / Client 流量统计 |
| 订阅 | 个人订阅、订阅套餐与专用用户入口、Mihomo 配置、路由配置与绑定 |
| Agent | 一次性注册、自动重连、配置同步、诊断和官方 Agent 自升级 |
| 管理 | 四种用户角色、列表拖拽排序、备份导入导出、审计日志 |

## 🚀 快速开始

原生安装使用 systemd，推荐 Debian / Ubuntu。以下命令在目标 Linux VPS 执行，需要 root 权限。

### IP 安装

```bash
curl -fsSL https://raw.githubusercontent.com/renaissance0721/vps-panel/main/scripts/install-panel.sh | sudo bash -s -- --domain :80
```

安装后访问 `http://服务器IP:8080`，首次打开页面创建管理员账号。
此处 `:80` 是脚本的无域名标记；原生安装的访问端口仍为 **8080**，请放行该端口。

### 域名 + HTTPS

先将域名解析到 Panel 所在 VPS，并确保 TCP 80 / 443 可从公网访问：

```bash
curl -fsSL https://raw.githubusercontent.com/renaissance0721/vps-panel/main/scripts/install-panel.sh | sudo bash -s -- --domain panel.example.com
```

将 `panel.example.com` 替换为自己的域名。脚本配置 Caddy 和 HTTPS，完成后访问 `https://panel.example.com`。
公网部署推荐此方式；Caddy 的自动安装适配 Debian / Ubuntu。

### 系统与架构

| 组件 | 安装环境 | Release 架构 |
| --- | --- | --- |
| Panel 原生安装 | Linux + systemd，推荐 Debian / Ubuntu | amd64、arm64 |
| Agent | Linux + systemd；或 Alpine Linux + OpenRC | amd64、arm64 |
| Panel Docker | 可运行 Docker Compose 的 Linux 主机 | 构建方式见下文 |

Agent 安装器按服务管理器选择 systemd 或 OpenRC；OpenRC 环境需要 `supervise-daemon`。

## 🖥️ Agent

1. 在 Panel 的服务器页面创建 Server。
2. 由管理员生成一次性 Agent 安装命令。
3. 在目标 VPS 以 root 执行页面提供的命令。
4. Agent 注册成功后连接 Panel，服务器状态与指标会自动更新。

使用页面生成的完整命令，避免手动拼接安装令牌。Agent 支持自动重连、Metrics、Probe 和受管 Xray / Realm 配置同步。
管理员可升级符合条件的官方 Agent；第三方 Agent 的可用功能取决于其声明的能力。

正常移除 Server 会请求兼容 Agent 清理受管服务并自卸载，成功后归档服务器。
离线或不兼容 Agent 可由管理员强制移除 Panel 记录，但目标 VPS 上的残留需要自行清理。

实现第三方 Agent 请阅读 [Agent API v1](docs/agent-api-v1.md)。

### 自动 TLS

VLESS TLS 支持手动证书，也支持通过 Let's Encrypt HTTP-01 申请证书。
自动申请时，节点域名应正确解析到对应 Agent VPS，并保持该 VPS 的 TCP 80 可访问；Agent 负责自动续期。

Panel 页面使用的 HTTPS 与代理节点的 TLS 证书分别管理。

### 探针与通知

在探针页面查看资源状态和网络延迟。管理员可配置 TCPing / ICMP Ping 任务及目标服务器。
延迟图支持 1h / 6h / 24h；CPU、内存和磁盘目前展示最新指标，暂未提供资源历史曲线。

管理员在探针页面配置 Telegram Bot Token 和 Chat ID，并可发送测试通知。
支持离线宽限、恢复提醒、流量首次阈值、后续提醒步进和用尽提醒。

## 🔐 用户与权限

| 角色 | 用途 |
| --- | --- |
| `admin` | 系统管理、账号与邀请、探测任务、通知、订阅分发，以及有权访问的服务器资源 |
| `vip` | 管理有权访问的服务器、代理和中转，维护自己的个人订阅 |
| `user` | 使用分配给自己的节点，并在授权范围内管理个人中转 |
| `subscriber` | 使用订阅套餐和专用订阅入口，查看自己的节点、流量及到期信息 |

首次初始化创建管理员，后续账号通过管理员管理或邀请加入。
用户名区分大小写。服务器访问权限由后端校验；完整权限边界见 [开发指南](DEV.md)。

## 项目结构

Panel、Agent 和 Web 位于同一个 monorepo：

```text
vps-panel/
├── panel/     # Go 模块，包含 Panel 与 Agent
├── web/       # Vue 管理界面
├── docs/      # Agent 协议规范
├── scripts/   # Panel 安装及 vp 管理命令
└── deploy/    # 可选 Docker Compose / Caddy 配置
```

## 管理命令

原生安装完成后，可执行 `sudo vp` 打开管理菜单，也可直接使用：

| 命令 | 用途 |
| --- | --- |
| `sudo vp update` | 更新到最新 Release |
| `sudo vp domain` | 修改 Panel 域名或切换为 IP 访问 |
| `sudo vp status` | 查看 Panel 服务状态 |
| `sudo vp logs` | 查看 Panel 日志 |
| `sudo vp restart` | 重启 Panel |
| `sudo vp uninstall` | 卸载 Panel，交互选择是否删除数据 |
| `vp help` | 查看命令帮助 |

修改 Panel 地址后，已有 Agent 的连接地址需要同步处理。
更新会保留业务数据；跨服务器迁移可使用管理员的备份导出 / 导入功能，并保持 Panel 访问域名一致。

## 🛠️ 本地开发

准备 Go 1.26 或更新版本，以及 Node.js 22 的最新维护版本和 npm。
具体依赖以 [go.mod](panel/go.mod)、[package.json](web/package.json) 和锁文件为准。

在仓库根目录构建前端并启动 Panel：

```bash
cd web
npm ci
npm run build
cd ../panel
go run ./cmd/panel
```

默认访问 `http://127.0.0.1:8080`。需要前端热更新时，另开终端，在仓库根目录执行：

```bash
cd web
npm run dev
```

Vite 开发服务器将 `/api` 转发给本地 Panel；订阅链接使用 Panel 地址访问。
开发配置、测试和 Agent 边界见 [DEV.md](DEV.md)。

## Release

推送 `v*` tag 会触发 [Release workflow](.github/workflows/release.yml)，产出：

- `vps-panel-linux-amd64.tar.gz` / `vps-panel-linux-arm64.tar.gz`：Panel 与构建后的 Web。
- `vps-panel-agent-linux-amd64` / `vps-panel-agent-linux-arm64`：Agent 二进制。
- `SHA256SUMS`：Agent 二进制的 SHA-256 校验值。

正式版本以 [GitHub Releases](https://github.com/renaissance0721/vps-panel/releases) 为准。

## Docker（可选）

仓库保留从源码构建 Panel 的 Compose 方式。在仓库根目录执行：

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

默认由 Caddy 提供 `http://服务器IP`，使用端口 80。
如需域名 HTTPS，可在同一命令前设置环境变量（Linux shell）：

```bash
PANEL_DOMAIN=panel.example.com docker compose -f deploy/docker-compose.yml up -d --build
```

Compose 使用持久化卷保存 Panel 与 Caddy 数据，不包含 Agent；Agent 仍安装在各目标 VPS 上。

## 📚 文档

- [DEV.md](DEV.md)：当前架构、模块边界和开发验证。
- [AGENTS.md](AGENTS.md)：Codex / AI Coding Agent 的执行规则。
- [Agent API v1](docs/agent-api-v1.md)：Agent 实现者的正式协议规范。

## Roadmap

持续完善探针、通知、订阅和管理体验。

## License

[MIT](LICENSE)
