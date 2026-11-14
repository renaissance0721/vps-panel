<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { NAlert, NButton, NInput, NPopconfirm } from 'naive-ui'
import { api } from '../../api/client'
import type { ServerRecord } from '../../types/server'
import { agentCapabilities, agentDeclaresCapability } from '../../server'

type Entry = { address: string; remark: string }
const props = defineProps<{ server: ServerRecord; readonly: boolean }>()
const emit = defineEmits<{ saved: [server: ServerRecord] }>()
const entries = ref<Entry[]>([])
const editing = ref<number | null>(null)
const formOpen = ref(false)
const address = ref('')
const remark = ref('')
const busy = ref(false)
const loaded = ref(false)
const error = ref('')
const message = ref('')
let controller: AbortController | undefined

// Canonical network keys detect duplicates before sending; the server validates independently.
function addressKey(raw: string): string | null {
  const parts = raw.trim().split('/')
  if (parts.length > 2 || !parts[0] || (parts.length === 2 && !/^\d+$/.test(parts[1]!))) return null
  const validIPv4 = (input: string) => {
    const octets = input.split('.')
    return octets.length === 4 && octets.every(part => /^(0|[1-9]\d{0,2})$/.test(part) && Number(part) <= 255)
  }
  let bits = 32
  let value = 0n
  if (parts[0]!.includes(':')) {
    bits = 128
    if (parts[0]!.includes('%') || (parts[0]!.includes('.') && !validIPv4(parts[0]!.split(':').at(-1)!))) return null
    try {
      const host = new URL(`http://[${parts[0]}]/`).hostname.slice(1, -1)
      const halves = host.split('::')
      const left = halves[0] ? halves[0].split(':') : []
      const right = halves.length > 1 && halves[1] ? halves[1].split(':') : []
      const groups = halves.length > 1 ? [...left, ...Array(8 - left.length - right.length).fill('0'), ...right] : left
      if (groups.length !== 8) return null
      for (const group of groups) value = (value << 16n) | BigInt(`0x${group}`)
      if (value >> 32n === 0xffffn) return null
    } catch { return null }
  } else {
    const octets = parts[0]!.split('.')
    if (!validIPv4(parts[0]!)) return null
    for (const part of octets) value = (value << 8n) | BigInt(part)
  }
  const prefix = parts.length === 2 ? Number(parts[1]) : bits
  if (!Number.isInteger(prefix) || prefix < 1 || prefix > bits) return null
  const shift = BigInt(bits - prefix)
  return `${bits}:${(value >> shift) << shift}/${prefix}`
}

async function load(id: number) {
  controller?.abort()
  const request = new AbortController()
  controller = request
  entries.value = []; loaded.value = false; error.value = ''; message.value = ''; formOpen.value = false
  try {
    const response = await api<{ entries: Entry[] }>(`/api/servers/${id}/china-inbound-whitelist`, { signal: request.signal })
    if (!request.signal.aborted) { entries.value = response.entries; loaded.value = true }
  } catch (reason) {
    if (!request.signal.aborted) error.value = reason instanceof Error ? reason.message : '白名单加载失败'
  }
}
watch(() => props.server.id, id => { void load(id) }, { immediate: true })
onUnmounted(() => controller?.abort())

function openForm(index: number | null) {
  editing.value = index
  address.value = index === null ? '' : entries.value[index]!.address
  remark.value = index === null ? '' : entries.value[index]!.remark
  error.value = ''; message.value = ''; formOpen.value = true
}
async function save(next: Entry[]) {
  const serverID = props.server.id
  busy.value = true; error.value = ''; message.value = ''
  try {
    const response = await api<{ entries: Entry[]; server: ServerRecord }>(`/api/servers/${serverID}/china-inbound-whitelist`, { method: 'PUT', body: JSON.stringify({ entries: next }), signal: controller?.signal })
    if (props.server.id !== serverID) return
    entries.value = response.entries; formOpen.value = false
    message.value = props.server.block_china_inbound ? '白名单已保存，请查看上方的配置应用状态。' : '白名单已保存，开启限制后才会应用。'
    emit('saved', response.server)
  } catch (reason) {
    if (!controller?.signal.aborted && props.server.id === serverID) error.value = reason instanceof Error ? reason.message : '保存失败'
  } finally { busy.value = false }
}
function submitEntry() {
  const key = addressKey(address.value)
  if (!key) { error.value = '请输入有效的 IPv4 / IPv6 地址或 CIDR，不能使用 /0。'; return }
  if (entries.value.some((entry, index) => index !== editing.value && addressKey(entry.address) === key)) { error.value = '该地址或网段已存在。'; return }
  if (editing.value === null && entries.value.length >= 128) { error.value = '最多添加 128 项。'; return }
  const next = entries.value.slice()
  const entry = { address: address.value.trim(), remark: remark.value.trim() }
  if (editing.value === null) next.push(entry); else next[editing.value] = entry
  void save(next)
}
</script>

<template>
  <div class="china-whitelist">
    <div class="section-heading"><strong>IP 白名单</strong><small>{{ entries.length }} 个地址</small></div>
    <p>以下来源 IP 不受中国 IP 入站限制影响，使用目标服务器实际看到的来源地址。仅影响受管 Proxy 和 Relay 端口，不影响 SSH、其他防火墙规则或服务认证。</p>
    <n-alert v-if="error" type="error">{{ error }} <n-button v-if="!loaded" size="tiny" @click="load(server.id)">重试</n-button></n-alert>
    <n-alert v-if="message" type="success">{{ message }}</n-alert>
    <n-alert v-if="server.block_china_inbound && entries.length && !agentDeclaresCapability(server, agentCapabilities.firewallCNWhitelist)" type="warning">当前 Agent 不支持白名单，请升级 Agent；已保存不代表规则已生效。</n-alert>
    <p v-if="!loaded && !error">正在加载白名单…</p>
    <p v-else-if="loaded && !entries.length">暂无白名单。</p>
    <div v-for="(entry, index) in entries" :key="entry.address" class="china-whitelist-row">
      <div><strong v-if="entry.remark">{{ entry.remark }}</strong><code>{{ entry.address }}</code></div>
      <div class="china-whitelist-actions"><n-button size="small" :disabled="readonly || busy" @click="openForm(index)">编辑</n-button><n-popconfirm @positive-click="save(entries.filter((_, i) => i !== index))"><template #trigger><n-button size="small" :disabled="readonly || busy">删除</n-button></template>删除后该来源可能被中国 IP 入站规则阻止，确定删除？</n-popconfirm></div>
    </div>
    <form v-if="formOpen" class="china-whitelist-form" @submit.prevent="submitEntry">
      <label>IP 地址或 CIDR<n-input v-model:value="address" placeholder="IPv4 / IPv6 地址或 CIDR" maxlength="80" /></label>
      <label>备注（可选）<n-input v-model:value="remark" maxlength="100" /></label>
      <div class="china-whitelist-actions"><n-button size="small" :disabled="busy" @click="formOpen = false">取消</n-button><n-button size="small" type="primary" attr-type="submit" :loading="busy">保存</n-button></div>
    </form>
    <n-button v-else size="small" :disabled="readonly || busy || !loaded || entries.length >= 128" @click="openForm(null)">+ 添加 IP 或 CIDR</n-button>
  </div>
</template>

<style scoped>
.china-whitelist { margin-top: 16px; padding-top: 14px; border-top: 1px solid var(--color-border); }
.china-whitelist p { color: var(--color-text-secondary); font-size: 12px; line-height: 1.5; }
.china-whitelist .n-alert { margin: 8px 0; }
.china-whitelist-row { display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 9px 0; border-bottom: 1px solid var(--color-border); }
.china-whitelist-row strong, .china-whitelist-row code { display: block; overflow-wrap: anywhere; }
.china-whitelist-row code { font-size: 12px; color: var(--color-text-secondary); }
.china-whitelist-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.china-whitelist-form { display: grid; gap: 10px; margin: 12px 0; }
.china-whitelist-form label { display: grid; gap: 5px; font-size: 12px; }
.china-whitelist > .n-button { margin-top: 10px; }
@media (max-width: 480px) { .china-whitelist-row { align-items: flex-start; flex-direction: column; } }
</style>
