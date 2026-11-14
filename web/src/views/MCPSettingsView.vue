<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { NAlert, NButton, NCard, NInput, NModal, NPopconfirm, NSelect, NSwitch } from 'naive-ui'
import { api } from '../api/client'
import { mcpTools } from '../mcpTools'
import { generateMCPAgentPrompt, generateMCPConnection, type MCPClient, type MCPPlatform } from '../mcpConnection'

type Token = { id: number; name: string; prefix: string; scope: string; created_at: number; expires_at: number; last_used_at: number | null; revoked_at: number | null }
type ChangeEvent = { plan_id: string; event: string; created_at: number; server_id: number; token_name: string }
type Audit = { id: number; token_name: string; tool_name: string; called_at: number; duration_ms: number; success: boolean }
const enabled = ref(false)
const writeEnabled = ref(false)
const changeEvents = ref<ChangeEvent[]>([])
const tokenScope = ref<'read' | 'read_write'>('read')
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
const selectedClient = ref<MCPClient | null>(null)
const activeClient = computed(() => selectedClient.value ?? 'codex')
const creationClient = ref<MCPClient | null>(null)
const creationPlatform = ref<MCPPlatform | null>(null)
const showTools = ref(false)
const showAdvanced = ref(false)
const selectedPlatform = ref<MCPPlatform>('windows')
const connection = computed(() => generateMCPConnection(activeClient.value, selectedPlatform.value, endpoint.value))
const agentPrompt = computed(() => generateMCPAgentPrompt(activeClient.value, selectedPlatform.value, endpoint.value))
const clients: { label: string; value: MCPClient; method: string; icon: string }[] = [
  { label: 'Codex', value: 'codex', method: 'Desktop / CLI', icon: '✳' },
  { label: 'Claude Code', value: 'claude', method: '终端命令', icon: '✺' },
  { label: 'Cursor', value: 'cursor', method: '配置文件', icon: '◈' },
]
const platformOptions = [
  { label: 'Windows PowerShell', value: 'windows' },
  { label: 'macOS / Linux（Bash）', value: 'unix' },
]
const toolGroups = Array.from(new Set(mcpTools.map(tool => tool.domain)), domain => ({ domain, tools: mcpTools.filter(tool => tool.domain === domain) }))
const readCount = mcpTools.filter(tool => !tool.write).length
const writeCount = mcpTools.length - readCount
const options = [30, 90, 180].map(value => ({ label: `${value} 天`, value }))
function date(value: number | null) { return value ? new Date(value * 1000).toLocaleString() : '—' }
function startCreate(client: MCPClient | null = null) {
  if (busy.value || !loaded.value) return
  creationClient.value = client ?? activeClient.value
  creationPlatform.value = selectedPlatform.value
  name.value = client ? clients.find(item => item.value === client)?.label ?? '' : ''
  error.value = ''; copyStatus.value = ''; open.value = true
}
function close() { if (busy.value) return; open.value = false; secret.value = ''; creationClient.value = null; creationPlatform.value = null; name.value = ''; tokenScope.value = 'read'; copyStatus.value = '' }
function fail(reason: unknown) { if (!controller.signal.aborted) error.value = reason instanceof Error ? reason.message : '请求失败' }
async function load() {
  try {
    const [settings, tokenList, audit, eventList] = await Promise.all([
      api<{ mcp_enabled: boolean; mcp_write_enabled?: boolean }>('/api/mcp/settings', { signal: controller.signal }),
      api<{ tokens: Token[] }>('/api/mcp/tokens', { signal: controller.signal }),
      api<{ logs: Audit[] }>('/api/mcp/audit-logs', { signal: controller.signal }),
      api<{ events?: ChangeEvent[] }>('/api/mcp/change-events', { signal: controller.signal }),
    ])
    enabled.value = settings.mcp_enabled; writeEnabled.value = settings.mcp_write_enabled ?? false; tokens.value = tokenList.tokens; logs.value = audit.logs; changeEvents.value = eventList.events ?? []; loaded.value = true
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
async function create() {
  busy.value = true; error.value = ''
  try {
    const result = await api<{ token: string }>('/api/mcp/tokens', { method: 'POST', body: JSON.stringify({ name: name.value, scope: tokenScope.value, expires_at: new Date(Date.now() + days.value * 86400000).toISOString() }), signal: controller.signal })
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
  if (!open.value || !secret.value || !creationClient.value || !creationPlatform.value) return
  await copy(generateMCPConnection(creationClient.value, creationPlatform.value, endpoint.value, secret.value))
}
async function copyAgentPromptWithToken() {
  if (!open.value || !secret.value || !creationClient.value || !creationPlatform.value) return
  await copy(generateMCPAgentPrompt(creationClient.value, creationPlatform.value, endpoint.value, secret.value))
}
function connectionWithToken() {
  return open.value && secret.value && creationClient.value && creationPlatform.value
    ? generateMCPConnection(creationClient.value, creationPlatform.value, endpoint.value, secret.value) : ''
}
onMounted(load)
onUnmounted(() => { secret.value = ''; controller.abort() })
</script>

<template>
  <div class="mcp-page">
    <header class="mcp-page-header"><h2>AI / MCP 集成</h2><p>连接 AI 客户端，安全地访问和管理 VPS Panel。</p></header>
    <n-alert v-if="error && !open" type="error" role="alert">{{ error }}</n-alert>
    <div v-if="copyStatus && !open" class="mcp-feedback" role="status">{{ copyStatus }}</div>

    <section class="mcp-card mcp-status-card" aria-label="MCP 服务状态">
      <div class="mcp-status-row"><div><h3>MCP 服务 <span class="mcp-badge">{{ enabled ? '已启用' : '已关闭' }}</span></h3><p>通过 HTTPS 允许 AI 客户端连接面板。</p></div><n-switch :value="enabled" :loading="busy" :disabled="!loaded" aria-label="MCP 服务" @update:value="change" /></div>
      <button type="button" class="mcp-endpoint" title="复制 MCP 地址" @click="copy(endpoint)"><span>MCP 地址</span><code>{{ endpoint }}</code><span>复制 ⧉</span></button>
    </section>

    <section class="mcp-card mcp-content-card">
      <header><h3>连接客户端</h3><p>选择客户端，查看适合当前环境的连接方法。</p></header>
      <div class="mcp-clients" role="group" aria-label="选择客户端">
        <button v-for="client in clients" :key="client.value" type="button" class="mcp-client" :class="{ selected: selectedClient === client.value }" :aria-pressed="selectedClient === client.value" @click="selectedClient = selectedClient === client.value ? null : client.value">
          <span class="mcp-client-icon" aria-hidden="true">{{ client.icon }}</span><span><strong>{{ client.label }}</strong><small>{{ client.method }}</small></span>
        </button>
      </div>
      <div v-if="selectedClient" class="mcp-setup">
        <div class="mcp-setup-heading"><strong>配置 {{ clients.find(client => client.value === selectedClient)?.label }}</strong><label v-if="selectedClient !== 'cursor'" class="mcp-platform"><span>操作系统 / 终端</span><n-select v-model:value="selectedPlatform" :options="platformOptions" /></label></div>
        <p>先创建访问令牌，再复制安装提示词交给 AI 助手，或复制原始配置自行安装。</p>
        <n-button type="primary" size="small" class="mcp-create-for-client" :disabled="!loaded || busy" @click="startCreate(selectedClient)">为 {{ clients.find(client => client.value === selectedClient)?.label }} 创建令牌</n-button>
        <template v-if="selectedClient === 'codex'"><p>已有配置时先检查地址和认证方式；完成后可能需要完全退出并重启 Codex Desktop。</p><p v-if="selectedPlatform === 'windows'">命令隐藏令牌输入，并将其保存到当前 Windows 用户环境变量及 PowerShell 进程；同一用户的其他程序可能读取该变量。</p><p v-else>命令隐藏令牌输入并仅为当前终端设置环境变量；桌面应用或后续终端也需要获得该变量。</p></template>
        <template v-else-if="selectedClient === 'claude'"><p>安装后可运行 <code>claude mcp get vps-panel</code> 或在 Claude Code 中使用 <code>/mcp</code> 检查连接；同名配置请先检查作用域。</p><n-alert type="warning">Claude Code 会将 Bearer 令牌作为请求头保存到用户配置文件，请保护该文件。</n-alert></template>
        <template v-else><p>将 JSON 的 <code>vps-panel</code> 条目合并到 <code>~/.cursor/mcp.json</code> 或项目的 <code>.cursor/mcp.json</code>，保留其他配置。先在 Cursor 的启动环境中设置 <code>VPS_PANEL_MCP_TOKEN</code>，然后完全退出并重启。</p></template>
        <div class="mcp-code"><div class="mcp-code-label">{{ selectedClient === 'cursor' ? 'JSON 配置预览' : '命令预览' }}</div><pre aria-label="原始配置预览">{{ connection }}</pre></div>
        <div class="mcp-actions"><n-button type="primary" size="small" @click="copy(agentPrompt)">复制给 AI 的安装提示词</n-button><n-button size="small" @click="copy(connection)">{{ selectedClient === 'cursor' ? '复制原始 MCP JSON' : '复制原始配置命令' }}</n-button></div>
      </div>
    </section>

    <section class="mcp-card mcp-content-card">
      <header class="mcp-card-heading"><div><h3>访问令牌 <span class="mcp-badge">{{ tokens.length }}</span></h3><p>管理客户端访问凭据。</p></div><n-button size="small" type="primary" :disabled="!loaded || busy" @click="startCreate()">创建令牌</n-button></header>
      <div v-if="loaded && !tokens.length" class="mcp-empty">还没有访问令牌，创建令牌后即可连接客户端。</div>
      <div v-else-if="!loaded" class="mcp-empty">{{ error ? '令牌加载失败。' : '正在加载令牌…' }}</div>
      <div v-else class="mcp-tokens">
        <div v-for="item in tokens" :key="item.id" class="mcp-token">
          <div class="mcp-token-name"><strong>{{ item.name }}</strong><span class="mcp-badge" :class="{ write: item.scope === 'read_write' }">{{ item.scope === 'read_write' ? '读写' : '只读' }}</span><span class="mcp-badge">{{ item.revoked_at ? '已撤销' : item.expires_at * 1000 <= Date.now() ? '已过期' : '有效' }}</span></div>
          <code class="mcp-token-prefix">{{ item.prefix }}…</code>
          <div class="mcp-token-meta"><span>创建 {{ date(item.created_at) }}</span><span>最近使用 {{ date(item.last_used_at) }}</span><span>到期 {{ date(item.expires_at) }}</span></div>
          <div class="mcp-token-action"><n-popconfirm v-if="!item.revoked_at" @positive-click="revoke(item.id)"><template #trigger><n-button size="small" :disabled="busy">撤销</n-button></template>撤销后该令牌立即失效，确定撤销？</n-popconfirm></div>
        </div>
      </div>
    </section>

    <section class="mcp-card mcp-content-card">
      <button type="button" class="mcp-disclosure" :aria-expanded="showTools" aria-controls="mcp-tools" @click="showTools = !showTools"><span><strong>可用 MCP 工具</strong><small>只读 {{ readCount }} · 写操作 {{ writeCount }}</small></span><span aria-hidden="true">{{ showTools ? '⌃' : '⌄' }}</span></button>
      <div v-if="showTools" id="mcp-tools" class="mcp-tools"><p>工具按 MCP 的只读标注分类；部分读取敏感信息的工具仍要求读写令牌和写操作权限。配置保存后 Agent 同步可能仍在进行。</p><div class="mcp-tool-groups"><section v-for="group in toolGroups" :key="group.domain" class="mcp-tool-group"><h4>{{ group.domain }} <span>{{ group.tools.length }}</span></h4><div class="mcp-tool-list"><div v-for="tool in group.tools" :key="tool.name" class="mcp-tool"><span class="mcp-tool-icon" :class="{ write: tool.write }" aria-hidden="true">{{ tool.write ? '✎' : '⌕' }}</span><div><code>{{ tool.name }}</code><small>{{ tool.description }}</small></div></div></div></section></div></div>
    </section>

    <section class="mcp-card mcp-content-card">
      <button type="button" class="mcp-disclosure" :aria-expanded="showAdvanced" aria-controls="mcp-advanced" @click="showAdvanced = !showAdvanced"><span><strong>高级设置与审计</strong><small>写操作权限、历史审批记录和最近调用</small></span><span aria-hidden="true">{{ showAdvanced ? '⌃' : '⌄' }}</span></button>
      <div v-if="showAdvanced" id="mcp-advanced" class="mcp-advanced"><div class="mcp-write-row"><div><strong>允许 MCP 写操作</strong><p>允许读写令牌直接执行管理操作，请仅授予可信客户端。</p></div><n-switch :value="writeEnabled" :loading="busy" :disabled="!loaded" aria-label="MCP 写操作" @update:value="changeWrite" /></div><p>旧版本中仅获准重命名的读写令牌升级后会变为只读；需要直接管理时请新建读写令牌。</p>
        <h4>历史审批记录</h4><div v-if="!changeEvents.length" class="mcp-empty">暂无历史审批记录</div><div v-else class="mcp-table-scroll"><table><thead><tr><th>时间</th><th>令牌</th><th>服务器 ID</th><th>事件</th></tr></thead><tbody><tr v-for="(item, index) in changeEvents" :key="`${item.plan_id}-${index}`"><td>{{ date(item.created_at) }}</td><td>{{ item.token_name }}</td><td>{{ item.server_id }}</td><td>{{ item.event }}</td></tr></tbody></table></div>
        <h4>最近调用</h4><div v-if="!logs.length" class="mcp-empty">暂无调用记录</div><div v-else class="mcp-table-scroll"><table><thead><tr><th>时间</th><th>令牌</th><th>工具</th><th>状态</th><th>耗时</th></tr></thead><tbody><tr v-for="item in logs" :key="item.id"><td>{{ date(item.called_at) }}</td><td>{{ item.token_name }}</td><td>{{ item.tool_name }}</td><td>{{ item.success ? '成功' : '失败' }}</td><td>{{ item.duration_ms }} ms</td></tr></tbody></table></div>
      </div>
    </section>
  </div>
  <n-modal :show="open" :mask-closable="!busy" :close-on-esc="!busy" @update:show="value => { if (!value) close() }">
    <n-card class="account-modal-card mcp-result-modal" title="创建 MCP 令牌" :closable="!busy" @close="close">
      <template v-if="secret">
        <n-alert type="warning">令牌仅显示一次，请立即保存。含令牌的 AI 提示词可能被记录在剪贴板、AI 对话历史和工具执行记录中，只建议在受信任的个人环境使用。原始命令也可能留在终端历史中；Cursor JSON 会将令牌保存到配置文件。</n-alert>
        <n-alert v-if="error" type="error">{{ error }}</n-alert>
        <p class="mcp-result-token">{{ secret }}</p>
        <div class="mcp-code"><div class="mcp-code-label">{{ clients.find(client => client.value === creationClient)?.label }} {{ creationClient === 'cursor' ? 'MCP JSON' : creationPlatform === 'windows' ? 'PowerShell 连接命令' : 'Bash 连接命令' }}（含令牌）</div><pre aria-label="含令牌的连接配置">{{ connectionWithToken() }}</pre></div>
        <div class="mcp-modal-actions">
          <n-button type="primary" @click="copyConnectionWithToken">{{ creationClient === 'cursor' ? '复制带令牌的 MCP JSON' : '复制带令牌的连接命令' }}</n-button>
          <n-button @click="copy(secret)">复制令牌</n-button>
          <n-button @click="copyAgentPromptWithToken">复制含令牌的 AI 安装提示词</n-button>
        </div>
        <p v-if="copyStatus" role="status">{{ copyStatus }}</p>
        <p>配置针对创建时选择的客户端<span v-if="creationClient !== 'cursor'">和终端</span>生成。复制提示词后可粘贴到 AI 对话框，助手将在具备必要执行权限时协助配置。</p>
      </template>
      <form v-else class="auth-form" @submit.prevent="create">
        <n-alert v-if="error" type="error">{{ error }}</n-alert>
        <p class="mcp-modal-context">创建后将显示 {{ clients.find(client => client.value === creationClient)?.label }}<template v-if="creationClient !== 'cursor'">（{{ creationPlatform === 'windows' ? 'Windows PowerShell' : 'macOS / Linux Bash' }}）</template> 的连接配置。</p>
        <label><span>令牌名称</span><n-input v-model:value="name" maxlength="100" /></label>
        <label><span>权限</span><n-select v-model:value="tokenScope" :options="[{ label: '只读', value: 'read' }, { label: '读写：直接管理资源', value: 'read_write' }]" /></label>
        <n-alert v-if="tokenScope === 'read_write'" type="warning">读写令牌可直接修改资源，且可能影响节点服务；请妥善保管。</n-alert>
        <label><span>有效期</span><n-select v-model:value="days" :options="options" /></label>
        <div class="modal-actions"><n-button :disabled="busy" @click="close">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">创建</n-button></div>
      </form>
    </n-card>
  </n-modal>
</template>

<style scoped>
.mcp-page { width: 100%; min-width: 0; display: flex; flex-direction: column; gap: 14px; color: var(--color-text); }
.mcp-page-header h2 { margin: 0; font-size: 20px; }
.mcp-page p, .mcp-page-header p { margin: 5px 0 0; color: var(--color-text-secondary); font-size: 13px; line-height: 1.55; }
.mcp-card { min-width: 0; border: 1px solid var(--color-border); border-radius: 12px; background: var(--color-surface); }
.mcp-content-card { padding: 20px; }
.mcp-content-card > header { margin-bottom: 16px; }
.mcp-card h3 { margin: 0; font-size: 15px; font-weight: 650; }
.mcp-card-heading, .mcp-status-row, .mcp-write-row, .mcp-setup-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.mcp-status-card { overflow: hidden; }
.mcp-status-row { padding: 18px 20px; }
.mcp-badge { display: inline-flex; align-items: center; padding: 2px 7px; border-radius: 999px; background: var(--color-surface-muted); color: var(--color-text-secondary); font-size: 11px; font-weight: 600; white-space: nowrap; }
.mcp-badge.write, .mcp-tool-icon.write { color: var(--color-warning, #b87516); }
.mcp-endpoint { display: flex; align-items: center; width: 100%; gap: 12px; padding: 12px 20px; border: 0; border-top: 1px solid var(--color-border); background: var(--color-surface-muted); color: var(--color-text-secondary); text-align: left; cursor: pointer; font: inherit; font-size: 12px; }
.mcp-endpoint code { flex: 1; color: var(--color-text); overflow-wrap: anywhere; }
.mcp-endpoint:hover, .mcp-client:hover { background: var(--color-primary-soft); }
.mcp-clients { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; }
.mcp-client { display: flex; align-items: center; gap: 10px; min-width: 0; padding: 10px 12px; border: 1px solid transparent; border-radius: 10px; background: var(--color-surface-muted); color: var(--color-text); font: inherit; text-align: left; cursor: pointer; }
.mcp-client.selected { border-color: var(--color-primary); background: var(--color-surface); }
.mcp-client-icon { display: grid; place-items: center; flex: 0 0 30px; height: 30px; border-radius: 8px; background: var(--color-surface); border: 1px solid var(--color-border); font-size: 20px; }
.mcp-client strong, .mcp-client small { display: block; overflow-wrap: anywhere; }
.mcp-client strong { font-size: 13px; }.mcp-client small { margin-top: 2px; color: var(--color-text-secondary); font-size: 11px; }
.mcp-setup { margin-top: 18px; padding-top: 16px; border-top: 1px solid var(--color-border); }
.mcp-create-for-client { margin-top: 12px; }
.mcp-setup-heading { align-items: end; font-size: 13px; }.mcp-platform { width: min(240px, 50%); }.mcp-platform span { display: block; color: var(--color-text-secondary); font-size: 12px; margin-bottom: 5px; }
.mcp-setup .n-alert { margin-top: 10px; }.mcp-code { margin: 14px 0 10px; overflow: hidden; border: 1px solid var(--color-border); border-radius: 10px; background: var(--color-surface-muted); }
.mcp-code-label { padding: 8px 12px; border-bottom: 1px solid var(--color-border); color: var(--color-text-secondary); font-size: 11px; }
.mcp-code pre { margin: 0; max-height: 260px; overflow: auto; padding: 12px; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 12px; line-height: 1.5; }
.mcp-actions, .mcp-modal-actions { display: flex; flex-wrap: wrap; gap: 8px; }.mcp-feedback { color: var(--color-primary); font-size: 13px; }
.mcp-result-token { overflow-wrap: anywhere; }
.mcp-modal-context { margin: 0; color: var(--color-text-secondary); font-size: 13px; }
.mcp-result-modal { max-height: calc(100dvh - 32px); overflow-y: auto; }
.mcp-empty { padding: 18px; border-radius: 10px; background: var(--color-surface-muted); color: var(--color-text-secondary); font-size: 13px; text-align: center; }
.mcp-token { display: grid; grid-template-columns: minmax(130px, 1fr) minmax(80px, .6fr) minmax(180px, 1.3fr) auto; gap: 12px; align-items: center; padding: 12px 0; border-top: 1px solid var(--color-border); font-size: 12px; }
.mcp-token-name { display: flex; gap: 5px; align-items: center; flex-wrap: wrap; min-width: 0; overflow-wrap: anywhere; }.mcp-token-prefix { overflow-wrap: anywhere; color: var(--color-text-secondary); }.mcp-token-meta { display: flex; flex-direction: column; gap: 3px; color: var(--color-text-secondary); }
.mcp-disclosure { display: flex; align-items: center; justify-content: space-between; width: 100%; padding: 0; border: 0; background: transparent; color: var(--color-text); font: inherit; text-align: left; cursor: pointer; }.mcp-disclosure strong { font-size: 15px; }.mcp-disclosure small { display: block; margin-top: 4px; color: var(--color-text-secondary); font-size: 12px; }
.mcp-tools, .mcp-advanced { margin-top: 16px; border-top: 1px solid var(--color-border); padding-top: 14px; }.mcp-tool-groups { columns: 2; column-gap: 18px; margin-top: 16px; }.mcp-tool-group { break-inside: avoid; margin-bottom: 18px; }.mcp-tool-group h4, .mcp-advanced h4 { margin: 0 0 8px; font-size: 12px; color: var(--color-text-secondary); }.mcp-tool-list { border-radius: 10px; background: var(--color-surface-muted); overflow: hidden; }.mcp-tool { display: flex; gap: 9px; padding: 9px 10px; font-size: 12px; }.mcp-tool + .mcp-tool { border-top: 1px solid var(--color-border); }.mcp-tool > div { min-width: 0; }.mcp-tool code { overflow-wrap: anywhere; }.mcp-tool small { display: block; margin-top: 3px; color: var(--color-text-secondary); line-height: 1.4; }.mcp-tool-icon { color: var(--color-text-secondary); font-size: 15px; }.mcp-advanced h4 { margin-top: 18px; }.mcp-write-row { align-items: center; }.mcp-table-scroll { overflow-x: auto; max-width: 100%; }.mcp-table-scroll table { min-width: 600px; width: 100%; border-collapse: collapse; font-size: 12px; }.mcp-table-scroll th, .mcp-table-scroll td { padding: 8px; text-align: left; white-space: nowrap; border-bottom: 1px solid var(--color-border); }
.mcp-endpoint:focus-visible, .mcp-client:focus-visible, .mcp-disclosure:focus-visible { outline: 2px solid var(--color-primary); outline-offset: -2px; }
@media (max-width: 720px) { .mcp-token { grid-template-columns: minmax(0, 1fr) auto; }.mcp-token-name { grid-column: 1; }.mcp-token-prefix, .mcp-token-meta { grid-column: 1; }.mcp-token-action { grid-column: 2; grid-row: 1; }.mcp-tool-groups { columns: 1; } }
@media (max-width: 520px) { .mcp-content-card { padding: 16px; }.mcp-clients { grid-template-columns: repeat(2, minmax(0, 1fr)); }.mcp-status-row { padding: 16px; }.mcp-endpoint { display: grid; grid-template-columns: minmax(0, 1fr) auto; padding: 10px 16px; }.mcp-endpoint > span:first-child { grid-column: 1 / -1; }.mcp-setup-heading { align-items: stretch; flex-direction: column; gap: 10px; }.mcp-platform { width: 100%; } }
</style>
