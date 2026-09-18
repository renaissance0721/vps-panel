<script setup lang="ts">
import {
  ref,
  toRefs,
} from 'vue'
import {
  NCard,
  NEmpty,
  NButton,
  NTag,
} from 'naive-ui'
import type {
  ServersViewState,
} from '../../composables/useServers'

const props = defineProps<{
  model: Pick<ServersViewState,
    | 'serverListMode'
    | 'servers'
    | 'serverReorderingID'
    | 'reorderServer'
    | 'visibilityLabel'
    | 'statusType'
    | 'statusLabel'
    | 'trafficWarningLevel'
    | 'trafficUsageLabel'
    | 'formatServerExpiration'
    | 'submitting'
    | 'viewServer'
    | 'archiveServer'
    | 'archivedServers'
    | 'formatTime'
  >
}>()
const {
  serverListMode,
  servers,
  serverReorderingID,
  reorderServer,
  visibilityLabel,
  statusType,
  statusLabel,
  trafficWarningLevel,
  trafficUsageLabel,
  formatServerExpiration,
  submitting,
  viewServer,
  archiveServer,
  archivedServers,
  formatTime,
} = toRefs(props.model)
const draggedID = ref<number | null>(null)
const dropTargetID = ref<number | null>(null)
const draggedArchived = ref(false)

function startDrag(event: DragEvent, id: number, archived: boolean) {
  if (serverReorderingID.value !== null || !event.dataTransfer) return
  draggedID.value = id
  draggedArchived.value = archived
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', String(id))
}

function endDrag() {
  draggedID.value = null
  dropTargetID.value = null
}

function dragOver(event: DragEvent, id: number, archived: boolean) {
  if (draggedID.value === null || draggedArchived.value !== archived || draggedID.value === id) return
  event.preventDefault()
  dropTargetID.value = id
}

async function dropServer(id: number, archived: boolean) {
  const sourceID = draggedID.value
  if (sourceID === null || draggedArchived.value !== archived) return
  const source = (archived ? archivedServers.value : servers.value).find((row) => row.id === sourceID)
  endDrag()
  if (source) await reorderServer.value(source, id)
}
</script>

<template>
<n-card v-if="serverListMode === 'active'" title="正常服务器" :bordered="true">
            <n-empty v-if="servers.length === 0" description="当前没有服务器" />
            <div v-else class="server-table-wrap">
              <table class="server-table">
                <thead>
                  <tr>
                    <th class="reorder-cell" aria-label="排序"></th>
                    <th>名称</th>
                    <th>状态</th>
                    <th>本周期流量</th>
                    <th>到期时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="value in servers" :key="value.id" :class="{ 'row-dragging': draggedID === value.id, 'row-drop-target': dropTargetID === value.id }" @dragover="dragOver($event, value.id, false)" @dragleave="dropTargetID === value.id && (dropTargetID = null)" @drop.prevent="dropServer(value.id, false)">
                    <td class="reorder-cell">
                      <span class="drag-handle" :class="{ 'drag-handle--disabled': serverReorderingID !== null }" :draggable="serverReorderingID === null" title="拖动排序" @dragstart="startDrag($event, value.id, false)" @dragend="endDrag"><span></span><span></span><span></span></span>
                    </td>
                    <td>
                      {{ value.name }}
                      <n-tag :type="value.visibility === 'private' ? 'warning' : 'default'" size="small">
                        {{ visibilityLabel(value.visibility) }}
                      </n-tag>
                    </td>
                    <td>
                      <div class="server-status-tags">
                        <n-tag :type="statusType(value.status)" size="small">
                          {{ statusLabel(value.status) }}
                        </n-tag>
                        <n-tag
                          v-if="trafficWarningLevel(value.traffic_used_bytes, value.monthly_traffic_limit_bytes) === 'warning'"
                          type="warning"
                          size="small"
                        >
                          流量预警
                        </n-tag>
                        <n-tag
                          v-else-if="trafficWarningLevel(value.traffic_used_bytes, value.monthly_traffic_limit_bytes) === 'exhausted'"
                          type="error"
                          size="small"
                        >
                          流量已用完
                        </n-tag>
                      </div>
                    </td>
                    <td>{{ trafficUsageLabel(value) }}</td>
                    <td>{{ formatServerExpiration(value.expires_at) }}</td>
                    <td class="server-actions">
                      <n-button
                        size="small"
                        secondary
                        :disabled="submitting"
                        @click="viewServer(value)"
                      >
                        查看
                      </n-button>
                      <n-button
                        size="small"
                        type="error"
                        secondary
                        :disabled="submitting"
                        @click="archiveServer(value)"
                      >
                        移除
                      </n-button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </n-card>

          <n-card v-if="serverListMode === 'archived'" title="已移除服务器" :bordered="true">
            <n-empty v-if="archivedServers.length === 0" description="当前没有已移除服务器" />
            <div v-else class="server-table-wrap">
              <table class="server-table">
                <thead>
                  <tr>
                    <th class="reorder-cell" aria-label="排序"></th>
                    <th>名称</th>
                    <th>移除时间</th>
                    <th>创建时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="value in archivedServers" :key="value.id" :class="{ 'row-dragging': draggedID === value.id, 'row-drop-target': dropTargetID === value.id }" @dragover="dragOver($event, value.id, true)" @dragleave="dropTargetID === value.id && (dropTargetID = null)" @drop.prevent="dropServer(value.id, true)">
                    <td class="reorder-cell">
                      <span class="drag-handle" :class="{ 'drag-handle--disabled': serverReorderingID !== null }" :draggable="serverReorderingID === null" title="拖动排序" @dragstart="startDrag($event, value.id, true)" @dragend="endDrag"><span></span><span></span><span></span></span>
                    </td>
                    <td>
                      {{ value.name }}
                      <n-tag :type="value.visibility === 'private' ? 'warning' : 'default'" size="small">
                        {{ visibilityLabel(value.visibility) }}
                      </n-tag>
                    </td>
                    <td>{{ value.archived_at ? formatTime(value.archived_at) : '—' }}</td>
                    <td>{{ formatTime(value.created_at) }}</td>
                    <td class="server-actions">
                      <n-button
                        size="small"
                        secondary
                        :disabled="submitting"
                        @click="viewServer(value)"
                      >
                        查看
                      </n-button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </n-card>
</template>
