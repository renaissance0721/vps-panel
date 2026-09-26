<script setup lang="ts">
import { toRefs } from 'vue'
import { NAlert, NButton, NCard, NModal } from 'naive-ui'
import type { ServersViewState } from '../../composables/useServers'

const props = defineProps<{
  model: Pick<ServersViewState,
    | 'ownerModalOpen'
    | 'ownerUserID'
    | 'ownerFormError'
    | 'submitting'
    | 'selectedServer'
    | 'orderedUsers'
    | 'closeOwnerModal'
    | 'saveServerOwner'
  >
}>()
const {
  ownerModalOpen,
  ownerUserID,
  ownerFormError,
  submitting,
  selectedServer,
  orderedUsers,
  closeOwnerModal,
  saveServerOwner,
} = toRefs(props.model)
</script>

<template>
  <n-modal v-model:show="ownerModalOpen" :mask-closable="!submitting" @after-leave="ownerFormError = ''">
    <n-card v-if="selectedServer" class="access-modal-card" title="修改所有者" :bordered="false" closable @close="closeOwnerModal">
      <form class="server-access-form" @submit.prevent="saveServerOwner">
        <n-alert v-if="ownerFormError" type="error" class="form-alert">{{ ownerFormError }}</n-alert>
        <label>
          <span>所有者</span>
          <select v-model.number="ownerUserID" class="settings-input" :disabled="submitting">
            <option :value="0">无所有者</option>
            <option v-for="user in orderedUsers" :key="user.id" :value="user.id">
              {{ user.username }}（{{ user.role }}）
            </option>
          </select>
        </label>
        <div class="expiration-modal-actions">
          <n-button :disabled="submitting" @click="closeOwnerModal">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button>
        </div>
      </form>
    </n-card>
  </n-modal>
</template>
