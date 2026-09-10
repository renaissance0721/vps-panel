# Codex Development Rules

## 1. 核心原则

本项目优先：

**简单、可运行、可验证、最小实现。**

不要为了未来可能出现的需求提前增加复杂度。

当前任务没有明确要求的功能，不实现。

---

## 2. 严格控制任务范围

每次开发只完成当前明确指定的 Phase 或 Issue。

禁止：

- 顺便实现下一阶段功能
- 顺便重构无关模块
- 顺便修改 UI 风格
- 顺便增加新配置项
- 顺便增加新的协议或内核
- 为未来功能提前创建大量接口、表、目录或空实现

如果当前任务是：

```text
实现 Agent 注册
```

则只实现 Agent 注册所必需的代码。

不要同时实现：

```text
Realm
Xray
流量统计
订阅
客户端
Chain
```

---

## 3. 最小修改原则

修改已有项目时：

1. 先阅读现有实现。
2. 优先修改已有代码。
3. 不要在已有实现足够的情况下新建重复模块。
4. 不要重写能够正常工作的代码。
5. 不要修改和当前任务无关的文件。

始终选择：

**完成需求所需的最小 diff。**

---

## 4. 禁止过度抽象

除非当前至少存在两个真实实现，否则不要为了未来扩展提前创建复杂抽象。

例如第一版只有 SQLite 时，不要提前实现：

```text
DatabaseProvider
SQLiteProvider
PostgresProvider
MySQLProvider
DatabaseFactory
```

直接使用 SQLite 即可。

以后真正增加 PostgreSQL 时再重构。

---

如果当前只有一种 Agent 通信方式，不要提前实现：

```text
Transport
WebSocketTransport
GRPCTransport
HTTPTransport
TransportFactory
```

当前直接实现 WebSocket。

---

## 5. 不要为未来功能写代码

禁止以以下理由增加实现：

```text
以后可能需要
未来方便扩展
后续也许会支持
为了更加通用
为了企业级
为了 scalability
```

未来需求出现时再改。

---

## 6. 控制架构层级

不要机械套用复杂企业架构。

简单功能优先：

```text
Handler
↓
Service
↓
Database
```

如果 Handler 可以清晰完成的小功能，不需要强制增加：

```text
Controller
UseCase
Repository
Adapter
Provider
Manager
Factory
Facade
Coordinator
```

只有在代码已经产生明显重复或复杂性时再抽象。

---

## 7. 不要创建无用接口

接口只在存在实际需要时创建。

不要出现：

```go
type ServerRepository interface {}
```

然后项目中永远只有：

```go
SQLiteServerRepository
```

且没有替换实现的实际需求。

能直接使用 struct 时优先直接使用 struct。

---

## 8. 不要提前创建空模块

禁止创建：

```text
realm/
xray/
singbox/
mihomo/
subscription/
chain/
billing/
```

然后里面只有：

```text
TODO
placeholder
coming soon
```

没有当前功能时不要创建。

---

## 9. 控制依赖

新增第三方依赖之前必须满足：

1. 当前需求确实需要。
2. 标准库或现有依赖无法合理解决。
3. 引入该依赖明显减少复杂度。

不要为了几行代码引入大型库。

每次新增 dependency 时，在最终说明中明确：

```text
新增依赖：
xxx

原因：
xxx
```

如果没有必要，不新增。

---

## 10. 不要重复造相同功能

创建新函数之前先搜索项目。

避免出现：

```text
GetServerIP()
ResolveServerIP()
FindServerAddress()
GetPrimaryAddress()
```

实际上执行相同逻辑。

优先复用现有实现。

---

## 11. 不要做无关重构

实现功能时发现旧代码“不够漂亮”，但不影响当前任务：

不要修改。

只有以下情况允许顺带修：

- 明确 bug
- 阻塞当前功能
- 安全问题
- 会直接造成当前实现重复或不可维护

否则记录即可，不执行。

---

## 12. 不主动改变技术栈

除非任务明确要求，否则不要：

- 更换 Web Framework
- 更换 ORM
- 更换数据库
- 更换前端框架
- 更换 UI 库
- 更改项目目录结构
- 更改通信协议

---

## 13. 数据库设计保持当前需求

不要为了未来需求提前加入大量字段。

当前 Server 如果只需要：

```text
id
name
hostname
os
arch
status
last_seen
```

就只实现这些实际使用字段。

真正需要新字段时通过 migration 添加。

---

## 14. 不要提前优化性能

除非发现实际性能问题，否则不要：

- 加缓存
- Redis
- 消息队列
- Worker Pool
- Event Bus
- 分布式锁
- CQRS
- 微服务

第一版保持单体架构。

---

## 15. 错误处理要求

错误处理要完整，但不要过度包装。

推荐：

```go
if err != nil {
    return fmt.Errorf("register agent: %w", err)
}
```

避免十几层自定义 Error 类型。

只有 API 明确需要区分的错误才定义专门错误类型。

---

## 16. 日志要求

只记录实际有价值的信息。

禁止大量：

```text
enter function
leave function
starting operation
operation initialized
processing...
```

正常流程保持精简。

重点记录：

```text
Agent connect/disconnect
注册失败
数据库错误
任务失败
服务异常
```

不要记录：

- Token
- Password
- UUID 凭据
- Reality Private Key
- Secret

---

## 17. 测试原则

测试当前功能的关键逻辑。

不要为了追求覆盖率给简单 getter、struct constructor 等写大量低价值测试。

重点测试：

```text
认证
注册
状态变化
数据解析
流量计算
任务执行
配置生成
```

---

## 18. 前端原则

前端第一目标是能用。

不要提前：

- 动画
- 大量渐变
- 地图
- 复杂 Dashboard
- 自定义设计系统
- 大量通用组件
- 过度封装表单

如果一个页面只使用一次，可以直接实现。

出现重复后再抽组件。

---

## 19. 当前阶段禁止实现

除非任务明确要求，否则当前不要实现：

```text
Xray
sing-box
Mihomo
Realm

Proxy
Client
Endpoint
Chain

Subscription
QR Code

多用户
RBAC
Billing

Redis
PostgreSQL

消息队列
插件系统
License
```

---

## 20. 每个任务开始前

先执行：

1. 阅读相关代码。
2. 明确最少需要修改哪些文件。
3. 检查现有实现是否可以复用。
4. 再开始编码。

不要看到需求后立即创建大量新文件。

---

## 21. 每个任务结束前

检查：

### 功能

当前要求是否全部完成？

### 范围

是否加入了任务没有要求的功能？

如果有，删除。

### 代码

是否出现：

- 未使用代码
- 未使用接口
- 空实现
- TODO scaffold
- 重复 helper
- 不必要 dependency

如果有，删除。

### 验证

运行：

```text
format
build
相关测试
```

确保项目仍可运行。

---

## 22. 输出修改报告

每次完成任务后，只报告：

### 完成

实际完成的内容。

### 修改文件

列出主要修改文件。

### 验证

运行了什么测试或 build。

### 未实现

明确指出哪些未来功能没有实现。

不要额外提出或实现大量未来功能。

---

# 最重要规则

当存在两种实现方式时：

```text
A：简单、直接、代码少、满足当前需求

B：高度抽象、可扩展、代码很多、可能未来有用
```

默认选择：

**A。**

只有当前需求已经证明 A 无法合理满足时，才选择 B。

本项目不追求一次性设计出最终架构。

本项目采用：

**需要什么，实现什么；出现重复，再抽象。**