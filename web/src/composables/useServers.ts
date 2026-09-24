import {
  computed,
  getCurrentScope,
  onScopeDispose,
  ref,
  type Ref,
  type UnwrapNestedRefs,
} from 'vue'
import {
  api,
  APIError,
} from '../api/client'
import type {
  AuthState,
  AccessUser,
} from '../types/auth'
import type {
  Health,
} from '../types/overview'
import type {
  ServerRecord,
  CreatedServer,
  DiagnosticReport,
} from '../types/server'
import {
  formatTime,
} from '../format'
import { moveRow, persistMove } from '../reorder'
import { adminFirst } from '../adminFirst'
import {
  formatPercent,
  formatBytes,
  formatTrafficBytes,
  trafficUsageLabel,
  trafficCountModeLabel,
  measuredTrafficUsed,
  trafficAdjustmentLabel,
  trafficUsagePercentLabel,
  formatUptime,
  visibilityLabel,
  agentUpgradeStatus,
  statusType,
  statusLabel,
  formatExpirationDate,
  formatServerExpiration,
  canBulkUpgradeAgent,
} from '../server'
import {
  formatTrafficLimitInput,
  parseTrafficLimit,
  trafficWarningLevel,
  useTrafficForm,
  type TrafficLimitUnit,
} from '../traffic'

const bulkAgentUpgradeConcurrency = 3
const bulkAgentUpgradePollMs = 2_000
const bulkAgentUpgradeTimeoutMs = 180_000

type BulkAgentUpgradeStatus =
  | 'waiting'
  | 'starting'
  | 'upgrading'
  | 'success'
  | 'failed'
  | 'timeout'

type BulkAgentUpgradeItem = {
  serverId: number
  serverName: string
  fromVersion: string
  targetVersion: string
  status: BulkAgentUpgradeStatus
  error: string
  startedAt?: number
}

type AgentUpgradeResponse = {
  status: 'upgrading' | 'already_current'
  version: string
}

function isBulkAgentUpgradeActive(item: BulkAgentUpgradeItem): boolean {
  return item.status === 'starting' || item.status === 'upgrading'
}

export function useServers(state: Ref<AuthState | null>, users: Ref<AccessUser[]>, health: Ref<Health | null>, submitting: Ref<boolean>, error: Ref<string>, submit: (action: () => Promise<void>) => Promise<void>) {
  const servers = ref<ServerRecord[]>([])
  const archivedServers = ref<ServerRecord[]>([])
  const selectedServer = ref<ServerRecord | null>(null)
  const createdServer = ref<CreatedServer | null>(null)
  const serverModalOpen = ref(false)
  const expirationModalOpen = ref(false)
  const trafficAdjustmentModalOpen = ref(false)
  const diagnosticOpen = ref(false)
  const diagnosticLoading = ref(false)
  const diagnosticReport = ref<DiagnosticReport | null>(null)
  const diagnosticError = ref('')
  const accessModalOpen = ref(false)
  const serverName = ref('')
  const serverVisibility = ref<ServerRecord['visibility']>('public')
  const serverAccessUserIDs = ref<number[]>([])
  const accessVisibility = ref<ServerRecord['visibility']>('public')
  const accessUserIDs = ref<number[]>([])
  const accessFormError = ref('')
  const orderedUsers = computed(() => adminFirst(users.value))
  const serverListMode = ref<'active' | 'archived'>('active')
  const serverReorderingID = ref<number | null>(null)
  const copiedCommand = ref(false)
  const copiedUpgradeCommand = ref(false)
  const expirationInput = ref('')
  const nameModalOpen = ref(false)
  const nameInput = ref('')
  const nameFormError = ref('')
  const trafficAdjustmentInput = ref<string | number>('')
  const trafficAdjustmentUnit = ref<TrafficLimitUnit>('G')
  const bulkUpgradeModalOpen = ref(false)
  const bulkUpgradePhase = ref<'confirm' | 'running' | 'done'>('confirm')
  const bulkUpgradeItems = ref<BulkAgentUpgradeItem[]>([])
  const bulkUpgradeTargetVersion = ref('')
  const bulkUpgradeStarting = ref(false)
  const bulkUpgradeError = ref('')
  let diagnosticRequest = 0
  let bulkUpgradeRunID = 0
  let bulkUpgradeTimer: ReturnType<typeof setTimeout> | undefined

  let serverLoadPromise: Promise<void> | null = null

  const {
    trafficModalOpen,
    trafficFormError,
    trafficLimitInput,
    trafficLimitUnit,
    trafficCountMode,
    trafficResetDay,
    trafficResetTime,
    openTrafficModal,
    closeTrafficModal,
    resetTrafficForm,
    saveTrafficConfig,
  } = useTrafficForm(
    selectedServer,
    submitting,
    async (serverID, payload) => {
      const response = await api<{ server: ServerRecord }>(`/api/servers/${serverID}`, {
        method: 'PATCH',
        body: JSON.stringify(payload),
      })
      return response.server
    },
    loadServers,
    (message) => {
      error.value = message
    },
  )

  const panelReleaseVersion = computed(() => {
    const value = health.value?.version ?? ''
    return /^v\d+\.\d+\.\d+$/.test(value) ? value : ''
  })

  const bootstrapUpgradeCommand = computed(() =>
    panelReleaseVersion.value
      ? `curl -fsSL ${window.location.origin}/upgrade-agent.sh | sh`
      : '',
  )

  const bulkUpgradeCandidates = computed(() => servers.value.filter(canBulkUpgradeAgent))
  const bulkUpgradeSkippedSummary = computed(() => {
    const counts = new Map<string, number>()
    for (const server of servers.value) {
      if (canBulkUpgradeAgent(server)) continue
      let label = '其他不可升级'
      if (server.agent_upgrade_status === 'upgrading') label = '正在升级'
      else if (server.agent_version_status === 'up_to_date') label = '已是最新版'
      else if (server.agent_version_status === 'unregistered') label = '尚未注册'
      else if (server.status !== 'online') label = '离线'
      else if (!server.agent_can_self_upgrade) label = '不支持官方自动升级'
      counts.set(label, (counts.get(label) ?? 0) + 1)
    }
    return [...counts].map(([label, count]) => ({ label, count }))
  })
  const bulkUpgradeProgress = computed(() => {
    const result = {
      total: bulkUpgradeItems.value.length,
      successCount: 0,
      failedCount: 0,
      timeoutCount: 0,
      runningCount: 0,
      waitingCount: 0,
      handledCount: 0,
    }
    for (const item of bulkUpgradeItems.value) {
      if (item.status === 'success') result.successCount++
      else if (item.status === 'failed') result.failedCount++
      else if (item.status === 'timeout') result.timeoutCount++
      else if (item.status === 'waiting') result.waitingCount++
      else result.runningCount++
    }
    result.handledCount = result.successCount + result.failedCount + result.timeoutCount
    return result
  })
  const bulkUpgradeRunning = computed(() => bulkUpgradePhase.value === 'running')
  const bulkUpgradeHasBatch = computed(() => bulkUpgradeItems.value.length > 0)

  async function loadServers() {
    if (serverLoadPromise) return serverLoadPromise
    serverLoadPromise = (async () => {
      const [activeResponse, archivedResponse] = await Promise.all([
        api<{ servers: ServerRecord[] }>('/api/servers'),
        api<{ servers: ServerRecord[] }>('/api/servers?archived=true'),
      ])
      if (!state.value?.authenticated) return
      servers.value = activeResponse.servers
      archivedServers.value = archivedResponse.servers
      if (selectedServer.value) {
        const selected = [...servers.value, ...archivedServers.value].find(
          (value) => value.id === selectedServer.value?.id,
        )
        if (selected) {
          selectedServer.value = selected
        } else {
          selectedServer.value = null
          createdServer.value = null
          serverModalOpen.value = false
          accessModalOpen.value = false
          nameModalOpen.value = false
          expirationModalOpen.value = false
          trafficModalOpen.value = false
          trafficAdjustmentModalOpen.value = false
          error.value = '服务器不存在或当前账号无权访问'
        }
      }
    })()
    try {
      await serverLoadPromise
    } finally {
      serverLoadPromise = null
    }
  }

  async function createServerRecord() {
    if (serverName.value.trim() === '') {
      error.value = '请输入服务器名称'
      return
    }
    await submit(async () => {
      createdServer.value = await api<CreatedServer>('/api/servers', {
        method: 'POST',
        body: JSON.stringify({
          name: serverName.value,
          visibility: serverVisibility.value,
          user_ids: serverVisibility.value === 'private' ? withCurrentUser(serverAccessUserIDs.value) : [],
        }),
      })
      selectedServer.value = createdServer.value.server
      serverModalOpen.value = true
      serverName.value = ''
      serverVisibility.value = 'public'
      serverAccessUserIDs.value = []
      copiedCommand.value = false
      expirationModalOpen.value = false
      trafficModalOpen.value = false
      trafficAdjustmentModalOpen.value = false
      await loadServers()
    })
  }

  function viewServer(value: ServerRecord) {
    diagnosticRequest++
    selectedServer.value = value
    createdServer.value = null
    copiedCommand.value = false
    copiedUpgradeCommand.value = false
    expirationModalOpen.value = false
    nameModalOpen.value = false
    trafficModalOpen.value = false
    trafficAdjustmentModalOpen.value = false
    diagnosticOpen.value = false
    diagnosticLoading.value = false
    diagnosticReport.value = null
    diagnosticError.value = ''
    expirationInput.value = ''
    serverModalOpen.value = true
  }

  function withCurrentUser(userIDs: number[]): number[] {
    const result = new Set(userIDs)
    if (state.value?.user?.id) result.add(state.value.user.id)
    return [...result]
  }

  function ensureCreateCurrentUser() {
    if (serverVisibility.value === 'private') {
      serverAccessUserIDs.value = withCurrentUser(serverAccessUserIDs.value)
    }
  }

  function accessUserNames(userIDs: number[]): string {
    const names = users.value.filter((user) => userIDs.includes(user.id)).map((user) => user.username)
    return names.length > 0 ? names.join('、') : '—'
  }

  function openAccessModal() {
    if (!selectedServer.value) return
    accessVisibility.value = selectedServer.value.visibility
    accessUserIDs.value = withCurrentUser(selectedServer.value.access_user_ids)
    accessFormError.value = ''
    accessModalOpen.value = true
  }

  function closeAccessModal() {
    accessModalOpen.value = false
    accessFormError.value = ''
  }

  function ensureAccessCurrentUser() {
    if (accessVisibility.value === 'private') {
      accessUserIDs.value = withCurrentUser(accessUserIDs.value)
    }
  }

  async function saveServerAccess() {
    if (!selectedServer.value || submitting.value) return
    accessFormError.value = ''
    submitting.value = true
    try {
      const response = await api<{ access: { visibility: ServerRecord['visibility']; user_ids: number[] } }>(
        `/api/servers/${selectedServer.value.id}/access`,
        {
          method: 'PATCH',
          body: JSON.stringify({
            visibility: accessVisibility.value,
            user_ids: accessVisibility.value === 'private' ? withCurrentUser(accessUserIDs.value) : [],
          }),
        },
      )
      selectedServer.value.visibility = response.access.visibility
      selectedServer.value.access_user_ids = response.access.user_ids
      accessModalOpen.value = false
      await loadServers()
    } catch (reason) {
      accessFormError.value = reason instanceof Error ? reason.message : '访问范围保存失败'
      if (reason instanceof APIError && reason.status === 404) {
        accessModalOpen.value = false
        serverModalOpen.value = false
        selectedServer.value = null
        createdServer.value = null
        await loadServers().catch(() => undefined)
      }
    } finally {
      submitting.value = false
    }
  }

  async function upgradeAgent(value: ServerRecord) {
    if (!value.agent_can_self_upgrade || !panelReleaseVersion.value || value.status !== 'online' || value.agent_version_status !== 'upgrade_available') return
    if (!window.confirm(`确定将 Agent 升级到 ${panelReleaseVersion.value} 吗？升级会短暂断开连接，但不会重新注册。`)) return
    await submit(async () => {
      await api(`/api/servers/${value.id}/agent-upgrade`, { method: 'POST' })
      await loadServers()
    })
  }

  function openBulkAgentUpgrade() {
    if (state.value?.user?.role !== 'admin') return
    if (!bulkUpgradeHasBatch.value) {
      bulkUpgradePhase.value = 'confirm'
      bulkUpgradeTargetVersion.value = panelReleaseVersion.value
      bulkUpgradeError.value = ''
    }
    bulkUpgradeModalOpen.value = true
  }

  async function startBulkAgentUpgrade() {
    if (
      state.value?.user?.role !== 'admin'
      || bulkUpgradeStarting.value
      || bulkUpgradeRunning.value
      || !panelReleaseVersion.value
    ) return
    bulkUpgradeStarting.value = true
    bulkUpgradeError.value = ''
    try {
      await loadServers()
      const candidates = servers.value.filter(canBulkUpgradeAgent)
      if (candidates.length === 0) {
        bulkUpgradeError.value = '当前没有可升级 Agent'
        return
      }
      const targetVersion = panelReleaseVersion.value
      if (!targetVersion) {
        bulkUpgradeError.value = '开发版本不可批量升级'
        return
      }
      bulkUpgradeRunID++
      const runID = bulkUpgradeRunID
      bulkUpgradeTargetVersion.value = targetVersion
      bulkUpgradeItems.value = candidates.map((server) => ({
        serverId: server.id,
        serverName: server.name,
        fromVersion: server.agent_version,
        targetVersion,
        status: 'waiting',
        error: '',
      }))
      bulkUpgradePhase.value = 'running'
      fillBulkUpgradeSlots(runID)
      scheduleBulkUpgradePoll(runID)
    } catch (reason) {
      bulkUpgradeError.value = reason instanceof Error ? reason.message : '无法刷新服务器状态'
    } finally {
      bulkUpgradeStarting.value = false
    }
  }

  function fillBulkUpgradeSlots(runID: number) {
    if (runID !== bulkUpgradeRunID || !bulkUpgradeRunning.value) return
    const active = bulkUpgradeItems.value.filter(isBulkAgentUpgradeActive).length
    const available = bulkAgentUpgradeConcurrency - active
    if (available <= 0) return
    for (const item of bulkUpgradeItems.value.filter((value) => value.status === 'waiting').slice(0, available)) {
      void startBulkUpgradeItem(item, runID)
    }
  }

  async function startBulkUpgradeItem(item: BulkAgentUpgradeItem, runID: number) {
    item.status = 'starting'
    item.startedAt = Date.now()
    try {
      const response = await api<AgentUpgradeResponse>(`/api/servers/${item.serverId}/agent-upgrade`, {
        method: 'POST',
      })
      if (runID !== bulkUpgradeRunID || !bulkUpgradeRunning.value || !isBulkAgentUpgradeActive(item)) return
      if (response.status === 'already_current') {
        item.status = 'success'
        item.error = ''
      } else if (response.status === 'upgrading') {
        item.status = 'upgrading'
      } else {
        item.status = 'failed'
        item.error = 'Agent 升级响应无效'
      }
    } catch (reason) {
      if (runID !== bulkUpgradeRunID || !bulkUpgradeRunning.value || !isBulkAgentUpgradeActive(item)) return
      item.status = 'failed'
      item.error = reason instanceof Error ? reason.message : '无法启动 Agent 升级'
    }
    advanceBulkAgentUpgrade(runID)
  }

  function reconcileBulkAgentUpgrade(now = Date.now()) {
    if (!bulkUpgradeRunning.value) return
    for (const item of bulkUpgradeItems.value) {
      if (item.status !== 'starting' && item.status !== 'upgrading') continue
      const server = servers.value.find((value) => value.id === item.serverId)
      if (!server) {
        item.status = 'failed'
        item.error = '服务器已不存在或当前账号无权访问'
        continue
      }
      if (item.status === 'upgrading' && server.agent_upgrade_status === 'failed') {
        item.status = 'failed'
        item.error = server.agent_upgrade_error || 'Agent 升级失败'
        continue
      }
      if (
        server.agent_version === item.targetVersion
        && server.agent_upgrade_status !== 'upgrading'
      ) {
        item.status = 'success'
        item.error = ''
        continue
      }
      if (item.status === 'starting' && server.agent_upgrade_status === 'upgrading') {
        item.status = 'upgrading'
      }
      if (item.startedAt !== undefined && now - item.startedAt >= bulkAgentUpgradeTimeoutMs) {
        item.status = 'timeout'
        item.error = '等待 Agent 升级完成超时'
      }
    }
    advanceBulkAgentUpgrade(bulkUpgradeRunID)
  }

  function advanceBulkAgentUpgrade(runID: number) {
    if (runID !== bulkUpgradeRunID || !bulkUpgradeRunning.value) return
    if (bulkUpgradeProgress.value.handledCount === bulkUpgradeProgress.value.total) {
      bulkUpgradePhase.value = 'done'
      stopBulkUpgradePolling()
      return
    }
    fillBulkUpgradeSlots(runID)
  }

  function scheduleBulkUpgradePoll(runID: number) {
    if (bulkUpgradeTimer !== undefined || runID !== bulkUpgradeRunID || !bulkUpgradeRunning.value) return
    bulkUpgradeTimer = setTimeout(async () => {
      bulkUpgradeTimer = undefined
      if (runID !== bulkUpgradeRunID || !bulkUpgradeRunning.value) return
      try {
        await loadServers()
      } catch {
        // A temporary refresh failure is not an Agent upgrade failure.
      }
      if (runID !== bulkUpgradeRunID || !bulkUpgradeRunning.value) return
      reconcileBulkAgentUpgrade()
      scheduleBulkUpgradePoll(runID)
    }, bulkAgentUpgradePollMs)
  }

  function stopBulkUpgradePolling() {
    if (bulkUpgradeTimer !== undefined) {
      clearTimeout(bulkUpgradeTimer)
      bulkUpgradeTimer = undefined
    }
  }

  function closeBulkAgentUpgrade() {
    bulkUpgradeModalOpen.value = false
    if (!bulkUpgradeRunning.value) resetBulkAgentUpgrade()
  }

  function resetBulkAgentUpgrade() {
    bulkUpgradeRunID++
    stopBulkUpgradePolling()
    bulkUpgradeModalOpen.value = false
    bulkUpgradeItems.value = []
    bulkUpgradePhase.value = 'confirm'
    bulkUpgradeTargetVersion.value = ''
    bulkUpgradeStarting.value = false
    bulkUpgradeError.value = ''
  }

  async function copyUpgradeCommand() {
    try {
      await navigator.clipboard.writeText(bootstrapUpgradeCommand.value)
      copiedUpgradeCommand.value = true
    } catch {
      error.value = '无法自动复制，请手动复制升级命令'
    }
  }

  async function archiveServer(value: ServerRecord) {
    if (
      !window.confirm(
        `确定移除服务器“${value.name}”吗？\n\n移除后将从服务器列表隐藏，并立即撤销当前 Agent 凭据，但服务器资料和历史数据会保留。之后可以重新生成 Agent 安装令牌恢复。`,
      )
    )
      return
    await submit(async () => {
      await api(`/api/servers/${value.id}`, { method: 'DELETE' })
      if (selectedServer.value?.id === value.id) selectedServer.value = null
      if (createdServer.value?.server.id === value.id) createdServer.value = null
      await loadServers()
    })
  }

  async function regenerateEnrollment(value: ServerRecord) {
    if (
      !window.confirm(
        '重新生成后，当前 Agent 凭据将立即失效；如果服务器在线，现有 Agent 连接也会断开。服务器资料和历史数据不会删除。是否继续？',
      )
    )
      return
    await submit(async () => {
      createdServer.value = await api<CreatedServer>(`/api/servers/${value.id}/enrollment`, {
        method: 'POST',
      })
      selectedServer.value = createdServer.value.server
      copiedCommand.value = false
      expirationModalOpen.value = false
      trafficModalOpen.value = false
      trafficAdjustmentModalOpen.value = false
      await loadServers()
    })
  }

  function closeServerDetails() {
    diagnosticRequest++
    selectedServer.value = null
    createdServer.value = null
    copiedCommand.value = false
    expirationModalOpen.value = false
    expirationInput.value = ''
    trafficModalOpen.value = false
    resetTrafficForm()
    trafficAdjustmentModalOpen.value = false
    diagnosticOpen.value = false
    diagnosticLoading.value = false
    diagnosticReport.value = null
    diagnosticError.value = ''
    accessModalOpen.value = false
    accessFormError.value = ''
    nameModalOpen.value = false
    nameInput.value = ''
    nameFormError.value = ''
    resetTrafficAdjustmentForm()
  }

  function openNameModal() {
    if (!selectedServer.value || selectedServer.value.archived_at) return
    nameInput.value = selectedServer.value.name
    nameFormError.value = ''
    nameModalOpen.value = true
  }

  function closeNameModal() {
    nameModalOpen.value = false
    nameInput.value = ''
    nameFormError.value = ''
  }

  async function saveServerName() {
    if (!selectedServer.value || submitting.value) return
    const name = nameInput.value.trim()
    if (!name || [...name].length > 100) {
      nameFormError.value = '服务器名称不能为空且不能超过 100 个字符'
      return
    }
    const id = selectedServer.value.id
    nameFormError.value = ''
    await submit(async () => {
      const response = await api<{ server: ServerRecord }>(`/api/servers/${id}`, {
        method: 'PATCH',
        body: JSON.stringify({ name }),
      })
      selectedServer.value = response.server
      closeNameModal()
      await loadServers()
    })
    if (nameModalOpen.value && error.value) nameFormError.value = error.value
  }

  async function setOutboundPreference(server: ServerRecord, preference: ServerRecord['outbound_preference']) {
    if (submitting.value || server.archived_at || server.outbound_preference === preference) return
    if (!window.confirm('切换出站 IP 优先级会重新应用 Xray 配置，现有代理连接可能短暂中断。是否继续？')) return
    await submit(async () => {
      const response = await api<{ server: ServerRecord }>(`/api/servers/${server.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ outbound_preference: preference }),
      })
      selectedServer.value = response.server
      await loadServers()
    })
  }

  function openExpirationModal() {
    if (!selectedServer.value || selectedServer.value.archived_at) return
    expirationInput.value = selectedServer.value.expires_at
      ? formatExpirationDate(selectedServer.value.expires_at)
      : ''
    expirationModalOpen.value = true
  }

  function closeExpirationModal() {
    expirationModalOpen.value = false
    expirationInput.value = ''
  }

  async function saveExpiration() {
    const value = expirationInput.value.trim()
    if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) {
      error.value = '请选择有效的到期日期'
      return
    }
    await updateExpiration(value)
  }

  async function clearExpiration() {
    await updateExpiration(null)
  }

  async function updateExpiration(expiresAt: string | null) {
    if (!selectedServer.value) return
    const serverID = selectedServer.value.id
    await submit(async () => {
      const response = await api<{ server: ServerRecord }>(`/api/servers/${serverID}`, {
        method: 'PATCH',
        body: JSON.stringify({ expires_at: expiresAt }),
      })
      selectedServer.value = response.server
      expirationModalOpen.value = false
      expirationInput.value = ''
      await loadServers()
    })
  }

  function openTrafficAdjustmentModal() {
    if (!selectedServer.value || selectedServer.value.archived_at) return
    const target = formatTrafficLimitInput(selectedServer.value.traffic_used_bytes)
    trafficAdjustmentInput.value = target.value || '0'
    trafficAdjustmentUnit.value = target.unit
    trafficAdjustmentModalOpen.value = true
  }

  function closeTrafficAdjustmentModal() {
    trafficAdjustmentModalOpen.value = false
    resetTrafficAdjustmentForm()
  }

  function resetTrafficAdjustmentForm() {
    trafficAdjustmentInput.value = ''
    trafficAdjustmentUnit.value = 'G'
  }

  async function saveTrafficAdjustment() {
    if (!selectedServer.value) return
    if (String(trafficAdjustmentInput.value).trim() === '') {
      error.value = '请输入目标已用流量'
      return
    }
    const parsed = parseTrafficLimit(trafficAdjustmentInput.value, trafficAdjustmentUnit.value)
    if (parsed === undefined) {
      error.value = '目标已用流量格式无效，请输入不小于 0 的数值'
      return
    }
    const serverID = selectedServer.value.id
    await submit(async () => {
      const response = await api<{ server: ServerRecord }>(
        `/api/servers/${serverID}/traffic-adjustment`,
        {
          method: 'PATCH',
          body: JSON.stringify({ target_used_bytes: parsed ?? 0 }),
        },
      )
      selectedServer.value = response.server
      closeTrafficAdjustmentModal()
      await loadServers()
    })
  }

  async function clearTrafficAdjustment() {
    if (!selectedServer.value) return
    const serverID = selectedServer.value.id
    await submit(async () => {
      const response = await api<{ server: ServerRecord }>(
        `/api/servers/${serverID}/traffic-adjustment`,
        { method: 'DELETE' },
      )
      selectedServer.value = response.server
      closeTrafficAdjustmentModal()
      await loadServers()
    })
  }

  async function permanentlyDeleteServer(value: ServerRecord) {
    if (
      !window.confirm(
        `确定彻底删除服务器“${value.name}”吗？\n\n这将永久删除该服务器及全部历史数据，无法通过重新安装 Agent 恢复。`,
      )
    )
      return
    await submit(async () => {
      await api(`/api/servers/${value.id}/permanent`, { method: 'DELETE' })
      serverModalOpen.value = false
      await loadServers()
    })
  }

  async function openDiagnostics(value: ServerRecord) {
    diagnosticOpen.value = true
    diagnosticReport.value = null
    diagnosticError.value = ''
    await runDiagnostics(value)
  }

  async function runDiagnostics(value?: ServerRecord) {
    const server = value ?? selectedServer.value
    if (!server || diagnosticLoading.value) return
    diagnosticLoading.value = true
    diagnosticError.value = ''
    const request = ++diagnosticRequest
    try {
      const report = await api<DiagnosticReport>(`/api/servers/${server.id}/diagnostics`, {
        method: 'POST',
      })
      if (request === diagnosticRequest && selectedServer.value?.id === server.id)
        diagnosticReport.value = report
    } catch (reason) {
      if (request !== diagnosticRequest) return
      diagnosticError.value = reason instanceof Error ? reason.message : '服务器诊断失败'
      if (reason instanceof APIError && reason.status === 404) {
        diagnosticOpen.value = false
        await handleMissingServer()
      }
    } finally {
      if (request === diagnosticRequest) diagnosticLoading.value = false
    }
  }

  async function copyAgentCommand(value: string) {
    try {
      await navigator.clipboard.writeText(value)
      copiedCommand.value = true
    } catch {
      error.value = '无法自动复制，请手动复制内容'
    }
  }

  async function reorderServer(value: ServerRecord, targetID: number) {
    if (serverReorderingID.value !== null) return
    const rows = value.archived_at ? archivedServers.value : servers.value
    const move = moveRow(rows, value.id, targetID)
    if (!move) return
    serverReorderingID.value = value.id
    error.value = ''
    try {
      await persistMove(move, (direction) =>
        api(`/api/servers/${value.id}/reorder`, {
          method: 'POST',
          body: JSON.stringify({ direction }),
        }),
        loadServers,
      )
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : '调整服务器顺序失败'
    } finally {
      serverReorderingID.value = null
    }
  }

  function resetSession() {
    diagnosticRequest++
    resetBulkAgentUpgrade()
    servers.value = []
    archivedServers.value = []
    selectedServer.value = null
    createdServer.value = null
    serverModalOpen.value = false
    expirationModalOpen.value = false
    nameModalOpen.value = false
    nameInput.value = ''
    nameFormError.value = ''
    trafficAdjustmentModalOpen.value = false
    accessModalOpen.value = false
    diagnosticOpen.value = false
    diagnosticLoading.value = false
    diagnosticReport.value = null
    diagnosticError.value = ''
    expirationInput.value = ''
  }

  if (getCurrentScope()) onScopeDispose(resetBulkAgentUpgrade)

  async function handleMissingServer() {
    if (!serverModalOpen.value) return
    serverModalOpen.value = false
    nameModalOpen.value = false
    selectedServer.value = null
    createdServer.value = null
    await loadServers().catch(() => undefined)
  }

  return {
    servers,
    archivedServers,
    selectedServer,
    createdServer,
    serverModalOpen,
    expirationModalOpen,
    nameModalOpen,
    nameInput,
    nameFormError,
    trafficAdjustmentModalOpen,
    diagnosticOpen,
    diagnosticLoading,
    diagnosticReport,
    diagnosticError,
    accessModalOpen,
    serverName,
    serverVisibility,
    serverAccessUserIDs,
    accessVisibility,
    accessUserIDs,
    accessFormError,
    serverListMode,
    serverReorderingID,
    copiedCommand,
    copiedUpgradeCommand,
    expirationInput,
    trafficAdjustmentInput,
    trafficAdjustmentUnit,
    bulkUpgradeModalOpen,
    bulkUpgradePhase,
    bulkUpgradeItems,
    bulkUpgradeTargetVersion,
    bulkUpgradeStarting,
    bulkUpgradeError,
    bulkUpgradeCandidates,
    bulkUpgradeSkippedSummary,
    bulkUpgradeProgress,
    bulkUpgradeRunning,
    bulkUpgradeHasBatch,
    trafficModalOpen,
    trafficFormError,
    trafficLimitInput,
    trafficLimitUnit,
    trafficCountMode,
    trafficResetDay,
    trafficResetTime,
    openTrafficModal,
    closeTrafficModal,
    resetTrafficForm,
    saveTrafficConfig,
    panelReleaseVersion,
    bootstrapUpgradeCommand,
    loadServers,
    createServerRecord,
    viewServer,
    withCurrentUser,
    ensureCreateCurrentUser,
    accessUserNames,
    openAccessModal,
    closeAccessModal,
    ensureAccessCurrentUser,
    saveServerAccess,
    upgradeAgent,
    openBulkAgentUpgrade,
    startBulkAgentUpgrade,
    reconcileBulkAgentUpgrade,
    closeBulkAgentUpgrade,
    resetBulkAgentUpgrade,
    copyUpgradeCommand,
    archiveServer,
    regenerateEnrollment,
    closeServerDetails,
    openNameModal,
    closeNameModal,
    saveServerName,
    setOutboundPreference,
    openExpirationModal,
    closeExpirationModal,
    saveExpiration,
    clearExpiration,
    updateExpiration,
    openTrafficAdjustmentModal,
    closeTrafficAdjustmentModal,
    resetTrafficAdjustmentForm,
    saveTrafficAdjustment,
    clearTrafficAdjustment,
    permanentlyDeleteServer,
    openDiagnostics,
    runDiagnostics,
    copyAgentCommand,
    reorderServer,
    state,
    users,
    orderedUsers,
    health,
    submitting,
    formatTime,
    formatPercent,
    formatBytes,
    formatTrafficBytes,
    trafficUsageLabel,
    trafficCountModeLabel,
    measuredTrafficUsed,
    trafficAdjustmentLabel,
    trafficUsagePercentLabel,
    formatUptime,
    visibilityLabel,
    agentUpgradeStatus,
    statusType,
    statusLabel,
    formatExpirationDate,
    formatServerExpiration,
    trafficWarningLevel,
    resetSession,
    handleMissingServer,
  }
}

export type ServersViewState = UnwrapNestedRefs<ReturnType<typeof useServers>>
