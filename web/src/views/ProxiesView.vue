<script setup lang="ts">
import {
  reactive,
} from 'vue'
import type {
  ServerOption,
} from '../types/proxy'
import {
  useProxies,
} from '../composables/useProxies'
import ProxyList from '../components/proxy/ProxyList.vue'
import ProxyForm from '../components/proxy/ProxyForm.vue'
import ProxyDetail from '../components/proxy/ProxyDetail.vue'
import ClientForm from '../components/proxy/ClientForm.vue'
import ClientDetail from '../components/proxy/ClientDetail.vue'
import ExternalNodeManager from '../components/proxy/ExternalNodeManager.vue'
import QRCodeModal from '../components/share/QRCodeModal.vue'
const props = defineProps<{ servers: ServerOption[] }>()
const model = reactive(useProxies(props))
</script>

<template>
<ProxyList :model="model" />
<ExternalNodeManager />
<ProxyForm :model="model" />
<ProxyDetail :model="model" />
<ClientForm :model="model" />
<ClientDetail :model="model" />
<QRCodeModal :show="model.qrOpen" :uri="model.qrURI" :title="model.qrTitle" :subtitle="model.qrSubtitle" @update:show="model.setQRCodeOpen" />
</template>
