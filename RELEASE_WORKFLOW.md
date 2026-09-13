# VPS Panel 发布与更新流程

本文用于约束 `vps-panel` 的日常开发、正式发布与服务器更新流程，避免因为频繁小改动而创建过多 Release，也避免出现“源码已经更新，但实际部署仍使用旧前端”的情况。

## 1. 基本原则

日常开发与正式发布分开处理。

日常开发：

```text
修改代码
→ 提交到 main
→ CI / build / test
→ 不创建 Release
```

阶段性功能完成后：

```text
确认 main 已包含本阶段所有目标改动
→ 创建版本 tag
→ GitHub Actions 从该 tag 对应 commit 构建
→ 创建正式 Release
→ 服务器通过 vp update 更新
```

一个 Release 是 **tag 所指向 commit 的完整代码快照**。

因此，只要某个小改动已经提交到 main，并且 tag 打在它之后，那么后续 Release 会自动包含这个改动。

例如：

```text
commit A：删除页面底部“v0.4 · 阶段 4”
commit B：删除“服务器 ID”展示
commit C：修复 Agent rebind
commit D：完成 Phase 5B

tag v0.6.0 → 指向 commit D
```

那么：

```text
v0.6.0 = A + B + C + D
```

不需要为 A、B、C 分别创建 Release。

---

## 2. main 分支

`main` 是开发主线。

普通提交到 `main` 时：

- 运行必要的 CI
- 运行 Go test / build
- 运行前端 typecheck / build
- 不自动创建 GitHub Release
- 不自动生成正式版本号
- 不要求每个 UI 小修改单独发布

例如以下修改都可以连续积累：

- 删除无意义 UI
- 调整按钮文案
- 删除 Server ID 展示
- 修复 Agent 重连
- 修复 Enrollment / rebind
- 增加 Server 字段
- 完成一个 Phase 内的多个子任务

只有在准备正式部署某个稳定阶段时，才创建 tag。

---

## 3. 正式 Release

正式 Release 只由版本 tag 触发。

例如：

```bash
git tag v0.7.0
git push origin v0.7.0
```

Release workflow 必须基于该 tag 所对应的 commit 构建完整产物。

正式 Release 应包含：

```text
Panel binary
Web frontend
Agent amd64
Agent arm64
Agent SHA256SUMS
```

其中 Panel tar.gz 应至少包含：

```text
vps-panel
web/
```

---

## 4. Release 构建要求

`.github/workflows/release.yml` 应满足以下原则。

### 4.1 使用 tag 对应 commit

Release workflow 必须 checkout 当前 tag 对应的源码状态。

不能：

- 使用未来的 main HEAD
- 使用旧工作区
- 使用其他 branch 的源码
- 使用旧构建目录

### 4.2 前端必须重新构建

正式 Release 中的前端必须由当前 tag 的源码重新构建：

```bash
cd web
npm ci
npm run build
```

然后使用本次生成的：

```text
web/dist/
```

打包进 Panel Release。

不要依赖仓库里预先存在的 `dist`。

### 4.3 Panel 和 Agent 使用同一版本

Release 构建时：

```text
panelVersion = 当前 Git tag
agentVersion = 当前 Git tag
```

例如：

```text
v0.7.0
```

正式安装 Agent 时，应优先下载与当前 Panel Release 相同版本的 Agent。

### 4.4 Agent 校验与原地升级

正式 Release 必须同时生成 `SHA256SUMS`，且至少包含：

```text
vps-panel-agent-linux-amd64
vps-panel-agent-linux-arm64
```

Panel 只允许将在线 Agent 升级到 Panel 当前的正式版本。Agent 必须先校验 SHA256 和二进制 `version` 输出，再原子替换 `/opt/vps-panel/agent/vps-panel-agent`；升级不重写或重新注册 `/etc/vps-panel-agent/config.json`。

---

## 5. Release 产物验证

不能只看源码确认 Release 正确。

必须验证 **最终真正上传到 GitHub Release 的产物**。

对于：

```text
vps-panel-linux-amd64.tar.gz
vps-panel-linux-arm64.tar.gz
```

在 workflow 中至少验证：

```text
web/index.html 存在
vps-panel binary 存在
```

并针对已经删除过的旧 UI 做回归检查。

当前明确不应再出现在正式前端中的文案包括：

```text
v0.4 · 阶段 4
服务器 ID
```

这里的“服务器 ID”仅指旧 UI 展示文案。

不要删除内部：

```text
ServerRecord.id
数据库 Server ID
API 请求中的 Server ID
Vue key
后端关联 ID
```

如果最终 tar.gz 中仍出现明确已经删除的旧 UI，则 Release workflow 应失败，不应继续发布。

---

## 6. `vp update`

`vp update` 只负责安装最新正式 Release。

语义：

```text
vp update
→ 获取 GitHub latest Release
→ 下载当前架构的 Panel tar.gz
→ 解压到 staging
→ 替换 /opt/vps-panel
→ 重启 Panel
→ 健康检查
```

`vp update` 不应直接拉取 `main` 源码。

正式服务器应始终运行正式 Release。

---

## 7. Web 更新要求

Panel 更新时，旧前端必须被新 Release 中的前端完整替换。

目标目录：

```text
/opt/vps-panel/web
```

更新过程应保证：

```text
旧 /opt/vps-panel
→ 备份或移走

新 Release
→ 解压到 staging

staging
→ 替换成为新的 /opt/vps-panel
```

不要只更新：

```text
/opt/vps-panel/vps-panel
```

而保留旧的：

```text
/opt/vps-panel/web
```

也不要简单把新前端文件覆盖到旧目录，导致旧 hashed assets 长期残留。

---

## 8. 版本确认

Panel runtime 应能够报告当前正式版本。

建议 `/api/health` 返回：

```json
{
  "status": "ok",
  "database": "ok",
  "version": "v0.7.0"
}
```

开发环境可以返回：

```json
{
  "version": "dev"
}
```

这样可以通过：

```bash
curl https://panel.example.com/api/health
```

快速确认当前服务器实际运行的 Panel 版本。

`web/package.json` 中的：

```json
"version": "0.4.0"
```

仅作为 npm package metadata。

它不应作为：

- Panel 正式版本
- 页面版本展示
- Release 版本
- Agent 版本

正式版本以 Git tag 和构建注入的 runtime version 为准。

---

## 9. 浏览器缓存

`index.html` 不应被设置为长期 immutable cache。

因为更新 Panel 后，浏览器必须能够及时获取新版入口 HTML。

带 hash 的 JS / CSS 可以依赖文件名进行缓存。

不要因为浏览器缓存问题建立复杂 CDN 机制；只需要保证：

```text
/
index.html
```

不会长期固定在旧版本。

---

## 10. 推荐开发节奏

推荐按 Phase 或稳定功能批次发布。

例如：

```text
Phase 5B 完成
→ v0.6.0

Phase 6A 完成
→ v0.7.0

Phase 6B 完成
→ v0.8.0
```

Phase 内的小改动直接提交 main：

```text
删除字段
修 UI
修 typo
补测试
修 bug
调整 Agent 行为
```

无需单独发布。

只有当：

- 当前阶段完成
- 或出现需要正式部署的关键修复

才创建新 Release。

---

## 11. 发布前检查

正式打 tag 前检查：

```bash
git status
git log --oneline -n 10
```

确认：

- 工作区没有遗漏的未提交改动
- 需要包含的小修改都已经 commit
- tag 将指向正确 commit
- CI / tests 已通过
- 前端已经能够 build
- Agent amd64 / arm64 能够 build

然后再创建正式 tag。

---

## 12. 发布后检查

Release 完成后，不只看 GitHub 页面。

应检查：

1. Release tag 指向正确 commit。
2. amd64 / arm64 Panel tar.gz 均存在。
3. amd64 / arm64 Agent binary 均存在。
4. `SHA256SUMS` 存在，包含两个 Agent binary，且本地重算结果一致。
5. 解压 Panel tar.gz 后存在：
   - `vps-panel`
   - `web/index.html`
6. Release 包不包含已经删除的旧 UI。
7. 测试服务器执行 `vp update` 后：
   - SQLite 数据保留
   - 用户数据保留
   - Server 数据保留
   - Panel binary 更新
   - Web 前端更新
   - systemd 正常
   - Caddy 正常
8. `/api/health` 返回正确版本。

---

## 13. 不要做的事情

当前阶段不要：

- 每次小修改都创建 Release
- main 每次 push 自动创建正式 Release
- 修改历史 tag
- 重复覆盖已经发布的正式版本
- 用 `web/package.json` 版本号驱动 Panel 发布
- 让正式服务器直接从 main 源码更新
- 为开发测试提前建立复杂版本兼容矩阵
- 实现 Agent 自动追 latest
- 实现 Agent 自动追 main

---

## 14. 未来开发版更新

如果以后需要高频测试 main，可以单独设计：

```text
vp update-dev
```

可能的流程：

```text
main commit
→ GitHub Actions 自动 build
→ 保存最新开发 artifact
→ 测试机通过 vp update-dev 拉取
```

但：

```text
vp update
```

仍然只负责正式 Release。

开发版更新机制应与正式发布完全分离。

当前没有真实需求时，不提前实现。

---

## 15. 最终规则

最重要的规则只有四条：

```text
1. 日常修改只提交 main，不必每次发 Release。

2. 正式 Release 是 tag 所在 commit 的完整快照。

3. 只要小改动已经在 tag 之前提交，它就会自动进入后续 Release。

4. 正式发布后必须验证实际 tar.gz 和真实升级结果，而不是只看源码。
```
