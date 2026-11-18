<script setup>
import { onMounted, ref, watch } from 'vue'
import QRCode from 'qrcode'

const props = defineProps({
  text: { type: String, required: true },
  size: { type: Number, default: 190 },
  bg: { type: String, default: '#ffffff' },
  fg: { type: String, default: '#0f1115' },
})

const canvas = ref(null)
const error = ref('')

async function render() {
  if (!canvas.value || !props.text) return
  try {
    await QRCode.toCanvas(canvas.value, props.text, {
      width: props.size,
      margin: 1,
      errorCorrectionLevel: 'M',
      color: { dark: props.fg, light: props.bg },
    })
    error.value = ''
  } catch (e) {
    error.value = '二维码生成失败：' + e.message
  }
}

onMounted(render)
watch(() => props.text, render)
watch(() => props.size, render)
</script>

<template>
  <div class="qr">
    <canvas ref="canvas" :width="size" :height="size"></canvas>
    <div v-if="error" class="hint" style="color: var(--err-text)">{{ error }}</div>
  </div>
</template>

<style scoped>
.qr {
  background: #fff;
  border-radius: 10px;
  padding: 8px;
  line-height: 0;
  display: inline-block;
}
</style>
