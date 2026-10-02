import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
const portalSource = await readFile(new URL('../src/views/SubscriberPortalView.vue', import.meta.url), 'utf8')
const managementSource = await readFile(new URL('../src/views/SubscriptionManagementView.vue', import.meta.url), 'utf8')
const groupEditorSource = await readFile(new URL('../src/components/subscription/RoutingGroupEditor.vue', import.meta.url), 'utf8')
const bindingEditorSource = await readFile(new URL('../src/components/subscription/RoutingBindingEditor.vue', import.meta.url), 'utf8')
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

test('Subscriber Portal 将共享订阅和导入入口收拢到单一卡片', () => {
  for (const label of ['共享订阅信息', '下次重置', '到期', '可用节点', '导入订阅', '重新生成订阅', 'Base64 通用订阅', 'Clash / Mihomo', '扫描二维码订阅', '复制地址']) {
    assert.match(portalSource, new RegExp(label))
  }
  assert.match(portalSource, /subscriber\.plan_name/)
  assert.match(portalSource, /subscriber\.used_bytes/)
  assert.match(portalSource, /subscriber\.traffic_limit_bytes/)
  assert.match(portalSource, /class="portal-section-title">共享订阅信息/)
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

test('Subscriber Portal 无共享订阅时只渲染极简空态且不开放订阅操作', () => {
  assert.match(portalSource, /const hasPlan = computed\(\(\) => \(subscriber\.value\?\.plan_name \?\? ''\)\.trim\(\) !== ''\)/)
  assert.match(portalSource, /<section v-if="!hasPlan" class="subscriber-empty-state">[\s\S]*尚未开通共享订阅[\s\S]*当前账号尚未开通任何共享订阅[\s\S]*<section v-else>/)
  assert.match(portalSource, /<n-modal v-if="hasPlan" v-model:show="importModalOpen">/)
  assert.match(portalSource, /<QRCodeModal\s+v-if="hasPlan"/)
})

test('Subscriber Portal 品牌区域使用统一轻量玻璃风格', () => {
  assert.match(portalSource, /class="app-brand app-brand--portal">夕凪云/)
  assert.match(styleSource, /\.app-brand\s*\{[\s\S]*background: linear-gradient[\s\S]*backdrop-filter: blur/)
  assert.match(styleSource, /\.subscriber-empty-state\s*\{[\s\S]*place-content: center/)
})

test('订阅管理对 Admin 和 VIP 开放，并严格限制可见 Tab', () => {
  assert.match(appSource, /v-if="state\.user\?\.role === 'admin' \|\| state\.user\?\.role === 'vip'"[\s\S]*currentPage === 'subscriptions'[\s\S]*订阅管理/)
  assert.match(appSource, /currentPage === 'subscriptions' && \(state\.user\?\.role === 'admin' \|\| state\.user\?\.role === 'vip'\)/)
  assert.match(appSource, /<SubscriptionManagementView[\s\S]*:role="state\.user\?\.role"/)
  assert.match(managementSource, /const currentTab = ref<Tab>\('personal'\)/)
  assert.match(managementSource, /props\.role === 'vip'[\s\S]*\{ id: 'personal', label: '个人订阅' \}/)
  assert.match(managementSource, /\{ id: 'personal', label: '个人订阅' \},\s*\{ id: 'users', label: '订阅用户' \},\s*\{ id: 'plans', label: '共享订阅' \},\s*\{ id: 'nodes', label: '发布节点' \},\s*\{ id: 'configuration', label: '分流模板' \}/)
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
    assert.ok(managementSource.includes(endpoint))
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

test('共享订阅表单区分后台名称与订阅显示名称', () => {
  assert.match(managementSource, /subscription_title: string/)
  assert.match(managementSource, /const planSubscriptionTitle = ref\(''\)/)
  assert.match(managementSource, /planSubscriptionTitle\.value = value\.subscription_title/)
  assert.match(managementSource, /订阅显示名称/)
  assert.match(managementSource, /客户端导入订阅后显示的名称。留空则使用共享订阅名称。/)
})

test('套餐保存使用 Modal 独立错误、前端校验和防重复提交', () => {
  assert.match(managementSource, /const planFormError = ref\(''\)/)
  assert.match(managementSource, /async function savePlan\(\) \{\s*if \(busy\.value\) return\s*planFormError\.value = ''/)
  assert.match(managementSource, /共享订阅名称不能为空/)
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
  assert.match(managementSource, /实际使用 1 GB 时，按该倍率计入共享订阅流量。允许 0\.10×–5\.00×。/)
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
  assert.match(managementSource, /editingNode \? '所属共享订阅' : '加入共享订阅'/)
  assert.match(managementSource, /v-for="plan in plans"[\s\S]*nodePlanIDs\.includes\(plan\.id\)/)
  assert.match(managementSource, /所属共享订阅：\{\{ nodePlanNames\(value\.id\) \|\| '未加入共享订阅' \}\}/)
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
  assert.match(managementSource, /共享订阅基本信息已保存，但节点列表保存失败：\$\{message\}/)
  assert.match(managementSource, /共享订阅已创建，但节点列表保存失败：\$\{message\}/)
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

test('分流方案使用策略组、结构化规则源和逐行规则编辑器', () => {
  assert.doesNotMatch(managementSource, /Groups JSON|Rules JSON|routingGroupsJSON|routingRulesJSON|published_node_id/)
  for (const label of ['包含订阅全部节点', '添加 DIRECT', '添加 REJECT', '添加分组引用']) {
    assert.match(groupEditorSource, new RegExp(label))
  }
  assert.doesNotMatch(groupEditorSource, /Published Node|node_ids|toggleNode|PublishedNodeOption/)
  assert.match(groupEditorSource, /key: string/)
  assert.match(groupEditorSource, /startGroupDrag[\s\S]*beginDragPreview[\s\S]*function dropGroup[\s\S]*values\.splice\(index, 0, group\)/)
  assert.doesNotMatch(groupEditorSource, /function moveGroup/)
  assert.match(groupEditorSource, /group\.proxies = group\.proxies\.map\(\(proxy\) => proxy === oldName \? name : proxy\)/)
  assert.match(groupEditorSource, /parts\[policyIndex\] === oldName[\s\S]*parts\[policyIndex\] = name/)
  assert.match(groupEditorSource, /仍被 \$\{references\.join\('、'\)\} 引用，请先解除引用/)
  assert.match(managementSource, /const routingProviders = ref<RoutingRuleProvider\[]>\(\[]\)/)
  assert.match(managementSource, /rule_providers: routingProviders\.value/)
  for (const field of ['provider.name', 'provider.url', 'provider.type', 'provider.behavior', 'provider.format', 'provider.interval']) {
    assert.match(managementSource, new RegExp(field.replace('.', '\\.')))
  }
  assert.doesNotMatch(managementSource, /routingProvidersYAML|Rule Providers YAML|rule_providers_yaml/)
  assert.match(managementSource, /function setRoutingProviderName[\s\S]*parts\[0\] === 'RULE-SET'[\s\S]*parts\[1\] = name/)
  assert.match(managementSource, /规则源“\$\{provider\.name\}”仍被第 \$\{referencedAt \+ 1\} 条 Rule 使用/)
  assert.match(managementSource, /Rules（一行一条 Mihomo rule）/)
  assert.match(managementSource, /RULE-SET,OpenAI,🤖 AI/)
})

test('分流方案弹窗将策略组、远程规则集和 Rules 拆成可独立折叠滚动区', () => {
  for (const state of ['routingGroupsExpanded', 'routingProvidersExpanded', 'routingRulesExpanded']) {
    assert.match(managementSource, new RegExp(`const ${state} = ref\\(true\\)`))
    assert.match(managementSource, new RegExp(`@click="${state} = !${state}"`))
  }
  assert.match(managementSource, /function openCreateRoutingPreset[\s\S]*routingGroupsExpanded\.value = true[\s\S]*routingProvidersExpanded\.value = true[\s\S]*routingRulesExpanded\.value = true/)
  assert.match(managementSource, /function openEditRoutingPreset[\s\S]*routingGroupsExpanded\.value = true[\s\S]*routingProvidersExpanded\.value = true[\s\S]*routingRulesExpanded\.value = true/)
  assert.equal((managementSource.match(/class="routing-config-section"/g) ?? []).length, 3)
  assert.equal((managementSource.match(/class="routing-config-scroll routing-config-scroll-/g) ?? []).length, 3)
  assert.match(styleSource, /\.routing-config-scroll\s*\{[^}]*max-height:[^}]*overflow-x:\s*hidden;[^}]*overflow-y:\s*auto;/s)
  assert.match(managementSource, /远程规则集（Rule Providers）/)
  assert.match(managementSource, /这里只用于 RULE-SET 远程规则。DOMAIN、DOMAIN-SUFFIX、GEOSITE、GEOIP 等单条规则请直接写在 Rules 中。/)
  assert.match(managementSource, /Rules 按从上到下顺序匹配，先命中先生效。支持 DOMAIN、DOMAIN-SUFFIX、GEOSITE、GEOIP、RULE-SET、MATCH 等规则。/)
})

test('个人和共享订阅分别编辑自身节点 binding 并用拖拽排序', () => {
  assert.match(managementSource, /const personalRoutingBindings = ref<RoutingBindings>\(\{\}\)/)
  assert.match(managementSource, /const planRoutingBindings = ref<RoutingBindings>\(\{\}\)/)
  assert.match(managementSource, /\/api\/personal-subscriptions\/\$\{id\}\/routing-bindings/)
  assert.match(managementSource, /\/api\/admin\/subscription\/plans\/\$\{id\}\/routing-bindings/)
  assert.match(managementSource, /:nodes="personalNodes\.map\(\(node\) => \(\{ id: node\.id, name: node\.display_name \}\)\)"/)
  assert.match(managementSource, /:nodes="nodes\.filter\(\(node\) => planNodeIDs\.includes\(node\.id\)\)\.map/)
  assert.match(bindingEditorSource, /指定策略组节点（可选）/)
  assert.match(bindingEditorSource, /function startDrag[\s\S]*beginDragPreview[\s\S]*function drop[\s\S]*ids\.splice\(newIndex, 0, value\)/)
  assert.doesNotMatch(bindingEditorSource, />上移<|>下移</)
  assert.match(styleSource, /\.routing-binding-selected-row\.routing-binding-dragging\s*\{[^}]*opacity:\s*0\.38/s)
  assert.match(styleSource, /\.routing-binding-selected-row\.routing-binding-drop-target\s*\{[^}]*border-color:\s*var\(--color-primary\)[^}]*transform:/s)
})

test('个人订阅使用 Proxy、Relay、Landing 独立实例与折叠拖拽编辑', () => {
  for (const endpoint of [
    '/api/personal-subscriptions',
    '/api/personal-subscriptions/sources?client_name=',
    '/nodes',
  ]) {
    assert.ok(managementSource.includes(endpoint))
  }
  for (const label of ['新增个人订阅', '同名 Client', '本地 Proxy', '中转 Relay', '外部节点 Landing', '添加全部可用节点', '自定义显示名称', '入口地址', '入口端口', '复制链接', '二维码', 'Mihomo 模板']) {
    assert.match(managementSource, new RegExp(label))
  }
  assert.match(managementSource, /source_type: 'proxy' \| 'relay' \| 'landing'/)
  assert.match(managementSource, /const personalSourceTypes:[^\n]*\['proxy', 'relay', 'landing'\]/)
  assert.match(managementSource, /v-model:value="personalClientName"/)
  assert.match(managementSource, /v-model:value="node\.display_name"/)
  assert.match(managementSource, /entry_host: node\.entry_host\?\.trim\(\) \|\| null, entry_port: node\.entry_port/)
  assert.match(managementSource, /personalStatusType\(node\.status\)/)
  assert.match(managementSource, /const id = nextPersonalNodeID--[\s\S]*personalNodes\.value\.push/)
  assert.doesNotMatch(managementSource, /if \(personalNodes\.value\.some\(\(node\) => node\.source_type === source\.source_type && node\.source_id === source\.source_id\)\) return/)
  assert.match(managementSource, /function resetPersonalEditorUI[\s\S]*personalSourceAdderExpanded\.value = false[\s\S]*expandedPersonalNodeIDs\.value = new Set\(\)/)
  assert.match(managementSource, /function openEditPersonal[\s\S]*resetPersonalEditorUI\(\)/)
  const addSourceStart = managementSource.indexOf('function addPersonalSource')
  const addSource = managementSource.slice(addSourceStart, managementSource.indexOf('async function addAllPersonalSources', addSourceStart))
  const addAllStart = managementSource.indexOf('async function addAllPersonalSources')
  const addAll = managementSource.slice(addAllStart, managementSource.indexOf('function removePersonalNode', addAllStart))
  assert.doesNotMatch(addSource, /expandedPersonalNodeIDs/)
  assert.doesNotMatch(addAll, /expandedPersonalNodeIDs/)
  assert.match(managementSource, /@click="togglePersonalNodeDetails\(node\.id\)"/)
  assert.match(managementSource, /v-if="expandedPersonalNodeIDs\.has\(node\.id\)" class="personal-node-details"/)
  assert.match(managementSource, /<TransitionGroup tag="div" class="personal-node-list" name="personal-node-order">/)
  assert.match(managementSource, /:key="node\.id"[\s\S]*@dragover="dragOverPersonalNode[\s\S]*@drop\.prevent="dropPersonalNode/)
  assert.match(managementSource, /function dropPersonalNode[\s\S]*values\.splice\(newIndex, 0, node\)[\s\S]*value\.position = position \+ 1/)
  const personalFormStart = managementSource.indexOf('<form class="auth-form" novalidate @submit.prevent="savePersonal">')
  const personalForm = managementSource.slice(personalFormStart, managementSource.indexOf('</form>', personalFormStart))
  assert.ok(personalFormStart >= 0)
  assert.doesNotMatch(personalForm, />上移<|>下移<|@click="movePersonalNode/)
  assert.doesNotMatch(personalForm, /发布节点/)
  assert.match(managementSource, /\{ label: 'Auto', value: personalQR\.subscription_auto_url \}/)
  assert.match(managementSource, /\{ label: 'Mihomo', value: personalQR\.subscription_mihomo_url \}/)
  assert.match(managementSource, /\{ label: 'Base64', value: personalQR\.subscription_base64_url \}/)
  assert.match(styleSource, /\.personal-node-item\.personal-node-dragging\s*{[^}]*opacity:\s*0\.38/s)
  assert.match(styleSource, /\.personal-node-item\.personal-node-drop-target\s*{[^}]*border-color:\s*var\(--color-primary\)[^}]*transform:/s)
  assert.match(styleSource, /\.personal-node-order-move\s*{[^}]*transition:\s*transform/s)
})

test('个人订阅候选来源是默认折叠且可搜索的临时添加器', () => {
  assert.match(managementSource, /const personalSourceAdderExpanded = ref\(false\)/)
  assert.match(managementSource, /function resetPersonalEditorUI[\s\S]*personalSourceAdderExpanded\.value = false[\s\S]*personalSourceSearch\.value = ''/)
  assert.match(managementSource, /@click="togglePersonalSourceAdder"[^>]*>[\s\S]*\+ 添加节点/)
  assert.match(managementSource, /v-if="personalSourceAdderExpanded" class="subscription-node-picker personal-source-adder"/)
  assert.match(managementSource, /v-model:value="personalSourceSearch"[^>]*placeholder="搜索节点名称或详情"/)
  assert.match(managementSource, /filteredPersonalSources[\s\S]*source\.name[\s\S]*source\.detail/)
  assert.match(managementSource, /personalSourceTypes:[^\n]*\['proxy', 'relay', 'landing'\]/)
  assert.match(managementSource, /class="personal-source-scroll"[\s\S]*v-for="sourceType in personalSourceTypes"/)
  assert.match(styleSource, /\.personal-source-scroll\s*\{[^}]*max-height:[^}]*overflow-x:\s*hidden;[^}]*overflow-y:\s*auto;/s)
  assert.doesNotMatch(managementSource, /节点来源选择器/)
})

test('个人订阅节点摘要常驻删除且详情区不重复删除', () => {
  const summaryStart = managementSource.indexOf('<div class="personal-node-summary">')
  const detailsStart = managementSource.indexOf('<div v-if="expandedPersonalNodeIDs.has(node.id)" class="personal-node-details">')
  const summary = managementSource.slice(summaryStart, detailsStart)
  const details = managementSource.slice(detailsStart, managementSource.indexOf('</div>\n        </div>', detailsStart))
  assert.match(summary, /personal-node-details-button[\s\S]*personal-node-delete-button[\s\S]*removePersonalNode\(index\)/)
  assert.doesNotMatch(details, /removePersonalNode|>删除(?:节点)?</)
  assert.match(managementSource, /function removePersonalNode[\s\S]*personalRoutingBindings\.value = pruneRoutingBindingNode[\s\S]*expanded\.delete\(id\)/)
  assert.match(styleSource, /\.personal-node-summary\s*\{[^}]*grid-template-columns:\s*auto minmax\(0, 1fr\) auto auto auto;/s)
})

test('Routing Binding 使用默认折叠的高级区和独立策略组摘要', () => {
  assert.match(managementSource, /<RoutingBindingEditor[\s\S]*v-model="personalRoutingBindings"[\s\S]*default-collapsed[\s\S]*:reset-key="personalBindingEditorKey"/)
  assert.match(bindingEditorSource, /defaultCollapsed:\s*false[\s\S]*const editorExpanded = ref\(!props\.defaultCollapsed\)/)
  assert.match(bindingEditorSource, /const expandedGroupKeys = ref<Set<string>>\(new Set\(\)\)/)
  assert.match(bindingEditorSource, /watch\(\(\) => props\.resetKey[\s\S]*expandedGroupKeys\.value = new Set\(\)/)
  assert.match(bindingEditorSource, /function toggleGroup[\s\S]*new Set\(expandedGroupKeys\.value\)[\s\S]*expanded\.has\(groupKey\)/)
  assert.match(bindingEditorSource, /v-if="expandedGroupKeys\.has\(group\.key\)" class="routing-binding-group-content"/)
  assert.match(bindingEditorSource, /if \(count === 0\) return '未指定'/)
  assert.match(bindingEditorSource, /group\.include_all \? `\$\{count\} 个优先节点` : `\$\{count\} 个节点`/)
  assert.match(bindingEditorSource, /v-if="group\.include_all"[^>]*>包含全部节点</)
  assert.match(bindingEditorSource, /用于为某些策略组额外指定当前订阅中的节点。开启“包含订阅全部节点”的策略组通常无需在这里选择。/)
  assert.match(bindingEditorSource, /如果策略组开启“包含订阅全部节点”，这里指定的节点会优先排在前面，其余节点随后补入。/)
  assert.match(styleSource, /\.routing-binding-editor-body\s*\{[^}]*max-height:[^}]*overflow-x:\s*hidden;[^}]*overflow-y:\s*auto;/s)
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

test('共享订阅直接选择 Mihomo 模板和分流方案', () => {
  assert.match(managementSource, /routing_preset_id: number/)
  assert.match(managementSource, /const planRoutingPresetID = ref\(0\)/)
  assert.match(managementSource, /routing_preset_id: planRoutingPresetID\.value/)
  assert.match(managementSource, /v-model\.number="planRoutingPresetID"/)
  assert.match(managementSource, /共享订阅直接引用分流方案，方案修改后无需重新保存共享订阅。/)
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
  assert.match(managementSource, /通用分流方案负责策略组、规则源和 Rules；客户端模板只负责对应客户端的基础配置。个人订阅和共享订阅分别选择一套分流方案与 Mihomo 模板。/)
  assert.match(managementSource, /<n-card title="通用分流方案"/)
  assert.match(managementSource, /<n-card title="客户端模板"/)
  assert.match(managementSource, /v-for="value in routingPresets"[\s\S]*value\.is_default[\s\S]*默认/)
  assert.match(managementSource, /内置默认 Mihomo 模板[\s\S]*复制为自定义模板/)
  assert.doesNotMatch(managementSource, /内置默认分流|复制为预设/)
})

test('分流方案可以在共享订阅中只读查看并统一到配置页编辑', () => {
  assert.match(managementSource, /function viewSelectedPlanRouting\(\)[\s\S]*openRoutingPreview/)
  assert.match(managementSource, /routingPreviewRules.join\('\\n'\)[\s\S]*readonly/)
  assert.match(managementSource, /routingPreviewProviders/)
  assert.match(managementSource, /v-for="provider in routingPreviewProviders"/)
  assert.match(groupEditorSource, /readonly\?: boolean/)
  assert.match(groupEditorSource, /v-if="readonly"[\s\S]*\{\{ proxy \}\}/)
  assert.match(groupEditorSource, /v-if="!readonly"[\s\S]*新增策略组/)
})

test('内置 Mihomo 基础模板可查看并复制', () => {
  assert.match(managementSource, /function viewBuiltinTemplate\(\)[\s\S]*templatePreviewYAML.value = builtinMihomo.value.yaml/)
  assert.match(managementSource, /真实 proxies 由 Panel 动态注入，策略组、规则源和 Rules 来自订阅选择的分流方案。/)
  assert.match(managementSource, /function copyBuiltinTemplate\(\)[\s\S]*内置默认 Mihomo 模板 - 副本[\s\S]*builtinMihomo.value.yaml.trim\(\)/)
})

test('订阅用户可预览并复制正式 Mihomo YAML', () => {
  assert.match(managementSource, /预览 Mihomo/)
  assert.match(managementSource, /\/api\/admin\/subscription\/users\/\$\{value.user_id\}\/mihomo-preview/)
  assert.match(managementSource, /mihomoPreviewYAML.value = response.yaml/)
  assert.match(managementSource, /navigator.clipboard.writeText\(mihomoPreviewYAML.value\)/)
  assert.match(managementSource, /复制 YAML/)
})

test('用户可见文案统一使用共享订阅而非套餐', () => {
  assert.doesNotMatch(managementSource, /套餐/)
  assert.doesNotMatch(portalSource, /套餐/)
  assert.doesNotMatch(groupEditorSource, /套餐/)
})
