<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { api } from '../api'
import ParamRow from '../components/ParamRow.vue'
import { identifyTemplate } from '../chatTemplates'
import {
  state,
  notify,
  refreshModels,
  refreshStatus,
  refreshParams,
  refreshEngineDetail,
  engineLabel,
} from '../store'

const busy = ref('')
const purpose = ref('chat')
const recoModel = ref('')
const manualPath = ref('')
const showReasons = ref(true)
// 原生模板查看/套用弹窗
const tplOpen = ref(false)
const tplModel = ref(null)
const tplApplying = ref(false)

const models = computed(() => state.models)
const reco = computed(() => state.recommendation)
const st = computed(() => state.status)
const health = computed(() => (st.value && st.value.engine && st.value.engine.health) || {})
const runtimes = computed(() => (state.engineDetail && state.engineDetail.runtimes) || [])

// 当前保存的全局模板（供原生模板弹窗回显，确认「套用」是否落盘）
const currentGlobalTemplate = computed(() =>
  state.params && state.params.values
    ? identifyTemplate(
        state.params.values.chat_template_mode,
        state.params.values.chat_template,
        state.models || [],
      )
    : null,
)
const tplMatchesCurrent = computed(
  () =>
    !!(
      tplModel.value &&
      currentGlobalTemplate.value &&
      currentGlobalTemplate.value.text &&
      currentGlobalTemplate.value.text === nativeTpl(tplModel.value)
    ),
)

const purposes = [
  { v: 'chat', l: '通用对话' },
  { v: 'code', l: '代码 / 数学' },
  { v: 'creative', l: '创意写作' },
  { v: 'long', l: '长文档 / RAG' },
]

// 加载级参数：会变成 llama-server 的启动开关
const loadKeys = ['n_gpu_layers', 'n_ctx', 'cache_type_k', 'cache_type_v', 'flash_attn', 'n_batch', 'n_threads']
const sampling = ['temperature', 'top_p', 'top_k', 'min_p', 'repeat_penalty', 'max_tokens', 'seed']

function meta(m) {
  const bits = []
  if (m.quant) bits.push(m.quant)
  if (m.size_mb) bits.push((m.size_mb / 1024).toFixed(2) + ' GB')
  if (m.arch) bits.push(m.arch)
  if (m.max_context_length) bits.push('上限 ' + m.max_context_length + ' ctx')
  return bits.join(' · ') || '元数据不可用'
}

// 视觉语言模型：llama.cpp 需要额外的 mmproj 文件才能处理图片
function isVlm(m) {
  return !!(m && (m.type === 'vlm' || /vl|vision/i.test(m.arch || '')))
}

// 模型 GGUF 内嵌的原生对话模板（tokenizer.chat_template）
function nativeTpl(m) {
  return (m && m.meta && m.meta.chat_template) || ''
}
function modelShort(m) {
  return (m && ((m.name || m.id) || '').split(/[\\/]/).pop()) || ''
}

function openTpl(m) {
  tplModel.value = m
  tplOpen.value = true
}

// 套用某模型的原生模板到全局参数（jinja 模式），下次加载模型生效。
async function applyNativeTpl() {
  const tpl = nativeTpl(tplModel.value)
  if (!tpl) return
  tplApplying.value = true
  try {
    // 原生模板是标准 Jinja，走 jinja 模式；引擎会自动加 --jinja 和 --chat-template。
    const values = { chat_template_mode: 'jinja', chat_template: tpl }
    state.params = await api.saveParams(values)
    notify(`已套用「${modelShort(tplModel.value)}」的原生模板，下次加载模型生效`, 'ok', 4500)
    tplOpen.value = false
  } catch (e) {
    notify(e.message, 'err')
  } finally {
    tplApplying.value = false
  }
}

function fmtUptime(sec) {
  sec = sec || 0
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  return h > 0 ? `${h}h${String(m).padStart(2, '0')}m` : `${m}m`
}

async function load(m) {
  busy.value = m.id
  try {
    await api.loadModel(m.id, 0, null)
    notify('llama-server 已拉起，模型正在进显存', 'ok')
  } catch (e) {
    notify(e.message, 'warn', 12000)
  } finally {
    busy.value = ''
    await refreshModels()
    await refreshStatus()
  }
}

async function unload(m) {
  busy.value = m.id
  try {
    await api.unloadModel(m.id)
    notify('已停止 llama-server（显存已释放）', 'ok')
  } catch (e) {
    notify(e.message, 'warn', 8000)
  } finally {
    busy.value = ''
    await refreshModels()
    await refreshStatus()
  }
}

async function useModel(m) {
  await api.setActiveModel(m.id, m.local_path || '')
  notify('已设为当前模型', 'ok', 1500)
  await refreshStatus()
}

async function recommend() {
  busy.value = 'reco'
  try {
    state.recommendation = await api.recommend({
      model: recoModel.value,
      path: manualPath.value,
      purpose: purpose.value,
    })
    state.recommendationModel = recoModel.value
    notify('已生成推荐参数', 'ok', 1800)
  } catch (e) {
    notify(e.message, 'err', 7000)
  } finally {
    busy.value = ''
  }
}

async function applyReco() {
  const r = reco.value
  if (!r) return
  const values = {}
  Object.entries(r.values || {}).forEach(([k, v]) => {
    if (sampling.includes(k) || loadKeys.includes(k)) values[k] = v
  })
  try {
    await api.saveParams(values)
    state.params = await api.params()
    notify('已把推荐值写入参数面板（加载级参数下次加载模型时生效）', 'ok', 5000)
  } catch (e) {
    notify(e.message, 'err')
  }
}

async function inspect() {
  busy.value = 'inspect'
  try {
    const meta = await api.inspectGGUF(manualPath.value)
    notify(`识别成功：${meta.arch} · ${meta.quant} · ${meta.layers} 层 · ${meta.native_ctx} ctx`, 'ok', 5000)
    await recommend()
  } catch (e) {
    notify(e.message, 'err', 7000)
  } finally {
    busy.value = ''
  }
}

// —— 模型记忆：每个模型自己调好的参数与使用统计 ——
//
// 标识与后端 core.ModelKey 保持一致：仅文件名、去 .gguf、去多分片序号、小写。
// 这样换个目录、重命名父文件夹，之前调好的参数依然认得这个模型。
const overrides = ref([])
const memOpen = ref(false)
const memLoading = ref(false)
const memSaving = ref(false)
const memModel = ref(null)
const memDraft = reactive({})
const memKeyword = ref('')
const memShowOnlyChanged = ref(false)
const memOpenGroups = reactive({})

const schema = computed(() => (state.params && state.params.schema) || [])
const globalValues = computed(() => (state.params && state.params.values) || {})

function modelKey(s) {
  let v = String(s || '').trim().replace(/^"+|"+$/g, '').toLowerCase()
  if (!v) return ''
  v = v.split(/[\\/]/).pop() || ''
  v = v.replace(/\.gguf$/, '')
  v = v.replace(/-\d{5}-of-\d{5}$/, '')
  return v
}

const memByKey = computed(() => {
  const map = {}
  overrides.value.forEach((o) => {
    map[o.key] = o
  })
  return map
})

function memoryOf(m) {
  return memByKey.value[modelKey(m.id)]
}

const memGroups = computed(() => {
  const seen = []
  schema.value.forEach((s) => {
    if (!seen.includes(s.group)) seen.push(s.group)
  })
  return seen
})

function memChanged(key) {
  return JSON.stringify(globalValues.value[key]) !== JSON.stringify(memDraft[key])
}

function memRows(group) {
  const k = memKeyword.value.toLowerCase()
  return schema.value.filter((s) => {
    if (s.group !== group) return false
    if (k) {
      return (
        s.key.toLowerCase().includes(k) ||
        s.label.toLowerCase().includes(k) ||
        (s.desc || '').toLowerCase().includes(k)
      )
    }
    if (memShowOnlyChanged.value) return memChanged(s.key)
    return true
  })
}

const memChangedCount = computed(() => schema.value.filter((s) => memChanged(s.key)).length)

async function loadOverrides() {
  try {
    const r = await api.modelOverrides()
    overrides.value = (r && r.overrides) || []
  } catch (e) {
    /* 静默：记忆列表不是主流程 */
  }
}

async function openMemory(m) {
  memModel.value = { id: m.id, name: m.name || '', path: m.local_path || '' }
  memKeyword.value = ''
  memShowOnlyChanged.value = false
  memOpen.value = true
  memLoading.value = true
  try {
    if (!state.params) await refreshParams()
    const r = await api.modelParams(m.id)
    const eff = (r && r.effective) || {}
    Object.keys(memDraft).forEach((k) => delete memDraft[k])
    Object.entries(eff).forEach(([k, v]) => {
      memDraft[k] = Array.isArray(v) ? [...v] : v
    })
    memGroups.value.forEach((g) => {
      if (memOpenGroups[g] === undefined) memOpenGroups[g] = g === '加载' || g === '采样'
    })
  } catch (e) {
    notify(e.message, 'err', 6000)
    memOpen.value = false
  } finally {
    memLoading.value = false
  }
}

function memResetToGlobal() {
  Object.keys(memDraft).forEach((k) => delete memDraft[k])
  Object.entries(globalValues.value).forEach(([k, v]) => {
    memDraft[k] = Array.isArray(v) ? [...v] : v
  })
  notify('已恢复为全局参数：保存后该模型将不再有覆盖', 'info', 3600)
}

async function saveMemory() {
  memSaving.value = true
  try {
    const values = { ...memDraft }
    await api.saveModelParams(memModel.value.id, memModel.value.name, memModel.value.path, values)
    await loadOverrides()
    notify('已记住这个模型的参数，下次加载会自动套用', 'ok', 4200)
    memOpen.value = false
  } catch (e) {
    notify(e.message, 'err', 7000)
  } finally {
    memSaving.value = false
  }
}

async function clearMemory(o) {
  try {
    await api.clearModelParams(o.key)
    await loadOverrides()
    notify('已清除该模型的记忆', 'ok', 2600)
  } catch (e) {
    notify(e.message, 'err')
  }
}

function fmtWhen(ts) {
  if (!ts) return '—'
  const diff = (Date.now() - ts * 1000) / 1000
  if (diff < 60) return '刚刚'
  if (diff < 3600) return Math.floor(diff / 60) + ' 分钟前'
  if (diff < 86400) return Math.floor(diff / 3600) + ' 小时前'
  if (diff < 86400 * 30) return Math.floor(diff / 86400) + ' 天前'
  return new Date(ts * 1000).toLocaleDateString()
}

onMounted(async () => {
  await refreshModels()
  await refreshEngineDetail()
  await loadOverrides()
})
</script>

<template>
  <div class="grid" style="gap: 16px">
    <div class="card tight">
      <div class="row wrap">
        <span class="dot" :class="health.online ? 'ok' : 'err'"></span>
        <b>{{ engineLabel }}</b>
        <span class="badge info">自管 · 可调度</span>
        <span class="mono hint">{{ st ? st.engine.endpoint : '' }}</span>
        <span class="badge" v-if="st">{{ models.length }} 个模型</span>
        <div class="spacer"></div>
        <button class="sm" @click="refreshModels">重新扫描</button>
        <button class="sm" @click="refreshStatus">刷新状态</button>
      </div>

      <!-- 进程级信息一目了然 -->
      <div class="grid cols-4" style="margin-top: 12px">
        <div class="card tight">
          <div class="stat-value">{{ health.running ? health.pid : '—' }}<small> PID</small></div>
          <div class="stat-label">子进程</div>
        </div>
        <div class="card tight">
          <div class="stat-value">{{ health.running ? health.port || '—' : '—' }}</div>
          <div class="stat-label">监听端口（仅回环）</div>
        </div>
        <div class="card tight">
          <div class="stat-value" style="font-size: 16px">{{ health.variant || '—' }}</div>
          <div class="stat-label">运行时变体</div>
        </div>
        <div class="card tight">
          <div class="stat-value">
            {{ health.running ? (health.rss_mb / 1024).toFixed(1) : '—' }}<small> GB</small>
          </div>
          <div class="stat-label">常驻内存 · {{ health.running ? fmtUptime(health.uptime_sec) : '未运行' }}</div>
        </div>
      </div>

      <div class="hint" style="margin-top: 10px">
        模型文件与推理进程都由本程序管：加载时把「参数」页的加载级参数
        （<span class="mono">n_gpu_layers / n_ctx / cache_type_k/v / flash_attn …</span>）变成
        <span class="mono">llama-server</span> 的启动开关，只绑回环地址，局域网侧统一走网关。
      </div>

      <div v-if="!runtimes.length" class="row wrap" style="margin-top: 10px; gap: 10px; align-items: center">
        <span class="badge warn">没找到 llama-server.exe</span>
        <span class="hint">
          到「设置 → 推理引擎」填它的路径，或把官方 llama.cpp 的 Windows 压缩包解压到程序目录的
          <span class="mono">runtime\llama.cpp\</span>
        </span>
      </div>
    </div>

    <div class="card">
      <div class="card-head">
        <h3>模型列表</h3>
        <div class="spacer"></div>
        <span class="badge">{{ models.filter((m) => m.loaded).length }} 个已加载</span>
      </div>
      <div v-if="!models.length" class="empty">
        没有扫描到 gguf。到「设置 → 模型根目录」添加一个存放 .gguf 的文件夹。
      </div>
      <div v-else class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>模型</th>
              <th>元数据</th>
              <th>状态</th>
              <th style="width: 380px">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="m in models" :key="m.id">
              <td>
                <div style="font-weight: 600">
                  {{ (m.name || m.id).split(/[\\/]/).pop() }}
                  <span
                    v-if="memoryOf(m)"
                    class="badge info"
                    style="margin-left: 6px"
                    title="已记住这个模型调好的参数，加载时自动套用"
                  >
                    记忆 {{ memoryOf(m).count }} 项
                  </span>
                  <span
                    v-if="nativeTpl(m)"
                    class="badge ok"
                    style="margin-left: 6px"
                    title="这个模型自带原生对话模板（GGUF 内嵌），点击查看并套用"
                  >
                    原生模板
                  </span>
                  <span
                    v-if="isVlm(m)"
                    class="badge warn"
                    style="margin-left: 6px"
                    title="视觉模型需要配套的 mmproj-*.gguf；把它放在模型同目录会自动识别，或在参数页填写 mmproj 路径"
                  >
                    视觉模型 · 需 mmproj
                  </span>
                </div>
                <div class="hint mono">{{ m.id }}</div>
              </td>
              <td class="hint">{{ meta(m) }}</td>
              <td>
                <span class="badge" :class="m.loaded ? 'ok' : ''">{{ m.loaded ? '已加载' : m.state || '未加载' }}</span>
              </td>
              <td>
                <div class="row wrap" style="gap: 6px">
                  <button class="sm" :disabled="busy === m.id || m.loaded" @click="load(m)">
                    {{ busy === m.id ? '处理中…' : '加载' }}
                  </button>
                  <button class="sm" :disabled="busy === m.id || !m.loaded" @click="unload(m)">卸载</button>
                  <button class="sm" @click="useModel(m)">设为当前</button>
                  <button class="sm" @click="openMemory(m)">记忆参数</button>
                  <button v-if="nativeTpl(m)" class="sm ghost" @click="openTpl(m)">原生模板</button>
                  <button
                    class="sm ghost"
                    @click="((recoModel = m.id), (manualPath = m.local_path || ''), recommend())"
                  >
                    推荐参数
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 模型记忆：每个模型自己调好的参数与使用统计（加载时自动套用） -->
    <div class="card" v-if="overrides.length">
      <div class="card-head">
        <h3>模型记忆</h3>
        <div class="spacer"></div>
        <span class="hint">参数只在加载时生效 · 换个目录也不丢</span>
        <button class="sm" @click="loadOverrides">刷新</button>
      </div>
      <div class="hint" style="margin-bottom: 8px">
        为某个模型调好的参数会记在它的「名字」上（<span class="mono">去路径 · 去 .gguf · 小写</span>），
        下次加载这个模型时自动套用，不用每次重调。
      </div>
      <div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>模型</th>
              <th>覆盖项</th>
              <th>上次上下文</th>
              <th>加载次数</th>
              <th>上次使用</th>
              <th style="width: 150px">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="o in overrides" :key="o.key">
              <td>
                <div style="font-weight: 600">{{ o.name || o.key }}</div>
                <div class="hint mono">{{ o.key }}</div>
                <div v-if="o.path" class="hint mono">{{ o.path }}</div>
              </td>
              <td>
                <span class="badge" :class="o.count ? 'info' : ''">{{ o.count ? o.count + ' 项' : '仅统计' }}</span>
                <div v-if="o.count" class="hint mono" style="margin-top: 4px">
                  {{ Object.keys(o.params || {}).join('、') }}
                </div>
              </td>
              <td class="hint">{{ o.last_ctx || '—' }}</td>
              <td class="hint">{{ o.load_count }}</td>
              <td class="hint">{{ fmtWhen(o.last_used_at) }}</td>
              <td>
                <button class="sm" @click="openMemory({ id: o.key, name: o.name, local_path: o.path })">编辑</button>
                <button class="sm ghost" @click="clearMemory(o)">忘记</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 运行细节：命令行与子进程日志尾巴（显存不足 / 模板不兼容都在这里） -->
    <div v-if="health.running" class="grid cols-2">
      <div class="card">
        <div class="card-head">
          <h3>启动命令行</h3>
          <div class="spacer"></div>
          <span class="badge">{{ health.bin_path ? health.bin_path.split('\\').pop() : 'llama-server.exe' }}</span>
        </div>
        <pre class="copybox" style="white-space: pre-wrap; word-break: break-all">{{ health.cmd_line }}</pre>
        <div class="hint" style="margin-top: 6px">
          日志文件：<span class="mono">{{ health.log_path || '—' }}</span>
        </div>
      </div>
      <div class="card">
        <div class="card-head"><h3>llama-server 日志（最近 12 行）</h3></div>
        <pre class="copybox" style="white-space: pre-wrap; max-height: 260px; overflow: auto">{{ (health.log_tail || []).join('\n') || '（暂无输出）' }}</pre>
      </div>
    </div>

    <div class="card">
      <div class="card-head">
        <h3>硬件参数推荐（纯算术，可核对）</h3>
        <div class="spacer"></div>
        <select v-model="purpose" style="max-width: 150px">
          <option v-for="p in purposes" :key="p.v" :value="p.v">{{ p.l }}</option>
        </select>
        <button class="sm primary" :disabled="busy === 'reco'" @click="recommend">
          {{ busy === 'reco' ? '计算中…' : '生成推荐' }}
        </button>
      </div>

      <div class="row wrap" style="margin-bottom: 10px">
        <select v-model="recoModel" style="max-width: 320px">
          <option value="">（选择已扫描的模型）</option>
          <option v-for="m in models" :key="m.id" :value="m.id">{{ m.id }}</option>
        </select>
        <input v-model="manualPath" placeholder="或直接填本地 .gguf 文件路径（可读取结构参数，推荐更准）" />
        <button class="sm" :disabled="busy === 'inspect'" @click="inspect">解析文件</button>
      </div>

      <div v-if="!reco" class="hint">
        推荐会读取模型头部的层数 / 隐藏维度 / KV head 数与本机实测显存，按「显存预算 → 缓存量化 → 上下文 → 卸载层数」
        的顺序逐项推导，并给出每一步的算式。
      </div>

      <div v-else>
        <div class="grid cols-4" style="margin-bottom: 12px">
          <div class="card tight">
            <div class="stat-value">{{ (reco.budget_mb / 1024).toFixed(1) }}<small> GB</small></div>
            <div class="stat-label">可用显存预算</div>
          </div>
          <div class="card tight">
            <div class="stat-value">{{ (reco.model_mb / 1024).toFixed(1) }}<small> GB</small></div>
            <div class="stat-label">模型文件大小</div>
          </div>
          <div class="card tight">
            <div class="stat-value">{{ (reco.kv_total_mb / 1024).toFixed(2) }}<small> GB</small></div>
            <div class="stat-label">KV 缓存估算</div>
          </div>
          <div class="card tight">
            <div class="stat-value">{{ (reco.estimated_total_mb / 1024).toFixed(1) }}<small> GB</small></div>
            <div class="stat-label">预计总占用</div>
          </div>
        </div>

        <div class="row" style="margin-bottom: 8px">
          <b>逐条算式依据</b>
          <button class="sm ghost" @click="showReasons = !showReasons">{{ showReasons ? '收起' : '展开' }}</button>
          <div class="spacer"></div>
          <button class="sm primary" @click="applyReco">把推荐值写入参数面板</button>
        </div>

        <div v-if="showReasons" class="grid" style="gap: 8px">
          <div v-for="r in reco.reasons" :key="r.key" class="card tight">
            <div class="row">
              <b>{{ r.label }}</b>
              <span class="badge info">{{ r.value }}</span>
              <div class="spacer"></div>
              <span v-if="r.source" class="hint">{{ r.source }}</span>
            </div>
            <div class="rec-lines">{{ (r.lines || []).join('\n') }}</div>
          </div>
        </div>

        <div v-if="reco.notes && reco.notes.length" class="card tight" style="margin-top: 10px; border-color: var(--warn)">
          <div v-for="(n, i) in reco.notes" :key="i" class="hint" style="color: var(--warn-text)">· {{ n }}</div>
        </div>

        <div class="card tight" style="margin-top: 10px">
          <div class="hint">
            加载级参数推荐值：
            <span class="mono">
              n_gpu_layers={{ reco.values.n_gpu_layers }} · n_ctx={{ reco.values.n_ctx }} ·
              cache={{ reco.values.cache_type_k }} · flash_attn={{ reco.values.flash_attn }} ·
              n_threads={{ reco.values.n_threads }}
            </span>
          </div>
          <div class="hint" style="margin-top: 6px; color: var(--ok-text)">
            点上面的按钮会把它们写进「参数」页；下次加载模型时真正变成 llama-server 的启动开关。
          </div>
        </div>
      </div>
    </div>
  </div>

  <!-- 模型记忆编辑弹窗：给这个模型单独调一套参数 -->
  <div v-if="memOpen" class="mem-mask" @click.self="memOpen = false">
    <div class="mem-panel">
      <div class="card-head">
        <h3>
          记忆参数 ·
          {{ memModel && memModel.name ? memModel.name.split(/[\\/]/).pop() : memModel && memModel.id }}
        </h3>
        <span class="badge" :class="memChangedCount ? 'info' : ''">
          {{ memChangedCount ? memChangedCount + ' 项与全局不同' : '与全局一致' }}
        </span>
        <div class="spacer"></div>
        <button class="sm ghost" @click="memOpen = false">关闭</button>
      </div>

      <div v-if="memLoading" class="empty">读取参数中…</div>
      <template v-else>
        <div class="row wrap" style="gap: 8px; margin-bottom: 8px">
          <input v-model="memKeyword" placeholder="搜索参数（key / 名称 / 说明）" style="max-width: 260px" />
          <label class="row" style="gap: 6px">
            <input type="checkbox" v-model="memShowOnlyChanged" style="width: auto" />
            <span class="hint">只看与全局不同的</span>
          </label>
          <div class="spacer"></div>
          <button class="sm ghost" @click="memResetToGlobal">恢复为全局</button>
          <button class="sm primary" :disabled="memSaving" @click="saveMemory">
            {{ memSaving ? '保存中…' : '保存记忆' }}
          </button>
        </div>
        <div class="hint" style="margin-bottom: 8px">
          这里的值会随「加载这个模型」自动套用（优先级：全局 &lt; 模型记忆 &lt; 本次加载的临时覆盖），
          不写回全局参数，因此不会影响别的模型。
        </div>

        <div class="mem-body">
          <div v-for="g in memGroups" :key="g" class="param-group">
            <header @click="memOpenGroups[g] = !memOpenGroups[g]">
              <span>{{ memOpenGroups[g] ? '▾' : '▸' }}</span>
              <b>{{ g }}</b>
              <span class="badge">{{ memRows(g).length }}</span>
            </header>
            <div class="body" v-show="memOpenGroups[g]">
              <div v-if="!memRows(g).length" class="empty">没有匹配的参数</div>
              <ParamRow
                v-for="p in memRows(g)"
                :key="p.key"
                :param="p"
                v-model="memDraft[p.key]"
                :source="memChanged(p.key) ? '本模型' : '全局'"
              />
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>

  <!-- 原生模板查看/套用弹窗：展示 GGUF 内嵌的 tokenizer.chat_template -->
  <div v-if="tplOpen" class="mem-mask" @click.self="tplOpen = false">
    <div class="mem-panel tpl-panel">
      <div class="card-head">
        <h3>原生对话模板 · {{ modelShort(tplModel) }}</h3>
        <div class="spacer"></div>
        <button class="sm ghost" @click="tplOpen = false">关闭</button>
      </div>
      <div class="hint" style="margin-bottom: 8px">
        这是模型 GGUF 内嵌的原生模板（<span class="mono">tokenizer.chat_template</span>），
        含模型训练时的角色标记与结束符。套用后到下次加载模型时生效，引擎会自动开启 Jinja 模式。
      </div>
      <div v-if="currentGlobalTemplate" class="hint" style="margin-bottom: 8px">
        当前全局模板：<b>{{ currentGlobalTemplate.label }}</b>
        <span class="badge">{{ currentGlobalTemplate.source }}</span>
        <span v-if="tplMatchesCurrent" class="badge ok">已套用此模型的原生模板</span>
        <span v-else class="badge warn">与此模型的原生模板不同</span>
      </div>
      <pre class="tpl-code">{{ nativeTpl(tplModel) }}</pre>
      <div class="row" style="margin-top: 12px; justify-content: flex-end; gap: 8px">
        <button class="sm ghost" @click="tplOpen = false">取消</button>
        <button class="sm primary" :disabled="tplApplying" @click="applyNativeTpl">
          {{ tplApplying ? '套用中…' : '套用此模板' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.mem-mask {
  position: fixed;
  inset: 0;
  background: rgba(8, 11, 16, 0.72);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 50;
  padding: 24px;
}

.mem-panel {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  width: min(920px, 100%);
  max-height: 88vh;
  display: flex;
  flex-direction: column;
  padding: 14px 16px;
  box-shadow: 0 24px 60px rgba(0, 0, 0, 0.5);
}

.mem-body {
  overflow: auto;
  display: grid;
  gap: 8px;
  padding-right: 4px;
}

.tpl-panel {
  width: min(860px, 100%);
  max-height: 86vh;
}

.tpl-code {
  margin: 0;
  flex: 1;
  min-height: 220px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px 14px;
  font: 11px/1.6 ui-monospace, Consolas, monospace;
  color: var(--text);
}
</style>
