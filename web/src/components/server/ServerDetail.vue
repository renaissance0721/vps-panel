<script setup lang="ts">
import {
  computed,
  toRefs,
} from 'vue'
import {
  NModal,
  NCard,
  NButton,
  NAlert,
  NInput,
  NEmpty,
  NDrawer,
  NDrawerContent,
  NSpin,
  NSwitch,
  NTag,
} from 'naive-ui'
import type {
  ServersViewState,
} from '../../composables/useServers'
import type {
  DiagnosticCheck,
  DiagnosticStatus,
} from '../../types/server'
import {
  agentAPILabel,
  agentCapabilities,
  chinaInboundApplyState,
  chinaInboundSupported,
  chinaInboundUnsupportedReason,
  agentImplementationLabel,
  agentSupportsCapability,
} from '../../server'
import ServerTraffic from './ServerTraffic.vue'
type TrafficModel = InstanceType<typeof ServerTraffic>['$props']['model']
const props = defineProps<{
  model: TrafficModel & Pick<ServersViewState,
    | 'selectedServer'
    | 'serverModalOpen'
    | 'closeServerDetails'
    | 'statusLabel'
    | 'visibilityLabel'
    | 'accessUserNames'
    | 'submitting'
    | 'openAccessModal'
    | 'openNameModal'
    | 'formatExpirationDate'
    | 'openExpirationModal'
    | 'renewalPeriodLabel'
    | 'setAutoRenew'
    | 'formatTime'
    | 'state'
    | 'panelReleaseVersion'
    | 'upgradeAgent'
    | 'health'
    | 'agentUpgradeStatus'
    | 'bootstrapUpgradeCommand'
    | 'copyUpgradeCommand'
    | 'copiedUpgradeCommand'
    | 'formatPercent'
    | 'formatBytes'
    | 'formatUptime'
    | 'setOutboundPreference'
    | 'setBlockChinaInbound'
    | 'archiveServer'
    | 'forceRemoveServer'
    | 'regenerateEnrollment'
    | 'permanentlyDeleteServer'
    | 'createdServer'
    | 'copyAgentCommand'
    | 'copiedCommand'
    | 'diagnosticOpen'
    | 'diagnosticLoading'
    | 'diagnosticReport'
    | 'diagnosticError'
    | 'openDiagnostics'
    | 'runDiagnostics'
  >
}>()
const {
  selectedServer,
  serverModalOpen,
  closeServerDetails,
  statusLabel,
  visibilityLabel,
  accessUserNames,
  submitting,
  openAccessModal,
  openNameModal,
  formatExpirationDate,
  openExpirationModal,
  renewalPeriodLabel,
  setAutoRenew,
  formatTime,
  state,
  panelReleaseVersion,
  upgradeAgent,
  health,
  agentUpgradeStatus,
  bootstrapUpgradeCommand,
  copyUpgradeCommand,
  copiedUpgradeCommand,
  formatPercent,
  formatBytes,
  formatUptime,
  setOutboundPreference,
  setBlockChinaInbound,
  archiveServer,
  forceRemoveServer,
  regenerateEnrollment,
  permanentlyDeleteServer,
  createdServer,
  copyAgentCommand,
  copiedCommand,
  diagnosticOpen,
  diagnosticLoading,
  diagnosticReport,
  diagnosticError,
  openDiagnostics,
  runDiagnostics,
} = toRefs(props.model)

const diagnosticsSupported = computed(() => selectedServer.value !== null && agentSupportsCapability(selectedServer.value, agentCapabilities.diagnosticsV1))
const outboundPreferenceSupported = computed(() => selectedServer.value !== null && agentSupportsCapability(selectedServer.value, agentCapabilities.outboundPreference))
const serverReadOnly = computed(() => selectedServer.value === null || !!selectedServer.value.archived_at || !!selectedServer.value.decommission_status)
const chinaInboundState = computed(() => selectedServer.value ? chinaInboundApplyState(selectedServer.value) : null)
const chinaInboundSwitchDisabled = computed(() => {
  const server = selectedServer.value
  return server === null || !!server.archived_at || !!server.decommission_status || submitting.value ||
    (!server.block_china_inbound && !chinaInboundSupported(server))
})
const chinaInboundSwitchTitle = computed(() => {
  const server = selectedServer.value
  if (!server) return ''
  if (server.archived_at) return '已移除服务器不能修改设置。'
  if (server.decommission_status) return '服务器正在退役，不能继续修改配置。'
  if (!server.block_china_inbound && !chinaInboundSupported(server)) return chinaInboundUnsupportedReason(server)
  return '仅限制中国大陆 IP 访问 VPS Panel 管理的 Proxy 和 Relay 入站端口。'
})

const diagnosticGroupDefinitions = [
  { title: 'Agent', prefixes: ['agent.'] },
  { title: '配置同步', prefixes: ['config.'] },
  { title: 'Xray', prefixes: ['xray.'] },
  { title: 'Realm', prefixes: ['realm.'] },
  { title: '中转目标', prefixes: ['relay.'] },
  { title: 'TLS', prefixes: ['tls.'] },
  { title: 'Panel → 入口', prefixes: ['panel.'] },
  { title: '协议端到端', prefixes: ['protocol.'] },
  { title: '其他', prefixes: ['diagnostic.'] },
]

const diagnosticGroups = computed(() => diagnosticGroupDefinitions
  .map((group) => ({
    title: group.title,
    checks: diagnosticReport.value?.checks.filter((check) =>
      group.prefixes.some((prefix) => check.code.startsWith(prefix)),
    ) ?? [],
  }))
  .filter((group) => group.checks.length > 0))

function diagnosticStatusIcon(status: DiagnosticStatus) {
  if (status === 'pass') return '✓'
  if (status === 'warning') return '!'
  if (status === 'fail') return '✕'
  return '—'
}

function diagnosticStatusType(status: DiagnosticStatus): 'success' | 'warning' | 'error' | 'default' {
  if (status === 'pass') return 'success'
  if (status === 'warning') return 'warning'
  if (status === 'fail') return 'error'
  return 'default'
}

function diagnosticCheckTitle(check: DiagnosticCheck) {
  if (check.label) return check.label
  const labels: Record<string, string> = {
    'agent.connected': 'Agent 在线',
    'config.version': '配置版本',
    'config.sync': '最近一次配置同步',
    'xray.service': 'Xray service',
    'xray.config': 'Xray 当前配置',
    'realm.service': 'Realm service',
    'protocol.end_to_end': 'VLESS / Shadowsocks 协议握手',
    'diagnostic.truncated': '诊断结果限制',
  }
  return labels[check.code] ?? check.code
}

function diagnosticCheckMeta(check: DiagnosticCheck) {
  const values = []
  if (check.endpoint) values.push(check.endpoint)
  if (check.protocol) values.push(check.protocol.toUpperCase())
  if (check.latency_ms !== undefined) values.push(`${check.latency_ms} ms`)
  return values.join(' · ')
}
</script>

<template>
<n-modal
          v-if="selectedServer"
          v-model:show="serverModalOpen"
          :mask-closable="true"
          @after-leave="closeServerDetails"
        >
          <n-card
            class="server-modal-card"
            title="服务器详情"
            :bordered="false"
            closable
            @close="serverModalOpen = false"
          >
            <n-alert v-if="selectedServer.decommission_status === 'pending'" type="warning" class="form-alert" title="正在退役">
              {{ selectedServer.status === 'offline' ? '等待 Agent 上线清理。' : 'Agent 正在清理 VPS Panel 管理的资源。' }}
            </n-alert>
            <n-alert v-else-if="selectedServer.decommission_status === 'failed'" type="error" class="form-alert" title="退役失败">
              <p>{{ selectedServer.decommission_error || '退役清理失败' }}</p>
              <p>Agent 将自动重试清理。</p>
            </n-alert>
            <div class="server-detail-grid">
              <section class="server-detail-section">
                <h3 class="system-info-title">基本信息</h3>
            <dl class="server-details">
              <div><dt>名称</dt><dd class="expiration-display"><span>{{ selectedServer.name }}</span><n-button v-if="!selectedServer.archived_at" class="expiration-edit-button" size="tiny" text title="修改名称" aria-label="修改名称" :disabled="submitting || !!selectedServer.decommission_status" @click="openNameModal">✎</n-button></dd></div>
              <div><dt>状态</dt><dd>{{ statusLabel(selectedServer.status) }}</dd></div>
              <div>
                <dt>访问范围</dt>
                <dd class="expiration-display">
                  <span>
                    {{ visibilityLabel(selectedServer.visibility) }}
                    <template v-if="selectedServer.visibility === 'private'">
                      · {{ accessUserNames(selectedServer.access_user_ids) }}
                    </template>
                  </span>
                  <n-button
                    class="expiration-edit-button"
                    size="tiny"
                    text
                    title="修改访问范围"
                    aria-label="修改访问范围"
                    :disabled="submitting || !!selectedServer.decommission_status"
                    @click="openAccessModal"
                  >
                    ✎
                  </n-button>
                </dd>
              </div>
              <div>
                <dt>到期日期</dt>
                <dd class="expiration-display">
                  <span>{{ selectedServer.expires_at ? formatExpirationDate(selectedServer.expires_at) : '不限' }}</span>
                  <n-button
                    v-if="!selectedServer.archived_at"
                    class="expiration-edit-button"
                    size="tiny"
                    text
                    title="修改到期日期"
                    aria-label="修改到期日期"
                    :disabled="submitting || !!selectedServer.decommission_status"
                    @click="openExpirationModal"
                  >
                    ✎
                  </n-button>
                </dd>
              </div>
              <div><dt>续费周期</dt><dd>{{ renewalPeriodLabel(selectedServer.renewal_period_months) }}</dd></div>
              <div>
                <dt>自动续费</dt>
                <dd>
                  <n-switch
                    :value="selectedServer.auto_renew"
                    :disabled="serverReadOnly || submitting || !selectedServer.expires_at || !selectedServer.renewal_period_months"
                    :title="!selectedServer.expires_at || !selectedServer.renewal_period_months ? '请先设置到期日期和续费周期' : '仅顺延 Panel 中记录的到期日期，不会向 VPS 商家付款。'"
                    @update:value="setAutoRenew(selectedServer, $event)"
                  />
                </dd>
              </div>
              <div>
                <dt>最后通信</dt>
                <dd>{{ selectedServer.last_seen_at ? formatTime(selectedServer.last_seen_at) : '—' }}</dd>
              </div>
              <div><dt>创建时间</dt><dd>{{ formatTime(selectedServer.created_at) }}</dd></div>
              <div><dt>更新时间</dt><dd>{{ formatTime(selectedServer.updated_at) }}</dd></div>
              <div v-if="selectedServer.archived_at">
                <dt>移除时间</dt><dd>{{ formatTime(selectedServer.archived_at) }}</dd>
              </div>
            </dl>
              </section>

              <section class="server-detail-section">
            <div class="section-heading">
              <h3 class="system-info-title">Agent</h3>
              <div class="section-heading-actions">
              <n-button
                size="small"
                secondary
                :loading="diagnosticLoading"
                :disabled="!!selectedServer.archived_at || !diagnosticsSupported"
                :title="diagnosticsSupported ? undefined : '当前 Agent 不支持一键诊断'"
                @click="openDiagnostics(selectedServer)"
              >
                一键诊断
              </n-button>
              <small v-if="!diagnosticsSupported" class="secondary-text">当前 Agent 不支持一键诊断</small>
              <n-button
                v-if="state?.user?.role === 'admin' && selectedServer.agent_can_self_upgrade && selectedServer.agent_version_status === 'upgrade_available'"
                size="small"
                type="primary"
                secondary
                :loading="submitting || selectedServer.agent_upgrade_status === 'upgrading'"
                :disabled="!panelReleaseVersion || selectedServer.status !== 'online' || selectedServer.agent_upgrade_status === 'upgrading' || !!selectedServer.decommission_status"
                @click="upgradeAgent(selectedServer)"
              >
                {{ panelReleaseVersion ? `升级 Agent 到 ${panelReleaseVersion}` : '开发版本不可升级' }}
              </n-button>
              </div>
            </div>
            <dl class="server-details">
              <div><dt>Agent 类型</dt><dd>{{ agentImplementationLabel(selectedServer.agent_implementation) }}</dd></div>
              <div><dt>Agent 版本</dt><dd>{{ selectedServer.agent_version || '—' }}</dd></div>
              <div><dt>Agent API</dt><dd>{{ agentAPILabel(selectedServer.agent_api_version) }}</dd></div>
              <div>
                <dt>能力</dt>
                <dd class="agent-capabilities">
                  <span v-if="(selectedServer.agent_capabilities?.length ?? 0) === 0">—</span>
                  <n-tag v-for="capability in selectedServer.agent_capabilities ?? []" :key="capability" size="small">
                    {{ capability }}
                  </n-tag>
                </dd>
              </div>
              <div><dt>Panel 版本</dt><dd>{{ health?.version || 'dev' }}</dd></div>
              <div><dt>升级状态</dt><dd>{{ agentUpgradeStatus(selectedServer) }}</dd></div>
              <div v-if="selectedServer.agent_upgrade_status === 'failed'"><dt>失败原因</dt><dd>{{ selectedServer.agent_upgrade_error || '升级失败' }}</dd></div>
            </dl>
            <n-alert v-if="selectedServer.agent_version_status === 'agent_newer'" type="warning" class="form-alert">
              当前 Agent {{ selectedServer.agent_version }} 高于 Panel {{ health?.version || 'dev' }}，请先升级 Panel；不支持自动降级 Agent。
            </n-alert>
            <div v-if="state?.user?.role === 'admin' && bootstrapUpgradeCommand && selectedServer.agent_can_self_upgrade && selectedServer.agent_version_status === 'upgrade_available'" class="secret-field">
              <strong>旧版 Agent 引导升级命令（无需 Token）</strong>
              <n-input :value="bootstrapUpgradeCommand" readonly />
              <n-button size="small" secondary @click="copyUpgradeCommand">{{ copiedUpgradeCommand ? '已复制' : '复制命令' }}</n-button>
            </div>
              </section>

              <section class="server-detail-section">
            <h3 class="system-info-title">系统信息</h3>
            <n-empty
              v-if="!selectedServer.system_info"
              size="small"
              description="暂无系统信息"
            />
            <dl v-else class="server-details">
              <div><dt>主机名</dt><dd>{{ selectedServer.system_info.hostname || '—' }}</dd></div>
              <div><dt>系统</dt><dd>{{ selectedServer.system_info.os_name || '—' }}</dd></div>
              <div><dt>系统版本</dt><dd>{{ selectedServer.system_info.os_version || '—' }}</dd></div>
              <div><dt>内核</dt><dd>{{ selectedServer.system_info.kernel || '—' }}</dd></div>
              <div><dt>架构</dt><dd>{{ selectedServer.system_info.arch || '—' }}</dd></div>
              <div>
                <dt>IPv4</dt>
                <dd class="address-list">
                  <span v-if="selectedServer.system_info.ipv4.length === 0">—</span>
                  <span v-for="address in selectedServer.system_info.ipv4" :key="address">{{ address }}</span>
                </dd>
              </div>
              <div>
                <dt>IPv6</dt>
                <dd class="address-list">
                  <span v-if="selectedServer.system_info.ipv6.length === 0">—</span>
                  <span v-for="address in selectedServer.system_info.ipv6" :key="address">{{ address }}</span>
                </dd>
              </div>
              <div><dt>公网 IPv4</dt><dd>{{ selectedServer.system_info.public_ipv4 || '未检测' }}</dd></div>
            </dl>
              </section>

              <section class="server-detail-section">
            <h3 class="system-info-title">动态指标</h3>
            <n-empty
              v-if="!selectedServer.metrics"
              size="small"
              description="暂无动态指标"
            />
            <dl class="server-details">
              <div v-if="selectedServer.metrics"><dt>CPU</dt><dd>{{ formatPercent(selectedServer.metrics.cpu_percent) }}</dd></div>
              <div v-if="selectedServer.metrics">
                <dt>内存</dt>
                <dd>
                  {{ formatBytes(selectedServer.metrics.memory_used_bytes) }} /
                  {{ formatBytes(selectedServer.metrics.memory_total_bytes) }}
                </dd>
              </div>
              <div v-if="selectedServer.metrics">
                <dt>根分区磁盘</dt>
                <dd>
                  {{ formatBytes(selectedServer.metrics.disk_used_bytes) }} /
                  {{ formatBytes(selectedServer.metrics.disk_total_bytes) }}
                </dd>
              </div>
              <div v-if="selectedServer.metrics"><dt>运行时间</dt><dd>{{ formatUptime(selectedServer.metrics.uptime_seconds) }}</dd></div>
              <div>
                <dt>当前出站</dt>
                <dd>
                  <span class="outbound-preference-buttons">
                    <n-button size="small" :type="selectedServer.outbound_preference === 'auto' ? 'primary' : 'default'" :secondary="selectedServer.outbound_preference === 'auto'" :disabled="serverReadOnly || submitting" @click="setOutboundPreference(selectedServer, 'auto')">系统默认</n-button>
                    <n-button size="small" :type="selectedServer.outbound_preference === 'prefer_ipv4' ? 'primary' : 'default'" :secondary="selectedServer.outbound_preference === 'prefer_ipv4'" :disabled="serverReadOnly || submitting || !outboundPreferenceSupported" @click="setOutboundPreference(selectedServer, 'prefer_ipv4')">优先 IPv4</n-button>
                    <n-button size="small" :type="selectedServer.outbound_preference === 'prefer_ipv6' ? 'primary' : 'default'" :secondary="selectedServer.outbound_preference === 'prefer_ipv6'" :disabled="serverReadOnly || submitting || !outboundPreferenceSupported" @click="setOutboundPreference(selectedServer, 'prefer_ipv6')">优先 IPv6</n-button>
                  </span>
                  <small v-if="!outboundPreferenceSupported" class="outbound-preference-note">当前 Agent 不支持出站 IPv4 / IPv6 偏好。</small>
                  <small v-else-if="!selectedServer.archived_at && selectedServer.status !== 'online'" class="outbound-preference-note">设置会保存，待 Agent 下次上线自动应用。</small>
                </dd>
              </div>
            </dl>
              </section>
            </div>

            <section class="server-detail-section server-detail-section--wide">
              <div class="section-heading">
                <h3 class="system-info-title">中国 IP 入站限制</h3>
                <n-switch
                  :value="selectedServer.block_china_inbound"
                  :disabled="chinaInboundSwitchDisabled"
                  :title="chinaInboundSwitchTitle"
                  @update:value="setBlockChinaInbound(selectedServer, $event)"
                />
              </div>
              <dl class="server-details">
                <div>
                  <dt>状态</dt>
                  <dd>
                    <n-tag :type="chinaInboundState?.type ?? 'default'" size="small">
                      {{ chinaInboundState?.label }}
                    </n-tag>
                    <small
                      v-if="chinaInboundState && chinaInboundState.key !== 'failed' && chinaInboundState.key !== 'unsupported_enabled'"
                      class="outbound-preference-note"
                    >
                      {{ chinaInboundState.detail }}
                    </small>
                  </dd>
                </div>
              </dl>
              <n-alert v-if="chinaInboundState?.key === 'unsupported_enabled'" type="warning" class="form-alert">
                {{ chinaInboundState.detail }}
              </n-alert>
              <n-alert v-else-if="chinaInboundState?.key === 'failed'" type="error" class="form-alert">
                <strong>配置应用失败</strong>
                <p>{{ chinaInboundState.detail }}</p>
                <p v-if="selectedServer.agent_config_sync_error">{{ selectedServer.agent_config_sync_error }}</p>
              </n-alert>
              <small class="secondary-text">
                仅限制中国大陆 IP 访问 VPS Panel 管理的 Proxy 和 Relay 入站端口。支持 IPv4 和 IPv6。数据来源：APNIC。SSH 和其他服务不受影响。
              </small>
            </section>

            <section class="server-detail-section server-detail-section--wide"><ServerTraffic :model="model" /></section>
<div v-if="state?.user?.role === 'admin'" class="server-modal-actions">
              <n-button
                v-if="!selectedServer.archived_at"
                type="error"
                secondary
                :disabled="submitting || !!selectedServer.decommission_status"
                @click="archiveServer(selectedServer)"
              >
                {{ selectedServer.decommission_status ? '正在退役' : '开始退役' }}
              </n-button>
              <n-button
                v-if="!selectedServer.archived_at"
                type="error"
                text
                :disabled="submitting"
                @click="forceRemoveServer(selectedServer)"
              >
                强制从 Panel 移除
              </n-button>
              <n-button
                v-if="!selectedServer.archived_at"
                type="primary"
                secondary
                :loading="submitting"
                :disabled="!!selectedServer.decommission_status"
                @click="regenerateEnrollment(selectedServer)"
              >
                重新生成 Agent 安装令牌
              </n-button>
              <n-button
                v-if="selectedServer.archived_at"
                type="error"
                secondary
                :disabled="submitting"
                @click="permanentlyDeleteServer(selectedServer)"
              >
                彻底删除
              </n-button>
            </div>

            <div v-if="createdServer" class="modal-enrollment">
              <n-alert
                type="warning"
                title="Agent 安装命令仅显示一次，请立即保存。"
              >
                请在目标 Debian/Ubuntu VPS 上以 root 用户执行下方安装命令。
              </n-alert>
              <dl class="server-details enrollment-summary">
                <div>
                  <dt>过期时间</dt>
                  <dd>{{ formatTime(createdServer.enrollment_token_expires_at) }}</dd>
                </div>
              </dl>
              <div class="secret-field">
                <strong>Agent 安装命令</strong>
                <n-input
                  :value="createdServer.agent_installation_command"
                  type="textarea"
                  readonly
                  :autosize="{ minRows: 3 }"
                />
                <n-button
                  secondary
                  @click="copyAgentCommand(createdServer.agent_installation_command)"
                >
                  {{ copiedCommand ? '已复制' : '复制命令' }}
                </n-button>
              </div>
            </div>
          </n-card>
        </n-modal>

        <n-drawer v-if="selectedServer" v-model:show="diagnosticOpen" width="min(720px, 100vw)" placement="right">
          <n-drawer-content title="服务器一键诊断" closable>
            <n-alert type="info" class="diagnostic-notice">
              TCP 检查只表示指定网络位置可以建立连接，不代表 VLESS / Shadowsocks 协议端到端可用；UDP 仅检查节点本地 listener，不判断远端可达性。
            </n-alert>
            <div v-if="diagnosticLoading && !diagnosticReport" class="loading-row">
              <n-spin size="small" /><span>正在诊断...</span>
            </div>
            <n-alert v-if="diagnosticError" type="error" class="diagnostic-notice">
              {{ diagnosticError }}
            </n-alert>
            <n-empty
              v-if="!diagnosticLoading && !diagnosticReport && !diagnosticError"
              description="尚未诊断"
            />
            <template v-if="diagnosticReport">
              <p class="diagnostic-summary">
                完成于 {{ formatTime(diagnosticReport.started_at) }} · 用时 {{ diagnosticReport.duration_ms }} ms
              </p>
              <section v-for="group in diagnosticGroups" :key="group.title" class="diagnostic-group">
                <h3>{{ group.title }}</h3>
                <div v-for="(check, index) in group.checks" :key="`${check.code}-${check.resource_id ?? 0}-${index}`" class="diagnostic-check">
                  <n-tag :type="diagnosticStatusType(check.status)" size="small" round>
                    {{ diagnosticStatusIcon(check.status) }}
                  </n-tag>
                  <div>
                    <strong>{{ diagnosticCheckTitle(check) }}</strong>
                    <span v-if="diagnosticCheckMeta(check)" class="diagnostic-meta">{{ diagnosticCheckMeta(check) }}</span>
                    <p v-if="check.detail">{{ check.detail }}</p>
                  </div>
                </div>
              </section>
            </template>
            <template #footer>
              <n-button type="primary" :loading="diagnosticLoading" @click="runDiagnostics()">
                {{ diagnosticReport ? '重新诊断' : '开始诊断' }}
              </n-button>
            </template>
          </n-drawer-content>
        </n-drawer>
</template>
