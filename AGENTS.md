# Codex Development Rules

本项目优先：简单、可运行、可验证、最小实现。
没有被当前任务要求的功能，不实现。

架构见 [DEV.md](DEV.md)，使用说明见 [README.md](README.md)，正式协议见 [Agent API v1](docs/agent-api-v1.md)。
本文件约束执行方式，不维护功能阶段或开发历史。

## 1. 开始前

1. 阅读当前真实代码和任务相关说明，不按通用模板猜实现。
2. 从 API route / DTO、Service、schema / migration 和对应 UI 确认行为。
3. 搜索现有函数、组件、类型、测试及 helper，明确哪些可以复用。
4. 列出完成任务所需的最少文件，再开始编码。
5. 文档与代码冲突时先核对事实，不为迎合旧文档改动正常代码。

## 2. 任务范围与最小 diff

- 只完成当前明确要求的功能或问题，不顺带实现下一项需求。
- 不添加未要求的配置、协议、内核、页面或服务。
- 不以未来可能使用为理由预建表、目录、空接口或 TODO scaffold。
- 优先修改已有实现，不重复造功能相同的 helper。
- 不顺便美化代码、改 UI 风格或移动目录。
- 只有明确 bug、安全问题、阻塞当前需求或直接造成重复的代码，才考虑相关修正。
- 用户限定只改文档时，发现代码问题应报告，不修业务代码。
- 保留用户已有改动，不覆盖与当前任务无关的工作。

## 3. 架构与依赖

- 沿用现有 Go、SQLite、Vue 3、TypeScript、Naive UI 和单体架构。
- Panel 保存业务状态；Agent 执行目标 VPS 操作；Web 不替代后端校验。
- 优先使用已有 Handler → Service → Database 路径，不机械增加层级。
- 未出现真实多个实现或必要解耦需求时，不新增 Provider / Factory / Adapter 或仅有一个实现的接口。
- 不为未来扩展增加 EventBus、插件系统、消息队列、缓存或新数据库。
- 不主动更换通信协议、ORM、前端框架或服务管理方式。
- 新依赖必须是当前需求必需，且标准库 / 已有依赖不能合理完成。
- 新增依赖时在最终报告写明名称和原因；不为几行代码引入大型库。

## 4. 数据库

- schema 改动同时覆盖空库初始化、版本 migration 和迁移测试。
- 沿用现有 migration 入口，不另建迁移体系。
- 修改 `CREATE TABLE IF NOT EXISTS` 不能代替已有数据库升级。
- 保留历史数据，验证外键、唯一约束、默认值和引用关系。
- 重建表时检查索引、自增序列及旧库升级路径。
- 不删除数据库来规避迁移；不在长期文档重复迁移版本清单。
- 数据修改沿用已有事务边界，避免业务记录与 desired state 状态脱节。

## 5. Agent、capability 与连接

- 区分 implementation、软件 version 和 API version。
- 新功能支持判断优先使用明确 capability，复用现有判断函数。
- 不按 implementation 名称或版本号猜功能；官方升级的身份校验按既有协议执行。
- Legacy 兼容仅沿用协议明确规定的例外，不将旧兜底扩大到新能力。
- 在线诊断、探测下发及接收使用当前连接的能力。
- 未知 WebSocket message type 必须保持兼容，不因新增类型中断旧 Agent。
- Panel → Agent 消息复用已有 connection write synchronization，不能绕过 write mutex 并发写 socket。
- Agent → Panel 消息复用既有写循环；新增状态写入检查 current connection，防止旧连接覆盖新状态。
- 配置变更复用 desired state 与结果回报，不另外建立远程命令通道。
- 不提供任意 shell 执行接口。
- 修改注册、认证、字段、capability 或消息语义时同步 [Agent API v1](docs/agent-api-v1.md)。

## 6. 受管资源与生命周期

- 区分保存配置、通知送达、Agent 应用成功，不提前报告成功。
- 复用现有配置校验、端口预留、候选配置和失败恢复流程。
- 区分禁用资源、删除最后一条配置、归档 Server 和远端自卸载。
- 清理操作检查受管资源归属，不删除其他软件的服务、目录或防火墙规则。
- Panel 更新 / 卸载不得误删同机 Agent，反向操作也一样。
- 订阅生成的 Client / Relay 沿用专门生命周期，不绕过其管理约束。
- 升级和清理能力遵循现有安全边界，不因实现方便放宽权限。

## 7. 权限、Secrets 与日志

- 每个新增 API 都在后端执行角色与资源权限检查，不能只隐藏按钮。
- 复用现有四种角色和门禁；Server 归属、创建来源与访问 ACL 不互相替代。
- 不假定管理员自动绕过所有资源权限。
- Server access 只允许 admin / vip；user / subscriber 使用独立门户和资源分配，不能通过 server_access 授权。
- 不把完整凭据或私钥放进普通列表 DTO。
- 分享、订阅、导出和备份接口同样检查授权与敏感信息边界。

日志、错误响应和调试输出不得包含：

- Password、Agent Token、Enrollment Token。
- Telegram Bot Token、Subscription Token。
- Shadowsocks credential、UUID 凭据。
- REALITY private key、TLS private key。

上游错误字符串也可能含秘密，不应未经处理直接返回或打印。
日志聚焦连接变化、注册失败、数据库错误、任务失败和服务异常，不增加大量进出函数日志。
错误处理保留有用上下文，不建立不必要的多层自定义 Error。

## 8. 前端与排序

- 使用 Vue 3 + TypeScript + Naive UI，沿用现有页面切换方式。
- 无任务要求不引入 React、Tailwind、Pinia、Vue Router 或新 UI framework。
- 优先复用 API client、类型、format helpers、领域组件、CSS variables 和共享样式。
- 不为单一页面创建大量通用组件或设计系统。
- 新可排序列表优先复用 `web/src/drag.ts`、`web/src/reorder.ts` 和后端 listorder。
- 不另写一套 drag preview / auto-scroll；根据列表实际存储方式选择持久化入口。
- 列表顺序属于用户偏好时，不修改 Agent desired-state version。
- 协调轮询与拖拽；失败恢复顺序，卸载时清理监听器、计时器和请求。
- 验证受影响的窄屏布局与实际交互，不只看类型检查通过。

## 9. Monitor、Traffic 与 Notification

- Monitor 复用已有 `ServerRecord` / API 和共享轮询。
- Probe 与 Server 管理职责分离，不把管理详情重新塞进 Monitor。
- CPU / RAM / Disk 最新指标与 Probe 延迟历史分开描述和处理。
- TCP / ICMP 按各自 capability 判断，不按 Agent 名称猜测。
- 保留失败、无权限、无样本的区别，不把未知数据当作正常零值。
- Server 和 Client 流量保持原有口径；通知复用正式流量计算，不另算一套。
- 通知复用现有 Panel watcher、持久化状态和异步发送路径，不阻塞 Agent 消息处理。
- 不顺便加入任务未要求的通知渠道或告警类型。

## 10. 第三方项目参考

可研究 Komari、monitor-probe 等项目的产品思路和架构思想。
不得复制源码、逐函数翻译、改变量名复用，或复制 CSS、数据库 schema、API schema、安装脚本。

协议实现优先参考官方上游规范，并根据本仓库现有模型独立实现。
不因其他项目已有某功能，就将其加入当前任务范围。

## 11. 验证要求

- Go 修改运行 gofmt；小改运行相关测试和 build。
- 涉及核心 backend、schema 或 protocol 时，在 `panel` 运行 `go test ./...`，并执行相关 vet / build。
- 涉及前端时，在 `web` 运行 `npm test` 和 `npm run build`。
- `npm run build` 已包含类型检查；不虚构不存在的 lint 或 typecheck 脚本。
- 测试覆盖权限、状态转换、解析、配置生成、流量边界和迁移等关键行为。
- 不为了覆盖率堆 getter / constructor 测试，不为无关代码反复跑昂贵测试。
- 安装脚本变更验证语法及受影响路径；涉及卸载或迁移时验证资源隔离。
- 纯文档修改检查 Markdown、链接、命令和事实，不要求无关业务测试。
- 验证命令与目录见 [DEV.md](DEV.md#19-testing)。
- 测试无法运行时说明具体原因；只有真实执行成功才能报告通过。

## 12. 文档同步

- Agent API / capability 变化更新正式协议文档。
- install command、support matrix、public API 变化同步相关文档。
- README 面向使用者；DEV 解释架构；AGENTS 规定执行；Agent API 维护协议细节。
- 使用交叉链接，避免多份文档复制完整 JSON、安装教程或规则段落。
- 小 UI 调整不必修改 README；不维护固定 commit 快照或阶段禁止清单。
- 不硬编码受管 Xray / Realm 或当前 Panel 的 Release 版本到长期文档。

## 13. 完成前与最终报告

检查 diff 是否只包含任务所需变更，删除额外功能、未使用代码、空实现、重复 helper 和不必要依赖。
核对格式、build、相关测试与文档；未验证的行为不能写成已完成。

最终只报告：

- **完成**：实际实现或修正的内容。
- **修改文件**：主要文件及职责。
- **验证**：实际执行的测试、build 或文档检查及结果。
- **未实现 / 已知限制**：当前范围外内容和确实存在的限制。

不自动提出长篇未来规划；用户另有输出要求时按其要求提供。
