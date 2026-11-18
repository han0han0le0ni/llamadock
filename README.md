# LlamaDock · 本地大模型控制台 + 局域网推理网关

一个给自己玩的**本地大模型控制台 + 局域网推理网关**：双击打开窗口，把本机的 GGUF 模型
以 **OpenAI 兼容 API** 共享给自己的手机 / 笔记本 / 平板。

**完全独立**：不依赖 LM Studio、Ollama、Python 或任何外部框架。`llama-server` 子进程由本程序自己
拉起与回收，显存与上下文也由本程序决定。

- **约 80 个 AI 参数**可视化调节，加载级参数真正变成 `llama-server` 的启动开关（不是只写进配置里当摆设）
- **硬件自检 + 算术推荐参数**：自动读本机显存与模型头部，算出 `n_gpu_layers` / `n_ctx` / `cache_type_*` 该填多少，**并给出可核对的算式依据**
- **Chat 模板不再折磨人**：消息自动规范化、内置模板库、模型名自动套模板，手机客户端的模板报错基本绝迹
- **可调度**：按需拉起、空闲卸载、端口池只绑回环
- **零摩擦接入**：二维码 + 一键复制，同一 WiFi 下手机只填 `base_url`（无鉴权、无需 API Key）
- **多格式输出**：SSE 流式 / NDJSON / 纯文本 / 单次 JSON
- 单进程、单文件可执行程序，托盘常驻；深色 / 浅色主题可切换

> 设计依据见 `docs/AI-Runner-v2-个人自用版.md`（文件名保留历史名，内容已更新为 LlamaDock）。
> 本仓库按该设计实现，并在下方「与设计文档的差异」中列出所有有意偏离。

---

## 1. 快速开始

```powershell
# 依赖：Go 1.22+、Node 18+（编译期）；运行期只需要一份 llama-server.exe
powershell -ExecutionPolicy Bypass -File build.ps1   # 前端 + 测试 + 两个可执行文件

build\llamadock-desktop.exe       # 窗口版（含系统托盘）
build\llamadock.exe -no-browser   # 无窗口版，管理台在 http://127.0.0.1:8081
```

**准备运行时**（任选一种，程序会自己找到）：

1. 把官方 [llama.cpp](https://github.com/ggml-org/llama.cpp/releases) 的 Windows 压缩包解压到
   `runtime\llama.cpp\`（与 exe 同级）——推荐，零配置；
2. 或在「设置 → 推理引擎」里直接填 `llama-server.exe` 的完整路径；
3. 或把 `llama-server.exe` 所在目录加进系统 PATH。

带 CUDA 的机器请用 `cuda` 变体压缩包；程序会按「cuda > vulkan > cpu」自动择优。

开发模式：

```powershell
go run .                          # headless + 自动打开浏览器管理台
cd frontend; npm run dev          # 前端热更新（已配置 /api 代理到 8081）
go build -tags desktop -o llamadock-desktop.exe .   # 桌面窗口版
```

`wails build` 同样可用（根目录已配置 `wails.json`，前端构建产物直接进 `internal/webui/dist`）。

> 说明：Go module 名仍为 `airunner`、配置目录仍为 `%APPDATA%/AIRunner`（内部标识与用户数据路径，
> 保持兼容不随品牌改名），不影响使用。

---

## 2. 它到底做了什么

```
┌─────────────────────── llamadock.exe（单进程）──────────────────────┐
│  Wails 窗口（同一份 Vue3 前端）│  浏览器管理台 http://127.0.0.1:8081  │
│            └──────────► 本机管理 API（只绑回环）◄──────────┘          │
│                                   │                                  │
│                     编排核心 core（状态/参数/推荐/日志/指标）          │
│                                   │                                  │
│        ┌──────────────┬───────────┴──────────┬──────────────┐        │
│        │ 参数注册表    │  硬件自检 hwprobe     │  推荐 advisor │        │
│        │ params(87项) │  显存/CPU/内存/磁盘   │  纯算术+算式  │        │
│        └──────────────┴──────────────────────┴──────────────┘        │
│                                   │                                  │
│      引擎管理（拉起/回收/健康检查/空闲卸载）│  推理网关 :8080（OpenAI 兼容）│
│      模板管道（消息规范化 / 自动套模板 / 报错翻译）                    │
└───────────────────────────────────┼──────────────────────────────────┘
                                    ▼
                  llama-server 子进程 :10001+（仅 127.0.0.1，模型与推理）
```

**分工**：本程序既管模型文件（扫描 GGUF 库）也管推理进程（拼参数、拉子进程、控显存），
对外只暴露一个 OpenAI 兼容网关。**整条链路里没有任何外部软件**。

---

## 3. 工程结构

| 路径 | 作用 |
| --- | --- |
| `main_headless.go` / `main_desktop.go` | 两个入口（默认 headless，`-tags desktop` 为窗口版） |
| `cmd/ai-runner` | headless 可执行文件（带命令行参数） |
| `internal/params` | **参数注册表**：87 个参数只声明一次，UI/校验/Schema/下发全由它生成 |
| `internal/hwprobe` | 硬件自检：nvidia-smi 精确显存、WMI 降级、CPUID 指令集、磁盘剩余 |
| `internal/gguf` | GGUF 头部解析（层数/隐藏维度/KV head/原生上下文/量化），只读头部不加载权重 |
| `internal/advisor` | 推荐引擎：显存预算 → 缓存量化 → 上下文 → 卸载层数，逐条给出算式 |
| `internal/engine/llamacpp` | 自管引擎：运行时探测择优、端口池、GGUF 扫描、`llama-server` 生命周期、启动参数拼装 |
| `internal/engine` | 引擎适配接口（当前只有上面的实现，作为将来新增后端的扩展点） |
| `internal/server` | 推理网关：参数合并、**消息规范化**、多格式协商、SSE 原样透传、记账、报错翻译 |
| `internal/core` | 编排核心：状态、指标、请求日志、配置、**按模型名自动套模板**、服务参数一致性 |
| `internal/api` | 本机管理 API（仅 127.0.0.1）+ 前端静态托管 |
| `internal/tray` | Windows 系统托盘（纯 syscall 调 Win32，无 cgo、无第三方依赖） |
| `frontend` | Vue3 + Vite 前端（6 个页面），构建产物进 `internal/webui/dist` |

---

## 4. 参数体系

- **87 个参数**，全部在 `internal/params/defs.go` 声明一次，属性包括：类型、范围、默认值、单位、分组、
  生效方式（立即/需重启）、作用域、**是否请求级可覆盖**、中英标签与说明。
- **两级作用域**：请求参数 > 全局默认（另有模型级「记忆」覆盖，见 §5.1）。
- **安全约束**：`n_gpu_layers` / `n_ctx` / `cache_type_*` 等加载级参数**任何请求都无法覆盖**，
  网关会拒绝并在日志中记录（UI 上标 🔒）。
- **两条互不重叠的下发路径**：加载级参数 → `BuildArgs` 拼成 `llama-server` 的**启动开关**；
  采样/长度/控制级参数 → `EngineBody` 进 **chat 请求体**。加载级参数绝不会混进请求体。
- 采样类参数**显式下发**，保证「面板上看到的 = 引擎实际用的」；无意义取值（`max_tokens=-1`、空停止串、
  `logprobs=false`、默认枚举）不下发，避免与引擎的严格校验冲突。

## 5. Chat 模板：不再折磨人

这是本项目花大力气打磨的一块——手机客户端最常见的 `Jinja Exception` 类报错在这里被系统性地解决。

### 5.1 消息规范化（默认开启）
网关在转发前把 `messages` 里的 `system` 消息统一提到首位。Llama 3 / Qwen 2.5 / Gemma 等模型的
模板要求 system 必须是第一条，而手机 APP 常把它放中间或不带——这是
`System message must be at the beginning` 报错的根因。可在「设置 → 局域网网关」关闭（高级）。

### 5.2 按模型名自动套模板（默认开启）
加载模型时若用户**未手动指定模板**，程序按「设置 → 模型名自动套用 Chat 模板」里的规则，
用模型名关键字（小写子串）匹配并自动套用对应模板。开箱即用内置 Qwen / Llama 3 / Gemma / ChatML
等规则，可在设置页增删改。优先级：**手动指定 > 自动映射 > 模型自带模板**。规则在下次加载模型时生效。

### 5.3 内置模板库 + 通用宽松兜底
「参数」页可一键套用主流模板：auto（模型自带）、**通用宽松**（报错概率最低，不挑角色顺序、无特殊 token）、
ChatML、Llama 3、Qwen 2.5、Gemma、Alpaca。选自定义模板时程序自动开启引擎的 `--jinja`——
否则新版 llama.cpp 会拒载内联 Jinja 模板。

### 5.4 报错人话翻译
引擎返回的模板类报错会被翻译成中文并保留原文，直接显示在请求错误里，不用再翻 llama-server 日志猜。

> `prompt_template`（替换式 `{{.System}}/{{.Prompt}}`）是占位参数，本版只保存不生效；模板能力统一走 `chat_template`。

## 6. 参数推荐（纯算术，可核对）

```
① 可用显存 = 专用显存 − 512MB − 当前占用        （目标模型自己已加载时，其占用会被扣除）
② cache_type_k/v   可用 ≥ 模型×2.0 → f16 ｜ ≥1.5 → q8_0 ｜ 否则 q4_0
③ n_ctx   kv/token = 2 × n_layer × (n_embd/n_head × n_head_kv) × bytes(cache)
          n_ctx = min(模型原生上限, KV 预算 ÷ kv/token) 向下取 2 的幂
④ n_gpu_layers = min(n_layer, ⌊(可用 − KV) ÷ 每层大小⌋ × 0.95)
⑤ n_batch = 512(GPU) / n_threads = min(物理核, 16)
⑥ flash_attn / mmap / mlock
```

界面上每一条都能展开看算式与代入数值，例如：

```
n_gpu_layers  31 / 32
  模型 4.7GB ÷ 32 层 = 147MB/层
  可用显存 4.6GB − KV 0.8GB = 3.8GB
  ⌊3.8GB ÷ 147MB⌋ = 26 层 → 应用 5% 安全收敛 → 31 层
```

推荐值只是**预填默认**，随时可手改；服务页与模型页都可触发重算。

## 7. 网关与接入

| 能力 | 说明 |
| --- | --- |
| `POST /v1/chat/completions` | 对话（流式 / 非流式） |
| `POST /v1/completions` `/v1/embeddings` | 补全 / 向量化（透传） |
| `GET /v1/models` | 模型列表（含量化、架构、加载状态） |
| `GET /healthz` `/readyz` `/api/status` | 探针与状态（浏览器直接看） |
| 输出格式 | `stream:true` → SSE；`Accept: application/x-ndjson` → 逐行 JSON；`?format=text` → 纯文本；默认单次 JSON |
| 模型解析 | 空模型名 → 当前已加载模型；未加载 → 按配置自动拉起；自动加载关闭 → `503` + 明确原因 |
| 错误码 | 复用 OpenAI 错误体：`MODEL_NOT_FOUND` / `PARAM_INVALID` / `QUEUE_FULL` / `ENGINE_OFFLINE` … |

手机端：**Base URL 填 `http://<局域网IP>:8080/v1`，API Key 留空**即可，任何 OpenAI 客户端零改代码。

## 8. 已知取舍与边界（有意为之）

| 项 | 说明 |
| --- | --- |
| 无鉴权、无来源过滤 | 定位是「家里自己用」，靠路由器不开端口映射。请勿在公司 / 合租 / 公共 WiFi 下暴露 |
| 明文 HTTP | 同上，属有意识接受的技术债 |
| 单模型常驻 | 一次只跑一个 `llama-server`（换模型 = 重启子进程）。多实例并存留待后续 |
| 不下载模型 | 只扫描本地既有 gguf，不含模型商店 / 断点续传 / 完整性校验 |
| 不自动下载运行时 | 需要用户自备 `llama-server.exe`（解压到 `runtime\llama.cpp\` 即可） |
| 请求日志上限 | 最近 200 条，内存环形缓冲（不落库） |

## 9. 与设计文档的差异

1. **本机管理 API（127.0.0.1:8081）**：设计文档写的是「只有 Wails 绑定、没有浏览器管理台」。
   实现上我用**一份 HTTP 管理 API + 同构前端**同时服务窗口与浏览器，理由：① 前端只需一条数据通路，
   不会出现窗口与浏览器两套逻辑；② headless 兜底（WebView2 缺失）零成本；③ 便于自动化验证。
   该端口**只绑回环**，局域网仍连不上。
2. **图表用自绘 canvas**（约 120 行的 `LineChart.vue`），未引入 ECharts：少 1MB 依赖，指标曲线只有折线需求。
3. **系统托盘自研**：Wails v2 不含托盘，第三方库需要 C 编译器，故用 `internal/tray` 直接调 Win32 API。
4. **没有实例池 / LRU 淘汰 / Pin**：与 v2.0 文档一致（多模型并存留待「加回菜单」）。
5. **无首次运行向导**：与 v2.0 文档一致，改为「设置页 + 状态提示」引导。
6. **模板能力超出原设计**：消息规范化、模型名自动套模板、内置模板库、报错翻译均为实现期新增，
   原设计仅有基本的 `chat_template_mode` / `chat_template` 两个参数。

## 10. 验证情况

- `go test ./...`：参数注册表自洽性、作用域合并、字段下发规则、运行时择优（含
  「硬件自检晚于探测 → 必须重新择优」这条回归）、启动参数拼装（含 `--jinja` 成对下发）、
  消息规范化 / 报错翻译、按模型名匹配模板等用例全部通过。
- 真机端到端（本机 RTX 5060 8GB，GGUF 库 6 个模型）：
  运行时探测到 8 个候选并自动选中 `cuda12`；模型全部扫描出结构参数；
  非流式 / SSE / NDJSON / 纯文本四种格式均拿到真实回复。
- 六个页面 + 硬件自检 + 二维码 + 托盘 + 深色/浅色主题均已实际运行核对。
