import type { NodeRole } from '../node'
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
  agentDeclaresCapability,
  agentSupportsCapability,
  serverHasAutoPublicIPv6,
  serverIPv6State,
  serverIPv6StatusMessage,
  serverSupportsIPv6Listener,
} from '../server'
import type {
  Ref,
} from 'vue'

export function useProxyForm(props: { servers: ServerOption[] }, selectedProxy: Ref<ProxyRecord | null>, error: Ref<string>, run: (action: () => Promise<void>) => Promise<void>, loadProxies: () => Promise<void>, showProxy: (id: number) => Promise<void>) {
  const proxyFormOpen = ref(false)
  const proxyFormMode = ref<'create' | 'edit'>('create')
  const editingProxyID = ref<number | null>(null)
  const proxyName = ref('')
  const proxyNodeRole = ref<NodeRole>('direct')
  const proxyServerID = ref<number | null>(null)
  const proxyPort = ref(443)
	const proxyListenFamily = ref<ProxyListenFamily>('ipv4')
  const proxyEntryHostMode = ref<'bound' | 'auto' | 'manual'>('auto')
  const proxyEntryHost = ref('')
  const proxyEnabled = ref(true)
  const proxyProtocol = ref<ProxyProtocol>('vless')
  const proxyKernel = ref<'xray' | 'sing-box'>('xray')
  const proxyMethod = ref<ShadowsocksMethod>('2022-blake3-aes-128-gcm')
  const proxyUpMbps = ref(0)
  const proxyDownMbps = ref(0)
  const proxyObfsEnabled = ref(false)
  const proxyObfsPassword = ref('')
  const proxyPortHoppingEnabled = ref(false)
  const proxyPortHoppingStart = ref(20000)
  const proxyPortHoppingEnd = ref(30000)
  const originalObfsEnabled = ref(false)
  const proxySecurity = ref<'tls' | 'reality'>('reality')
  const proxyTLSMode = ref<'acme' | 'manual'>('acme')
  const proxyServerName = ref('')
  const proxyCertificate = ref('')
  const proxyPrivateKey = ref('')
  const proxyRealityTarget = ref('')
  const firstClientName = ref('默认客户端')
  const firstClientUDP443 = ref(false)
  const originalRuntimeFields = ref('')

  const selectedServer = computed(() => props.servers.find((server) => server.id === proxyServerID.value) ?? null)
  const selectedServerPublicIPv4 = computed(() => selectedServer.value?.system_info?.public_ipv4 ?? '')
	const selectedServerPublicIPv6 = computed(() => selectedServer.value?.system_info?.public_ipv6 ?? '')
	const selectedServerPublicAddress = computed(() => proxyListenFamily.value === 'ipv6'
	  ? selectedServerPublicIPv6.value
	  : selectedServerPublicIPv4.value)
	const selectedServerBoundDomain = computed(() => proxyListenFamily.value === 'ipv6'
	  ? selectedServer.value?.bound_domain_ipv6 ?? ''
	  : selectedServer.value?.bound_domain_ipv4 ?? '')
	const selectedServerSupportsIPv6Listener = computed(() => serverSupportsIPv6Listener(selectedServer.value))
	const selectedServerHasAutoPublicIPv6 = computed(() => serverHasAutoPublicIPv6(selectedServer.value))
	const selectedServerIPv6State = computed(() => serverIPv6State(selectedServer.value))
	const selectedServerIPv6Status = computed(() => serverIPv6StatusMessage(selectedServer.value))
  const proxyVLESSRealitySupported = computed(() => selectedServer.value === null || agentSupportsCapability(selectedServer.value, agentCapabilities.proxyVLESSReality))
  const proxyHysteria2Supported = computed(() => selectedServer.value !== null && agentDeclaresCapability(selectedServer.value, agentCapabilities.proxyHysteria2))
  const proxyPortHoppingSupported = computed(() => selectedServer.value !== null &&
    agentDeclaresCapability(selectedServer.value, agentCapabilities.proxyHysteria2PortHopping) &&
    agentDeclaresCapability(selectedServer.value, proxyListenFamily.value === 'ipv6' ? agentCapabilities.proxyHysteria2HoppingIPv6 : agentCapabilities.proxyHysteria2HoppingIPv4))
  const proxyTLSACMESupported = computed(() => proxyProtocol.value === 'hysteria2' ? proxyHysteria2Supported.value : selectedServer.value === null || agentSupportsCapability(selectedServer.value, agentCapabilities.proxyVLESSTLSACME))
  const proxyTLSManualSupported = computed(() => proxyProtocol.value === 'hysteria2' ? proxyHysteria2Supported.value : selectedServer.value === null || agentSupportsCapability(selectedServer.value, agentCapabilities.proxyVLESSTLSManual))
  const proxyVLESSSupported = computed(() => proxyVLESSRealitySupported.value || proxyTLSACMESupported.value || proxyTLSManualSupported.value)
  const proxyTLSSupported = computed(() => proxyTLSACMESupported.value || proxyTLSManualSupported.value)
  const proxyShadowsocksSupported = computed(() => selectedServer.value === null || agentSupportsCapability(selectedServer.value, agentCapabilities.proxyShadowsocks))
  const runtimeFields = computed(() => JSON.stringify([
    proxyName.value, proxyServerID.value, proxyPort.value, proxyListenFamily.value,
    proxyEntryHostMode.value, proxyEntryHost.value, selectedServerBoundDomain.value, proxyEnabled.value,
    proxyProtocol.value, proxyMethod.value, proxySecurity.value, proxyTLSMode.value,
    proxyServerName.value, proxyCertificate.value, proxyPrivateKey.value, proxyRealityTarget.value,
    proxyUpMbps.value, proxyDownMbps.value, proxyObfsEnabled.value, proxyObfsPassword.value,
    proxyPortHoppingEnabled.value, proxyPortHoppingStart.value, proxyPortHoppingEnd.value,
  ]))
  const proxyRoleOnlyUpdate = computed(() => proxyFormMode.value === 'edit' && runtimeFields.value === originalRuntimeFields.value)
  const proxyCapabilityWarning = computed(() => {
    if (proxyProtocol.value === 'hysteria2') {
      if (!proxyHysteria2Supported.value) return '当前 Agent 不支持 Hysteria2，请先升级 Agent'
      if (proxyPortHoppingEnabled.value && !proxyPortHoppingSupported.value) return '当前 Agent 不支持端口跳跃，请先升级 Agent'
      return ''
    }
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
	proxyEntryHostMode.value = selectedServerBoundDomain.value
	  ? 'bound'
	  : proxyListenFamily.value === 'ipv6' && !selectedServerHasAutoPublicIPv6.value
	    ? 'manual'
	    : 'auto'
    proxyEntryHost.value = ''
  }

  function onProxyServerChange() {
	  if (proxyListenFamily.value === 'ipv6' && !selectedServerSupportsIPv6Listener.value) {
	    proxyListenFamily.value = 'ipv4'
	    error.value = '当前服务器未检测到 IPv6 地址，已切换为 IPv4'
	  }
    syncCreateProxyEntryHost()
  }

	function onProxyFamilyChange() {
	  if (proxyListenFamily.value === 'ipv6' && !selectedServerSupportsIPv6Listener.value) {
	    proxyListenFamily.value = 'ipv4'
	    error.value = '当前服务器未检测到 IPv6 地址，已切换为 IPv4'
	  }
	  if (proxyFormMode.value === 'create') {
	    syncCreateProxyEntryHost()
	    return
	  }
	  if (proxyEntryHostMode.value === 'bound' && !selectedServerBoundDomain.value) {
	    proxyEntryHostMode.value = proxyListenFamily.value === 'ipv6' && !selectedServerHasAutoPublicIPv6.value ? 'manual' : 'auto'
	  } else if (proxyEntryHostMode.value === 'auto' && proxyListenFamily.value === 'ipv6' && !selectedServerHasAutoPublicIPv6.value) {
	    proxyEntryHostMode.value = 'manual'
	  }
	}

  function openEditProxy(value: ProxyRecord) {
    proxyFormMode.value = 'edit'
    editingProxyID.value = value.id
    proxyName.value = value.name
    proxyNodeRole.value = value.node_role
    proxyServerID.value = value.server_id
    proxyPort.value = value.listen_port
	proxyListenFamily.value = value.listen_family
    proxyEntryHostMode.value = value.entry_host_mode === 'manual' && selectedServerBoundDomain.value && value.entry_host === selectedServerBoundDomain.value
      ? 'bound'
      : value.entry_host_mode
    proxyEntryHost.value = value.entry_host
    proxyEnabled.value = value.enabled
    proxyProtocol.value = value.protocol
    proxyKernel.value = value.kernel
    proxyMethod.value = value.config.method ?? '2022-blake3-aes-128-gcm'
    proxyUpMbps.value = value.config.up_mbps ?? 0
    proxyDownMbps.value = value.config.down_mbps ?? 0
    proxyObfsEnabled.value = value.config.obfs_enabled ?? false
    proxyPortHoppingEnabled.value = value.config.port_hopping_enabled ?? false
    proxyPortHoppingStart.value = value.config.port_hopping_start ?? 20000
    proxyPortHoppingEnd.value = value.config.port_hopping_end ?? 30000
    originalObfsEnabled.value = proxyObfsEnabled.value
    proxyObfsPassword.value = ''
    proxySecurity.value = value.config.security ?? 'reality'
    proxyTLSMode.value = value.config.tls_mode ?? (value.config.tls_certificate_configured ? 'manual' : 'acme')
    proxyServerName.value = value.config.server_name ?? ''
    proxyCertificate.value = ''
    proxyPrivateKey.value = ''
    proxyRealityTarget.value = value.config.reality_target ?? ''
    originalRuntimeFields.value = runtimeFields.value
    proxyFormOpen.value = true
  }

  function resetProxyForm() {
    editingProxyID.value = null
    originalRuntimeFields.value = ''
    proxyName.value = ''
    proxyNodeRole.value = 'direct'
    proxyPort.value = 443
	proxyListenFamily.value = 'ipv4'
    proxyEntryHostMode.value = 'auto'
    proxyEntryHost.value = ''
    proxyEnabled.value = true
    proxyProtocol.value = 'vless'
    proxyKernel.value = 'xray'
    proxyMethod.value = '2022-blake3-aes-128-gcm'
    proxyUpMbps.value = 0
    proxyDownMbps.value = 0
    proxyObfsEnabled.value = false
    proxyPortHoppingEnabled.value = false
    proxyPortHoppingStart.value = 20000
    proxyPortHoppingEnd.value = 30000
    originalObfsEnabled.value = false
    proxyObfsPassword.value = ''
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
	if (!proxyRoleOnlyUpdate.value && proxyEnabled.value && proxyCapabilityWarning.value) {
	  error.value = proxyCapabilityWarning.value
	  return
	}
    if (!proxyName.value.trim() || (proxyProtocol.value !== 'shadowsocks' && !proxyServerName.value.trim())) {
      error.value = proxyProtocol.value !== 'shadowsocks' ? '请填写节点名称和 SNI' : '请填写节点名称'
      return
    }
    if (proxyProtocol.value === 'hysteria2' && (!Number.isInteger(proxyUpMbps.value) || proxyUpMbps.value < 0 || !Number.isInteger(proxyDownMbps.value) || proxyDownMbps.value < 0 || (proxyObfsEnabled.value && !proxyObfsPassword.value && (proxyFormMode.value === 'create' || !originalObfsEnabled.value)))) {
      error.value = '带宽必须是非负整数，启用混淆时请填写密码'
      return
    }
    if (!Number.isInteger(proxyPort.value) || proxyPort.value < 1 || proxyPort.value > 65535) {
      error.value = '监听端口必须在 1–65535 之间'
      return
    }
    if (proxyProtocol.value === 'hysteria2' && proxyPortHoppingEnabled.value &&
        (!Number.isInteger(proxyPortHoppingStart.value) || !Number.isInteger(proxyPortHoppingEnd.value) ||
         proxyPortHoppingStart.value < 1 || proxyPortHoppingEnd.value > 65535 ||
         proxyPortHoppingStart.value > proxyPortHoppingEnd.value ||
         (proxyPort.value >= proxyPortHoppingStart.value && proxyPort.value <= proxyPortHoppingEnd.value))) {
      error.value = '跳跃范围必须在 1–65535 之间，起始端口不大于结束端口，且不能包含基础监听端口'
      return
    }
    if (proxyFormMode.value === 'create' && proxyServerID.value === null) {
      error.value = '请选择服务器'
      return
    }
	if (!proxyRoleOnlyUpdate.value && proxyListenFamily.value === 'ipv6' && !selectedServerSupportsIPv6Listener.value) {
	  error.value = '当前服务器未检测到 IPv6 地址'
	  return
	}
	if (!proxyRoleOnlyUpdate.value && proxyListenFamily.value === 'ipv6' && proxyEntryHostMode.value === 'auto' && !selectedServerHasAutoPublicIPv6.value) {
	  error.value = '无法自动检测公网 IPv6，请使用绑定域名或手动填写'
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
        node_role: proxyNodeRole.value,
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
        : proxyProtocol.value === 'hysteria2' ? {
          kernel: 'sing-box', security: 'tls', server_name: proxyServerName.value, tls_mode: proxyTLSMode.value,
          ...(proxyTLSMode.value === 'manual' ? { certificate: proxyCertificate.value, private_key: proxyPrivateKey.value } : {}),
          up_mbps: proxyUpMbps.value, down_mbps: proxyDownMbps.value,
          port_hopping_enabled: proxyPortHoppingEnabled.value,
          port_hopping_start: proxyPortHoppingEnabled.value ? proxyPortHoppingStart.value : 0,
          port_hopping_end: proxyPortHoppingEnabled.value ? proxyPortHoppingEnd.value : 0,
          ...(!proxyObfsEnabled.value ? { obfs_password: '' } : proxyObfsPassword.value ? { obfs_password: proxyObfsPassword.value } : {}),
        } : {}
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
          body: JSON.stringify(proxyRoleOnlyUpdate.value ? { node_role: proxyNodeRole.value } : { ...common, ...protocolConfig }),
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
    proxyNodeRole,
    proxyRoleOnlyUpdate,
    proxyServerID,
    proxyPort,
	proxyListenFamily,
    proxyEntryHostMode,
    proxyEntryHost,
    proxyEnabled,
    proxyProtocol,
    proxyKernel,
    proxyMethod,
    proxyUpMbps,
    proxyDownMbps,
    proxyObfsEnabled,
    proxyObfsPassword,
    proxyPortHoppingEnabled,
    proxyPortHoppingStart,
    proxyPortHoppingEnd,
    proxyPortHoppingSupported,
    proxyHysteria2Supported,
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
	selectedServerSupportsIPv6Listener,
	selectedServerHasAutoPublicIPv6,
	selectedServerIPv6State,
	selectedServerIPv6Status,
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
