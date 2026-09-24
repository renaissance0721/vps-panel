<script setup lang="ts">
import {
  computed,
  toRefs,
} from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NModal,
  NProgress,
  NTag,
} from 'naive-ui'
import type {
  ServersViewState,
} from '../../composables/useServers'

const props = defineProps<{
  model: Pick<ServersViewState,
    | 'bulkUpgradeModalOpen'
    | 'bulkUpgradePhase'
    | 'bulkUpgradeItems'
    | 'bulkUpgradeTargetVersion'
    | 'bulkUpgradeStarting'
    | 'bulkUpgradeError'
    | 'bulkUpgradeCandidates'
    | 'bulkUpgradeSkippedSummary'
    | 'bulkUpgradeProgress'
    | 'startBulkAgentUpgrade'
    | 'closeBulkAgentUpgrade'
  >
}>()

const {
  bulkUpgradeModalOpen,
  bulkUpgradePhase,
  bulkUpgradeItems,
  bulkUpgradeTargetVersion,
  bulkUpgradeStarting,
  bulkUpgradeError,
  bulkUpgradeCandidates,
  bulkUpgradeSkippedSummary,
  bulkUpgradeProgress,
  startBulkAgentUpgrade,
  closeBulkAgentUpgrade,
} = toRefs(props.model)

const skippedCount = computed(() =>
  bulkUpgradeSkippedSummary.value.reduce((total, item) => total + item.count, 0),
)
const progressPercentage = computed(() =>
  bulkUpgradeProgress.value.total === 0
    ? 0
    : Math.round((bulkUpgradeProgress.value.handledCount / bulkUpgradeProgress.value.total) * 100),
)
const unsuccessfulItems = computed(() =>
  bulkUpgradeItems.value.filter((item) => item.status === 'failed' || item.status === 'timeout'),
)

function statusLabel(status: ServersViewState['bulkUpgradeItems'][number]['status']) {
  switch (status) {
    case 'waiting': return '等待中'
    case 'starting': return '正在发送升级'
    case 'upgrading': return '正在升级'
    case 'success': return '已完成'
    case 'failed': return '失败'
    case 'timeout': return '超时'
  }
}

function statusType(status: ServersViewState['bulkUpgradeItems'][number]['status']) {
  if (status === 'success') return 'success'
  if (status === 'failed' || status === 'timeout') return 'error'
  if (status === 'starting' || status === 'upgrading') return 'info'
  return 'default'
}
</script>

<template>
  <n-modal
    :show="bulkUpgradeModalOpen"
    :mask-closable="false"
    :close-on-esc="false"
  >
    <n-card
      class="agent-bulk-upgrade-card"
      :title="bulkUpgradePhase === 'done' ? '批量升级完成' : '批量升级 Agent'"
      :bordered="false"
    >
      <template v-if="bulkUpgradePhase === 'confirm'">
        <dl class="agent-bulk-upgrade-summary">
          <div><dt>目标版本</dt><dd>{{ bulkUpgradeTargetVersion || '—' }}</dd></div>
          <div><dt>本次可升级</dt><dd>{{ bulkUpgradeCandidates.length }} 台</dd></div>
          <div><dt>跳过</dt><dd>{{ skippedCount }} 台</dd></div>
        </dl>
        <ul v-if="bulkUpgradeSkippedSummary.length > 0" class="agent-bulk-upgrade-skipped">
          <li v-for="item in bulkUpgradeSkippedSummary" :key="item.label">
            {{ item.label }} {{ item.count }} 台
          </li>
        </ul>
        <n-alert type="info" class="agent-bulk-upgrade-notice">
          升级过程中 Agent 会短暂断开并自动重新连接。
        </n-alert>
        <n-alert v-if="bulkUpgradeError" type="error" class="agent-bulk-upgrade-notice">
          {{ bulkUpgradeError }}
        </n-alert>
        <div class="modal-actions">
          <n-button :disabled="bulkUpgradeStarting" @click="closeBulkAgentUpgrade">取消</n-button>
          <n-button
            type="primary"
            :loading="bulkUpgradeStarting"
            :disabled="bulkUpgradeCandidates.length === 0 || !bulkUpgradeTargetVersion"
            @click="startBulkAgentUpgrade"
          >
            开始升级
          </n-button>
        </div>
      </template>

      <template v-else>
        <p class="agent-bulk-upgrade-target">目标版本 {{ bulkUpgradeTargetVersion }}</p>
        <strong class="agent-bulk-upgrade-success">
          {{ bulkUpgradeProgress.successCount }} / {{ bulkUpgradeProgress.total }}
          {{ bulkUpgradePhase === 'done' ? '升级成功' : '已成功升级' }}
        </strong>
        <n-progress
          type="line"
          :percentage="progressPercentage"
          :show-indicator="false"
          :processing="bulkUpgradePhase === 'running'"
        />
        <p class="agent-bulk-upgrade-handled">
          已处理 {{ bulkUpgradeProgress.handledCount }} / {{ bulkUpgradeProgress.total }}
        </p>
        <p class="agent-bulk-upgrade-counts">
          成功 {{ bulkUpgradeProgress.successCount }} ·
          失败 {{ bulkUpgradeProgress.failedCount }} ·
          <template v-if="bulkUpgradeProgress.timeoutCount > 0">超时 {{ bulkUpgradeProgress.timeoutCount }} · </template>
          升级中 {{ bulkUpgradeProgress.runningCount }} ·
          等待 {{ bulkUpgradeProgress.waitingCount }}
        </p>

        <div v-if="bulkUpgradePhase === 'running'" class="agent-bulk-upgrade-list">
          <div v-for="item in bulkUpgradeItems" :key="item.serverId" class="agent-bulk-upgrade-item">
            <n-tag :type="statusType(item.status)" size="small">{{ statusLabel(item.status) }}</n-tag>
            <div>
              <strong>{{ item.serverName }}</strong>
              <p v-if="item.status === 'success'">已升级到 {{ item.targetVersion }}</p>
              <p v-else-if="item.error">{{ item.error }}</p>
            </div>
          </div>
        </div>

        <n-alert
          v-else-if="unsuccessfulItems.length === 0"
          type="success"
          class="agent-bulk-upgrade-result"
        >
          所有 Agent 已升级到 {{ bulkUpgradeTargetVersion }}
        </n-alert>
        <n-alert v-else type="warning" title="未升级成功" class="agent-bulk-upgrade-result">
          <div v-for="item in unsuccessfulItems" :key="item.serverId" class="agent-bulk-upgrade-failure">
            <strong>{{ item.serverName }}</strong>
            <p>{{ item.error }}</p>
          </div>
        </n-alert>

        <div class="modal-actions">
          <n-button type="primary" @click="closeBulkAgentUpgrade">
            {{ bulkUpgradePhase === 'running' ? '关闭窗口（升级继续）' : '关闭' }}
          </n-button>
        </div>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.agent-bulk-upgrade-card {
  width: min(680px, calc(100vw - 32px));
  max-height: calc(100vh - 48px);
  overflow-y: auto;
}

.agent-bulk-upgrade-summary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  margin: 0 0 14px;
}

.agent-bulk-upgrade-summary > div {
  padding: 12px;
  border: 1px solid #e3e8ee;
  border-radius: 8px;
}

.agent-bulk-upgrade-summary dt,
.agent-bulk-upgrade-item p,
.agent-bulk-upgrade-failure p {
  color: #65717e;
}

.agent-bulk-upgrade-summary dd {
  margin: 4px 0 0;
  font-weight: 700;
}

.agent-bulk-upgrade-skipped {
  margin: 0 0 14px;
  padding-left: 20px;
}

.agent-bulk-upgrade-notice,
.agent-bulk-upgrade-result {
  margin-top: 14px;
}

.agent-bulk-upgrade-target,
.agent-bulk-upgrade-handled,
.agent-bulk-upgrade-counts {
  margin: 0 0 10px;
}

.agent-bulk-upgrade-success {
  display: block;
  margin-bottom: 12px;
  font-size: 22px;
}

.agent-bulk-upgrade-list {
  display: grid;
  gap: 10px;
  margin-top: 18px;
}

.agent-bulk-upgrade-item {
  display: grid;
  grid-template-columns: 92px minmax(0, 1fr);
  gap: 10px;
  align-items: start;
  padding-bottom: 10px;
  border-bottom: 1px solid #edf0f2;
}

.agent-bulk-upgrade-item p,
.agent-bulk-upgrade-failure p {
  margin: 3px 0 0;
  overflow-wrap: anywhere;
}

.agent-bulk-upgrade-failure + .agent-bulk-upgrade-failure {
  margin-top: 12px;
}

@media (max-width: 560px) {
  .agent-bulk-upgrade-summary {
    grid-template-columns: 1fr;
  }
}
</style>
