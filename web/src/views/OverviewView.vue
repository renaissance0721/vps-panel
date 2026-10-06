<script setup lang="ts">
import {
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
import { userRoleLabel } from '../format'

const props = defineProps<{ model: Pick<OverviewViewState, 'overview' | 'isHealthy' | 'health' | 'state' | 'submitting' | 'createInvitation' | 'generatedLink' | 'invitationRole' | 'copyInvitation' | 'copied' | 'invitations' | 'formatTime' | 'revokeInvitation' | 'passwordChangeRequests' | 'reviewPasswordChangeRequest' | 'backupFile' | 'backupBusy' | 'backupStatus' | 'selectBackup' | 'exportBackup' | 'importBackup'> }>()
const { overview, isHealthy, health, state, submitting, createInvitation, generatedLink, invitationRole, copyInvitation, copied, invitations, formatTime, revokeInvitation, passwordChangeRequests, reviewPasswordChangeRequest, backupFile, backupBusy, backupStatus, selectBackup, exportBackup, importBackup } = toRefs(props.model)
</script>

<template>
  <div class="overview-page">
    <div class="overview-summary-grid">
            <n-card title="已注册账号" :bordered="true" class="overview-summary-card">
              <strong class="overview-summary-number">{{ overview?.users.length ?? '—' }}</strong>
            </n-card>
            <n-card title="服务器" :bordered="true" class="overview-summary-card">
              <span class="overview-summary-caption">当前账号可访问</span>
              <strong class="overview-summary-number">{{ overview?.server_count ?? '—' }}</strong>
            </n-card>
            <n-card title="代理节点" :bordered="true" class="overview-summary-card">
              <span class="overview-summary-caption">当前账号可访问</span>
              <strong class="overview-summary-number">{{ overview?.proxy_count ?? '—' }}</strong>
            </n-card>
            <n-card title="配置待同步" :bordered="true" class="overview-summary-card">
              <span class="overview-summary-caption">已下发或等待 Agent 确认</span>
              <strong class="overview-summary-number">{{ overview?.pending_operation_count ?? '—' }}</strong>
            </n-card>
            <n-card title="配置失败" :bordered="true" class="overview-summary-card">
              <span class="overview-summary-caption">需要检查的变更</span>
              <strong class="overview-summary-number">{{ overview?.failed_operation_count ?? '—' }}</strong>
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

            <n-card v-if="state?.user?.role === 'admin'" title="邀请账号" :bordered="true">
              <p class="card-copy">生成 24 小时有效的一次性注册链接。</p>
			  <div class="invitation-form">
				<label><span>账号等级</span><select v-model="invitationRole" class="settings-input"><option value="vip">VIP用户</option><option value="carpool">拼车用户</option><option value="subscriber">订阅用户</option></select></label>
                <n-button type="primary" :loading="submitting" @click="createInvitation">
                  生成邀请链接
                </n-button>
			  </div>
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
                    角色：{{ userRoleLabel(invitation.role) }} · {{ invitation.created_by_username }} 创建 ·
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
		  <n-card v-if="state?.user?.role === 'admin'" title="密码重置申请" :bordered="true">
			<n-empty v-if="passwordChangeRequests.length === 0" description="当前没有待审核申请" />
			<div v-else class="invitation-list">
			  <div v-for="request in passwordChangeRequests" :key="request.id" class="invitation-row">
				<div><strong>{{ request.username }}</strong><span>{{ userRoleLabel(request.role) }} · {{ formatTime(request.created_at) }} 提交</span></div>
				<div class="modal-actions"><n-tag type="warning" size="small">待审核</n-tag><n-button size="small" type="error" secondary :disabled="submitting" @click="reviewPasswordChangeRequest(request.id, 'reject')">拒绝</n-button><n-button size="small" type="primary" :disabled="submitting" @click="reviewPasswordChangeRequest(request.id, 'approve')">批准</n-button></div>
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
  </div>
</template>
