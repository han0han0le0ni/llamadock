<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import { state, notify, refreshRequests, refreshLogs } from '../store'

const tab = ref('requests')
const level = ref('')
const fModel = ref('')
const fStatus = ref(0)
const fKeyword = ref('')

const reqs = computed(() => state.requests)
const stats = computed(() => state.requestStats)
const logs = computed(() => state.logs)

async function load() {
  const query = {
    limit: 200,
    model: fModel.value,
    status: fStatus.value,
    keyword: fKeyword.value,
  }
  // 让后台轮询沿用当前筛选，避免筛选结果 2.5 秒后被轮询冲掉
  state.requestQuery = query
  await refreshRequests(query)
}

// 筛选条件变化后自动查询（防抖），不必手点「查询」
let searchTimer = null
watch([fModel, fStatus, fKeyword], () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(load, 350)
})

async function loadLogs() {
  await refreshLogs(level.value)
}

function fmtTime(t) {
  return new Date(t).toLocaleTimeString('zh-CN', { hour12: false })
}

function exportCSV() {
  const head = [
    'time',
    'source_ip',
    'model',
    'endpoint',
    'format',
    'status',
    'prompt_tokens',
    'completion_tokens',
    'ttft_ms',
    'duration_ms',
    'error',
  ]
  const lines = [head.join(',')]
  reqs.value.forEach((r) => {
    lines.push(
      [
        new Date(r.time).toISOString(),
        r.source_ip,
        `"${r.model}"`,
        r.endpoint,
        r.format,
        r.status,
        r.prompt_tokens,
        r.completion_tokens,
        r.ttft_ms,
        r.duration_ms,
        `"${(r.error || '').replace(/"/g, "'")}"`,
      ].join(','),
    )
  })
  const blob = new Blob(['\ufeff' + lines.join('\n')], { type: 'text/csv;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `llamadock-requests-${Date.now()}.csv`
  a.click()
  notify('已导出 CSV', 'ok', 1600)
}

async function clearAll() {
  if (!confirm('确定清空全部请求日志？')) return
  await api.clearRequests()
  await load()
  notify('已清空', 'ok', 1400)
}

onMounted(async () => {
  await load()
  await loadLogs()
})
</script>

<template>
  <div class="grid" style="gap: 14px">
    <div class="grid cols-4">
      <div class="card tight">
        <div class="stat-value">{{ stats ? stats.total : 0 }}</div>
        <div class="stat-label">记录数</div>
      </div>
      <div class="card tight">
        <div class="stat-value">{{ stats ? stats.failed : 0 }}</div>
        <div class="stat-label">失败</div>
      </div>
      <div class="card tight">
        <div class="stat-value">{{ stats ? stats.prompt_tokens : 0 }}</div>
        <div class="stat-label">输入 tokens（估算）</div>
      </div>
      <div class="card tight">
        <div class="stat-value">{{ stats ? stats.completion_tokens : 0 }}</div>
        <div class="stat-label">输出 tokens</div>
      </div>
    </div>

    <div class="card">
      <div class="card-head">
        <div class="tabs">
          <button :class="{ active: tab === 'requests' }" @click="tab = 'requests'">请求日志</button>
          <button :class="{ active: tab === 'runtime' }" @click="tab = 'runtime'">运行日志</button>
        </div>
        <div class="spacer"></div>
        <template v-if="tab === 'requests'">
          <input
            v-model="fKeyword"
            placeholder="搜索 IP / 模型 / 路径（自动查询）"
            style="max-width: 220px"
            @keyup.enter="load"
          />
          <select v-model="fModel" style="max-width: 200px">
            <option value="">全部模型</option>
            <option v-for="m in state.models" :key="m.id" :value="m.id">{{ m.id.split('/').pop() }}</option>
          </select>
          <select v-model.number="fStatus" style="max-width: 120px">
            <option :value="0">全部状态</option>
            <option :value="200">成功</option>
            <option :value="400">400</option>
            <option :value="404">404</option>
            <option :value="503">503</option>
          </select>
          <button class="sm" @click="load">查询</button>
          <button class="sm" @click="exportCSV">导出 CSV</button>
          <button class="sm danger" @click="clearAll">清空</button>
        </template>
        <template v-else>
          <select v-model="level" style="max-width: 140px">
            <option value="">全部级别</option>
            <option value="info">info</option>
            <option value="warn">warn</option>
            <option value="error">error</option>
            <option value="debug">debug</option>
          </select>
          <button class="sm" @click="loadLogs">刷新</button>
          <button class="sm danger" @click="api.clearLogs().then(loadLogs)">清空</button>
        </template>
      </div>

      <div v-if="tab === 'requests'">
        <div v-if="!reqs.length" class="empty">
          还没有请求。手机端或本机发起一次 <span class="mono">/v1/chat/completions</span> 后就会出现在这里（保留最近 200 条）。
        </div>
        <div v-else class="scroll-y" style="max-height: 56vh">
          <table>
            <thead>
              <tr>
                <th>时间</th>
                <th>来源</th>
                <th>模型</th>
                <th>端点</th>
                <th>格式</th>
                <th>状态</th>
                <th>tokens</th>
                <th>首字</th>
                <th>总耗时</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in reqs" :key="r.id">
                <td class="mono">{{ fmtTime(r.time) }}</td>
                <td class="mono">{{ r.source_ip }}</td>
                <td>{{ (r.model || '').split('/').pop() }}</td>
                <td class="mono">{{ r.endpoint }}</td>
                <td><span class="badge">{{ r.format }}</span></td>
                <td>
                  <span class="badge" :class="r.status === 200 ? 'ok' : 'err'">{{ r.status }}</span>
                </td>
                <td class="mono">{{ r.prompt_tokens }} / {{ r.completion_tokens }}</td>
                <td class="mono">{{ r.ttft_ms }}ms</td>
                <td class="mono">{{ r.duration_ms }}ms</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div v-else>
        <div v-if="!logs.length" class="empty">暂无日志</div>
        <div v-else class="scroll-y mono" style="max-height: 56vh; font-size: 12px">
          <div v-for="l in logs" :key="l.seq" style="padding: 3px 0; border-bottom: 1px dashed var(--border-soft)">
            <span class="hint">{{ fmtTime(l.time) }}</span>
            <span class="badge" :class="l.level === 'error' ? 'err' : l.level === 'warn' ? 'warn' : ''" style="margin: 0 6px">
              {{ l.level }}
            </span>
            <span>{{ l.msg }}</span>
            <span class="hint" v-if="l.attrs">
              {{ Object.entries(l.attrs).map(([k, v]) => `${k}=${v}`).join(' ') }}
            </span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
