import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
const portalSource = await readFile(new URL('../src/views/SubscriberPortalView.vue', import.meta.url), 'utf8')
const managementSource = await readFile(new URL('../src/views/SubscriptionManagementView.vue', import.meta.url), 'utf8')
const groupEditorSource = await readFile(new URL('../src/components/subscription/RoutingGroupEditor.vue', import.meta.url), 'utf8')
const relaySource = await readFile(new URL('../src/views/RelaysView.vue', import.meta.url), 'utf8')
const clientSource = await readFile(new URL('../src/components/proxy/ClientList.vue', import.meta.url), 'utf8')
const styleSource = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')

test('订阅用户登录进入独立 Subscriber Portal 且不加载管理端数据', () => {
  assert.match(appSource, /state\.value\.user\?\.role === 'user' \|\| state\.value\.user\?\.role === 'subscriber'[\s\S]*stopServerPolling\(\)[\s\S]*return/)
  assert.match(appSource, /<SubscriberPortalView[^>]*state\.user\?\.role === 'subscriber'/)
  assert.doesNotMatch(portalSource, /<UserPortalView|sidebar|\/api\/(?:servers|proxies|relays|overview)/)
  for (const endpoint of ['/api/subscriber/me', '/api/subscriber/subscription/regenerate']) {
    assert.match(portalSource, new RegExp(endpoint.replaceAll('/', '\\/')))
  }
  assert.doesNotMatch(portalSource, /\/api\/subscriber\/(?:nodes|password-change-request)/)
})

test('Subscriber Portal 将套餐和导入入口收拢到单一卡片', () => {
  for (const label of ['套餐信息', '下次重置', '到期', '可用节点', '导入订阅', '重新生成订阅', 'Base64 通用订阅', 'Clash / Mihomo', '扫描二维码订阅', '复制地址']) {
    assert.match(portalSource, new RegExp(label))
  }
  assert.match(portalSource, /subscriber\.plan_name/)
  assert.match(portalSource, /subscriber\.used_bytes/)
  assert.match(portalSource, /subscriber\.traffic_limit_bytes/)
  assert.match(portalSource, /class="portal-section-title">套餐信息/)
  assert.match(portalSource, /<n-card[\s\S]*subscriber-plan-actions[\s\S]*importModalOpen = true/)
  assert.match(portalSource, /subscription-import-card[\s\S]*regenerateSubscription[\s\S]*重新生成订阅/)
  assert.match(portalSource, /copySubscription\('base64', subscriber\?\.subscription_base64_url/)
  assert.match(portalSource, /copySubscription\('mihomo', subscriber\?\.subscription_mihomo_url/)
  assert.match(portalSource, /<QRCodeModal[\s\S]*:uri="subscriber\?\.subscription_auto_url \|\| ''"/)
  assert.equal([...portalSource.matchAll(/<QRCodeModal/g)].length, 1)
  assert.match(portalSource, /modal-title="扫描二维码订阅"/)
  assert.match(portalSource, /instruction="使用支持订阅二维码的客户端扫描导入。"/)
  assert.match(portalSource, /Object\.assign\(subscriber\.value, value\)/)
  assert.doesNotMatch(portalSource, /我的订阅|申请修改密码|subscriber-node-list/)
  assert.doesNotMatch(portalSource, /<h1>可用节点<\/h1>|<h1>账号<\/h1>|<h1>订阅<\/h1>/)
  assert.doesNotMatch(portalSource, /server_name|proxy_name|client_name|UUID|SS Password|REALITY|Realm|添加中转|自定义落地/)
})

test('Subscriber Portal 无套餐时只渲染极简空态且不开放订阅操作', () => {
  assert.match(portalSource, /const hasPlan = computed\(\(\) => \(subscriber\.value\?\.plan_name \?\? ''\)\.trim\(\) !== ''\)/)
  assert.match(portalSource, /<section v-if="!hasPlan" class="subscriber-empty-state">[\s\S]*尚未开通套餐[\s\S]*当前账号尚未开通任何套餐[\s\S]*<section v-else>/)
  assert.match(portalSource, /<n-modal v-if="hasPlan" v-model:show="importModalOpen">/)
  assert.match(portalSource, /<QRCodeModal\s+v-if="hasPlan"/)
})

test('Subscriber Portal 品牌区域使用统一轻量玻璃风格', () => {
  assert.match(portalSource, /class="app-brand app-brand--portal">夕凪云/)
  assert.match(styleSource, /\.app-brand\s*\{[\s\S]*background: linear-gradient[\s\S]*backdrop-filter: blur/)
  assert.match(styleSource, /\.subscriber-empty-state\s*\{[\s\S]*place-content: center/)
})

test('订阅管理只在 Admin Sidebar 显示且包含三个 Tab', () => {
  assert.match(appSource, /v-if="state\.user\?\.role === 'admin'"[\s\S]*currentPage === 'subscriptions'[\s\S]*订阅管理/)
  assert.match(appSource, /currentPage === 'subscriptions' && state\.user\?\.role === 'admin'/)
  assert.doesNotMatch(appSource, /role === 'vip'[^\n]*subscriptions/)
  for (const label of ['订阅用户', '套餐', '发布节点']) {
    assert.match(managementSource, new RegExp(`label: '${label}'`))
  }
})

test('订阅管理覆盖用户、套餐和发布节点操作且固定已创建节点拓扑', () => {
  for (const endpoint of [
    '/api/admin/subscription/users',
    '/api/admin/subscription/plans',
    '/api/admin/subscription/nodes',
    '/api/admin/subscription/relay-servers',
    '/traffic/reset',
    '/token/regenerate',
  ]) {
    assert.match(managementSource, new RegExp(endpoint.replaceAll('/', '\\/')))
  }
  assert.match(managementSource, /v-if="!editingNode"[\s\S]*中转服务器[\s\S]*落地 Proxy/)
  assert.match(managementSource, /v-for="server in relayServers"[\s\S]*\{\{ server\.name \}\}/)
  assert.match(managementSource, /创建后不能修改模式、中转服务器或落地 Proxy/)
  assert.match(managementSource, /source_server_id: nodeMode\.value === 'relay' \? nodeSourceServerID\.value : null/)
  assert.match(managementSource, /source_server_name \}\} · Realm →/)
  assert.doesNotMatch(managementSource, /source_proxy_id|source_proxy_name|nodeSourceProxyID|中转 Proxy/)
  assert.match(managementSource, /movePlanNode/)
  assert.match(managementSource, /node_ids: planNodeIDs\.value/)
})

test('套餐布尔状态使用 NSwitch 双向绑定并原样写入保存请求', () => {
  assert.match(managementSource, /import \{[^}]*NSwitch[^}]*\} from 'naive-ui'/)
  assert.match(managementSource, /<n-switch v-model:value="planEnabled"/)
  assert.match(managementSource, /<n-switch v-model:value="nodeEnabled"/)
  assert.match(managementSource, /<n-switch v-model:value="userEnabled"/)
  assert.match(managementSource, /function openCreatePlan\(\)[\s\S]*planEnabled\.value = true/)
  assert.match(managementSource, /function openEditPlan\(value: Plan\)[\s\S]*populatePlanForm\(value\)/)
  assert.match(managementSource, /function populatePlanForm\(value: Plan\)[\s\S]*planEnabled\.value = value\.enabled/)
  assert.match(managementSource, /name, subscription_title: planSubscriptionTitle\.value\.trim\(\)/)
  assert.match(managementSource, /enabled: planEnabled\.value, traffic_limit_bytes: trafficLimit/)
})

test('套餐表单区分后台名称与订阅显示名称', () => {
  assert.match(managementSource, /subscription_title: string/)
  assert.match(managementSource, /const planSubscriptionTitle = ref\(''\)/)
  assert.match(managementSource, /planSubscriptionTitle\.value = value\.subscription_title/)
  assert.match(managementSource, /订阅显示名称/)
  assert.match(managementSource, /客户端导入订阅后显示的名称。留空则使用套餐名称。/)
})

test('套餐保存使用 Modal 独立错误、前端校验和防重复提交', () => {
  assert.match(managementSource, /const planFormError = ref\(''\)/)
  assert.match(managementSource, /async function savePlan\(\) \{\s*if \(busy\.value\) return\s*planFormError\.value = ''/)
  assert.match(managementSource, /套餐名称不能为空/)
  assert.match(managementSource, /流量额度必须是有限且不小于 0 的数字/)
  assert.match(managementSource, /String\(planTrafficGiB\.value \?\? ''\)\.trim\(\)/)
  assert.match(managementSource, /<form class="auth-form" novalidate @submit\.prevent="savePlan">/)
  assert.match(managementSource, /v-if="planFormError"[\s\S]*\{\{ planFormError \}\}/)
  assert.match(managementSource, /:loading="busy" :disabled="busy">保存<\/n-button>/)
  assert.doesNotMatch(managementSource, /planNodeIDs\.value\.length\s*(?:===?|<=?)\s*0/)
})

test('发布节点可手动输入倍率且管理端统一显示最终名称', () => {
  assert.match(managementSource, /NInputNumber/)
  assert.match(managementSource, /v-model:value="nodeTrafficMultiplier"/)
  assert.match(managementSource, /:min="0\.1"/)
  assert.match(managementSource, /:max="5"/)
  assert.match(managementSource, /:precision="2"/)
  assert.match(managementSource, /实际使用 1 GB 时，按该倍率计入套餐流量。允许 0\.10×–5\.00×。/)
  assert.match(managementSource, /traffic_multiplier: multiplier/)
  assert.match(managementSource, /function nodeDisplayName\(value: PublishedNode\)/)
  assert.match(managementSource, /`\$\{value\.name\} \[\$\{Number\(value\.traffic_multiplier\.toFixed\(2\)\)\}×\]`/)
  assert.match(managementSource, /:title="nodeDisplayName\(value\)"/)
  assert.match(managementSource, /<span>\{\{ nodeDisplayName\(node\) \}\}<\/span>/)
  assert.match(managementSource, /function openEditNode\(value: PublishedNode\)[\s\S]*nodeName\.value = value\.name/)
  assert.doesNotMatch(portalSource, /traffic_multiplier|multiplierLabel/)
})

test('发布节点创建和编辑一次提交所属套餐并在列表展示关系', () => {
  assert.match(managementSource, /const nodePlanIDs = ref<number\[]>\(\[]\)/)
  assert.match(managementSource, /function openCreateNode\(\)[\s\S]*nodePlanIDs\.value = \[]/)
  assert.match(managementSource, /function openEditNode\(value: PublishedNode\)[\s\S]*nodePlanIDs\.value = plans\.value\.filter/)
  assert.match(managementSource, /function toggleNodePlan\(id: number, checked: boolean\)/)
  assert.match(managementSource, /method: 'PATCH'[\s\S]*plan_ids: nodePlanIDs\.value/)
  assert.match(managementSource, /method: 'POST'[\s\S]*plan_ids: nodePlanIDs\.value/)
  assert.match(managementSource, /editingNode \? '所属套餐' : '加入套餐'/)
  assert.match(managementSource, /v-for="plan in plans"[\s\S]*nodePlanIDs\.includes\(plan\.id\)/)
  assert.match(managementSource, /所属套餐：\{\{ nodePlanNames\(value\.id\) \|\| '未加入套餐' \}\}/)
  assert.match(managementSource, /nodeModalOpen\.value = false\s*await loadAll\(\)/)
})

test('发布节点入口地址和 Relay 端口支持继承、候选复制与自定义', () => {
  for (const label of ['入口地址', '继承落地节点', '已有地址', '自定义', '入口端口', '自动分配', '最终入口']) {
    assert.match(managementSource, new RegExp(label))
  }
  assert.match(managementSource, /proxy\.server_id !== nodeEntryAddressServerID\.value/)
  assert.match(managementSource, /proxy\.entry_host_mode !== 'manual'/)
  assert.match(managementSource, /const key = host\.toLowerCase\(\)/)
  assert.match(managementSource, /existing\.sources\.includes\(proxy\.name\)/)
  assert.match(managementSource, /保存时只复制地址文本，不建立对来源 Proxy 的依赖。/)
  assert.match(managementSource, /entry_host_mode: entryHostMode/)
  assert.match(managementSource, /entry_host: entryHost/)
  assert.match(managementSource, /entry_port_mode: entryPortMode/)
  assert.match(managementSource, /entry_port: nodeMode\.value === 'relay'/)
  assert.match(managementSource, /value\.entry_address \}\}:\{\{ value\.entry_port/)
  assert.doesNotMatch(managementSource, /entry_source_proxy_id|entry_port\s*:\s*nodeMode\.value === 'direct'/)
})

test('生命周期字段只在订阅用户表单管理', () => {
  for (const field of ['userResetMode', 'userResetDay', 'userResetTime', 'userBillingMonths']) {
    assert.match(managementSource, new RegExp(field))
  }
  assert.match(managementSource, /traffic_reset_mode: userResetMode\.value/)
  assert.match(managementSource, /billing_period_months: userBillingMonths\.value \|\| null/)
  assert.doesNotMatch(managementSource, /planResetMode|planResetDay|planResetTime|planValidityDays|planBillingMonths/)
  assert.doesNotMatch(managementSource, /默认有效天数/)
})

test('套餐节点保存失败保留 Modal、说明部分成功并刷新服务端状态', () => {
  assert.match(managementSource, /let planSaved = false/)
  assert.match(managementSource, /let nodesSaved = false/)
  assert.match(managementSource, /套餐基本信息已保存，但节点列表保存失败：\$\{message\}/)
  assert.match(managementSource, /套餐已创建，但节点列表保存失败：\$\{message\}/)
  assert.match(managementSource, /if \(planSaved && !nodesSaved\)[\s\S]*await loadAll\(\)[\s\S]*populatePlanForm\(current\)/)
  assert.match(managementSource, /await loadAll\(\)\s*planModalOpen\.value = false/)
  assert.match(managementSource, /function movePlanNode/)
  assert.match(managementSource, /node_ids: planNodeIDs\.value/)
})

test('普通管理页标记并阻止修改订阅托管 Relay 与 Client', () => {
  assert.match(relaySource, /subscription_published/)
  assert.match(relaySource, /订阅发布/)
  assert.match(relaySource, /v-if="!value\.subscription_published"/)
  assert.match(clientSource, /client\.subscription_managed/)
  assert.match(clientSource, /订阅托管/)
  assert.match(clientSource, /v-if="!client\.subscription_managed"/)
})

test('分流方案使用策略组、Rule Providers YAML 和逐行规则编辑器', () => {
  assert.doesNotMatch(managementSource, /Groups JSON|Rules JSON|routingGroupsJSON|routingRulesJSON|published_node_id/)
  for (const label of ['包含套餐全部节点', '指定 Published Node', '添加 DIRECT', '添加 REJECT', '添加分组引用', '上移', '下移']) {
    assert.match(groupEditorSource, new RegExp(label))
  }
  assert.match(groupEditorSource, /node.name/)
  assert.match(groupEditorSource, /node_ids/)
  assert.match(managementSource, /v-model:value="routingProvidersYAML"/)
  assert.match(managementSource, /Rule Providers YAML/)
  assert.match(managementSource, /Rules（一行一条 Mihomo rule）/)
  assert.match(managementSource, /RULE-SET,OpenAI,🤖 AI/)
})

test('分流方案支持编辑且默认方案不能停用或删除', () => {
  assert.match(managementSource, /function openCreateRoutingPreset/)
  assert.match(managementSource, /function openEditRoutingPreset/)
  assert.match(managementSource, /async function saveRoutingPreset/)
  assert.match(managementSource, /async function deleteRoutingPreset/)
  assert.match(managementSource, /method: id \? 'PATCH' : 'POST'/)
  assert.match(managementSource, /v-model:value="routingEnabled"/)
  assert.match(managementSource, /editingRoutingPreset\?\.is_default/)
  assert.match(managementSource, /v-if="!value\.is_default"[^>]*type="error"/)
  assert.match(managementSource, /v-if="value\.is_default"[^>]*>默认</)
})

test('套餐直接选择 Mihomo 模板和分流方案', () => {
  assert.match(managementSource, /routing_preset_id: number/)
  assert.match(managementSource, /const planRoutingPresetID = ref\(0\)/)
  assert.match(managementSource, /routing_preset_id: planRoutingPresetID\.value/)
  assert.match(managementSource, /v-model\.number="planRoutingPresetID"/)
  assert.match(managementSource, /套餐直接引用分流方案，方案修改后无需重新保存套餐。/)
  assert.match(managementSource, /function viewSelectedPlanRouting\(\)/)
  assert.doesNotMatch(managementSource, /planRoutingGroups|planRoutingRulesText|编辑当前分流|套用预设|恢复模板分流|继承 Mihomo 模板分流/)
})

test('Mihomo 模板只负责客户端基础配置', () => {
  assert.match(managementSource, /内置默认 Mihomo 模板/)
  assert.match(managementSource, /模板只负责 Mihomo 客户端基础配置，例如 DNS、sniffer、TUN、profile 等。proxies 由 Panel 动态生成，分流由所选分流方案提供。/)
  assert.match(managementSource, /function openEditTemplate/)
  assert.match(managementSource, /method: id \? 'PATCH' : 'POST'/)
  assert.match(managementSource, /v-model:value="templateEnabled"/)
  assert.match(managementSource, /不能包含 proxies、proxy-groups、rule-providers 或 rules。/)
})

test('分流与模板页面展示数据库默认方案并解释产品语义', () => {
  assert.match(managementSource, /api<MihomoConfiguration>\('\/api\/admin\/subscription\/builtin-mihomo'\)/)
  assert.match(managementSource, /Mihomo 模板负责 DNS、sniffer 等客户端基础配置；分流方案负责策略组、Rule Providers 和 Rules。套餐分别选择一套模板和一套分流方案。/)
  assert.match(managementSource, /v-for="value in routingPresets"[\s\S]*value\.is_default[\s\S]*默认/)
  assert.match(managementSource, /内置默认 Mihomo 模板[\s\S]*复制为自定义模板/)
  assert.doesNotMatch(managementSource, /内置默认分流|复制为预设/)
})

test('分流方案可以在套餐中只读查看并统一到配置页编辑', () => {
  assert.match(managementSource, /function viewSelectedPlanRouting\(\)[\s\S]*openRoutingPreview/)
  assert.match(managementSource, /routingPreviewRules.join\('\\n'\)[\s\S]*readonly/)
  assert.match(managementSource, /routingPreviewProvidersYAML/)
  assert.match(groupEditorSource, /readonly\?: boolean/)
  assert.match(groupEditorSource, /v-if="readonly"[\s\S]*\{\{ proxy \}\}/)
  assert.match(groupEditorSource, /v-if="!readonly"[\s\S]*新增策略组/)
})

test('内置 Mihomo 基础模板可查看并复制', () => {
  assert.match(managementSource, /function viewBuiltinTemplate\(\)[\s\S]*templatePreviewYAML.value = builtinMihomo.value.yaml/)
  assert.match(managementSource, /真实 proxies 由 Panel 动态注入，策略组、Rule Providers 和 Rules 来自套餐选择的分流方案。/)
  assert.match(managementSource, /function copyBuiltinTemplate\(\)[\s\S]*内置默认 Mihomo 模板 - 副本[\s\S]*builtinMihomo.value.yaml.trim\(\)/)
})

test('订阅用户可预览并复制正式 Mihomo YAML', () => {
  assert.match(managementSource, /预览 Mihomo/)
  assert.match(managementSource, /\/api\/admin\/subscription\/users\/\$\{value.user_id\}\/mihomo-preview/)
  assert.match(managementSource, /mihomoPreviewYAML.value = response.yaml/)
  assert.match(managementSource, /navigator.clipboard.writeText\(mihomoPreviewYAML.value\)/)
  assert.match(managementSource, /复制 YAML/)
})
