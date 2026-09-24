<script setup lang="ts">
import { ref, watch } from 'vue'
import { NAlert, NButton, NCard, NEmpty, NInput, NModal, NTag } from 'naive-ui'

import { api } from '../../api/client'
import {
  landingProtocolLabel,
  landingVisibilityLabel,
  type LandingRecord,
  type LandingVisibility,
} from '../../landing'
import { relayEndpointLabel } from '../../relay'

const props = defineProps<{
  show: boolean
  landings: LandingRecord[]
}>()
const emit = defineEmits<{
  'update:show': [show: boolean]
  changed: [landing: LandingRecord | null]
}>()

const editingID = ref<number | null>(null)
const name = ref('')
const visibility = ref<LandingVisibility>('private')
const uri = ref('')
const submitting = ref(false)
const error = ref('')

function resetForm() {
  editingID.value = null
  name.value = ''
  visibility.value = 'private'
  uri.value = ''
  error.value = ''
}

function editLanding(value: LandingRecord) {
  editingID.value = value.id
  name.value = value.name
  visibility.value = value.visibility
  uri.value = ''
  error.value = ''
}

async function saveLanding() {
  submitting.value = true
  error.value = ''
  try {
    const payload = {
      name: name.value,
      visibility: visibility.value,
      ...((editingID.value === null || uri.value.trim()) ? { uri: uri.value } : {}),
    }
    const creating = editingID.value === null
    const response = await api<{ landing: LandingRecord }>(creating ? '/api/landings' : `/api/landings/${editingID.value}`, {
      method: creating ? 'POST' : 'PATCH',
      body: JSON.stringify(payload),
    })
    resetForm()
    emit('changed', creating ? response.landing : null)
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '保存落地失败'
  } finally {
    submitting.value = false
  }
}

async function removeLanding(value: LandingRecord) {
  if (!window.confirm(`确定删除落地“${value.name}”吗？`)) return
  submitting.value = true
  error.value = ''
  try {
    await api(`/api/landings/${value.id}`, { method: 'DELETE' })
    if (editingID.value === value.id) resetForm()
    emit('changed', null)
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '删除落地失败'
  } finally {
    submitting.value = false
  }
}

function setShow(show: boolean) {
  if (!show) resetForm()
  emit('update:show', show)
}

watch(() => props.show, (show) => {
  if (!show) resetForm()
})
</script>

<template>
  <n-modal :show="show" :mask-closable="!submitting" @update:show="setShow">
    <n-card class="landing-manager-card" title="管理落地" :bordered="false" closable @close="setShow(false)">
      <form class="relay-form" @submit.prevent="saveLanding">
        <n-alert v-if="error" type="error">{{ error }}</n-alert>
        <label><span>名称</span><n-input v-model:value="name" maxlength="100" :placeholder="editingID === null ? '可留空，将从链接名称推导' : ''" /></label>
        <label>
          <span>可见性</span>
          <select v-model="visibility" class="settings-input">
            <option value="private">私有</option>
            <option value="public">公开</option>
          </select>
        </label>
        <n-alert v-if="visibility === 'public'" type="warning">公开后，其他 Panel 用户可以使用此落地创建中转，并获取可实际连接的中转节点链接。</n-alert>
        <label>
          <span>{{ editingID === null ? 'VLESS / Shadowsocks 节点链接' : '替换节点链接（可选）' }}</span>
          <n-input v-model:value="uri" type="textarea" :autosize="{ minRows: 3 }" :placeholder="editingID === null ? 'vless://... 或 ss://...' : '留空则保持当前链接不变'" />
        </label>
        <div class="modal-actions">
          <n-button v-if="editingID !== null" @click="resetForm">取消编辑</n-button>
          <n-button type="primary" attr-type="submit" :loading="submitting" :disabled="editingID === null && !uri.trim()">{{ editingID === null ? '导入落地' : '保存修改' }}</n-button>
        </div>
      </form>

      <h3>可用落地</h3>
      <n-empty v-if="landings.length === 0" description="当前没有已导入落地" />
      <div v-else class="landing-list">
        <div v-for="value in landings" :key="value.id" class="landing-row">
          <div>
            <strong>{{ value.name }}</strong>
            <span>{{ landingProtocolLabel(value.protocol) }} · {{ relayEndpointLabel(value.host, value.port) }}</span>
          </div>
          <n-tag :type="value.visibility === 'public' ? 'success' : 'default'" size="small">{{ landingVisibilityLabel(value.visibility) }}</n-tag>
          <div v-if="value.owned_by_me" class="landing-actions">
            <n-button size="small" secondary @click="editLanding(value)">编辑</n-button>
            <n-button size="small" type="error" secondary @click="removeLanding(value)">删除</n-button>
          </div>
          <small v-else>公开落地 · 仅所有者可编辑</small>
        </div>
      </div>
    </n-card>
  </n-modal>
</template>

<style scoped>
.landing-manager-card {
  width: min(760px, calc(100vw - 32px));
  max-height: calc(100vh - 48px);
  overflow: auto;
}

.landing-list {
  display: grid;
  gap: 8px;
}

.landing-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 12px;
  padding: 10px 0;
  border-top: 1px solid #e6e9ed;
}

.landing-row > div:first-child {
  display: grid;
  gap: 2px;
}

.landing-row span,
.landing-row small {
  color: #65717e;
}

.landing-actions {
  display: flex;
  gap: 6px;
}

@media (max-width: 720px) {
  .landing-manager-card {
    width: calc(100vw - 24px);
  }

  .landing-row {
    grid-template-columns: 1fr auto;
  }

  .landing-actions,
  .landing-row > small {
    grid-column: 1 / -1;
  }
}
</style>
