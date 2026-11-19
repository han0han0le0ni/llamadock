<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../api'
import { state, notify, refreshStatus, refreshNetwork, engineLabel, engineSchedulable } from '../store'
import LineChart from '../components/LineChart.vue'
import QrCode from '../components/QrCode.vue'
import Icon from '../components/Icon.vue'

const testing = ref(false)
const prompt = ref('用一句话介绍你自己。')
const reply = ref('')
const testModel = ref('')

const st = computed(() => state.status)
const hw = computed(() => state.hardware || (st.value && st.value.hardware) || null)
const net = computed(() => state.network)

// 网卡列表：推荐项优先，其次是可用项
const lanIfaces = computed(() => {
  const list = ((net.value && net.value.interfaces) || []).filter((i) => !i.loopback)
  return list.slice().sort((a, b) => (b.recommended ? 1 : 0) - (a.recommended ? 1 : 0))
})
// 程序自动选用的网卡（决定 Base URL / 二维码）
const recommendedIface = computed(() => {
  const list = (net.value && net.value.interfaces) || []
  return list.find((i) => i.recommended) || null
})

const tpsSeries = computed(() => [
  {
    name: 't/s',
    color: '--accent',
    points: ((st.value && st.value.metrics.recent) || []).map((s) => ({ t: s.t, v: s.tps })),
  },
])
const vramSeries = computed(() => [
  {
    name: '显存',
    color: '--accent-2',
    points: ((st.value && st.value.metrics.recent) || []).map((s) => ({ t: s.t, v: s.vram_used_mb / 1024 })),
  },
])

const vram = computed(() => {
  const g = hw.value && hw.value.gpus && hw.value.gpus.length ? pickPrimary(hw.value.gpus) : null
  if (!g) return null
  return {
    total: g.vram_dedicated_mb,
    used: g.vram_used_mb,
    free: g.vram_free_mb || g.vram_dedicated_mb - g.vram_used_mb,
  }
})
const usedPct = computed(() => (vram.value ? Math.min(100, (vram.value.used / vram.value.total) * 100) : 0))

function pickPrimary(gpus) {
  return gpus.reduce((a, b) => (b.vram_dedicated_mb > a.vram_dedicated_mb ? b : a), gpus[0])
}

function fmtUptime(sec) {
  sec = sec || 0
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

async function rescan() {
  try {
    state.hardware = await api.rescanHardware()
    notify('硬件自检完成', 'ok')
    await refreshStatus()
  } catch (e) {
    notify(e.message, 'err')
  }
}

async function runTest() {
  testing.value = true
  reply.value = ''
  try {
    const r = await api.testChat(testModel.value, prompt.value)
    reply.value = r.reply
    notify(`完成，用时 ${r.duration_ms} ms`, 'ok')
    await refreshStatus()
  } catch (e) {
    notify(e.message, 'err', 6000)
  } finally {
    testing.value = false
  }
}

function copy(text) {
  navigator.clipboard.writeText(text).then(
    () => notify('已复制', 'ok', 1500),
    () => notify('复制失败', 'err'),
  )
}

onMounted(refreshNetwork)
</script>

<template>
  <div class="grid" style="gap: 16px">
    <div class="grid cols-4">
      <div class="card tight">
        <div class="card-head">
          <span class="dot" :class="st && st.engine.health.online ? 'ok' : 'err'"></span>
          <h3>推理引擎</h3>
        </div>
        <div class="stat-value">
          {{ st && st.engine.health.online ? '在线' : '离线' }}<small> · {{ engineLabel }}</small>
        </div>
        <div class="stat-label">
          {{ st ? st.engine.endpoint : '' }}
          <span v-if="engineSchedulable"> · 自管</span>
          <span v-if="st && st.engine.health.online"> · {{ st.engine.health.latency_ms }}ms</span>
        </div>
      </div>

      <div class="card tight">
        <div class="card-head"><h3>吞吐</h3></div>
        <div class="stat-value">{{ st ? st.metrics.tps.toFixed(1) : '0.0' }}<small> t/s</small></div>
        <div class="stat-label">
          活跃 {{ st ? st.metrics.active : 0 }} · 平均 {{ st ? st.metrics.avg_tps.toFixed(1) : '0' }} t/s · 首字
          {{ st ? Math.round(st.metrics.ttft_ms) : 0 }}ms
        </div>
      </div>

      <div class="card tight">
        <div class="card-head"><h3>请求</h3></div>
        <div class="stat-value">{{ st ? st.metrics.total_requests : 0 }}<small> 次</small></div>
        <div class="stat-label">
          失败 {{ st ? st.metrics.failed_requests : 0 }} · 输出 {{ st ? st.metrics.completion_tokens : 0 }} tokens ·
          设备 {{ st ? st.devices : 0 }}
        </div>
      </div>

      <div class="card tight">
        <div class="card-head"><h3>显存</h3></div>
        <div class="stat-value">
          {{ vram ? (vram.used / 1024).toFixed(1) : '—' }}<small> / {{ vram ? (vram.total / 1024).toFixed(1) : '—' }} GB</small>
        </div>
        <div class="stat-label">运行 {{ fmtUptime(st && st.uptime_sec) }}</div>
      </div>
    </div>

    <div v-if="st && st.notes && st.notes.length" class="card tight" style="border-color: var(--warn)">
      <div v-for="(n, i) in st.notes" :key="i" class="hint" style="color: var(--warn-text)">⚠ {{ n }}</div>
    </div>

    <div class="grid cols-2">
      <div class="card">
        <div class="card-head">
          <h3><Icon name="activity" :size="16" />吞吐曲线（2 秒采样）</h3>
          <div class="spacer"></div>
          <span class="badge">{{ st ? st.metrics.recent.length : 0 }} 点</span>
        </div>
        <LineChart :series="tpsSeries" :height="180" :y-format="(v) => (v < 5 ? v.toFixed(1) : v.toFixed(0))" empty-text="还没有请求，先到「服务」页试运行一次" />
      </div>
      <div class="card">
        <div class="card-head">
          <h3><Icon name="drive" :size="16" />显存占用（GB）</h3>
          <div class="spacer"></div>
          <span class="badge" :class="usedPct > 90 ? 'err' : usedPct > 75 ? 'warn' : 'ok'">{{ usedPct.toFixed(0) }}%</span>
        </div>
        <LineChart :series="vramSeries" :height="180" :y-format="(v) => v.toFixed(1)" />
        <div class="bar" style="margin-top: 12px">
          <i class="used" :style="{ width: usedPct + '%' }"></i>
        </div>
        <div class="hint" style="margin-top: 6px">
          {{ vram ? `已用 ${(vram.used / 1024).toFixed(1)} GB · 空闲 ${(vram.free / 1024).toFixed(1)} GB` : '未读到显存信息' }}
          <span v-if="hw && hw.gpus && hw.gpus[0]">（来源：{{ hw.gpus[0].source }}）</span>
        </div>
      </div>
    </div>

    <div class="grid cols-2">
      <div class="card">
        <div class="card-head">
          <h3><Icon name="cpu" :size="16" />硬件自检</h3>
          <div class="spacer"></div>
          <button class="sm" @click="rescan"><Icon name="refresh" :size="13" />重新自检</button>
        </div>
        <table v-if="hw">
          <tbody>
            <tr>
              <th style="width: 130px">GPU</th>
              <td>{{ hw.gpus && hw.gpus.length ? pickPrimary(hw.gpus).name : '未检测到' }}</td>
            </tr>
            <tr>
              <th>专用显存</th>
              <td>{{ hw.gpus && hw.gpus.length ? (pickPrimary(hw.gpus).vram_dedicated_mb / 1024).toFixed(1) + ' GB' : '—' }}</td>
            </tr>
            <tr>
              <th>驱动</th>
              <td>{{ hw.gpus && hw.gpus.length ? pickPrimary(hw.gpus).driver || '—' : '—' }}</td>
            </tr>
            <tr>
              <th>CPU</th>
              <td>{{ hw.cpu.name }}（{{ hw.cpu.physical_cores }} 物理核 / {{ hw.cpu.logical_cores }} 逻辑核）</td>
            </tr>
            <tr>
              <th>指令集</th>
              <td>{{ (hw.cpu.instruction_sets || []).join(' · ') || '—' }}</td>
            </tr>
            <tr>
              <th>内存</th>
              <td>{{ (hw.memory.total_mb / 1024).toFixed(1) }} GB（可用 {{ (hw.memory.avail_mb / 1024).toFixed(1) }} GB）</td>
            </tr>
            <tr>
              <th>可用后端</th>
              <td>{{ (hw.backends || []).join(' · ') }}</td>
            </tr>
            <tr>
              <th>磁盘</th>
              <td class="mono">
                {{ (hw.disks || []).map((d) => `${d.path} 剩余 ${d.free_gb.toFixed(0)}G`).join(' ｜ ') || '—' }}
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else class="empty">暂无自检结果</div>
      </div>

      <div class="card">
        <div class="card-head">
          <h3><Icon name="globe" :size="16" />设备接入（手机 / 笔记本 / 平板）</h3>
        </div>
        <div v-if="net" class="row wrap" style="gap: 18px; align-items: flex-start">
          <QrCode :text="net.base_url" :size="170" />
          <div style="flex: 1; min-width: 240px">
            <div class="hint">Base URL（API Key 留空）</div>
            <div class="row" style="margin: 6px 0 12px">
              <input readonly :value="net.base_url" />
              <button class="sm" @click="copy(net.base_url)"><Icon name="copy" :size="13" />复制</button>
            </div>
            <div class="hint">{{ net.tip }}</div>
            <div class="hint" style="margin-top: 8px">
              自动选用网卡：
              <span v-if="recommendedIface" class="mono">{{ recommendedIface.ip }}（{{ recommendedIface.name }}）</span>
              <span v-else class="mono">无</span>
            </div>
            <div v-if="lanIfaces.length" class="hint" style="margin-top: 8px">
              全部网卡：
              <span
                v-for="i in lanIfaces"
                :key="i.name + i.ip"
                class="mono"
                :style="{ opacity: i.usable ? 1 : 0.4, marginRight: '10px', whiteSpace: 'nowrap' }"
              >
                {{ i.ip }}
                <template v-if="i.recommended">（已选用）</template>
                <template v-else-if="i.virtual">（虚拟）</template>
                <template v-else-if="i.link_local">（无效）</template>
                <template v-else-if="!i.usable">（备用）</template>
              </span>
            </div>
          </div>
        </div>
        <div v-else class="empty">加载中…</div>
      </div>
    </div>

    <div class="card">
      <div class="card-head">
        <h3><Icon name="terminal" :size="16" />快捷试运行</h3>
        <div class="spacer"></div>
        <select v-model="testModel" style="max-width: 260px">
          <option value="">（自动选择已加载模型）</option>
          <option v-for="m in state.models" :key="m.id" :value="m.id">{{ m.id }}</option>
        </select>
      </div>
      <div class="row">
        <input v-model="prompt" placeholder="输入一句话，直接问本机模型" @keyup.enter="runTest" />
        <button class="primary" :disabled="testing" @click="runTest">
          <Icon :name="testing ? 'refresh' : 'play'" :size="14" />{{ testing ? '生成中…' : '发送' }}
        </button>
      </div>
      <pre v-if="reply" class="copybox">{{ reply }}</pre>
      <div v-else class="hint" style="margin-top: 8px">
        直接经引擎发一次非流式请求，用于验证「引擎可用」。管理台与局域网客户端走的是同一个引擎。
      </div>
    </div>
  </div>
</template>
