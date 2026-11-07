<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { NAlert, NButton, NCard, NInput, NModal, NPopconfirm, NSelect, NSwitch } from 'naive-ui'
import { api } from '../api/client'

type Token = { id: number; name: string; prefix: string; scope: string; created_at: number; expires_at: number; last_used_at: number | null; revoked_at: number | null }
type Audit = { id: number; token_name: string; tool_name: string; called_at: number; duration_ms: number; success: boolean }
const enabled = ref(false)
const tokens = ref<Token[]>([])
const logs = ref<Audit[]>([])
const error = ref('')
const busy = ref(false)
const loaded = ref(false)
const open = ref(false)
const name = ref('')
const days = ref(90)
const secret = ref('')
const controller = new AbortController()
const endpoint = computed(() => `${window.location.origin}/mcp`)
const command = computed(() => `codex mcp add vps-panel --url ${endpoint.value} --bearer-token-env-var VPS_PANEL_MCP_TOKEN`)
const tools = [
  ['list_servers', '列出可访问的服务器'], ['get_server_details', '读取服务器基本信息'],
  ['get_server_metrics', '读取最新资源指标'], ['get_latency_history', '汇总探测延迟历史'],
  ['list_proxies', '列出代理节点'], ['list_relays', '列出中转'],
  ['check_relay_health', '检查中转配置状态'], ['list_subscriptions', '列出个人订阅'],
  ['list_carpool_nodes', '汇总拼车分配'], ['get_panel_info', '查看面板和授权信息'],
]
const options = [30, 90, 180].map(value => ({ label: `${value} 天`, value }))
function date(value: number | null) { return value ? new Date(value * 1000).toLocaleString() : '—' }
function close() { if (busy.value) return; open.value = false; secret.value = ''; name.value = '' }
function fail(reason: unknown) { if (!controller.signal.aborted) error.value = reason instanceof Error ? reason.message : '请求失败' }
async function load() {
  try {
    const [settings, tokenList, audit] = await Promise.all([
      api<{ mcp_enabled: boolean }>('/api/mcp/settings', { signal: controller.signal }),
      api<{ tokens: Token[] }>('/api/mcp/tokens', { signal: controller.signal }),
      api<{ logs: Audit[] }>('/api/mcp/audit-logs', { signal: controller.signal }),
    ])
    enabled.value = settings.mcp_enabled; tokens.value = tokenList.tokens; logs.value = audit.logs; loaded.value = true
  } catch (reason) { fail(reason) }
}
async function change(value: boolean) {
  busy.value = true; error.value = ''
  try { const result = await api<{ mcp_enabled: boolean }>('/api/mcp/settings', { method: 'PATCH', body: JSON.stringify({ mcp_enabled: value }), signal: controller.signal }); enabled.value = result.mcp_enabled }
  catch (reason) { fail(reason) } finally { busy.value = false }
}
async function create() {
  busy.value = true; error.value = ''
  try {
    const result = await api<{ token: string }>('/api/mcp/tokens', { method: 'POST', body: JSON.stringify({ name: name.value, scope: 'read', expires_at: new Date(Date.now() + days.value * 86400000).toISOString() }), signal: controller.signal })
    secret.value = result.token; await load()
  } catch (reason) { fail(reason) } finally { busy.value = false }
}
async function revoke(id: number) { busy.value = true; error.value = ''; try { await api(`/api/mcp/tokens/${id}`, { method: 'DELETE', signal: controller.signal }); await load() } catch (reason) { fail(reason) } finally { busy.value = false } }
async function copy(value: string) { try { await navigator.clipboard.writeText(value) } catch { error.value = '复制失败，请手动复制。' } }
onMounted(load)
onUnmounted(() => { secret.value = ''; controller.abort() })
</script>

<template>
  <n-card title="AI / MCP 集成" style="margin-top: 16px">
    <n-alert v-if="error" type="error">{{ error }}</n-alert>
    <p>通过 HTTPS 连接 MCP 客户端。令牌仅允许读取当前管理员可访问的资源。</p>
    <p>MCP 服务：<n-switch :value="enabled" :loading="busy" :disabled="!loaded" @update:value="change" /> {{ enabled ? '已启用' : '已关闭' }}</p>
    <p>服务地址：<code>{{ endpoint }}</code> <n-button size="small" @click="copy(endpoint)">复制地址</n-button></p>
    <h3>访问令牌</h3>
    <n-button :disabled="!loaded" @click="open = true">创建令牌</n-button>
    <div class="mcp-table-scroll"><table class="mcp-token-table"><thead><tr><th>名称</th><th>前缀</th><th>权限</th><th>创建时间</th><th>到期时间</th><th>最近使用</th><th>状态</th><th></th></tr></thead><tbody>
      <tr v-for="item in tokens" :key="item.id"><td>{{ item.name }}</td><td>{{ item.prefix }}…</td><td>只读</td><td>{{ date(item.created_at) }}</td><td>{{ date(item.expires_at) }}</td><td>{{ date(item.last_used_at) }}</td><td>{{ item.revoked_at ? '已撤销' : item.expires_at * 1000 <= Date.now() ? '已过期' : '有效' }}</td><td><n-popconfirm v-if="!item.revoked_at" @positive-click="revoke(item.id)"><template #trigger><n-button size="small" :disabled="busy">撤销</n-button></template>撤销后该令牌立即失效，确定撤销？</n-popconfirm></td></tr>
    </tbody></table></div>
    <h3>可用工具（只读）</h3><ul><li v-for="tool in tools" :key="tool[0]"><code>{{ tool[0] }}</code>：{{ tool[1] }}</li></ul>
    <h3>连接指南</h3><p>创建令牌并保存到客户端环境变量 VPS_PANEL_MCP_TOKEN；客户端使用 Bearer Token 访问服务地址。测试工具列表后即可查询；不再使用时请撤销令牌。</p><pre style="white-space: pre-wrap; overflow-wrap: anywhere">{{ command }}</pre><n-button size="small" @click="copy(command)">复制 Codex 命令</n-button>
    <h3>最近调用</h3><div class="mcp-table-scroll"><table class="mcp-audit-table"><thead><tr><th>时间</th><th>令牌</th><th>工具</th><th>状态</th><th>耗时</th></tr></thead><tbody><tr v-for="item in logs" :key="item.id"><td>{{ date(item.called_at) }}</td><td>{{ item.token_name }}</td><td>{{ item.tool_name }}</td><td>{{ item.success ? '成功' : '失败' }}</td><td>{{ item.duration_ms }} ms</td></tr></tbody></table></div>
  </n-card>
  <n-modal :show="open" :mask-closable="!busy" :close-on-esc="!busy" @update:show="value => { if (!value) close() }"><n-card class="account-modal-card" title="创建 MCP 令牌" :closable="!busy" @close="close"><template v-if="secret"><n-alert type="warning">令牌仅显示一次，请立即保存。</n-alert><p style="overflow-wrap: anywhere">{{ secret }}</p><n-button @click="copy(secret)">复制令牌</n-button></template><form v-else class="auth-form" @submit.prevent="create"><label><span>令牌名称</span><n-input v-model:value="name" maxlength="100" /></label><p>权限：只读</p><label><span>有效期</span><n-select v-model:value="days" :options="options" /></label><div class="modal-actions"><n-button :disabled="busy" @click="close">取消</n-button><n-button type="primary" attr-type="submit" :loading="busy">创建</n-button></div></form></n-card></n-modal>
</template>

<style scoped>
.mcp-table-scroll { overflow-x: auto; max-width: 100%; }
.mcp-token-table { min-width: 900px; }
.mcp-audit-table { min-width: 600px; }
.mcp-table-scroll th, .mcp-table-scroll td { padding: 6px 10px; text-align: left; white-space: nowrap; }
</style>
