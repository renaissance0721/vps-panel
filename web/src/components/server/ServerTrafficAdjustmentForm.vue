<script setup lang="ts">
import {
  toRefs,
} from 'vue'
import {
  NModal,
  NCard,
  NButton,
} from 'naive-ui'
import type {
  ServersViewState,
} from '../../composables/useServers'

const props = defineProps<{
  model: Pick<ServersViewState,
    | 'trafficAdjustmentModalOpen'
    | 'submitting'
    | 'resetTrafficAdjustmentForm'
    | 'selectedServer'
    | 'closeTrafficAdjustmentModal'
    | 'saveTrafficAdjustment'
    | 'formatTrafficBytes'
    | 'measuredTrafficUsed'
    | 'trafficAdjustmentLabel'
    | 'trafficAdjustmentInput'
    | 'trafficAdjustmentUnit'
    | 'clearTrafficAdjustment'
  >
}>()
const {
  trafficAdjustmentModalOpen,
  submitting,
  resetTrafficAdjustmentForm,
  selectedServer,
  closeTrafficAdjustmentModal,
  saveTrafficAdjustment,
  formatTrafficBytes,
  measuredTrafficUsed,
  trafficAdjustmentLabel,
  trafficAdjustmentInput,
  trafficAdjustmentUnit,
  clearTrafficAdjustment,
} = toRefs(props.model)
</script>

<template>
<n-modal
          v-model:show="trafficAdjustmentModalOpen"
          :mask-closable="!submitting"
          @after-leave="resetTrafficAdjustmentForm"
        >
          <n-card
            v-if="selectedServer"
            class="traffic-adjustment-modal-card"
            title="校准本周期流量"
            :bordered="false"
            closable
            @close="closeTrafficAdjustmentModal"
          >
            <form class="traffic-form" @submit.prevent="saveTrafficAdjustment">
              <dl class="server-details">
                <div>
                  <dt>当前机器统计</dt>
                  <dd>{{ formatTrafficBytes(measuredTrafficUsed(selectedServer)) }}</dd>
                </div>
                <div><dt>当前校准偏移</dt><dd>{{ trafficAdjustmentLabel(selectedServer) }}</dd></div>
                <div><dt>当前最终已用</dt><dd>{{ formatTrafficBytes(selectedServer.traffic_used_bytes) }}</dd></div>
              </dl>
              <label>
                <span>目标已用流量</span>
                <div class="traffic-limit-input">
                  <input
                    v-model="trafficAdjustmentInput"
                    class="settings-input"
                    type="number"
                    min="0"
                    step="any"
                    placeholder="例如 183"
                    :disabled="submitting"
                  />
                  <select
                    v-model="trafficAdjustmentUnit"
                    class="settings-input"
                    aria-label="目标已用流量单位"
                    :disabled="submitting"
                  >
                    <option value="G">G</option>
                    <option value="T">T</option>
                  </select>
                </div>
              </label>
              <div class="expiration-modal-actions">
                <n-button secondary :disabled="submitting" @click="clearTrafficAdjustment">
                  清除校准
                </n-button>
                <n-button :disabled="submitting" @click="closeTrafficAdjustmentModal">取消</n-button>
                <n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button>
              </div>
            </form>
          </n-card>
        </n-modal>
</template>
