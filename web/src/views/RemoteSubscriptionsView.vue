<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NInputNumber, NModal, NSpin, NSwitch, NTag } from 'naive-ui'
import { api } from '../api/client'
import { formatTime } from '../format'
import { formatClientTrafficBytes } from '../proxy'

type RemoteNode = { id: number; name: string; protocol: string; format: string; position: number }
type RemoteSource = {
  id: number; mode: 'url' | 'manual'; name: string; user_agent: string; enabled: boolean; interval_seconds: number
  last_attempt_at: string | null; last_success_at: string | null; last_error: string
  refreshing: boolean
  warnings: string[]; node_count: number; upload: number | null; download: number | null
  total: number | null; expire: number | null
}
type Preview = { nodes: RemoteNode[]; warnings: string[]; usable: boolean; upload: number | null; download: number | null; total: number | null; expire: number | null }

const sources = ref<RemoteSource[]>([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const importNotice = ref('')
const formOpen = ref(false)
const editing = ref<RemoteSource | null>(null)
const name = ref('')
const mode = ref<'url' | 'manual'>('url')
const url = ref('')
const userAgent = ref('')
const enabled = ref(true)
const intervalMinutes = ref<number | null>(60)
const formError = ref('')
const preview = ref<Preview | null>(null)
const nodesOpen = ref(false)
const nodesTitle = ref('')
const nodes = ref<RemoteNode[]>([])
const lifecycle = new AbortController()
let pollTimer: number | undefined
watch([url, userAgent, mode], () => { preview.value = null })

async function load() {
  const response = await api<{ sources: RemoteSource[] }>('/api/remote-subscriptions', { signal: lifecycle.signal })
  if (!lifecycle.signal.aborted) sources.value = response.sources
}

function openForm(source?: RemoteSource) {
  editing.value = source ?? null
  mode.value = source?.mode === 'manual' ? 'manual' : 'url'
  name.value = source?.name ?? ''
  url.value = ''
  userAgent.value = source?.user_agent ?? ''
  enabled.value = source?.enabled ?? true
  intervalMinutes.value = source ? source.interval_seconds / 60 : 60
  formError.value = ''
  preview.value = null
  formOpen.value = true
}

function closeForm() {
  formOpen.value = false
  url.value = ''
  preview.value = null
}

async function save() {
  if (busy.value) return
  if (!name.value.trim() || (mode.value === 'url' && !editing.value && !url.value.trim()) || intervalMinutes.value === null || !Number.isInteger(intervalMinutes.value) || intervalMinutes.value < 5 || intervalMinutes.value > 10080) {
    formError.value = '请填写名称；网络来源需要订阅地址，刷新间隔为 5–10080 分钟。'
    return
  }
  busy.value = true; formError.value = ''
  try {
    await api(`/api/remote-subscriptions${editing.value ? `/${editing.value.id}` : ''}`, {
      method: editing.value ? 'PUT' : 'POST',
      signal: lifecycle.signal,
      body: JSON.stringify({ name: name.value.trim(), url: mode.value === 'manual' ? '' : url.value.trim(), user_agent: mode.value === 'manual' ? '' : userAgent.value.trim(), enabled: enabled.value, interval_seconds: intervalMinutes.value * 60, ...(!editing.value && mode.value === 'manual' ? { mode: 'manual' } : {}) }),
    })
    closeForm()
    await load()
  } catch (reason) { formError.value = reason instanceof Error ? reason.message : '保存失败' }
  finally { busy.value = false }
}

async function testSource() {
  if (busy.value) return
  if (!url.value.trim() && !editing.value) { formError.value = '请先填写订阅地址。'; return }
  busy.value = true; formError.value = ''; preview.value = null
  try {
    const response = url.value.trim()
      ? await api<{ preview: Preview }>('/api/remote-subscriptions/test', { method: 'POST', signal: lifecycle.signal, body: JSON.stringify({ url: url.value.trim(), user_agent: userAgent.value.trim() }) })
      : await api<{ preview: Preview }>(`/api/remote-subscriptions/${editing.value!.id}/test`, { method: 'POST', signal: lifecycle.signal, body: JSON.stringify({ user_agent: userAgent.value.trim() }) })
    preview.value = response.preview
  } catch (reason) { formError.value = reason instanceof Error ? reason.message : '测试失败' }
  finally { busy.value = false }
}

async function refresh(source: RemoteSource) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await api(`/api/remote-subscriptions/${source.id}/refresh`, { method: 'POST', signal: lifecycle.signal }) }
  catch (reason) { error.value = reason instanceof Error ? reason.message : '刷新失败' }
  finally {
    try { await load() } catch { error.value ||= '无法加载机场订阅' }
    busy.value = false
  }
}

async function importFile(source: RemoteSource, event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || busy.value) return
  error.value = ''; importNotice.value = ''
  if (file.size === 0 || file.size > 4 * 1024 * 1024) { error.value = '请选择非空且不超过 4 MiB 的文件。'; return }
  busy.value = true
  try {
    const form = new FormData(); form.append('file', file)
    const response = await fetch(`/api/remote-subscriptions/${source.id}/import`, { method: 'POST', body: form, cache: 'no-store', credentials: 'same-origin', signal: lifecycle.signal })
    const result = await response.json().catch(() => null) as { source?: RemoteSource; error?: string } | null
    if (!response.ok || !result?.source) throw new Error(result?.error ?? '导入失败')
    importNotice.value = `已为“${source.name}”导入 ${result.source.node_count} 个节点。`
    await load()
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '导入失败' }
  finally { busy.value = false }
}

async function remove(source: RemoteSource) {
  if (busy.value || !window.confirm(`删除机场订阅“${source.name}”及其缓存节点？`)) return
  busy.value = true; error.value = ''
  try { await api(`/api/remote-subscriptions/${source.id}`, { method: 'DELETE', signal: lifecycle.signal }); await load() }
  catch (reason) { error.value = reason instanceof Error ? reason.message : '删除失败' }
  finally { busy.value = false }
}

async function showNodes(source: RemoteSource) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const response = await api<{ nodes: RemoteNode[] }>(`/api/remote-subscriptions/${source.id}/nodes`, { signal: lifecycle.signal })
    nodes.value = response.nodes; nodesTitle.value = source.name; nodesOpen.value = true
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '加载节点失败' }
  finally { busy.value = false }
}

function traffic(value: number | null) { return value === null ? '未知' : formatClientTrafficBytes(value) }
function expiry(value: number | null) { return value === null ? '未知' : value === 0 ? '未设置' : formatTime(new Date(value * 1000).toISOString()) }

onMounted(async () => {
  try { await load() } catch (reason) { error.value = reason instanceof Error ? reason.message : '无法加载机场订阅' }
  finally { loading.value = false }
  if (!lifecycle.signal.aborted) pollTimer = window.setInterval(() => { if (!busy.value) void load().catch(() => undefined) }, 10_000)
})
onUnmounted(() => {
  lifecycle.abort()
  if (pollTimer !== undefined) window.clearInterval(pollTimer)
  url.value = ''
})
</script>

<template>
  <section class="subscription-section remote-subscriptions">
    <div class="section-heading"><span></span><n-button type="primary" @click="openForm()">添加机场订阅</n-button></div>
    <n-alert v-if="error" type="error" closable @close="error = ''">{{ error }}</n-alert>
    <n-alert v-if="importNotice" type="success" closable @close="importNotice = ''">{{ importNotice }}</n-alert>
    <div v-if="loading" class="loading-row"><n-spin size="small" />正在加载机场订阅…</div>
    <n-empty v-else-if="sources.length === 0" description="暂无机场订阅" />
    <n-card v-for="source in sources" v-else :key="source.id" :title="source.name" class="remote-source-card">
      <template #header-extra><n-tag :type="source.enabled ? 'success' : 'default'">{{ source.enabled ? '启用' : '停用' }}</n-tag></template>
      <div class="remote-summary">
        <span>节点 {{ source.node_count }}</span><span>{{ source.mode === 'manual' ? '手动导入来源' : `间隔 ${source.interval_seconds / 60} 分钟` }}</span>
        <span v-if="source.refreshing">正在刷新…</span>
        <span>上次成功：{{ source.last_success_at ? formatTime(source.last_success_at) : '从未' }}</span>
        <span>上次尝试：{{ source.last_attempt_at ? formatTime(source.last_attempt_at) : '从未' }}</span>
        <span>上传 {{ traffic(source.upload) }} · 下载 {{ traffic(source.download) }} · 总量 {{ traffic(source.total) }}</span>
        <span>到期：{{ expiry(source.expire) }}</span>
      </div>
      <n-alert v-if="source.last_error" type="error">{{ source.last_error }}</n-alert>
      <n-alert v-if="source.warnings.length" type="warning">{{ source.warnings.join('；') }}</n-alert>
      <div class="remote-actions">
        <n-button size="small" :disabled="busy" @click="showNodes(source)">查看节点</n-button>
        <n-button v-if="source.mode !== 'manual'" size="small" :disabled="busy || source.refreshing" @click="refresh(source)">立即刷新</n-button>
        <label class="remote-upload" :class="{ 'remote-upload-disabled': busy || source.refreshing }">导入文件<input type="file" :disabled="busy || source.refreshing" @change="importFile(source, $event)" /></label>
        <n-button size="small" :disabled="busy" @click="openForm(source)">编辑</n-button>
        <n-button size="small" type="error" secondary :disabled="busy" @click="remove(source)">删除</n-button>
      </div>
      <p v-if="source.mode !== 'manual'" class="form-help">导入文件后仍会按计划刷新；成功的网络刷新会更新缓存。</p>
    </n-card>
    <n-modal v-model:show="formOpen" @after-leave="closeForm">
      <n-card class="client-form-card subscription-form-card" :title="editing ? '编辑机场订阅' : '添加机场订阅'" closable @close="closeForm">
        <form class="auth-form" @submit.prevent="save">
          <n-alert v-if="formError" type="error">{{ formError }}</n-alert>
          <label><span>名称</span><n-input v-model:value="name" maxlength="100" /></label>
          <label v-if="!editing"><span>来源类型</span><select v-model="mode" class="settings-input" :disabled="busy"><option value="url">订阅地址</option><option value="manual">手动导入文件</option></select></label>
          <template v-if="mode === 'url'">
            <label><span>{{ editing ? '替换订阅地址（留空保留原地址）' : '订阅地址' }}</span><n-input v-model:value="url" type="password" show-password-on="click" :disabled="busy" :input-props="{ autocomplete: 'off', spellcheck: false }" /></label>
            <label><span>User-Agent（可选）</span><n-input v-model:value="userAgent" maxlength="512" :disabled="busy" /></label>
            <label><span>刷新间隔（分钟，最少 5 分钟）</span><n-input-number v-model:value="intervalMinutes" :min="5" :max="10080" :precision="0" /></label>
          </template>
          <p v-else class="form-help">手动来源不会自动联网刷新；保存后在来源卡片导入文件。</p>
          <label><span>启用来源</span><n-switch v-model:value="enabled" /></label>
          <div class="remote-actions"><n-button v-if="mode === 'url'" :disabled="busy" @click="testSource">测试并预览</n-button><n-button type="primary" attr-type="submit" :loading="busy">保存</n-button></div>
          <div v-if="preview" class="remote-preview">
            <strong>{{ preview.usable ? `测试成功：${preview.nodes.length} 个节点` : '未发现可缓存节点' }}</strong>
            <span>上传 {{ traffic(preview.upload) }} · 下载 {{ traffic(preview.download) }} · 总量 {{ traffic(preview.total) }} · 到期 {{ expiry(preview.expire) }}</span>
            <n-alert v-if="preview.warnings.length" type="warning">{{ preview.warnings.join('；') }}</n-alert>
            <div v-for="(node, index) in preview.nodes" :key="index">{{ node.name }} · {{ node.protocol }}</div>
          </div>
        </form>
      </n-card>
    </n-modal>
    <n-modal v-model:show="nodesOpen"><n-card class="client-form-card subscription-form-card" :title="`${nodesTitle} · 缓存节点`" closable @close="nodesOpen = false">
      <n-empty v-if="nodes.length === 0" description="暂无缓存节点" />
      <div v-for="node in nodes" :key="node.id" class="remote-node">{{ node.name }} <n-tag size="small">{{ node.protocol }}</n-tag></div>
    </n-card></n-modal>
  </section>
</template>

<style scoped>
.remote-subscriptions { display: grid; gap: 12px; }
.remote-summary { display: flex; flex-wrap: wrap; gap: 8px 18px; margin-bottom: 12px; }
.remote-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
.remote-upload { position: relative; display: inline-flex; align-items: center; padding: 0 10px; min-height: 28px; border: 1px solid var(--border-color, #d0d0d0); border-radius: 4px; cursor: pointer; }
.remote-upload input { position: absolute; inset: 0; opacity: 0; width: 100%; cursor: pointer; }
.remote-upload-disabled { opacity: .5; cursor: not-allowed; }
.remote-upload:focus-within { outline: 2px solid var(--color-primary, #36ad6a); outline-offset: 2px; }
.remote-preview { max-height: 240px; overflow: auto; display: grid; gap: 6px; }
.remote-node { display: flex; justify-content: space-between; gap: 12px; padding: 8px 0; overflow-wrap: anywhere; }
</style>
