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
    | 'expirationModalOpen'
    | 'submitting'
    | 'expirationInput'
    | 'closeExpirationModal'
    | 'saveExpiration'
    | 'clearExpiration'
  >
}>()
const {
  expirationModalOpen,
  submitting,
  expirationInput,
  closeExpirationModal,
  saveExpiration,
  clearExpiration,
} = toRefs(props.model)
</script>

<template>
<n-modal
          v-model:show="expirationModalOpen"
          :mask-closable="!submitting"
          @after-leave="expirationInput = ''"
        >
          <n-card
            class="expiration-modal-card"
            title="修改到期日期"
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
