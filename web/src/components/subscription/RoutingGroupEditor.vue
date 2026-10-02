<script setup lang="ts">
import { computed, ref } from 'vue'
import { NButton, NInput, NSwitch } from 'naive-ui'
import { beginDragPreview, endDragPreview } from '../../drag'

export type RoutingGroup = {
  key: string
  name: string
  type: string
  proxies: string[]
  include_all?: boolean
}

const props = withDefaults(defineProps<{ modelValue: RoutingGroup[]; rules?: string[]; readonly?: boolean }>(), {
  rules: () => [],
  readonly: false,
})
const emit = defineEmits<{
  'update:modelValue': [value: RoutingGroup[]]
  'update:rules': [value: string[]]
  'validation-error': [message: string]
}>()

const groups = computed(() => props.modelValue)
const draggedGroupIndex = ref<number | null>(null)
const groupDropTargetIndex = ref<number | null>(null)

function cloneGroups() {
  return props.modelValue.map((group) => ({ ...group, proxies: [...group.proxies] }))
}

function update(mutator: (values: RoutingGroup[]) => void) {
  const values = cloneGroups()
  mutator(values)
  emit('update:modelValue', values)
}

function addGroup() {
  update((values) => values.push({ key: '', name: `新分组 ${values.length + 1}`, type: 'select', proxies: [] }))
}

function rulePolicyIndex(parts: string[]) {
  if (parts[0] === 'MATCH') return parts.length > 1 ? 1 : -1
  if (parts[0] === 'RULE-SET') return parts.length > 2 ? 2 : -1
  if (parts.length < 2) return -1
  return parts[parts.length - 1] === 'no-resolve' ? parts.length - 2 : parts.length - 1
}

function ruleUsesGroup(rule: string, name: string) {
  const parts = rule.split(',').map((part) => part.trim())
  const index = rulePolicyIndex(parts)
  return index >= 0 && parts[index] === name
}

function removeGroup(index: number) {
  const target = props.modelValue[index]
  if (!target) return
  const references = props.modelValue
    .filter((group, candidate) => candidate !== index && group.proxies.includes(target.name))
    .map((group) => `策略组“${group.name}”`)
  props.rules.forEach((rule, ruleIndex) => {
    if (ruleUsesGroup(rule, target.name)) references.push(`第 ${ruleIndex + 1} 条 Rule`)
  })
  if (references.length > 0) {
    emit('validation-error', `策略组“${target.name}”仍被 ${references.join('、')} 引用，请先解除引用`)
    return
  }
  update((values) => values.splice(index, 1))
}

function setName(index: number, name: string) {
  const oldName = props.modelValue[index]?.name
  if (oldName === undefined || oldName === name) return
  update((values) => {
    values[index].name = name
    for (const [candidate, group] of values.entries()) {
      if (candidate === index) continue
      group.proxies = group.proxies.map((proxy) => proxy === oldName ? name : proxy)
    }
  })
  emit('update:rules', props.rules.map((rule) => {
    const parts = rule.split(',').map((part) => part.trim())
    const policyIndex = rulePolicyIndex(parts)
    if (policyIndex >= 0 && parts[policyIndex] === oldName) parts[policyIndex] = name
    return parts.join(',')
  }))
}

function setIncludeAll(index: number, value: boolean) {
  update((values) => { values[index].include_all = value })
}

function addProxy(index: number, value: string) {
  if (!value) return
  update((values) => {
    if (!values[index].proxies.includes(value)) values[index].proxies.push(value)
  })
}

function setProxy(groupIndex: number, proxyIndex: number, value: string) {
  update((values) => { values[groupIndex].proxies[proxyIndex] = value })
}

function removeProxy(groupIndex: number, proxyIndex: number) {
  update((values) => { values[groupIndex].proxies.splice(proxyIndex, 1) })
}

function moveProxy(groupIndex: number, proxyIndex: number, direction: -1 | 1) {
  const target = proxyIndex + direction
  if (target < 0 || target >= props.modelValue[groupIndex].proxies.length) return
  update((values) => {
    const proxies = values[groupIndex].proxies
    ;[proxies[proxyIndex], proxies[target]] = [proxies[target], proxies[proxyIndex]]
  })
}

function firstGroupReference(index: number) {
  return props.modelValue.find((_, candidate) => candidate !== index)?.name ?? ''
}

function startGroupDrag(event: DragEvent, index: number) {
  const source = (event.currentTarget as HTMLElement | null)?.closest('.routing-group-card') as HTMLElement | null
  if (!source || !beginDragPreview(event, source, String(index))) return
  draggedGroupIndex.value = index
}

function endGroupDrag() {
  endDragPreview()
  draggedGroupIndex.value = null
  groupDropTargetIndex.value = null
}

function dragOverGroup(event: DragEvent, index: number) {
  if (draggedGroupIndex.value === null || draggedGroupIndex.value === index) return
  event.preventDefault()
  groupDropTargetIndex.value = index
}

function dropGroup(index: number) {
  const sourceIndex = draggedGroupIndex.value
  endGroupDrag()
  if (sourceIndex === null || sourceIndex === index) return
  update((values) => {
    const [group] = values.splice(sourceIndex, 1)
    values.splice(index, 0, group)
  })
}
</script>

<template>
  <div class="routing-group-editor">
    <div
      v-for="(group, groupIndex) in groups"
      :key="group.key || `new-${groupIndex}`"
      class="routing-group-card"
      :class="{ 'routing-group-dragging': draggedGroupIndex === groupIndex, 'routing-group-drop-target': groupDropTargetIndex === groupIndex }"
      @dragover="dragOverGroup($event, groupIndex)"
      @dragleave="groupDropTargetIndex === groupIndex && (groupDropTargetIndex = null)"
      @drop.prevent="dropGroup(groupIndex)"
    >
      <div class="routing-group-heading">
        <span v-if="!readonly" class="drag-handle" draggable="true" aria-label="拖动策略组排序" title="拖动排序" @dragstart="startGroupDrag($event, groupIndex)" @dragend="endGroupDrag"><span></span><span></span><span></span></span>
        <n-input :value="group.name" :readonly="readonly" placeholder="分组名称" @update:value="setName(groupIndex, $event)" />
        <span class="form-help">{{ group.type }}</span>
        <n-button v-if="!readonly" size="tiny" type="error" secondary @click="removeGroup(groupIndex)">删除</n-button>
      </div>
      <div v-if="!readonly" class="switch-row"><span>包含订阅全部节点</span><n-switch :value="Boolean(group.include_all)" @update:value="setIncludeAll(groupIndex, $event)" /></div>
      <div class="routing-member-list">
        <div v-for="(proxy, proxyIndex) in group.proxies" :key="proxyIndex" class="routing-member-row">
          <span v-if="readonly">{{ proxy }}</span>
          <template v-else>
            <select :value="proxy" class="settings-input" @change="setProxy(groupIndex, proxyIndex, ($event.target as HTMLSelectElement).value)">
              <option value="DIRECT">DIRECT</option><option value="REJECT">REJECT</option>
              <option v-for="(target, targetIndex) in groups" v-show="targetIndex !== groupIndex" :key="target.key || targetIndex" :value="target.name">{{ target.name }}</option>
            </select>
            <n-button size="tiny" secondary @click="moveProxy(groupIndex, proxyIndex, -1)">上移</n-button>
            <n-button size="tiny" secondary @click="moveProxy(groupIndex, proxyIndex, 1)">下移</n-button>
            <n-button size="tiny" type="error" secondary @click="removeProxy(groupIndex, proxyIndex)">移除</n-button>
          </template>
        </div>
        <div v-if="readonly && group.include_all" class="routing-member-row"><span>全部订阅节点</span></div>
      </div>
      <div v-if="!readonly" class="modal-actions routing-add-actions">
        <n-button size="small" secondary @click="addProxy(groupIndex, 'DIRECT')">添加 DIRECT</n-button>
        <n-button size="small" secondary @click="addProxy(groupIndex, 'REJECT')">添加 REJECT</n-button>
        <n-button size="small" secondary :disabled="!firstGroupReference(groupIndex)" @click="addProxy(groupIndex, firstGroupReference(groupIndex))">添加分组引用</n-button>
      </div>
    </div>
    <n-button v-if="!readonly" secondary @click="addGroup">新增策略组</n-button>
  </div>
</template>
