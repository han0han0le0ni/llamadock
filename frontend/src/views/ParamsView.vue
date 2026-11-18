<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { api } from '../api'
import { state, notify, refreshParams } from '../store'
import ParamRow from '../components/ParamRow.vue'
import { chatTemplates, findTemplate } from '../chatTemplates'

const draft = reactive({})
const keyword = ref('')
const openGroups = reactive({})
const showOnlyChanged = ref(false)
const saving = ref(false)
const newPresetName = ref('')
const showReco = ref(false)
const recoValues = ref({})
const presetPick = ref('')
const templatePick = ref('')

const params = computed(() => state.params)
const templates = chatTemplates
// 模型 GGUF 内嵌的原生模板，作为下拉的第二组选项
const nativeTemplates = computed(() =>
  (state.models || []).filter((m) => m.meta && m.meta.chat_template),
)

// 分组：常用 / 采样 / 长度 / 控制 / 结构化 / Prompt / 加载 / 缓存 / 加速 / 服务
const groups = computed(() => {
  const p = params.value
  if (!p) return []
  const seen = []
  p.schema.forEach((s) => {
    if (!seen.includes(s.group)) seen.push(s.group)
  })
  return seen
})

const commonKeys = computed(() => (params.value ? params.value.common || [] : []))

function initDraft() {
  const p = params.value
  if (!p) return
  Object.keys(draft).forEach((k) => delete draft[k])
  Object.entries(p.values).forEach(([k, v]) => {
    draft[k] = Array.isArray(v) ? [...v] : v
  })
  groups.value.forEach((g, i) => {
    if (openGroups[g] === undefined) openGroups[g] = g === '采样' || g === '加载' || i === 0
  })
}

watch(params, initDraft, { immediate: true })

const rowsOf = (group) =>
  params.value.schema.filter((s) => {
    if (s.group !== group) return false
    if (keyword.value) {
      const k = keyword.value.toLowerCase()
      return (
        s.key.toLowerCase().includes(k) ||
        s.label.toLowerCase().includes(k) ||
        (s.desc || '').toLowerCase().includes(k)
      )
    }
    if (showOnlyChanged.value) return changed(s.key)
    return true
  })

function commonRows() {
  return params.value.schema.filter((s) => {
    if (!commonKeys.value.includes(s.key)) return false
    if (keyword.value) {
      const k = keyword.value.toLowerCase()
      return s.key.toLowerCase().includes(k) || s.label.toLowerCase().includes(k)
    }
    return true
  })
}

function changed(key) {
  const base = params.value.values[key]
  const cur = draft[key]
  return JSON.stringify(base) !== JSON.stringify(cur)
}

const changedCount = computed(() => {
  if (!params.value) return 0
  return Object.keys(draft).filter((k) => changed(k)).length
})

async function save() {
  saving.value = true
  try {
    const values = {}
    Object.entries(draft).forEach(([k, v]) => {
      if (changed(k)) values[k] = v
    })
    if (!Object.keys(values).length) {
      notify('没有改动需要保存', 'info', 1600)
      return
    }
    state.params = await api.saveParams(values)
    notify(`已保存 ${Object.keys(values).length} 项参数`, 'ok')
  } catch (e) {
    notify(e.message, 'err', 6000)
  } finally {
    saving.value = false
  }
}

function resetGroup(group) {
  const defaults = params.value.values
  params.value.schema
    .filter((s) => s.group === group)
    .forEach((s) => {
      draft[s.key] = Array.isArray(defaults[s.key]) ? [...defaults[s.key]] : defaults[s.key]
    })
  notify(`已把「${group}」恢复为当前已保存值`, 'info', 1800)
}

async function applyPreset(name) {
  if (!name) return
  try {
    state.params = await api.applyPreset(name)
    await refreshParams()
    initDraft()
    notify(`已应用预设：${name}`, 'ok')
  } catch (e) {
    notify(e.message, 'err')
  } finally {
    // 重置选择，保证同一个预设可以反复选择触发
    presetPick.value = ''
  }
}

async function saveAsPreset() {
  const name = newPresetName.value.trim()
  if (!name) return notify('请输入预设名', 'warn')
  try {
    await api.savePreset(name)
    state.params = await api.params()
    newPresetName.value = ''
    notify(`已保存预设：${name}`, 'ok')
  } catch (e) {
    notify(e.message, 'err')
  }
}

async function delPreset(name) {
  try {
    await api.deletePreset(name)
    state.params = await api.params()
    notify('已删除预设', 'ok')
  } catch (e) {
    notify(e.message, 'err')
  }
}

// 套用 Chat 模板：写入 draft 并立即保存到全局参数。
// 支持两类：内置模板（builtin:<id>）与模型 GGUF 内嵌的原生模板（native:<modelId>）。
// chat_template_mode / chat_template 是加载级参数，下次加载模型时生效。
async function applyTemplate(pick) {
  let mode, text, label
  if (pick.startsWith('native:')) {
    const mid = pick.slice(7)
    const m = (state.models || []).find((x) => x.id === mid)
    if (!m || !m.meta || !m.meta.chat_template) return
    mode = 'jinja'
    text = m.meta.chat_template
    label = (m.name || m.id).split(/[\\/]/).pop() + ' 的原生模板'
  } else {
    const t = findTemplate(pick.startsWith('builtin:') ? pick.slice(8) : pick)
    if (!t) return
    mode = t.mode
    text = t.text
    label = t.label.split(' · ')[0]
  }
  draft.chat_template_mode = mode
  draft.chat_template = text
  templatePick.value = ''
  try {
    const values = { chat_template_mode: mode, chat_template: text }
    state.params = await api.saveParams(values)
    notify(mode === 'auto'
      ? '已切回 auto：下次加载模型将使用 GGUF 自带模板'
      : `已套用「${label}」，下次加载模型时生效`, 'ok', 4200)
  } catch (e) {
    notify(e.message, 'err')
  }
}

onMounted(refreshParams)
</script>

<template>
  <div v-if="params" class="grid" style="gap: 14px">
    <!-- 推荐横幅（静默应用，默认收起） -->
    <div class="card tight">
      <div class="row wrap">
        <span class="badge info">已根据你的硬件优化</span>
        <span class="hint">共 {{ params.count }} 项参数 · 常用 15 项默认展开 · 其余折叠在各自分组内</span>
        <div class="spacer"></div>
        <button class="sm ghost" @click="showReco = !showReco">
          {{ showReco ? '收起算式依据 ▲' : '查看算式依据 ▼' }}
        </button>
      </div>
      <div v-if="showReco" class="hint" style="margin-top: 8px">
        推荐值在「模型」页生成（读取本机显存与模型头部结构）。这里展示的是参数面板当前的取值来源：
        <b>全局默认</b>（本页保存的值）。加载级参数会在下次加载模型时变成 llama-server 的启动开关，
        采样类参数则随每次请求下发。
      </div>
    </div>

    <!-- Chat 模板快捷套用 -->
    <div class="card tight">
      <div class="row wrap">
        <span class="badge info">Chat 模板</span>
        <span class="hint">
          当前模式：<b class="mono">{{ draft.chat_template_mode || 'auto' }}</b>
          <span v-if="draft.chat_template"> · 已设置自定义模板</span>
          <span v-else> · 用模型自带模板</span>
        </span>
        <div class="spacer"></div>
        <select v-model="templatePick" style="max-width: 300px" @change="applyTemplate(templatePick)">
          <option value="" disabled>一键套用模板…</option>
          <optgroup label="内置模板">
            <option v-for="t in templates" :key="t.id" :value="'builtin:' + t.id">{{ t.label }}</option>
          </optgroup>
          <optgroup v-if="nativeTemplates.length" label="模型自带模板（GGUF 内嵌）">
            <option
              v-for="m in nativeTemplates"
              :key="m.id"
              :value="'native:' + m.id"
            >
              {{ (m.name || m.id).split(/[\\/]/).pop() }}
            </option>
          </optgroup>
        </select>
      </div>
      <div class="hint" style="margin-top: 8px">
        <b>推荐优先用「模型自带模板」</b>（从 GGUF 里提取的原生模板，含正确的结束符，最不容易出错或复读）。
        模板属于加载级参数，套用后在「模型」页重新加载模型才生效。若手机客户端报
        <span class="mono">System message must be at the beginning</span>，
        网关会自动把 system 提到首位；万一个别模型仍报错，再退而求其次用 <b>通用宽松</b> 模板。
        套用自定义/原生模板时程序会自动开启引擎的 Jinja 模式。
      </div>
    </div>

    <!-- 工具条 -->
    <div class="card tight">
      <div class="row wrap">
        <input v-model="keyword" placeholder="搜索参数（key / 名称 / 说明）" style="max-width: 300px" />
        <label class="row" style="gap: 6px">
          <input type="checkbox" v-model="showOnlyChanged" style="width: auto" />
          <span class="hint">只看改动</span>
        </label>
        <span class="badge" v-if="changedCount">{{ changedCount }} 项待保存</span>
        <div class="spacer"></div>
        <span class="hint">预设</span>
        <select v-model="presetPick" style="max-width: 140px" @change="applyPreset(presetPick)">
          <option value="" disabled>选择预设…</option>
          <option v-for="p in params.presets" :key="p.name" :value="p.name">
            {{ p.name }}{{ p.name === params.active_preset ? '（当前）' : '' }}
          </option>
        </select>
        <input v-model="newPresetName" placeholder="另存为…" style="max-width: 120px" />
        <button class="sm" @click="saveAsPreset">保存预设</button>
        <button class="sm primary" :disabled="saving || !changedCount" @click="save">
          {{ saving ? '保存中…' : '保存参数' }}
        </button>
      </div>
      <div v-if="params.issues && params.issues.length" class="hint" style="margin-top: 8px; color: var(--warn-text)">
        <div v-for="(i, n) in params.issues" :key="n">· {{ i.label }}：{{ i.message }}</div>
      </div>
    </div>

    <!-- 常用 15 项 -->
    <div class="card" v-if="!keyword && !showOnlyChanged">
      <div class="card-head">
        <h3>常用参数（15 项）</h3>
        <span class="badge ok">默认展开</span>
      </div>
      <ParamRow v-for="p in commonRows()" :key="'c-' + p.key" :param="p" v-model="draft[p.key]" />
    </div>

    <!-- 全部分组 -->
    <div v-for="g in groups" :key="g" class="param-group">
      <header @click="openGroups[g] = !openGroups[g]">
        <span>{{ openGroups[g] ? '▾' : '▸' }}</span>
        <b>{{ g }}</b>
        <span class="badge">{{ rowsOf(g).length }}</span>
        <div class="spacer"></div>
        <button class="sm ghost" @click.stop="resetGroup(g)">恢复已保存值</button>
      </header>
      <div class="body" v-show="openGroups[g]">
        <div v-if="!rowsOf(g).length" class="empty">没有匹配的参数</div>
        <ParamRow
          v-for="p in rowsOf(g)"
          :key="p.key"
          :param="p"
          v-model="draft[p.key]"
          :recommended="recoValues[p.key]"
        />
      </div>
    </div>
  </div>
  <div v-else class="empty">加载参数注册表…</div>
</template>
