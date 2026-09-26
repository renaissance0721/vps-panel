<script setup lang="ts">
import {
  toRefs,
} from 'vue'
import {
  NButton,
} from 'naive-ui'
import type {
  ServersViewState,
} from '../composables/useServers'
import ServerForm from '../components/server/ServerForm.vue'
import ServerList from '../components/server/ServerList.vue'
import ServerDetail from '../components/server/ServerDetail.vue'
import ServerAccessForm from '../components/server/ServerAccessForm.vue'
import ServerNameForm from '../components/server/ServerNameForm.vue'
import ServerOwnerForm from '../components/server/ServerOwnerForm.vue'
import ServerExpirationForm from '../components/server/ServerExpirationForm.vue'
import ServerTrafficForm from '../components/server/ServerTrafficForm.vue'
import ServerTrafficAdjustmentForm from '../components/server/ServerTrafficAdjustmentForm.vue'
import AgentBulkUpgradeModal from '../components/server/AgentBulkUpgradeModal.vue'
const props = defineProps<{ model: ServersViewState; active: boolean }>()
const {
  serverListMode,
  state,
  panelReleaseVersion,
  bulkUpgradeCandidates,
  bulkUpgradeProgress,
  bulkUpgradeHasBatch,
  openBulkAgentUpgrade,
} = toRefs(props.model)
</script>

<template>
<template v-if="active">
          <div class="admin-nav server-list-nav" aria-label="服务器列表">
            <n-button
              size="small"
              :type="serverListMode === 'active' ? 'primary' : 'default'"
              :secondary="serverListMode === 'active'"
              @click="serverListMode = 'active'"
            >
              正常服务器
            </n-button>
            <n-button
              size="small"
              :type="serverListMode === 'archived' ? 'primary' : 'default'"
              :secondary="serverListMode === 'archived'"
              @click="serverListMode = 'archived'"
            >
              已移除
            </n-button>
            <n-button
              v-if="state?.user?.role === 'admin' && serverListMode === 'active'"
              size="small"
              type="primary"
              :secondary="!bulkUpgradeHasBatch"
              :disabled="!bulkUpgradeHasBatch && (!panelReleaseVersion || bulkUpgradeCandidates.length === 0)"
              @click="openBulkAgentUpgrade"
            >
              <template v-if="bulkUpgradeHasBatch">
                升级进度 {{ bulkUpgradeProgress.handledCount }} / {{ bulkUpgradeProgress.total }}
              </template>
              <template v-else-if="!panelReleaseVersion">开发版本不可批量升级</template>
              <template v-else-if="bulkUpgradeCandidates.length === 0">暂无可升级 Agent</template>
              <template v-else>一键升级 Agent（{{ bulkUpgradeCandidates.length }}）</template>
            </n-button>
          </div>

          <ServerForm :model="model" /><ServerList :model="model" /></template>
<ServerDetail :model="model" />
<ServerAccessForm :model="model" />
<ServerNameForm :model="model" />
<ServerOwnerForm :model="model" />
<ServerExpirationForm :model="model" />
<ServerTrafficForm :model="model" />
<ServerTrafficAdjustmentForm :model="model" />
<AgentBulkUpgradeModal :model="model" />
</template>
