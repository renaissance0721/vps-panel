# VPS Panel

多 VPS 管理面板。目前已完成 **Phase 4.5** 和 **Phase 5A**：提供 admin / vip 两级邀请制账号认证、Server 管理、一次性 Agent 注册、认证 WebSocket 长连接、SQLite、健康检查，以及 Panel 和 Agent 的原生 Linux + systemd 部署。

当前 Agent 只支持注册、保存长期凭据和建立一次认证 WebSocket 连接，尚未实现 Heartbeat、自动重连、监控、代理内核或端口转发。

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

在 Panel 的 `Servers` 页面创建 Server，复制仅显示一次的 Agent 安装命令，并在目标 Debian/Ubuntu VPS 上以 root 执行。安装程序会自动检测 amd64 或 arm64、下载对应 Agent 二进制、完成一次性注册并启用 `vps-panel-agent.service`。

Agent 安装位置：

```text
/usr/local/bin/vps-panel-agent
/etc/vps-panel-agent/config.json
/etc/systemd/system/vps-panel-agent.service
```

注册成功后 Server 状态为 `offline`；Agent WebSocket 连接期间状态为 `online`，连接断开或 Panel 重启后恢复为 `offline`。

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
