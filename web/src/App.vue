<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NAlert, NCard, NConfigProvider, NSpin, NTag } from 'naive-ui'

type Health = {
  status: string
  database: string
}

const health = ref<Health | null>(null)
const error = ref('')
const loading = ref(true)

const isHealthy = computed(
  () => health.value?.status === 'ok' && health.value.database === 'ok',
)

onMounted(async () => {
  try {
    const response = await fetch('/api/health', { cache: 'no-store' })
    if (!response.ok) {
      throw new Error(`健康检查返回 ${response.status}`)
    }
    health.value = (await response.json()) as Health
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '无法连接 Panel API'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <n-config-provider>
    <main class="page-shell">
      <n-card class="status-card" :bordered="true">
        <div class="heading-row">
          <div>
            <p class="eyebrow">VPS MANAGEMENT</p>
            <h1>VPS Panel</h1>
          </div>
          <n-tag v-if="isHealthy" type="success" round>运行正常</n-tag>
        </div>

        <p class="description">Panel is running.</p>

        <div v-if="loading" class="loading-row">
          <n-spin size="small" />
          <span>正在检查服务状态…</span>
        </div>

        <n-alert v-else-if="error" title="服务暂不可用" type="error">
          {{ error }}
        </n-alert>

        <div v-else class="health-grid">
          <div>
            <span>Backend</span>
            <strong>{{ health?.status === 'ok' ? 'Healthy' : 'Unavailable' }}</strong>
          </div>
          <div>
            <span>SQLite</span>
            <strong>{{ health?.database === 'ok' ? 'Connected' : 'Unavailable' }}</strong>
          </div>
        </div>

        <p class="phase-note">v0.1 · Phase 1</p>
      </n-card>
    </main>
  </n-config-provider>
</template>
