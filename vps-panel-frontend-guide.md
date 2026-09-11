# VPS Panel 前端 UI Guide

> 本文只约束 VPS Panel 的前端布局、列表信息密度、弹窗交互与页面稳定性。
>
> 目标是参考 Komari 这类监控面板的**布局逻辑、信息密度和整洁程度**，但不复制其品牌、图标、文案、配色、组件结构或页面实现。
>
> 当前项目继续沿用现有 Vue 3 + TypeScript + Naive UI 技术栈，不为了 UI 改造额外引入新的前端框架。

---

# 1. 总体设计目标

前端固定遵循：

```text
左侧固定 Sidebar
+
右侧宽内容区
+
列表优先
+
详情弹窗
+
信息密度高但不拥挤
```

核心目标：

- Sidebar 必须贴住浏览器左边缘。
- 主内容区必须尽可能利用剩余横向空间。
- Server / Proxy / Realm 等列表页都使用长表格或长列表，不使用大量独立卡片堆叠。
- 列表容器保持稳定，不因为切换详情、筛选、编辑而上下乱跳。
- 查看详情统一使用 Modal。
- 不使用行内展开详情。
- 不为每条资源进入单独详情路由。
- 新增 / 编辑优先使用 Modal，避免页面结构被表单撑开。
- 桌面端优先保证信息密度；移动端再做折叠适配。
- 不追求动画、渐变、大面积阴影或复杂视觉效果。

---

# 2. 参考 Komari 的部分

允许借鉴截图中的以下高层布局特征：

```text
左侧窄 Sidebar
右侧大面积内容区
顶部页面标题
标题右侧搜索 / 新增按钮
主体使用宽列表
表头浅色区分
行高统一
操作集中在最右侧
```

不要复制：

- Komari Logo
- Komari 名称
- Komari 图标组合
- Komari 原始颜色值
- Komari 原始 CSS
- Komari 原始组件代码
- Komari 的字段排列原样复刻

要求：

> 只参考“结构与信息密度”，VPS Panel 必须保持自己的视觉样式和组件实现。

---

# 3. 桌面端固定布局

## 3.1 Sidebar

桌面端 Sidebar 固定贴住屏幕左边：

```text
left: 0
top: 0
bottom: 0
position: fixed
```

建议宽度：

```text
180px ~ 200px
```

第一版可以固定：

```text
192px
```

不要在不同页面修改 Sidebar 宽度。

Sidebar 内建议：

```text
VPS Panel
────────────
概览
服务器
代理节点
Realm
订阅（实现后）
────────────
账号
退出登录
```

要求：

- Logo / 项目名位于 Sidebar 顶部。
- 账号区域固定靠底部。
- 当前页面有清晰但轻量的 active 状态。
- 不使用巨大图标。
- 不使用二级菜单时不要提前做折叠树。
- Sidebar 不跟随主内容滚动。
- 页面滚动只发生在主内容区域或浏览器主体。

---

## 3.2 主内容区

主内容从 Sidebar 右侧开始：

```text
margin-left: 192px
width: calc(100vw - 192px)
```

主内容区不要再套一个很窄的居中容器。

禁止类似：

```css
max-width: 1100px;
margin: 0 auto;
```

导致大屏左右留出大量空白。

建议：

```text
主内容左右 padding：20px ~ 28px
顶部 padding：20px ~ 24px
```

列表主容器必须尽可能拉长：

```text
width: 100%
```

桌面宽屏下，Server / Proxy / Realm 表格应该占满 Sidebar 右侧的大部分可用宽度。

---

# 4. 页面顶部固定结构

所有资源列表页统一使用：

```text
页面标题                         搜索框    + 新增
──────────────────────────────────────────────
列表容器
```

例如：

```text
服务器列表                    [搜索服务器] [+ 新增服务器]
```

```text
代理节点                      [搜索节点]   [+ 新增节点]
```

```text
Realm 转发                    [搜索规则]   [+ 新增转发]
```

要求：

- 页面标题位置固定。
- 搜索框与新增按钮固定在右侧。
- 不因为列表为空而改变工具栏位置。
- 不把新增表单直接常驻在列表上方。
- 点击“新增”打开 Modal。
- 筛选条件较少时直接放工具栏。
- 筛选条件较多时可以增加一行轻量 filter bar，但不能改变主容器宽度。

---

# 5. 列表容器硬性要求

这是前端固定规则。

## 5.1 容器位置与尺寸稳定

Server / Proxy / Realm 主列表使用固定位置的列表容器。

要求：

- 宽度固定为主内容可用宽度的 100%。
- 不随详情内容改变宽度。
- 不随新增 / 编辑表单改变高度结构。
- 不在表格行下面展开详情。
- 不把详情插入列表内部。
- 表格列顺序固定。
- 操作列固定在最右侧。
- 页面切换只替换列表内容，不重排 Sidebar 和页面骨架。

建议：

```text
min-height: calc(100vh - 页面标题与边距)
```

列表很短时也保持页面结构稳定。

列表很长时自然纵向滚动。

---

## 5.2 表格风格

参考 Komari 的“高密度长列表”思路：

- 表头使用轻微背景区分。
- 每行高度统一。
- 行间距不要过大。
- 不给每一行单独套 Card。
- 不使用大圆角卡片堆叠。
- 文本以单行优先。
- 过长内容使用省略号，并允许复制。
- IP、端口、UUID 等技术字段使用容易识别的等宽或紧凑字体。
- 状态使用轻量 Tag。
- 操作按钮使用小尺寸 icon / text button，不使用巨大主按钮。

---

# 6. Server 列表

Server 页继续作为机器层管理页面。

推荐列：

```text
名称
状态
IP 地址
Agent 版本
到期日期
本周期流量
分组
备注
操作
```

示例：

```text
JP-Tokyo-01
在线
203.0.113.10
v0.x.x
2026-12-31
38.2G / 500G
日本
入口
[查看] [编辑] [重绑] [移除]
```

IP 可以两行显示：

```text
203.0.113.10
2001:db8::10
```

如果没有 IPv6，只显示 IPv4。

本周期流量：

```text
38.2G / 500G
```

未设置总量：

```text
38.2G / 不限
```

不显示实时上传 / 下载速度。

---

# 7. Server 详情 Modal

点击“查看”后打开 Modal。

禁止：

- 跳转单独详情页。
- 表格行展开。
- 右侧抽屉挤压列表。
- 在当前列表下面插入详情。

建议 Modal 宽度：

```text
760px ~ 920px
```

内容按区块展示：

```text
基础信息
- 名称
- 状态
- Hostname
- IPv4
- IPv6
- OS
- Kernel
- Arch
- Agent Version
- Last Seen

资源
- CPU
- RAM
- Disk
- Uptime

流量
- 本周期 RX
- 本周期 TX
- 已用 / 总量
- 单向 / 双向
- 重置时间

运营信息
- 到期日期
- 分组
- 标签
- 备注
```

编辑单项信息时继续使用小 Modal，不要让详情 Modal 变成巨大编辑器。

---

# 8. Proxy / 节点列表

代理节点页面必须保持信息清晰、可扫读。

列表至少清晰标出：

```text
名称
所属 Server
入口 IP
出口 IP
端口
协议
传输层
安全 / 加密层
流控
节点域名
状态
操作
```

推荐列顺序：

```text
名称
Server
入口 IP
出口 IP
端口
协议
传输
安全层
流控
状态
操作
```

如果横向空间不足：

- `节点域名` 可以作为入口 IP 下的第二行。
- `Server` 可以显示为小号次级文本。
- 不要隐藏协议 / 端口 / 安全层 / 流控这些核心信息。

---

## 8.1 入口 IP

定义：

> 客户端连接该 Proxy 时对应的入口机器公网地址。

显示：

```text
203.0.113.10
```

如果 Proxy 配置了：

```text
public_host = jp.example.com
```

列表可以显示：

```text
203.0.113.10
jp.example.com
```

其中：

- IP 仍然清楚显示。
- 域名使用次级文本。
- 分享链接优先使用 `public_host`。
- 不因为存在域名就把入口 IP 从管理界面隐藏。

---

## 8.2 出口 IP

定义：

> 该 Proxy 所在 Server 对公网访问时实际使用的主要出口公网 IP。

第一版可以显示 Agent / Server 已知的主要公网 IP。

如果暂时不能可靠区分：

```text
入口 IP
出口 IP
```

不要伪造数据。

允许第一版在无法确定出口 IP 时显示：

```text
--
```

以后再补充出口 IP 探测能力。

出口 IP 的展示不等于主动测速。

---

## 8.3 VLESS 列表展示

VLESS 第一版固定两种：

```text
VLESS + TCP + TLS + XTLS Vision
VLESS + TCP + REALITY + XTLS Vision
```

列表必须一眼看清：

```text
协议：VLESS
传输：TCP
安全：TLS / REALITY
流控：XTLS Vision
```

如果客户端启用 UDP/443：

不要把列表的服务端 Flow 改成：

```text
xtls-rprx-vision-udp443
```

服务端显示仍然：

```text
XTLS Vision
```

可以在详情 Modal 中额外显示：

```text
客户端 UDP/443：允许
```

---

## 8.4 Shadowsocks 列表展示

至少显示：

```text
协议：Shadowsocks
端口
加密方法
入口 IP
出口 IP
```

对于不适用的列：

```text
传输
安全层
流控
```

统一显示：

```text
--
```

不要为了填满表格制造虚假概念。

---

# 9. Proxy 详情 Modal

Proxy 的所有详情使用 Modal。

建议按区块：

```text
基础
- 名称
- Server
- 状态
- 入口 IP
- 出口 IP
- public_host
- Port

协议
- Protocol
- Transport
- Security
- Flow

VLESS TLS
- UUID
- SNI
- Certificate
- Fingerprint
- Client UDP/443

VLESS REALITY
- UUID
- SNI
- Dest
- Public Key
- Short ID
- Fingerprint
- Client UDP/443

Shadowsocks
- Method
- Password / credential 状态
```

敏感字段：

- 默认遮蔽。
- 不在列表直接显示。
- private key 不默认显示。
- 需要复制时使用明确的复制按钮。
- 不在 UI 到处重复 secret。

---

# 10. Proxy 新增 / 编辑 Modal

点击：

```text
+ 新增节点
```

打开 Modal。

第一版不要做“万能协议构建器”。

推荐结构：

```text
名称
Server

协议
○ VLESS
○ Shadowsocks
```

选择 VLESS 后：

```text
端口
节点域名（可选）

传输
TCP        固定

安全
○ TLS
○ REALITY

Flow
XTLS Vision    固定

允许 UDP/443 / QUIC
开 / 关
```

TLS / REALITY 使用条件表单：

```text
TLS
→ 只展示 TLS 字段

REALITY
→ 只展示 REALITY 字段
```

不要两套字段同时出现。

---

# 11. Realm 列表

Realm 页面与 Proxy 页保持同一视觉语言。

列表建议：

```text
名称
源 Server
入口 IP
监听端口
目标 Host / IP
目标端口
网络
状态
操作
```

例如：

```text
JP → US Home
JP-Tokyo-01
203.0.113.10
9502
198.51.100.20
443
TCP
启用
[查看] [编辑] [删除]
```

如果目标是已有 Proxy：

目标列可以显示：

```text
US-Home-VLESS
198.51.100.20:443
```

第一行资源名，第二行真实 target。

如果目标是手工 host:port：

直接显示：

```text
example.com:443
```

Realm 列表不要出现无意义的：

```text
协议
TLS
XTLS
```

Realm 只显示与转发真正有关的信息。

---

# 12. Realm 详情 Modal

查看 Realm 使用 Modal。

建议：

```text
基础
- 名称
- 状态
- 源 Server

监听
- 入口 IP
- Listen Address
- Listen Port
- Network

目标
- Target Type
- Target Proxy（如有）
- Target Host
- Target Port

运行
- Realm 状态
- 最后同步状态
- 最后同步时间
```

不做行内展开。

---

# 13. 操作列

所有资源列表统一把操作放在最右侧。

推荐：

```text
查看
编辑
复制（有意义时）
删除 / 移除
```

高频、安全操作使用中性颜色。

危险操作：

```text
删除
彻底删除
撤销
```

使用红色。

不要让每行出现 6~10 个带文字的大按钮。

桌面端可以使用：

```text
icon + tooltip
```

或者：

```text
小尺寸 text button
```

保持紧凑。

---

# 14. Modal 固定规范

所有“查看详情”必须使用 Modal。

硬性要求：

```text
Server 详情 → Modal
Proxy 详情 → Modal
Realm 详情 → Modal
邀请详情 → Modal（如未来需要）
订阅详情 → Modal（如未来需要）
```

Modal 原则：

- 居中。
- 最大高度不超过 viewport。
- 内容过长时 Modal 内部滚动。
- 背景列表保持位置，不跳动。
- 关闭 Modal 后回到原列表滚动位置。
- 不改变 Sidebar。
- 不重新加载整个页面。
- 不因为打开 Modal 改变列表宽度。

建议：

```text
普通编辑：600~720px
资源详情：760~920px
复杂 Proxy 编辑：800~960px
```

不要做超宽全屏 Modal，除非后续有明确需要。

---

# 15. 搜索、筛选和排序

列表顶部保持一行工具栏。

Server：

```text
搜索
状态
分组
标签
```

Proxy：

```text
搜索
Server
协议
安全层
状态
```

Realm：

```text
搜索
源 Server
状态
```

不要在第一版做复杂 Query Builder。

排序优先支持：

```text
名称
创建时间
状态
到期日期（Server）
```

如果没有明确需求，不要为每一列都加排序。

---

# 16. 固定容器与“不能随便动”的实现要求

前端开发时必须把页面骨架当成稳定 API。

以下位置一旦确定，不要在后续 Phase 随意变化：

```text
Sidebar 宽度
页面标题位置
顶部工具栏位置
主列表容器位置
表格操作列位置
Modal 交互方式
```

后续新增字段时：

> 优先调整表格列宽、二级文本或 Modal 内容，不重做页面骨架。

禁止因为增加一个字段就：

- 把 Sidebar 改成顶部导航。
- 把表格改成卡片瀑布流。
- 把详情改成新页面。
- 把操作列移到左边。
- 把新增表单常驻在列表顶部。
- 给每个资源设计完全不同的页面结构。

Server / Proxy / Realm 必须看起来属于同一个产品。

---

# 17. 响应式规则

桌面端是第一优先级。

## >= 1200px

完整 Sidebar + 完整表格。

主内容尽可能拉宽。

## 768px ~ 1199px

Sidebar 可以保持固定或收窄。

表格允许横向滚动。

不要为了避免横向滚动就隐藏核心协议字段。

## < 768px

Sidebar 改为抽屉 / 折叠菜单。

列表可以：

- 横向滚动
- 或切换成紧凑移动列表

但：

- Server / Proxy / Realm 的信息语义保持一致。
- 详情仍使用 Modal / 全屏 Modal。
- 不重新设计一套完全不同的数据结构。

---

# 18. 前端代码组织

当前不因为 UI 改造引入：

```text
Vue Router
Pinia
新的 UI Framework
复杂 Design System
通用 Table Engine
Schema Form Engine
```

如果 `App.vue` 继续增长，可以在真正开始 Proxy / Realm 页面时做最小拆分：

```text
App.vue
components/
  AppSidebar.vue
views/
  OverviewView.vue
  ServersView.vue
  ProxiesView.vue
  RealmView.vue
```

必要时再增加：

```text
modals/
  ServerDetailModal.vue
  ProxyDetailModal.vue
  ProxyEditModal.vue
  RealmDetailModal.vue
  RealmEditModal.vue
```

原则：

> 页面开始真实存在时再拆，不提前创建空组件。

---

# 19. 视觉规范

整体保持：

```text
白 / 浅灰背景
轻边框
少量主色
高信息密度
清晰状态标签
```

避免：

- 大渐变。
- 玻璃拟态。
- 大面积阴影。
- 超大圆角。
- 每个区块都套 Card。
- 大号装饰图。
- 大面积品牌色背景。
- 动画过多。

表格表头可以使用浅色背景，以增强结构感。

Sidebar active 项使用项目自己的主色浅背景即可。

---

# 20. 前端验收

前端完成相关页面时至少验证：

1. Desktop Sidebar 紧贴屏幕左边，并固定宽度。
2. Sidebar 不随主内容滚动。
3. 主内容区从 Sidebar 右侧开始并充分利用屏幕宽度。
4. Server 列表容器明显比旧版更宽，不被窄 `max-width` 限制。
5. Server / Proxy / Realm 使用统一的长列表视觉结构。
6. 所有列表的操作列固定在最右侧。
7. Server 详情使用 Modal。
8. Proxy 详情使用 Modal。
9. Realm 详情使用 Modal。
10. 不存在列表行内展开详情。
11. 不为资源查看详情跳转单独页面。
12. Proxy 列表清晰展示入口 IP、出口 IP、端口、协议、传输层、安全层和流控。
13. VLESS TLS / REALITY 的安全层能够一眼区分。
14. VLESS 的 Flow 清晰显示 XTLS Vision。
15. Shadowsocks 对不适用字段显示 `--`，不制造虚假协议层。
16. Proxy 配置 `public_host` 后，列表仍保留入口 IP，并以次级信息显示节点域名。
17. Realm 列表清晰展示入口 IP、监听端口、目标 host / IP、目标端口和网络类型。
18. 搜索框与“新增”按钮保持在页面标题右侧区域。
19. 打开 / 关闭 Modal 不改变列表宽度和滚动位置。
20. 不新增 Vue Router / Pinia / 其他 UI Framework，除非未来出现明确需求。
21. UI 只借鉴 Komari 的布局逻辑与信息密度，不复制其品牌、代码、样式或组件实现。

---

# 21. 固定原则摘要

```text
Sidebar：
贴左、固定、稳定。

主内容：
尽可能宽，不做窄居中容器。

Server：
长列表。

Proxy：
长列表，必须看清
入口 IP / 出口 IP / 端口 /
协议 / 传输 / 安全层 / 流控。

Realm：
长列表，必须看清
入口 IP / Listen Port /
Target / Target Port / Network。

详情：
全部 Modal。

新增 / 编辑：
优先 Modal。

容器：
位置固定，不因为新增功能随意重做。

视觉：
参考 Komari 的空间利用和信息密度，
但不复制 Komari 的实现与品牌。
```
