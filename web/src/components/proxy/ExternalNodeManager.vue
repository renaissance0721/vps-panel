<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NModal, NSpin, NTag } from 'naive-ui'

import { api } from '../../api/client'
import {
  autofillExternalNodeName,
  landingProtocolLabel,
  landingVisibilityLabel,
  type LandingRecord,
  type LandingVisibility,
} from '../../landing'

const landings = ref<LandingRecord[]>([])
const loading = ref(true)
const submitting = ref(false)
const error = ref('')
const formOpen = ref(false)
const editingID = ref<number | null>(null)
const name = ref('')
const visibility = ref<LandingVisibility>('private')
const uri = ref('')

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
              <template v-if="value.owned_by_me">
                <n-button size="small" secondary @click="openEdit(value)">编辑</n-button>
                <n-button size="small" type="error" secondary @click="removeExternalNode(value)">删除</n-button>
              </template>
              <span v-else class="secondary-text">可作为中转目标使用</span>
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
        <n-alert v-if="visibility === 'public'" type="warning">公开外部节点后，其他 Panel 用户可以使用该节点创建中转，并获得可实际连接的中转节点链接。这可能间接暴露可使用的节点凭据。</n-alert>
        <div class="modal-actions">
          <n-button attr-type="button" @click="closeForm">取消</n-button>
          <n-button type="primary" attr-type="submit" :loading="submitting" :disabled="editingID === null && !uri.trim()">{{ editingID === null ? '导入' : '保存修改' }}</n-button>
        </div>
      </form>
    </n-card>
  </n-modal>
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

.external-node-form-card {
  width: min(640px, calc(100vw - 32px));
  max-height: calc(100vh - 48px);
  overflow: auto;
}

@media (max-width: 720px) {
  .external-node-form-card {
    width: calc(100vw - 24px);
  }
}
</style>
