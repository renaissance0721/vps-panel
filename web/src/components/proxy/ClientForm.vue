<script setup lang="ts">
import {
  toRefs,
} from 'vue'
import {
  NModal,
  NCard,
  NInput,
  NButton,
} from 'naive-ui'
import type {
  ProxiesViewState,
} from '../../composables/useProxies'

const props = defineProps<{
  model: Pick<ProxiesViewState,
    | 'clientFormOpen'
    | 'submitting'
    | 'clientFormMode'
    | 'saveClient'
    | 'clientName'
    | 'selectedProxy'
    | 'clientUDP443'
    | 'clientTrafficLimit'
    | 'clientTrafficLimitUnit'
    | 'clientTrafficResetMode'
    | 'clientTrafficResetWeekday'
    | 'clientTrafficResetDay'
    | 'clientTrafficResetTime'
    | 'clientExpirationMode'
    | 'clientExpiresAt'
    | 'clientEnabled'
  >
}>()
const {
  clientFormOpen,
  submitting,
  clientFormMode,
  saveClient,
  clientName,
  selectedProxy,
  clientUDP443,
  clientTrafficLimit,
  clientTrafficLimitUnit,
  clientTrafficResetMode,
  clientTrafficResetWeekday,
  clientTrafficResetDay,
  clientTrafficResetTime,
  clientExpirationMode,
  clientExpiresAt,
  clientEnabled,
} = toRefs(props.model)
</script>

<template>
<n-modal v-model:show="clientFormOpen" :mask-closable="!submitting">
    <n-card class="client-form-card" :title="clientFormMode === 'create' ? '新增客户端' : '编辑客户端'" :bordered="false" closable @close="clientFormOpen = false">
      <form class="proxy-form" @submit.prevent="saveClient">
        <label><span>名称</span><n-input v-model:value="clientName" maxlength="100" /></label>
		<p>客户端凭据由系统自动生成，不会在普通 API 中显示。</p>
		<label v-if="selectedProxy?.protocol === 'vless'" class="checkbox-row"><input v-model="clientUDP443" type="checkbox" /><span>允许 UDP/443 / QUIC</span></label>
        <label><span>流量额度</span><div class="inline-fields"><input v-model="clientTrafficLimit" class="settings-input" type="number" min="0" step="any" placeholder="留空表示不限" /><select v-model="clientTrafficLimitUnit" class="settings-input"><option value="G">G</option><option value="T">T</option></select></div></label>
        <label><span>重置周期</span><select v-model="clientTrafficResetMode" class="settings-input"><option value="never">不重置</option><option value="daily">每日</option><option value="weekly">每周</option><option value="monthly">每月</option></select></label>
        <label v-if="clientTrafficResetMode === 'weekly'"><span>星期</span><select v-model.number="clientTrafficResetWeekday" class="settings-input"><option :value="1">周一</option><option :value="2">周二</option><option :value="3">周三</option><option :value="4">周四</option><option :value="5">周五</option><option :value="6">周六</option><option :value="7">周日</option></select></label>
        <label v-if="clientTrafficResetMode === 'monthly'"><span>日期</span><input v-model.number="clientTrafficResetDay" class="settings-input" type="number" min="1" max="31" /></label>
        <label v-if="clientTrafficResetMode !== 'never'"><span>重置时间（上海时区）</span><input v-model="clientTrafficResetTime" class="settings-input" type="time" /></label>
        <label><span>到期时间</span><select v-model="clientExpirationMode" class="settings-input"><option value="unlimited">不限</option><option value="specified">指定日期时间</option></select></label>
        <label v-if="clientExpirationMode === 'specified'"><span>到期日期时间（上海时区）</span><input v-model="clientExpiresAt" class="settings-input" type="datetime-local" /></label>
        <label class="checkbox-row"><input v-model="clientEnabled" type="checkbox" /><span>启用客户端</span></label>
        <div class="modal-actions"><n-button @click="clientFormOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>
</template>
