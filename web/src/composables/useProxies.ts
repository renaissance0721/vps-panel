import {
  computed,
  onMounted,
  ref,
  watch,
} from 'vue'
import {
  clientTrafficCycleLabel,
  clientTrafficUsageLabel,
  clientTrafficUsagePercentLabel,
  clientStatusLabel,
  clientStatusTagType,
  formatClientExpiration,
  formatClientTrafficBytes,
  proxyListProtocolFields,
  shadowsocksMethods,
  showsVLESSClientFields,
} from '../proxy'
import type {
  ServerOption,
  ClientSummary,
  ClientRecord,
  ProxyRecord,
  ClientShare,
} from '../types/proxy'
import {
  api,
  APIError,
} from '../api/client'
import type {
  UnwrapNestedRefs,
} from 'vue'
import {
  formatTime,
} from '../format'
import {
  useProxyForm,
} from './useProxyForm'
import {
  useClientForm,
} from './useClientForm'
export function useProxies(props: { servers: ServerOption[] }) {
  const proxies = ref<ProxyRecord[]>([])
  const loading = ref(true)
  const submitting = ref(false)
  const reorderingID = ref<number | null>(null)
  const error = ref('')
  const search = ref('')
  const proxyDetailOpen = ref(false)
  const selectedProxy = ref<ProxyRecord | null>(null)
  const clientDetailOpen = ref(false)
  const selectedShare = ref<ClientShare | null>(null)
  const copiedShareURI = ref(false)
  const copiedClientID = ref<number | null>(null)

  const proxyForm = useProxyForm(props, selectedProxy, error, run, loadProxies, showProxy)
  const { proxyFormOpen } = proxyForm
  const clientForm = useClientForm(selectedProxy, error, run, loadProxies, refreshSelectedProxy)
  const { clientFormOpen } = clientForm

  const filteredProxies = computed(() => {
    const keyword = search.value.trim().toLowerCase()
    if (!keyword) return proxies.value
    return proxies.value.filter((value) =>
      [value.name, value.server_name, value.entry_address, value.entry_host, value.config.security ?? '', value.config.method ?? '']
        .some((field) => field.toLowerCase().includes(keyword)),
    )
  })

  async function run(action: () => Promise<void>) {
    submitting.value = true
    error.value = ''
    try {
      await action()
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : '操作失败'
      if (reason instanceof APIError && reason.status === 404) {
        proxyDetailOpen.value = false
        proxyFormOpen.value = false
        clientDetailOpen.value = false
        clientFormOpen.value = false
        selectedProxy.value = null
        selectedShare.value = null
        await loadProxies().catch(() => undefined)
      }
    } finally {
      submitting.value = false
    }
  }

  async function loadProxies() {
    const response = await api<{ proxies: ProxyRecord[] }>('/api/proxies')
    proxies.value = response.proxies
  }

  async function reorderProxy(value: ProxyRecord, direction: 'up' | 'down') {
    if (reorderingID.value !== null || search.value.trim()) return
    reorderingID.value = value.id
    await run(async () => {
      await api(`/api/proxies/${value.id}/reorder`, {
        method: 'POST',
        body: JSON.stringify({ direction }),
      })
      await loadProxies()
    })
    reorderingID.value = null
  }

  watch(
    () => props.servers.map((server) => server.id).join(','),
    async () => {
      try {
        await loadProxies()
        if (selectedProxy.value && !proxies.value.some((value) => value.id === selectedProxy.value?.id)) {
          selectedProxy.value = null
          proxyDetailOpen.value = false
          clientDetailOpen.value = false
          selectedShare.value = null
          error.value = '代理节点不存在或当前账号无权访问'
        }
      } catch (reason) {
        error.value = reason instanceof Error ? reason.message : '无法加载代理节点'
      }
    },
  )

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

  async function resetClientTraffic() {
    if (!selectedShare.value || !window.confirm('确定重置此客户端的本周期流量吗？客户端凭据、额度和重置规则不会改变。')) return
    await run(async () => {
      const response = await api<{ client: ClientRecord }>(`/api/clients/${selectedShare.value?.client.id}/traffic/reset`, {
        method: 'POST',
      })
      if (selectedShare.value) selectedShare.value.client = response.client
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
      copiedShareURI.value = false
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

  async function copyShareURI(value: string) {
    try {
      await navigator.clipboard.writeText(value)
      copiedShareURI.value = true
    } catch {
      error.value = '无法自动复制，请手动复制内容'
    }
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
  return { proxies, loading, submitting, reorderingID, error, search, proxyDetailOpen, selectedProxy, clientDetailOpen, selectedShare, copiedShareURI, copiedClientID, filteredProxies, run, loadProxies, reorderProxy, showProxy, refreshSelectedProxy, toggleProxy, removeProxy, resetClientTraffic, toggleClient, removeClient, loadShare, showClient, copyClientURI, copyShareURI, ...proxyForm, ...clientForm, servers: computed(() => props.servers), clientTrafficCycleLabel, clientTrafficUsageLabel, clientTrafficUsagePercentLabel, clientStatusLabel, clientStatusTagType, formatClientExpiration, formatClientTrafficBytes, proxyListProtocolFields, shadowsocksMethods, showsVLESSClientFields, formatTime }
}
export type ProxiesViewState = UnwrapNestedRefs<ReturnType<typeof useProxies>>
