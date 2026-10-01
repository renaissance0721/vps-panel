<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NInput, NSwitch } from 'naive-ui'

export type RoutingGroup = {
  name: string
  type: string
  proxies: string[]
  node_ids?: number[]
  include_all?: boolean
}

type PublishedNodeOption = { id: number; name: string }

const props = withDefaults(defineProps<{ modelValue: RoutingGroup[]; nodes: PublishedNodeOption[]; readonly?: boolean }>(), {
  readonly: false,
})
const emit = defineEmits<{ 'update:modelValue': [value: RoutingGroup[]] }>()

const groups = computed(() => props.modelValue)

function update(mutator: (values: RoutingGroup[]) => void) {
  const values = props.modelValue.map((group) => ({
    ...group,
    proxies: [...group.proxies],
    node_ids: [...(group.node_ids ?? [])],
  }))
  mutator(values)
  emit('update:modelValue', values)
}

function addGroup() {
  update((values) => values.push({ name: `新分组 ${values.length + 1}`, type: 'select', proxies: [], node_ids: [] }))
}

function removeGroup(index: number) {
  update((values) => values.splice(index, 1))
}

function moveGroup(index: number, direction: -1 | 1) {
  const target = index + direction
  if (target < 0 || target >= props.modelValue.length) return
  update((values) => { [values[index], values[target]] = [values[target], values[index]] })
}

function setName(index: number, name: string) {
  update((values) => { values[index].name = name })
}

function setIncludeAll(index: number, value: boolean) {
  update((values) => { values[index].include_all = value })
}

function toggleNode(index: number, nodeID: number, checked: boolean) {
  update((values) => {
    const ids = values[index].node_ids ?? []
    values[index].node_ids = checked
      ? [...ids.filter((id) => id !== nodeID), nodeID]
      : ids.filter((id) => id !== nodeID)
  })
}

function moveNode(groupIndex: number, nodeID: number, direction: -1 | 1) {
  const ids = props.modelValue[groupIndex].node_ids ?? []
  const index = ids.indexOf(nodeID)
  const target = index + direction
  if (index < 0 || target < 0 || target >= ids.length) return
  update((values) => {
    const nodeIDs = values[groupIndex].node_ids ?? []
    ;[nodeIDs[index], nodeIDs[target]] = [nodeIDs[target], nodeIDs[index]]
    values[groupIndex].node_ids = nodeIDs
  })
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

function orderedNodes(group: RoutingGroup) {
  const byID = new Map(props.nodes.map((node) => [node.id, node]))
  const selected = (group.node_ids ?? []).flatMap((id) => {
    const node = byID.get(id)
    return node ? [node] : []
  })
  const selectedIDs = new Set(selected.map((node) => node.id))
  return [...selected, ...props.nodes.filter((node) => !selectedIDs.has(node.id))]
}

function nodeLabel(nodeID: number) {
  return props.nodes.find((node) => node.id === nodeID)?.name ?? `节点 #${nodeID}`
}
</script>

<template>
  <div class="routing-group-editor">
    <div v-for="(group, groupIndex) in groups" :key="groupIndex" class="routing-group-card">
      <div class="routing-group-heading">
        <n-input :value="group.name" :readonly="readonly" placeholder="分组名称" @update:value="setName(groupIndex, $event)" />
        <span class="form-help">{{ group.type }}</span>
        <template v-if="!readonly">
          <n-button size="tiny" secondary @click="moveGroup(groupIndex, -1)">上移</n-button>
          <n-button size="tiny" secondary @click="moveGroup(groupIndex, 1)">下移</n-button>
          <n-button size="tiny" type="error" secondary @click="removeGroup(groupIndex)">删除</n-button>
        </template>
      </div>
      <div v-if="!readonly" class="switch-row"><span>包含订阅全部节点</span><n-switch :value="Boolean(group.include_all)" @update:value="setIncludeAll(groupIndex, $event)" /></div>
      <fieldset v-if="!readonly" class="subscription-node-picker"><legend>指定 Published Node</legend>
        <span v-if="nodes.length === 0" class="form-help">暂无发布节点</span>
        <label v-for="node in orderedNodes(group)" :key="node.id" class="subscription-node-option">
          <input type="checkbox" :checked="(group.node_ids ?? []).includes(node.id)" @change="toggleNode(groupIndex, node.id, ($event.target as HTMLInputElement).checked)" />
          <span>{{ node.name }}</span>
          <template v-if="(group.node_ids ?? []).includes(node.id)"><n-button size="tiny" secondary @click.prevent="moveNode(groupIndex, node.id, -1)">上移</n-button><n-button size="tiny" secondary @click.prevent="moveNode(groupIndex, node.id, 1)">下移</n-button></template>
        </label>
      </fieldset>
      <div class="routing-member-list">
        <div v-for="(proxy, proxyIndex) in group.proxies" :key="proxyIndex" class="routing-member-row">
          <span v-if="readonly">{{ proxy }}</span>
          <template v-else>
            <select :value="proxy" class="settings-input" @change="setProxy(groupIndex, proxyIndex, ($event.target as HTMLSelectElement).value)">
              <option value="DIRECT">DIRECT</option><option value="REJECT">REJECT</option>
              <option v-for="(target, targetIndex) in groups" v-show="targetIndex !== groupIndex" :key="targetIndex" :value="target.name">{{ target.name }}</option>
            </select>
            <n-button size="tiny" secondary @click="moveProxy(groupIndex, proxyIndex, -1)">上移</n-button>
            <n-button size="tiny" secondary @click="moveProxy(groupIndex, proxyIndex, 1)">下移</n-button>
            <n-button size="tiny" type="error" secondary @click="removeProxy(groupIndex, proxyIndex)">移除</n-button>
          </template>
        </div>
        <div v-for="nodeID in readonly ? (group.node_ids ?? []) : []" :key="`node-${nodeID}`" class="routing-member-row"><span>{{ nodeLabel(nodeID) }}</span></div>
        <div v-if="readonly && group.include_all" class="routing-member-row"><span>全部订阅节点（&#123;&#123;all&#125;&#125;）</span></div>
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
