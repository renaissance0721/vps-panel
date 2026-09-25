<script setup lang="ts">
import {
  toRefs,
} from 'vue'
import {
  NButton,
} from 'naive-ui'
import type {
  ServersViewState,
} from '../../composables/useServers'

const props = defineProps<{
  model: Pick<ServersViewState,
    | 'selectedServer'
    | 'submitting'
    | 'openTrafficModal'
    | 'openTrafficAdjustmentModal'
    | 'formatTrafficBytes'
    | 'trafficCountModeLabel'
    | 'measuredTrafficUsed'
    | 'trafficAdjustmentLabel'
    | 'trafficUsagePercentLabel'
    | 'formatTime'
  >
}>()
const {
  selectedServer,
  submitting,
  openTrafficModal,
  openTrafficAdjustmentModal,
  formatTrafficBytes,
  trafficCountModeLabel,
  measuredTrafficUsed,
  trafficAdjustmentLabel,
  trafficUsagePercentLabel,
  formatTime,
} = toRefs(props.model)
</script>

<template>
<template v-if="selectedServer"><div class="section-heading">
              <h3 class="system-info-title">月流量</h3>
              <div v-if="!selectedServer.archived_at" class="section-heading-actions">
                <n-button
                  size="tiny"
                  secondary
                  :disabled="submitting || !!selectedServer.decommission_status"
                  @click="openTrafficModal"
                >
                  修改设置
                </n-button>
                <n-button
                  size="tiny"
                  secondary
                  :disabled="submitting || !!selectedServer.decommission_status"
                  @click="openTrafficAdjustmentModal"
                >
                  校准本周期流量
                </n-button>
              </div>
            </div>
            <dl class="server-details">
              <div>
                <dt>本周期 RX</dt>
                <dd>{{ formatTrafficBytes(selectedServer.metrics?.cycle_rx_bytes ?? 0) }}</dd>
              </div>
              <div>
                <dt>本周期 TX</dt>
                <dd>{{ formatTrafficBytes(selectedServer.metrics?.cycle_tx_bytes ?? 0) }}</dd>
              </div>
              <div>
                <dt>统计方式</dt>
                <dd>{{ trafficCountModeLabel(selectedServer.traffic_count_mode) }}</dd>
              </div>
              <div>
                <dt>机器统计已用</dt>
                <dd>{{ formatTrafficBytes(measuredTrafficUsed(selectedServer)) }}</dd>
              </div>
              <div><dt>校准偏移</dt><dd>{{ trafficAdjustmentLabel(selectedServer) }}</dd></div>
              <div><dt>当前已用</dt><dd>{{ formatTrafficBytes(selectedServer.traffic_used_bytes) }}</dd></div>
              <div>
                <dt>月流量额度</dt>
                <dd>
                  {{ selectedServer.monthly_traffic_limit_bytes
                    ? formatTrafficBytes(selectedServer.monthly_traffic_limit_bytes)
                    : '不限' }}
                </dd>
              </div>
              <div><dt>使用比例</dt><dd>{{ trafficUsagePercentLabel(selectedServer) }}</dd></div>
              <div>
                <dt>重置时间</dt>
                <dd>每月 {{ selectedServer.traffic_reset_day }} 日 {{ selectedServer.traffic_reset_time }}</dd>
              </div>
              <div>
                <dt>本周期开始</dt>
                <dd>
                  {{ selectedServer.metrics?.cycle_started_at
                    ? formatTime(selectedServer.metrics.cycle_started_at)
                    : '—' }}
                </dd>
              </div>
            </dl>

            </template>
</template>
