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
    | 'proxyRealityTarget'
  >
}>()
const {
  proxyRealityTarget,
} = toRefs(props.model)
</script>

<template>
<label><span>REALITY 目标地址</span><n-input v-model:value="proxyRealityTarget" placeholder="例如：www.example.com:443" /></label>
</template>
