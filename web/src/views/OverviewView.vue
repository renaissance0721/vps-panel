<script setup lang="ts">
import {
  computed,
  toRefs,
} from 'vue'
import {
  NCard,
  NTag,
  NButton,
  NInput,
  NEmpty,
} from 'naive-ui'
import type {
  OverviewViewState,
} from '../composables/useOverview'
import { adminFirst } from '../adminFirst'

const props = defineProps<{ model: Pick<OverviewViewState, 'overview' | 'isHealthy' | 'health' | 'state' | 'submitting' | 'createInvitation' | 'generatedLink' | 'copyInvitation' | 'copied' | 'invitations' | 'formatTime' | 'revokeInvitation' | 'backupFile' | 'backupBusy' | 'backupStatus' | 'selectBackup' | 'exportBackup' | 'importBackup'> }>()
const { overview, isHealthy, health, state, submitting, createInvitation, generatedLink, copyInvitation, copied, invitations, formatTime, revokeInvitation, backupFile, backupBusy, backupStatus, selectBackup, exportBackup, importBackup } = toRefs(props.model)
const orderedUsers = computed(() => adminFirst(overview.value?.users ?? []))
</script>

<template>
<div class="overview-summary-grid">
            <n-card title="已注册账号" :bordered="true" class="overview-summary-card">
              <strong class="overview-summary-number">{{ overview?.users.length ?? '—' }}</strong>
              <div class="overview-users">
                <div v-for="account in orderedUsers" :key="account.username" class="overview-user">
                  <span>{{ account.username }}</span>
                  <n-tag :type="account.role === 'admin' ? 'info' : 'default'" size="small">{{ account.role }}</n-tag>
                </div>
              </div>
            </n-card>
            <n-card title="服务器" :bordered="true" class="overview-summary-card">
              <span class="overview-summary-caption">当前账号可访问</span>
              <strong class="overview-summary-number">{{ overview?.server_count ?? '—' }}</strong>
            </n-card>
            <n-card title="代理节点" :bordered="true" class="overview-summary-card">
              <span class="overview-summary-caption">当前账号可访问</span>
              <strong class="overview-summary-number">{{ overview?.proxy_count ?? '—' }}</strong>
            </n-card>
          </div>
          <div class="dashboard-grid">
            <n-card title="运行状态" :bordered="true">
              <template #header-extra>
                <n-tag v-if="isHealthy" type="success" round>运行正常</n-tag>
              </template>
              <div class="health-grid">
                <div>
                  <span>后端服务</span>
                  <strong>{{ health?.status === 'ok' ? '正常' : '不可用' }}</strong>
                </div>
                <div>
                  <span>SQLite</span>
                  <strong>{{ health?.database === 'ok' ? '已连接' : '不可用' }}</strong>
                </div>
              </div>
            </n-card>

            <n-card v-if="state?.user?.role === 'admin'" title="邀请 VIP 账号" :bordered="true">
              <p class="card-copy">生成 24 小时有效的一次性 VIP 注册链接。</p>
              <n-button type="primary" :loading="submitting" @click="createInvitation">
                生成邀请链接
              </n-button>
              <div v-if="generatedLink" class="generated-link">
                <strong>请立即保存，此链接不会再次显示</strong>
                <n-input :value="generatedLink" readonly />
                <n-button secondary @click="copyInvitation">
                  {{ copied ? '已复制' : '复制链接' }}
                </n-button>
              </div>
            </n-card>
          </div>

          <n-card v-if="state?.user?.role === 'admin'" title="有效邀请" :bordered="true">
            <n-empty v-if="invitations.length === 0" description="当前没有未过期的邀请" />
            <div v-else class="invitation-list">
              <div v-for="invitation in invitations" :key="invitation.id" class="invitation-row">
                <div>
                  <strong>邀请 #{{ invitation.id }}</strong>
                  <span>
                    {{ invitation.created_by_username }} 创建 ·
                    {{ formatTime(invitation.expires_at) }} 过期
                  </span>
                </div>
                <n-button
                  type="error"
                  secondary
                  size="small"
                  :disabled="submitting"
                  @click="revokeInvitation(invitation.id)"
                >
                  撤销
                </n-button>
              </div>
            </div>
          </n-card>
          <n-card v-if="state?.user?.role === 'admin'" title="备份与恢复" :bordered="true">
            <h3>整站备份</h3>
            <p class="card-copy">导出的 ZIP 包含 Panel 数据、账号、服务器、Agent 身份、代理节点、中转、客户端、流量和权限等敏感信息，请妥善保管。</p>
            <n-button type="primary" :loading="backupBusy" @click="exportBackup">导出备份</n-button>
            <h3>恢复备份</h3>
            <input type="file" accept=".zip,application/zip" :disabled="backupBusy" @change="selectBackup" />
            <p v-if="backupFile">已选择：{{ backupFile.name }}</p>
            <n-button type="error" :disabled="!backupFile || backupBusy" :loading="backupBusy" @click="importBackup">导入并恢复</n-button>
            <p v-if="backupStatus" role="status">{{ backupStatus }}</p>
          </n-card>
</template>
