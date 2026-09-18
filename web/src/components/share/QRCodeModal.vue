<script setup lang="ts">
import { ref, watch } from 'vue'
import { NAlert, NButton, NCard, NModal, NSpin } from 'naive-ui'
import QRCode from 'qrcode'

const props = defineProps<{
  show: boolean
  uri: string
  title: string
  subtitle?: string
}>()
const emit = defineEmits<{ 'update:show': [show: boolean] }>()

const image = ref('')
const error = ref('')
let generation = 0

watch([() => props.show, () => props.uri], async ([show, uri]) => {
  const current = ++generation
  image.value = ''
  error.value = ''
  if (!show || !uri) return
  try {
    const result = await QRCode.toDataURL(uri, {
      errorCorrectionLevel: 'M',
      margin: 2,
      width: 280,
    })
    if (current === generation && props.show && props.uri === uri) image.value = result
  } catch {
    if (current === generation && props.show && props.uri === uri) error.value = '二维码生成失败'
  }
}, { immediate: true })

function setShow(show: boolean) {
  if (!show) {
    generation++
    image.value = ''
    error.value = ''
  }
  emit('update:show', show)
}
</script>

<template>
  <n-modal :show="show" @update:show="setShow">
    <n-card class="qr-modal-card" title="二维码" :bordered="false" closable @close="setShow(false)">
      <div class="qr-modal-content">
        <strong>{{ title }}</strong>
        <small v-if="subtitle">{{ subtitle }}</small>
        <n-alert v-if="error" type="error">{{ error }}</n-alert>
        <img v-else-if="image" class="qr-modal-image" :src="image" :alt="`${title} 二维码`" />
        <n-spin v-else-if="show && uri" size="small" />
        <p>使用支持该协议的客户端扫描二维码即可导入</p>
      </div>
      <div class="modal-actions"><n-button @click="setShow(false)">关闭</n-button></div>
    </n-card>
  </n-modal>
</template>

<style scoped>
.qr-modal-card {
  width: min(380px, calc(100vw - 32px));
}

.qr-modal-content {
  display: grid;
  justify-items: center;
  gap: 10px;
  text-align: center;
}

.qr-modal-content small,
.qr-modal-content p {
  color: #65717e;
}

.qr-modal-content p {
  margin: 0;
  font-size: 13px;
}

.qr-modal-image {
  width: 280px;
  max-width: 100%;
  height: auto;
  background: #fff;
}
</style>
