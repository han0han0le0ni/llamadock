<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { api } from '../api'
import { chatTemplates, findTemplate } from '../chatTemplates'
import Icon from '../components/Icon.vue'
import {
  state,
  notify,
  refreshConfig,
  refreshParams,
  refreshStatus,
  refreshEngineDetail,
  refreshModelRoots,
  engineLabel,
  IMAGE_FEATURE_ENABLED,
} from '../store'

const form = reactive({
  host: '',
  port: 8080,
  autoStart: true,
  normalizeMessages: true,
  lockLoadedModel: false,
  theme: 'dark',
  minimizeToTray: true,
  openBrowser: true,
  logLevel: 'info',
})
// 引擎配置（对应 config.engine）
const eng = reactive({
  exe_path: '',
  host: '127.0.0.1',
  port_min: 10001,
  port_max: 10099,
  auto_load: true,
  auto_detect: true,
  keep_alive: false,
})
// 运行时搜索目录：用多行文本编辑，保存时按行拆开
const runtimeDirsText = ref('')
// 生图引擎配置（对应 config.image，stable-diffusion.cpp）
const img = reactive({
  enabled: false,
  exe_path: '',
  model_dir: '',
  model_path: '',
  extra_args: '',
  port: 1234,
  backend: 'cuda',
  offload_to_cpu: true,
  unload_llm: true,
  default_width: 512,
  default_height: 512,
  default_steps: 25,
  default_cfg_scale: 7,
  output_dir: '',
})
const imgPickBusy = ref(false)
const svc = reactive({
  max_concurrent_requests: 4,
  request_timeout: 300,
  queue_policy: 'queue',
  allow_auto_load: true,
  idle_unload_minutes: 30,
  vram_reserve_mb: 512,
  advisor_conservative: 1.0,
})
// 模型名 → Chat 模板自动套用规则
const tplAuto = reactive({
  enabled: true,
  rules: [], // 每项：{ pattern, template_id, mode, text, label }
})

const saving = ref(false)
const detecting = ref(false)
const newRoot = ref('')

// 一键安装 llama-server：后台任务 + 轮询进度
const installVariant = ref('auto')
const installState = ref(null)
const installVariants = ref(['auto', 'cuda-13.4', 'cuda-12.4', 'vulkan', 'cpu'])
let installTimer = null

const installing = computed(() => !!(installState.value && installState.value.running))

const variantLabel = {
  auto: '自动（按硬件选择）',
  'cuda-13.4': 'CUDA 13.4（N 卡最快）',
  'cuda-12.4': 'CUDA 12.4',
  vulkan: 'Vulkan（体积小，通用）',
  cpu: 'CPU（不占显卡）',
}

const cfg = computed(() => state.config)
const st = computed(() => state.status)
const detail = computed(() => state.engineDetail)
const runtimes = computed(() => (detail.value && detail.value.runtimes) || [])
const roots = computed(() => {
  if (state.modelRoots && state.modelRoots.length) return state.modelRoots
  const c = cfg.value
  return (c && c.model_roots) || []
})

watch(
  cfg,
  (c) => {
    if (!c) return
    form.host = c.server.host
    form.port = c.server.port
    form.autoStart = c.server.auto_start_gateway
    // 后端指针字段：undefined/null 表示默认开启，只有显式 false 才关闭
    form.normalizeMessages = c.server.normalize_messages !== false
    form.lockLoadedModel = !!c.server.lock_loaded_model
    form.theme = c.ui.theme
    form.minimizeToTray = c.ui.minimize_to_tray
    form.openBrowser = c.ui.open_browser
    form.logLevel = c.ui.log_level

    const e = c.engine || {}
    eng.exe_path = e.exe_path || ''
    eng.host = e.host || '127.0.0.1'
    eng.port_min = e.port_min || 10001
    eng.port_max = e.port_max || 10099
    eng.auto_load = e.auto_load !== false
    eng.auto_detect = e.auto_detect !== false
    eng.keep_alive = !!e.keep_alive
    runtimeDirsText.value = (e.runtime_dirs || []).join('\n')

    const im = c.image || {}
    img.enabled = !!im.enabled
    img.exe_path = im.exe_path || ''
    img.model_dir = im.model_dir || ''
    img.model_path = im.model_path || ''
    img.extra_args = im.extra_args || ''
    img.port = im.port || 1234
    img.backend = im.backend || 'cuda'
    img.offload_to_cpu = im.offload_to_cpu !== false
    img.unload_llm = im.unload_llm !== false
    img.default_width = im.default_width || 512
    img.default_height = im.default_height || 512
    img.default_steps = im.default_steps || 25
    img.default_cfg_scale = im.default_cfg_scale || 7
    img.output_dir = im.output_dir || ''

    // 模板自动映射：mode/text 优先匹配内置模板 id 供下拉回显，匹配不到记为自定义
    tplAuto.enabled = c.template_auto_enabled !== false
    tplAuto.rules = (c.template_auto_map || []).map((r) => ({
      pattern: r.pattern || '',
      template_id: matchTemplateId(r.mode, r.text, r.label),
      mode: r.mode || 'jinja',
      text: r.text || '',
      label: r.label || '',
    }))
  },
  { immediate: true },
)

// 反查内置模板 id 用于下拉回显：先按 mode+text 精确匹配，再按 label 匹配（兼容后端正文的转义差异）。
// 找不到返回 '__custom__'（保留原文本，功能不受影响）。
function matchTemplateId(mode, text, label) {
  let t = chatTemplates.find((x) => x.mode === mode && x.text === text)
  if (!t && label) {
    const short = String(label).split(' · ')[0]
    t = chatTemplates.find((x) => x.label.split(' · ')[0] === short)
  }
  return t ? t.id : '__custom__'
}

watch(
  () => state.params,
  (p) => {
    if (!p) return
    Object.keys(svc).forEach((k) => {
      if (p.values[k] !== undefined) svc[k] = p.values[k]
    })
  },
  { immediate: true },
)

function parseDirs(text) {
  return text
    .split('\n')
    .map((s) => s.trim().replace(/^"|"$/g, ''))
    .filter(Boolean)
}

function enginePatch() {
  return {
    exe_path: eng.exe_path.trim(),
    runtime_dirs: parseDirs(runtimeDirsText.value),
    host: eng.host.trim() || '127.0.0.1',
    port_min: Number(eng.port_min) || 10001,
    port_max: Number(eng.port_max) || 10099,
    auto_load: !!eng.auto_load,
    auto_detect: !!eng.auto_detect,
    keep_alive: !!eng.keep_alive,
  }
}

async function reloadEngineState() {
  await refreshConfig()
  await refreshEngineDetail()
  await refreshStatus()
}

async function saveEngine() {
  saving.value = true
  try {
    state.config = await api.saveConfig({ engine: enginePatch() })
    const list = (await api.detectRuntimes().catch(() => ({ runtimes: [] }))).runtimes || []
    await reloadEngineState()
    if (!list.length) {
      notify(
        '已保存，但仍没找到 llama-server.exe：请到下面的「探测到的运行时」说明里看怎么放一份自带运行时',
        'warn',
        8000,
      )
    } else {
      const cur = list.find((r) => r.current) || list[0]
      notify(`已保存引擎设置，将使用 ${cur.variant} 运行时（${cur.exe}）`, 'ok', 6000)
    }
  } catch (e) {
    notify(e.message, 'err', 6000)
  } finally {
    saving.value = false
  }
}

function imagePatch() {
  return {
    enabled: !!img.enabled,
    exe_path: img.exe_path.trim(),
    model_dir: img.model_dir.trim(),
    model_path: img.model_path.trim(),
    extra_args: (img.extra_args || '').trim(),
    port: Number(img.port) || 1234,
    backend: img.backend,
    offload_to_cpu: !!img.offload_to_cpu,
    unload_llm: !!img.unload_llm,
    default_width: Number(img.default_width) || 512,
    default_height: Number(img.default_height) || 512,
    default_steps: Number(img.default_steps) || 25,
    default_cfg_scale: Number(img.default_cfg_scale) || 7,
    output_dir: (img.output_dir || '').trim(),
  }
}

async function saveImage() {
  saving.value = true
  try {
    state.config = await api.saveConfig({ image: imagePatch() })
    notify('生图引擎设置已保存', 'ok')
  } catch (e) {
    notify(e.message, 'err', 6000)
  } finally {
    saving.value = false
  }
}

async function pickImageExe() {
  imgPickBusy.value = true
  try {
    const r = await api.pickFile('选择 sd-server.exe', img.exe_path, 'exe')
    if (r && r.path) img.exe_path = r.path
  } catch (e) {
    notify(e.message, 'err')
  } finally {
    imgPickBusy.value = false
  }
}

async function pickImageDir() {
  imgPickBusy.value = true
  try {
    const r = await api.pickFolder('选择生图模型目录', img.model_dir)
    if (r && r.path) img.model_dir = r.path
  } catch (e) {
    notify(e.message, 'err')
  } finally {
    imgPickBusy.value = false
  }
}

async function rescan() {
  try {
    const info = await api.rescanEngine()
    await refreshEngineDetail()
    await reloadEngineState()
    const h = info.health || {}
    notify(
      h.running
        ? `llama-server 运行中：PID ${h.pid}${h.port ? ' · 端口 ' + h.port : ''}${h.variant ? ' · ' + h.variant : ''}`
        : 'llama-server 未运行（尚未加载模型，或已被空闲卸载）',
      h.running ? 'ok' : 'warn',
      6000,
    )
    state.models = (await api.models()) || []
  } catch (e) {
    notify(e.message, 'err', 6000)
  }
}

async function detectRuntimes() {
  detecting.value = true
  try {
    const list = (await api.detectRuntimes()).runtimes || []
    await refreshEngineDetail()
    notify(
      list.length ? `发现 ${list.length} 个 llama-server 运行时` : '没有找到 llama-server.exe，看下面的说明',
      list.length ? 'ok' : 'warn',
      6000,
    )
  } catch (e) {
    notify(e.message, 'err', 6000)
  } finally {
    detecting.value = false
  }
}

function useRuntime(rt) {
  eng.exe_path = rt.exe
  notify('已填入该运行时路径，点「保存引擎设置」生效', 'info', 4000)
}

async function addRoot() {
  const p = newRoot.value.trim()
  if (!p) return
  try {
    const r = await api.addModelRoot(p)
    state.modelRoots = r.roots || []
    newRoot.value = ''
    state.config = await api.config()
    state.models = (await api.models()) || []
    notify(`已添加模型根目录（当前 ${state.models.length} 个模型）`, 'ok', 4000)
  } catch (e) {
    notify(e.message, 'err', 6000)
  }
}

async function dropRoot(path) {
  try {
    const r = await api.removeModelRoot(path)
    state.modelRoots = r.roots || []
    state.config = await api.config()
    state.models = (await api.models()) || []
    notify('已移除（磁盘上的文件未删除）', 'ok', 4000)
  } catch (e) {
    notify(e.message, 'err', 6000)
  }
}

async function saveService() {
  saving.value = true
  try {
    state.config = await api.saveConfig({
      server: {
        host: form.host,
        port: Number(form.port),
        auto_start_gateway: form.autoStart,
        normalize_messages: form.normalizeMessages,
        lock_loaded_model: form.lockLoadedModel,
      },
      ui: {
        theme: form.theme,
        minimize_to_tray: form.minimizeToTray,
        open_browser: form.openBrowser,
        log_level: form.logLevel,
      },
    })
    await api.saveParams({ ...svc, advisor_conservative: Number(svc.advisor_conservative) })
    state.params = await api.params()
    notify('设置已保存（监听地址/端口变更需重启网关或程序后生效）', 'ok', 4200)
    await refreshStatus()
  } catch (e) {
    notify(e.message, 'err', 6000)
  } finally {
    saving.value = false
  }
}

// —— 模型名 → 模板自动映射 ——
// 默认规则只存「关键字 + 模板 id」，正文从 chatTemplates 取，避免与后端模板正文漂移。
// 顺序与后端 config.DefaultTemplateAutoMap 一致（具体在前）。
const defaultRuleSpecs = [
  { pattern: 'qwen', template_id: 'qwen25' },
  { pattern: 'llama-3', template_id: 'llama3' },
  { pattern: 'llama3', template_id: 'llama3' },
  { pattern: 'gemma', template_id: 'gemma' },
  { pattern: 'chatml', template_id: 'chatml' },
  { pattern: 'openchat', template_id: 'chatml' },
]

function restoreDefaultRules() {
  tplAuto.rules = defaultRuleSpecs.map((s) => {
    const r = { pattern: s.pattern, template_id: s.template_id, mode: 'jinja', text: '', label: '' }
    onTplRulePick(r, s.template_id)
    return r
  })
  notify('已载入默认规则，记得点「保存映射规则」', 'info', 3500)
}

function addTplRule() {
  tplAuto.rules.push({ pattern: '', template_id: 'loose', mode: 'jinja', text: '', label: '' })
}

function removeTplRule(i) {
  tplAuto.rules.splice(i, 1)
}

// 下拉选择内置模板时，把 mode/text/label 同步进规则；自定义项保留原值
function onTplRulePick(rule, id) {
  rule.template_id = id
  if (id === '__custom__') return
  const t = findTemplate(id)
  if (!t) return
  rule.mode = t.mode
  rule.text = t.text
  rule.label = t.label.split(' · ')[0]
}

async function saveTemplateAuto() {
  saving.value = true
  try {
    // 校验：pattern 不能为空，且必须选了有效模板（有正文或 auto）
    const rules = []
    for (const r of tplAuto.rules) {
      const pattern = (r.pattern || '').trim().toLowerCase()
      if (!pattern) continue
      if (r.mode === 'auto') {
        rules.push({ pattern, mode: 'auto', text: '', label: r.label || '模型自带模板' })
        continue
      }
      if (!r.text || !r.text.trim()) continue
      rules.push({ pattern, mode: r.mode, text: r.text, label: r.label || '' })
    }
    state.config = await api.saveConfig({
      template_auto_enabled: !!tplAuto.enabled,
      template_auto_map: rules,
    })
    await refreshConfig()
    notify(`已保存 ${rules.length} 条模板映射规则（下次加载模型时生效）`, 'ok', 4200)
  } catch (e) {
    notify(e.message, 'err', 6000)
  } finally {
    saving.value = false
  }
}

// —— 原生选择框：对话框开在运行本程序的机器上 ——
async function browseModelRoot() {
  try {
    const r = await api.pickFolder('选择存放 .gguf 模型的文件夹', newRoot.value)
    if (r && r.path) {
      newRoot.value = r.path
      await addRoot() // 选完即加，少点一次
    }
  } catch (e) {
    notify(e.message, 'err', 6000)
  }
}

async function browseExe() {
  try {
    const init = eng.exe_path ? eng.exe_path.replace(/[\\/][^\\/]*$/, '') : ''
    const r = await api.pickFile('选择 llama-server.exe', init, '可执行文件 (*.exe)|*.exe|所有文件 (*.*)|*.*')
    if (r && r.path) eng.exe_path = r.path
  } catch (e) {
    notify(e.message, 'err', 6000)
  }
}

async function browseRuntimeDir() {
  try {
    const r = await api.pickFolder('选择 llama.cpp 运行时目录', '')
    if (r && r.path) {
      const cur = runtimeDirsText.value.trim()
      runtimeDirsText.value = cur ? cur + '\n' + r.path : r.path
    }
  } catch (e) {
    notify(e.message, 'err', 6000)
  }
}

// —— 一键安装 ——
async function refreshInstall() {
  try {
    const r = await api.engineInstallState()
    installState.value = r.state
    if (r.variants && r.variants.length) installVariants.value = r.variants
    if (r.state && r.state.running) ensureInstallPoll()
    else stopInstallPoll()
  } catch (e) {
    /* 静默：安装状态不是主流程 */
  }
}

function ensureInstallPoll() {
  if (installTimer) return
  installTimer = setInterval(refreshInstall, 1000)
}

function stopInstallPoll() {
  if (installTimer) {
    clearInterval(installTimer)
    installTimer = null
  }
}

async function installLlama() {
  try {
    installState.value = await api.installEngine(installVariant.value)
    ensureInstallPoll()
    notify('已开始下载安装 llama-server（后台进行，可切到别的页面）', 'info', 6000)
  } catch (e) {
    notify(e.message, 'err', 7000)
  }
}

// 安装完成后刷新一次运行时列表，让「探测到的运行时」立刻出现新装的这份
watch(
  () => installState.value && installState.value.stage,
  async (stage, prev) => {
    if (stage === 'done' && prev !== 'done') {
      await reloadEngineState()
      await detectRuntimes()
      state.models = (await api.models()) || []
    }
  },
)

onMounted(async () => {
  await refreshConfig()
  await refreshParams()
  await refreshEngineDetail()
  await refreshModelRoots()
  await refreshInstall()
})

onUnmounted(stopInstallPoll)
</script>

<template>
  <div class="grid cols-2" style="gap: 16px">
    <!-- 保存分区说明：设置分两类，各有就近的保存按钮 -->
    <div class="card tight" style="grid-column: 1 / -1; border-color: var(--accent)">
      <div class="hint" style="color: var(--text-dim)">
        <b>关于保存</b>　设置分两类，改完点所在卡片内的按钮即可生效：
        <span class="badge info">推理引擎</span> 用「保存引擎设置」；
        <span class="badge">网关 · 界面 · 推荐参数</span> 用各自卡片内的「保存…」按钮（会被一起写入）。
        引擎配置与其它设置分开保存，请勿混淆。
      </div>
    </div>

    <!-- 引擎：这是整个程序的心脏，自己拉起 llama-server -->
    <div class="card" style="grid-column: 1 / -1">
      <div class="card-head">
        <h3><Icon name="cpu" :size="16" />推理引擎</h3>
        <div class="spacer"></div>
        <span class="badge info">自管 · 可调度</span>
        <span class="badge" :class="st && st.engine.health.online ? 'ok' : 'err'">
          {{ st && st.engine.health.online ? '运行中' : '未运行' }}
        </span>
        <button class="sm" @click="rescan"><Icon name="refresh" :size="13" />重新探测</button>
      </div>

      <div class="grid cols-2" style="gap: 12px">
        <label class="field">
          <span>llama-server.exe 路径（留空则从下面的探测结果里自动择优）</span>
          <div class="row" style="gap: 6px">
            <input v-model="eng.exe_path" placeholder="例如 D:\\llama.cpp\\llama-server.exe" style="flex: 1" />
            <button class="sm" type="button" @click="browseExe">浏览…</button>
          </div>
        </label>
        <label class="field">
          <span>监听地址（只绑回环，局域网不可见）</span>
          <input v-model="eng.host" placeholder="127.0.0.1" />
        </label>
      </div>

      <div class="grid cols-2" style="gap: 12px">
        <label class="field">
          <span>端口池下界</span>
          <input type="number" v-model.number="eng.port_min" />
        </label>
        <label class="field">
          <span>端口池上界（每次加载从中挑一个空闲端口，仅本机可访问）</span>
          <input type="number" v-model.number="eng.port_max" />
        </label>
      </div>

      <label class="field">
        <span>额外的运行时搜索目录（每行一个，递归找 llama-server.exe）</span>
        <textarea v-model="runtimeDirsText" rows="2" placeholder="例如&#10;D:\\llama.cpp\\build\\bin"></textarea>
        <button class="sm" type="button" style="align-self: flex-start" @click="browseRuntimeDir">浏览并添加目录…</button>
      </label>

      <div class="grid cols-3" style="gap: 12px">
        <label class="row" style="gap: 8px; align-items: center">
          <input type="checkbox" v-model="eng.auto_load" style="width: auto" />
          <span class="hint">请求未加载模型时自动拉起</span>
        </label>
        <label class="row" style="gap: 8px; align-items: center">
          <input type="checkbox" v-model="eng.auto_detect" style="width: auto" />
          <span class="hint">启动时探测运行时与模型</span>
        </label>
        <label class="row" style="gap: 8px; align-items: center">
          <input type="checkbox" v-model="eng.keep_alive" style="width: auto" />
          <span class="hint">空闲也常驻（否则按网关的空闲卸载分钟数停掉）</span>
        </label>
      </div>

      <div class="row" style="margin-top: 12px">
        <button class="primary" :disabled="saving" @click="saveEngine"><Icon name="check" :size="14" />保存引擎设置</button>
        <button :disabled="detecting" @click="detectRuntimes">
          <Icon name="search" :size="14" />{{ detecting ? '探测中…' : '重新探测 llama-server' }}
        </button>
      </div>

      <!-- 一键安装：官方 release → 运行时目录，装完自动写回 exe 路径 -->
      <div class="card tight" style="margin-top: 14px">
        <div class="card-head">
          <h3 style="font-size: 14px"><Icon name="download" :size="15" />一键安装 llama-server</h3>
          <div class="spacer"></div>
          <span class="badge" :class="installing ? 'info' : ''">{{ installing ? '进行中' : '空闲' }}</span>
        </div>
        <div class="hint" style="margin-bottom: 8px">
          从官方 GitHub Release 下载 Windows 构建并解压到
          <span class="mono">{{ (detail && detail.default_dir) || 'runtime\\llama.cpp' }}</span>，
          完成后自动写入上面的引擎路径。选 CUDA 会连同 cudart 运行库一起装。
        </div>
        <div class="row wrap" style="gap: 8px">
          <select v-model="installVariant" style="max-width: 220px" :disabled="installing">
            <option v-for="v in installVariants" :key="v" :value="v">{{ variantLabel[v] || v }}</option>
          </select>
          <button class="primary sm" :disabled="installing" @click="installLlama">
            {{ installing ? '安装中…' : '下载并安装' }}
          </button>
        </div>

        <div v-if="installState && (installState.running || installState.stage)" style="margin-top: 10px">
          <div class="bar">
            <i class="used" :style="{ width: (installState.percent || 0) + '%' }"></i>
          </div>
          <div
            class="hint"
            style="margin-top: 6px"
            :style="installState.stage === 'error' ? 'color:var(--err-text)' : installState.stage === 'done' ? 'color:var(--ok-text)' : ''"
          >
            {{ installState.message || installState.stage }}
            <span v-if="installState.tag" class="mono">
              · {{ installState.tag }}{{ installState.variant ? ' / ' + installState.variant : '' }}
            </span>
          </div>
          <div v-if="installState.exe_path" class="hint mono" style="margin-top: 4px; word-break: break-all">
            {{ installState.exe_path }}
          </div>
        </div>
      </div>

      <div class="card tight" style="margin-top: 14px">
        <div class="card-head">
          <h3 style="font-size: 14px"><Icon name="layers" :size="15" />探测到的运行时</h3>
          <div class="spacer"></div>
          <span class="badge">{{ runtimes.length }} 个</span>
        </div>
        <div v-if="!runtimes.length" class="hint">
          没有找到 llama-server.exe。把官方 llama.cpp 的 Windows 压缩包解压到
          <span class="mono">{{ (detail && detail.default_dir) || '程序目录\\runtime\\llama.cpp' }}</span>
          即可，本程序会自动发现（也可以在上面直接填 exe 路径，或加进系统 PATH）。
        </div>
        <div v-else class="table-scroll">
          <table>
            <thead>
              <tr>
                <th>变体</th>
                <th>版本</th>
                <th>来源</th>
                <th>路径</th>
                <th style="width: 110px">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="rt in runtimes" :key="rt.exe">
                <td>
                  <span class="badge" :class="rt.gpu ? 'ok' : ''">{{ rt.variant }}</span>
                  <span v-if="rt.current" class="badge info">当前</span>
                </td>
                <td class="mono hint">{{ rt.version || '—' }}</td>
                <td class="hint">{{ rt.source || '—' }}</td>
                <td class="mono hint" style="word-break: break-all">{{ rt.exe }}</td>
                <td><button class="sm" @click="useRuntime(rt)">使用</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="hint" style="margin-top: 8px">
          变体优先级由硬件决定：有 N 卡用 <span class="mono">cuda</span>，否则 <span class="mono">vulkan</span>，
          再否则 <span class="mono">cpu</span>。探测结果里的第一个就是会被使用的那个。
        </div>
      </div>
    </div>

    <!-- 生图引擎：软回退后默认隐藏；源码与配置保留，识图逻辑不受影响 -->
    <template v-if="IMAGE_FEATURE_ENABLED">
    <div class="card" style="grid-column: 1 / -1">
      <div class="card-head">
        <h3><Icon name="sparkles" :size="16" />生图引擎（stable-diffusion.cpp）</h3>
        <div class="spacer"></div>
        <span class="badge" :class="img.enabled ? 'ok' : ''">{{ img.enabled ? '已启用' : '未启用' }}</span>
      </div>
      <div class="hint" style="margin-bottom: 12px; line-height: 1.7">
        生图走独立的 <span class="mono">sd-server.exe</span>，与文本模型不是同一个引擎。
        需要先从 stable-diffusion.cpp 的 GitHub Release 下载 Windows CUDA 版
        （<span class="mono">sd-master-*-bin-win-cuda12-x64.zip</span> 和
        <span class="mono">cudart-sd-bin-win-cu12-x64.zip</span>，解压到同一目录），再在下面填 exe 路径。
        8GB 显存下生图和文本不能同时跑，程序会自动互斥切换。
      </div>

      <label class="row" style="gap: 8px; margin-bottom: 12px">
        <input type="checkbox" v-model="img.enabled" style="width: auto" />
        <span>启用生图功能</span>
      </label>

      <div class="grid cols-2" style="gap: 12px">
        <label class="field">
          <span>sd-server.exe 路径</span>
          <div class="row" style="gap: 6px">
            <input v-model="img.exe_path" placeholder="例如 D:\sd.cpp\sd-server.exe" style="flex: 1" />
            <button class="sm" type="button" :disabled="imgPickBusy" @click="pickImageExe">浏览…</button>
          </div>
        </label>
        <label class="field">
          <span>生图模型目录（gguf / safetensors / ckpt）</span>
          <div class="row" style="gap: 6px">
            <input v-model="img.model_dir" placeholder="例如 E:\LM\SD\models" style="flex: 1" />
            <button class="sm" type="button" :disabled="imgPickBusy" @click="pickImageDir">浏览…</button>
          </div>
        </label>
      </div>

      <div class="hint" style="margin: 4px 0 10px; padding: 9px 11px; border-radius: var(--radius-sm); background: color-mix(in srgb, var(--accent) 7%, var(--surface)); border: 1px solid var(--border-soft); line-height: 1.6">
        生图模型预设（SD1.5 / Qwen-Image 2.1 等）在左侧「生图」页顶部切换。切换预设会自动改写下面的模型路径与额外参数，这里看到的是<b>当前预设正在使用</b>的值；一般不需要手动改。
      </div>

      <label class="field">
        <span>默认生图模型路径（sd-server 单文件模型，可留空在生图页选）</span>
        <input v-model="img.model_path" placeholder="例如 E:\LM\SD\models\v1-5-pruned.gguf" />
      </label>

      <label class="field">
        <span>出图保存目录（用于生成手机可访问的链接，留空则用配置目录下的 images 文件夹）</span>
        <input v-model="img.output_dir" placeholder="留空默认：%APPDATA%/AIRunner/images" />
        <span class="hint" style="margin-top: 4px">生成的图片会存到这里，并通过网关 <span class="mono">/v1/images/files/…</span> 提供局域网访问；最多保留最近 200 张。</span>
      </label>

      <label class="field">
        <span>额外启动参数（Flux / Z-Image 等多组件模型用，原样拼到命令行）</span>
        <input v-model="img.extra_args" placeholder='例如 --diffusion-model "E:\SD\flux.gguf" --llm "E:\SD\t5.gguf" --vae "E:\SD\ae.sft"' />
        <span class="hint" style="margin-top: 4px">单文件 SD1.5/SDXL 不用填；上面的「默认生图模型」会自动用 <span class="mono">-m</span> 传入。</span>
      </label>

      <div class="grid cols-3" style="gap: 12px">
        <label class="field">
          <span>端口</span>
          <input v-model.number="img.port" type="number" min="1" max="65535" />
        </label>
        <label class="field">
          <span>后端</span>
          <select v-model="img.backend">
            <option value="cuda">CUDA（N 卡）</option>
            <option value="vulkan">Vulkan（通用）</option>
            <option value="cpu">CPU</option>
          </select>
        </label>
        <label class="field">
          <span>默认步数</span>
          <input v-model.number="img.default_steps" type="number" min="1" max="100" />
        </label>
      </div>

      <div class="grid cols-3" style="gap: 12px">
        <label class="field">
          <span>默认宽</span>
          <input v-model.number="img.default_width" type="number" min="64" step="64" />
        </label>
        <label class="field">
          <span>默认高</span>
          <input v-model.number="img.default_height" type="number" min="64" step="64" />
        </label>
        <label class="field">
          <span>默认 CFG</span>
          <input v-model.number="img.default_cfg_scale" type="number" min="1" step="0.5" />
        </label>
      </div>

      <div class="grid cols-2" style="gap: 12px; margin-bottom: 6px">
        <label class="row" style="gap: 8px; align-items: center">
          <input type="checkbox" v-model="img.offload_to_cpu" style="width: auto" />
          <span class="hint">权重留内存按需进显存（省显存，推荐开）</span>
        </label>
        <label class="row" style="gap: 8px; align-items: center">
          <input type="checkbox" v-model="img.unload_llm" style="width: auto" />
          <span class="hint">生图前自动卸载文本模型释放显存</span>
        </label>
      </div>

      <div class="row">
        <button class="primary" :disabled="saving" @click="saveImage"><Icon name="check" :size="14" />保存生图设置</button>
        <span class="hint">保存后到左侧「生图」页启动引擎、填提示词出图。</span>
      </div>
    </div>
    </template>

    <!-- 模型根目录 -->
    <div class="card">
      <div class="card-head">
        <h3><Icon name="drive" :size="16" />模型根目录（gguf 库）</h3>
        <div class="spacer"></div>
        <span class="badge">{{ roots.length }} 个</span>
      </div>
      <div v-if="!roots.length" class="hint">还没有根目录，添加一个存放 .gguf 的文件夹即可。</div>
      <div v-for="r in roots" :key="r" class="row" style="gap: 8px; margin-bottom: 6px">
        <span class="mono hint" style="flex: 1; word-break: break-all">{{ r }}</span>
        <button class="sm ghost" @click="dropRoot(r)">移除</button>
      </div>
      <div class="row" style="margin-top: 10px; gap: 6px">
        <input v-model="newRoot" placeholder="例如 E:\\Models" style="flex: 1" @keyup.enter="addRoot" />
        <button class="sm" @click="browseModelRoot">浏览…</button>
        <button class="sm" @click="addRoot">添加</button>
      </div>
      <div class="hint" style="margin-top: 8px">
        目录不存在会自动创建；移除只从列表里去掉，磁盘上的模型文件不动。本程序扫描这些目录下的 gguf。
      </div>
    </div>

    <!-- 参数推荐 -->
    <div class="card">
      <div class="card-head"><h3><Icon name="zap" :size="16" />参数推荐</h3></div>
      <label class="field">
        <span>保守系数（显存预估算 × 该系数，1.15 更稳）</span>
        <input type="number" step="0.01" min="1" max="1.5" v-model.number="svc.advisor_conservative" />
      </label>
      <label class="field">
        <span>显存安全余量（MB）</span>
        <input type="number" step="64" v-model.number="svc.vram_reserve_mb" />
      </label>
      <div class="hint">
        推荐引擎是纯确定性算术（无 AI）：同一台机器 + 同一个模型永远得到同一组推荐值，且每条都给出算式依据。
      </div>
      <div class="row" style="margin-top: 12px">
        <button class="sm primary" :disabled="saving" @click="saveService">
          {{ saving ? '保存中…' : '保存推荐设置' }}
        </button>
      </div>
    </div>

    <!-- 模型名 → Chat 模板自动套用 -->
    <div class="card" style="grid-column: 1 / -1">
      <div class="card-head">
        <h3><Icon name="sparkles" :size="16" />模型名自动套用 Chat 模板</h3>
        <div class="spacer"></div>
        <label class="row" style="gap: 6px; margin-right: 10px">
          <input type="checkbox" v-model="tplAuto.enabled" style="width: auto" />
          <span class="hint">启用自动套用</span>
        </label>
        <button class="sm primary" :disabled="saving" @click="saveTemplateAuto">
          {{ saving ? '保存中…' : '保存映射规则' }}
        </button>
      </div>
      <div class="hint" style="margin-bottom: 10px">
        加载模型时，若你<strong>没有</strong>手动指定模板（参数页仍为 auto 且模板为空），程序会按下面的规则自动套用对应模板，
        省去逐个模型手动切换。匹配用模型名关键字（不区分大小写的子串）；<b>自上而下首条命中生效</b>，越具体的规则越靠前。
        优先级：<b>手动指定 &gt; 自动映射 &gt; 模型自带模板</b>。规则在下次加载模型时生效。
      </div>

      <div v-if="!tplAuto.rules.length" class="empty" style="margin: 6px 0">
        暂无规则。已内置常见模型族（Qwen / Llama 3 / Gemma / ChatML）的默认规则；若这里为空，点右侧「恢复默认规则」可重新载入。
      </div>

      <div v-for="(r, i) in tplAuto.rules" :key="i" class="row wrap" style="gap: 8px; margin-bottom: 8px">
        <span class="badge">{{ i + 1 }}</span>
        <input
          v-model="r.pattern"
          placeholder="模型名关键字，如 qwen2.5 / llama-3 / gemma"
          style="max-width: 320px; flex: 1 1 260px"
        />
        <span class="hint">→</span>
        <select
          :value="r.template_id"
          style="max-width: 260px; flex: 1 1 220px"
          @change="onTplRulePick(r, $event.target.value)"
        >
          <option v-for="t in chatTemplates" :key="t.id" :value="t.id">{{ t.label }}</option>
          <option value="__custom__">自定义（保留下方文本）</option>
        </select>
        <button class="sm" :disabled="saving" @click="removeTplRule(i)">删除</button>
      </div>

      <div class="row wrap" style="gap: 8px; margin-top: 10px">
        <button class="sm" :disabled="saving" @click="addTplRule"><Icon name="sparkles" :size="13" />+ 添加规则</button>
        <button class="sm" :disabled="saving" @click="restoreDefaultRules">
          <Icon name="refresh" :size="13" />恢复默认规则
        </button>
        <div class="spacer"></div>
        <span class="hint">「通用宽松」模板报错概率最低，适合拿不准的模型兜底。</span>
      </div>
    </div>

    <div class="card">
      <div class="card-head"><h3><Icon name="globe" :size="16" />局域网网关</h3></div>
      <div class="grid cols-2" style="gap: 12px">
        <label class="field">
          <span>监听地址（0.0.0.0 = 局域网可访问）</span>
          <input v-model="form.host" />
        </label>
        <label class="field">
          <span>端口</span>
          <input type="number" v-model.number="form.port" />
        </label>
      </div>
      <div class="grid cols-2" style="gap: 12px">
        <label class="field">
          <span>最大并发请求</span>
          <input type="number" v-model.number="svc.max_concurrent_requests" />
        </label>
        <label class="field">
          <span>请求超时（秒）</span>
          <input type="number" v-model.number="svc.request_timeout" />
        </label>
      </div>
      <div class="grid cols-2" style="gap: 12px">
        <label class="field">
          <span>并发满时策略</span>
          <select v-model="svc.queue_policy">
            <option value="queue">排队等待</option>
            <option value="reject">直接拒绝</option>
          </select>
        </label>
        <label class="field">
          <span>空闲自动卸载（分钟，0=不卸载）</span>
          <input type="number" v-model.number="svc.idle_unload_minutes" />
        </label>
      </div>
      <label class="row" style="gap: 8px; margin-bottom: 8px">
        <input type="checkbox" v-model="form.autoStart" style="width: auto" />
        <span class="hint">程序启动时自动开启网关</span>
      </label>
      <label class="row" style="gap: 8px; margin-bottom: 8px">
        <input type="checkbox" v-model="form.normalizeMessages" style="width: auto" />
        <span class="hint">规范化消息顺序：把 system 提到首位（兼容 Llama3/Qwen 严格模板，建议开启）</span>
      </label>
      <label class="row" style="gap: 8px; margin-bottom: 8px">
        <input type="checkbox" v-model="form.lockLoadedModel" style="width: auto" />
        <span class="hint">
          <b>锁定当前模型</b>：忽略客户端请求里的 model 字段，始终用「模型」页已加载的模型和模板。
          单引擎一次只能跑一个 GGUF，开启后外部客户端就不会把当前模型顶掉、连带模板一起切换。
          要换模型请先在「模型」页手动加载。
        </span>
      </label>
      <label class="row" style="gap: 8px">
        <input type="checkbox" v-model="svc.allow_auto_load" style="width: auto" />
        <span class="hint">允许按需自动加载（没有已加载模型时，客户端请求可自动拉起）</span>
      </label>
      <div class="row" style="margin-top: 12px">
        <button class="sm primary" :disabled="saving" @click="saveService">
          {{ saving ? '保存中…' : '保存网关设置' }}
        </button>
      </div>
    </div>

    <div class="card">
      <div class="card-head"><h3><Icon name="settings" :size="16" />界面与运行</h3></div>
      <div class="grid cols-2" style="gap: 12px">
        <label class="field">
          <span>主题</span>
          <select v-model="form.theme">
            <option value="dark">深色</option>
            <option value="light">浅色</option>
          </select>
          <div class="hint" style="margin-top: 4px">切换后立即生效（保存后应用）</div>
        </label>
        <label class="field">
          <span>日志级别</span>
          <select v-model="form.logLevel">
            <option value="debug">debug</option>
            <option value="info">info</option>
            <option value="warn">warn</option>
            <option value="error">error</option>
          </select>
        </label>
      </div>
      <label class="row" style="gap: 8px; margin-bottom: 8px">
        <input type="checkbox" v-model="form.minimizeToTray" style="width: auto" />
        <span class="hint">关闭窗口时最小化到托盘（桌面版）</span>
      </label>
      <label class="row" style="gap: 8px">
        <input type="checkbox" v-model="form.openBrowser" style="width: auto" />
        <span class="hint">headless 模式下自动打开浏览器管理台</span>
      </label>
      <div class="row" style="margin-top: 12px">
        <button class="sm primary" :disabled="saving" @click="saveService">
          {{ saving ? '保存中…' : '保存界面设置' }}
        </button>
      </div>
    </div>

    <div class="card" style="grid-column: 1 / -1">
      <div class="card-head">
        <h3><Icon name="info" :size="16" />信息</h3>
        <div class="spacer"></div>
        <button class="sm" :disabled="saving" @click="saveService">
          {{ saving ? '保存中…' : '保存网关 / 界面 / 参数' }}
        </button>
      </div>
      <div class="grid cols-3">
        <div>
          <div class="hint">引擎</div>
          <div class="mono">{{ engineLabel }}（本程序自管）</div>
        </div>
        <div>
          <div class="hint">版本</div>
          <div class="mono">{{ st ? st.version : '—' }}</div>
        </div>
        <div>
          <div class="hint">参数总数</div>
          <div class="mono">{{ st ? st.params_count : '—' }}</div>
        </div>
        <div>
          <div class="hint">管理口</div>
          <div class="mono">{{ st ? st.admin_addr : '—' }}</div>
        </div>
        <div>
          <div class="hint">配置文件</div>
          <div class="mono">%APPDATA%/AIRunner/config.json</div>
        </div>
        <div>
          <div class="hint">日志文件</div>
          <div class="mono">%APPDATA%/AIRunner/llamadock.log</div>
        </div>
      </div>
    </div>
  </div>
</template>
