<script setup lang="ts">
import {
  toRefs,
} from 'vue'
import {
  NCard,
  NInput,
  NButton,
} from 'naive-ui'
import type {
  ServersViewState,
} from '../../composables/useServers'

const props = defineProps<{
  model: Pick<ServersViewState,
    | 'serverListMode'
    | 'createServerRecord'
    | 'serverName'
    | 'serverVisibility'
    | 'submitting'
    | 'ensureCreateCurrentUser'
    | 'orderedUsers'
    | 'serverAccessUserIDs'
    | 'state'
  >
}>()
const {
  serverListMode,
  createServerRecord,
  serverName,
  serverVisibility,
  submitting,
  ensureCreateCurrentUser,
  orderedUsers,
  serverAccessUserIDs,
  state,
} = toRefs(props.model)
</script>

<template>
<div v-if="serverListMode === 'active'" class="server-create">
            <n-card title="新增服务器" :bordered="true">
              <p class="card-copy">创建后将生成一个 24 小时有效的 Agent 安装令牌。</p>
              <form class="server-access-form" @submit.prevent="createServerRecord">
                <label>
                  <span>名称</span>
                  <n-input
                    v-model:value="serverName"
                    maxlength="100"
                    placeholder="例如：日本服务器 01"
                  />
                </label>
                <label>
                  <span>访问范围</span>
                  <select
                    v-model="serverVisibility"
                    class="settings-input"
                    :disabled="submitting"
                    @change="ensureCreateCurrentUser"
                  >
                    <option value="public">公开（所有已登录账号）</option>
                    <option value="private">私有（仅指定账号）</option>
                  </select>
                </label>
                <fieldset v-if="serverVisibility === 'private'" class="server-access-users">
                  <legend>允许访问的账号</legend>
                  <label v-for="user in orderedUsers" :key="user.id" class="server-access-user">
                    <input
                      v-model="serverAccessUserIDs"
                      type="checkbox"
                      :value="user.id"
                      :disabled="submitting || user.id === state?.user?.id"
                    />
                    <span>{{ user.username }}（{{ user.role }}）</span>
                  </label>
                </fieldset>
                <n-button type="primary" attr-type="submit" :loading="submitting">
                  新增服务器
                </n-button>
              </form>
            </n-card>
          </div>
</template>
