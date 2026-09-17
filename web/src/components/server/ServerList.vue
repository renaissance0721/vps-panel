<script setup lang="ts">
import {
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
                  <tr v-for="value in servers" :key="value.id">
                    <td class="reorder-cell">
                      <div class="reorder-controls">
                        <n-button size="tiny" quaternary aria-label="上移服务器" :disabled="serverReorderingID !== null || servers[0]?.id === value.id" @click="reorderServer(value, 'up')">↑</n-button>
                        <n-button size="tiny" quaternary aria-label="下移服务器" :disabled="serverReorderingID !== null || servers[servers.length - 1]?.id === value.id" @click="reorderServer(value, 'down')">↓</n-button>
                      </div>
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
                  <tr v-for="value in archivedServers" :key="value.id">
                    <td class="reorder-cell">
                      <div class="reorder-controls">
                        <n-button size="tiny" quaternary aria-label="上移已移除服务器" :disabled="serverReorderingID !== null || archivedServers[0]?.id === value.id" @click="reorderServer(value, 'up')">↑</n-button>
                        <n-button size="tiny" quaternary aria-label="下移已移除服务器" :disabled="serverReorderingID !== null || archivedServers[archivedServers.length - 1]?.id === value.id" @click="reorderServer(value, 'down')">↓</n-button>
                      </div>
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
