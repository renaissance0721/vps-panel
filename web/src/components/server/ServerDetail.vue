<script setup lang="ts">
import {
  toRefs,
} from 'vue'
import {
  NModal,
  NCard,
  NButton,
  NAlert,
  NInput,
  NEmpty,
} from 'naive-ui'
import type {
  ServersViewState,
} from '../../composables/useServers'
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
    | 'regenerateEnrollment'
    | 'permanentlyDeleteServer'
    | 'createdServer'
    | 'copyAgentCommand'
    | 'copiedCommand'
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
  regenerateEnrollment,
  permanentlyDeleteServer,
  createdServer,
  copyAgentCommand,
  copiedCommand,
} = toRefs(props.model)
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
            <div class="server-detail-grid">
              <section class="server-detail-section">
                <h3 class="system-info-title">基本信息</h3>
            <dl class="server-details">
              <div><dt>名称</dt><dd class="expiration-display"><span>{{ selectedServer.name }}</span><n-button v-if="!selectedServer.archived_at" class="expiration-edit-button" size="tiny" text title="修改名称" aria-label="修改名称" :disabled="submitting" @click="openNameModal">✎</n-button></dd></div>
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
                    :disabled="submitting"
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
                    :disabled="submitting"
                    @click="openExpirationModal"
                  >
                    ✎
                  </n-button>
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
              <n-button
                v-if="state?.user?.role === 'admin' && selectedServer.agent_version_status === 'upgrade_available'"
                size="small"
                type="primary"
                secondary
                :loading="submitting || selectedServer.agent_upgrade_status === 'upgrading'"
                :disabled="!panelReleaseVersion || selectedServer.status !== 'online' || selectedServer.agent_upgrade_status === 'upgrading'"
                @click="upgradeAgent(selectedServer)"
              >
                {{ panelReleaseVersion ? `升级 Agent 到 ${panelReleaseVersion}` : '开发版本不可升级' }}
              </n-button>
            </div>
            <dl class="server-details">
              <div><dt>Agent 版本</dt><dd>{{ selectedServer.agent_version || '—' }}</dd></div>
              <div><dt>Panel 版本</dt><dd>{{ health?.version || 'dev' }}</dd></div>
              <div><dt>升级状态</dt><dd>{{ agentUpgradeStatus(selectedServer) }}</dd></div>
              <div v-if="selectedServer.agent_upgrade_status === 'failed'"><dt>失败原因</dt><dd>{{ selectedServer.agent_upgrade_error || '升级失败' }}</dd></div>
            </dl>
            <n-alert v-if="selectedServer.agent_version_status === 'agent_newer'" type="warning" class="form-alert">
              当前 Agent {{ selectedServer.agent_version }} 高于 Panel {{ health?.version || 'dev' }}，请先升级 Panel；不支持自动降级 Agent。
            </n-alert>
            <div v-if="state?.user?.role === 'admin' && bootstrapUpgradeCommand && selectedServer.agent_version_status === 'upgrade_available'" class="secret-field">
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
                    <n-button size="small" :type="selectedServer.outbound_preference === 'auto' ? 'primary' : 'default'" :secondary="selectedServer.outbound_preference === 'auto'" :disabled="!!selectedServer.archived_at || submitting" @click="setOutboundPreference(selectedServer, 'auto')">系统默认</n-button>
                    <n-button size="small" :type="selectedServer.outbound_preference === 'prefer_ipv4' ? 'primary' : 'default'" :secondary="selectedServer.outbound_preference === 'prefer_ipv4'" :disabled="!!selectedServer.archived_at || submitting" @click="setOutboundPreference(selectedServer, 'prefer_ipv4')">优先 IPv4</n-button>
                    <n-button size="small" :type="selectedServer.outbound_preference === 'prefer_ipv6' ? 'primary' : 'default'" :secondary="selectedServer.outbound_preference === 'prefer_ipv6'" :disabled="!!selectedServer.archived_at || submitting" @click="setOutboundPreference(selectedServer, 'prefer_ipv6')">优先 IPv6</n-button>
                  </span>
                  <small v-if="!selectedServer.archived_at && selectedServer.status !== 'online'" class="outbound-preference-note">设置会保存，待 Agent 下次上线自动应用。</small>
                </dd>
              </div>
            </dl>
              </section>
            </div>

            <section class="server-detail-section server-detail-section--wide"><ServerTraffic :model="model" /></section>
<div v-if="state?.user?.role === 'admin'" class="server-modal-actions">
              <n-button
                type="primary"
                secondary
                :loading="submitting"
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
</template>
