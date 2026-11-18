// 与后端通信的唯一入口。
//
// 两种入口共用一份前端：
//   1) 本机浏览器（http://127.0.0.1:8081）→ 同源相对路径
//   2) Wails 桌面窗口（wails:// 协议）  → 显式指向 127.0.0.1:8081 管理口
// 因此这里先探测同源是否可用，不可用再退回回环地址。

let base = null

async function probe(prefix) {
  try {
    const r = await fetch(prefix + '/api/v1/ping', { cache: 'no-store' })
    // 必须是真正的 JSON 接口：桌面窗口里未命中的路径可能被静态资源兜底成 HTML
    if (!r.ok) return false
    const ct = r.headers.get('content-type') || ''
    if (!ct.includes('application/json')) return false
    const data = await r.json()
    return !!data.ok
  } catch (e) {
    /* 忽略，继续尝试下一个候选 */
  }
  return false
}

export async function resolveBase() {
  if (base !== null) return base
  if (await probe('')) {
    base = ''
    return base
  }
  const candidates = []
  if (typeof window !== 'undefined' && window.__AIRUNNER_ADMIN__) {
    candidates.push(window.__AIRUNNER_ADMIN__)
  }
  candidates.push('http://127.0.0.1:8081')
  for (const c of candidates) {
    if (await probe(c)) {
      base = c
      return base
    }
  }
  base = candidates[0] || ''
  return base
}

export class ApiError extends Error {
  constructor(message, status, payload) {
    super(message)
    this.status = status
    this.payload = payload
  }
}

async function request(path, { method = 'GET', body, timeout = 60000 } = {}) {
  const b = await resolveBase()
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), timeout)
  try {
    const r = await fetch(b + path, {
      method,
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
      signal: ctrl.signal,
    })
    const text = await r.text()
    let data = null
    if (text) {
      try {
        data = JSON.parse(text)
      } catch (e) {
        data = { raw: text }
      }
    }
    if (!r.ok) {
      const msg = (data && (data.error || data.message)) || `请求失败（HTTP ${r.status}）`
      throw new ApiError(typeof msg === 'string' ? msg : JSON.stringify(msg), r.status, data)
    }
    return data
  } finally {
    clearTimeout(timer)
  }
}

export const api = {
  ping: () => request('/api/v1/ping'),
  status: () => request('/api/v1/status'),
  metrics: () => request('/api/v1/metrics'),
  hardware: (rescan = false) => request(`/api/v1/hardware${rescan ? '?rescan=1' : ''}`),
  rescanHardware: () => request('/api/v1/hardware/rescan', { method: 'POST' }),

  models: () => request('/api/v1/models'),
  recommend: (payload) => request('/api/v1/models/recommend', { method: 'POST', body: payload }),
  loadModel: (id, context_length, params) =>
    request('/api/v1/models/load', {
      method: 'POST',
      body: { id, context_length, params },
      // 自管引擎首次加载要等模型进显存，给足时间
      timeout: 300000,
    }),
  unloadModel: (id) => request('/api/v1/models/unload', { method: 'POST', body: { id } }),
  setActiveModel: (model, path) => request('/api/v1/models/active', { method: 'POST', body: { model, path } }),
  inspectGGUF: (path) => request('/api/v1/gguf/inspect', { method: 'POST', body: { path } }),

  // 模型记忆：每个模型自己调好的参数与使用统计
  modelOverrides: () => request('/api/v1/models/overrides'),
  modelParams: (key) => request(`/api/v1/models/params?key=${encodeURIComponent(key)}`),
  saveModelParams: (key, name, path, values) =>
    request('/api/v1/models/params', { method: 'PUT', body: { key, name, path, values } }),
  clearModelParams: (key) =>
    request(`/api/v1/models/params?key=${encodeURIComponent(key)}`, { method: 'DELETE' }),

  modelRoots: () => request('/api/v1/model-roots'),
  addModelRoot: (path) => request('/api/v1/model-roots', { method: 'POST', body: { path } }),
  removeModelRoot: (path) =>
    request(`/api/v1/model-roots?path=${encodeURIComponent(path)}`, { method: 'DELETE' }),

  params: () => request('/api/v1/params'),
  saveParams: (values) => request('/api/v1/params', { method: 'PUT', body: { values } }),
  schema: () => request('/api/v1/schema'),
  presets: () => request('/api/v1/presets'),
  applyPreset: (name) => request('/api/v1/presets/apply', { method: 'POST', body: { name } }),
  savePreset: (name) => request('/api/v1/presets', { method: 'POST', body: { name } }),
  deletePreset: (name) => request(`/api/v1/presets?name=${encodeURIComponent(name)}`, { method: 'DELETE' }),

  logs: (limit = 300, level = '') =>
    request(`/api/v1/logs?limit=${limit}${level ? `&level=${level}` : ''}`),
  clearLogs: () => request('/api/v1/logs', { method: 'DELETE' }),
  requests: (q = {}) => {
    const p = new URLSearchParams()
    Object.entries(q).forEach(([k, v]) => {
      if (v !== '' && v !== null && v !== undefined && v !== 0) p.set(k, v)
    })
    return request(`/api/v1/requests?${p.toString()}`)
  },
  requestStats: () => request('/api/v1/requests/stats'),
  clearRequests: () => request('/api/v1/requests', { method: 'DELETE' }),

  network: () => request('/api/v1/network'),
  engine: () => request('/api/v1/engine'),
  rescanEngine: () => request('/api/v1/engine/rescan', { method: 'POST' }),
  engineDetail: () => request('/api/v1/engine/detail'),
  detectRuntimes: () => request('/api/v1/engine/runtimes/detect', { method: 'POST', timeout: 30000 }),

  // 一键下载安装 llama-server（后台任务，用 engineInstallState 轮询进度）
  installEngine: (variant = 'auto') =>
    request('/api/v1/engine/install', { method: 'POST', body: { variant } }),
  engineInstallState: () => request('/api/v1/engine/install'),

  // 本机原生目录/文件选择框（对话框开在运行本程序的机器上）
  pickFolder: (title = '', initial = '') =>
    request('/api/v1/dialog/pick-folder', { method: 'POST', body: { title, initial }, timeout: 300000 }),
  pickFile: (title = '', initial = '', filter = '') =>
    request('/api/v1/dialog/pick-file', {
      method: 'POST',
      body: { title, initial, filter },
      timeout: 300000,
    }),
  testChat: (model, prompt) =>
    request('/api/v1/test-chat', { method: 'POST', body: { model, prompt }, timeout: 180000 }),

  gatewayStart: () => request('/api/v1/gateway/start', { method: 'POST' }),
  gatewayStop: () => request('/api/v1/gateway/stop', { method: 'POST' }),

  config: () => request('/api/v1/config'),
  saveConfig: (patch) => request('/api/v1/config', { method: 'PUT', body: { patch } }),
}
