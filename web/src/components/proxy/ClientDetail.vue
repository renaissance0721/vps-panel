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
    | 'selectedShare'
    | 'clientDetailOpen'
    | 'clientStatusLabel'
    | 'formatClientTrafficBytes'
    | 'clientTrafficUsageLabel'
    | 'clientTrafficUsagePercentLabel'
    | 'clientTrafficCycleLabel'
    | 'formatTime'
    | 'formatClientExpiration'
    | 'submitting'
    | 'resetClientTraffic'
    | 'copyShareURI'
    | 'showSelectedShareQRCode'
    | 'copiedShareURI'
  >
}>()
const {
  selectedShare,
  clientDetailOpen,
  clientStatusLabel,
  formatClientTrafficBytes,
  clientTrafficUsageLabel,
  clientTrafficUsagePercentLabel,
  clientTrafficCycleLabel,
  formatTime,
  formatClientExpiration,
  submitting,
  resetClientTraffic,
  copyShareURI,
  showSelectedShareQRCode,
  copiedShareURI,
} = toRefs(props.model)
</script>

<template>
<n-modal v-if="selectedShare" v-model:show="clientDetailOpen">
    <n-card class="client-detail-card" title="客户端详情" :bordered="false" closable @close="clientDetailOpen = false">
      <dl class="server-details">
		<div><dt>名称</dt><dd>{{ selectedShare.client.name }}</dd></div><div><dt>状态</dt><dd>{{ clientStatusLabel(selectedShare.client.status) }}</dd></div>
		<div><dt>用户启用</dt><dd>{{ selectedShare.client.enabled ? '是' : '否' }}</dd></div><div><dt>实际可用</dt><dd>{{ selectedShare.client.effective_enabled ? '是' : '否' }}</dd></div>
		<div><dt>失效原因</dt><dd>{{ selectedShare.client.effective_enabled ? '—' : clientStatusLabel(selectedShare.client.status) }}</dd></div>
		<div><dt>本周期上行</dt><dd>{{ formatClientTrafficBytes(selectedShare.client.metrics?.cycle_uplink_bytes ?? 0) }}</dd></div><div><dt>本周期下行</dt><dd>{{ formatClientTrafficBytes(selectedShare.client.metrics?.cycle_downlink_bytes ?? 0) }}</dd></div>
		<div><dt>本周期已用 / 总量</dt><dd>{{ clientTrafficUsageLabel(selectedShare.client.metrics, selectedShare.client.traffic_limit_bytes) }}</dd></div><div><dt>使用率</dt><dd>{{ clientTrafficUsagePercentLabel(selectedShare.client.metrics, selectedShare.client.traffic_limit_bytes) }}</dd></div>
		<div><dt>流量状态</dt><dd>{{ selectedShare.client.quota_exhausted ? '流量已用完' : (selectedShare.client.status === 'warning' ? '流量预警' : '正常') }}</dd></div>
		<div><dt>流量周期</dt><dd>{{ clientTrafficCycleLabel(selectedShare.client.traffic_reset_mode, selectedShare.client.traffic_reset_weekday, selectedShare.client.traffic_reset_day, selectedShare.client.traffic_reset_time) }}</dd></div><div><dt>下次重置</dt><dd>{{ selectedShare.client.next_reset_at ? formatTime(selectedShare.client.next_reset_at) : '不重置' }}</dd></div>
		<div><dt>到期时间</dt><dd>{{ formatClientExpiration(selectedShare.client.expires_at) }}</dd></div>
		<div><dt>最近活动</dt><dd>{{ selectedShare.client.metrics?.last_activity_at ? formatTime(selectedShare.client.metrics.last_activity_at) : '—' }}</dd></div>
		<template v-if="selectedShare.protocol === 'vless'"><div><dt>UDP/443</dt><dd>{{ selectedShare.client.client_udp443 ? '开启' : '关闭' }}</dd></div><div><dt>客户端 Flow</dt><dd>{{ selectedShare.flow }}</dd></div></template>
		<div><dt>连接地址</dt><dd>{{ selectedShare.address }}</dd></div><div><dt>端口</dt><dd>{{ selectedShare.port }}</dd></div>
		<template v-if="selectedShare.protocol === 'vless'"><div><dt>安全层</dt><dd>{{ selectedShare.security === 'tls' ? 'TLS' : 'REALITY' }}</dd></div><div><dt>SNI</dt><dd>{{ selectedShare.server_name }}</dd></div></template>
		<template v-else><div><dt>加密方法</dt><dd>{{ selectedShare.method }}</dd></div><div><dt>网络</dt><dd>TCP + UDP</dd></div></template>
      </dl>
		<div class="share-field"><strong>直连 {{ selectedShare.protocol === 'vless' ? 'VLESS' : 'Shadowsocks' }} URI</strong><n-input :value="selectedShare.uri" type="textarea" readonly :autosize="{ minRows: 4 }" /></div>
		<div class="modal-actions"><n-button secondary :disabled="submitting" @click="resetClientTraffic">重置本周期流量</n-button><n-button secondary @click="showSelectedShareQRCode">二维码</n-button><n-button type="primary" @click="copyShareURI(selectedShare.uri)">{{ copiedShareURI ? '链接已复制' : `复制 ${selectedShare.protocol === 'vless' ? 'VLESS' : 'Shadowsocks'} 链接` }}</n-button></div>
    </n-card>
  </n-modal>
</template>
