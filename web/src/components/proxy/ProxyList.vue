<script setup lang="ts">
import {
  ref,
  toRefs,
} from 'vue'
import {
  NAlert,
  NInput,
  NButton,
  NCard,
  NSpin,
  NEmpty,
  NTag,
} from 'naive-ui'
import type {
  ProxiesViewState,
} from '../../composables/useProxies'
import { proxyAddressLines } from '../../proxy'

const props = defineProps<{
  model: Pick<ProxiesViewState,
    | 'error'
    | 'search'
    | 'servers'
    | 'openCreateProxy'
    | 'loading'
    | 'filteredProxies'
    | 'reorderingID'
    | 'proxies'
    | 'reorderProxy'
    | 'proxyListProtocolFields'
    | 'showProxy'
    | 'openEditProxy'
    | 'toggleProxy'
    | 'removeProxy'
  >
}>()
const {
  error,
  search,
  servers,
  openCreateProxy,
  loading,
  filteredProxies,
  reorderingID,
  proxies,
  reorderProxy,
  proxyListProtocolFields,
  showProxy,
  openEditProxy,
  toggleProxy,
  removeProxy,
} = toRefs(props.model)
const draggedID = ref<number | null>(null)
const dropTargetID = ref<number | null>(null)

function startDrag(event: DragEvent, id: number) {
  if (reorderingID.value !== null || search.value.trim() || !event.dataTransfer) return
  draggedID.value = id
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', String(id))
}

function endDrag() {
  draggedID.value = null
  dropTargetID.value = null
}

function dragOver(event: DragEvent, id: number) {
  if (draggedID.value === null || draggedID.value === id || search.value.trim()) return
  event.preventDefault()
  dropTargetID.value = id
}

async function dropProxy(id: number) {
  const source = proxies.value.find((row) => row.id === draggedID.value)
  endDrag()
  if (source && !search.value.trim()) await reorderProxy.value(source, id)
}
</script>

<template>
<n-alert v-if="error" class="page-alert" type="error" closable @close="error = ''">
    {{ error }}
  </n-alert>

  <div class="proxy-toolbar">
    <n-input v-model:value="search" clearable placeholder="搜索名称、服务器、入口地址或安全层" />
    <n-button type="primary" :disabled="servers.length === 0" @click="openCreateProxy">
      新增代理节点
    </n-button>
  </div>

  <n-card :bordered="true">
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载代理节点…</span></div>
    <n-empty v-else-if="filteredProxies.length === 0" description="当前没有代理节点" />
    <div v-else class="server-table-wrap">
      <table class="server-table proxy-table">
        <thead>
          <tr>
            <th class="reorder-cell" aria-label="排序"></th>
            <th>名称</th><th>服务器</th><th>IP / 地址</th><th>端口</th>
            <th>协议</th><th>传输</th><th>安全层</th><th>流控</th><th>状态</th><th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="value in filteredProxies" :key="value.id" :class="{ 'row-dragging': draggedID === value.id, 'row-drop-target': dropTargetID === value.id }" @dragover="dragOver($event, value.id)" @dragleave="dropTargetID === value.id && (dropTargetID = null)" @drop.prevent="dropProxy(value.id)">
            <td class="reorder-cell">
              <span class="drag-handle" :class="{ 'drag-handle--disabled': reorderingID !== null || !!search.trim() }" :title="search.trim() ? '清除搜索后可调整顺序' : '拖动排序'" :draggable="reorderingID === null && !search.trim()" aria-label="拖动代理节点排序" @dragstart="startDrag($event, value.id)" @dragend="endDrag"><span></span><span></span><span></span></span>
            </td>
            <td>{{ value.name }}</td>
            <td>{{ value.server_name }}</td>
            <td>
              <span>{{ proxyAddressLines(value)[0] }}</span>
              <small v-if="proxyAddressLines(value)[1]" class="secondary-text">{{ proxyAddressLines(value)[1] }}</small>
            </td>
            <td>{{ value.listen_port }}</td>
			<td>{{ proxyListProtocolFields(value.protocol, value.config.security).protocol }}</td>
			<td>{{ proxyListProtocolFields(value.protocol, value.config.security).transport }}</td>
			<td>{{ proxyListProtocolFields(value.protocol, value.config.security).security }}</td>
			<td>{{ proxyListProtocolFields(value.protocol, value.config.security).flow }}</td>
            <td><n-tag :type="value.enabled ? 'success' : 'default'" size="small">{{ value.enabled ? '启用' : '禁用' }}</n-tag></td>
            <td class="server-actions">
              <n-button size="small" secondary @click="showProxy(value.id)">查看</n-button>
              <n-button size="small" secondary @click="openEditProxy(value)">编辑</n-button>
              <n-button size="small" secondary @click="toggleProxy(value)">{{ value.enabled ? '禁用' : '启用' }}</n-button>
              <n-button size="small" type="error" secondary @click="removeProxy(value)">删除</n-button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </n-card>
</template>
