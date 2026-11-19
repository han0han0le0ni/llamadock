<script setup>
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { api } from '../api'
import { notify, state } from '../store'
import Icon from '../components/Icon.vue'
import QrCode from '../components/QrCode.vue'

// 生图引擎状态与模型列表
const status = ref(null)
const models = ref([])
const loading = ref(false)
const starting = ref(false)
const generating = ref(false)
const switching = ref(false) // 切换模型预设中

// 表单
const model = ref('')
const prompt = ref('')
const negative = ref('')
const size = ref('512x512')
const steps = ref(25)
const cfg = ref(7)
const seed = ref(-1)
const sampler = ref('euler_a')

// 图生图：底图（data URL）+ 重绘强度
const initImage = ref('')
const initImageName = ref('')
const strength = ref(0.6)
const initBusy = ref(false)

// 结果：{ src, url, name }
const images = ref([])
const elapsed = ref(0)
const lastError = ref('')
const shareIdx = ref(-1) // 展开分享面板的结果下标
let appliedProfile = '' // 已套用默认参数的预设 id，避免刷新时覆盖用户改过的值

// 生成进度（轮询 image/status 读取 sd-server verbose 日志解析出的 step/speed/eta）
const genProgress = ref(null)
let progressTimer = null

const sizes = ['512x512', '768x768', '1024x1024', '896x1152', '1152x896', '832x1216', '1216x832']
const samplers = ['euler_a', 'euler', 'dpm++_2m', 'dpm++_2m_karras', 'dpm++_2s_a', 'heun', 'ddim', 'lcm']

const process = computed(() => (status.value && status.value.process) || {})
const running = computed(() => !!(status.value && status.value.running))
const online = computed(() => !!(status.value && status.value.online))
const enabled = computed(() => !!(status.value && status.value.enabled))

const profiles = computed(() => (status.value && status.value.profiles) || [])
const activeProfile = computed(() => (status.value && status.value.active_profile) || '')
const activeProfileObj = computed(() => profiles.value.find((p) => p.id === activeProfile.value) || null)
// 当前预设展示名（引擎未启动时也能看出选的是哪个模型）
const profileName = computed(() => {
  if (activeProfileObj.value) return activeProfileObj.value.name
  const mp = process.value.model_path || model.value
  return mp ? mp.split(/[\\/]/).pop() : '未选择'
})

const checkpointModels = computed(() => models.value.filter((m) => m.kind !== 'lora' && m.kind !== 'vae'))
const gatewayOn = computed(() => !!(state.status && state.status.gateway_running))

// —— 把预设拆成看得见的“模型文件清单” ——
// 多组件预设（Qwen-Image 等）不是三个独立模型，而是一套必须一起加载的组件：
// 扩散主干 + 文本编码器 + VAE。这里从 model_path / extra_args 里解析出来展示，
// 避免界面只显示一个预设名，让人以为模型选择丢了。
const modelArgRoles = {
  '--diffusion-model': '扩散主干',
  '--unet': '扩散主干',
  '--llm': '文本编码器',
  '--llm_vision': '视觉编码器',
  '--clip-l': 'CLIP-L 编码器',
  '--clip_l': 'CLIP-L 编码器',
  '--clip-g': 'CLIP-G 编码器',
  '--clip_g': 'CLIP-G 编码器',
  '--t5xxl': 'T5-XXL 编码器',
  '--vae': 'VAE',
}
function splitCliArgs(s) {
  const out = []
  let cur = ''
  let quote = ''
  for (const ch of String(s || '')) {
    if (quote) {
      if (ch === quote) quote = ''
      else cur += ch
    } else if (ch === '"' || ch === "'") {
      quote = ch
    } else if (/\s/.test(ch)) {
      if (cur) { out.push(cur); cur = '' }
    } else {
      cur += ch
    }
  }
  if (cur) out.push(cur)
  return out
}
function baseName(p) {
  const s = String(p || '')
  return s.split(/[\\/]/).pop() || s
}
function profileFiles(p) {
  if (!p) return []
  const files = []
  if (p.model_path) files.push({ role: '主模型', path: p.model_path, name: baseName(p.model_path) })
  const args = splitCliArgs(p.extra_args)
  for (let i = 0; i < args.length; i++) {
    const arg = args[i]
    const eq = arg.indexOf('=')
    const flag = eq >= 0 ? arg.slice(0, eq) : arg
    const role = modelArgRoles[flag]
    if (!role) continue
    let value = eq >= 0 ? arg.slice(eq + 1) : args[i + 1]
    if (eq < 0) i++
    if (!value || value.startsWith('--')) continue
    files.push({ role, path: value, name: baseName(value) })
  }
  return files
}
function profileFileCount(p) {
  const n = profileFiles(p).length
  return n || (p && p.mode === 'components' ? 3 : 1)
}
const activeFiles = computed(() => profileFiles(activeProfileObj.value))

async function refresh() {
  loading.value = true
  try {
    const [s, m] = await Promise.all([api.imageStatus(), api.imageModels()])
    status.value = s
    models.value = (m && m.models) || []
    const defs = s.defaults || {}
    const cur = s.active_profile || ''
    // 预设变化时套用该预设的默认出图参数（尺寸/步数/CFG/采样器）。
    if (cur && cur !== appliedProfile) {
      if (defs.width && defs.height) size.value = `${defs.width}x${defs.height}`
      if (defs.steps) steps.value = defs.steps
      if (defs.cfg_scale) cfg.value = defs.cfg_scale
      if (defs.sampler) sampler.value = defs.sampler
      appliedProfile = cur
    } else if (!cur) {
      // 老配置无预设：仅首次填空
      if (!size.value && defs.width) size.value = `${defs.width}x${defs.height}`
      if (!steps.value && defs.steps) steps.value = defs.steps
      if (!cfg.value && defs.cfg_scale) cfg.value = defs.cfg_scale
    }
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
    // components 预设（如 Qwen-Image）modelPath 为空，靠后端 ExtraArgs 加载，不传 model。
    // single 预设或老配置则把当前选中的单文件路径带上。
    const isComponents = activeProfileObj.value && activeProfileObj.value.mode === 'components'
    const s = await api.imageStart(isComponents ? '' : model.value)
    status.value = s
    notify('生图引擎已启动', 'ok')
  } catch (e) {
    lastError.value = e.message
    notify(e.message, 'err', 7000)
  } finally {
    starting.value = false
  }
}

// 切换生图模型预设：后端写回配置并停掉旧引擎；若之前在运行则自动用新模型重启。
async function useProfile(p) {
  if (switching.value || p.id === activeProfile.value) return
  switching.value = true
  lastError.value = ''
  const wasRunning = running.value
  try {
    let s = await api.imageUseProfile(p.id)
    status.value = s
    appliedProfile = '' // 强制下次 refresh 套用新预设默认参数
    notify(`已切换到「${p.name}」`, 'ok')
    if (wasRunning) {
      // 旧引擎已被后端停掉，用新模型重启
      starting.value = true
      const isComponents = p.mode === 'components'
      s = await api.imageStart(isComponents ? '' : model.value)
      status.value = s
      notify(`已用「${p.name}」重新启动引擎`, 'ok')
    } else {
      // 刷新一次让默认参数套用生效
      await refresh()
    }
  } catch (e) {
    lastError.value = e.message
    notify(e.message, 'err', 8000)
    await refresh()
  } finally {
    switching.value = false
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
  shareIdx.value = -1
  genProgress.value = null
  startProgressPolling()
  const t0 = performance.now()
  try {
    const payload = {
      prompt: prompt.value,
      negative_prompt: negative.value,
      size: size.value,
      steps: Number(steps.value),
      cfg_scale: Number(cfg.value),
      seed: Number(seed.value) || -1,
      sampler: sampler.value,
    }
    if (initImage.value) {
      payload.init_image = initImage.value
      payload.strength = Number(strength.value)
    }
    const r = await api.imageGenerate(payload)
    const data = (r && r.data) || []
    images.value = data.map((d) => {
      const b64 = d.b64_json || d.url || ''
      const src = b64.startsWith('data:') || b64.startsWith('http') ? b64 : `data:image/png;base64,${b64}`
      return { src, url: d.url || '', name: d.file_name || '' }
    })
    elapsed.value = Math.round((performance.now() - t0) / 1000)
    if (!images.value.length) lastError.value = '引擎返回了空结果，请查看运行日志'
    else notify(`出图完成，用时 ${elapsed.value} 秒`, 'ok')
  } catch (e) {
    lastError.value = e.message
    notify(e.message, 'err', 8000)
  } finally {
    stopProgressPolling()
    generating.value = false
  }
}

// —— 生成进度轮询 ——
// sd-server 的 OpenAI 接口同步返回、不暴露进度；后端改为解析它的 verbose 日志进度条，
// 这里定时拉取 image/status 读 process.progress，展示步数 / 速度 / 剩余时间。
function startProgressPolling() {
  stopProgressPolling()
  const poll = async () => {
    try {
      const s = await api.imageStatus()
      const p = s && s.process && s.process.progress
      if (p && p.total > 0) genProgress.value = p
    } catch (_) {
      // 轮询失败不影响出图主流程
    }
  }
  progressTimer = setInterval(poll, 1200)
  poll()
}
function stopProgressPolling() {
  if (progressTimer) {
    clearInterval(progressTimer)
    progressTimer = null
  }
}
function speedText(p) {
  if (!p || !p.speed) return ''
  const v = p.speed >= 10 ? p.speed.toFixed(1) : p.speed.toFixed(2)
  return `${v} ${p.speed_unit || 'it/s'}`
}
onBeforeUnmount(() => stopProgressPolling())

function saveImage(src, idx) {
  const a = document.createElement('a')
  a.href = src
  a.download = `llamadock-${Date.now()}-${idx + 1}.png`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
}

// —— 图生图底图 ——
// 读文件 → canvas 缩放压缩 → data URL（控制体积，避免超过网关 32MB 请求上限）
function fileToDataURL(file, maxSide = 1536, quality = 0.9) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(new Error('读取图片失败'))
    reader.onload = () => {
      const img = new Image()
      img.onerror = () => reject(new Error('图片解码失败'))
      img.onload = () => {
        let { width: w, height: h } = img
        const scale = Math.min(1, maxSide / Math.max(w, h))
        w = Math.max(1, Math.round(w * scale))
        h = Math.max(1, Math.round(h * scale))
        const canvas = document.createElement('canvas')
        canvas.width = w
        canvas.height = h
        const ctx = canvas.getContext('2d')
        ctx.fillStyle = '#ffffff'
        ctx.fillRect(0, 0, w, h)
        ctx.drawImage(img, 0, 0, w, h)
        resolve(canvas.toDataURL('image/jpeg', quality))
      }
      img.src = reader.result
    }
    reader.readAsDataURL(file)
  })
}

async function pickInitFile(e) {
  const file = e.target.files && e.target.files[0]
  if (!file) return
  if (!file.type.startsWith('image/')) {
    notify('请选择图片文件', 'warn')
    return
  }
  initBusy.value = true
  try {
    initImage.value = await fileToDataURL(file)
    initImageName.value = file.name
    notify('底图已载入', 'ok')
  } catch (err) {
    notify(err.message, 'err')
  } finally {
    initBusy.value = false
    e.target.value = ''
  }
}

function onPaste(e) {
  const items = (e.clipboardData && e.clipboardData.items) || []
  for (const it of items) {
    if (it.type.startsWith('image/')) {
      const file = it.getAsFile()
      if (file) {
        e.preventDefault()
        initBusy.value = true
        fileToDataURL(file).then((url) => {
          initImage.value = url
          initImageName.value = '粘贴的图片'
          notify('已从剪贴板载入底图', 'ok')
        }).catch((err) => notify(err.message, 'err')).finally(() => (initBusy.value = false))
      }
      break
    }
  }
}

function useAsInit(item) {
  initImage.value = item.src
  initImageName.value = item.name ? `上次结果 · ${item.name}` : '上次生成结果'
  notify('已把这张图设为底图，调整提示词和强度后再次生成', 'ok')
}

function clearInit() {
  initImage.value = ''
  initImageName.value = ''
}

function toggleShare(idx) {
  shareIdx.value = shareIdx.value === idx ? -1 : idx
}

async function copyURL(url) {
  try {
    await navigator.clipboard.writeText(url)
    notify('链接已复制，发到手机即可打开', 'ok')
  } catch (e) {
    notify('复制失败，请手动长按链接复制', 'err')
  }
}

onMounted(() => {
  refresh()
  window.addEventListener('paste', onPaste)
})
onBeforeUnmount(() => window.removeEventListener('paste', onPaste))
</script>

<template>
  <div class="page">
    <!-- 引擎状态条 -->
    <section class="card engine-bar">
      <div class="row wrap">
        <div class="engine-mark"><Icon name="sparkles" :size="18" /></div>
        <div class="engine-title">
          <b>生图引擎</b>
          <span class="hint">stable-diffusion.cpp</span>
        </div>
        <span class="dot" :class="online ? 'ok' : running ? 'warn' : 'err'"></span>
        <span class="hint engine-state">
          <template v-if="!enabled">未启用（到「设置 → 生图引擎」打开）</template>
          <template v-else-if="online">引擎在线 · 端口 {{ process.port }}</template>
          <template v-else-if="running">引擎启动中 / 加载模型…</template>
          <template v-else>引擎未启动</template>
        </span>
        <span v-if="profileName !== '未选择'" class="badge info engine-model" :title="activeProfileObj ? activeProfileObj.note || activeProfileObj.name : (process.model || model)">
          {{ profileName }}
        </span>
        <div class="spacer"></div>
        <button v-if="!running" class="btn primary sm" :disabled="starting || !enabled" @click="startEngine">
          <Icon :name="starting ? 'refresh' : 'play'" :size="14" />
          {{ starting ? '启动中…' : '启动引擎 / 加载模型' }}
        </button>
        <button v-else class="btn sm" :disabled="starting" @click="stopEngine">
          <Icon name="stop" :size="14" />停止引擎
        </button>
        <button class="btn ghost sm" :disabled="loading" @click="refresh" title="刷新">
          <Icon name="refresh" :size="14" :class="{ spin: loading }" />
        </button>
      </div>
      <div class="engine-note">
        <Icon name="info" :size="14" />
        <span>生图与文本模型使用独立进程。<b>8GB 显存下两者不能同时运行</b>：启动生图会先卸载文本模型，之后在「模型」页加载文本模型也会自动停掉生图引擎。首次加载模型 + 出图通常需要 30~90 秒。</span>
      </div>
      <div v-if="lastError" class="error-box">{{ lastError }}</div>
      <div v-if="running && !online && process.log_tail && process.log_tail.length" class="log-tail mono">
        {{ process.log_tail.slice(-6).join('\n') }}
      </div>
    </section>

    <div class="gen-grid">
      <!-- 参数表单 -->
      <section class="card gen-form">
        <div class="card-head"><h3><Icon name="params" :size="16" />生成参数</h3></div>

        <label class="field">
          <span>生图模型预设</span>
          <div v-if="profiles.length" class="profile-list">
            <button
              v-for="p in profiles"
              :key="p.id"
              class="profile-item"
              :class="{ active: p.id === activeProfile }"
              :disabled="switching || starting || running"
              :title="p.note || ''"
              @click="useProfile(p)"
            >
              <span class="profile-radio"><span v-if="p.id === activeProfile" class="profile-dot"></span></span>
              <span class="profile-body">
                <span class="profile-name">{{ p.name }}</span>
                <span class="profile-mode" :class="p.mode === 'components' ? 'multi' : 'single'">
                  {{ p.mode === 'components' ? `多组件 · ${profileFileCount(p)} 个文件 · 慢/高质` : '单文件 · 快速' }}
                </span>
                <span v-if="profileFiles(p).length" class="profile-files mono">
                  {{ profileFiles(p).map((f) => f.name).join(' + ') }}
                </span>
              </span>
              <Icon v-if="p.id === activeProfile" name="check" :size="15" />
            </button>
            <div v-if="activeProfileObj && activeProfileObj.note" class="profile-note">
              <Icon name="info" :size="13" />{{ activeProfileObj.note }}
            </div>
            <div v-if="activeProfileObj && activeFiles.length" class="active-files">
              <div class="af-head">
                <Icon name="layers" :size="13" />
                <b>当前预设实际加载的 {{ activeFiles.length }} 个文件</b>
                <span class="hint">（一套模型，不是 3 个可选模型）</span>
              </div>
              <div v-for="(f, i) in activeFiles" :key="i" class="af-row">
                <span class="af-role">{{ f.role }}</span>
                <span class="af-name mono" :title="f.path">{{ f.name }}</span>
              </div>
              <div class="af-tip">
                启动时把它们<b>一起</b>交给 sd-server：主干负责去噪，文本编码器理解提示词，VAE 负责图像压缩/还原。
                所以这里选的是「一整套模型」，不会被拆成三个独立选项。
              </div>
            </div>
          </div>
          <div v-else class="hint">还没有模型预设。请在「设置 → 生图引擎」里配置，或直接在下方选择单文件模型。</div>
          <div v-if="running" class="hint" style="margin-top: 5px">引擎运行中，停止后才能切换预设。</div>
        </label>

        <!-- 高级：直接选单文件模型（无预设或调试用） -->
        <label v-if="!profiles.length || (activeProfileObj && activeProfileObj.mode === 'single')" class="field">
          <span>单文件模型（高级 · 直接覆盖预设路径）</span>
          <select v-model="model" :disabled="running">
            <option value="" disabled>{{ checkpointModels.length ? '选择模型' : '没有扫描到模型，请在设置里配置模型目录' }}</option>
            <option v-for="m in checkpointModels" :key="m.id" :value="m.path">
              {{ m.name }} · {{ m.size_mb }} MB
            </option>
          </select>
        </label>

        <!-- 底图区（图生图） -->
        <div class="init-box" :class="{ has: !!initImage }">
          <div class="init-head">
            <span class="init-label"><Icon name="image" :size="14" />底图（图生图）</span>
            <span class="hint">留空 = 文生图</span>
          </div>
          <div v-if="initImage" class="init-preview">
            <img :src="initImage" alt="底图预览" />
            <div class="init-meta">
              <div class="hint" style="word-break: break-all">{{ initImageName }}</div>
              <button class="btn ghost sm" @click="clearInit"><Icon name="close" :size="13" />移除底图</button>
            </div>
          </div>
          <label v-else class="init-drop">
            <Icon name="image" :size="22" />
            <span>点击选图，或直接 <b>Ctrl+V</b> 粘贴截图</span>
            <input type="file" accept="image/*" :disabled="initBusy" @change="pickInitFile" />
          </label>
          <div v-if="initImage" class="field strength-field">
            <span><Icon name="params" :size="13" />重绘强度 <b class="text-accent">{{ strength.toFixed(2) }}</b></span>
            <input v-model.number="strength" type="range" min="0.05" max="1" step="0.05" />
            <div class="hint">越小越接近原图（适合改风格/上色），越大越自由发挥（接近重新画）。</div>
          </div>
        </div>

        <label class="field">
          <span>正向提示词</span>
          <textarea v-model="prompt" rows="4" placeholder="例如：一只坐在雪地里的红色狐狸，清晨，电影级打光，高细节"></textarea>
        </label>

        <label class="field">
          <span>反向提示词</span>
          <textarea v-model="negative" rows="2" placeholder="例如：低质量，模糊，变形，多余的手指"></textarea>
        </label>

        <div class="row wrap field-row">
          <label class="field">
            <span>尺寸</span>
            <select v-model="size"><option v-for="s in sizes" :key="s" :value="s">{{ s }}</option></select>
          </label>
          <label class="field">
            <span>步数</span>
            <input v-model.number="steps" type="number" min="1" max="100" />
          </label>
          <label class="field">
            <span>CFG</span>
            <input v-model.number="cfg" type="number" min="1" max="30" step="0.5" />
          </label>
        </div>

        <div class="row wrap field-row">
          <label class="field">
            <span>采样器</span>
            <select v-model="sampler"><option v-for="s in samplers" :key="s" :value="s">{{ s }}</option></select>
          </label>
          <label class="field">
            <span>种子（-1 随机）</span>
            <input v-model.number="seed" type="number" />
          </label>
        </div>

        <button class="btn primary gen-btn" :disabled="generating || !online" @click="generate">
          <Icon :name="generating ? 'refresh' : 'sparkles'" :size="16" />
          {{ generating ? '生成中…请稍候' : (initImage ? '基于底图生成' : '生成图片') }}
        </button>

        <!-- 实时进度：步数 / 百分比 / 速度 / 剩余时间 -->
        <div v-if="generating" class="gen-progress">
          <template v-if="genProgress && genProgress.total > 0">
            <div class="gp-head">
              <span class="gp-stats">
                <b class="text-accent">{{ genProgress.percent }}%</b>
                <span class="gp-sep">·</span>
                步 <b>{{ genProgress.step }}/{{ genProgress.total }}</b>
              </span>
              <span class="gp-metrics">
                <span v-if="speedText(genProgress)" class="gp-speed"><Icon name="activity" :size="12" />{{ speedText(genProgress) }}</span>
                <span v-if="genProgress.eta" class="gp-eta">剩余 {{ genProgress.eta }}</span>
              </span>
            </div>
            <div class="gp-track"><div class="gp-fill" :style="{ width: genProgress.percent + '%' }"></div></div>
            <div class="hint gp-note">正在采样，多组件大模型（Qwen-Image）首步 conditioning 较慢，之后速度会稳定。</div>
          </template>
          <div v-else class="hint" style="text-align: center; padding: 6px 0">
            正在准备模型与提示词…这一步（含首次加载）通常需要几十秒到几分钟。
          </div>
        </div>
      </section>

      <!-- 结果 -->
      <section class="card gen-result">
        <div class="card-head">
          <h3><Icon name="image" :size="16" />出图结果</h3>
          <div class="spacer"></div>
          <span v-if="elapsed" class="badge ok"><Icon name="clock" :size="12" />用时 {{ elapsed }} 秒</span>
        </div>
        <div v-if="!images.length && !generating" class="empty result-empty">
          <Icon name="image" :size="34" />
          <span>填好提示词后点「生成图片」，结果会显示在这里。</span>
        </div>
        <div v-else-if="generating" class="empty result-empty">
          <div class="spinner"></div>
          <span class="hint">生成中…</span>
        </div>
        <div v-else class="img-grid">
          <div v-for="(item, i) in images" :key="i" class="img-item">
            <div class="img-thumb"><img :src="item.src" :alt="`生成图片 ${i + 1}`" /></div>
            <div class="img-actions">
              <button class="btn ghost sm" @click="saveImage(item.src, i)"><Icon name="download" :size="13" />保存</button>
              <button class="btn ghost sm" title="把这张图作为底图继续修改" @click="useAsInit(item)"><Icon name="image" :size="13" />二次修改</button>
              <button v-if="item.url" class="btn ghost sm" @click="toggleShare(i)"><Icon name="globe" :size="13" />发到手机</button>
            </div>
            <!-- 手机分享面板 -->
            <div v-if="item.url && shareIdx === i" class="share-panel">
              <div class="share-title"><Icon name="globe" :size="14" />手机扫码打开 / 保存</div>
              <div class="share-body">
                <QrCode :text="item.url" :size="128" />
                <div class="share-info">
                  <div class="hint">确保手机与本机在<b>同一 WiFi</b>，且网关已启动。</div>
                  <div class="share-link mono">{{ item.url }}</div>
                  <div class="row" style="gap: 6px; margin-top: 6px">
                    <button class="btn sm" @click="copyURL(item.url)"><Icon name="copy" :size="13" />复制链接</button>
                    <span v-if="!gatewayOn" class="badge warn"><Icon name="warning" :size="12" />网关未启动</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.engine-bar .engine-mark {
  width: 32px; height: 32px; border-radius: 9px; display: grid; place-items: center;
  color: #0b0e14; background: var(--accent-grad); box-shadow: 0 4px 14px var(--accent-glow); flex: 0 0 auto;
}
html[data-theme='light'] .engine-bar .engine-mark { color: #fff; }
.engine-title { display: flex; flex-direction: column; line-height: 1.3; margin-right: 2px; }
.engine-title b { font-size: 13px; }
.engine-title .hint { font-size: 10.5px; }
.engine-state { font-size: 12px; }
.engine-model { max-width: 220px; overflow: hidden; text-overflow: ellipsis; }
.engine-note {
  display: flex; align-items: flex-start; gap: 8px; margin-top: 12px; padding: 10px 12px;
  border-radius: var(--radius-sm); background: var(--surface); border: 1px solid var(--border-soft);
  color: var(--text-mute); font-size: 12px; line-height: 1.7;
}
.engine-note .ui-icon { color: var(--accent); margin-top: 2px; flex: 0 0 auto; }
.error-box {
  margin-top: 10px; padding: 9px 12px; border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--danger) 11%, transparent);
  border: 1px solid color-mix(in srgb, var(--danger) 34%, transparent);
  color: var(--err-text); font-size: 12px; word-break: break-all;
}
.log-tail {
  margin-top: 10px; white-space: pre-wrap; max-height: 120px; overflow: auto; padding: 9px 11px;
  border-radius: var(--radius-sm); background: var(--surface); border: 1px solid var(--border-soft);
  color: var(--text-dim); font-size: 11px; line-height: 1.6;
}

.gen-grid { display: grid; grid-template-columns: minmax(320px, 420px) 1fr; gap: 16px; margin-top: 16px; }
@media (max-width: 900px) { .gen-grid { grid-template-columns: 1fr; } }
.gen-form .field { margin-bottom: 14px; display: flex; flex-direction: column; gap: 5px; }
.gen-form .field > span { font-size: 12px; color: var(--text-dim); font-weight: 600; }

/* 生图模型预设选择器 */
.profile-list { display: flex; flex-direction: column; gap: 7px; }
.profile-item {
  display: flex; align-items: center; gap: 10px; width: 100%; text-align: left;
  padding: 10px 11px; border-radius: var(--radius-sm); border: 1px solid var(--border);
  background: var(--surface); color: var(--text); cursor: pointer; transition: border-color 0.18s, background 0.18s, box-shadow 0.18s;
}
.profile-item:hover:not(:disabled) { border-color: var(--accent); background: var(--panel-hover); }
.profile-item.active { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 9%, var(--surface)); box-shadow: 0 0 0 1px color-mix(in srgb, var(--accent) 30%, transparent); }
.profile-item:disabled { opacity: 0.6; cursor: not-allowed; }
.profile-radio { width: 16px; height: 16px; border-radius: 50%; border: 2px solid var(--border-strong); flex: 0 0 auto; display: grid; place-items: center; }
.profile-item.active .profile-radio { border-color: var(--accent); }
.profile-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--accent); }
.profile-body { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.profile-name { font-size: 12.5px; font-weight: 600; }
.profile-mode { font-size: 10.5px; color: var(--text-mute); }
.profile-mode.multi { color: var(--warn); }
.profile-mode.single { color: var(--ok-text, var(--accent)); }
.profile-files {
  font-size: 10px; color: var(--text-mute); line-height: 1.5;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 100%;
}
.profile-item > .ui-icon { color: var(--accent); flex: 0 0 auto; }
.profile-note {
  display: flex; align-items: flex-start; gap: 6px; margin-top: 2px; padding: 8px 10px;
  border-radius: var(--radius-xs); background: color-mix(in srgb, var(--accent) 6%, var(--surface));
  border: 1px solid var(--border-soft); color: var(--text-mute); font-size: 11px; line-height: 1.6;
}
.profile-note .ui-icon { color: var(--accent); flex: 0 0 auto; margin-top: 1px; }
.active-files {
  margin-top: 8px; padding: 9px 10px; border-radius: var(--radius-xs);
  background: var(--surface); border: 1px solid var(--border-soft);
}
.af-head { display: flex; align-items: center; gap: 6px; font-size: 11.5px; margin-bottom: 7px; }
.af-head .ui-icon { color: var(--accent); }
.af-head .hint { margin-left: auto; font-size: 10px; }
.af-row { display: flex; align-items: center; gap: 8px; padding: 3px 0; font-size: 10.5px; }
.af-role {
  flex: 0 0 72px; color: var(--text-mute);
}
.af-name { color: var(--text-dim); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.af-tip { margin-top: 6px; color: var(--text-mute); font-size: 10px; line-height: 1.6; }
.af-tip b { color: var(--text-dim); }
.gen-form .field-row .field { flex: 1; min-width: 96px; margin-bottom: 0; }
.gen-btn { width: 100%; margin-top: 6px; padding: 10px; }

/* 底图区 */
.init-box {
  border: 1px dashed var(--border-strong); border-radius: var(--radius-sm);
  padding: 11px 12px; margin-bottom: 14px; background: var(--surface); transition: border-color 0.2s;
}
.init-box.has { border-style: solid; border-color: color-mix(in srgb, var(--accent) 40%, transparent); }
.init-head { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.init-label { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; font-weight: 600; color: var(--text-dim); }
.init-head .hint { margin-left: auto; font-size: 11px; }
.init-drop {
  position: relative; display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 6px; min-height: 84px; border-radius: var(--radius-xs); cursor: pointer;
  color: var(--text-mute); font-size: 12px; text-align: center; transition: background 0.15s, color 0.15s;
}
.init-drop:hover { background: var(--panel-hover); color: var(--text); }
.init-drop .ui-icon { color: var(--accent); opacity: 0.7; }
.init-drop input[type='file'] { position: absolute; inset: 0; opacity: 0; cursor: pointer; height: 100%; }
.init-preview { display: flex; gap: 10px; align-items: center; }
.init-preview img {
  width: 72px; height: 72px; object-fit: cover; border-radius: 8px; border: 1px solid var(--border); flex: 0 0 auto;
}
.init-meta { flex: 1; display: flex; flex-direction: column; align-items: flex-start; gap: 6px; min-width: 0; }
.strength-field { margin-top: 11px !important; margin-bottom: 2px !important; }
.strength-field > span { display: flex; align-items: center; gap: 6px; }
.strength-field input[type='range'] { width: 100%; }

.result-empty { min-height: 300px; }
.img-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 14px; }
.img-item { display: flex; flex-direction: column; }
.img-thumb {
  border-radius: 10px; overflow: hidden; border: 1px solid var(--border); background: var(--surface);
  margin-bottom: 8px; transition: border-color 0.2s, box-shadow 0.2s, transform 0.2s;
}
.img-thumb:hover { border-color: var(--accent); box-shadow: 0 8px 24px var(--accent-glow); transform: translateY(-2px); }
.img-item img { width: 100%; aspect-ratio: 1 / 1; object-fit: cover; display: block; }
.img-actions { display: flex; flex-wrap: wrap; gap: 5px; }
.share-panel {
  margin-top: 10px; padding: 12px; border-radius: var(--radius-sm);
  background: var(--surface); border: 1px solid color-mix(in srgb, var(--accent) 30%, transparent);
  animation: share-in 0.2s ease-out;
}
@keyframes share-in { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: translateY(0); } }
.share-title { display: flex; align-items: center; gap: 6px; font-size: 12px; font-weight: 600; color: var(--accent); margin-bottom: 10px; }
.share-body { display: flex; gap: 12px; align-items: flex-start; }
.share-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 6px; }
.share-link { font-size: 10px; color: var(--text-dim); background: var(--panel); border: 1px solid var(--border-soft);
  border-radius: 6px; padding: 6px 8px; word-break: break-all; line-height: 1.5; }
.spin { animation: spin 0.9s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

/* 生成进度条 */
.gen-progress {
  margin-top: 12px; padding: 11px 12px; border-radius: var(--radius-sm);
  background: var(--surface); border: 1px solid color-mix(in srgb, var(--accent) 26%, var(--border));
}
.gp-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 8px; }
.gp-stats { font-size: 12px; color: var(--text-dim); }
.gp-stats b { color: var(--text); }
.gp-stats .text-accent { font-size: 14px; }
.gp-sep { margin: 0 5px; color: var(--text-mute); }
.gp-metrics { display: flex; align-items: center; gap: 10px; font-size: 11px; color: var(--text-dim); }
.gp-speed { display: inline-flex; align-items: center; gap: 4px; color: var(--accent); font-weight: 600; }
.gp-eta { font-variant-numeric: tabular-nums; }
.gp-track {
  height: 7px; border-radius: 99px; overflow: hidden;
  background: color-mix(in srgb, var(--border) 70%, transparent);
}
.gp-fill {
  height: 100%; border-radius: 99px; background: var(--accent-grad);
  box-shadow: 0 0 10px var(--accent-glow); transition: width 0.6s ease-out;
}
.gp-note { margin-top: 7px; font-size: 10.5px; line-height: 1.5; }
</style>
