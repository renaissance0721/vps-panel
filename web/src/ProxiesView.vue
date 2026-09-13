<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NModal, NSpin, NTag } from 'naive-ui'
import {
  clientTrafficUsedBytes,
  formatClientTrafficBytes,
  proxyListProtocolFields,
  shadowsocksMethods,
  showsVLESSClientFields,
  type ProxyProtocol,
  type ShadowsocksMethod,
  type ClientMetrics,
} from './proxy'

type ServerOption = {
  id: number
  name: string
  system_info: { public_ipv4: string } | null
}

type ProxyConfig = {
	transport?: 'tcp'
	security?: 'tls' | 'reality'
	server_flow?: 'xtls-rprx-vision'
	server_name?: string
	fingerprint?: 'chrome'
	tls_certificate_configured: boolean
  reality_target?: string
  reality_public_key?: string
	reality_short_id?: string
	method?: ShadowsocksMethod
	network?: 'tcp,udp'
}

type ClientSummary = {
  id: number
  proxy_id: number
  name: string
  uuid_summary: string
  client_udp443: boolean
  enabled: boolean
  metrics: ClientMetrics
  created_at: string
  updated_at: string
}

type ClientRecord = {
  id: number
  proxy_id: number
  name: string
	uuid?: string
  client_udp443: boolean
  enabled: boolean
  metrics: ClientMetrics
  created_at: string
  updated_at: string
}

type ProxyRecord = {
  id: number
  server_id: number
  server_name: string
  server_ipv4: string[]
  server_ipv6: string[]
  server_public_ipv4: string
  name: string
	protocol: ProxyProtocol
  listen_port: number
  entry_host_mode: 'auto' | 'manual'
  entry_host: string
  entry_address: string
  enabled: boolean
  config: ProxyConfig
  clients?: ClientSummary[]
  created_at: string
  updated_at: string
}

type ClientShare = {
  client: ClientRecord
  proxy_name: string
  address: string
  port: number
	protocol: ProxyProtocol
	method?: ShadowsocksMethod
	network?: 'tcp,udp'
	security?: 'tls' | 'reality'
	server_name?: string
	fingerprint?: string
	flow?: string
  reality_public_key?: string
  reality_short_id?: string
  uri: string
}

const props = defineProps<{ servers: ServerOption[] }>()

const proxies = ref<ProxyRecord[]>([])
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const search = ref('')

const proxyFormOpen = ref(false)
const proxyFormMode = ref<'create' | 'edit'>('create')
const editingProxyID = ref<number | null>(null)
const proxyName = ref('')
const proxyServerID = ref<number | null>(null)
const proxyPort = ref(443)
const proxyEntryHostMode = ref<'auto' | 'manual'>('auto')
const proxyEntryHost = ref('')
const proxyEnabled = ref(true)
const proxyProtocol = ref<ProxyProtocol>('vless')
const proxyMethod = ref<ShadowsocksMethod>('2022-blake3-aes-128-gcm')
const proxySecurity = ref<'tls' | 'reality'>('reality')
const proxyServerName = ref('')
const proxyCertificate = ref('')
const proxyPrivateKey = ref('')
const proxyRealityTarget = ref('')
const firstClientName = ref('默认客户端')
const firstClientUDP443 = ref(false)

const proxyDetailOpen = ref(false)
const selectedProxy = ref<ProxyRecord | null>(null)

const clientFormOpen = ref(false)
const clientFormMode = ref<'create' | 'edit'>('create')
const editingClientID = ref<number | null>(null)
const clientName = ref('')
const clientEnabled = ref(true)
const clientUDP443 = ref(false)

const clientDetailOpen = ref(false)
const selectedShare = ref<ClientShare | null>(null)
const copied = ref<'uuid' | 'uri' | null>(null)
const copiedClientID = ref<number | null>(null)

const selectedServerPublicIPv4 = computed(() =>
  props.servers.find((server) => server.id === proxyServerID.value)?.system_info?.public_ipv4 ?? '',
)

const filteredProxies = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  if (!keyword) return proxies.value
  return proxies.value.filter((value) =>
    [value.name, value.server_name, value.entry_address, value.entry_host, value.config.security ?? '', value.config.method ?? '']
      .some((field) => field.toLowerCase().includes(keyword)),
  )
})

async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    cache: 'no-store',
    credentials: 'same-origin',
    ...options,
    headers: options?.body
      ? { 'Content-Type': 'application/json', ...options.headers }
      : options?.headers,
  })
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { error?: string } | null
    throw new Error(body?.error ?? `请求失败（${response.status}）`)
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

async function run(action: () => Promise<void>) {
  submitting.value = true
  error.value = ''
  try {
    await action()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '操作失败'
  } finally {
    submitting.value = false
  }
}

async function loadProxies() {
  const response = await api<{ proxies: ProxyRecord[] }>('/api/proxies')
  proxies.value = response.proxies
}

function openCreateProxy() {
  resetProxyForm()
  proxyFormMode.value = 'create'
  proxyServerID.value = props.servers[0]?.id ?? null
  proxyFormOpen.value = true
}

function openEditProxy(value: ProxyRecord) {
  proxyFormMode.value = 'edit'
  editingProxyID.value = value.id
  proxyName.value = value.name
  proxyServerID.value = value.server_id
  proxyPort.value = value.listen_port
  proxyEntryHostMode.value = value.entry_host_mode
  proxyEntryHost.value = value.entry_host
  proxyEnabled.value = value.enabled
	proxyProtocol.value = value.protocol
	proxyMethod.value = value.config.method ?? '2022-blake3-aes-128-gcm'
	proxySecurity.value = value.config.security ?? 'reality'
	proxyServerName.value = value.config.server_name ?? ''
  proxyCertificate.value = ''
  proxyPrivateKey.value = ''
  proxyRealityTarget.value = value.config.reality_target ?? ''
  proxyFormOpen.value = true
}

function resetProxyForm() {
  editingProxyID.value = null
  proxyName.value = ''
  proxyPort.value = 443
  proxyEntryHostMode.value = 'auto'
  proxyEntryHost.value = ''
  proxyEnabled.value = true
	proxyProtocol.value = 'vless'
	proxyMethod.value = '2022-blake3-aes-128-gcm'
  proxySecurity.value = 'reality'
  proxyServerName.value = ''
  proxyCertificate.value = ''
  proxyPrivateKey.value = ''
  proxyRealityTarget.value = ''
  firstClientName.value = '默认客户端'
  firstClientUDP443.value = false
}

async function saveProxy() {
	if (!proxyName.value.trim() || (proxyProtocol.value === 'vless' && !proxyServerName.value.trim())) {
		error.value = proxyProtocol.value === 'vless' ? '请填写节点名称和 SNI' : '请填写节点名称'
    return
  }
  if (!Number.isInteger(proxyPort.value) || proxyPort.value < 1 || proxyPort.value > 65535) {
    error.value = '监听端口必须在 1–65535 之间'
    return
  }
  if (proxyFormMode.value === 'create' && proxyServerID.value === null) {
    error.value = '请选择服务器'
    return
  }
  if (proxyEntryHostMode.value === 'manual' && !proxyEntryHost.value.trim()) {
    error.value = '请填写手动入口地址'
    return
  }
  await run(async () => {
		const common = {
      name: proxyName.value,
      listen_port: proxyPort.value,
      entry_host_mode: proxyEntryHostMode.value,
      entry_host: proxyEntryHost.value,
      enabled: proxyEnabled.value,
    }
		const protocolConfig = proxyProtocol.value === 'vless'
			? {
				security: proxySecurity.value,
				server_name: proxyServerName.value,
				certificate: proxyCertificate.value,
				private_key: proxyPrivateKey.value,
				reality_target: proxyRealityTarget.value,
			}
			: {}
    let value: ProxyRecord
    if (proxyFormMode.value === 'create') {
      const response = await api<{ proxy: ProxyRecord }>('/api/proxies', {
        method: 'POST',
        body: JSON.stringify({
          ...common,
					...protocolConfig,
          server_id: proxyServerID.value,
					protocol: proxyProtocol.value,
					method: proxyProtocol.value === 'shadowsocks' ? proxyMethod.value : undefined,
          first_client_name: firstClientName.value,
					first_client_udp443: proxyProtocol.value === 'vless' && firstClientUDP443.value,
        }),
      })
      value = response.proxy
    } else {
      const response = await api<{ proxy: ProxyRecord }>(`/api/proxies/${editingProxyID.value}`, {
        method: 'PATCH',
			body: JSON.stringify({ ...common, ...protocolConfig }),
      })
      value = response.proxy
    }
    proxyFormOpen.value = false
    await loadProxies()
    if (selectedProxy.value?.id === value.id || proxyFormMode.value === 'create') {
      await showProxy(value.id)
    }
  })
}

async function showProxy(id: number) {
  await run(async () => {
    const response = await api<{ proxy: ProxyRecord }>(`/api/proxies/${id}`)
    selectedProxy.value = response.proxy
    proxyDetailOpen.value = true
  })
}

async function refreshSelectedProxy() {
  if (!selectedProxy.value) return
  const response = await api<{ proxy: ProxyRecord }>(`/api/proxies/${selectedProxy.value.id}`)
  selectedProxy.value = response.proxy
}

async function toggleProxy(value: ProxyRecord) {
  await run(async () => {
    await api(`/api/proxies/${value.id}`, {
      method: 'PATCH',
      body: JSON.stringify({ enabled: !value.enabled }),
    })
    await loadProxies()
    if (selectedProxy.value?.id === value.id) await refreshSelectedProxy()
  })
}

async function removeProxy(value: ProxyRecord) {
  if (!window.confirm(`确定删除代理节点“${value.name}”及其全部客户端吗？`)) return
  await run(async () => {
    await api(`/api/proxies/${value.id}`, { method: 'DELETE' })
    if (selectedProxy.value?.id === value.id) {
      proxyDetailOpen.value = false
      selectedProxy.value = null
    }
    await loadProxies()
  })
}

function openCreateClient() {
  if (!selectedProxy.value) return
  clientFormMode.value = 'create'
  editingClientID.value = null
  clientName.value = ''
  clientEnabled.value = true
  clientUDP443.value = false
  clientFormOpen.value = true
}

async function openEditClient(client: ClientSummary) {
  await run(async () => {
    const response = await api<{ client: ClientRecord }>(`/api/clients/${client.id}`)
    clientFormMode.value = 'edit'
    editingClientID.value = client.id
    clientName.value = response.client.name
    clientEnabled.value = response.client.enabled
    clientUDP443.value = response.client.client_udp443
    clientFormOpen.value = true
  })
}

async function saveClient() {
  if (!selectedProxy.value || !clientName.value.trim()) {
    error.value = '请填写客户端名称'
    return
  }
	const proxy = selectedProxy.value
  await run(async () => {
    const body = JSON.stringify({
      name: clientName.value,
      enabled: clientEnabled.value,
			client_udp443: proxy.protocol === 'vless' && clientUDP443.value,
    })
    if (clientFormMode.value === 'create') {
		await api(`/api/proxies/${proxy.id}/clients`, { method: 'POST', body })
    } else {
      await api(`/api/clients/${editingClientID.value}`, { method: 'PATCH', body })
    }
    clientFormOpen.value = false
    await Promise.all([loadProxies(), refreshSelectedProxy()])
  })
}

async function toggleClient(client: ClientSummary) {
  await run(async () => {
    await api(`/api/clients/${client.id}`, {
      method: 'PATCH',
      body: JSON.stringify({ enabled: !client.enabled }),
    })
    await Promise.all([loadProxies(), refreshSelectedProxy()])
  })
}

async function removeClient(client: ClientSummary) {
  if (!window.confirm(`确定删除客户端“${client.name}”吗？`)) return
  await run(async () => {
    await api(`/api/clients/${client.id}`, { method: 'DELETE' })
    await Promise.all([loadProxies(), refreshSelectedProxy()])
  })
}

async function loadShare(clientID: number): Promise<ClientShare> {
  const response = await api<{ share: ClientShare }>(`/api/clients/${clientID}/share`)
  return response.share
}

async function showClient(client: ClientSummary) {
  await run(async () => {
    selectedShare.value = await loadShare(client.id)
    copied.value = null
    clientDetailOpen.value = true
  })
}

async function copyClientURI(client: ClientSummary) {
  await run(async () => {
    const share = await loadShare(client.id)
    await navigator.clipboard.writeText(share.uri)
    copiedClientID.value = client.id
  })
}

async function copyValue(type: 'uuid' | 'uri', value: string | undefined) {
	if (!value) return
  try {
    await navigator.clipboard.writeText(value)
    copied.value = type
  } catch {
    error.value = '无法自动复制，请手动复制内容'
  }
}

function formatTime(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

onMounted(async () => {
  try {
    await loadProxies()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法加载代理节点'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <n-alert v-if="error" class="page-alert" type="error" closable @close="error = ''">
    {{ error }}
  </n-alert>

  <div class="proxy-toolbar">
    <n-input v-model:value="search" clearable placeholder="搜索名称、服务器、入口地址或安全层" />
    <n-button type="primary" :disabled="props.servers.length === 0" @click="openCreateProxy">
      新增代理节点
    </n-button>
  </div>

  <n-card :bordered="true">
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载代理节点…</span></div>
    <n-empty v-else-if="filteredProxies.length === 0" description="当前没有代理节点" />
    <div v-else class="server-table-wrap">
      <table class="server-table proxy-table">
        <thead>
          <tr>
            <th>名称</th><th>服务器</th><th>入口地址</th><th>出口 IP</th><th>端口</th>
            <th>协议</th><th>传输</th><th>安全层</th><th>流控</th><th>状态</th><th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="value in filteredProxies" :key="value.id">
            <td>{{ value.name }}</td>
            <td>{{ value.server_name }}</td>
            <td>
              <span>{{ value.entry_address || '未检测' }}</span>
              <small class="secondary-text">{{ value.entry_host_mode === 'auto' ? '自动检测' : '手动' }}</small>
            </td>
            <td>--</td>
            <td>{{ value.listen_port }}</td>
			<td>{{ proxyListProtocolFields(value.protocol, value.config.security).protocol }}</td>
			<td>{{ proxyListProtocolFields(value.protocol, value.config.security).transport }}</td>
			<td>{{ proxyListProtocolFields(value.protocol, value.config.security).security }}</td>
			<td>{{ proxyListProtocolFields(value.protocol, value.config.security).flow }}</td>
            <td><n-tag :type="value.enabled ? 'success' : 'default'" size="small">{{ value.enabled ? '启用' : '禁用' }}</n-tag></td>
            <td class="server-actions">
              <n-button size="small" secondary @click="showProxy(value.id)">查看</n-button>
              <n-button size="small" secondary @click="openEditProxy(value)">编辑</n-button>
              <n-button size="small" secondary @click="toggleProxy(value)">{{ value.enabled ? '禁用' : '启用' }}</n-button>
              <n-button size="small" type="error" secondary @click="removeProxy(value)">删除</n-button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </n-card>

  <n-modal v-model:show="proxyFormOpen" :mask-closable="!submitting">
    <n-card class="proxy-form-card" :title="proxyFormMode === 'create' ? '新增代理节点' : '编辑代理节点'" :bordered="false" closable @close="proxyFormOpen = false">
      <form class="proxy-form" @submit.prevent="saveProxy">
        <label><span>名称</span><n-input v-model:value="proxyName" maxlength="100" /></label>
        <label>
          <span>服务器</span>
          <select v-model.number="proxyServerID" class="settings-input" :disabled="proxyFormMode === 'edit'">
            <option v-for="server in props.servers" :key="server.id" :value="server.id">{{ server.name }}</option>
          </select>
        </label>
		<label v-if="proxyFormMode === 'create'">
			<span>协议</span>
			<select v-model="proxyProtocol" class="settings-input">
				<option value="vless">VLESS</option><option value="shadowsocks">Shadowsocks</option>
			</select>
		</label>
		<div v-else class="fixed-fields"><span>协议：{{ proxyProtocol === 'vless' ? 'VLESS' : 'Shadowsocks' }}</span></div>
        <label><span>监听端口</span><input v-model.number="proxyPort" class="settings-input" type="number" min="1" max="65535" /></label>
        <label>
          <span>入口地址模式</span>
          <select v-model="proxyEntryHostMode" class="settings-input">
            <option value="auto">自动检测</option><option value="manual">手动输入</option>
          </select>
        </label>
        <label v-if="proxyEntryHostMode === 'manual'"><span>入口 IP / 域名</span><n-input v-model:value="proxyEntryHost" placeholder="例如：1.2.3.4 或 jp.example.com" /></label>
        <p v-else>自动使用服务器公网 IPv4。当前公网 IPv4：{{ selectedServerPublicIPv4 || '未检测到' }}</p>
		<div v-if="proxyProtocol === 'vless'" class="fixed-fields"><span>传输：TCP</span><span>流控：XTLS Vision</span></div>
		<template v-if="proxyProtocol === 'vless'">
		<label>
          <span>安全层</span>
          <select v-model="proxySecurity" class="settings-input">
            <option value="reality">REALITY</option><option value="tls">TLS</option>
          </select>
        </label>
        <label><span>SNI / Server Name</span><n-input v-model:value="proxyServerName" placeholder="例如：www.example.com" /></label>
        <template v-if="proxySecurity === 'tls'">
          <label><span>证书 PEM</span><n-input v-model:value="proxyCertificate" type="textarea" :autosize="{ minRows: 4 }" :placeholder="proxyFormMode === 'edit' ? '留空则保留现有证书' : '粘贴完整证书 PEM'" /></label>
          <label><span>私钥 PEM</span><n-input v-model:value="proxyPrivateKey" type="textarea" :autosize="{ minRows: 4 }" :placeholder="proxyFormMode === 'edit' ? '留空则保留现有私钥' : '粘贴匹配的私钥 PEM'" /></label>
        </template>
		<label v-else><span>REALITY 目标地址</span><n-input v-model:value="proxyRealityTarget" placeholder="例如：www.example.com:443" /></label>
		</template>
		<template v-else>
			<label>
				<span>加密方法</span>
				<select v-model="proxyMethod" class="settings-input" :disabled="proxyFormMode === 'edit'">
					<option v-for="method in shadowsocksMethods" :key="method" :value="method">{{ method }}</option>
				</select>
			</label>
			<div class="fixed-fields"><span>网络：TCP + UDP</span></div>
		</template>
        <label class="checkbox-row"><input v-model="proxyEnabled" type="checkbox" /><span>启用代理节点</span></label>
        <fieldset v-if="proxyFormMode === 'create'" class="client-fieldset">
          <legend>首个客户端</legend>
          <label><span>客户端名称</span><n-input v-model:value="firstClientName" maxlength="100" /></label>
			<p>{{ proxyProtocol === 'vless' ? 'UUID 将由后端安全生成。' : '客户端密钥将由后端安全生成。' }}</p>
			<label v-if="proxyProtocol === 'vless'" class="checkbox-row"><input v-model="firstClientUDP443" type="checkbox" /><span>允许 UDP/443 / QUIC</span></label>
        </fieldset>
        <div class="modal-actions"><n-button @click="proxyFormOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>

  <n-modal v-if="selectedProxy" v-model:show="proxyDetailOpen">
    <n-card class="proxy-detail-card" title="代理节点详情" :bordered="false" closable @close="proxyDetailOpen = false">
      <h3>基础</h3>
      <dl class="server-details">
        <div><dt>名称</dt><dd>{{ selectedProxy.name }}</dd></div><div><dt>服务器</dt><dd>{{ selectedProxy.server_name }}</dd></div>
        <div><dt>入口模式</dt><dd>{{ selectedProxy.entry_host_mode === 'auto' ? '自动检测' : '手动输入' }}</dd></div><div><dt>入口地址</dt><dd>{{ selectedProxy.entry_address || '未检测' }}</dd></div>
        <div><dt>监听地址</dt><dd>0.0.0.0:{{ selectedProxy.listen_port }}</dd></div><div><dt>状态</dt><dd>{{ selectedProxy.enabled ? '启用' : '禁用' }}</dd></div>
      </dl>
      <h3>协议</h3>
		<dl v-if="selectedProxy.protocol === 'vless'" class="server-details"><div><dt>协议</dt><dd>VLESS</dd></div><div><dt>传输</dt><dd>TCP</dd></div><div><dt>流控</dt><dd>XTLS Vision</dd></div><div><dt>安全层</dt><dd>{{ selectedProxy.config.security === 'tls' ? 'TLS' : 'REALITY' }}</dd></div></dl>
		<dl v-else class="server-details"><div><dt>协议</dt><dd>Shadowsocks</dd></div><div><dt>加密方法</dt><dd>{{ selectedProxy.config.method }}</dd></div><div><dt>网络</dt><dd>TCP + UDP</dd></div></dl>
		<h3 v-if="selectedProxy.protocol === 'vless'">{{ selectedProxy.config.security === 'tls' ? 'TLS' : 'REALITY' }}</h3>
		<dl v-if="selectedProxy.protocol === 'vless'" class="server-details">
        <div><dt>SNI</dt><dd>{{ selectedProxy.config.server_name }}</dd></div>
        <div><dt>指纹</dt><dd>{{ selectedProxy.config.fingerprint }}</dd></div>
        <div v-if="selectedProxy.config.security === 'tls'"><dt>证书</dt><dd>{{ selectedProxy.config.tls_certificate_configured ? '已配置' : '未配置' }}</dd></div>
        <template v-else><div><dt>目标地址</dt><dd>{{ selectedProxy.config.reality_target }}</dd></div><div><dt>Public Key</dt><dd>{{ selectedProxy.config.reality_public_key }}</dd></div><div><dt>Short ID</dt><dd>{{ selectedProxy.config.reality_short_id }}</dd></div></template>
      </dl>
      <div class="section-heading"><h3>客户端</h3><n-button size="small" type="primary" @click="openCreateClient">新增客户端</n-button></div>
      <n-empty v-if="!selectedProxy.clients?.length" size="small" description="暂无客户端" />
      <div v-else class="server-table-wrap">
		<table class="server-table client-table"><thead><tr><th>名称</th><th>状态</th><th>已用流量</th><th>最近活动</th><th v-if="showsVLESSClientFields(selectedProxy.protocol)">UUID</th><th v-if="showsVLESSClientFields(selectedProxy.protocol)">UDP/443</th><th>操作</th></tr></thead>
			<tbody><tr v-for="client in selectedProxy.clients" :key="client.id"><td>{{ client.name }}</td><td>{{ client.enabled ? '启用' : '禁用' }}</td><td>{{ formatClientTrafficBytes(clientTrafficUsedBytes(client.metrics)) }}</td><td>{{ client.metrics?.last_activity_at ? formatTime(client.metrics.last_activity_at) : '—' }}</td><td v-if="showsVLESSClientFields(selectedProxy.protocol)">{{ client.uuid_summary }}</td><td v-if="showsVLESSClientFields(selectedProxy.protocol)">{{ client.client_udp443 ? '开启' : '关闭' }}</td><td class="server-actions"><n-button size="tiny" secondary @click="copyClientURI(client)">{{ copiedClientID === client.id ? '已复制' : '复制链接' }}</n-button><n-button size="tiny" secondary @click="showClient(client)">查看</n-button><n-button size="tiny" secondary @click="openEditClient(client)">编辑</n-button><n-button size="tiny" secondary @click="toggleClient(client)">{{ client.enabled ? '禁用' : '启用' }}</n-button><n-button size="tiny" type="error" secondary @click="removeClient(client)">删除</n-button></td></tr></tbody>
        </table>
      </div>
      <div class="modal-actions"><n-button secondary @click="openEditProxy(selectedProxy)">编辑节点</n-button><n-button @click="proxyDetailOpen = false">关闭</n-button></div>
    </n-card>
  </n-modal>

  <n-modal v-model:show="clientFormOpen" :mask-closable="!submitting">
    <n-card class="client-form-card" :title="clientFormMode === 'create' ? '新增客户端' : '编辑客户端'" :bordered="false" closable @close="clientFormOpen = false">
      <form class="proxy-form" @submit.prevent="saveClient">
        <label><span>名称</span><n-input v-model:value="clientName" maxlength="100" /></label>
		<p>{{ selectedProxy?.protocol === 'vless' ? 'UUID 由后端生成，创建后保持不变。' : '客户端密钥由后端生成，且不会在普通 API 中显示。' }}</p>
		<label v-if="selectedProxy?.protocol === 'vless'" class="checkbox-row"><input v-model="clientUDP443" type="checkbox" /><span>允许 UDP/443 / QUIC</span></label>
        <label class="checkbox-row"><input v-model="clientEnabled" type="checkbox" /><span>启用客户端</span></label>
        <div class="modal-actions"><n-button @click="clientFormOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>

  <n-modal v-if="selectedShare" v-model:show="clientDetailOpen">
    <n-card class="client-detail-card" title="客户端详情" :bordered="false" closable @close="clientDetailOpen = false">
      <dl class="server-details">
        <div><dt>名称</dt><dd>{{ selectedShare.client.name }}</dd></div><div><dt>状态</dt><dd>{{ selectedShare.client.enabled ? '启用' : '禁用' }}</dd></div>
		<div><dt>本周期上行</dt><dd>{{ formatClientTrafficBytes(selectedShare.client.metrics?.cycle_uplink_bytes ?? 0) }}</dd></div><div><dt>本周期下行</dt><dd>{{ formatClientTrafficBytes(selectedShare.client.metrics?.cycle_downlink_bytes ?? 0) }}</dd></div>
		<div><dt>本周期已用</dt><dd>{{ formatClientTrafficBytes(clientTrafficUsedBytes(selectedShare.client.metrics)) }}</dd></div><div><dt>最近活动</dt><dd>{{ selectedShare.client.metrics?.last_activity_at ? formatTime(selectedShare.client.metrics.last_activity_at) : '—' }}</dd></div>
		<template v-if="selectedShare.protocol === 'vless'"><div><dt>UUID</dt><dd>{{ selectedShare.client.uuid }}</dd></div><div><dt>客户端 Flow</dt><dd>{{ selectedShare.flow }}</dd></div></template>
		<div><dt>连接地址</dt><dd>{{ selectedShare.address }}</dd></div><div><dt>端口</dt><dd>{{ selectedShare.port }}</dd></div>
		<template v-if="selectedShare.protocol === 'vless'"><div><dt>安全层</dt><dd>{{ selectedShare.security === 'tls' ? 'TLS' : 'REALITY' }}</dd></div><div><dt>SNI</dt><dd>{{ selectedShare.server_name }}</dd></div>
		<template v-if="selectedShare.security === 'reality'"><div><dt>Public Key</dt><dd>{{ selectedShare.reality_public_key }}</dd></div><div><dt>Short ID</dt><dd>{{ selectedShare.reality_short_id }}</dd></div></template></template>
		<template v-else><div><dt>加密方法</dt><dd>{{ selectedShare.method }}</dd></div><div><dt>网络</dt><dd>TCP + UDP</dd></div></template>
      </dl>
		<div class="share-field"><strong>直连 {{ selectedShare.protocol === 'vless' ? 'VLESS' : 'Shadowsocks' }} URI</strong><n-input :value="selectedShare.uri" type="textarea" readonly :autosize="{ minRows: 4 }" /></div>
		<div class="modal-actions"><n-button v-if="selectedShare.protocol === 'vless'" secondary @click="copyValue('uuid', selectedShare.client.uuid)">{{ copied === 'uuid' ? 'UUID 已复制' : '复制 UUID' }}</n-button><n-button type="primary" @click="copyValue('uri', selectedShare.uri)">{{ copied === 'uri' ? '链接已复制' : `复制 ${selectedShare.protocol === 'vless' ? 'VLESS' : 'Shadowsocks'} 链接` }}</n-button></div>
    </n-card>
  </n-modal>
</template>
