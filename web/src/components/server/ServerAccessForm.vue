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
    | 'accessModalOpen'
    | 'submitting'
    | 'accessFormError'
    | 'selectedServer'
    | 'closeAccessModal'
    | 'saveServerAccess'
    | 'accessVisibility'
    | 'ensureAccessCurrentUser'
    | 'orderedUsers'
    | 'accessUserIDs'
    | 'state'
  >
}>()
const {
  accessModalOpen,
  submitting,
  accessFormError,
  selectedServer,
  closeAccessModal,
  saveServerAccess,
  accessVisibility,
  ensureAccessCurrentUser,
  orderedUsers,
  accessUserIDs,
  state,
} = toRefs(props.model)
</script>

<template>
<n-modal
          v-model:show="accessModalOpen"
          :mask-closable="!submitting"
          @after-leave="accessFormError = ''"
        >
          <n-card
            v-if="selectedServer"
            class="access-modal-card"
            title="修改访问范围"
            :bordered="false"
            closable
            @close="closeAccessModal"
          >
            <form class="server-access-form" @submit.prevent="saveServerAccess">
              <n-alert v-if="accessFormError" type="error" class="form-alert">
                {{ accessFormError }}
              </n-alert>
              <label>
                <span>访问范围</span>
                <select
                  v-model="accessVisibility"
                  class="settings-input"
                  :disabled="submitting"
                  @change="ensureAccessCurrentUser"
                >
                  <option value="public">公开（所有已登录账号）</option>
                  <option value="private">私有（仅指定账号）</option>
                </select>
              </label>
              <fieldset v-if="accessVisibility === 'private'" class="server-access-users">
                <legend>允许访问的账号</legend>
                <label v-for="user in orderedUsers" :key="user.id" class="server-access-user">
                  <input
                    v-model="accessUserIDs"
                    type="checkbox"
                    :value="user.id"
                    :disabled="submitting || user.id === state?.user?.id"
                  />
                  <span>{{ user.username }}（{{ user.role }}）</span>
                </label>
              </fieldset>
              <p v-if="accessVisibility === 'private'">当前账号会自动保留访问权限。</p>
              <div class="expiration-modal-actions">
                <n-button :disabled="submitting" @click="closeAccessModal">取消</n-button>
                <n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button>
              </div>
            </form>
          </n-card>
        </n-modal>
</template>
