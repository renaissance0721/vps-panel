<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { NAlert, NButton, NCard, NInput, NModal, NPopconfirm, NSelect, NSwitch } from 'naive-ui'
import { api } from '../api/client'
import { generateMCPAgentPrompt, generateMCPConnection, type MCPClient, type MCPPlatform } from '../mcpConnection'

type Token = { id: number; name: string; prefix: string; scope: string; created_at: number; expires_at: number; last_used_at: number | null; revoked_at: number | null }
type ChangePlan = { plan_id: string; server_id: number; old_name: string; new_name: string; status: string; token_name: string; created_at: number; expires_at: number }
type ChangeEvent = { plan_id: string; event: string; created_at: number; server_id: number; token_name: string }
type Audit = { id: number; token_name: string; tool_name: string; called_at: number; duration_ms: number; success: boolean }
const enabled = ref(false)
const writeEnabled = ref(false)
const plans = ref<ChangePlan[]>([])
const changeEvents = ref<ChangeEvent[]>([])
const tokenScope = ref<'read' | 'read_write'>('read')
const selectedPlan = ref<ChangePlan | null>(null)
const approvalPassword = ref('')
const decision = ref<'approve' | 'reject'>('approve')
const tokens = ref<Token[]>([])
const logs = ref<Audit[]>([])
const error = ref('')
const copyStatus = ref('')
const busy = ref(false)
const loaded = ref(false)
const open = ref(false)
const name = ref('')
const days = ref(90)
const secret = ref('')
const controller = new AbortController()
const endpoint = computed(() => `${window.location.origin}/mcp`)
const selectedClient = ref<MCPClient>('codex')
const selectedPlatform = ref<MCPPlatform>('windows')
const connection = computed(() => generateMCPConnection(selectedClient.value, selectedPlatform.value, endpoint.value))
const agentPrompt = computed(() => generateMCPAgentPrompt(selectedClient.value, selectedPlatform.value, endpoint.value))
const clientOptions = [
  { label: 'Codex Desktop / CLI', value: 'codex' },
  { label: 'Claude Code', value: 'claude' },
  { label: 'Cursor', value: 'cursor' },
]
const platformOptions = [
  { label: 'Windows PowerShell', value: 'windows' },
  { label: 'macOS / Linux（Bash）', value: 'unix' },
]
const tools = [
  ['list_servers', '列出可访问的服务器'], ['get_server_details', '读取服务器基本信息'],
  ['get_server_metrics', '读取最新资源指标'], ['get_latency_history', '汇总探测延迟历史'],
  ['list_proxies', '列出代理节点'], ['list_relays', '列出中转'],
  ['check_relay_health', '检查中转配置状态'], ['list_subscriptions', '列出个人订阅'],
  ['list_carpool_nodes', '汇总拼车分配'], ['get_panel_info', '查看面板和授权信息'],
]
const options = [30, 90, 180].map(value => ({ label: `${value} 天`, value }))
function planStatus(value: string) { return ({ pending: '待审批', approved: '已批准，等待执行', rejected: '已拒绝', committed: '已完成', conflicted: '资源已变化', expired: '已过期', revoked: '已失效' } as Record<string, string>)[value] ?? value }
function date(value: number | null) { return value ? new Date(value * 1000).toLocaleString() : '—' }
function close() { if (busy.value) return; open.value = false; secret.value = ''; name.value = ''; tokenScope.value = 'read'; copyStatus.value = '' }
function closeApproval() { if (busy.value) return; selectedPlan.value = null; approvalPassword.value = '' }
function fail(reason: unknown) { if (!controller.signal.aborted) error.value = reason instanceof Error ? reason.message : '请求失败' }
async function load() {
  try {
    const [settings, tokenList, audit, planList, eventList] = await Promise.all([
      api<{ mcp_enabled: boolean; mcp_write_enabled?: boolean }>('/api/mcp/settings', { signal: controller.signal }),
      api<{ tokens: Token[] }>('/api/mcp/tokens', { signal: controller.signal }),
      api<{ logs: Audit[] }>('/api/mcp/audit-logs', { signal: controller.signal }),
      api<{ plans?: ChangePlan[] }>('/api/mcp/change-plans', { signal: controller.signal }),
      api<{ events?: ChangeEvent[] }>('/api/mcp/change-events', { signal: controller.signal }),
    ])
    enabled.value = settings.mcp_enabled; writeEnabled.value = settings.mcp_write_enabled ?? false; tokens.value = tokenList.tokens; logs.value = audit.logs; plans.value = planList.plans ?? []; changeEvents.value = eventList.events ?? []; loaded.value = true
  } catch (reason) { fail(reason) }
}
async function change(value: boolean) {
  busy.value = true; error.value = ''
  try { const result = await api<{ mcp_enabled: boolean }>('/api/mcp/settings', { method: 'PATCH', body: JSON.stringify({ mcp_enabled: value }), signal: controller.signal }); enabled.value = result.mcp_enabled }
  catch (reason) { fail(reason) } finally { busy.value = false }
}
async function changeWrite(value: boolean) {
  busy.value = true; error.value = ''
  try { const result = await api<{ mcp_write_enabled: boolean }>('/api/mcp/settings', { method: 'PATCH', body: JSON.stringify({ mcp_write_enabled: value }), signal: controller.signal }); writeEnabled.value = result.mcp_write_enabled }
  catch (reason) { fail(reason) } finally { busy.value = false }
}
function selectDecision(plan: ChangePlan, value: 'approve' | 'reject') { selectedPlan.value = plan; decision.value = value; approvalPassword.value = '' }
async function submitDecision() {
  if (!selectedPlan.value || !approvalPassword.value) return
  busy.value = true; error.value = ''
  try {
    await api(`/api/mcp/change-plans/${encodeURIComponent(selectedPlan.value.plan_id)}/${decision.value}`, { method: 'POST', body: JSON.stringify({ password: approvalPassword.value }), signal: controller.signal })
    selectedPlan.value = null; approvalPassword.value = ''; await load()
  } catch (reason) { approvalPassword.value = ''; fail(reason) } finally { busy.value = false }
}
async function create() {
  busy.value = true; error.value = ''
  try {
    const result = await api<{ token: string }>('/api/mcp/tokens', { method: 'POST', body: JSON.stringify({ name: name.value, scope: tokenScope.value, ...(tokenScope.value === 'read_write' ? { capabilities: ['server.rename'] } : {}), expires_at: new Date(Date.now() + days.value * 86400000).toISOString() }), signal: controller.signal })
    secret.value = result.token; await load()
  } catch (reason) { fail(reason) } finally { busy.value = false }
}
async function revoke(id: number) { busy.value = true; error.value = ''; try { await api(`/api/mcp/tokens/${id}`, { method: 'DELETE', signal: controller.signal }); await load() } catch (reason) { fail(reason) } finally { busy.value = false } }
async function copy(value: string) {
  error.value = ''; copyStatus.value = ''
  try { await navigator.clipboard.writeText(value); copyStatus.value = '已复制到剪贴板。' }
  catch { error.value = '复制失败，请检查浏览器的剪贴板权限。' }
}
async function copyConnectionWithToken() {
  if (!secret.value) return
  await copy(generateMCPConnection(selectedClient.value, selectedPlatform.value, endpoint.value, secret.value))
}
async function copyAgentPromptWithToken() {
  if (!secret.value) return
  await copy(generateMCPAgentPrompt(selectedClient.value, selectedPlatform.value, endpoint.value, secret.value))
}
onMounted(load)
onUnmounted(() => { secret.value = ''; approvalPassword.value = ''; controller.abort() })
</script>

<template>
  <n-card title="AI / MCP 集成" style="margin-top: 16px">
    <n-alert v-if="error" type="error">{{ error }}</n-alert>
    <p>通过 HTTPS 连接 MCP 客户端。写操作需要独立授权，并由管理员在网页审批。</p>
    <p>MCP 服务：<n-switch :value="enabled" :loading="busy" :disabled="!loaded" @update:value="change" /> {{ enabled ? '已启用' : '已关闭' }}</p>
    <p>MCP 写操作：<n-switch :value="writeEnabled" :loading="busy" :disabled="!loaded" @update:value="changeWrite" /> {{ writeEnabled ? '已启用' : '已关闭' }}（仅服务器重命名，仍需审批）</p>
    <p>服务地址：<code>{{ endpoint }}</code> <n-button size="small" @click="copy(endpoint)">复制地址</n-button></p>
    <h3>访问令牌</h3>
    <n-button :disabled="!loaded" @click="copyStatus = ''; open = true">创建令牌</n-button>
    <div class="mcp-table-scroll"><table class="mcp-token-table"><thead><tr><th>名称</th><th>前缀</th><th>权限</th><th>创建时间</th><th>到期时间</th><th>最近使用</th><th>状态</th><th></th></tr></thead><tbody>
      <tr v-for="item in tokens" :key="item.id"><td>{{ item.name }}</td><td>{{ item.prefix }}…</td><td>{{ item.scope === 'read_write' ? '读写：服务器重命名' : '只读' }}</td><td>{{ date(item.created_at) }}</td><td>{{ date(item.expires_at) }}</td><td>{{ date(item.last_used_at) }}</td><td>{{ item.revoked_at ? '已撤销' : item.expires_at * 1000 <= Date.now() ? '已过期' : '有效' }}</td><td><n-popconfirm v-if="!item.revoked_at" @positive-click="revoke(item.id)"><template #trigger><n-button size="small" :disabled="busy">撤销</n-button></template>撤销后该令牌立即失效，确定撤销？</n-popconfirm></td></tr>
    </tbody></table></div>
    <h3>变更审批</h3>
    <p>审批后客户端还须执行一次；计划在重启或恢复备份后失效。窄屏可左右滑动表格。</p>
    <n-button size="small" :disabled="busy" @click="load">刷新计划</n-button>
    <div class="mcp-table-scroll"><table class="mcp-plan-table"><thead><tr><th>操作</th><th>服务器 ID</th><th>旧名称</th><th>新名称</th><th>发起令牌</th><th>创建时间</th><th>过期时间</th><th>状态</th><th>审批</th></tr></thead><tbody>
      <tr v-for="plan in plans" :key="plan.plan_id"><td>服务器重命名</td><td>{{ plan.server_id }}</td><td>{{ plan.old_name }}</td><td>{{ plan.new_name }}</td><td>{{ plan.token_name }}</td><td>{{ date(plan.created_at) }}</td><td>{{ date(plan.expires_at) }}</td><td>{{ planStatus(plan.status) }}</td><td><template v-if="plan.status === 'pending'"><n-button size="small" :disabled="busy" @click="selectDecision(plan, 'approve')">批准</n-button> <n-button size="small" :disabled="busy" @click="selectDecision(plan, 'reject')">拒绝</n-button></template><n-button v-else-if="plan.status === 'approved'" size="small" :disabled="busy" @click="selectDecision(plan, 'reject')">撤销批准</n-button></td></tr>
    </tbody></table></div>
    <h3>可用工具（只读）</h3><ul><li v-for="tool in tools" :key="tool[0]"><code>{{ tool[0] }}</code>：{{ tool[1] }}</li></ul>
    <h3>写操作工具</h3><p><code>prepare_change</code> 创建服务器重命名计划；<code>get_change_status</code> 查询审批状态；<code>apply_change</code> 执行已批准的计划。</p>
    <h3>客户端连接配置</h3>
    <div class="mcp-connection-options">
      <label><span>客户端</span><n-select v-model:value="selectedClient" :options="clientOptions" /></label>
      <label v-if="selectedClient !== 'cursor'"><span>操作系统 / 终端</span><n-select v-model:value="selectedPlatform" :options="platformOptions" /></label>
    </div>
    <p>推荐复制提示词并发送给 AI 助手，让它在具备必要权限时帮助配置 MCP 并验证连接。</p>
    <template v-if="selectedClient === 'codex'">
      <p>复制提示词并粘贴到 Codex Desktop 对话框，Codex 可在具备必要执行权限时协助配置和验证；已有正确配置时，可按其实际环境变量名补齐令牌。也可以复制原始命令，在对应终端执行；该命令遇到同名配置时仍会请求替换确认。完成后可能需要完全退出并重启 Codex Desktop。</p>
      <p v-if="selectedPlatform === 'windows'">命令会隐藏输入令牌，并将其保存到当前 Windows 用户环境变量及当前 PowerShell 进程；同一用户的其他程序可能读取该变量。</p>
      <p v-else>命令使用 Bash 隐藏输入令牌，并仅为当前终端设置环境变量。后续终端或桌面应用也需要获得该变量；从此终端启动 Codex，或自行在桌面会话中配置该变量。</p>
    </template>
    <template v-else-if="selectedClient === 'claude'">
      <p>可将提示词发给 AI 助手，或复制命令并在对应终端执行一次。然后运行 <code>claude mcp get vps-panel</code> 或在 Claude Code 中使用 <code>/mcp</code> 检查连接。若存在同名配置，请先检查其作用域。</p>
      <n-alert type="warning">Claude Code 会将 Bearer 令牌作为请求头保存到用户配置文件，请保护该文件。</n-alert>
    </template>
    <template v-else>
      <p>可让 AI 助手协助配置，或复制 JSON，将 <code>vps-panel</code> 条目合并到已有 <code>~/.cursor/mcp.json</code> 的 <code>mcpServers</code> 中，不要覆盖其他配置。也可以使用项目目录的 <code>.cursor/mcp.json</code>。</p>
      <p>先在启动 Cursor 的用户环境中设置 <code>VPS_PANEL_MCP_TOKEN</code>，然后完全退出并重启 Cursor；环境变量需要对 Cursor 进程可见。</p>
    </template>
    <pre class="mcp-connection-code" aria-label="AI 安装提示词预览">{{ agentPrompt }}</pre>
    <div class="mcp-modal-actions">
      <n-button type="primary" size="small" @click="copy(agentPrompt)">复制给 AI 的安装提示词</n-button>
      <n-button size="small" @click="copy(connection)">{{ selectedClient === 'cursor' ? '复制原始 MCP JSON' : '复制原始配置命令' }}</n-button>
    </div>
    <span v-if="copyStatus && !open" class="mcp-copy-status" role="status">{{ copyStatus }}</span>
    <h3>变更审计</h3><div class="mcp-table-scroll"><table class="mcp-audit-table"><thead><tr><th>时间</th><th>令牌</th><th>服务器 ID</th><th>事件</th></tr></thead><tbody><tr v-for="(item, index) in changeEvents" :key="`${item.plan_id}-${index}`"><td>{{ date(item.created_at) }}</td><td>{{ item.token_name }}</td><td>{{ item.server_id }}</td><td>{{ item.event }}</td></tr></tbody></table></div>
    <h3>最近调用</h3><div class="mcp-table-scroll"><table class="mcp-audit-table"><thead><tr><th>时间</th><th>令牌</th><th>工具</th><th>状态</th><th>耗时</th></tr></thead><tbody><tr v-for="item in logs" :key="item.id"><td>{{ date(item.called_at) }}</td><td>{{ item.token_name }}</td><td>{{ item.tool_name }}</td><td>{{ item.success ? '成功' : '失败' }}</td><td>{{ item.duration_ms }} ms</td></tr></tbody></table></div>
  </n-card>
  <n-modal :show="open" :mask-closable="!busy" :close-on-esc="!busy" @update:show="value => { if (!value) close() }"><n-card class="account-modal-card" title="创建 MCP 令牌" :closable="!busy" @close="close"><template v-if="secret"><n-alert type="warning">令牌仅显示一次，请立即保存。含令牌的 AI 提示词可能被记录在剪贴板、AI 对话历史和工具执行记录中，只建议在受信任的个人环境使用。原始命令也可能留在终端历史中；Cursor JSON 会将令牌保存到配置文件。</n-alert><n-alert v-if="error" type="error">{{ error }}</n-alert><p style="overflow-wrap: anywhere">{{ secret }}</p><div class="mcp-modal-actions"><n-button @click="copy(secret)">复制令牌</n-button><n-button type="primary" @click="copyAgentPromptWithToken">复制含令牌的 AI 安装提示词（推荐）</n-button><n-button @click="copyConnectionWithToken">{{ selectedClient === 'cursor' ? '复制带令牌的原始 MCP JSON' : '复制带令牌的原始配置命令' }}</n-button></div><p v-if="copyStatus" role="status">{{ copyStatus }}</p><p>使用连接配置区域当前选择的客户端<span v-if="selectedClient !== 'cursor'">和终端</span>生成。复制后可粘贴到 AI 对话框，助手将在具备必要执行权限时协助配置。</p></template><form v-else class="auth-form" @submit.prevent="create"><label><span>令牌名称</span><n-input v-model:value="name" maxlength="100" /></label><label><span>权限</span><n-select v-model:value="tokenScope" :options="[{ label: '只读', value: 'read' }, { label: '读写：允许申请服务器重命名', value: 'read_write' }]" /></label><n-alert v-if="tokenScope === 'read_write'" type="warning">明确授予 server.rename 能力；每次修改仍须在网页审批。</n-alert><label><span>有效期</span><n-select v-model:value="days" :options="options" /></label><div class="modal-actions"><n-button :disabled="busy" @click="close">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">创建</n-button></div></form></n-card></n-modal>
  <n-modal :show="!!selectedPlan" :mask-closable="!busy" @update:show="value => { if (!value) closeApproval() }"><n-card class="account-modal-card" :title="decision === 'approve' ? '批准服务器重命名' : (selectedPlan?.status === 'approved' ? '撤销批准' : '拒绝变更计划')" :closable="!busy" @close="closeApproval"><form class="auth-form" @submit.prevent="submitDecision"><p>服务器 #{{ selectedPlan?.server_id }}：{{ selectedPlan?.old_name }} → {{ selectedPlan?.new_name }}</p><p>请重新输入当前管理员密码以确认。</p><n-alert v-if="error" type="error">{{ error }}</n-alert><n-input v-model:value="approvalPassword" type="password" placeholder="当前管理员密码" /><div class="modal-actions"><n-button :disabled="busy" @click="closeApproval">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">{{ decision === 'approve' ? '确认批准' : (selectedPlan?.status === 'approved' ? '确认撤销' : '确认拒绝') }}</n-button></div></form></n-card></n-modal>
</template>

<style scoped>
.mcp-table-scroll { overflow-x: auto; max-width: 100%; }
.mcp-token-table { min-width: 900px; }
.mcp-audit-table { min-width: 600px; }
.mcp-plan-table { min-width: 1050px; }
.mcp-table-scroll th, .mcp-table-scroll td { padding: 6px 10px; text-align: left; white-space: nowrap; }
.mcp-connection-options { display: flex; flex-wrap: wrap; gap: 12px; }
.mcp-connection-options label { flex: 1 1 220px; min-width: 0; }
.mcp-connection-options label span { display: block; margin-bottom: 6px; }
.mcp-connection-code { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 280px; overflow: auto; }
.mcp-modal-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.mcp-copy-status { margin-left: 8px; }
</style>
