<script setup lang="ts">
import {
  toRefs,
} from 'vue'

import type {
  ProxiesViewState,
} from '../../composables/useProxies'

const props = defineProps<{
  model: Pick<ProxiesViewState,
    | 'proxyMethod'
    | 'proxyFormMode'
    | 'shadowsocksMethods'
  >
}>()
const {
  proxyMethod,
  proxyFormMode,
  shadowsocksMethods,
} = toRefs(props.model)
</script>

<template>
<label>
				<span>加密方法</span>
				<select v-model="proxyMethod" class="settings-input" :disabled="proxyFormMode === 'edit'">
					<option v-for="method in shadowsocksMethods" :key="method" :value="method">{{ method }}</option>
				</select>
			</label>
			<div class="fixed-fields"><span>网络：TCP + UDP</span></div>
</template>
