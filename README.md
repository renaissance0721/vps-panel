# VPS Panel

多 VPS 管理面板。目前已完成 **Phase 7A、Phase 8A、Phase 8B、Phase 9A、Phase 9B、Phase 10 和 Phase 11**，Phase 7B 服务器分组、标签与筛选暂缓且不阻塞代理主链路。项目提供 admin / vip 两级邀请制账号认证、Server 安全移除与 Agent 重新绑定、一次性 Agent 注册、Agent 原地升级、带 Heartbeat 和自动重连的认证 WebSocket 长连接、静态系统信息与动态指标上报、Server 到期日期、月流量统计与校准、Panel ↔ Agent desired-state 配置同步，以及 Panel Agent 的 Xray、Realm 安全托管与 Proxy、Relay 管理。

当前 Server 管理能力包括在线/离线状态、Heartbeat、`last_seen`、静态系统信息、到期日期，以及 CPU、RAM、根分区磁盘、Uptime 和累计网卡流量。Agent 约每 5 秒通过现有 WebSocket 上报动态指标；Server 详情展示当前月周期流量，支持单向/双向统计、额度与重置时间配置、90%/100% 预警，以及不修改原始网卡计数的本周期流量手动校准。尚未实现历史指标和到期副作用。

Phase 8A 已完成带版本的完整 desired state 拉取、`config_changed` WebSocket 通知、同步结果持久化和约 30 秒 REST 兜底。Phase 8B 已完成 Agent 侧固定官方 Xray `v26.3.27` 的 SHA256 校验安装、独立受管路径、基础配置校验与原子替换、systemd 启停、健康检查和失败回滚。

Phase 9A 已支持 VLESS + TCP + TLS / REALITY + XTLS Vision。Phase 9B 在同一套 Proxy / Client 模型上增加 Shadowsocks 2022，支持 `2022-blake3-aes-128-gcm` 和 `2022-blake3-aes-256-gcm`，固定 TCP + UDP，并为每个 Client 生成可直接导入的 SIP002 URI。Proxy 或 Client 配置保存后会递增对应 Server 的 desired-state 版本并通知 Agent，Agent 继续复用 Phase 8B 的候选配置校验、原子替换、健康检查和失败回滚。

Phase 10 已完成。Xray 使用稳定的非敏感 Client 统计标识维护累计上行/下行计数，Agent 约每 15 秒通过独立认证 HTTP 接口上报，Panel 持久化 baseline、本周期累计和最近活动时间。Client 支持 G/T 流量额度、never / daily / weekly / monthly 上海时区周期、下次重置时间、本周期手动重置和到期时间。实际可用状态实时按 `effective_enabled = enabled && !expired && !quota_exhausted` 派生；达到 90% 显示预警，到期或额度耗尽时从 Xray desired state 失效，周期重置、手动重置或调整配置解除阻塞后自动恢复，同时保留原 Client 凭据和分享 URI。

Phase 11 已完成。Panel 提供 Relay CRUD，可将中转目标绑定到现有 Proxy 或手动 Host/IP 与端口，并支持 TCP、UDP、TCP+UDP。Agent 固定使用 Realm 官方 `v2.9.4`，按 amd64 / arm64 选择并校验 Release，安装在独立受管路径；完整 Relay desired state 会确定性生成单进程多 endpoint 配置，经真实 Realm candidate 校验、原子替换、systemd 重启、TCP/UDP listener 检查和独立防火墙同步后生效，失败时恢复 previous 配置与规则。Realm 与 Xray 的配置、服务、回滚和防火墙所有权相互独立。下一阶段为 Phase 12：分享与订阅，当前未开始。

Realm 受管路径：

```text
/opt/vps-panel/realm/realm
/opt/vps-panel/realm/.managed-by-vps-panel
/etc/vps-panel/realm/config.toml
/etc/vps-panel/realm/config.previous.toml
/etc/systemd/system/vps-panel-realm.service
```

如果检测到第三方 `realm.service`，或上述 Panel 专用路径在没有受管标记时已被占用，Agent 会拒绝接管。禁用最后一条 Relay 时会停止服务、清理当前配置和 Realm 自有防火墙规则，但保留受管二进制、标记和 unit 供再次启用。

## VPS 部署

默认部署不依赖 Docker，也不需要在 VPS 安装 Go、Node.js 或 npm。GitHub Release 提供已经编译完成的 `linux-amd64` 和 `linux-arm64` 压缩包。

要求：

- 使用 systemd 的 Debian 或 Ubuntu VPS
- CPU 架构为 amd64 或 arm64
- 使用 IP 直连时放行 TCP 8080
- 使用域名时，A/AAAA 记录已指向 VPS，并放行 TCP 80、TCP/UDP 443

### 一键安装

使用 VPS IP，通过 HTTP 访问：

```bash
curl -fsSL https://raw.githubusercontent.com/renaissance0721/vps-panel/main/scripts/install-panel.sh | sudo bash
```

交互式终端会询问域名。直接按回车时，Panel 会监听 `0.0.0.0:8080`，安装完成后访问：

```text
http://VPS_IP:8080
```

使用域名并自动配置 HTTPS：

```bash
curl -fsSL https://raw.githubusercontent.com/renaissance0721/vps-panel/main/scripts/install-panel.sh | sudo bash -s -- \
  --domain panel.example.com
```

域名模式下，Panel 只监听 `127.0.0.1:8080`。安装脚本会在需要时通过 Caddy 官方 Debian/Ubuntu 软件源安装原生 Caddy，配置反向代理，并自动申请、保存和续期 HTTPS 证书。

安装脚本会自动检测 CPU 架构，从最新 GitHub Release 下载对应文件：

```text
vps-panel-linux-amd64.tar.gz
vps-panel-linux-arm64.tar.gz
```

不会安装 Docker，也不会在 VPS 上运行 Go 或 npm 构建。

> 安装前必须至少发布一个包含上述文件的 GitHub Release。推送 `v*` tag 会触发 Release 工作流并生成文件。

### 安装位置

```text
/opt/vps-panel/
├── vps-panel
└── web/

/var/lib/vps-panel/        SQLite 持久化数据
/etc/vps-panel/environment 运行配置
/etc/systemd/system/vps-panel.service
```

服务安装后通过 systemd 自动启动：

```bash
systemctl status vps-panel
```

首次打开会进入初始化页面，用于创建唯一的 admin。创建成功后初始化入口永久关闭；后续账号只能通过 admin 生成的 24 小时一次性邀请链接注册，且统一为 vip。

## Agent 安装

在 Panel 的“服务器”页面创建 Server，复制仅显示一次的 Agent 安装命令，并在目标 Debian/Ubuntu VPS 上以 root 执行。安装程序会自动检测 amd64 或 arm64、下载对应 Agent 二进制、完成一次性注册并启用 `vps-panel-agent.service`。正式 Release 生成的命令会固定下载与当前 Panel 相同版本的 Agent；开发版本未指定版本时才回退到最新 Release。

Agent 安装位置：

```text
/opt/vps-panel/agent/vps-panel-agent
/etc/vps-panel-agent/config.json
/etc/systemd/system/vps-panel-agent.service
```

注册成功后 Server 状态为 `offline`；Agent WebSocket 连接期间状态为 `online`，连接断开或 Panel 重启后恢复为 `offline`。Agent 每次连接成功后上报一次静态系统信息，每约 10 秒发送一次最小 Heartbeat，并按 1、2、4、8、16、30 秒的上限退避自动重连；异常退出时 systemd 会在 3 秒后兜底重启。服务器详情会显示最后通信时间和最近一次静态系统信息，并约每 5 秒刷新状态和动态指标。

普通“移除”只归档 Server、撤销当前 Agent 凭据并关闭在线连接，不会删除 Server 档案。admin 可以在正常或已移除 Server 的详情弹窗中统一使用“重新生成 Agent 安装令牌”；新建 Server 自动生成的首个令牌用于首次安装，管理员主动重新生成的令牌始终用于重新绑定。重新绑定成功后，Agent 才会原子替换旧配置；只有单独的“彻底删除”操作会永久删除归档 Server 及其关联数据。

生成新令牌会立即使旧的未使用令牌失效；曾注册过 Agent 的 Server 还会撤销旧 Agent 凭据并关闭在线连接，但 Server ID、档案和历史数据保持不变。首次安装和重新绑定使用相同的安装命令，无需额外覆盖参数。

正式版 Panel 可在在线 Server 详情中将 Agent 原地升级到与 Panel 相同的版本。升级通过现有 WebSocket 发送专用指令，下载固定 tag 的对应架构二进制，校验 `SHA256SUMS` 和 `version` 后才原子替换并重启。整个过程保留 `config.json`、`server_id`、`agent_id` 和 `agent_token`，不需要 Enrollment Token 或重新注册。

首次迁移不识别升级指令的旧 Agent 时，可在 Agent VPS 上执行：

```bash
curl -fsSL https://PANEL_HOST/upgrade-agent.sh | sudo bash
```

该 bootstrap 脚本只升级二进制和 systemd unit，不调用注册接口，并保留原凭据。

### `vp` 管理命令

安装完成后输入 `vp` 可打开交互菜单，也可以直接运行：

```bash
vp status
vp logs
vp restart
vp update
vp uninstall
```

域名变更仍可使用：

```bash
vp domain
```

`vp update` 会下载最新 Release，在不删除 `/var/lib/vps-panel` 数据的情况下替换程序文件并重启服务。健康检查失败时，安装脚本会尽可能恢复上一版程序。

从旧 Docker Compose 版本首次执行 `vp update` 时，脚本会停止旧容器，并将 `vps-panel_panel-data` 卷中的 SQLite 数据迁移到 `/var/lib/vps-panel`。旧 Docker 数据卷不会自动删除。

`vp uninstall` 默认保留 SQLite 数据；只有在二次确认时才会删除 `/var/lib/vps-panel`。卸载不会自动移除 Caddy 软件包，以免影响 VPS 上的其他站点。

## GitHub Release 构建

[Release 工作流](.github/workflows/release.yml)支持手动验证构建。推送以 `v` 开头的 tag 时，会构建 Vue、交叉编译两个 Linux 架构，并创建或更新对应 GitHub Release：

```bash
git tag v0.4.0
git push origin v0.4.0
```

每个压缩包的根目录只包含：

```text
vps-panel
web/
```

Release 同时直接提供：

```text
vps-panel-agent-linux-amd64
vps-panel-agent-linux-arm64
SHA256SUMS
```

## 可选 Docker 部署

`Dockerfile` 和 `deploy/docker-compose.yml` 暂时保留用于开发或兼容，但不再是默认安装路径。

```bash
git clone https://github.com/renaissance0721/vps-panel.git
cd vps-panel/deploy
PANEL_DOMAIN=panel.example.com docker compose up -d --build
```

直接使用 HTTP 时可将 `PANEL_DOMAIN` 设置为 `:80`。

## 本地开发

后端需要 Go 1.26 或更高版本：

```bash
cd panel
go run ./cmd/panel
```

前端需要 Node.js 22 或更高版本：

```bash
cd web
npm install
npm run dev
```

Vite 开发服务器会把 `/api` 请求代理到 `http://127.0.0.1:8080`。
