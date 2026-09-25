<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NModal, NSpin, NTag } from 'naive-ui'

import { api } from '../../api/client'
import { relayEndpointLabel } from '../../relay'
import {
  autofillExternalNodeName,
  landingProtocolLabel,
  landingVisibilityLabel,
  type LandingRecord,
  type LandingVisibility,
} from '../../landing'
import QRCodeModal from '../share/QRCodeModal.vue'

type ExternalNodeShare = {
  landing: LandingRecord
  uri: string
}

const landings = ref<LandingRecord[]>([])
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const formOpen = ref(false)
const editingID = ref<number | null>(null)
const name = ref('')
const visibility = ref<LandingVisibility>('private')
const uri = ref('')
const detailOpen = ref(false)
const detailLoading = ref(false)
const detailError = ref('')
const selectedShare = ref<ExternalNodeShare | null>(null)
const copiedShareURI = ref(false)
const qrOpen = ref(false)
const qrURI = ref('')
const qrTitle = ref('')
const qrSubtitle = ref('')
let copiedResetTimer: ReturnType<typeof setTimeout> | null = null
let detailRequest = 0

function resetForm() {
  editingID.value = null
  name.value = ''
  visibility.value = 'private'
  uri.value = ''
}

function openCreate() {
  resetForm()
  error.value = ''
  formOpen.value = true
}

function openEdit(value: LandingRecord) {
  editingID.value = value.id
  name.value = value.name
  visibility.value = value.visibility
  uri.value = ''
  error.value = ''
  formOpen.value = true
}

function closeForm() {
  if (submitting.value) return
  formOpen.value = false
  resetForm()
  error.value = ''
}

async function loadExternalNodes() {
  const response = await api<{ landings: LandingRecord[] }>('/api/landings')
  landings.value = response.landings
}

function clearCopiedState() {
  if (copiedResetTimer !== null) clearTimeout(copiedResetTimer)
  copiedResetTimer = null
  copiedShareURI.value = false
}

function setQRCodeOpen(show: boolean) {
  qrOpen.value = show
  if (!show) {
    qrURI.value = ''
    qrTitle.value = ''
    qrSubtitle.value = ''
  }
}

function setDetailOpen(show: boolean) {
  detailOpen.value = show
  if (!show) {
    detailRequest++
    detailLoading.value = false
    detailError.value = ''
    selectedShare.value = null
    clearCopiedState()
    setQRCodeOpen(false)
  }
}

async function showExternalNode(value: LandingRecord) {
  const request = ++detailRequest
  setQRCodeOpen(false)
  clearCopiedState()
  selectedShare.value = null
  detailError.value = ''
  detailLoading.value = true
  detailOpen.value = true
  try {
    const response = await api<ExternalNodeShare>(`/api/landings/${value.id}/share`)
    if (request === detailRequest) selectedShare.value = response
  } catch (reason) {
    if (request === detailRequest) detailError.value = reason instanceof Error ? reason.message : '无法加载外部节点链接'
  } finally {
    if (request === detailRequest) detailLoading.value = false
  }
}

async function copyExternalNodeURI() {
  if (!selectedShare.value) return
  detailError.value = ''
  try {
    await navigator.clipboard.writeText(selectedShare.value.uri)
    clearCopiedState()
    copiedShareURI.value = true
    copiedResetTimer = setTimeout(() => {
      copiedShareURI.value = false
      copiedResetTimer = null
    }, 2000)
  } catch {
    detailError.value = '复制链接失败，请手动复制'
  }
}

function showExternalNodeQRCode() {
  if (!selectedShare.value) return
  qrURI.value = selectedShare.value.uri
  qrTitle.value = selectedShare.value.landing.name
  qrSubtitle.value = `${landingProtocolLabel(selectedShare.value.landing.protocol)} · 外部节点`
  qrOpen.value = true
}

async function saveExternalNode() {
  submitting.value = true
  error.value = ''
  try {
    const creating = editingID.value === null
    const payload = {
      name: name.value,
      visibility: visibility.value,
      ...((creating || uri.value.trim()) ? { uri: uri.value } : {}),
    }
    await api(creating ? '/api/landings' : `/api/landings/${editingID.value}`, {
      method: creating ? 'POST' : 'PATCH',
      body: JSON.stringify(payload),
    })
    formOpen.value = false
    resetForm()
    await loadExternalNodes()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '保存外部节点失败'
  } finally {
    submitting.value = false
  }
}

async function removeExternalNode(value: LandingRecord) {
  if (!window.confirm(`确定删除外部节点“${value.name}”吗？`)) return
  submitting.value = true
  error.value = ''
  try {
    await api(`/api/landings/${value.id}`, { method: 'DELETE' })
    await loadExternalNodes()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '删除外部节点失败'
  } finally {
    submitting.value = false
  }
}

watch(uri, (value) => {
  name.value = autofillExternalNodeName(name.value, value)
})

onMounted(async () => {
  try {
    await loadExternalNodes()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法加载外部节点'
  } finally {
    loading.value = false
  }
})

onUnmounted(clearCopiedState)
</script>

<template>
  <n-card class="external-node-card" title="外部节点" :bordered="true">
    <template #header-extra>
      <n-button type="primary" @click="openCreate">导入外部节点</n-button>
    </template>

    <n-alert v-if="error" class="external-node-alert" type="error" closable @close="error = ''">{{ error }}</n-alert>
    <div v-if="loading" class="loading-row"><n-spin size="small" /><span>正在加载外部节点…</span></div>
    <n-empty v-else-if="landings.length === 0" description="当前没有外部节点" />
    <div v-else class="server-table-wrap">
      <table class="server-table external-node-table">
        <thead><tr><th>名称</th><th>协议</th><th>地址</th><th>端口</th><th>可见性</th><th>所有者状态</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="value in landings" :key="value.id">
            <td>{{ value.name }}</td>
            <td>{{ landingProtocolLabel(value.protocol) }}</td>
            <td>{{ value.host }}</td>
            <td>{{ value.port }}</td>
            <td><n-tag :type="value.visibility === 'public' ? 'success' : 'default'" size="small">{{ landingVisibilityLabel(value.visibility) }}</n-tag></td>
            <td>{{ value.owned_by_me ? '我的节点' : '公开节点 · 仅所有者可编辑' }}</td>
            <td class="server-actions">
              <n-button size="small" secondary @click="showExternalNode(value)">查看</n-button>
              <template v-if="value.owned_by_me">
                <n-button size="small" secondary @click="openEdit(value)">编辑</n-button>
                <n-button size="small" type="error" secondary @click="removeExternalNode(value)">删除</n-button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </n-card>

  <n-modal :show="formOpen" :mask-closable="!submitting" @update:show="(show) => !show && closeForm()">
    <n-card class="external-node-form-card" :title="editingID === null ? '导入外部节点' : '编辑外部节点'" :bordered="false" closable @close="closeForm">
      <form class="relay-form" @submit.prevent="saveExternalNode">
        <n-alert v-if="error" type="error">{{ error }}</n-alert>
        <label>
          <span>{{ editingID === null ? '节点链接' : '替换节点链接（可选）' }}</span>
          <n-input v-model:value="uri" type="textarea" :autosize="{ minRows: 3 }" :placeholder="editingID === null ? 'vless://... 或 ss://...' : '留空保持当前节点链接不变'" />
        </label>
        <label><span>名称</span><n-input v-model:value="name" maxlength="100" placeholder="可留空，将从链接名称自动读取" /></label>
        <label>
          <span>可见性</span>
          <select v-model="visibility" class="settings-input">
            <option value="private">私有（仅自己可见和使用）</option>
            <option value="public">公开（其他 Panel 用户可看到并作为中转目标使用）</option>
          </select>
        </label>
        <n-alert v-if="visibility === 'public'" type="warning">公开外部节点后，其他 Panel 用户可以查看、复制完整节点链接并生成二维码，也可以使用该节点创建中转。公开即意味着其他登录用户能够获得完整连接凭据。</n-alert>
        <div class="modal-actions">
          <n-button attr-type="button" @click="closeForm">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="submitting" :disabled="editingID === null && !uri.trim()">{{ editingID === null ? '导入' : '保存修改' }}</n-button>
        </div>
      </form>
    </n-card>
  </n-modal>

  <n-modal :show="detailOpen" :mask-closable="!detailLoading" @update:show="setDetailOpen">
    <n-card class="external-node-detail-card" title="外部节点详情" :bordered="false" closable @close="setDetailOpen(false)">
      <div v-if="detailLoading" class="loading-row"><n-spin size="small" /><span>正在加载外部节点链接…</span></div>
      <n-alert v-else-if="detailError" type="error">{{ detailError }}</n-alert>
      <template v-if="selectedShare">
        <dl class="server-details">
          <div><dt>名称</dt><dd>{{ selectedShare.landing.name }}</dd></div>
          <div><dt>协议</dt><dd>{{ landingProtocolLabel(selectedShare.landing.protocol) }}</dd></div>
          <div><dt>地址</dt><dd>{{ relayEndpointLabel(selectedShare.landing.host, selectedShare.landing.port) }}</dd></div>
          <div><dt>可见性</dt><dd>{{ landingVisibilityLabel(selectedShare.landing.visibility) }}</dd></div>
        </dl>
        <h3>节点链接</h3>
        <n-input :value="selectedShare.uri" type="textarea" readonly :autosize="{ minRows: 3 }" />
        <div class="modal-actions">
          <n-button secondary @click="showExternalNodeQRCode">二维码</n-button>
          <n-button type="primary" @click="copyExternalNodeURI">{{ copiedShareURI ? '链接已复制' : '复制节点链接' }}</n-button>
          <n-button @click="setDetailOpen(false)">关闭</n-button>
        </div>
      </template>
      <div v-else-if="!detailLoading" class="modal-actions"><n-button @click="setDetailOpen(false)">关闭</n-button></div>
    </n-card>
  </n-modal>

  <QRCodeModal :show="qrOpen" :uri="qrURI" :title="qrTitle" :subtitle="qrSubtitle" @update:show="setQRCodeOpen" />
</template>

<style scoped>
.external-node-card {
  margin-top: 20px;
}

.external-node-alert {
  margin-bottom: 16px;
}

.external-node-table {
  min-width: 800px;
}

.external-node-form-card,
.external-node-detail-card {
  width: min(640px, calc(100vw - 32px));
  max-height: calc(100vh - 48px);
  overflow: auto;
}

@media (max-width: 720px) {
  .external-node-form-card,
  .external-node-detail-card {
    width: calc(100vw - 24px);
  }
}
</style>
