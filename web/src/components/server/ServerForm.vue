<script setup lang="ts">
import { toRefs } from 'vue'
import { NAlert, NButton, NCard, NInput, NModal, NSelect, NSwitch } from 'naive-ui'
import type { ServersViewState } from '../../composables/useServers'
import { userRoleLabel } from '../../format'
import { renewalPeriodOptions } from '../../server'

const props = defineProps<{
  model: Pick<ServersViewState,
    | 'createServerModalOpen'
    | 'createServerFormError'
    | 'createServerRecord'
    | 'closeCreateServerModal'
    | 'resetCreateServerForm'
    | 'createServerName'
	| 'createServerBoundDomainIPv4'
	| 'createServerBoundDomainIPv6'
    | 'createServerVisibility'
    | 'createServerAccessUserIDs'
    | 'createServerExpiration'
    | 'createServerRenewalPeriod'
    | 'createServerAutoRenew'
    | 'createServerTrafficLimit'
    | 'createServerTrafficUnit'
    | 'createServerTrafficCountMode'
    | 'createServerTrafficResetDay'
    | 'createServerTrafficResetTime'
    | 'submitting'
    | 'ensureCreateCurrentUser'
    | 'serverAccessUsers'
    | 'state'
  >
}>()

const {
  createServerModalOpen, createServerFormError, createServerRecord, closeCreateServerModal,
	resetCreateServerForm, createServerName, createServerBoundDomainIPv4, createServerBoundDomainIPv6, createServerVisibility, createServerAccessUserIDs,
  createServerExpiration, createServerRenewalPeriod, createServerAutoRenew, createServerTrafficLimit,
  createServerTrafficUnit, createServerTrafficCountMode, createServerTrafficResetDay,
  createServerTrafficResetTime, submitting, ensureCreateCurrentUser, serverAccessUsers, state,
} = toRefs(props.model)
</script>

<template>
  <n-modal
    v-model:show="createServerModalOpen"
    :mask-closable="!submitting"
    :close-on-esc="!submitting"
    @after-leave="resetCreateServerForm"
  >
    <n-card class="server-create-modal-card" title="新增服务器" :bordered="false" closable @close="closeCreateServerModal">
      <form class="server-create-form" @submit.prevent="createServerRecord">
        <n-alert v-if="createServerFormError" type="error" class="form-alert">{{ createServerFormError }}</n-alert>

        <fieldset class="server-create-section">
          <legend>基本信息</legend>
          <label>
            <span>名称</span>
            <n-input v-model:value="createServerName" maxlength="100" placeholder="例如：日本服务器 01" :disabled="submitting" />
          </label>
          <label>
			<span>IPv4 已绑定域名（可选）</span>
			<n-input v-model:value="createServerBoundDomainIPv4" maxlength="254" placeholder="例如：v4.jp.example.com" :disabled="submitting" />
			<small class="form-help">新增代理节点或中转时，可作为 IPv4 入口地址。</small>
		  </label>
		  <label>
			<span>IPv6 已绑定域名（可选）</span>
			<n-input v-model:value="createServerBoundDomainIPv6" maxlength="254" placeholder="例如：v6.jp.example.com" :disabled="submitting" />
			<small class="form-help">服务器尚未上报网络信息，可先保存；注册 Agent 后会检测公网 IPv6。</small>
          </label>
        </fieldset>

        <fieldset class="server-create-section">
          <legend>访问控制</legend>
          <label>
            <span>访问范围</span>
            <select v-model="createServerVisibility" class="settings-input" :disabled="submitting" @change="ensureCreateCurrentUser">
              <option value="public">公开（所有管理账号）</option>
              <option value="private">私有（仅指定账号）</option>
            </select>
          </label>
          <fieldset v-if="createServerVisibility === 'private'" class="server-access-users">
            <legend>允许访问的管理账号</legend>
            <label v-for="user in serverAccessUsers" :key="user.id" class="server-access-user">
              <input v-model="createServerAccessUserIDs" type="checkbox" :value="user.id" :disabled="submitting || user.id === state?.user?.id" />
              <span>{{ user.username }}（{{ userRoleLabel(user.role) }}）</span>
            </label>
          </fieldset>
          <p v-if="createServerVisibility === 'private'" class="switch-help">当前账号会自动保留访问权限。</p>
        </fieldset>

        <fieldset class="server-create-section">
          <legend>到期与续费</legend>
          <label>
            <span>到期日期</span>
            <input v-model="createServerExpiration" class="date-input" type="date" :disabled="submitting" />
            <small class="form-help">留空表示不限；设置后按 Asia/Shanghai 当日 23:59:59 到期。</small>
          </label>
          <label>
            <span>续费周期</span>
            <n-select v-model:value="createServerRenewalPeriod" :options="renewalPeriodOptions" :disabled="submitting" />
          </label>
          <div class="switch-field">
            <span>自动续费</span>
            <n-switch
              v-model:value="createServerAutoRenew"
              :disabled="submitting || !createServerExpiration || createServerRenewalPeriod === 0"
              :title="!createServerExpiration || createServerRenewalPeriod === 0 ? '请先设置到期日期和续费周期' : '仅顺延 Panel 中记录的到期日期，不会向 VPS 商家付款。'"
            />
          </div>
        </fieldset>

        <fieldset class="server-create-section">
          <legend>流量设置</legend>
          <label>
            <span>月流量额度</span>
            <div class="traffic-limit-input">
              <input v-model="createServerTrafficLimit" class="settings-input" type="number" min="0" step="any" placeholder="例如 500，留空表示不限" :disabled="submitting" />
              <select v-model="createServerTrafficUnit" class="settings-input" aria-label="月流量额度单位" :disabled="submitting">
                <option value="G">G</option>
                <option value="T">T</option>
              </select>
            </div>
          </label>
          <label>
            <span>流量统计方式</span>
            <select v-model="createServerTrafficCountMode" class="settings-input" :disabled="submitting">
              <option value="single">单向（TX）</option>
              <option value="bidirectional">双向（RX + TX）</option>
            </select>
          </label>
          <label>
            <span>重置日期</span>
            <input v-model.number="createServerTrafficResetDay" class="settings-input" type="number" min="1" max="31" :disabled="submitting" />
          </label>
          <label>
            <span>重置时间</span>
            <input v-model="createServerTrafficResetTime" class="settings-input" type="time" :disabled="submitting" />
          </label>
          <small class="form-help">重置时间按 Asia/Shanghai 计算；当月没有该日期时使用当月最后一天。</small>
        </fieldset>

        <div class="expiration-modal-actions">
          <n-button :disabled="submitting" @click="closeCreateServerModal">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="submitting">新增服务器</n-button>
        </div>
      </form>
    </n-card>
  </n-modal>
</template>
