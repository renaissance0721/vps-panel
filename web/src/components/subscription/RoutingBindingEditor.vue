<script setup lang="ts">
import { computed, ref } from 'vue'
import type { RoutingGroup } from './RoutingGroupEditor.vue'
import { beginDragPreview, endDragPreview } from '../../drag'

export type RoutingBindings = Record<string, number[]>
export type RoutingBindingNode = { id: number; name: string }

const props = defineProps<{ modelValue: RoutingBindings; groups: RoutingGroup[]; nodes: RoutingBindingNode[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: RoutingBindings] }>()

const dragged = ref<{ groupKey: string; nodeID: number } | null>(null)
const dropTarget = ref<{ groupKey: string; nodeID: number } | null>(null)
const nodeByID = computed(() => new Map(props.nodes.map((node) => [node.id, node])))

function selectedIDs(groupKey: string) {
  return (props.modelValue[groupKey] ?? []).filter((id) => nodeByID.value.has(id))
}

function updateGroup(groupKey: string, ids: number[]) {
  emit('update:modelValue', { ...props.modelValue, [groupKey]: ids })
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
</script>

<template>
  <fieldset class="subscription-node-picker routing-binding-editor">
    <legend>策略组节点绑定</legend>
    <p class="form-help">节点按绑定顺序加入策略组；开启“包含订阅全部节点”时，其余节点随后补入。</p>
    <div v-for="group in groups" :key="group.key" class="routing-binding-group">
      <strong>{{ group.name }}</strong>
      <span v-if="nodes.length === 0" class="form-help">当前订阅没有可绑定节点</span>
      <label v-for="node in nodes" :key="node.id" class="subscription-node-option">
        <input type="checkbox" :checked="selectedIDs(group.key).includes(node.id)" @change="toggleNode(group.key, node.id, ($event.target as HTMLInputElement).checked)" />
        <span>{{ node.name }}</span>
      </label>
      <div v-if="selectedIDs(group.key).length" class="routing-binding-selected-list">
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
  </fieldset>
</template>
