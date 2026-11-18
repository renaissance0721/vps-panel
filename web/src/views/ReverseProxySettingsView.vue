<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { NAlert, NButton, NCard, NCheckbox, NInput, NSpin } from 'naive-ui'
import { api } from '../api/client'

type Settings = {
  trusted_proxy_ips: string[]
  source: 'default' | 'environment' | 'database'
  max_trusted_proxy_ips: number
}
type Diagnostics = {
  peer_ip: string
  peer_loopback: boolean
  peer_trusted: boolean
  backend_tls: boolean
  forwarded_status: 'missing' | 'http' | 'https' | 'invalid'
  https_accepted: boolean
}

const endpoint = '/api/admin/settings/reverse-proxy'
const settings = ref<Settings | null>(null)
const addresses = ref<string[]>([])
const manualIP = ref('')
const confirmed = ref(false)
const diagnostics = ref<Diagnostics | null>(null)
const loading = ref(true)
const saving = ref(false)
const checking = ref(false)
const error = ref('')
const diagnosticError = ref('')
const success = ref('')
const lifecycle = new AbortController()
const browserHTTPS = typeof window !== 'undefined' && window.location.protocol === 'https:'
const browserProtocol = browserHTTPS ? 'HTTPS' : 'HTTP'
const sourceLabels = { default: '默认（仅回环）', environment: '旧环境变量', database: '网页设置' }
const forwardedLabels = { missing: '缺少 X-Forwarded-Proto', http: '报告 HTTP', https: '报告 HTTPS', invalid: '重复、畸形或冲突' }

const addsTrust = computed(() => addresses.value.some(ip => !settings.value?.trusted_proxy_ips.includes(ip)))
const canAddCurrent = computed(() => {
  const result = diagnostics.value
  return !!result && browserHTTPS && !result.backend_tls && result.forwarded_status === 'https' &&
    !!result.peer_ip && !result.peer_trusted && !addresses.value.includes(result.peer_ip)
})
const diagnosticAdvice = computed(() => {
  const result = diagnostics.value
  if (!result) return ''
  if (!browserHTTPS) return '请用证书有效的 HTTPS 域名打开 Panel，再检测当前连接。'
  if (result.backend_tls) return 'Panel 收到了 TLS 连接，可从此 HTTPS 地址进行配对。'
  switch (result.forwarded_status) {
    case 'missing': return '请让终止 TLS 的代理覆盖 X-Forwarded-Proto，再检测当前连接。'
    case 'invalid': return '请让代理发送单个 X-Forwarded-Proto，清除 Forwarded，并检查 X-Forwarded-Scheme 与其一致。'
    case 'http': return '请检查 NPM 是否实际接收 HTTPS，以及转发协议是否反映实际连接。'
  }
  if (!result.peer_trusted) return '请核实 TCP 对端确实是你的代理，再添加地址并保存。'
  return '此请求通过了连接器 HTTPS 检查。代理链路仍须覆盖客户端协议头，并让明文后端仅对受控代理可达。'
})

// The browser canonicalizes IPv6 for duplicate detection. The API validates again.
function normalizeIP(raw: string): string | null {
  const value = raw.trim()
  if (/^(0|[1-9]\d{0,2})(\.(0|[1-9]\d{0,2})){3}$/.test(value)) {
    const octets = value.split('.').map(Number)
    return octets.every(part => part <= 255) && (octets[0]! < 224 || octets[0]! > 239) &&
      value !== '0.0.0.0' && value !== '255.255.255.255' ? value : null
  }
  if (!value.includes(':') || !/^[\da-f:.]+$/i.test(value)) return null
  try {
    const ip = new URL(`http://[${value}]/`).hostname.slice(1, -1)
    const mapped = /^::ffff:([\da-f]+):([\da-f]+)$/.exec(ip)
    if (mapped) {
      const high = parseInt(mapped[1]!, 16)
      const low = parseInt(mapped[2]!, 16)
      return normalizeIP([high >> 8, high & 255, low >> 8, low & 255].join('.'))
    }
    return ip !== '::' && !/^ff[\da-f]{2}:/.test(ip) ? ip : null
  } catch { return null }
}

function applySettings(value: Settings) {
  settings.value = value
  addresses.value = value.trusted_proxy_ips.map(ip => normalizeIP(ip) ?? ip)
  confirmed.value = false
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    applySettings(await api<Settings>(endpoint, { signal: lifecycle.signal }))
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '无法读取可信代理设置'
  } finally { loading.value = false }
}

function addIP(raw: string) {
  if (!settings.value || saving.value) return false
  error.value = ''; success.value = ''
  const ip = normalizeIP(raw)
  if (!ip) error.value = '请输入单个有效的 IPv4 或 IPv6 地址。'
  else if (addresses.value.includes(ip)) error.value = '此地址已在列表中。'
  else if (addresses.value.length >= settings.value.max_trusted_proxy_ips) error.value = `最多可保存 ${settings.value.max_trusted_proxy_ips} 个地址。`
  if (error.value || !ip) return false
  addresses.value = [...addresses.value, ip]
  confirmed.value = false
  return true
}

function addManual() {
  if (addIP(manualIP.value)) manualIP.value = ''
}

function removeIP(ip: string) {
  addresses.value = addresses.value.filter(value => value !== ip)
  confirmed.value = false
  error.value = ''; success.value = ''
}

async function check() {
  if (checking.value) return
  checking.value = true; diagnosticError.value = ''
  try {
    diagnostics.value = await api<Diagnostics>(endpoint + '/diagnostics', { signal: lifecycle.signal })
  } catch (reason) {
    diagnostics.value = null
    if (!lifecycle.signal.aborted) diagnosticError.value = reason instanceof Error ? reason.message : '无法检测当前连接'
  } finally { checking.value = false }
}

async function save() {
  if (!settings.value || saving.value || checking.value || (addsTrust.value && !confirmed.value)) return
  saving.value = true; error.value = ''; success.value = ''
  try {
    const value = await api<Settings>(endpoint, {
      method: 'PUT', signal: lifecycle.signal, body: JSON.stringify({ trusted_proxy_ips: addresses.value }),
    })
    applySettings(value)
    success.value = '可信代理已保存并生效。'
    await check()
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '无法保存可信代理设置'
  } finally { saving.value = false }
}

onMounted(load)
onUnmounted(() => lifecycle.abort())
</script>

<template>
  <section class="reverse-proxy-settings">
    <h2>网络与安全</h2>
    <n-card title="反向代理" size="small">
      <div class="proxy-content">
        <div class="proxy-actions">
          <n-button :loading="checking" :disabled="saving" @click="check">
            {{ diagnostics ? '测试 HTTPS 识别' : '检测当前连接' }}
          </n-button>
        </div>
        <n-alert v-if="diagnosticError" type="error">{{ diagnosticError }}</n-alert>
        <dl v-if="diagnostics" class="proxy-diagnostics">
          <dt>浏览器访问协议</dt><dd>{{ browserProtocol }}</dd>
          <dt>Panel 后端接收协议</dt><dd>{{ diagnostics.backend_tls ? 'HTTPS（直接 TLS）' : 'HTTP' }}</dd>
          <dt>当前 TCP 对端</dt><dd>{{ diagnostics.peer_ip || '无法识别' }}</dd>
          <dt>可信代理状态</dt><dd>{{ diagnostics.peer_loopback ? '可信回环地址' : diagnostics.peer_trusted ? '已信任' : '未信任' }}</dd>
          <dt>协议转发头</dt><dd>{{ forwardedLabels[diagnostics.forwarded_status] }}</dd>
          <dt>连接器 HTTPS 检查</dt><dd>{{ diagnostics.https_accepted ? '通过' : '未通过' }}</dd>
          <dt>WebSocket</dt><dd>未验证</dd>
        </dl>
        <n-alert v-if="diagnostics" :type="diagnostics.https_accepted && browserHTTPS ? 'info' : 'warning'">{{ diagnosticAdvice }}</n-alert>
        <h3>可信代理地址</h3>
        <n-spin v-if="loading" size="small" />
        <n-button v-else-if="!settings" @click="load">重新加载</n-button>
        <template v-else>
          <p class="proxy-help">配置来源 · {{ sourceLabels[settings.source] }}。回环代理默认受信任。</p>
          <n-alert type="warning">
            只信任由你控制、会覆盖客户端协议头的代理。信任共享 NAT 或 Docker 网关会同时信任经该地址到达的其他客户端。遇到共享地址，请先隔离后端网络。
          </n-alert>
          <p v-if="addresses.length === 0" class="proxy-help">暂无额外的可信代理地址。</p>
          <ul v-else class="proxy-addresses">
            <li v-for="ip in addresses" :key="ip"><code>{{ ip }}</code><n-button size="small" :disabled="saving" :aria-label="`删除 ${ip}`" @click="removeIP(ip)">删除</n-button></li>
          </ul>
          <div class="proxy-actions">
            <n-button v-if="canAddCurrent" :disabled="saving || checking" @click="addIP(diagnostics!.peer_ip)">添加当前来源 IP</n-button>
          </div>
          <form class="proxy-actions" @submit.prevent="addManual">
            <n-input v-model:value="manualIP" aria-label="手动输入代理 IP" placeholder="输入 IPv4 或 IPv6 地址" maxlength="64" :disabled="saving" />
            <n-button attr-type="submit" :disabled="saving || !manualIP.trim()">手动添加 IP</n-button>
          </form>
          <n-checkbox v-if="addsTrust" v-model:checked="confirmed" :disabled="saving">我已确认新增地址只由受控代理使用</n-checkbox>
          <div class="proxy-actions">
            <n-button type="primary" :loading="saving" :disabled="checking || (addsTrust && !confirmed)" @click="save">保存配置</n-button>
          </div>
        </template>
        <n-alert v-if="error" type="error">{{ error }}</n-alert>
        <n-alert v-if="success" type="success">{{ success }}</n-alert>
      </div>
    </n-card>
  </section>
</template>

<style scoped>
.reverse-proxy-settings h2 { margin: 0 0 10px; font-size: 18px; }
.proxy-content { display: grid; gap: 12px; }
.proxy-content h3, .proxy-help, .proxy-diagnostics { margin: 0; }
.proxy-help, .proxy-diagnostics dt { color: var(--color-text-secondary); }
.proxy-diagnostics { display: grid; grid-template-columns: minmax(0, auto) minmax(0, 1fr); gap: 6px 14px; }
.proxy-diagnostics dd { margin: 0; overflow-wrap: anywhere; }
.proxy-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.proxy-actions:empty { display: none; }
.proxy-actions .n-input { flex: 1 1 200px; min-width: 0; }
.proxy-addresses { list-style: none; margin: 0; padding: 0; }
.proxy-addresses li { display: flex; gap: 8px; flex-wrap: wrap; justify-content: space-between; align-items: center; padding: 5px 0; }
.proxy-addresses code { overflow-wrap: anywhere; min-width: 0; }
</style>
