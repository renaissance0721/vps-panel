import {
  computed,
  ref,
} from 'vue'
import {
  type ProxyProtocol,
  type ProxyListenFamily,
  type ShadowsocksMethod,
  manualEntryHostMatchesFamily,
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
  serverHasUsableIPv6,
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
	const proxyListenFamily = ref<ProxyListenFamily>('ipv4')
  const proxyEntryHostMode = ref<'bound' | 'auto' | 'manual'>('auto')
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
	const selectedServerPublicIPv6 = computed(() => selectedServer.value?.system_info?.public_ipv6 ?? '')
	const selectedServerPublicAddress = computed(() => proxyListenFamily.value === 'ipv6'
	  ? selectedServerPublicIPv6.value
	  : selectedServerPublicIPv4.value)
  const selectedServer = computed(() => props.servers.find((server) => server.id === proxyServerID.value) ?? null)
	const selectedServerBoundDomain = computed(() => proxyListenFamily.value === 'ipv6'
	  ? selectedServer.value?.bound_domain_ipv6 ?? ''
	  : selectedServer.value?.bound_domain_ipv4 ?? '')
	const selectedServerHasUsableIPv6 = computed(() => serverHasUsableIPv6(selectedServer.value))
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
    proxyFormMode.value = 'create'
    resetProxyForm()
    proxyServerID.value = props.servers[0]?.id ?? null
    syncCreateProxyEntryHost()
    proxyFormOpen.value = true
  }

  function syncCreateProxyEntryHost() {
    if (proxyFormMode.value !== 'create') return
    proxyEntryHostMode.value = selectedServerBoundDomain.value ? 'bound' : 'auto'
    proxyEntryHost.value = ''
  }

  function onProxyServerChange() {
	  if (proxyListenFamily.value === 'ipv6' && !selectedServerHasUsableIPv6.value) {
	    proxyListenFamily.value = 'ipv4'
	    error.value = '当前服务器未检测到可用公网 IPv6，已切换为 IPv4'
	  }
    syncCreateProxyEntryHost()
  }

	function onProxyFamilyChange() {
	  if (proxyListenFamily.value === 'ipv6' && !selectedServerHasUsableIPv6.value) {
	    proxyListenFamily.value = 'ipv4'
	    error.value = '当前服务器未检测到可用公网 IPv6，已切换为 IPv4'
	  }
	  if (proxyEntryHostMode.value === 'bound' && !selectedServerBoundDomain.value) {
	    proxyEntryHostMode.value = 'auto'
	  }
	}

  function openEditProxy(value: ProxyRecord) {
    proxyFormMode.value = 'edit'
    editingProxyID.value = value.id
    proxyName.value = value.name
    proxyServerID.value = value.server_id
    proxyPort.value = value.listen_port
	proxyListenFamily.value = value.listen_family
    proxyEntryHostMode.value = value.entry_host_mode === 'manual' && selectedServerBoundDomain.value && value.entry_host === selectedServerBoundDomain.value
      ? 'bound'
      : value.entry_host_mode
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
	proxyListenFamily.value = 'ipv4'
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
	if (proxyListenFamily.value === 'ipv6' && !selectedServerHasUsableIPv6.value) {
	  error.value = '当前服务器未检测到可用公网 IPv6'
	  return
	}
    if (proxyEntryHostMode.value === 'bound' && !selectedServerBoundDomain.value) {
      error.value = '当前服务器未设置绑定域名'
      return
    }
    if (proxyEntryHostMode.value === 'manual' && !proxyEntryHost.value.trim()) {
      error.value = '请填写手动入口地址'
      return
    }
	if (proxyEntryHostMode.value === 'manual' && !manualEntryHostMatchesFamily(proxyEntryHost.value, proxyListenFamily.value)) {
	  error.value = `手动入口 IP 必须与 ${proxyListenFamily.value === 'ipv6' ? 'IPv6' : 'IPv4'} 监听类型一致`
	  return
	}
    await run(async () => {
      const entryHostMode = proxyEntryHostMode.value === 'auto' ? 'auto' : 'manual'
      const entryHost = proxyEntryHostMode.value === 'bound'
        ? selectedServerBoundDomain.value
        : proxyEntryHostMode.value === 'manual'
          ? proxyEntryHost.value
          : ''
      const common = {
        name: proxyName.value,
		listen_family: proxyListenFamily.value,
        listen_port: proxyPort.value,
        entry_host_mode: entryHostMode,
        entry_host: entryHost,
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
	proxyListenFamily,
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
	selectedServerPublicIPv6,
	selectedServerPublicAddress,
    selectedServerBoundDomain,
	selectedServerHasUsableIPv6,
    proxyVLESSSupported,
    proxyVLESSRealitySupported,
    proxyTLSSupported,
    proxyTLSACMESupported,
    proxyTLSManualSupported,
    proxyShadowsocksSupported,
    proxyCapabilityWarning,
    openCreateProxy,
    onProxyServerChange,
	onProxyFamilyChange,
    openEditProxy,
    resetProxyForm,
    saveProxy,
  }
}
