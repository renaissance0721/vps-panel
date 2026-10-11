<script setup lang="ts">
import {
  toRefs,
} from 'vue'
import {
  NModal,
  NCard,
  NInput,
  NButton,
  NAlert,
  NSwitch,
} from 'naive-ui'
import type {
  ProxiesViewState,
} from '../../composables/useProxies'
import TLSForm from './TLSForm.vue'
import RealityForm from './RealityForm.vue'
import ShadowsocksForm from './ShadowsocksForm.vue'
type ProtocolForms = InstanceType<typeof TLSForm>['$props']['model'] & InstanceType<typeof RealityForm>['$props']['model'] & InstanceType<typeof ShadowsocksForm>['$props']['model']
const props = defineProps<{
  model: ProtocolForms & Pick<ProxiesViewState,
    | 'proxyFormOpen'
    | 'submitting'
    | 'proxyFormMode'
    | 'saveProxy'
    | 'proxyName'
    | 'proxyNodeRole'
    | 'proxyRoleOnlyUpdate'
    | 'proxyServerID'
    | 'servers'
    | 'proxyProtocol'
    | 'proxyPort'
	| 'proxyListenFamily'
    | 'proxyEntryHostMode'
    | 'proxyEntryHost'
	| 'selectedServerPublicAddress'
	| 'selectedServerSupportsIPv6Listener'
	| 'selectedServerHasAutoPublicIPv6'
	| 'selectedServerIPv6State'
	| 'selectedServerIPv6Status'
    | 'selectedServerBoundDomain'
    | 'onProxyServerChange'
	| 'onProxyFamilyChange'
    | 'proxySecurity'
    | 'proxyServerName'
    | 'proxyEnabled'
    | 'firstClientName'
    | 'firstClientUDP443'
    | 'proxyVLESSSupported'
    | 'proxyVLESSRealitySupported'
    | 'proxyTLSSupported'
    | 'proxyShadowsocksSupported'
    | 'proxyCapabilityWarning'
  >
}>()
const {
  proxyFormOpen,
  submitting,
  proxyFormMode,
  saveProxy,
  proxyName,
  proxyNodeRole,
  proxyRoleOnlyUpdate,
  proxyServerID,
  servers,
  proxyProtocol,
  proxyPort,
	proxyListenFamily,
  proxyEntryHostMode,
  proxyEntryHost,
	selectedServerPublicAddress,
	selectedServerSupportsIPv6Listener,
	selectedServerHasAutoPublicIPv6,
	selectedServerIPv6State,
	selectedServerIPv6Status,
  selectedServerBoundDomain,
  onProxyServerChange,
	onProxyFamilyChange,
  proxySecurity,
  proxyServerName,
  proxyEnabled,
  firstClientName,
  firstClientUDP443,
  proxyVLESSSupported,
  proxyVLESSRealitySupported,
  proxyTLSSupported,
  proxyShadowsocksSupported,
  proxyCapabilityWarning,
} = toRefs(props.model)
</script>

<template>
<n-modal v-model:show="proxyFormOpen" :mask-closable="!submitting">
    <n-card class="proxy-form-card" :title="proxyFormMode === 'create' ? '新增代理节点' : '编辑代理节点'" :bordered="false" closable @close="proxyFormOpen = false">
      <form class="proxy-form" @submit.prevent="saveProxy">
        <label><span>名称</span><n-input v-model:value="proxyName" maxlength="100" /></label>
        <label><span>节点用途</span><select v-model="proxyNodeRole" class="settings-input"><option value="direct">直连节点</option><option value="landing">落地节点</option></select></label>
        <label>
          <span>服务器</span>
          <select v-model.number="proxyServerID" class="settings-input" :disabled="proxyFormMode === 'edit'" @change="onProxyServerChange">
            <option v-for="server in servers" :key="server.id" :value="server.id">{{ server.name }}</option>
          </select>
        </label>
		<label v-if="proxyFormMode === 'create'">
			<span>协议</span>
			<select v-model="proxyProtocol" class="settings-input">
				<option value="vless" :disabled="!proxyVLESSSupported">VLESS</option><option value="shadowsocks" :disabled="!proxyShadowsocksSupported">Shadowsocks</option>
			</select>
		</label>
		<div v-else class="fixed-fields"><span>协议：{{ proxyProtocol === 'vless' ? 'VLESS' : 'Shadowsocks' }}</span></div>
		<label>
		  <span>监听 IP 类型</span>
		  <select v-model="proxyListenFamily" class="settings-input" @change="onProxyFamilyChange">
			<option value="ipv4">IPv4</option><option value="ipv6" :disabled="!selectedServerSupportsIPv6Listener">IPv6</option>
		  </select>
		</label>
		<n-alert v-if="proxyListenFamily === 'ipv6'" :type="selectedServerIPv6State === 'none' ? 'warning' : 'info'">{{ selectedServerIPv6Status }}</n-alert>
        <label><span>监听端口</span><input v-model.number="proxyPort" class="settings-input" type="number" min="1" max="65535" /></label>
        <label>
          <span>入口地址模式</span>
          <select v-model="proxyEntryHostMode" class="settings-input">
            <option v-if="selectedServerBoundDomain" value="bound">已绑定域名：{{ selectedServerBoundDomain }}</option>
			<option value="auto" :disabled="proxyListenFamily === 'ipv6' && !selectedServerHasAutoPublicIPv6">自动检测公网 {{ proxyListenFamily === 'ipv6' ? 'IPv6' : 'IPv4' }}</option><option value="manual">手动输入</option>
          </select>
        </label>
		<n-alert v-if="proxyListenFamily === 'ipv6' && !selectedServerHasAutoPublicIPv6" type="warning">无法自动检测公网 IPv6，请使用绑定域名或手动填写。</n-alert>
        <label v-if="proxyEntryHostMode === 'manual'"><span>入口 IP / 域名</span><n-input v-model:value="proxyEntryHost" placeholder="例如：1.2.3.4 或 jp.example.com" /></label>
        <p v-else-if="proxyEntryHostMode === 'bound'">使用服务器绑定域名：{{ selectedServerBoundDomain }}</p>
		<p v-else>自动使用服务器公网 {{ proxyListenFamily === 'ipv6' ? 'IPv6' : 'IPv4' }}。当前地址：{{ selectedServerPublicAddress || '未检测到' }}</p>
		<div v-if="proxyProtocol === 'vless'" class="fixed-fields"><span>传输：TCP</span><span>流控：XTLS Vision</span></div>
		<template v-if="proxyProtocol === 'vless'">
		<label>
          <span>安全层</span>
          <select v-model="proxySecurity" class="settings-input">
			<option value="reality" :disabled="!proxyVLESSRealitySupported">REALITY</option><option value="tls" :disabled="!proxyTLSSupported">TLS</option>
          </select>
        </label>
        <label><span>SNI / Server Name</span><n-input v-model:value="proxyServerName" placeholder="例如：www.example.com" /></label>
        <TLSForm v-if="proxySecurity === 'tls'" :model="model" />
		<RealityForm v-else :model="model" />
		</template>
		<ShadowsocksForm v-else :model="model" />
		<n-alert v-if="proxyCapabilityWarning" type="warning">{{ proxyCapabilityWarning }}</n-alert>
        <div class="switch-row"><span>启用代理节点</span><n-switch v-model:value="proxyEnabled" /></div>
        <fieldset v-if="proxyFormMode === 'create'" class="client-fieldset">
          <legend>首个客户端</legend>
          <label><span>客户端名称</span><n-input v-model:value="firstClientName" maxlength="100" /></label>
			<p>客户端凭据由系统安全生成。</p>
			<div v-if="proxyProtocol === 'vless'" class="switch-row"><span>允许 UDP/443 / QUIC</span><n-switch v-model:value="firstClientUDP443" /></div>
        </fieldset>
        <div class="modal-actions"><n-button @click="proxyFormOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="submitting" :disabled="!proxyRoleOnlyUpdate && proxyEnabled && !!proxyCapabilityWarning">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>
</template>
