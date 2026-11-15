# 个人订阅：Mihomo 链式代理

Panel 只输出自己管理的落地节点；花云订阅留在 Clash Verge Rev，Panel 不需要花云订阅地址，也不会因本功能抓取花云。以下示例使用 Mihomo 原生 `dialer-proxy`，不使用 relay 类型策略组或客户端图形链式代理。

## Panel 设置

1. 在个人订阅中添加四个可用的本地 Proxy、Relay 或 Landing 节点，分别命名为 `美国链式落地`、`新加坡链式落地`、`菲律宾链式落地`、`德国链式落地`。
2. 展开每个节点，启用「链式代理」，将「前置代理策略组」设为 `机场入口`，保存。重新打开可检查设置。前置组名称可改，但必须与客户端最终配置完全一致。
3. 在「复制链接」选择 **Mihomo Provider**。这是携带订阅凭据的私密地址，脚本和客户端缓存也应妥善保管。

新地址为 `/sub/personal/{订阅令牌}/mihomo-provider`，遵循个人订阅原有令牌、停用及重置行为。只包含可用的所选本地节点，不包括外部订阅节点、策略组或规则，示例：

```yaml
proxies:
  - name: 美国链式落地
    type: vless
    server: EXAMPLE_SERVER
    port: 443
    uuid: EXAMPLE_UUID
    tls: true
    reality-opts:
      public-key: EXAMPLE_PUBLIC_KEY
      short-id: EXAMPLE_SHORT_ID
    dialer-proxy: 机场入口
```

普通完整 Mihomo 配置必须自行定义该策略组；Panel 会拒绝未定义的前置组和可检测到的循环。Provider 可以引用稍后由客户端定义的外部组。Base64/URI 和 Shadowrocket 不携带链式设置；Auto 取决于客户端识别，不能保证使用 Mihomo 格式。

## Clash Verge Rev 配置

1. 保留本地花云配置，关闭其自动更新并按需手动更新。选中花云配置，按照 [Clash Verge Rev 的配置文件脚本说明](https://www.clashverge.dev/guide/script.html) 为**该配置文件**添加脚本，不要将其设为所有配置的全局脚本。
2. 复制 [示例脚本](examples/clash-verge-mihomo-chain.js) 的全部内容到该脚本编辑器。把 `PANEL_PROVIDER_URL` 换成刚复制的 Provider 链接；把 `AIRPORT_NODES` 换成当前花云配置 `proxies` 中的准确节点名称；把 `LANDING_NODES` 的四个值换成 Panel 显示名称。
3. 保存并应用配置，以 Rule 模式运行。在「机场入口」选择花云节点；四个地区策略组各自选中对应落地节点。切换机场入口不需要重新生成 Panel 订阅。

脚本只把明确列出的、无 `dialer-proxy` 的内联花云节点放入机场入口，不引用全部节点或 Panel provider。若节点名不存在、重复或自身带前置代理，脚本报错；更新花云导致名称变化时需调整列表。此示例要求花云节点在当前配置的 `proxies` 中；如果花云节点只存在于另一个 provider，先使用能在本地展开其节点的花云配置，再按上述方式选择。不要把落地节点或其他链式节点加入机场入口。

脚本保留原配置的节点、其他 providers 和其他策略组，并在原规则前插入示例规则。它保留了 `vps-panel-landing` provider、`机场入口`、`AI-美国`、`X-新加坡`、`Maya-菲律宾`、`NodeSeek-德国` 这些名称：如原配置已使用这些名字，应先在脚本及 Panel 中改成不冲突的名称。重复应用不会累积脚本自己的组或相同规则。`proxy: DIRECT` 只决定客户端下载 Panel provider 的路径；如果直连 Panel 不通，可按实际网络修改为不依赖该 provider 的现有代理，避免下载依赖自身的循环。

最终配置关键结构如下，其余花云节点、规则和 providers 由原配置保留：

```yaml
proxy-providers:
  vps-panel-landing:
    type: http
    url: https://YOUR-PANEL/sub/personal/YOUR-TOKEN/mihomo-provider
    interval: 3600
    proxy: DIRECT
proxy-groups:
  - { name: 机场入口, type: select, proxies: [花云节点名称1, 花云节点名称2] }
  - { name: AI-美国, type: select, use: [vps-panel-landing], filter: '^美国链式落地$', empty-fallback: REJECT }
  - { name: X-新加坡, type: select, use: [vps-panel-landing], filter: '^新加坡链式落地$', empty-fallback: REJECT }
  - { name: Maya-菲律宾, type: select, use: [vps-panel-landing], filter: '^菲律宾链式落地$', empty-fallback: REJECT }
  - { name: NodeSeek-德国, type: select, use: [vps-panel-landing], filter: '^德国链式落地$', empty-fallback: REJECT }
rules:
  - DOMAIN-SUFFIX,openai.com,AI-美国
  - DOMAIN-SUFFIX,chatgpt.com,AI-美国
  - DOMAIN-SUFFIX,x.com,X-新加坡
  - DOMAIN-SUFFIX,twitter.com,X-新加坡
  - DOMAIN-SUFFIX,maya.ph,Maya-菲律宾
  - DOMAIN-SUFFIX,nodeseek.com,NodeSeek-德国
  - GEOIP,CN,DIRECT
  # 原有规则继续保留在后面
```

示例域名不覆盖各服务的全部接口；可以自行增删规则，注意规则从上到下匹配且 `GEOIP,CN,DIRECT` 排在原规则之前。策略组节点名必须与 Provider 一致；不存在时示例使用 `REJECT`，避免意外直连。实际链路还受入口协议、UDP、落地节点协议与网络兼容性影响，参见 [Mihomo dialer-proxy](https://wiki.metacubex.one/config/proxies/dialer-proxy/)、[代理集合](https://wiki.metacubex.one/config/proxy-providers/) 和 [策略组](https://wiki.metacubex.one/config/proxy-groups/) 官方文档。
