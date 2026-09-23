import {
  computed,
  ref,
} from 'vue'
import {
  type ProxyProtocol,
  type ShadowsocksMethod,
} from '../proxy'
import type {
  ServerOption,
  ProxyRecord,
} from '../types/proxy'
import {
  api,
} from '../api/client'
import {
  agentCapabilities,
  agentSupportsCapability,
} from '../server'
import type {
  Ref,
} from 'vue'

export function useProxyForm(props: { servers: ServerOption[] }, selectedProxy: Ref<ProxyRecord | null>, error: Ref<string>, run: (action: () => Promise<void>) => Promise<void>, loadProxies: () => Promise<void>, showProxy: (id: number) => Promise<void>) {
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
  const proxyTLSMode = ref<'acme' | 'manual'>('acme')
  const proxyServerName = ref('')
  const proxyCertificate = ref('')
  const proxyPrivateKey = ref('')
  const proxyRealityTarget = ref('')
  const firstClientName = ref('默认客户端')
  const firstClientUDP443 = ref(false)

  const selectedServerPublicIPv4 = computed(() =>
    props.servers.find((server) => server.id === proxyServerID.value)?.system_info?.public_ipv4 ?? '',
  )
  const selectedServer = computed(() => props.servers.find((server) => server.id === proxyServerID.value) ?? null)
  const proxyVLESSRealitySupported = computed(() => selectedServer.value === null || agentSupportsCapability(selectedServer.value, agentCapabilities.proxyVLESSReality))
  const proxyTLSACMESupported = computed(() => selectedServer.value === null || agentSupportsCapability(selectedServer.value, agentCapabilities.proxyVLESSTLSACME))
  const proxyTLSManualSupported = computed(() => selectedServer.value === null || agentSupportsCapability(selectedServer.value, agentCapabilities.proxyVLESSTLSManual))
  const proxyVLESSSupported = computed(() => proxyVLESSRealitySupported.value || proxyTLSACMESupported.value || proxyTLSManualSupported.value)
  const proxyTLSSupported = computed(() => proxyTLSACMESupported.value || proxyTLSManualSupported.value)
  const proxyShadowsocksSupported = computed(() => selectedServer.value === null || agentSupportsCapability(selectedServer.value, agentCapabilities.proxyShadowsocks))
  const proxyCapabilityWarning = computed(() => {
    if (proxyProtocol.value === 'shadowsocks') {
      return proxyShadowsocksSupported.value ? '' : '当前 Agent 不支持 Shadowsocks'
    }
    if (proxySecurity.value === 'reality') {
      return proxyVLESSRealitySupported.value ? '' : '当前 Agent 不支持 VLESS + REALITY'
    }
    if (proxyTLSMode.value === 'manual') {
      return proxyTLSManualSupported.value ? '' : '当前 Agent 不支持 VLESS + TLS（手动证书）'
    }
    return proxyTLSACMESupported.value ? '' : '当前 Agent 不支持 VLESS + TLS（ACME）'
  })

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
    proxyTLSMode.value = value.config.tls_mode ?? (value.config.tls_certificate_configured ? 'manual' : 'acme')
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
    proxyTLSMode.value = 'acme'
    proxyServerName.value = ''
    proxyCertificate.value = ''
    proxyPrivateKey.value = ''
    proxyRealityTarget.value = ''
    firstClientName.value = '默认客户端'
    firstClientUDP443.value = false
  }

  async function saveProxy() {
	if (proxyEnabled.value && proxyCapabilityWarning.value) {
	  error.value = proxyCapabilityWarning.value
	  return
	}
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
          ...(proxySecurity.value === 'tls' ? {
            tls_mode: proxyTLSMode.value,
            ...(proxyTLSMode.value === 'manual' ? { certificate: proxyCertificate.value, private_key: proxyPrivateKey.value } : {}),
          } : {}),
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
  return {
    proxyFormOpen,
    proxyFormMode,
    editingProxyID,
    proxyName,
    proxyServerID,
    proxyPort,
    proxyEntryHostMode,
    proxyEntryHost,
    proxyEnabled,
    proxyProtocol,
    proxyMethod,
    proxySecurity,
    proxyTLSMode,
    proxyServerName,
    proxyCertificate,
    proxyPrivateKey,
    proxyRealityTarget,
    firstClientName,
    firstClientUDP443,
    selectedServerPublicIPv4,
    proxyVLESSSupported,
    proxyVLESSRealitySupported,
    proxyTLSSupported,
    proxyTLSACMESupported,
    proxyTLSManualSupported,
    proxyShadowsocksSupported,
    proxyCapabilityWarning,
    openCreateProxy,
    openEditProxy,
    resetProxyForm,
    saveProxy,
  }
}
