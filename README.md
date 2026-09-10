# VPS Panel

多 VPS 管理面板。目前处于 **v0.1 / Phase 1**：提供可部署的 Panel 基础、SQLite、基础 Web 页面、健康检查和 Caddy 域名反代。

当前尚未实现登录、服务器管理、Agent、监控、WebSocket、代理内核或端口转发。

## VPS 部署

要求：

- Debian 或 Ubuntu VPS
- 使用域名时，域名的 A/AAAA 记录已经指向 VPS，并放行 TCP 80、TCP/UDP 443

### 一键安装

使用 VPS IP，通过 HTTP 访问：

```bash
curl -fsSL https://raw.githubusercontent.com/renaissance0721/vps-panel/main/scripts/install-panel.sh | sudo bash
```

使用域名并自动配置 HTTPS：

```bash
curl -fsSL https://raw.githubusercontent.com/renaissance0721/vps-panel/main/scripts/install-panel.sh | sudo bash -s -- \
  --domain panel.example.com
```

脚本会在缺少 Docker 时通过 Docker 官方软件源安装 Docker Engine、Buildx 和 Compose 插件，然后把项目安装到 `/opt/vps-panel`、启动容器，并等待 Panel 健康检查通过。再次执行相同命令即可更新；不传 `--domain` 时会保留已有域名配置。

> 一键安装地址只有在本次代码推送到 GitHub `main` 分支后才会生效。

### 手动安装

手动安装前需要自行准备 Docker Engine 和 Docker Compose 插件。

```bash
git clone https://github.com/renaissance0721/vps-panel.git
cd vps-panel/deploy
cp .env.example .env
```

直接使用 VPS IP 访问时，保留 `.env` 中的默认值：

```dotenv
PANEL_DOMAIN=:80
```

使用域名和自动 HTTPS 时，改为自己的域名：

```dotenv
PANEL_DOMAIN=panel.example.com
```

启动：

```bash
docker compose up -d --build
docker compose ps
```

随后访问 `http://VPS_IP`，或使用域名时访问 `https://panel.example.com`。Caddy 会自动申请并续期 HTTPS 证书。

健康检查：

```bash
docker compose exec panel /app/vps-panel healthcheck
```

查看日志：

```bash
docker compose logs -f
```

更新：

```bash
git pull
docker compose up -d --build
```

`docker compose down` 不会删除 Panel 数据或 Caddy 证书。不要执行 `docker compose down -v`，除非确定要删除这些持久数据。

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
