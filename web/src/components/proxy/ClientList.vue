<script setup lang="ts">
import {
  toRefs,
} from 'vue'
import {
  NButton,
  NEmpty,
  NTag,
} from 'naive-ui'
import type {
  ProxiesViewState,
} from '../../composables/useProxies'

const props = defineProps<{
  model: Pick<ProxiesViewState,
    | 'selectedProxy'
    | 'openCreateClient'
    | 'showsVLESSClientFields'
    | 'clientStatusTagType'
    | 'clientStatusLabel'
    | 'clientTrafficUsageLabel'
    | 'clientTrafficCycleLabel'
    | 'formatClientExpiration'
    | 'formatTime'
    | 'copyClientURI'
    | 'showClientQRCode'
    | 'copiedClientID'
    | 'showClient'
    | 'openEditClient'
    | 'toggleClient'
    | 'removeClient'
  >
}>()
const {
  selectedProxy,
  openCreateClient,
  showsVLESSClientFields,
  clientStatusTagType,
  clientStatusLabel,
  clientTrafficUsageLabel,
  clientTrafficCycleLabel,
  formatClientExpiration,
  formatTime,
  copyClientURI,
  showClientQRCode,
  copiedClientID,
  showClient,
  openEditClient,
  toggleClient,
  removeClient,
} = toRefs(props.model)
</script>

<template>
<template v-if="selectedProxy"><div class="section-heading"><h3>客户端</h3><n-button size="small" type="primary" @click="openCreateClient">新增客户端</n-button></div>
      <n-empty v-if="!selectedProxy.clients?.length" size="small" description="暂无客户端" />
      <div v-else class="server-table-wrap">
		<table class="server-table client-table"><thead><tr><th>名称</th><th>状态</th><th>已用 / 总量</th><th>周期</th><th>到期时间</th><th>最近活动</th><th v-if="showsVLESSClientFields(selectedProxy.protocol)">UDP/443</th><th>操作</th></tr></thead>
			<tbody><tr v-for="client in selectedProxy.clients" :key="client.id"><td>{{ client.name }}</td><td><n-tag :type="clientStatusTagType(client.status)" size="small">{{ clientStatusLabel(client.status) }}</n-tag></td><td>{{ clientTrafficUsageLabel(client.metrics, client.traffic_limit_bytes) }}</td><td>{{ clientTrafficCycleLabel(client.traffic_reset_mode, client.traffic_reset_weekday, client.traffic_reset_day, client.traffic_reset_time) }}</td><td>{{ formatClientExpiration(client.expires_at) }}</td><td>{{ client.metrics?.last_activity_at ? formatTime(client.metrics.last_activity_at) : '—' }}</td><td v-if="showsVLESSClientFields(selectedProxy.protocol)">{{ client.client_udp443 ? '开启' : '关闭' }}</td><td class="server-actions"><n-button size="tiny" secondary @click="copyClientURI(client)">{{ copiedClientID === client.id ? '已复制' : '复制链接' }}</n-button><n-button size="tiny" secondary @click="showClientQRCode(client)">二维码</n-button><n-button size="tiny" secondary @click="showClient(client)">查看</n-button><n-button size="tiny" secondary @click="openEditClient(client)">编辑</n-button><n-button size="tiny" secondary @click="toggleClient(client)">{{ client.enabled ? '禁用' : '启用' }}</n-button><n-button size="tiny" type="error" secondary @click="removeClient(client)">删除</n-button></td></tr></tbody>
        </table>
      </div>
      </template>
</template>
