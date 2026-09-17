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
const props = defineProps<{ servers: ServerOption[] }>()
const model = reactive(useProxies(props))
</script>

<template>
<ProxyList :model="model" />
<ProxyForm :model="model" />
<ProxyDetail :model="model" />
<ClientForm :model="model" />
<ClientDetail :model="model" />
</template>
