<script lang="ts">
const flagAssets = import.meta.glob<string>('../../../node_modules/flag-icons/flags/4x3/[a-z][a-z].svg', {
  eager: true,
  query: '?no-inline',
  import: 'default',
})
const flags = new Map(
  Object.entries(flagAssets)
    .map(([path, source]) => [path.match(/\/([a-z]{2})\.svg$/)?.[1]?.toUpperCase(), source] as const)
    .filter((entry): entry is readonly [string, string] => Boolean(entry[0]) && entry[0] !== 'XX'),
)
</script>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ countryCode?: string | null }>()
const normalizedCode = computed(() => {
  const value = props.countryCode?.trim() ?? ''
  if (!/^[a-z]{2}$/i.test(value)) return ''
  const code = value.toUpperCase()
  return flags.has(code) ? code : ''
})
const source = computed(() => flags.get(normalizedCode.value))
</script>

<template>
  <img v-if="source" class="country-flag" :src="source" alt="" aria-hidden="true" :data-country-code="normalizedCode">
</template>

<style scoped>
.country-flag {
  width: 18px;
  height: 13.5px;
  flex: none;
  object-fit: cover;
  vertical-align: -2px;
}
</style>
