<script setup>
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { state, bootstrap, startPolling, stopPolling, engineLabel, IMAGE_FEATURE_ENABLED } from './store'
import { api } from './api'
import { notify } from './store'
import Dashboard from './views/Dashboard.vue'
import Models from './views/ModelsView.vue'
import Params from './views/ParamsView.vue'
import Service from './views/ServiceView.vue'
import Logs from './views/LogsView.vue'
import Settings from './views/SettingsView.vue'
import Image from './views/ImageView.vue'
import Icon from './components/Icon.vue'

const nav = [
  { id: 'dashboard', label: '仪表盘', icon: 'dashboard', comp: Dashboard },
  { id: 'models', label: '模型', icon: 'models', comp: Models },
  { id: 'params', label: '参数', icon: 'params', comp: Params },
  ...(IMAGE_FEATURE_ENABLED ? [{ id: 'image', label: '生图', icon: 'image', comp: Image }] : []),
  { id: 'service', label: '服务', icon: 'service', comp: Service },
  { id: 'logs', label: '日志', icon: 'logs', comp: Logs },
  { id: 'settings', label: '设置', icon: 'settings', comp: Settings },
]

const current = computed(() => nav.find((n) => n.id === state.view) || nav[0])
const st = computed(() => state.status)

function go(id) {
  if (id === 'image' && !IMAGE_FEATURE_ENABLED) {
    notify('生图功能已停用，识图功能不受影响', 'warn')
    return
  }
  state.view = id
  if (typeof location !== 'undefined') {
    location.hash = '#' + id
  }
}

function syncFromHash() {
  if (typeof location === 'undefined') return
  const id = (location.hash || '').replace(/^#\/?/, '')
  if (id === 'image' && !IMAGE_FEATURE_ENABLED) {
    state.view = 'dashboard'
    location.hash = '#dashboard'
    return
  }
  if (nav.some((n) => n.id === id)) {
    state.view = id
  }
}

// 主题：把配置里的选项真正作用到根节点。设置页保存后 state.config 更新即自动生效。
function applyTheme() {
  const t = state.config && state.config.ui && state.config.ui.theme
  document.documentElement.dataset.theme = t === 'light' ? 'light' : 'dark'
}
watch(() => state.config, applyTheme, { immediate: true })

async function toggleGateway() {
  try {
    if (st.value && st.value.gateway_running) {
      await api.gatewayStop()
      notify('推理网关已停止', 'warn')
    } else {
      await api.gatewayStart()
      notify('推理网关已启动', 'ok')
    }
    await bootstrap()
  } catch (e) {
    notify(e.message || String(e), 'err')
  }
}

onMounted(async () => {
  syncFromHash()
  window.addEventListener('hashchange', syncFromHash)
  await bootstrap()
  startPolling()
})

onBeforeUnmount(() => {
  window.removeEventListener('hashchange', syncFromHash)
  stopPolling()
})
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-logo">LD</div>
        <div>
          <div class="brand-title">LlamaDock</div>
          <div class="brand-sub">本地大模型控制台</div>
        </div>
      </div>
      <div
        v-for="n in nav"
        :key="n.id"
        class="nav-item"
        :class="{ active: state.view === n.id }"
        @click="go(n.id)"
      >
        <span class="icon"><Icon :name="n.icon" :size="18" /></span>
        <span class="label">{{ n.label }}</span>
        <span v-if="n.id === 'logs' && state.requests.length" class="nav-badge">{{ state.requests.length }}</span>
        <span v-else-if="n.id === 'models' && st" class="nav-badge">{{ st.model_count }}</span>
      </div>
      <div style="flex: 1"></div>
      <div class="side-status" style="padding: 12px 8px 2px; border-top: 1px solid var(--border-soft)">
        <div class="row" style="gap: 8px">
          <span class="dot" :class="st && st.engine.health.online ? 'ok' : 'err'"></span>
          <span class="status-line">{{ st && st.engine.health.online ? '引擎在线' : '引擎离线' }}</span>
          <span class="status-sub">{{ engineLabel }}</span>
        </div>
        <div class="row" style="gap: 8px; margin-top: 7px">
          <span class="dot" :class="st && st.gateway_running ? 'ok' : 'off'"></span>
          <span class="status-line">{{ st && st.gateway_running ? '网关运行中' : '网关已停止' }}</span>
        </div>
      </div>
    </aside>

    <main class="main">
      <header class="topbar">
        <h1><Icon :name="current.icon" :size="19" :stroke="2" />{{ current.label }}</h1>
        <span class="topbar-divider"></span>
        <template v-if="st">
          <span class="badge info">{{ engineLabel }}</span>
          <span class="badge">v{{ st.version }}</span>
          <span class="badge" :class="st.gateway_running ? 'ok' : ''">
            <span class="dot" :class="st.gateway_running ? 'ok' : 'off'"></span>
            :{{ st.gateway_addr ? st.gateway_addr.split(':').pop() : '—' }}
          </span>
          <span v-if="st.hardware" class="badge info">{{ st.hardware_summary }}</span>
        </template>
        <div class="spacer"></div>
        <span v-if="st" class="topbar-metric">
          <b>{{ st.metrics.total_requests }}</b> 次请求
          <span class="metric-sep">·</span>
          <b>{{ st.metrics.tps.toFixed(1) }}</b> t/s
        </span>
        <button class="sm" title="刷新状态" @click="bootstrap"><Icon :name="'refresh'" :size="14" />刷新</button>
        <button class="sm primary" @click="toggleGateway">
          <Icon :name="st && st.gateway_running ? 'stop' : 'play'" :size="14" :stroke="2.2" />
          {{ st && st.gateway_running ? '停止网关' : '启动网关' }}
        </button>
      </header>

      <section class="content">
        <div v-if="state.loading" class="empty">正在连接本机服务…</div>
        <div v-else-if="state.error" class="card" style="border-color: var(--danger)">
          <b>无法连接本机管理服务</b>
          <div class="hint" style="margin-top: 6px">{{ state.error }}</div>
          <div class="hint" style="margin-top: 6px">
            请确认 LlamaDock 主进程正在运行（管理口监听 127.0.0.1:8081）。
          </div>
        </div>
        <component v-else :is="current.comp" />
      </section>
    </main>

    <div v-if="state.toast" class="toast" :class="state.toast.kind">{{ state.toast.message }}</div>
  </div>
</template>

<style scoped>
.side-status .status-line {
  font-size: 12px;
  color: var(--text-dim);
  font-weight: 500;
}
.side-status .status-sub {
  font-size: 11px;
  color: var(--text-mute);
  margin-left: auto;
  max-width: 96px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.topbar-metric {
  font-size: 12px;
  color: var(--text-mute);
  white-space: nowrap;
  margin-right: 2px;
}
.topbar-metric b {
  color: var(--text);
  font-variant-numeric: tabular-nums;
  font-weight: 650;
}
.topbar-metric .metric-sep {
  margin: 0 6px;
  opacity: 0.5;
}
</style>
