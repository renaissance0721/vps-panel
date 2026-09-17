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
    | 'proxyServerID'
    | 'servers'
    | 'proxyProtocol'
    | 'proxyPort'
    | 'proxyEntryHostMode'
    | 'proxyEntryHost'
    | 'selectedServerPublicIPv4'
    | 'proxySecurity'
    | 'proxyServerName'
    | 'proxyEnabled'
    | 'firstClientName'
    | 'firstClientUDP443'
  >
}>()
const {
  proxyFormOpen,
  submitting,
  proxyFormMode,
  saveProxy,
  proxyName,
  proxyServerID,
  servers,
  proxyProtocol,
  proxyPort,
  proxyEntryHostMode,
  proxyEntryHost,
  selectedServerPublicIPv4,
  proxySecurity,
  proxyServerName,
  proxyEnabled,
  firstClientName,
  firstClientUDP443,
} = toRefs(props.model)
</script>

<template>
<n-modal v-model:show="proxyFormOpen" :mask-closable="!submitting">
    <n-card class="proxy-form-card" :title="proxyFormMode === 'create' ? '新增代理节点' : '编辑代理节点'" :bordered="false" closable @close="proxyFormOpen = false">
      <form class="proxy-form" @submit.prevent="saveProxy">
        <label><span>名称</span><n-input v-model:value="proxyName" maxlength="100" /></label>
        <label>
          <span>服务器</span>
          <select v-model.number="proxyServerID" class="settings-input" :disabled="proxyFormMode === 'edit'">
            <option v-for="server in servers" :key="server.id" :value="server.id">{{ server.name }}</option>
          </select>
        </label>
		<label v-if="proxyFormMode === 'create'">
			<span>协议</span>
			<select v-model="proxyProtocol" class="settings-input">
				<option value="vless">VLESS</option><option value="shadowsocks">Shadowsocks</option>
			</select>
		</label>
		<div v-else class="fixed-fields"><span>协议：{{ proxyProtocol === 'vless' ? 'VLESS' : 'Shadowsocks' }}</span></div>
        <label><span>监听端口</span><input v-model.number="proxyPort" class="settings-input" type="number" min="1" max="65535" /></label>
        <label>
          <span>入口地址模式</span>
          <select v-model="proxyEntryHostMode" class="settings-input">
            <option value="auto">自动检测</option><option value="manual">手动输入</option>
          </select>
        </label>
        <label v-if="proxyEntryHostMode === 'manual'"><span>入口 IP / 域名</span><n-input v-model:value="proxyEntryHost" placeholder="例如：1.2.3.4 或 jp.example.com" /></label>
        <p v-else>自动使用服务器公网 IPv4。当前公网 IPv4：{{ selectedServerPublicIPv4 || '未检测到' }}</p>
		<div v-if="proxyProtocol === 'vless'" class="fixed-fields"><span>传输：TCP</span><span>流控：XTLS Vision</span></div>
		<template v-if="proxyProtocol === 'vless'">
		<label>
          <span>安全层</span>
          <select v-model="proxySecurity" class="settings-input">
            <option value="reality">REALITY</option><option value="tls">TLS</option>
          </select>
        </label>
        <label><span>SNI / Server Name</span><n-input v-model:value="proxyServerName" placeholder="例如：www.example.com" /></label>
        <TLSForm v-if="proxySecurity === 'tls'" :model="model" />
		<RealityForm v-else :model="model" />
		</template>
		<ShadowsocksForm v-else :model="model" />
        <label class="checkbox-row"><input v-model="proxyEnabled" type="checkbox" /><span>启用代理节点</span></label>
        <fieldset v-if="proxyFormMode === 'create'" class="client-fieldset">
          <legend>首个客户端</legend>
          <label><span>客户端名称</span><n-input v-model:value="firstClientName" maxlength="100" /></label>
			<p>客户端凭据由系统安全生成。</p>
			<label v-if="proxyProtocol === 'vless'" class="checkbox-row"><input v-model="firstClientUDP443" type="checkbox" /><span>允许 UDP/443 / QUIC</span></label>
        </fieldset>
        <div class="modal-actions"><n-button @click="proxyFormOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button></div>
      </form>
    </n-card>
  </n-modal>
</template>
