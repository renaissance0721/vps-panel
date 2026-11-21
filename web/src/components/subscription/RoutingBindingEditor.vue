<script setup lang="ts">
import { onUnmounted, computed, ref, watch } from 'vue'
import { NButton, NSelect, NTag } from 'naive-ui'
import type { RoutingGroup } from './RoutingGroupEditor.vue'
import { beginDragPreview, endDragPreview } from '../../drag'

export type RoutingBindings = Record<string, number[]>
export type RoutingBindingNode = { id: number; name: string }
export type ShadowrocketPolicyTarget =
  | { type: 'node'; node_id: number }
  | { type: 'group'; group_key: string }
  | { type: 'builtin'; policy: 'DIRECT' | 'REJECT' }
export type ShadowrocketDefaults = Record<string, ShadowrocketPolicyTarget>
export type ShadowrocketPolicyOptions = Record<string, { target: ShadowrocketPolicyTarget; name: string }[]>

const props = withDefaults(defineProps<{
  modelValue: RoutingBindings
  groups: RoutingGroup[]
  nodes: RoutingBindingNode[]
  defaultCollapsed?: boolean
  resetKey?: number
  shadowrocketDefaults?: ShadowrocketDefaults
  shadowrocketOptions?: ShadowrocketPolicyOptions | null
  shadowrocketOptionsError?: string
}>(), {
  defaultCollapsed: false,
  resetKey: 0,
})
const emit = defineEmits<{
  'update:modelValue': [value: RoutingBindings]
  'expanded-change': [expanded: boolean]
  'shadowrocket-default-change': [key: string, target: ShadowrocketPolicyTarget | null]
}>()

const editorExpanded = ref(!props.defaultCollapsed)
const expandedGroupKeys = ref<Set<string>>(new Set())
const dragged = ref<{ groupKey: string; nodeID: number } | null>(null)
const dropTarget = ref<{ groupKey: string; nodeID: number } | null>(null)
const nodeByID = computed(() => new Map(props.nodes.map((node) => [node.id, node])))
const removedDefaultKeys = computed(() => {
  const groupKeys = new Set(props.groups.map(group => group.key))
  return Object.keys(props.shadowrocketDefaults ?? {}).filter(key => !groupKeys.has(key))
})
const policyChoices = computed(() => Object.fromEntries(props.groups.map(group => {
  const available = (props.shadowrocketOptions?.[group.key] ?? []).map(({ target, name }) => ({
    value: policyKey(target), label: policyLabel(name), target,
  }))
  const saved = props.shadowrocketDefaults?.[group.key]
  const unavailable = Boolean(saved && props.shadowrocketOptions && !available.some(option => option.value === policyKey(saved)))
  const options = [{ value: '', label: '不指定（使用客户端默认行为）' }, ...available]
  if (saved && !available.some(option => option.value === policyKey(saved))) {
    options.push({ value: policyKey(saved), label: unavailable ? `${savedPolicyLabel(saved)}（暂不可用）` : savedPolicyLabel(saved) })
  }
  return [group.key, { options, available, unavailable, value: saved ? policyKey(saved) : '' }]
})))

function policyKey(target: ShadowrocketPolicyTarget) {
  switch (target.type) {
    case 'node': return `node:${target.node_id}`
    case 'group': return `group:${target.group_key}`
    case 'builtin': return `builtin:${target.policy}`
  }
}

function policyLabel(name: string) {
  if (name === 'DIRECT') return 'DIRECT（直连）'
  if (name === 'REJECT') return 'REJECT（拒绝连接）'
  if (name === 'PROXY') return 'PROXY（首页当前节点）'
  return name
}

function savedPolicyLabel(target: ShadowrocketPolicyTarget) {
  switch (target.type) {
    case 'node': return nodeByID.value.get(target.node_id)?.name ?? `节点 #${target.node_id}`
    case 'group': return props.groups.find(group => group.key === target.group_key)?.name ?? '已保存的策略组'
    case 'builtin': return policyLabel(target.policy)
  }
}

function updateShadowrocketDefault(groupKey: string, value: string) {
  if (!value) {
    emit('shadowrocket-default-change', groupKey, null)
    return
  }
  const choice = policyChoices.value[groupKey]?.available.find(option => option.value === value)
  if (choice) emit('shadowrocket-default-change', groupKey, choice.target)
}

watch(() => props.resetKey, () => {
  editorExpanded.value = !props.defaultCollapsed
  expandedGroupKeys.value = new Set()
})

function selectedIDs(groupKey: string) {
  return (props.modelValue[groupKey] ?? []).filter((id) => nodeByID.value.has(id))
}

function updateGroup(groupKey: string, ids: number[]) {
  emit('update:modelValue', { ...props.modelValue, [groupKey]: ids })
}

function toggleGroup(groupKey: string) {
  const expanded = new Set(expandedGroupKeys.value)
  if (expanded.has(groupKey)) expanded.delete(groupKey)
  else expanded.add(groupKey)
  expandedGroupKeys.value = expanded
}

function bindingSummary(group: RoutingGroup) {
  const count = selectedIDs(group.key).length
  if (count === 0) return '未指定'
  return group.include_all ? `${count} 个优先节点` : `${count} 个节点`
}

function toggleNode(groupKey: string, nodeID: number, checked: boolean) {
  const current = selectedIDs(groupKey)
  updateGroup(groupKey, checked
    ? [...current.filter((id) => id !== nodeID), nodeID]
    : current.filter((id) => id !== nodeID))
}

function startDrag(event: DragEvent, groupKey: string, nodeID: number) {
  const source = (event.currentTarget as HTMLElement | null)?.closest('.routing-binding-selected-row') as HTMLElement | null
  if (!source || !beginDragPreview(event, source, `${groupKey}:${nodeID}`)) return
  dragged.value = { groupKey, nodeID }
}

function endDrag() {
  endDragPreview()
  dragged.value = null
  dropTarget.value = null
}

function dragOver(event: DragEvent, groupKey: string, nodeID: number) {
  if (!dragged.value || dragged.value.groupKey !== groupKey || dragged.value.nodeID === nodeID) return
  event.preventDefault()
  dropTarget.value = { groupKey, nodeID }
}

function drop(groupKey: string, nodeID: number) {
  const source = dragged.value
  endDrag()
  if (!source || source.groupKey !== groupKey || source.nodeID === nodeID) return
  const ids = selectedIDs(groupKey)
  const oldIndex = ids.indexOf(source.nodeID)
  const newIndex = ids.indexOf(nodeID)
  if (oldIndex < 0 || newIndex < 0) return
  const [value] = ids.splice(oldIndex, 1)
  ids.splice(newIndex, 0, value)
  updateGroup(groupKey, ids)
}
onUnmounted(() => { if (dragged.value !== null) endDrag() })
</script>

<template>
  <section class="subscription-node-picker routing-binding-editor">
    <div class="collapsible-section-header">
      <strong>指定策略组节点（可选）</strong>
      <n-button size="tiny" secondary attr-type="button" :aria-expanded="editorExpanded" @click="editorExpanded = !editorExpanded; emit('expanded-change', editorExpanded)">
        {{ editorExpanded ? '收起' : '展开' }}
      </n-button>
    </div>
    <div v-if="editorExpanded" class="routing-binding-editor-body">
      <p class="form-help">指定的节点会排在策略组最前，拖动已选节点可调整顺序。</p>
      <template v-if="shadowrocketDefaults">
        <p class="form-help">Shadowrocket 默认策略独立于绑定顺序。客户端是否沿用上次手动选择，由客户端决定。机场整源展开的节点需单独添加后才能指定。</p>
        <div v-if="removedDefaultKeys.length" class="collapsible-section-header">
          <span class="form-help">{{ removedDefaultKeys.length }} 项默认策略所属的策略组已移除。</span>
          <n-button size="tiny" secondary attr-type="button" @click="removedDefaultKeys.forEach(key => emit('shadowrocket-default-change', key, null))">清除</n-button>
        </div>
        <p v-if="shadowrocketOptionsError" class="form-help" role="alert">{{ shadowrocketOptionsError }}</p>
        <p v-else-if="!shadowrocketOptions" class="form-help" role="status">正在解析可选策略…</p>
      </template>
      <div v-for="group in groups" :key="group.key" class="routing-binding-group">
        <div class="routing-binding-group-summary">
          <div class="routing-binding-group-name">
            <strong>{{ group.name }}</strong>
            <n-tag v-if="group.include_all" type="default" size="small">包含全部节点</n-tag>
          </div>
          <span class="form-help">{{ bindingSummary(group) }}</span>
          <n-button size="tiny" secondary attr-type="button" :aria-expanded="expandedGroupKeys.has(group.key)" @click="toggleGroup(group.key)">
            {{ expandedGroupKeys.has(group.key) ? '收起' : '展开' }}
          </n-button>
        </div>
        <label v-if="shadowrocketDefaults" class="routing-binding-default-policy">
          <span>Shadowrocket 默认策略</span>
          <n-select
            size="small"
            :aria-label="`${group.name} 的 Shadowrocket 默认策略`"
            :options="policyChoices[group.key].options"
            :value="policyChoices[group.key].value"
            :status="policyChoices[group.key].unavailable ? 'warning' : undefined"
            @update:value="updateShadowrocketDefault(group.key, $event)"
          />
        </label>
        <p v-if="shadowrocketDefaults && group.name === 'PROXY'" class="form-help">原生 PROXY 跟随客户端首页选择。</p>
        <div v-if="expandedGroupKeys.has(group.key)" class="routing-binding-group-content">
          <span v-if="nodes.length === 0" class="form-help">当前订阅没有可绑定节点</span>
          <div v-if="selectedIDs(group.key).length" class="routing-binding-selected-list">
            <strong class="routing-binding-order-title">已选节点顺序</strong>
            <div
              v-for="nodeID in selectedIDs(group.key)"
              :key="nodeID"
              class="routing-binding-selected-row"
              :class="{ 'routing-binding-dragging': dragged?.groupKey === group.key && dragged.nodeID === nodeID, 'routing-binding-drop-target': dropTarget?.groupKey === group.key && dropTarget.nodeID === nodeID }"
              @dragover="dragOver($event, group.key, nodeID)"
              @dragleave="dropTarget?.groupKey === group.key && dropTarget.nodeID === nodeID && (dropTarget = null)"
              @drop.prevent="drop(group.key, nodeID)"
            >
              <span class="drag-handle" draggable="true" aria-label="拖动绑定节点排序" title="拖动排序" @dragstart="startDrag($event, group.key, nodeID)" @dragend="endDrag"><span></span><span></span><span></span></span>
              <span>{{ nodeByID.get(nodeID)?.name }}</span>
            </div>
          </div>
          <label v-for="node in nodes" :key="node.id" class="subscription-node-option">
            <input type="checkbox" :checked="selectedIDs(group.key).includes(node.id)" @change="toggleNode(group.key, node.id, ($event.target as HTMLInputElement).checked)" />
            <span>{{ node.name }}</span>
          </label>
        </div>
      </div>
    </div>
  </section>
</template>
