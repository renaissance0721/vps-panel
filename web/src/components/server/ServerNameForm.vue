<script setup lang="ts">
import { toRefs } from 'vue'
import { NAlert, NButton, NCard, NInput, NModal } from 'naive-ui'
import type { ServersViewState } from '../../composables/useServers'

const props = defineProps<{ model: Pick<ServersViewState, 'nameModalOpen' | 'nameInput' | 'nameFormError' | 'submitting' | 'closeNameModal' | 'saveServerName'> }>()
const { nameModalOpen, nameInput, nameFormError, submitting, closeNameModal, saveServerName } = toRefs(props.model)
</script>

<template>
  <n-modal v-model:show="nameModalOpen" :mask-closable="!submitting">
    <n-card class="expiration-modal-card" title="修改名称" :bordered="false" closable @close="closeNameModal">
      <form class="expiration-form" @submit.prevent="saveServerName">
        <n-alert v-if="nameFormError" type="error" class="form-alert">{{ nameFormError }}</n-alert>
        <label><span>名称</span><n-input v-model:value="nameInput" maxlength="100" :disabled="submitting" /></label>
        <div class="expiration-modal-actions">
          <n-button :disabled="submitting" @click="closeNameModal">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button>
        </div>
      </form>
    </n-card>
  </n-modal>
</template>
