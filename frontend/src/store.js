import { reactive, computed } from 'vue'
import { api } from './api'

export const state = reactive({
  ready: false,
  loading: true,
  error: '',
  toast: null,
  view: 'dashboard',
  status: null,
  hardware: null,
  models: [],
  params: null,
  network: null,
  engine: null,
  engineDetail: null,
  modelRoots: [],
  config: null,
  requests: [],
  requestStats: null,
  requestQuery: null,
  logs: [],
  recommendation: null,
  recommendationModel: '',
})

export function notify(message, kind = 'info', ms = 3200) {
  state.toast = { message, kind, id: Date.now() }
  const id = state.toast.id
  setTimeout(() => {
    if (state.toast && state.toast.id === id) state.toast = null
  }, ms)
}

function handle(e) {
  state.error = e && e.message ? e.message : String(e)
}

export const gatewayRunning = computed(() => !!(state.status && state.status.gateway_running))
export const engineOnline = computed(() => !!(state.status && state.status.engine && state.status.engine.health.online))

// 推理引擎：本程序自己拉起并管理 llama-server 子进程（可调度）
export const engineKind = computed(
  () => (state.status && state.status.engine && state.status.engine.kind) || 'llamacpp',
)
export const engineSchedulable = computed(
  () => !!(state.status && state.status.engine && state.status.engine.schedulable),
)
export const engineLabel = computed(() => engineKind.value || 'llama.cpp')

export async function refreshEngineDetail() {
  try {
    state.engineDetail = await api.engineDetail()
  } catch (e) {
    handle(e)
  }
}

export async function refreshModelRoots() {
  try {
    const r = await api.modelRoots()
    state.modelRoots = (r && r.roots) || []
  } catch (e) {
    handle(e)
  }
}

export async function refreshStatus() {
  try {
    state.status = await api.status()
    if (state.status.hardware) state.hardware = state.status.hardware
    if (state.status.engine) state.engine = state.status.engine
    state.error = ''
  } catch (e) {
    handle(e)
  }
}

export async function refreshModels() {
  try {
    state.models = (await api.models()) || []
  } catch (e) {
    handle(e)
  }
}

export async function refreshParams() {
  try {
    state.params = await api.params()
  } catch (e) {
    handle(e)
  }
}

export async function refreshNetwork() {
  try {
    state.network = await api.network()
  } catch (e) {
    handle(e)
  }
}

export async function refreshRequests(query = {}) {
  try {
    state.requests = (await api.requests(query)) || []
    state.requestStats = await api.requestStats()
  } catch (e) {
    handle(e)
  }
}

export async function refreshLogs(level = '') {
  try {
    state.logs = (await api.logs(400, level)) || []
  } catch (e) {
    handle(e)
  }
}

export async function refreshConfig() {
  try {
    state.config = await api.config()
  } catch (e) {
    handle(e)
  }
}

export async function bootstrap() {
  state.loading = true
  await refreshStatus()
  state.ready = true
  state.loading = false
  await Promise.all([
    refreshModels(),
    refreshParams(),
    refreshNetwork(),
    refreshConfig(),
    refreshEngineDetail(),
    refreshModelRoots(),
  ])
  const h = state.status && state.status.engine && state.status.engine.health
  if (h && !h.online) {
    notify('引擎未启动：在「模型」页加载一个 GGUF，或直接发一次请求由本程序自动拉起', 'warn', 7000)
  }
}

let timer = null
export function startPolling() {
  if (timer) return
  timer = setInterval(() => {
    if (state.view === 'logs') {
      // 沿用日志页当前筛选，避免筛选结果被轮询冲掉
      refreshRequests(state.requestQuery || { limit: 200 })
      refreshLogs()
    } else {
      refreshStatus()
    }
    if (state.view === 'models') refreshModels()
  }, 2500)
}

export function stopPolling() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}
