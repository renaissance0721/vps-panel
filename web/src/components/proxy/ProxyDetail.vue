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
  ProxiesViewState,
} from '../../composables/useProxies'
import ClientList from './ClientList.vue'
type ClientListModel = InstanceType<typeof ClientList>['$props']['model']
const props = defineProps<{
  model: ClientListModel & Pick<ProxiesViewState,
    | 'selectedProxy'
    | 'proxyDetailOpen'
    | 'openEditProxy'
  >
}>()
const {
  selectedProxy,
  proxyDetailOpen,
  openEditProxy,
} = toRefs(props.model)
</script>

<template>
<n-modal v-if="selectedProxy" v-model:show="proxyDetailOpen">
    <n-card class="proxy-detail-card" title="代理节点详情" :bordered="false" closable @close="proxyDetailOpen = false">
      <h3>基础</h3>
      <dl class="server-details">
        <div><dt>名称</dt><dd>{{ selectedProxy.name }}</dd></div><div><dt>服务器</dt><dd>{{ selectedProxy.server_name }}</dd></div>
        <div><dt>入口模式</dt><dd>{{ selectedProxy.entry_host_mode === 'auto' ? '自动检测' : '手动输入' }}</dd></div><div><dt>入口地址</dt><dd>{{ selectedProxy.entry_address || '未检测' }}</dd></div>
        <div><dt>监听地址</dt><dd>0.0.0.0:{{ selectedProxy.listen_port }}</dd></div><div><dt>状态</dt><dd>{{ selectedProxy.enabled ? '启用' : '禁用' }}</dd></div>
      </dl>
      <h3>协议</h3>
		<dl v-if="selectedProxy.protocol === 'vless'" class="server-details"><div><dt>协议</dt><dd>VLESS</dd></div><div><dt>传输</dt><dd>TCP</dd></div><div><dt>流控</dt><dd>XTLS Vision</dd></div><div><dt>安全层</dt><dd>{{ selectedProxy.config.security === 'tls' ? 'TLS' : 'REALITY' }}</dd></div></dl>
		<dl v-else class="server-details"><div><dt>协议</dt><dd>Shadowsocks</dd></div><div><dt>加密方法</dt><dd>{{ selectedProxy.config.method }}</dd></div><div><dt>网络</dt><dd>TCP + UDP</dd></div></dl>
		<h3 v-if="selectedProxy.protocol === 'vless'">{{ selectedProxy.config.security === 'tls' ? 'TLS' : 'REALITY' }}</h3>
		<dl v-if="selectedProxy.protocol === 'vless'" class="server-details">
        <div><dt>SNI</dt><dd>{{ selectedProxy.config.server_name }}</dd></div>
        <div><dt>指纹</dt><dd>{{ selectedProxy.config.fingerprint }}</dd></div>
		<div v-if="selectedProxy.config.security === 'tls'"><dt>证书来源</dt><dd>{{ selectedProxy.config.tls_mode === 'acme' ? '自动 ACME' : '手动证书' }}</dd></div>
		<div v-if="selectedProxy.config.security === 'tls'"><dt>证书</dt><dd>{{ selectedProxy.config.tls_mode === 'acme' ? 'Agent 自动管理' : (selectedProxy.config.tls_certificate_configured ? '已配置' : '未配置') }}</dd></div>
		<template v-else><div><dt>目标地址</dt><dd>{{ selectedProxy.config.reality_target }}</dd></div></template>
      </dl>
      <ClientList :model="model" />
<div class="modal-actions"><n-button secondary @click="openEditProxy(selectedProxy)">编辑节点</n-button><n-button @click="proxyDetailOpen = false">关闭</n-button></div>
    </n-card>
  </n-modal>
</template>
