<script setup>
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps({
  series: { type: Array, default: () => [] }, // [{name, color, points:[{t,v}]}]
  height: { type: Number, default: 170 },
  yMax: { type: Number, default: 0 }, // 0 = 自动
  yFormat: { type: Function, default: (v) => String(v) },
  emptyText: { type: String, default: '等待采样…' },
})

const canvas = ref(null)
const wrap = ref(null)
let ro = null
let themeObserver = null

// 从 CSS 变量取色，保证深色/浅色主题下网格与刻度都可读
function cssVar(name, fallback) {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return v || fallback
}

function draw() {
  const el = canvas.value
  if (!el) return
  const dpr = window.devicePixelRatio || 1
  const w = el.clientWidth || 600
  const h = props.height
  el.width = w * dpr
  el.height = h * dpr
  const ctx = el.getContext('2d')
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  ctx.clearRect(0, 0, w, h)

  const padL = 42
  const padR = 10
  const padT = 10
  const padB = 18
  const innerW = Math.max(10, w - padL - padR)
  const innerH = Math.max(10, h - padT - padB)

  const series = (props.series || []).filter((s) => s && s.points && s.points.length > 1)
  let maxV = props.yMax
  if (!maxV) {
    maxV = 1
    series.forEach((s) => s.points.forEach((p) => (maxV = Math.max(maxV, p.v))))
    maxV = niceMax(maxV)
  }

  // 主题色（随 html[data-theme] 变化）
  const isLight = document.documentElement.dataset.theme === 'light'
  const gridColor = isLight ? 'rgba(30,41,59,0.09)' : 'rgba(255,255,255,0.07)'
  const tickColor = cssVar('--text-mute', isLight ? '#8592a3' : '#758091')
  const emptyColor = cssVar('--text-mute', isLight ? '#8592a3' : '#758091')

  // 网格
  ctx.strokeStyle = gridColor
  ctx.fillStyle = tickColor
  ctx.font = '11px ui-monospace, Consolas, monospace'
  ctx.lineWidth = 1
  for (let i = 0; i <= 4; i++) {
    const y = padT + (innerH * i) / 4
    ctx.beginPath()
    ctx.moveTo(padL, y)
    ctx.lineTo(w - padR, y)
    ctx.stroke()
    const val = maxV * (1 - i / 4)
    ctx.fillText(props.yFormat(val), 6, y + 4)
  }

  if (series.length === 0) {
    ctx.fillStyle = emptyColor
    ctx.font = '12px system-ui, sans-serif'
    ctx.fillText(props.emptyText, padL + 8, padT + innerH / 2)
    return
  }

  const n = Math.max(...series.map((s) => s.points.length))
  const xAt = (i) => padL + (innerW * i) / (n - 1 || 1)
  const yAt = (v) => padT + innerH - (Math.min(v, maxV) / maxV) * innerH

  series.forEach((s, si) => {
    const pts = s.points
    ctx.beginPath()
    pts.forEach((p, i) => {
      const x = xAt(i + (n - pts.length))
      const y = yAt(p.v)
      if (i === 0) ctx.moveTo(x, y)
      else ctx.lineTo(x, y)
    })
    const lineColor = resolveColor(s.color, '#6ea8fe')
    ctx.strokeStyle = lineColor
    ctx.lineWidth = si === 0 ? 2.2 : 1.8
    ctx.lineJoin = 'round'
    ctx.lineCap = 'round'
    ctx.stroke()

    if (si === 0) {
      const grad = ctx.createLinearGradient(0, padT, 0, padT + innerH)
      grad.addColorStop(0, toRGBA(lineColor, 0.3))
      grad.addColorStop(1, toRGBA(lineColor, 0))
      ctx.lineTo(xAt(n - 1), padT + innerH)
      ctx.lineTo(xAt(n - pts.length), padT + innerH)
      ctx.closePath()
      ctx.fillStyle = grad
      ctx.fill()
    }
  })
}

// color 可以是 '--css-var' 形式（跟随主题），也可以是 #hex / rgb()
function resolveColor(color, fallback) {
  if (!color) return fallback
  if (color.startsWith('--')) return cssVar(color, fallback)
  return color
}

// 把 #hex / rgb() / var 解析成 rgba(...)，用于渐变透明
function toRGBA(color, a) {
  const c = color.trim()
  if (c.startsWith('rgb')) {
    return c.replace(/rgba?\(([^)]+)\)/, (_, inner) => {
      const parts = inner.split(/[,/\s]+/).filter(Boolean)
      return `rgba(${parts[0]},${parts[1]},${parts[2]},${a})`
    })
  }
  return hexA(c, a)
}

function hexA(hex, a) {
  const h = hex.replace('#', '')
  const n = parseInt(h.length === 3 ? h.split('').map((c) => c + c).join('') : h, 16)
  if (Number.isNaN(n)) return `rgba(110,168,254,${a})`
  return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${a})`
}

function niceMax(v) {
  if (v <= 1) return 1
  const exp = Math.floor(Math.log10(v))
  const base = Math.pow(10, exp)
  const n = v / base
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10
  return step * base
}

watch(() => props.series, draw, { deep: true })
watch(() => props.height, draw)

onMounted(() => {
  draw()
  if (window.ResizeObserver && wrap.value) {
    ro = new ResizeObserver(draw)
    ro.observe(wrap.value)
  }
  // 主题切换后重绘 canvas
  if (window.MutationObserver) {
    themeObserver = new MutationObserver(draw)
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  }
})

onBeforeUnmount(() => {
  if (ro) ro.disconnect()
  if (themeObserver) themeObserver.disconnect()
})
</script>

<template>
  <div ref="wrap" class="chart-wrap">
    <canvas ref="canvas" :style="{ height: height + 'px' }"></canvas>
  </div>
</template>

<style scoped>
.chart-wrap {
  width: 100%;
}
canvas {
  width: 100%;
  display: block;
}
</style>
