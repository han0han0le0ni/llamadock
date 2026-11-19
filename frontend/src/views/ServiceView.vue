<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../api'
import { state, notify, refreshNetwork, refreshStatus, refreshConfig, engineLabel } from '../store'
import QrCode from '../components/QrCode.vue'
import Icon from '../components/Icon.vue'

const tab = ref('curl')
const testing = ref(false)
const prompt = ref('你好，请用一句话自我介绍。')
const reply = ref('')
const testModel = ref('')
const meta = ref({ ms: 0, chars: 0 })

const net = computed(() => state.network)
const cfg = computed(() => state.config)
const st = computed(() => state.status)

// 程序自动选用的网卡（决定 Base URL / 二维码）
const recommendedIface = computed(() => {
  const list = (net.value && net.value.interfaces) || []
  return list.find((i) => i.recommended) || null
})

async function toggle() {
  try {
    if (st.value && st.value.gateway_running) {
      await api.gatewayStop()
      notify('网关已停止', 'warn')
    } else {
      await api.gatewayStart()
      notify('网关已启动', 'ok')
    }
    await refreshStatus()
  } catch (e) {
    notify(e.message, 'err')
  }
}

async function runTest() {
  testing.value = true
  reply.value = ''
  const t0 = performance.now()
  try {
    const r = await api.testChat(testModel.value, prompt.value)
    reply.value = r.reply
    meta.value = { ms: r.duration_ms, chars: (r.reply || '').length }
    notify('试运行成功', 'ok', 1800)
    await refreshStatus()
  } catch (e) {
    notify(e.message, 'err', 7000)
  } finally {
    testing.value = false
  }
}

// 直接打自己的网关（验证局域网路径是否通）
async function runViaGateway() {
  const base = net.value ? net.value.base_url : ''
  if (!base) return
  testing.value = true
  reply.value = ''
  try {
    const body = {
      model: testModel.value,
      messages: [{ role: 'user', content: prompt.value }],
      stream: false,
    }
    const r = await fetch(base + '/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    const data = await r.json()
    reply.value = data?.choices?.[0]?.message?.content || JSON.stringify(data, null, 2)
    meta.value = { ms: 0, chars: (reply.value || '').length }
    notify('网关链路正常', 'ok', 1800)
    await refreshStatus()
  } catch (e) {
    reply.value = '网关请求失败：' + e.message
    notify('网关链路失败（网络层）', 'err', 6000)
  } finally {
    testing.value = false
  }
}

function copy(t) {
  navigator.clipboard.writeText(t).then(
    () => notify('已复制', 'ok', 1400),
    () => notify('复制失败', 'err'),
  )
}

const host = computed(() => (net.value ? net.value.host : ''))
const port = computed(() => (cfg.value ? cfg.value.server.port : 8080))

const formatTips = [
  { k: 'stream: true', v: 'SSE 流式，逐 token 返回，末帧 [DONE]' },
  { k: 'Accept: application/x-ndjson', v: '每行一个 JSON 对象' },
  { k: '?format=text', v: '纯文本流，浏览器地址栏直接看' },
  { k: 'stream: false', v: '单次 JSON 完整响应' },
]

onMounted(async () => {
  await refreshNetwork()
  await refreshConfig()
})
</script>

<template>
  <div class="grid" style="gap: 16px">
    <div class="card">
      <div class="card-head">
        <span class="dot" :class="st && st.gateway_running ? 'ok' : 'off'"></span>
        <h3>推理网关</h3>
        <span class="badge" :class="st && st.gateway_running ? 'ok' : ''">
          {{ st && st.gateway_running ? '运行中' : '已停止' }}
        </span>
        <div class="spacer"></div>
        <button class="primary" @click="toggle">
          <Icon :name="st && st.gateway_running ? 'stop' : 'play'" :size="14" :stroke="2.2" />
          {{ st && st.gateway_running ? '停止网关' : '启动网关' }}
        </button>
      </div>
      <div class="grid cols-3">
        <div>
          <div class="hint">监听地址</div>
          <div class="mono">{{ cfg ? cfg.server.host + ':' + cfg.server.port : '—' }}</div>
        </div>
        <div>
          <div class="hint">局域网地址</div>
          <div class="mono">{{ net ? net.base_url : '—' }}</div>
        </div>
        <div>
          <div class="hint">并发 / 超时</div>
          <div class="mono">
            {{ cfg ? cfg.server.max_concurrent_requests : '—' }} 并发 · {{ cfg ? cfg.server.request_timeout_sec : '—' }}s
          </div>
        </div>
      </div>
      <div class="hint" style="margin-top: 10px">
        本程序定位为「自己家里用」：不鉴权、不做来源过滤。请确认路由器没有把这个端口映射到公网。
      </div>
    </div>

    <div class="grid cols-2">
      <div class="card">
        <div class="card-head"><h3><Icon name="globe" :size="16" />设备接入</h3><div class="spacer"></div>
          <button class="sm" @click="refreshNetwork"><Icon name="refresh" :size="13" />刷新</button>
        </div>
        <div v-if="net" class="row wrap" style="gap: 18px; align-items: flex-start">
          <QrCode :text="net.base_url" :size="168" />
          <div style="flex: 1; min-width: 220px">
            <div class="hint">手机 AI 客户端只填 Base URL，API Key 留空</div>
            <div class="row" style="margin: 8px 0">
              <input readonly :value="net.base_url" />
              <button class="sm" @click="copy(net.base_url)"><Icon name="copy" :size="13" />复制</button>
            </div>
            <div class="row wrap" style="gap: 6px">
              <button class="sm" @click="tab = 'curl'">cURL</button>
              <button class="sm" @click="tab = 'python'">Python</button>
              <button class="sm" @click="tab = 'node'">Node</button>
            </div>
            <pre class="copybox">{{ tab === 'python' ? net.python : tab === 'node' ? net.node : net.curl }}</pre>
            <button class="sm" style="margin-top: 8px" @click="copy(tab === 'python' ? net.python : tab === 'node' ? net.node : net.curl)">
              <Icon name="copy" :size="13" />复制示例
            </button>
            <div class="hint" style="margin-top: 8px">{{ net.tip }}</div>
            <div class="hint" style="margin-top: 4px">
              自动选用网卡：
              <span v-if="recommendedIface" class="mono">{{ recommendedIface.ip }}（{{ recommendedIface.name }}）</span>
              <span v-else class="mono">无</span>
            </div>
          </div>
        </div>
        <div v-else class="empty">加载中…</div>
      </div>

      <div class="card">
        <div class="card-head"><h3><Icon name="terminal" :size="16" />多格式响应</h3></div>
        <table>
          <tbody>
            <tr v-for="f in formatTips" :key="f.k">
              <th style="width: 240px" class="mono">{{ f.k }}</th>
              <td class="hint">{{ f.v }}</td>
            </tr>
          </tbody>
        </table>
        <div class="hint" style="margin-top: 10px">
          OpenAI 兼容入口：<span class="mono">{{ net ? net.base_url : '/v1' }}</span><br />
          健康探针：<span class="mono">{{ host }}/healthz</span> · 运行状态：<span class="mono">{{ host }}/api/status</span>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-head">
        <h3><Icon name="activity" :size="16" />试运行面板</h3>
        <div class="spacer"></div>
        <select v-model="testModel" style="max-width: 260px">
          <option value="">（自动选择已加载模型）</option>
          <option v-for="m in state.models" :key="m.id" :value="m.id">{{ m.id }}</option>
        </select>
      </div>
      <div class="row">
        <input v-model="prompt" @keyup.enter="runTest" />
        <button :disabled="testing" @click="runTest"><Icon name="cpu" :size="14" />直连引擎</button>
        <button class="primary" :disabled="testing || !(st && st.gateway_running)" @click="runViaGateway">
          <Icon name="globe" :size="14" />经网关
        </button>
      </div>
      <div class="hint" style="margin-top: 8px">
        「直连引擎」验证 {{ engineLabel }} 本身；「经网关」走本程序 :{{ port }} 端口，等价于手机端的访问路径。
      </div>
      <pre v-if="reply" class="copybox">{{ reply }}</pre>
      <div v-if="reply" class="hint" style="margin-top: 6px">
        用时 {{ meta.ms }}ms · {{ meta.chars }} 字符
      </div>
    </div>
  </div>
</template>
