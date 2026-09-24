<script setup lang="ts">
import {
  toRefs,
  watch,
} from 'vue'
import {
  NModal,
  NCard,
  NButton,
  NSelect,
  NSwitch,
} from 'naive-ui'
import type {
  ServersViewState,
} from '../../composables/useServers'

const props = defineProps<{
  model: Pick<ServersViewState,
    | 'expirationModalOpen'
    | 'submitting'
    | 'expirationInput'
    | 'renewalPeriodInput'
    | 'autoRenewInput'
    | 'closeExpirationModal'
    | 'saveExpiration'
    | 'clearExpiration'
  >
}>()
const {
  expirationModalOpen,
  submitting,
  expirationInput,
  renewalPeriodInput,
  autoRenewInput,
  closeExpirationModal,
  saveExpiration,
  clearExpiration,
} = toRefs(props.model)

const renewalPeriodOptions = [
  { label: '不设置', value: 0 },
  { label: '月付', value: 1 },
  { label: '季付', value: 3 },
  { label: '半年付', value: 6 },
  { label: '年付', value: 12 },
  { label: '两年付', value: 24 },
  { label: '三年付', value: 36 },
]

watch(renewalPeriodInput, (value) => {
  if (value === 0) autoRenewInput.value = false
})
</script>

<template>
<n-modal
          v-model:show="expirationModalOpen"
          :mask-closable="!submitting"
          @after-leave="closeExpirationModal"
        >
          <n-card
            class="expiration-modal-card"
            title="到期与续费"
            :bordered="false"
            closable
            @close="closeExpirationModal"
          >
            <form class="expiration-form" @submit.prevent="saveExpiration">
              <label>
                <span>到期日期</span>
                <input
                  v-model="expirationInput"
                  class="date-input"
                  type="date"
                  :disabled="submitting"
                />
              </label>
              <p>按 Asia/Shanghai 当日 23:59:59 到期。</p>
              <label>
                <span>续费周期</span>
                <n-select
                  v-model:value="renewalPeriodInput"
                  :options="renewalPeriodOptions"
                  :disabled="submitting"
                />
              </label>
              <label class="switch-field">
                <span>自动续费</span>
                <n-switch
                  v-model:value="autoRenewInput"
                  :disabled="submitting || !expirationInput || renewalPeriodInput === 0"
                  :title="!expirationInput || renewalPeriodInput === 0 ? '请先设置到期日期和续费周期' : '仅顺延 Panel 中记录的到期日期，不会向 VPS 商家付款。'"
                />
              </label>
              <p>自动续费仅会按续费周期顺延 VPS Panel 中的到期日期，不会向 VPS 商家付款。</p>
              <div class="expiration-modal-actions">
                <n-button secondary :disabled="submitting" @click="clearExpiration">
                  设为不限
                </n-button>
                <n-button :disabled="submitting" @click="closeExpirationModal">
                  取消
                </n-button>
                <n-button type="primary" attr-type="submit" :loading="submitting">
                  保存
                </n-button>
              </div>
            </form>
          </n-card>
        </n-modal>
</template>
