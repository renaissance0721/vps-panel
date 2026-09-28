<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NEmpty,
  NInput,
  NModal,
  NSpin,
  NTag,
} from 'naive-ui'

import { api } from '../api/client'
import { formatTime } from '../format'
import {
  clientStatusLabel,
  clientStatusTagType,
  clientTrafficCycleLabel,
  clientTrafficUsageLabel,
  formatClientExpiration,
} from '../proxy'
import { useUserManagement } from '../composables/useUserManagement'
import type { ManagedUserNode } from '../types/userManagement'
import type { AccessUser } from '../types/auth'

const model = useUserManagement()
const {
  users, selectedUserID, detail, assignedNodes, availableNodes, loading, submitting, error,
  formOpen, formMode, editingNode, createProxyID, clientName, clientEnabled, clientUDP443,
  trafficLimit, trafficLimitUnit, trafficResetMode, trafficResetWeekday,
  trafficResetDay, trafficResetTime, expirationMode, expiresAt, billingPeriodMonths,
  userRelayPortCount,
} = model

const viewedNode = ref<ManagedUserNode | null>(null)
const accounts = ref<AccessUser[]>([])
const accountBusy = ref(false)

async function loadAccounts() {
  accounts.value = (await api<{ users: AccessUser[] }>('/api/users')).users
}

async function loadPage() {
  await Promise.all([model.load(), loadAccounts()])
}

async function deleteAccount(account: AccessUser) {
  if (account.role === 'admin') return
  if (!window.confirm(`删除用户“${account.username}”后不可恢复，确定继续吗？`)) return
  if (window.prompt(`请输入用户名“${account.username}”再次确认删除`) !== account.username) {
    error.value = '用户名确认不匹配，已取消删除'
    return
  }
  accountBusy.value = true
  error.value = ''
  try {
    await api(`/api/admin/users/${account.id}`, { method: 'DELETE' })
    await loadPage()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '删除用户失败'
  } finally {
    accountBusy.value = false
  }
}

function roleLabel(role: AccessUser['role']) {
  return role === 'admin' ? '管理员' : role === 'vip' ? 'VIP' : role === 'subscriber' ? '订阅用户' : '普通用户'
}

function billingLabel(months: 1 | 3 | 6 | 12 | null) {
  return months === 1 ? '月付' : months === 3 ? '季付' : months === 6 ? '半年付' : months === 12 ? '年付' : '未设置'
}

function relayPortLabel(node: ManagedUserNode) {
  const client = node.client
  if (!client || client.user_relay_port_count === 0 || client.user_relay_port_start === null || client.user_relay_port_end === null) return '未分配'
  const range = client.user_relay_port_start === client.user_relay_port_end
    ? String(client.user_relay_port_start)
    : `${client.user_relay_port_start}–${client.user_relay_port_end}`
  return `${range}（${client.user_relay_port_count} 个）`
}

onMounted(loadPage)
</script>

<template>
  <div class="user-management">
    <h1>用户管理</h1>
    <n-card title="账号列表" :bordered="true">
      <div class="invitation-list">
        <div v-for="account in accounts" :key="account.id" class="invitation-row">
          <div><strong>{{ account.username }}</strong><span>{{ roleLabel(account.role) }}</span></div>
          <n-button v-if="account.role !== 'admin'" type="error" secondary size="small" :disabled="accountBusy" @click="deleteAccount(account)">删除用户</n-button>
        </div>
      </div>
    </n-card>
    <div class="relay-toolbar user-management-toolbar">
      <label>
        <span>普通用户</span>
        <select v-model.number="selectedUserID" class="settings-input" :disabled="loading" @change="model.selectUser">
          <option v-for="user in users" :key="user.id" :value="user.id">{{ user.username }}</option>
        </select>
      </label>
      <div class="relay-toolbar-actions">
        <n-button secondary :loading="loading" @click="model.load">刷新</n-button>
      </div>
    </div>

    <n-alert v-if="error" class="page-alert" type="error">{{ error }}</n-alert>
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载用户信息…</span></div>
    <n-empty v-else-if="users.length === 0" description="暂无普通用户" />

    <template v-else-if="detail">
      <n-card class="user-management-summary" size="small">
        <strong>{{ detail.user.username }} · 普通用户</strong>
        <span>已开通节点 {{ assignedNodes.length }} · 自建中转 {{ detail.relays.length }} · 密码申请 {{ detail.password_request ? '等待审核' : '无' }}</span>
      </n-card>

      <section>
        <div class="section-heading"><h2>客户端</h2><n-button type="primary" :disabled="availableNodes.length === 0" @click="model.openCreate">+ 新建客户端</n-button></div>
        <n-empty v-if="assignedNodes.length === 0" description="暂无已开通客户端" />
        <div v-else class="user-management-grid">
          <n-card v-for="node in assignedNodes" :key="node.proxy_id" size="small" :bordered="true">
            <template #header>{{ node.server_name }} · {{ node.proxy_name }}</template>
            <template #header-extra><n-tag :type="node.client ? clientStatusTagType(node.client.status) : 'default'" size="small">{{ node.protocol === 'vless' ? 'VLESS' : 'Shadowsocks' }} · {{ node.client ? clientStatusLabel(node.client.status) : '未知' }}</n-tag></template>
            <template v-if="node.client">
              <div class="user-management-status">
                <span>{{ node.client.name }}</span>
              </div>
              <dl class="user-details">
                <div><dt>流量</dt><dd>{{ clientTrafficUsageLabel(node.client.metrics, node.client.traffic_limit_bytes) }}</dd></div>
                <div><dt>重置</dt><dd>{{ clientTrafficCycleLabel(node.client.traffic_reset_mode, node.client.traffic_reset_weekday, node.client.traffic_reset_day, node.client.traffic_reset_time) }}</dd></div>
                <div><dt>到期</dt><dd>{{ formatClientExpiration(node.client.expires_at) }}</dd></div>
                <div><dt>付款周期</dt><dd>{{ billingLabel(node.client.billing_period_months) }}</dd></div>
                <div><dt>中转端口</dt><dd>{{ relayPortLabel(node) }}</dd></div>
              </dl>
              <div class="server-actions">
                <n-button size="small" secondary @click="viewedNode = node">查看</n-button>
                <n-button size="small" secondary @click="model.openEdit(node)">编辑</n-button>
                <n-button size="small" type="error" secondary :disabled="submitting" @click="model.removeNode(node)">删除</n-button>
              </div>
            </template>
          </n-card>
        </div>
      </section>

      <section>
        <div class="section-heading"><h2>用户中转</h2></div>
        <n-empty v-if="detail.relays.length === 0" description="暂无用户中转" />
        <div v-else class="user-management-grid">
          <n-card v-for="relay in detail.relays" :key="relay.id" size="small">
            <template #header>{{ relay.name }}</template>
            <p>{{ relay.source.server_name }} · {{ relay.source.proxy_name }}</p>
            <dl class="user-details">
              <div><dt>入口</dt><dd>{{ relay.entry_address }}</dd></div>
              <div><dt>落地</dt><dd>{{ relay.mode === 'assigned_node' && relay.target ? `${relay.target.server_name} · ${relay.target.proxy_name}` : '自定义落地' }}</dd></div>
            </dl>
            <n-button size="small" type="error" secondary :disabled="submitting" @click="model.removeRelay(relay.id)">删除中转</n-button>
          </n-card>
        </div>
      </section>

      <section>
        <div class="section-heading"><h2>密码修改申请</h2></div>
        <n-empty v-if="!detail.password_request" description="无待处理申请" />
        <n-card v-else size="small">
          <p>{{ detail.password_request.username }} · {{ formatTime(detail.password_request.created_at) }}</p>
          <div class="server-actions">
            <n-button type="error" secondary :disabled="submitting" @click="model.reviewPasswordRequest('reject')">拒绝</n-button>
            <n-button type="primary" :disabled="submitting" @click="model.reviewPasswordRequest('approve')">批准</n-button>
          </div>
        </n-card>
      </section>
    </template>

    <n-modal v-model:show="formOpen">
      <n-card class="client-form-card" :title="formMode === 'create' ? '新建客户端' : '编辑用户节点'" closable @close="formOpen = false">
        <form class="proxy-form" @submit.prevent="model.saveNode">
          <label v-if="formMode === 'create'"><span>代理节点</span><select v-model.number="createProxyID" class="settings-input" @change="model.selectCreateNode"><option v-for="node in availableNodes" :key="node.proxy_id" :value="node.proxy_id">{{ node.server_name }} · {{ node.proxy_name }}</option></select></label>
          <div v-else class="fixed-fields"><span>{{ editingNode?.server_name }}</span><span>{{ editingNode?.proxy_name }}</span><span>{{ editingNode?.protocol }}</span></div>
          <label><span>客户端名称</span><n-input v-model:value="clientName" /></label>
          <label><span>流量额度（留空为不限）</span><div class="user-management-inline"><n-input v-model:value="trafficLimit" placeholder="例如 100" /><select v-model="trafficLimitUnit" class="settings-input"><option value="G">GiB</option><option value="T">TiB</option></select></div></label>
          <label><span>流量重置</span><select v-model="trafficResetMode" class="settings-input"><option value="never">不重置</option><option value="daily">每日</option><option value="weekly">每周</option><option value="monthly">每月</option></select></label>
          <label v-if="trafficResetMode === 'weekly'"><span>星期（1–7）</span><input v-model.number="trafficResetWeekday" class="settings-input" type="number" min="1" max="7" /></label>
          <label v-if="trafficResetMode === 'monthly'"><span>日期（1–31）</span><input v-model.number="trafficResetDay" class="settings-input" type="number" min="1" max="31" /></label>
          <label v-if="trafficResetMode !== 'never'"><span>重置时间</span><input v-model="trafficResetTime" class="settings-input" type="time" /></label>
          <label><span>到期设置</span><select v-model="expirationMode" class="settings-input"><option value="unlimited">不限</option><option value="specified">指定时间</option></select></label>
          <label v-if="expirationMode === 'specified'"><span>到期时间</span><input v-model="expiresAt" class="settings-input" type="datetime-local" /></label>
          <label><span>付款周期</span><select v-model.number="billingPeriodMonths" class="settings-input"><option :value="0">未设置</option><option :value="1">1 个月</option><option :value="3">3 个月</option><option :value="6">6 个月</option><option :value="12">12 个月</option></select></label>
          <label><span>用户中转端口数量</span><select v-model.number="userRelayPortCount" class="settings-input"><option v-for="count in 6" :key="count - 1" :value="count - 1">{{ count - 1 }}</option></select><small class="secondary-text">从 20000–29999 随机分配连续端口；设为 0 表示不允许该客户端创建用户中转。</small></label>
          <n-checkbox v-model:checked="clientEnabled">启用节点</n-checkbox>
          <n-checkbox v-if="editingNode?.protocol === 'vless'" v-model:checked="clientUDP443">允许 UDP/443</n-checkbox>
          <div class="modal-actions"><n-button @click="formOpen = false">取消</n-button><n-button type="primary" attr-type="submit" :loading="submitting">保存</n-button></div>
        </form>
      </n-card>
    </n-modal>

    <n-modal :show="viewedNode !== null" @update:show="(show) => { if (!show) viewedNode = null }">
      <n-card class="client-detail-card" title="用户节点详情" closable @close="viewedNode = null">
        <template v-if="viewedNode?.client">
          <h3>{{ viewedNode.server_name }} · {{ viewedNode.proxy_name }}</h3>
          <dl class="user-details">
            <div><dt>客户端名称</dt><dd>{{ viewedNode.client.name }}</dd></div>
            <div><dt>状态</dt><dd>{{ clientStatusLabel(viewedNode.client.status) }}</dd></div>
            <div><dt>流量</dt><dd>{{ clientTrafficUsageLabel(viewedNode.client.metrics, viewedNode.client.traffic_limit_bytes) }}</dd></div>
            <div><dt>到期</dt><dd>{{ formatClientExpiration(viewedNode.client.expires_at) }}</dd></div>
            <div><dt>付款周期</dt><dd>{{ billingLabel(viewedNode.client.billing_period_months) }}</dd></div>
            <div><dt>中转端口</dt><dd>{{ relayPortLabel(viewedNode) }}</dd></div>
            <div v-if="viewedNode.protocol === 'vless'"><dt>UDP/443</dt><dd>{{ viewedNode.client.client_udp443 ? '允许' : '禁止' }}</dd></div>
          </dl>
        </template>
      </n-card>
    </n-modal>
  </div>
</template>
