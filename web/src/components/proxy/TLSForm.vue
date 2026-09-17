<script setup lang="ts">
import {
  toRefs,
} from 'vue'
import {
  NInput,
} from 'naive-ui'
import type {
  ProxiesViewState,
} from '../../composables/useProxies'

const props = defineProps<{
  model: Pick<ProxiesViewState,
    | 'proxyTLSMode'
    | 'proxyCertificate'
    | 'proxyFormMode'
    | 'proxyPrivateKey'
  >
}>()
const {
  proxyTLSMode,
  proxyCertificate,
  proxyFormMode,
  proxyPrivateKey,
} = toRefs(props.model)
</script>

<template>
<label><span>TLS 证书来源</span><select v-model="proxyTLSMode" class="settings-input"><option value="acme">自动申请（推荐）</option><option value="manual">手动证书</option></select></label>
          <p v-if="proxyTLSMode === 'acme'">Agent 将使用 Let's Encrypt 自动申请和续期证书。请确保域名已解析到当前服务器，并允许公网访问 TCP 80。</p>
          <template v-else>
            <label><span>证书 PEM</span><n-input v-model:value="proxyCertificate" type="textarea" :autosize="{ minRows: 4 }" :placeholder="proxyFormMode === 'edit' ? '留空则保留现有证书' : '粘贴完整证书 PEM'" /></label>
            <label><span>私钥 PEM</span><n-input v-model:value="proxyPrivateKey" type="textarea" :autosize="{ minRows: 4 }" :placeholder="proxyFormMode === 'edit' ? '留空则保留现有私钥' : '粘贴匹配的私钥 PEM'" /></label>
          </template>
</template>
