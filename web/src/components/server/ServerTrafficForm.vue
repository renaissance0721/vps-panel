<script setup lang="ts">
import {
  toRefs,
} from 'vue'
import {
  NModal,
  NCard,
  NAlert,
  NButton,
} from 'naive-ui'
import type {
  ServersViewState,
} from '../../composables/useServers'

const props = defineProps<{
  model: Pick<ServersViewState,
    | 'trafficModalOpen'
    | 'submitting'
    | 'resetTrafficForm'
    | 'closeTrafficModal'
    | 'saveTrafficConfig'
    | 'trafficFormError'
    | 'trafficLimitInput'
    | 'trafficLimitUnit'
    | 'trafficCountMode'
    | 'trafficResetDay'
    | 'trafficResetTime'
  >
}>()
const {
  trafficModalOpen,
  submitting,
  resetTrafficForm,
  closeTrafficModal,
  saveTrafficConfig,
  trafficFormError,
  trafficLimitInput,
  trafficLimitUnit,
  trafficCountMode,
  trafficResetDay,
  trafficResetTime,
} = toRefs(props.model)
</script>

<template>
<n-modal
          v-model:show="trafficModalOpen"
          :mask-closable="!submitting"
          @after-leave="resetTrafficForm"
        >
          <n-card
            class="traffic-modal-card"
            title="修改月流量设置"
            :bordered="false"
            closable
            @close="closeTrafficModal"
          >
            <form class="traffic-form" @submit.prevent="saveTrafficConfig">
              <n-alert v-if="trafficFormError" type="error" class="form-alert">
                {{ trafficFormError }}
              </n-alert>
              <label>
                <span>月流量额度</span>
                <div class="traffic-limit-input">
                  <input
                    v-model="trafficLimitInput"
                    class="settings-input"
                    type="number"
                    min="0"
                    step="any"
                    placeholder="例如 500，留空表示不限"
                    :disabled="submitting"
                  />
                  <select
                    v-model="trafficLimitUnit"
                    class="settings-input"
                    aria-label="月流量额度单位"
                    :disabled="submitting"
                  >
                    <option value="G">G</option>
                    <option value="T">T</option>
                  </select>
                </div>
              </label>
              <label>
                <span>统计方式</span>
                <select v-model="trafficCountMode" class="settings-input" :disabled="submitting">
                  <option value="single">单向（TX）</option>
                  <option value="bidirectional">双向（RX + TX）</option>
                </select>
              </label>
              <label>
                <span>每月重置日</span>
                <input
                  v-model.number="trafficResetDay"
                  class="settings-input"
                  type="number"
                  min="1"
                  max="31"
                  :disabled="submitting"
                />
              </label>
              <label>
                <span>重置时间</span>
                <input
                  v-model="trafficResetTime"
                  class="settings-input"
                  type="time"
                  :disabled="submitting"
                />
              </label>
              <p>重置时间按 Asia/Shanghai 计算；当月没有该日期时使用当月最后一天。修改统计方式不会清除现有校准偏移，必要时请重新校准。</p>
              <div class="expiration-modal-actions">
                <n-button :disabled="submitting" @click="closeTrafficModal">取消</n-button>
                <n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button>
              </div>
            </form>
          </n-card>
        </n-modal>
</template>
