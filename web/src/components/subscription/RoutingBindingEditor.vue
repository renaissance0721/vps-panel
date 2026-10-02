<script setup lang="ts">
import { onUnmounted, computed, ref, watch } from 'vue'
import { NButton, NTag } from 'naive-ui'
import type { RoutingGroup } from './RoutingGroupEditor.vue'
import { beginDragPreview, endDragPreview } from '../../drag'

export type RoutingBindings = Record<string, number[]>
export type RoutingBindingNode = { id: number; name: string }

const props = withDefaults(defineProps<{
  modelValue: RoutingBindings
  groups: RoutingGroup[]
  nodes: RoutingBindingNode[]
  defaultCollapsed?: boolean
  resetKey?: number
}>(), {
  defaultCollapsed: false,
  resetKey: 0,
})
const emit = defineEmits<{ 'update:modelValue': [value: RoutingBindings] }>()

const editorExpanded = ref(!props.defaultCollapsed)
const expandedGroupKeys = ref<Set<string>>(new Set())
const dragged = ref<{ groupKey: string; nodeID: number } | null>(null)
const dropTarget = ref<{ groupKey: string; nodeID: number } | null>(null)
const nodeByID = computed(() => new Map(props.nodes.map((node) => [node.id, node])))

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
      <n-button size="tiny" secondary attr-type="button" :aria-expanded="editorExpanded" @click="editorExpanded = !editorExpanded">
        {{ editorExpanded ? '收起' : '展开' }}
      </n-button>
    </div>
    <div v-if="editorExpanded" class="routing-binding-editor-body">
      <p class="form-help">用于为某些策略组额外指定当前订阅中的节点。开启“包含订阅全部节点”的策略组通常无需在这里选择。</p>
      <p class="form-help">如果策略组开启“包含订阅全部节点”，这里指定的节点会优先排在前面，其余节点随后补入。</p>
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
        <div v-if="expandedGroupKeys.has(group.key)" class="routing-binding-group-content">
          <span v-if="nodes.length === 0" class="form-help">当前订阅没有可绑定节点</span>
          <label v-for="node in nodes" :key="node.id" class="subscription-node-option">
            <input type="checkbox" :checked="selectedIDs(group.key).includes(node.id)" @change="toggleNode(group.key, node.id, ($event.target as HTMLInputElement).checked)" />
            <span>{{ node.name }}</span>
          </label>
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
        </div>
      </div>
    </div>
  </section>
</template>
