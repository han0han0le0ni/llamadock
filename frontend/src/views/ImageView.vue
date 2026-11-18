<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../api'
import { notify } from '../store'

// 生图引擎状态与模型列表
const status = ref(null)
const models = ref([])
const loading = ref(false)
const starting = ref(false)
const generating = ref(false)

// 表单
const model = ref('')
const prompt = ref('')
const negative = ref('')
const size = ref('512x512')
const steps = ref(25)
const cfg = ref(7)
const seed = ref(-1)
const sampler = ref('euler_a')
const batch = ref(1)

// 结果
const images = ref([])
const elapsed = ref(0)
const lastError = ref('')

const sizes = ['512x512', '768x768', '1024x1024', '896x1152', '1152x896', '832x1216', '1216x832']
const samplers = ['euler_a', 'euler', 'dpm++_2m', 'dpm++_2m_karras', 'dpm++_2s_a', 'heun', 'ddim', 'lcm']

const process = computed(() => (status.value && status.value.process) || {})
const running = computed(() => !!(status.value && status.value.running))
const online = computed(() => !!(status.value && status.value.online))
const enabled = computed(() => !!(status.value && status.value.enabled))
const defaults = computed(() => (status.value && status.value.defaults) || {})

const checkpointModels = computed(() => models.value.filter((m) => m.kind !== 'lora' && m.kind !== 'vae'))

async function refresh() {
  loading.value = true
  try {
    const [s, m] = await Promise.all([api.imageStatus(), api.imageModels()])
    status.value = s
    models.value = (m && m.models) || []
    const defs = s.defaults || {}
    if (!size.value && defs.width) size.value = `${defs.width}x${defs.height}`
    if (!steps.value && defs.steps) steps.value = defs.steps
    if (!cfg.value && defs.cfg_scale) cfg.value = defs.cfg_scale
    // 默认选配置的模型，否则选第一个 checkpoint
    if (!model.value) {
      model.value = (s.process && s.process.model_path) || (checkpointModels.value[0] && checkpointModels.value[0].path) || ''
    }
    if (s.error) lastError.value = s.error
  } catch (e) {
    lastError.value = e.message
  } finally {
    loading.value = false
  }
}

async function startEngine() {
  starting.value = true
  lastError.value = ''
  try {
    const s = await api.imageStart(model.value)
    status.value = s
    notify('生图引擎已启动', 'ok')
  } catch (e) {
    lastError.value = e.message
    notify(e.message, 'err', 7000)
  } finally {
    starting.value = false
  }
}

async function stopEngine() {
  starting.value = true
  try {
    status.value = await api.imageStop()
    notify('生图引擎已停止', 'warn')
  } catch (e) {
    notify(e.message, 'err')
  } finally {
    starting.value = false
  }
}

async function generate() {
  if (!prompt.value.trim()) {
    notify('请先填写提示词', 'warn')
    return
  }
  if (!running.value || !online.value) {
    notify('生图引擎未就绪，请先启动引擎', 'warn')
    return
  }
  generating.value = true
  lastError.value = ''
  images.value = []
  const t0 = performance.now()
  try {
    const r = await api.imageGenerate({
      prompt: prompt.value,
      negative_prompt: negative.value,
      size: size.value,
      steps: Number(steps.value),
      cfg_scale: Number(cfg.value),
      seed: Number(seed.value) || -1,
      sampler: sampler.value,
    })
    const data = (r && r.data) || []
    images.value = data
      .map((d) => d.b64_json || d.url)
      .filter(Boolean)
      .map((src) => (src.startsWith('data:') || src.startsWith('http') ? src : `data:image/png;base64,${src}`))
    elapsed.value = Math.round((performance.now() - t0) / 1000)
    if (!images.value.length) lastError.value = '引擎返回了空结果，请查看运行日志'
    else notify(`出图完成，用时 ${elapsed.value} 秒`, 'ok')
  } catch (e) {
    lastError.value = e.message
    notify(e.message, 'err', 8000)
  } finally {
    generating.value = false
  }
}

function saveImage(src, idx) {
  const a = document.createElement('a')
  a.href = src
  a.download = `llamadock-${Date.now()}-${idx + 1}.png`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
}

onMounted(refresh)
</script>

<template>
  <div class="page">
    <!-- 引擎状态条 -->
    <section class="card">
      <div class="row wrap">
        <span class="badge info">生图引擎 · stable-diffusion.cpp</span>
        <span class="dot" :class="online ? 'ok' : running ? 'warn' : 'err'"></span>
        <span class="hint">
          <template v-if="!enabled">未启用（到「设置 → 生图引擎」打开）</template>
          <template v-else-if="online">引擎在线 · 端口 {{ process.port }}</template>
          <template v-else-if="running">引擎启动中 / 加载模型…</template>
          <template v-else>引擎未启动</template>
        </span>
        <span v-if="process.model" class="badge">{{ process.model }}</span>
        <div class="spacer"></div>
        <button v-if="!running" class="btn primary" :disabled="starting || !enabled" @click="startEngine">
          {{ starting ? '启动中…' : '启动引擎 / 加载模型' }}
        </button>
        <button v-else class="btn" :disabled="starting" @click="stopEngine">停止引擎</button>
        <button class="btn ghost" :disabled="loading" @click="refresh">刷新</button>
      </div>
      <div class="hint" style="margin-top: 10px; line-height: 1.7">
        生图与文本模型使用独立进程。<b>8GB 显存下两者不能同时运行</b>：启动生图会先卸载文本模型，
        之后在「模型」页加载文本模型也会自动停掉生图引擎。首次加载模型 + 出图通常需要 30~90 秒，请耐心等待。
      </div>
      <div v-if="lastError" class="hint" style="margin-top: 10px; padding: 8px 10px; border-radius: 8px; background: rgba(220, 53, 69, 0.12); color: #ff8a94; word-break: break-all">{{ lastError }}</div>
      <div v-if="running && !online && process.log_tail && process.log_tail.length" class="hint mono" style="margin-top: 8px; white-space: pre-wrap; max-height: 120px; overflow: auto">
        {{ process.log_tail.slice(-6).join('\n') }}
      </div>
    </section>

    <div class="gen-grid">
      <!-- 参数表单 -->
      <section class="card">
        <h2 style="margin-top: 0">生成参数</h2>
        <div class="field">
          <label>生图模型</label>
          <select v-model="model" :disabled="running">
            <option value="" disabled>{{ checkpointModels.length ? '选择模型' : '没有扫描到模型，请在设置里配置模型目录' }}</option>
            <option v-for="m in checkpointModels" :key="m.id" :value="m.path">
              {{ m.name }} · {{ m.size_mb }} MB
            </option>
          </select>
          <div class="hint" style="margin-top: 4px">切换模型需先停止引擎再重新启动。SD1.5/SDXL 单文件可直接选；Flux/Z-Image 请在设置的「额外启动参数」里填多组件。</div>
        </div>

        <div class="field">
          <label>正向提示词</label>
          <textarea v-model="prompt" rows="4" placeholder="例如：一只坐在雪地里的红色狐狸，清晨，电影级打光，高细节"></textarea>
        </div>

        <div class="field">
          <label>反向提示词</label>
          <textarea v-model="negative" rows="2" placeholder="例如：低质量，模糊，变形，多余的手指"></textarea>
        </div>

        <div class="row wrap" style="gap: 12px">
          <div class="field" style="flex: 1; min-width: 130px">
            <label>尺寸</label>
            <select v-model="size">
              <option v-for="s in sizes" :key="s" :value="s">{{ s }}</option>
            </select>
          </div>
          <div class="field" style="flex: 1; min-width: 100px">
            <label>步数</label>
            <input v-model.number="steps" type="number" min="1" max="100" />
          </div>
          <div class="field" style="flex: 1; min-width: 100px">
            <label>CFG</label>
            <input v-model.number="cfg" type="number" min="1" max="30" step="0.5" />
          </div>
        </div>

        <div class="row wrap" style="gap: 12px">
          <div class="field" style="flex: 1; min-width: 130px">
            <label>采样器</label>
            <select v-model="sampler">
              <option v-for="s in samplers" :key="s" :value="s">{{ s }}</option>
            </select>
          </div>
          <div class="field" style="flex: 1; min-width: 130px">
            <label>种子（-1 随机）</label>
            <input v-model.number="seed" type="number" />
          </div>
        </div>

        <button class="btn primary" style="width: 100%; margin-top: 6px" :disabled="generating || !online" @click="generate">
          {{ generating ? '生成中…请稍候' : '生成图片' }}
        </button>
        <div v-if="generating" class="hint" style="margin-top: 8px; text-align: center">
          正在出图，这一步通常需要几十秒，期间页面不会卡住。
        </div>
      </section>

      <!-- 结果 -->
      <section class="card">
        <div class="row">
          <h2 style="margin-top: 0">出图结果</h2>
          <div class="spacer"></div>
          <span v-if="elapsed" class="badge ok">用时 {{ elapsed }} 秒</span>
        </div>
        <div v-if="!images.length && !generating" class="empty" style="min-height: 280px">
          填好提示词后点「生成图片」，结果会显示在这里。
        </div>
        <div v-else-if="generating" class="empty" style="min-height: 280px">
          <div class="spinner"></div>
          <div class="hint" style="margin-top: 10px">生成中…</div>
        </div>
        <div v-else class="img-grid">
          <div v-for="(src, i) in images" :key="i" class="img-item">
            <img :src="src" :alt="`生成图片 ${i + 1}`" />
            <button class="btn ghost" style="margin-top: 6px; width: 100%" @click="saveImage(src, i)">保存</button>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.gen-grid {
  display: grid;
  grid-template-columns: minmax(320px, 420px) 1fr;
  gap: 16px;
  margin-top: 16px;
}
@media (max-width: 900px) {
  .gen-grid { grid-template-columns: 1fr; }
}
.field { margin-bottom: 14px; display: flex; flex-direction: column; gap: 5px; }
.field label { font-size: 12px; color: var(--text-dim, #9aa4b2); font-weight: 600; }
.field input, .field select, .field textarea {
  width: 100%;
  box-sizing: border-box;
  background: var(--surface-2, #1b1f27);
  border: 1px solid var(--border, #2a303b);
  color: var(--text, #e6e8eb);
  border-radius: 8px;
  padding: 8px 10px;
  font: inherit;
  resize: vertical;
}
.img-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 12px;
}
.img-item img {
  width: 100%;
  border-radius: 10px;
  border: 1px solid var(--border, #2a303b);
  display: block;
}
.spinner {
  width: 32px; height: 32px;
  border: 3px solid var(--border, #2a303b);
  border-top-color: var(--accent, #6ea8fe);
  border-radius: 50%;
  margin: 0 auto;
  animation: spin 0.9s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
