<script setup lang="ts">
import { toRefs, watch } from 'vue'
import { NAlert, NButton, NCard, NInput, NInputNumber, NModal, NSelect, NSwitch } from 'naive-ui'
import type { ServersViewState } from '../../composables/useServers'
import { userRoleLabel } from '../../format'
import { networkStackOptions, renewalPeriodOptions, serverIPv6State, serverIPv6StatusMessage } from '../../server'

const props = defineProps<{
  model: Pick<ServersViewState,
    | 'basicInfoModalOpen'
    | 'basicInfoFormError'
    | 'submitting'
	| 'selectedServer'
    | 'nameInput'
    | 'bandwidthMbpsInput'
    | 'networkStackInput'
	| 'boundDomainIPv4Input'
	| 'boundDomainIPv6Input'
    | 'ownerUserID'
    | 'orderedUsers'
    | 'serverAccessUsers'
    | 'accessVisibility'
    | 'accessUserIDs'
    | 'state'
    | 'expirationInput'
    | 'renewalPeriodInput'
    | 'autoRenewInput'
    | 'ensureAccessCurrentUser'
    | 'closeBasicInfoModal'
    | 'saveBasicInfo'
  >
}>()

const {
  basicInfoModalOpen,
  basicInfoFormError,
  submitting,
  nameInput,
  bandwidthMbpsInput,
  networkStackInput,
	boundDomainIPv4Input,
	boundDomainIPv6Input,
  ownerUserID,
  orderedUsers,
  serverAccessUsers,
  accessVisibility,
  accessUserIDs,
  state,
  expirationInput,
  renewalPeriodInput,
  autoRenewInput,
  ensureAccessCurrentUser,
  closeBasicInfoModal,
  saveBasicInfo,
} = toRefs(props.model)

watch(renewalPeriodInput, (value) => {
  if (value === 0) autoRenewInput.value = false
})

watch(expirationInput, (value) => {
  if (!value) autoRenewInput.value = false
})
</script>

<template>
  <n-modal v-model:show="basicInfoModalOpen" :mask-closable="!submitting">
    <n-card
      class="server-basic-info-modal-card"
      title="修改基本信息"
      :bordered="false"
      closable
      @close="closeBasicInfoModal"
    >
      <form class="basic-info-form" @submit.prevent="saveBasicInfo">
        <n-alert v-if="basicInfoFormError" type="error" class="form-alert">
          {{ basicInfoFormError }}
        </n-alert>

        <label><span>名称</span><n-input v-model:value="nameInput" maxlength="100" :disabled="submitting" /></label>

        <label>
          <span>带宽大小</span>
          <n-input-number v-model:value="bandwidthMbpsInput" :min="1" :max="10000000" :precision="0" placeholder="未设置" :disabled="submitting">
            <template #suffix>Mbps</template>
          </n-input-number>
          <small class="form-help">填写 VPS 套餐标称网络带宽；留空表示未知。</small>
        </label>
        <label>
          <span>网络栈</span>
          <n-select v-model:value="networkStackInput" :options="networkStackOptions" clearable placeholder="未设置" :disabled="submitting" />
          <small class="form-help">管理员声明的网络栈分类，不会修改服务器实际网络配置。</small>
        </label>

        <label>
		  <span>IPv4 已绑定域名（可选）</span>
		  <n-input v-model:value="boundDomainIPv4Input" maxlength="254" placeholder="例如：v4.jp.example.com" :disabled="submitting" />
		</label>
		<label>
		  <span>IPv6 已绑定域名（可选）</span>
		  <n-input v-model:value="boundDomainIPv6Input" maxlength="254" placeholder="例如：v6.jp.example.com" :disabled="submitting" />
		  <n-alert :type="serverIPv6State(model.selectedServer) === 'none' ? 'warning' : 'info'">
			{{ serverIPv6StatusMessage(model.selectedServer) }}
		  </n-alert>
        </label>

        <label>
          <span>所有者</span>
          <select v-model.number="ownerUserID" class="settings-input" :disabled="submitting">
            <option :value="0">无所有者</option>
            <option v-for="user in orderedUsers" :key="user.id" :value="user.id">
              {{ user.username }}（{{ userRoleLabel(user.role) }}）
            </option>
          </select>
        </label>

        <label>
          <span>访问范围</span>
          <select
            v-model="accessVisibility"
            class="settings-input"
            :disabled="submitting"
            @change="ensureAccessCurrentUser"
          >
            <option value="public">公开（所有管理账号）</option>
            <option value="private">私有（仅指定账号）</option>
          </select>
        </label>

        <fieldset v-if="accessVisibility === 'private'" class="server-access-users">
          <legend>允许访问的管理账号</legend>
          <label v-for="user in serverAccessUsers" :key="user.id" class="server-access-user">
            <input
              v-model="accessUserIDs"
              type="checkbox"
              :value="user.id"
              :disabled="submitting || user.id === state?.user?.id"
            />
            <span>{{ user.username }}（{{ userRoleLabel(user.role) }}）</span>
          </label>
        </fieldset>
        <p v-if="accessVisibility === 'private'" class="switch-help">当前账号会自动保留访问权限。</p>

        <label>
          <span>到期日期</span>
          <input v-model="expirationInput" class="date-input" type="date" :disabled="submitting" />
          <small class="form-help">留空表示不限；设置后按 Asia/Shanghai 当日 23:59:59 到期。</small>
        </label>

        <label>
          <span>续费周期</span>
          <n-select v-model:value="renewalPeriodInput" :options="renewalPeriodOptions" :disabled="submitting" />
        </label>

        <div class="switch-field">
          <span>自动续费</span>
          <n-switch
            v-model:value="autoRenewInput"
            :disabled="submitting || !expirationInput || renewalPeriodInput === 0"
            :title="!expirationInput || renewalPeriodInput === 0 ? '请先设置到期日期和续费周期' : '仅顺延 Panel 中记录的到期日期，不会向 VPS 商家付款。'"
          />
        </div>
        <p class="switch-help">自动续费仅会按续费周期顺延 VPS Panel 中的到期日期，不会向 VPS 商家付款。</p>

        <div class="expiration-modal-actions">
          <n-button :disabled="submitting" @click="closeBasicInfoModal">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button>
        </div>
      </form>
    </n-card>
  </n-modal>
</template>
