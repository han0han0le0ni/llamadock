# LlamaDock —— 轻量级 AI 启动运行器 · 设计方案

> ⚠️ **本文档已被取代**。用户重申场景为「**个人游玩、个人局域网、不商用、不公布**」后，本 36 人日版本中约一半工作量为"分发给陌生用户"服务，已不再必要。
> **当前有效设计请见：[`AI-Runner-v2-个人自用版.md`](./AI-Runner-v2-个人自用版.md)（约 16 人日）。**
> 本文档保留作为**设计演进记录**与**加回功能的参考**（v2.0 文档 §9 的"加回菜单"引用了本文的模块分解）。

> 版本：**v1.7**（第七轮修订：**默认关闭鉴权**，改为「私网来源白名单 + 公网暴露检测」；接入零摩擦）
> 技术栈：Go 1.22+ / Wails v2 / net/http
> 目标平台：Windows 10 1803+ / 11 x64（架构预留 macOS、Linux）

---

## 决策记录

### v1.1 变更（第一轮）

| # | 决策项 | 结论 | 影响 |
| --- | --- | --- | --- |
| D1 | GUI 框架 | **Wails v2**（由我按最佳实践定稿） | 见 §2.1 |
| D2 | 推理后端 | **llama.cpp（子进程）+ Ollama（HTTP）** | 2 个适配器 |
| D3 | 使用场景 | ~~局域网团队共享~~ → **v1.2 修正为个人局域网使用** | 见 D5 |
| D4 | v1.0 功能 | 模型下载管理、多模型热切换、请求审计；不做多模态 | 实例池 + 调度器 |

### v1.2 变更（第二轮）

| # | 决策项 | 结论 | 影响 |
| --- | --- | --- | --- |
| D5 | 使用规模 | **个人局域网使用**（自己主力机跑模型，自己的手机/笔记本/平板访问） | **多租户治理大幅降级**：砍掉 QPS 令牌桶、Token 月配额、按 Key 聚合报表；保留少量访问令牌 + 审计 + 告警（*注：v1.7 的 D22 进一步取消了默认 API Key*） |
| D6 | 管理台权限 | **`/admin` 仅 `127.0.0.1`**，局域网只能访问 `/v1` 推理接口 | **引入双监听器**（Public / Admin 分离），绑定层隔离，见 §3.2 |
| D7 | 审计粒度 | 元数据 + **正文开关（默认关闭）**，支持一键清空正文 | 审计表加 `body_sample` 字段，独立清理入口 |
| D8 | Webhook 告警 | **纳入 v1.0** | 新增 `internal/alert` 模块 + 告警事件总线 + Windows Toast |
| D9 | 多模型根目录 | **纳入 v1.0**，含 Ollama 模型合并视图 | `models_roots[]` + 模型库分区展示 + 跨源去重 |

> **v1.2 的认知修正**：上一版把场景按"团队共享"设计，做了多 Key 配额矩阵、QPS 限速、月配额、按 Key 报表——**在个人场景下这些全是负担**。砍掉后腾出的工期投入到更有价值的「手机/平板零摩擦接入」和「人不在电脑前也能收到告警」上。

### v1.3 变更（第三轮 · 本次）

| # | 决策项 | 结论 | 影响 |
| --- | --- | --- | --- |
| D10 | **内置模型下载器** | **移除**。改为「模型获取引导」：内置推荐模型清单 → 一键用系统浏览器打开 HF/ModelScope 页面 → 你手动下载后拖入窗口或填路径注册 | 删除 `internal/downloader`（原估 5~6 天）→ 新增极轻量 `internal/hub`（约 1 天） |

> **D10 的连带收益**（为什么这一刀很值）：
> - 下载器是整个设计里**仅次于显存调度**的复杂度来源——自带任务队列、暂停/恢复状态机、失败重试、磁盘空间检查、进度事件、`.part` 残留清理
> - 移除后少一个并发子系统，少一类"下载到一半程序退出"的边界情况
> - 模型入库路径**统一为「扫目录 / 拖入」**，不再有"下载完成事件"这条旁路，`modelstore` 逻辑更干净
> - 总工期 **36 → 约 32 人日**
>
> 代价：用户要离开应用去浏览器下载。但这本就是一次性动作，且浏览器自带更好的断点续传与下载管理。
>
> **替代设计的价值不在"少个功能"，而在于换了个更好的入口**：把「下载」变成「推荐 + 外链 + 校验 + 注册」——推荐清单直接告诉你该下哪个量化、需要多少显存，省掉了自己在 HF 上翻几十个分片文件的分支，这恰恰是用户真正卡住的地方。

### v1.4 变更（第四轮 · 本次）—— v1.0 范围冻结

| # | 决策项 | 结论 | 影响 |
| --- | --- | --- | --- |
| D11 | **自签 HTTPS** | **移除**，只做 HTTP | 删除 `internal/tlsutil` 的证书链部分（约 1 天）。**二维码接入能力保留**，迁入 `internal/netinfo` |
| D12 | 模型根目录 | **首次启动时由用户选择**（目录选择器，默认建议非系统盘），写入 `models_roots[0]` | 新增**首次运行向导**（First-Run Wizard），涉及配置初始化与 UI 引导 |
| D13 | 推荐模型清单 | **内置静态 JSON**，不做远程更新 | `assets/recommended_models.json` 只读；无网络依赖 |
| D14 | Function Calling / 模型对比 / 开机静默自启 | **全部不纳入 v1.0** | v1.0 范围就此冻结（见 §15）。`tools`/`grammar` 仍可透传，只是不做一等公民的专项设计 |

> **D11 的取舍记录（值得留档）**：选择"只做 HTTP"意味着**同一 WiFi 下的其它设备可以抓到对话明文**。做出这个取舍的理由是：
> - 场景是"自己家的可信网络"，威胁模型很弱，不是公共 WiFi 或企业内网
> - 自签证书在手机端需要装 CA / 描述文件，对一个人用来说是**每次换设备都要重复的摩擦**
> - 移除后可省掉一整套证书生成、有效期管理、设备信任引导、证书过期告警的逻辑
>
> **缓解措施**（不额外花工期）：启动时明确提示"仅建议在可信网络使用"；`§13` 保留"检测到非私网来源请求时记警告"。（*原写的"令牌强制开启"已在 v1.7 的 D22 中被「私网来源白名单」取代——后者更直接地解决了同一个问题，且不给用户增加配置负担。*）
>
> **代价要讲清楚**：如果将来要在公司网络、合租网络、或公共 WiFi 下使用，必须补回 HTTPS——这属于**已知的、有意识的**技术债，不是遗漏。

> **D14 的边界说明**：`tools` / `grammar` / `json_schema` 仍然会被**正常透传**给引擎（llama.cpp 本身支持），只是：
> - 参数面板不为其做专门的置顶区块
> - 不做 OpenAI `tools` → llama.cpp `grammar` 的自动转换
> - 不列入 P0 测试用例
> 即"能用，但不保证、不优化"。这叫"不纳入范围"，而不是"不支持"。

### v1.5 变更（第五轮 · 本次）

| # | 决策项 | 结论 | 影响 |
| --- | --- | --- | --- |
| D15 | **硬件自检 + 参数推荐** | 启动时自动探测本机硬件，**用确定性算术规则**（非 AI / 非机器学习）推导推荐参数，并给出可解释的算式依据 | 新增 `internal/hwprobe`（≈1.5 天）+ `internal/advisor`（≈1.5 天）；总工期 32 → **35 人日** |
| D16 | llama.cpp 后端分发 | **首次运行下载 + 内置 CPU 兜底**（见 §9.6） | `internal/hub` 增加"获取推理后端"能力；M1 增加后端校验 |
| D17 | 「多种格式」界定 | **指接口/协议格式，不是模型文件格式**；模型格式明确只支持 GGUF（见 §9.1 与 §17.4） | 消除原始需求歧义；Anthropic 协议恢复为一等目标（原被静默删除，予以补回） |

> **D15 为什么值得做（而不是可有可无的小功能）**：
> 1. **直接解决"AI 参数太多"这个原始需求的落地难题**——80 个参数对个人用户仍是认知负担，算术推荐把"默认值"从拍脑袋变成有依据的推导
> 2. **顺带补上两个上一轮评测指出的漏洞**：非 N 卡用户（检测不到 NVML → 自动降级 CPU/Vulkan）、显存预估不准（有了实测回填校准，见 §5.7）
> 3. **零依赖、零网络、零模型**：纯 CPUID / WMI / NVML / 注册表读取 + 四则运算，可复现、可解释、可测试
>
> **D16 的必要性**：整个设计的起点是"启动 llama-server 子进程"，但此前从未说明这个可执行文件从哪来。若不解决，M1 一开工就会卡住。

### v1.6 变更（第六轮 · 本次）

| # | 决策项 | 结论 | 影响 |
| --- | --- | --- | --- |
| D17✅ | 「多种格式」 | **确认为接口/协议格式**；模型文件格式**只做 GGUF**。safetensors/ONNX 等**以后再加** | §9.0 界定冻结，不再有歧义；后端抽象层预留扩展点即可 |
| D18 | **LM Studio 引擎** | **新增第三个引擎适配器**。本机已有 LM Studio，直接作为首选引擎接入，模型库合并其模型目录 | 新增 `internal/engine/lmstudio`（≈1.5 天）；M2 6 → 7 天。**收益：第一版做完就能立刻用真实模型跑通，无需先解决后端分发** |
| D19 | 推理后端分发 | **简化为自用方案**：内置一份 CPU 后端 + 复用/探测 LM Studio 自带运行时 + 按需下载 CUDA 变体，**不做多版本管理与自动更新**（自用、非商业分发） | M7 后端管理 4 → 3 天 |
| D20 | 推荐参数的应用方式 | **静默自动应用，不弹窗**；仅在参数页显示小字「已根据你的硬件优化（可点击查看算式依据）」 | 去掉推荐确认弹窗；算式依据改为**可展开**而非强制阅读 |
| D21 | 推进方式 | **直接做完整版 M1–M8**（不再走 M1' 最小版路线） | 撤回 §15.2 的 M1' 建议（保留其风险提示作为备忘） |
| D23⚠️ | **撤回 D18 / D19** | **不再接入任何外部引擎**：删掉 `internal/engine/lmstudio`，不探测 1234、不读 `~/.lmstudio`、不复用它的模型目录与自带运行时。本程序只做**自管 llama.cpp 子进程**（模型库自选根目录、运行时自备/自探测），成为可独立运行的应用 | §5.1 的 LM Studio 一行、§5.8 的「可调度 vs 不可调度」、§9.1 的 LM Studio 行、§6.4 的 `enabled_engines[]`/`lmstudio_*` 配置**全部作废**；`engine.Adapter` 保留为接口，但当前只有一个实现 |

> **D18 为什么是个好消息**（已作废，保留决策记录）：原设计假设"必须先解决 llama-server 从哪来"才能跑通第一条链路——这本来是个冷启动硬门槛。但本机已有 LM Studio 且它自带编译好的运行时，于是：
> - **第一版做完即可用它已有的模型跑通完整链路**（检测 1234 端口 → 列出模型 → 转发请求）
> - llama.cpp 子进程路径可以并行推进，不再是阻塞项
> - LM Studio 适配器本身极轻（纯 HTTP 探测 + 模型列表 + 请求转发），约 1.5 天
>
> **需要注意的耦合点**（已作废，保留决策记录）：LM Studio 不受本程序控制"加载几层到 GPU"，因此**显存调度对 LM Studio 实例只能观测、不能干预**——它的模型由 LM Studio 自己管理。设计中明确标注为「不可调度引擎」（见 §5.8）。
>
> **为什么撤回（D23）**：一旦接入外部引擎，参数面板就必然出现"整组灰置、由对方管理"的死角——
> 恰恰是本项目存在的理由（把加载级参数真正下发出去）被削掉了一半；而且运行期多出一个必须
> 先装好、先启动的外部依赖，与"独立应用"的目标直接冲突。改为自管后：
> - 加载级参数（`n_gpu_layers` / `n_ctx` / `cache_type_*` / `flash_attn` …）100% 变成启动开关，无死角
> - 运行期唯一依赖是一份 `llama-server.exe`（放到 `runtime\llama.cpp\` 即自动发现，或手动指定）
> - 模型库只由用户自己的根目录决定，不再混入别的软件的目录

### v1.7 变更（第七轮 · 本次）

| # | 决策项 | 结论 | 影响 |
| --- | --- | --- | --- |
| D22 | **访问令牌 / API Key** | **默认不启用**。改为「**私网来源白名单 + 公网暴露检测**」这一零摩擦方案；Key 降级为可选的进阶功能 | 删除「令牌」页面与令牌 CRUD/轮换/二维码分享；首次运行向导 **4 步 → 3 步**；`internal/auth` 大幅简化 |

> **D22 的推理（威胁模型纠正）**：原设计犯了"用 Key 防局域网内乱用"的错——但本场景**局域网里只有用户自己的设备**，这个威胁几乎不存在，而 Key 带来的是每台设备都要填一次的**持续摩擦**（换 Key 还要挨个改）。
>
> 真正存在的风险是另一个：**路由器 UPnP 自动映射、手动端口转发、或运营商分配公网 IP** —— 一旦 `8080` 暴露到公网，无鉴权的推理接口会在数小时内被扫描机器人发现（这类扫描是常态，不是假设）。
>
> **正确的措施不是 Key，而是来源过滤**：
> ```
> 非私网来源请求  → 直接 403（在 IP 层挡住，用户无需配置任何东西）
> 启动自检发现本机有公网 IP 且端口可达 → 界面显著告警 + 告警通道推送
> ```
> 效果对比：手机端只填 `base_url` 即可用（零摩擦）✅，公网陌生人扫到也进不来 ✅，且**不依赖用户记得配 Key**（这是原方案最脆弱的地方——用户很可能图省事随便填一个 `123`，或干脆关掉鉴权）。
>
> **保留 Key 的入口**：设置页提供「启用访问令牌」开关（默认关）。当用户将来需要（如公司网络、给朋友临时用）时一键开启，届时令牌管理界面才出现。**默认路径上不出现它。**

---

## 1. 项目定位

一个**个人使用的本地大模型控制台 + 局域网推理网关**：在自己带 GPU 的 Windows 主力机上双击启动，图形化调参、管理模型，把模型以 OpenAI 兼容 API 共享给**自己的**其它设备（手机 / 笔记本 / 平板 / 电视盒子）。

- 图形窗口 + 按钮，零命令行操作
- 集中调节 **模型加载参数 + 采样参数 + 服务参数**（约 80 项）
- **三个引擎可选**：**LM Studio（本机已有，首选）** / llama.cpp（自管子进程） / Ollama，统一成一套参数与 API
- **多模型并存**：显存允许时同时加载多个模型，按请求 `model` 字段自动路由
- **模型获取引导**：内置推荐模型清单（HF / ModelScope / 国内镜像），一键用系统浏览器打开；你手动下载后把文件拖进窗口即可注册
- **多模型根目录**：`D:\Models` + `E:\Models` 统一视图，并与 Ollama 已有模型合并展示
- **硬件自检 + 推荐参数**：启动时自动探测 GPU/显存/CPU/内存，**用算术规则**（非 AI）推导 `n_gpu_layers`、`n_ctx`、`cache_type_k/v` 等推荐值，并给出可核对的算式依据
- **零摩擦接入**：二维码 + 一键复制，手机端**只填 base_url**（无需 API Key，D22），30 秒配好
- **审计 + 告警**：自己用了多少一目了然；不在电脑前也能收到崩溃/显存告警

### 目标 / 非目标

| 是目标 | 非目标 |
| --- | --- |
| 一键加载 GGUF 并对外起服务 | 自己实现推理内核（复用 llama.cpp / Ollama） |
| 多模型热切换 + 显存自动调度 | 训练 / 微调 / 数据标注 |
| **硬件自检 + 算术推荐参数**（D15） | 用 AI 做参数推荐（本设计坚持纯确定性算术） |
| 模型获取引导、多目录扫描、量化标注 | 多模态（图像 / 语音）—— v2 再说 |
| 单机多设备接入（手机/笔记本/平板） | ~~多租户配额体系~~（D5 砍掉） |
| 审计与 Webhook 告警 | ~~管理员/成员角色分级~~（D6 砍掉） |
| 主程序 < 20MB（不含模型） | 公网暴露 / SaaS 托管 |
| — | ~~内置模型下载~~（D10）、~~自签 HTTPS~~（D11）、~~Function Calling 一等公民 / 模型快速对比 / 开机静默自启~~（D14） |

---

## 2. 技术选型

| 层次 | 选型 | 理由 |
| --- | --- | --- |
| 语言 | Go 1.22+ | 单文件交叉编译、并发模型天然适合做网关 |
| GUI | **Wails v2** | 见 §2.1 |
| 前端 | Vue 3 + Vite + Naive UI | 表单控件齐全；虚拟滚动表格撑得住万行审计 |
| 图表 | ECharts（按需引入） | 吞吐 / 显存实时曲线 |
| HTTP 路由 | `chi` | 标准库风格、中间件轻、无反射 |
| 配置 | `viper` + JSON Schema | JSON/YAML/TOML + 环境变量覆盖 + 热重载 |
| 持久化 | `bbolt` | 审计、预设、实例历史、校准样本；**个人规模完全够用，无需 SQL** |
| 日志 | `log/slog` + `lumberjack` | 结构化 + 滚动 |
| 子进程 | `os/exec` + Windows Job Object | 主程序退出连带回收推理进程，杜绝僵尸占显存 |
| 硬件监控 | `nvml`(NVIDIA) + `gopsutil` | 显存占用是调度器的输入，必须精确 |
| 系统集成 | `browser.OpenURL` + Wails `OnFileDrop` | 打开外部链接（D10）、接收用户拖入的模型文件 |
| 二维码 | `skip2/go-qrcode` | 生成接入配置二维码，手机扫码即配（D11 后证书逻辑移除，此能力保留） |
| 通知 | `go-toast`（Windows Toast）+ HTTP | 桌面通知 + 钉钉/企微/自定义 Webhook |
| 打包 | Wails NSIS + Inno Setup | 绿色 zip + 安装器 |

### 2.1 GUI 定稿：为什么是 Wails v2

| 需求 | Wails v2 | Fyne | Walk |
| --- | --- | --- | --- |
| 80 参数的分组/折叠/搜索/滑块联动 | HTML+CSS 组件生态，开发量最低 | 需手写大量布局 | Win32 拼装，美化受限 |
| 实时曲线（吞吐/显存） | ECharts 开箱即用 | 需自绘 | 需自绘 |
| 万行审计表格（虚拟滚动/列筛选/导出） | Web 侧基础设施成熟 | 基本等于自研 | 需自绘虚拟列表 |
| **文件拖拽入库**（D10 的交互基础） | `OnFileDrop` 原生支持 | 支持 | 支持 |
| **本机浏览器管理台 / headless 降级** | ✅ 同构前端 | ❌ | ❌ |
| 二进制体积 | 8~15MB | ~30MB | ~10MB |

**决定性因素**：D6 之后"局域网同事用浏览器访问管理台"这个理由不再成立，真正决定性的变为——

1. **参数面板 + 图表 + 审计表格 + 拖拽入库**这几块在 Web 侧的成本远低于原生（本项目的 UI 复杂度主要就在这），
2. **headless 降级路径**：WebView2 缺失时，以无窗口模式启动并自动用浏览器打开本机管理台（`http://127.0.0.1:8081/admin`）。因为前端同构，这条兜底路径**零额外开发成本**，把 Wails 唯一的风险点消除。

**关键设计 —— Transport 抽象**：

```
BridgeTransport interface {
    Invoke(method string, payload any) (any, error)   // 命令
    Subscribe(topic string, cb func(data any))         // 事件
    Capabilities() []string                            // 能力差异标记
}
├── WailsTransport  # 桌面窗口：Wails Bind / Events（支持托盘、窗口控制）
└── HTTPTransport   # 本机浏览器：fetch / WebSocket /ws/events（无托盘能力）
```

同一份前端代码，两个入口：`index.html`（Wails 窗口）与 `admin.html`（本机 HTTP 托管）。

---

## 3. 总体架构

### 3.1 进程结构

```
┌─────────────────────────────────────────────────────────────────────┐
│                     Wails 应用进程 (llamadock.exe)                    │
│                                                                     │
│  ┌───────────────┐        ┌──────────────────────────────────────┐  │
│  │ 前端 (Vue3)    │◄──────►│  Bridge (WailsTransport / HTTPT.)    │  │
│  │ 桌面窗口 ∥ 本机浏览器│    └───────────────┬──────────────────────┘  │
│  └───────────────┘                        │                         │
│              ┌────────────────────────────▼──────────────────────┐  │
│              │              Core Orchestrator                    │  │
│              │  状态机 · 参数作用域合并 · 事件总线 · 任务调度       │  │
│              └──┬────────┬─────────┬────────┬────────┬───────────┘  │
│                 │        │         │        │        │              │
│      ┌──────────▼──┐ ┌───▼────┐ ┌──▼────┐ ┌─▼─────┐ ┌▼──────────┐  │
│      │ ModelRouter │ │  VRAM  │ │  API  │ │ Audit │ │ ModelHub  │  │
│      │  模型路由    │ │Scheduler│ │Gateway│ │ 审计  │ │ 模型获取  │  │
│      └──────┬──────┘ └───┬────┘ └───┬───┘ └───────┘ └───────────┘  │
│             │            │          │              ┌────────────┐  │
│      ┌──────▼────────────▼───┐  ┌───▼──────────┐   │ Alert/告警 │  │
│      │    Engine Instance     │  │  SrcFilter   │   │ Toast/Webhook│ │
│      │    Pool (实例池)        │  │ 来源过滤(可选) │   └────────────┘  │
│      └──┬──────────────┬─────┘  └──────────────┘                   │
│         │              │                                            │
│    ┌────▼─────┐ ┌─────▼────┐ ┌─────▼────┐  ┌──────────┐ ┌────────┐ │
│    │ llamacpp │ │  ollama  │ │ lmstudio │  │ModelStore│ │NetInfo │ │
│    │  适配器   │ │  适配器   │ │  适配器   │  │库/多根目录│ │二维码   │ │
│    │ ←子进程   │ │ ←HTTP    │ │ ←HTTP    │  └──────────┘ └────────┘ │
│    └────┬─────┘ └────┬─────┘ └────┬─────┘                          │
└─────────┼────────────┼────────────┼─────────────────────────────────┘
          │            │            │
   llama-server    Ollama      LM Studio
   :10001~:10099   (11434)     (1234)
```

### 3.2 双监听器（D6 · v1.2 新增）

**在绑定层面隔离推理口与管理口**，而不是靠判断来源 IP：

```
┌─ PublicServer   0.0.0.0:8080  ─────────────────────────────┐
│  /v1/chat/completions  /v1/completions  /v1/embeddings     │
│  /v1/models            /healthz         /readyz            │
│  ★ 私网来源白名单（非私网来源直接 403，用户无需配置）        │
│  ★ 可选：启用访问令牌（默认关闭，D22）                       │
│  ★ 访问 /api/v1/* 与 /admin → 404（管理面不对外暴露）        │
└────────────────────────────────────────────────────────────┘
                       ▲
                       │ 局域网：手机 / 笔记本 / 平板 / 电视盒子
                       │ （只能推理，不能改配置）
                       │
┌─ AdminServer    127.0.0.1:8081 ────────────────────────────┐
│  /admin            前端管理台（静态资源）                    │
│  /api/v1/*         全部管理 API（配置/实例/模型库/审计/告警）  │
│  /ws/events        事件推送                                 │
│  ★ 免鉴权（物理上只有本机能连）                              │
│  ★ 端口可配置（默认 8081）                                  │
└────────────────────────────────────────────────────────────┘
```

设计收益：
- 局域网设备**在 TCP 层面就连不上**管理口，不存在鉴权绕过风险
- 管理 API 无需再做登录/会话体系（D6 的直接结果，省掉一整套认证开发）
- 桌面窗口走 `WailsTransport` 内存直调，连 8081 都不经过；8081 仅供「浏览器管理台 / headless 模式」使用

### 3.3 分层职责

| 层 | 职责 | 关键约束 |
| --- | --- | --- |
| 表现层 | 桌面窗口 + 本机浏览器管理台 | 只依赖 `Transport` 接口 |
| 编排层 | 参数作用域合并、实例池、显存调度、事件广播 | 单一事实源，状态变更必发事件 |
| 适配层 | 屏蔽 llama.cpp / Ollama 差异 | 统一接口，新增引擎不改上层 |
| 治理层 | 来源过滤（默认）、审计、告警；访问令牌为可选（D22） | 与业务解耦 |

### 3.4 核心接口契约（设计级）

```
EngineAdapter interface {
    Kind()             // "llamacpp" | "ollama" | "lmstudio"
    Capabilities()     // 能力位图: chat/embed/stream/grammar/tools
    Spawn(spec InstanceSpec) (Handle, error)   // llamacpp 拉进程; ollama 校验模型存在
    Kill(h Handle, graceful bool) error
    Health(h Handle) Health
    ApplyRuntime(h Handle, p SamplingParams) error
    Chat(h Handle, req UnifiedRequest) (<-chan Chunk, error)
    Embed(h Handle, req EmbedRequest) (EmbedResponse, error)
    Metrics(h Handle) InstanceMetrics
}

Handle interface {
    ID() string          // hash(model + loadParams)[:12]
    BaseURL() string     // llamacpp: 127.0.0.1:100xx ; ollama: 11434 ; lmstudio: 1234
    Schedulable() bool   // llamacpp=true（自己管进程）；ollama/lmstudio=false（见 §5.8）
    ModelName() string   // 传给下游的模型名
    Port() int
}
```

参数分两类，UI 必须区分标注：
- **Load 级（需重启实例）**：`n_gpu_layers`、`n_ctx`、`cache_type_k/v`、`mmap`、`flash_attn` …
- **Runtime 级（立即生效）**：`temperature`、`top_p`、`max_tokens`、`system_prompt` …

---

## 4. 模块设计

| 模块 | 包路径 | 说明 |
| --- | --- | --- |
| 应用入口 | `cmd/ai-runner` | 单实例锁、DPI 感知、托盘、headless 降级、**文件拖拽接收**（D10）、生命周期 |
| 桥接层 | `internal/bridge` | `WailsTransport` / `HTTPTransport` 双实现 |
| 核心编排 | `internal/core` | 状态机、参数作用域合并、事件总线 |
| **硬件自检** | `internal/hwprobe` | GPU 厂商/型号/显存、CPU 核数与指令集、内存、磁盘剩余；**全本地、无网络、无 AI**（D15） |
| **参数推荐** | `internal/advisor` | 确定性规则引擎：由硬件画像 + GGUF 元数据推导推荐参数，输出**可解释的算式依据**（D15） |
| **实例池** | `internal/pool` | 实例注册表、端口池分配、健康巡检 |
| **模型路由** | `internal/router` | `model` 字段 → 实例解析、别名、按需加载 |
| **显存调度** | `internal/scheduler` | 显存预算、KV 预估、LRU 淘汰、Pin、排队/拒绝 |
| 引擎适配 | `internal/engine/{llamacpp,ollama,lmstudio,mock}` | 统一 `EngineAdapter` 实现；**lmstudio 为 D18 新增，纯 HTTP 探测 + 转发** |
| 进程管家 | `internal/process` | 子进程、stdout 探活、Job Object、指数退避重启 |
| **模型获取** | `internal/hub` | **推荐模型清单（内置静态表）、外链白名单打开、GGUF 魔数校验、拖拽/路径注册**（D10，无 HTTP 下载逻辑） |
| **模型库** | `internal/modelstore` | **多根目录扫描**、GGUF 元数据解析、量化识别、Ollama 合并视图 |
| **来源过滤** | `internal/netguard` | **默认生效**：私网网段白名单判定、非私网来源 403、公网暴露自检（D22） |
| 访问令牌（可选） | `internal/auth` | **默认不加载**；仅在用户开启「启用访问令牌」时生效（D22） |
| **审计** | `internal/audit` | 异步落库、按天分桶、聚合、CSV/JSONL 导出、TTL、**正文开关** |
| **告警** | `internal/alert` | 事件规则引擎、静默期去重、Windows Toast、钉钉/企微/自定义 Webhook |
| 网络信息 | `internal/netinfo` | 网卡 IP 枚举、接入信息组装、二维码生成（D11：自签证书逻辑移除） |
| API 网关 | `internal/server` | **双监听器**、中间件链、静态托管 `/admin`、优雅关闭 |
| 协议转换 | `internal/protocol/{openai,anthropic,ollama,plain}` | 多格式请求/响应互转（v1.5 恢复 anthropic，见 §9.4） |
| 参数注册表 | `internal/params` | **单一事实源**：元数据 + 作用域 + 生效方式 + 引擎适用性 |
| 指标采集 | `internal/metrics` | 环形缓冲：30s 粒度存 2h，1min 粒度存 24h |
| 配置中心 | `internal/config` | 加载/校验/迁移/热更/落盘 |
| 预设仓库 | `internal/store` | 预设 CRUD、导入导出、启动历史 |
| 日志 | `internal/logging` | slog + 多 sink（文件 / UI / API） |

> **亮点设计 —— 参数注册表（`internal/params`）**
> 约 80 个参数在代码中只声明一次：`{key, 中英标签, 类型, 范围, 默认值, 单位, 生效方式, 作用域可覆盖性, 适用引擎, 说明}`。
> UI 表单、`/api/v1/config` 读写、JSON Schema 校验、配置文件默认值填充、审计字段全部由它自动生成。
> 新增参数只改一处，UI / API / 校验 / 文档永不失步。

---

## 5. 硬件自检、实例池与显存调度

### 5.1 实例池模型

```
InstanceSpec = { modelPath, engineKind, loadParams, alias, pinned, priority }
instance_id  = hash(modelPath + loadParams)[:12]
```

- 同模型 + 同加载参数 = 同一实例（复用，不重复占显存）
- 同模型 + 不同加载参数 = 两个实例（如 长上下文版 / 短上下文快版）
- **llama.cpp**：一个子进程 + 一个内网端口（端口池 `10001~10099`，**只绑 `127.0.0.1`，局域网不可见**）
- **Ollama**：不占端口，复用 daemon（11434），`ModelName` 透传
- **LM Studio**（D18）：不占端口，探测 1234 端口，复用其已加载模型；**无需用户先解决后端分发问题**

### 5.2 显存预算与 KV Cache 预估

调度器必须"先算后加载"，否则必然 OOM。估算公式（llama.cpp）：

```
n_layer, n_kv_head, n_embd   ← 从 GGUF 头部读取（无需加载全模型）

权重显存  ≈ 文件大小 × 1.02 × (n_gpu_layers / n_layer)
KV Cache ≈ 2 × n_layer × n_ctx × (n_embd / n_head × n_kv_head)
             × bytes(cache_type) × n_parallel
             bytes: f16=2 | q8_0≈1.06 | q4_0≈0.56
计算缓冲  ≈ n_batch × n_embd × 4 × 2
安全余量   = 512 MB
需求 = 权重 + KV + 缓冲 + 余量
```

UI 实时显示 **已用 / 预占 / 可用** 三条进度条；加载前预告「预计占用 6.2GB，可用 5.1GB，将卸载 `llama-3-8b`（空闲 42 分钟）」。
提供**保守模式**（预估 ×1.15）应对不同模型的实际偏差。

> **已知局限（诚实记录）**：上述公式只对"标准 GQA 稠密模型"准确。**MoE**（Qwen3-MoE / Mixtral）的专家层常驻显存计算不同；**MLA**（DeepSeek 系列）的 KV 被压缩得极小；**滑动窗口注意力**的 KV 不随 `n_ctx` 线性增长——后两者会被**严重高估**（误差可能达数倍）。
>
> **v1.5 的应对**：本公式不再"单打独斗"，而是作为 `internal/advisor`（§5.6）第 ②③④ 步的计算内核，并叠加两道修正：
> 1. 输入不再是估算值，而是 `hwprobe` 实测的**专用显存与当前占用**（§5.5）
> 2. 输出会被**实测回填校准**（§5.7）——同一架构累积样本后用中位数系数修正理论值
>
> 即：**理论公式给起点，实测数据给精度。** 加载失败时仍然自动回滚并释放，不把预估误差变成卡死。

### 5.3 淘汰策略（VRAM Scheduler）

```
请求命中已加载实例 ────────────────────────► 直接转发
请求未命中 ─► 计算需求 ─► 可用显存足够？ ─是─► 加载新实例 ─► 转发
                              │否
                              ▼
             按 [LRU + 空闲时长 + 优先级] 排序候选
             （pinned=true 永不入选；有在途请求的不入选）
                              │
             逐个卸载直到满足 ──► 仍不足？
                              │是
                    按策略: queue(排队) | reject(507 VRAM不足)
```

- `pin` 固定：关键模型钉在显存，永不被淘汰（UI 一键）
- `idle_unload_minutes`：空闲超时自动卸载（默认 30 分钟）
- 突发保护：同一时刻仅 1 个实例在执行加载/卸载（全局锁），防显存踩踏

### 5.4 按需自动加载

请求 `model` 未加载时，若 `allow_auto_load=true`：网关阻塞等待加载完成（仅首字延迟变长，客户端无感）；失败返回 `503 MODEL_LOAD_FAILED`。
关闭则严格只服务已加载模型，返回 `404 MODEL_NOT_LOADED` + 可用模型列表。**个人使用建议开启**（手机端不用关心模型有没有加载）。

---

### 5.5 硬件自检（D15 · 全本地 · 无 AI）

启动时执行一次（此后可手动重扫），结果缓存进配置并驱动后续所有推荐。

| 采集项 | Windows 数据源 | 用途 |
| --- | --- | --- |
| GPU 厂商 / 型号 / 驱动版本 | WMI `Win32_VideoController` + 注册表 `HardwareInformation.qwMemorySize` | 决定推理后端 |
| **独显专用显存**（关键） | **NVIDIA: NVML**（`nvml.dll`，精确且区分专用/共享）<br>**AMD/Intel: 注册表 `qwMemorySize`**（WMI 的 `AdapterRAM` 是 32 位、>4GB 会溢出，**必须绕开**） | 显存预算基数 |
| 共享显存 / 当前占用 | NVML（NVIDIA）/ `Win32_VideoController` DRIVER 部分（其余厂商） | 扣除项 |
| 后端可用性 | 探测 CUDA / Vulkan / 无 | 选择 llama.cpp 编译分支 |
| CPU 逻辑核 / 物理核 | WMI `Win32_Processor` | `n_threads` |
| **CPU 指令集**（AVX2 / AVX512 / AMX） | 自研 CPUID 调用（`golang.org/x/sys`），或读 `Win32_Processor.InstructionSet` | 判断是否值得用 CPU 推理、估算 CPU 速度档位 |
| 内存总量 / 可用 | `GlobalMemoryStatusEx` | 决定 `mmap` 策略与 CPU 可行性 |
| 各磁盘剩余空间 | `GetDiskFreeSpaceEx` | 模型目录选择提示、`disk_low` 告警 |

**厂商分支 → 后端选择（纯 if-else，无需 AI）**

```
if 检测到 NVIDIA 独显 and 显存 ≥ 2GB      → 后端 CUDA        → 显存调度【精确可用】
else if 检测到 AMD/Intel 独显 and Vulkan  → 后端 Vulkan      → 显存调度【读总量，粗略】
else if 检测到核显 且 内存 ≥ 8GB          → 后端 Vulkan/CPU  → 显存调度【降级为档位估】
else                                      → 后端 CPU only    → 无显存调度，仅按内存约束
```

> **这一分支直接解决了上一轮评测指出的"非 N 卡用户显存调度整体失灵"问题**：读不到 NVML 时，调度器自动切换为**档位保守模式**（按"小/中/大"三档估算）+ **加载失败即回滚释放**，而不是硬套精确公式。

### 5.6 参数推荐规则引擎（D15 · 算术 · 非 AI）

**四条设计原则**

| 原则 | 含义 |
| --- | --- |
| **确定性** | 同一台机器 + 同一个模型 → 永远得到同一组推荐值。可复现、可单测、可回归 |
| **可解释** | 每条推荐都附**算式与代入数值**，用户可以自己核验（"为什么是 31 层"能被看见） |
| **可覆盖** | 推荐值只作为**预填默认**，用户随时手改；不隐藏、不锁定 |
| **保守优先** | 宁可少卸载几层到 GPU（慢一点），也不要 OOM（崩掉）——失败成本不对称 |

**推荐链路（全部四则运算）**

```
输入：HardwareProfile(H) + GGUFMeta(M) + 用途标签(P)

① 后端与可用显存
   backend     = H.backend                       // 见 §5.5 分支
   vram_total  = H.gpu.vram_dedicated
   reserve     = 512MB                           // 桌面/系统占用 + 安全余量
   vram_budget = vram_total - reserve - H.gpu.vram_used

② cache_type_k/v（先定它，因为 KV 大小依赖它）
   若 vram_budget ≥ 模型大小 × 2.0 → f16   （质量优先）
   若 vram_budget ≥ 模型大小 × 1.5 → q8_0  （甜点）
   否则                            → q4_0  （省显存优先）

③ n_ctx（KV 预算反推，而非直接取模型上限）
   kv_per_token = 2 × n_layer × (n_embd / n_head × n_head_kv) × bytes(cache_type)
   kv_budget    = vram_budget − 模型大小 × 1.02          // 权重占用
   ctx_max      = floor(kv_budget / kv_per_token)
   n_ctx        = clamp_down_pow2(min(M.native_ctx, ctx_max), 512)
   // 显存紧张时【降 cache_type】而不是【砍 n_ctx】——上下文长度对可用性影响更大

④ n_gpu_layers（核心算术）
   per_layer    = 模型大小 / n_layer
   layers_max   = floor((vram_budget − KV(n_ctx)) / per_layer)
   n_gpu_layers = min(n_layer, layers_max × 0.95)         // 5% 安全收敛
   若 backend == CPU → n_gpu_layers = 0

⑤ 批处理与线程
   n_threads       = min(physical_cores, 16)              // 超过 16 收益递减
   n_threads_batch = n_threads
   n_batch         = GPU ? 512 : (RAM ≥ 16GB ? 256 : 128)
   n_ubatch        = n_batch

⑥ 并发槽
   n_parallel = clamp(floor(kv_budget / (kv_per_token × n_ctx)), 1, 4)

⑦ 加速开关
   flash_attn = (backend ∈ {CUDA, Vulkan}) 且 cache_type 受支持
   mmap       = true                                      // Windows 上始终开启
   mlock      = false                                     // 桌面系统上收益小、风险大

⑧ 采样参数（与硬件无关，按模型用途标签 P 给预设）
   代码/数学类  → temperature 0.2 / top_p 0.9 / repeat_penalty 1.05
   通用对话     → 0.7 / 0.95 / 1.1
   创意写作     → 1.1 / 0.98 / 1.05
   （即 §6.5 的四套预设，由模型名/标签自动匹配）
```

**输出示例（UI 实际呈现形式）**

```
推荐配置 · 依据可核对
  n_gpu_layers  31 / 32     模型 4.7GB ÷ 32 层 = 147MB/层
                            可用显存 4.6GB − KV 0.8GB = 3.8GB
                            ⌊3.8GB ÷ 147MB⌋ = 26 层 … 应用 5% 安全收敛后 31 层
                            依据：RTX 4060 8GB，当前已占用 2.1GB

  n_ctx         8192        模型原生上限 32768，KV 预算只够 8192
                            kv/token = 2×32×(4096/32×8)×1.06 ≈ 69KB
                            0.9GB ÷ 69KB ≈ 13,000 → 向下取 2 的幂 = 8192

  cache_type    q8_0        可用显存 / 模型大小 = 1.6 倍 → 落在 1.5~2.0 区间

  n_threads     16          物理核 16，超过 16 收益递减

  [一键应用]  [仅应用采样参数]  [我自己调]
```

**推荐值标注「实测校准中」的情形**：模型架构属于 MoE / MLA / 滑动窗口注意力时（由 GGUF 头部 `architecture` 字段识别），对应项标注"该架构 KV 计算方式特殊，预估值仅供参考"，并直接使用保守系数。

### 5.7 闭环校准（D15 · 让公式自己变准）

§5.2 的公式对 MoE / MLA 天然不准。解法不是把公式写得更复杂，而是**用实测数据回填修正**：

```
加载时：预测值 P = advisor.predict(model, hardware)
加载后：3 秒后读实测值 A = nvml.used() - 加载前基线
记录：  ratio = A / P，按 architecture 分组存入 bbolt
下次：  P' = P × median(该架构历史 ratio)          // 中位数抗噪，避免单次异常
展示：  预估界面标注"基于 N 次实测样本校准，偏差 ±X%"
```

- 首次加载（无样本）→ 用理论公式 + 保守系数
- 累积 3 次以上 → 用中位数校准
- 单次异常值不污染（用中位数而非均值）

**这条闭环是"用算术解决"的关键**：不引入任何模型，但让公式随本机真实数据自我修正。

### 5.8 可调度引擎 vs 不可调度引擎（D18 · **已随 D23 作废**）

> ⚠️ 本节描述的是"同时接入 LM Studio / Ollama 等外部引擎"时代的设计。D23 撤回外部引擎后，
> 只剩自管 llama.cpp 一种（全部可调度），本节的区分不再需要。保留作为将来的备忘。

引入 LM Studio / Ollama 后，实例池里出现了两类性质不同的实例，**必须显式区分**，否则调度器会做出错误决策：

| 类型 | 引擎 | 加载/卸载能力 | 显存调度 |
| --- | --- | --- | --- |
| **可调度** | llama.cpp | ✅ 完全可控（自己拉子进程、指定 `n_gpu_layers`） | 参与 LRU 淘汰、Pin、显存预算计算 |
| **不可调度** | LM Studio / Ollama | ❌ 模型由外部程序管理，本程序**只能观测** | **只统计、不淘汰、不计入预算**；也不被淘汰 |

**具体规则**

```
instance.Schedulable() == false 时：
  ├── 显存调度器：不将其显存纳入"可回收预算"，也不把它选作淘汰候选
  ├── 预算视图：单独一行展示"外部引擎占用 x.xGB（不受本程序控制）"
  ├── 加载模型：不触发择优淘汰，直接转发到外部引擎，由其自行处理
  ├── 参数作用域：只透传【采样参数】与【Prompt 参数】
  │                Load 级参数（n_gpu_layers / n_ctx / cache_type_*）不展示为可编辑，
  │                而是提示「该参数由 LM Studio / Ollama 管理，请在对应软件中调整」
  └── 能力位图：Capabilities() 标注哪些参数真正生效，UI 自动灰置其余项
```

> **这是本设计必须主动暴露的"能力边界"**：如果让 LM Studio 实例的参数面板照常显示 `n_gpu_layers` 却点了没反应，用户会认为是 bug。**宁可显式灰置 + 给出跳转提示，也不要静默无效。**

**混合场景示例**：同时跑 1 个 llama.cpp 实例（自己调度）+ LM Studio 的 2 个模型（外部管理）时，显存预算条显示三段：`本程序已用 9.4GB` / `外部引擎 2.8GB` / `可回收 1.2GB`。

---

## 6. 参数体系

### 6.1 三级参数作用域（v1.2 简化）

```
请求级 (HTTP body)  >  模型级 (实例绑定预设)  >  全局默认 (config)
```

> v1.1 的 "Key 级" 随 D5 一并砍掉——个人使用不存在"限制某个团队"的需求。

| 作用域 | 用途 | 可覆盖范围 |
| --- | --- | --- |
| 请求级 | 客户端临时调参 | **仅采样参数白名单** |
| 模型级 | 每个模型的最佳采样配置 | 采样参数 |
| 全局默认 | 兜底 | 全部含 Load 参数 |

> **安全设计**：`n_ctx`、`n_gpu_layers`、`cache_type_k/v` 等 **Load 级参数任何请求都无法覆盖**，否则局域网内一个恶意请求就能撑爆显存。
> UI 上每个参数行显示「来源」标签（全局 / 模型覆盖），悬浮展示完整取值链。

### 6.2 模型加载参数（Load · 需重启实例）

| 分组 | 参数 | 说明 |
| --- | --- | --- |
| 模型 | `model_path` / `lora[]` / `lora_scale` | 主模型、LoRA 挂载 |
| GPU | `n_gpu_layers` / `split_mode` / `tensor_split[]` / `main_gpu` | 卸载层数、多卡切分 |
| 上下文 | `n_ctx` / `n_batch` / `n_ubatch` / `rope_freq_base` / `rope_freq_scale` | 上下文长度、批大小、RoPE 扩展 |
| 缓存 | `cache_type_k` / `cache_type_v` / `cache_reuse` | KV 量化（f16/q8_0/q4_0），省显存利器 |
| CPU | `n_threads` / `n_threads_batch` / `numa` | 线程数 |
| 内存 | `mmap` / `mlock` / `no_kv_offload` | 映射、锁页 |
| 加速 | `flash_attn` / `no_perf` | FlashAttention |
| 模式 | `embedding` / `pooling_type` | 嵌入模式、池化 |

### 6.3 采样参数（Runtime · 立即生效）

| 分组 | 参数 |
| --- | --- |
| 温度类 | `temperature`、`dynatemp_range`、`dynatemp_exponent` |
| 截断类 | `top_k`、`top_p`、`min_p`、`typical_p`、`tfs_z`、`top_n_sigma` |
| 惩罚类 | `repeat_penalty`、`repeat_last_n`、`frequency_penalty`、`presence_penalty`、`dry_multiplier`、`dry_base`、`dry_allowed_length`、`xtc_probability`、`xtc_threshold` |
| Mirostat | `mirostat`(0/1/2)、`mirostat_tau`、`mirostat_eta` |
| 长度 | `max_tokens`、`n_predict`、`min_keep` |
| 控制 | `seed`、`stop[]`、`ignore_eos`、`stream`、`logprobs`、`top_logprobs`、`logit_bias` |
| 结构化 | `grammar`(GBNF)、`json_schema`、`response_format` |

> 引擎能力差异由 `Capabilities()` 位图处理：Ollama 不支持 `grammar`/`dry_*` 时 UI 自动灰置并提示，网关收到此类参数时降级丢弃 + 审计记 warning。

### 6.4 Prompt 与服务参数

- **Prompt**：`system_prompt`、`prompt_template`、`chat_template`（自动/手动/Jinja）、`stop_words`、`prefix_suffix`(FIM)、`context_shift` 策略
- **服务**：`public_host`、`public_port`(8080)、`admin_port`(8081)、`cors_origins`、`n_parallel`、`cont_batching`、`max_concurrent_requests`、`request_timeout`、`queue_policy`（D11：`enable_https` 已移除）
- **来源过滤（D22）**：`allow_lan_only`(**默认 true**)、`allowed_networks[]`(默认 `127.0.0.0/8`、`10.0.0.0/8`、`172.16.0.0/12`、`192.168.0.0/16`、`fc00::/7`)、`enable_auth`(**默认 false**)、`warn_on_public_exposure`(默认 true)
- **调度**：`allow_auto_load`、`idle_unload_minutes`、`vram_reserve_mb`、`max_loaded_instances`、`vram_warn_threshold`、`vram_estimate_conservative`
- **运行**：`process_priority`、`auto_restart`、`health_check_interval`、`log_level`、`start_with_os`、`minimize_to_tray`
- **模型库**：`models_roots[]`（多根目录，**`[0]` 由首次运行向导选择**，D12）、`include_ollama_models`（合并视图开关）
- **首次运行**：`first_run_done`（向导完成标记，D12）
- **硬件与推荐（D15）**：`hw_profile`（缓存的自检结果）、`hw_rescan_on_start`、`advisor_enabled`、`advisor_conservative`（×1.15 保守系数）、`advisor_calibrate`（启用 §5.7 实测校准）、`advisor_auto_apply`（**默认 true**，静默应用，D20）、`vram_reserve_mb`(默认 512)
- **引擎（D18）**：`enabled_engines[]`(default `[lmstudio, llamacpp, ollama]`)、`lmstudio_endpoint`(默认 `http://127.0.0.1:1234`)、`lmstudio_autodetect`、`ollama_endpoint`(默认 `http://127.0.0.1:11434`)
- **推理后端（D19）**：`backend_variant`(cpu/cuda/vulkan)、`backend_path`、`backend_reuse_lmstudio`（复用 LM Studio 自带运行时，默认 true）
- **审计**：`audit_enabled`、`audit_retain_days`(默认 30)、`audit_body_enabled`(默认 **false**)、`audit_body_prefix_len`(默认 200)
- **告警**：`alert_enabled`、`alert_channels[]`、`alert_silence_minutes`(默认 5)、`alert_events[]`

### 6.5 预设系统

| 预设 | temperature | top_p | repeat_penalty | 场景 |
| --- | --- | --- | --- | --- |
| 精确 | 0.2 | 0.9 | 1.05 | 代码、数学、抽取 |
| 平衡 | 0.7 | 0.95 | 1.1 | 通用对话（默认） |
| 创意 | 1.1 | 0.98 | 1.05 | 文案、头脑风暴 |
| 长文 | 0.6 | 0.9 | 1.15 | 长文档摘要 / RAG |

支持：内置预设 + 自定义 + JSON 导入导出 + 参数差异高亮对比 + **一键绑定到模型实例**。

### 6.6 「常用参数」的明确定义（v1.5 补漏）

此前设计只说"默认只展开常用"，但未定义哪些是常用——会导致 M4 无法开工。现明确为 15 项，**其余全部折叠在「高级参数」里**：

| # | 参数 | 分组 | 为什么常用 |
| --- | --- | --- | --- |
| 1 | `temperature` | 采样 | 调风格的第一旋钮 |
| 2 | `top_p` | 采样 | 与 temperature 搭配 |
| 3 | `top_k` | 采样 | 抑制长尾 |
| 4 | `min_p` | 采样 | 比 top_p 更稳的截断 |
| 5 | `repeat_penalty` | 采样 | 治复读 |
| 6 | `max_tokens` | 长度 | 控制输出长度 |
| 7 | `seed` | 控制 | 复现结果 |
| 8 | `stop` | 控制 | 截断标记 |
| 9 | `stream` | 控制 | 是否流式 |
| 10 | `system_prompt` | Prompt | 最常改的一项 |
| 11 | `n_ctx` | 加载 | 上下文长度（决定能否塞长文档） |
| 12 | `n_gpu_layers` | 加载 | 显存与速度的核心权衡 |
| 13 | `cache_type_k` | 缓存 | 省显存最有效的手段 |
| 14 | `cache_type_v` | 缓存 | 同 13 |
| 15 | `flash_attn` | 加速 | 一个开关，速度差异明显 |

> 其余约 65 项（`dynatemp_*` / `dry_*` / `xtc_*` / `mirostat_*` / `tfs_z` / `logit_bias` / `numa` / `mlock` / `split_mode` / `tensor_split` / `rope_*` / `pooling_type` …）归入**高级参数**，默认折叠，可用搜索框直接定位。

---

## 7. 告警模块设计（D8 · v1.2 新增）

个人场景的核心痛点：**人不在电脑前，模型崩了 / 显存爆了不知道**。

### 7.1 告警事件

| 事件 | 级别 | 默认开启 |
| --- | --- | --- |
| `instance_crashed` 实例子进程崩溃 | 严重 | ✅ |
| `instance_restart_failed` 自动重启失败（重试耗尽） | 严重 | ✅ |
| `load_failed` 模型加载失败 | 警告 | ✅ |
| `vram_insufficient` 显存不足（淘汰后仍不够 / 拒绝请求） | 警告 | ✅ |
| `server_down` 监听端口起不来 | 严重 | ✅ |
| `disk_low` 模型盘可用空间 < 10GB | 警告 | ✅ |
| `quota_soft` 单设备请求异常高频（唯一保留的防刷） | 提示 | ❌ |
| `instance_unloaded` 空闲自动卸载 | 提示 | ❌ |

### 7.2 通知通道

```
AlertChannel interface {
    Kind()      // "toast" | "dingtalk" | "wecom" | "feishu" | "webhook"
    Send(a Alert) error
    Test() error   // UI「发送测试」按钮
}
├── WindowsToast   # 桌面通知，本机即时可感知（个人场景最高频）
├── DingTalk       # 钉钉机器人（markdown 卡片，支持 @手机号）
├── WeCom          # 企业微信机器人（markdown）
├── Feishu         # 飞书机器人（可选）
└── CustomWebhook  # 任意 HTTP POST，支持 {{.Title}} {{.Level}} {{.Detail}} 模板
```

### 7.3 去重与可靠性

- **静默期**：同一 `event + 目标` 在 `alert_silence_minutes`（默认 5 分钟）内只推一次，避免崩溃循环刷屏
- **重试**：失败指数退避重试 3 次（1s / 3s / 9s），仍失败则降级为 Windows Toast 兜底
- **告警历史**：bbolt 保留最近 200 条，UI 可查、可重发
- **测试按钮**：每个通道配置页都有「发送测试消息」，配置即时验证

---

## 8. API 接口设计

### 8.1 推理面（PublicServer · `0.0.0.0:8080`）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/chat/completions` | 对话（stream、tools、vision 透传） |
| POST | `/v1/completions` | 文本补全 |
| POST | `/v1/embeddings` | 向量化（需 embedding 模式实例） |
| GET | `/v1/models` | 模型库 + 已加载实例的并集 |
| GET | `/healthz` `/readyz` | 探针（免鉴权） |

`model` 字段解析顺序：**实例 ID → 别名 → 模型库名 → 自动加载**。
手机/笔记本端 base_url 填 `http://<局域网IP>:8080/v1`，**API Key 留空或随便填**（默认不校验），任何 OpenAI 客户端零改代码。

> **D22**：推理面**默认不要求鉴权**，改为「私网来源白名单」——非私网来源直接 403。客户端侧无需任何配置。

### 8.2 管理面（AdminServer · `127.0.0.1:8081`）

**实例与调度**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/instances` | 运行中实例：模型、端口、显存、t/s、请求数、Pin |
| POST | `/api/v1/instances` | 加载新实例 `{model, loadParams, alias, pinned}` |
| DELETE | `/api/v1/instances/{id}` | 卸载 |
| PUT | `/api/v1/instances/{id}/pin` | 固定 / 取消固定 |
| GET | `/api/v1/vram` | 显存预算视图（已用/预占/可用/各实例明细） |
| GET | `/api/v1/hardware` | **硬件自检结果**（GPU/显存/CPU/内存/磁盘/后端可用性）（D15） |
| POST | `/api/v1/hardware/rescan` | 重新自检（换显卡、加内存后） |
| POST | `/api/v1/advisor/recommend` | `{model, purpose}` → **推荐参数 + 逐条算式依据**（D15） |
| PUT | `/api/v1/advisor/calibration` | 查看/清空实测校准样本（§5.7） |
| GET | `/api/v1/scheduler/preview?model=x` | **加载前预估**：需要多少显存、要淘汰谁 |

**模型库与模型获取（D10）**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/library` | 本地模型列表（**按根目录分组** + Ollama 来源标记 + 量化 + 显存预估） |
| GET/PUT | `/api/v1/library/roots` | 模型根目录增删改（D9） |
| POST | `/api/v1/library/scan` | 重扫全部根目录 + Ollama |
| POST | `/api/v1/library/import` | **手动注册**：`{path, targetRoot}`，注册前校验 GGUF 魔数 |
| DELETE | `/api/v1/library/{id}` | 删除文件（需确认 token，使用中的模型禁止删除） |
| GET | `/api/v1/hub/recommended` | **推荐模型清单**（内置静态表 + 按本机可用显存自动分组） |
| POST | `/api/v1/hub/open` | `{url}` 用**系统默认浏览器**打开（仅白名单域名，见 §13） |
| POST | `/api/v1/hub/inspect` | 校验某个文件是否为可加载的 GGUF，并返回量化/体积/建议 |

**来源过滤（D22 · 默认生效）**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET/PUT | `/api/v1/netguard` | 查看/设置私网白名单、`allow_lan_only` 开关 |
| GET | `/api/v1/netguard/exposure` | **公网暴露自检**：本机是否有公网 IP、端口是否可达、最近是否出现非私网来源请求 |
| GET/PUT | `/api/v1/auth` | **可选**：启用/关闭访问令牌（默认关闭） |
| GET/POST/PUT/DELETE | `/api/v1/tokens[/{id}]` | 仅当访问令牌已启用时可用（D22：默认路径不出现） |

**审计**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/audit?from=&to=&token=&model=&status=&page=` | 分页查询 |
| GET | `/api/v1/audit/stats?group_by=model\|token\|hour` | 聚合统计 |
| GET | `/api/v1/audit/export?format=csv\|jsonl` | 导出（流式） |
| POST | `/api/v1/audit/body/purge` | **一键清空历史正文**（D7） |

**告警**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET/PUT | `/api/v1/alerts/config` | 通道与规则配置 |
| POST | `/api/v1/alerts/test` | 发送测试消息 |
| GET | `/api/v1/alerts/history` | 最近 200 条告警 |
| POST | `/api/v1/alerts/{id}/resend` | 重发 |

**系统**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/status` `/api/v1/health` | 状态 / 健康报告 |
| GET/PUT | `/api/v1/config` | 配置读写（PUT 支持局部 patch） |
| GET | `/api/v1/params/schema` | 参数元数据（UI 与外部共用） |
| POST | `/api/v1/engine/restart` | 重启全部实例（保留配置） |
| GET | `/api/v1/logs?level=&tail=` | 增量日志 |
| GET | `/api/v1/network` | 网卡 IP 列表、监听状态、**接入配置二维码 payload** |
| GET | `/api/v1/setup/state` | 首次运行向导状态（D12） |
| POST | `/api/v1/setup/models-root` | 设置首个模型根目录（D12） |
| GET | `/ws/events` | WebSocket：状态/指标/日志/告警推送 |
| GET | `/admin` | 浏览器管理台（静态资源） |

### 8.3 中间件链

**PublicServer**（面向局域网，严格）：
```
RequestID → Recover → AccessLog → **SourceFilter(私网白名单, 默认开)** → CORS → BodyLimit(32MB) → Auth(可选, 默认关)
   → 管理面路径拦截(404) → ContentType 协商 → ModelRouter → Handler
   → Audit(异步落库) → 指标上报
```

**AdminServer**（仅本机，轻量）：
```
RequestID → Recover → AccessLog → BodyLimit(64MB, 走本机文件操作) → Handler
```

- 管理口**免鉴权**——因为它物理上只绑 `127.0.0.1`，局域网 TCP 都连不上（D6 的红利）
- 审计走异步 channel + 批量写入，**绝不阻塞推理响应**

### 8.4 错误码

复用 OpenAI 错误体 `{"error":{"message","type","code","param"}}`：
`MODEL_NOT_FOUND(404)`、`MODEL_NOT_LOADED(503)`、`MODEL_LOAD_FAILED(503)`、
`VRAM_INSUFFICIENT(507)`、`RATE_LIMITED(429)`、`SOURCE_DENIED(403)`（非私网来源，D22）、`TOKEN_DISABLED(403)`（仅令牌启用时）、
`PARAM_INVALID(400)`、`PARAM_NOT_OVERRIDABLE(403)`、`QUEUE_FULL(503)`。

---

## 9. 模型格式与模型获取

### 9.0 「多种格式」的界定（D17 · v1.5 明确）

原始需求中的"支持多种格式"存在歧义，**现明确界定**：

| 维度 | 是否覆盖 | 说明 |
| --- | --- | --- |
| **接口 / 序列化格式** | ✅ **这是本次实现的重点** | 6 种请求格式 + 4 种响应格式（见 §9.2 / §9.3） |
| **协议风格** | ✅ | OpenAI / Ollama / **Anthropic Messages** / 纯文本，四种（见 §9.4） |
| **模型文件格式** | ⚠️ **仅 GGUF** | 受 D2（llama.cpp + Ollama）约束，safetensors / ONNX / GPTQ / AWQ 明确不做 |

> **v1.6 结论（D17 已确认）**：用户明确"模型文件格式**以后再加**"。因此本设计**按接口/协议格式维度交付即视为满足需求**，模型格式扩展（safetensors / ONNX / GPTQ / AWQ）列入 §17.2 的 v1.1+ 候选。
>
> **为此保留的扩展点**：`EngineAdapter` 已按"屏蔽引擎差异"设计，新增 vLLM / transformers 后端时**不需要改动上层**（ModelRouter、参数注册表、网关、UI 均不受影响）。这是当初选这个抽象的直接收益。

### 9.1 模型文件格式

| 格式 | 支持方式 | 优先级 |
| --- | --- | --- |
| **GGUF** | llama.cpp 原生 + GGUF 头部解析（量化/显存预估/推荐引擎的输入） | P0 |
| GGML（旧） | llama.cpp，含 legacy 提示 | P2 |
| PEFT / LoRA | `--lora` 挂载，UI 多选 | P1 |
| **Ollama 本地模型** | 通过 Ollama API 列出，**合并进模型库视图**（D9） | P1 |
| ~~LM Studio 本地模型~~ | ~~探测 1234 端口 + 扫描其模型目录（`~/.lmstudio/models`）~~ **D23 撤回：不探测、不扫描** | ❌ 不做 |
| safetensors / ONNX / GPTQ / AWQ | ❌ v1.0 不做（见 §9.0 的界定说明） | — |

### 9.2 请求格式（Content-Type 协商）

`application/json`（默认）、`text/event-stream` / `application/x-ndjson`（流式）、`multipart/form-data`（文件+参数）、`application/x-www-form-urlencoded`、`application/yaml`。

### 9.3 响应格式

| 参数 | 效果 |
| --- | --- |
| `stream=false` | 单 JSON |
| `stream=true` | SSE `data: {...}\n\n`，末帧 `data: [DONE]` |
| `Accept: application/x-ndjson` | 每行一个 JSON |
| `?format=text` | 纯文本流（浏览器直接看） |

### 9.4 协议风格兼容

OpenAI / Ollama 原生 / **Anthropic Messages** / 纯文本 `/generate` → 统一转 `UnifiedRequest` → 交给 EngineAdapter。**新增协议不动引擎代码**。

> **v1.5 补记**：Anthropic Messages 协议在 v1.2 修订时被**静默删除**（未记录在任何决策编号中），属于流程瑕疵。现予以补回，并在 §4 的 `internal/protocol/` 中恢复 `anthropic` 子包。

### 9.5 模型获取引导（D10 · v1.3 新增）

替代内置下载器。核心不是"少个功能"，而是**降低"我该下哪个文件"的决策成本**——这才是用户在 HuggingFace 上真正卡住的地方（几十个分片、多个量化、看不清该选谁）。

**推荐模型清单**（内置静态表 `assets/recommended_models.json`）：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| `name` | Qwen2.5-7B-Instruct | 展示名 |
| `params` | 7B | 参数量 |
| `quant` | Q4_K_M | **明确推荐量化** |
| `size` | 4.7 GB | 文件体积 |
| `vram_hint` | ~6 GB | 建议显存 |
| `tags` | 中文 / 对话 / 通用 | 用途标签 |
| `links` | `{hf, hf_mirror, ms}` | 仓库页链接 |
| `note` | 7B 的甜点量化，速度与质量平衡 | 一句话建议 |

UI 按「建议显存 ≤ 本机可用显存」自动分为 **「你的机器能跑」/「勉强能跑」/「显存不足」** 三组，直接消除选型焦虑。

**交互流程**

```
打开「模型获取」页
 → 看到按本机显存分好组的推荐清单
 → 点 [用浏览器打开]（系统默认浏览器，登录 HF 等由浏览器负责）
 → 自己在浏览器下载 .gguf
 → 回到应用：把文件拖进窗口（Wails OnFileDrop）或点 [手动选择文件]
 → hub 校验 GGUF 魔数 + 版本 → 复制/移动到目标根目录（同盘则 rename，秒完成）
 → modelstore 扫描入库 → 出现在模型库，可直接点加载
```

**设计要点**

- **不做 HTTP 下载**：`internal/hub` 只有「清单、外链、校验、注册」四件事，无队列、无状态机、无重试
- **外链安全**：只接受 `huggingface.co` / `hf-mirror.com` / `modelscope.cn` / `ollama.com` 白名单域名 + `https`
- **拖拽优先**：拖进来是最短路径，比"添加目录再扫描"少两步
- **入库前校验，而非加载时才发现**：注册时读 GGUF 头部，不是 GGUF 就明确告知（"这是 safetensors 分片，本项目只支持 GGUF"），而不是等加载时抛一句看不懂的错
- **顺带给建议**：检测到未量化（F16）的大文件时提示"建议改用 Q4_K_M，显存占用可减少约 70%"

### 9.6 推理后端的分发（D16 · v1.5 补齐的关键空白）

**此前设计的盲点**：整个方案以"启动 llama-server 子进程"为前提，但从头到尾没有说明这个可执行文件如何到达用户机器。不解决它，M1 一开工就卡住。

**v1.6 修订（D18 + D19）**：因为本机已有 LM Studio，**后端分发不再是冷启动阻塞项**——LM Studio 适配器让第一版做完就能用真实模型跑通。llama.cpp 后端路径因此可以走最简方案。

**优先级 1：复用本机已有的运行时**

```
启动自检
 ├── 探测 LM Studio（默认 127.0.0.1:1234）
 │     ├── 在线 → 直接作为可用引擎（零下载、零配置），并记录其模型目录
 │     └── 扫描其运行时目录（~/.lmstudio/extensions/backends/...）寻找 llama-server
 │           └── 找到则复用为 llama.cpp 路径的后端（backend_reuse_lmstudio=true）
 └── 探测 Ollama（127.0.0.1:11434）→ 在线则纳入可用引擎
```

**优先级 2：内置 CPU 后端兜底**

```
安装包内置 llama-server-cpu.exe（约 15MB）
 ├── 作用：三个外部引擎都不可用时的最后保障，能跑小模型验证链路
 └── 不依赖网络、不依赖任何外部软件
```

**优先级 3：按需下载 GPU 变体（可选，非阻塞）**

```
hwprobe 判定为 NVIDIA 且当前只有 CPU 后端
 → 设置页提示「检测到 NVIDIA 显卡，可下载 CUDA 加速后端（约 300MB）以获得 GPU 推理」
 → 用户确认后下载，校验 SHA256，试启动 --version 验证
 → 失败回退 CPU，明确告知原因
```

**v1.6 明确删除的复杂度**（自用、非商业，不值得做）：

| 原设计 | 现在 |
| --- | --- |
| 多版本并存 + 回滚 | ❌ 只保留一份，覆盖更新 |
| 自动更新检查 | ❌ 删除 |
| CUDA 12.x / 13.x 双版本适配 | ❌ 只按自检结果取一个 |
| 完整的后端版本矩阵管理 | ❌ 简化为单文件 + 版本号记录 |

**保留的必要机制**

| 项 | 做法 |
| --- | --- |
| 兼容校验 | 比对后端版本与参数注册表的 `min_engine_version`，不满足则明确报错而非静默失败 |
| 能力发现 | 首次加载时解析 `llama-server --help`，得到该后端**真实支持**的参数集（这是 §16「参数随版本变动」风险的落地手段，必须保留） |
| 复用探测 | 若使用 LM Studio 的运行时，参数集以**实测 `--help` 输出为准**，不假设与官方 release 一致 |

---

## 10. UI 设计

窗口 1280×800（最小 960×640），深色/浅色主题，高 DPI 适配。

```
┌──────────────────────────────────────────────────────────────────────────┐
│ ● 就绪 │ 实例 3/5 │ 显存 9.4/12GB │ :8080 对外 │ [启动全部][停止全部][重启] │  ← 工具栏
├───────────┬──────────────────────────────────────────────────────────────┤
│ ▸ 仪表盘   │  吞吐 78.2 t/s   今日请求 12,047   设备 3   运行 06:14:22     │
│ ▸ 模型库   │  ┌──────── ECharts 实时曲线（t/s · 显存 · 队列） ────────┐    │
│ ▸ 获取     │  └───────────────────────────────────────────────────────┘    │
│ ▸ 实例     │  显存预算条  ███████████████░░░░░  已用 9.4 / 预占 1.2 / 12GB   │
│ ▸ 参数     │                                                             │
│ ▸ 服务     │  ┌── 设备接入（手机/笔记本） ──────────────────────────┐      │
│ ▸ 审计     │  │  [接入二维码]  http://192.168.1.100:8080/v1        │      │
│ ▸ 告警     │  │  无需填写 API Key   [复制全部配置]                  │      │
│ ▸ 预设     │  └───────────────────────────────────────────────────┘      │
│ ▸ 日志     │                                                             │
│ ▸ 设置     │                                                             │
├───────────┴──────────────────────────────────────────────────────────────┤
│ 端口 8080 · 仅私网可访问 · llama.cpp×2 + LM Studio×1 · 告警正常            │  ← 状态条
└──────────────────────────────────────────────────────────────────────────┘
```

### 页面要点

| 页面 | 关键控件 |
| --- | --- |
| **仪表盘** | 状态卡、实时曲线、显存预算条、快捷测试输入框、**硬件信息卡（GPU / 专用显存 / CPU 核数 / 内存 / 可用后端，带「重新自检」）**（D15）、**设备接入卡片（二维码 + 一键复制 base_url/Key/SDK 示例）** |
| **硬件与推荐**（可并入仪表盘右侧栏） | 自检明细、后端状态与版本、当前模型的**推荐参数 + 算式依据**、校准样本数（§5.7）、「清空校准」 |
| **模型库** | **按来源分区**：`本程序目录` / `LM Studio` / `Ollama` **三个分区**（D18/D9）、量化徽标、参数量/大小排序、搜索、**加载前显存预估悬浮**、一键加载/删除/绑定预设；外部来源的模型标注「由 LM Studio 管理」并禁止删除；顶部「管理根目录」入口 |
| **模型获取** | 推荐清单（模型名/参数量/推荐量化/体积/建议显存/用途标签），按本机显存自动分组；**[用浏览器打开]**（HF / ModelScope / hf-mirror）、[复制下载命令]、**拖拽文件到窗口注册**、[手动选择文件]、目标根目录选择；入库前校验 GGUF 并给出量化建议 |
| **实例** | 运行中实例卡片：模型、**引擎徽标（llama.cpp / LM Studio / Ollama）**、端口、显存占用、t/s、请求数、Pin 图钉、卸载、绑定预设、显存明细展开；**外部引擎实例标注「外部管理」并隐藏 Pin/卸载按钮**（§5.8），显示「请在 LM Studio 中操作」跳转提示 |
| **参数** | 左分组树 + 右表单；滑块与数值联动；每参数 `i` 悬浮说明（中英/默认值/生效方式）；「来源」标签；🔒 锁图标标记请求级不可覆盖；搜索过滤；每组"恢复默认"；底部「仅保存 / 保存并重启实例」；**默认只展开「常用 15 项」**，其余折叠（§6.6）。**推荐值自动应用（D20）**：顶部一行小字「已根据你的硬件优化 · 查看算式依据 ▾」，点开即 §5.6 的算式面板（默认收起，不弹窗）。**针对不可调度引擎（§5.8）**：Load 级参数整组灰置并提示「该参数由 LM Studio / Ollama 管理」 |
| **服务** | 绑定地址与端口、格式勾选、CORS、`allow_auto_load`、并发与队列策略、**试运行面板**、**网络信息 + 接入二维码**；顶部一句醒目提示「仅建议在可信局域网使用，当前为明文 HTTP」（D11） |
| **访问控制**（默认隐藏，D22） | 默认只显示一行：「当前仅允许私网访问 · 无需 API Key」+ [公网暴露自检] 结果。**只有用户点「启用访问令牌」后，才展开令牌管理 UI**（备注、启用/禁用、生成/复制/轮换） |
| **审计** | 表格（虚拟滚动）：时间/设备令牌/模型/端点/输入输出 token/TTFT/总耗时/状态；筛选栏；聚合图；导出 CSV/JSONL；**正文记录开关 + 一键清空正文**（D7） |
| **告警** | 通道配置卡片（Toast/钉钉/企微/自定义，各带「发送测试」）、事件类型勾选、静默期设置、告警历史列表（可重发） |
| **预设** | 预设卡片、一键应用、另存为、导入导出、参数差异对比、绑定到实例 |
| **日志** | 等级过滤、关键字搜索、自动滚动、导出、清空 |
| **设置** | 主题、开机自启、托盘行为、日志级别、空闲卸载、进程优先级、审计保留天数、**重新运行首次运行向导**（D12）、**硬件重新自检**、**引擎管理**（启用/禁用 LM Studio / llama.cpp / Ollama，探测端点，连通性测试，D18）、**推理后端**（当前类型与路径、是否复用 LM Studio 运行时、下载 GPU 变体入口，D19）、**推荐参数**（自动应用开关、保守系数、清空校准样本，D20/§5.7）、重置配置 |

### 交互规则

0. **首次运行向导（D12 + D15 + D22）**：第一次启动时展示 **3 步**引导——① **硬件自检 + 引擎探测**（自动探测 GPU/显存/CPU/内存 + 发现本机 LM Studio / Ollama，展示"你的机器能跑什么"）→ ② 选择模型根目录（默认建议非系统盘）→ ③ 展示接入卡片（局域网 IP + 二维码 + base_url，**提示无需填 Key**）。全程可跳过，跳过则设置页保留"重新运行向导"入口。
1. 引擎/实例状态变化 → 状态灯（灰=未启动 / 绿=就绪 / 黄=加载中 / 橙=淘汰中 / 红=错误），事件推送不轮询
2. 改 **Load 级参数** → 出现「需重启实例」黄标，按钮变「保存并重启实例」
3. 加载模型前**必弹显存预估**：「预计占用 6.2GB，可用 5.1GB，将卸载 `llama-3-8b`（空闲 42 分钟），继续？」→ 用户知情
4. 请求级不可覆盖的参数打锁图标 🔒
5. 危险操作（停止服务、删除模型、重置配置、启用公网访问）二次确认；删除模型需输入名称
6. 托盘：最小化到托盘、右键启动/停止/打开面板/打开浏览器管理台/退出、悬停显示"3 实例 · 78 t/s · 9.4GB"

---

## 11. 配置与目录结构

### 11.1 配置分层（后覆盖前）

```
内置默认值 → config.json → 预设 → 环境变量(AIRUNNER_*) → 命令行 flag → API PUT
```

配置文件 `%APPDATA%/AIRunner/config.json`，含 `version` 字段用于迁移。
模型根目录 `config.models_roots[]` 支持多路径（如 `D:\Models` + `E:\Models`）。**`models_roots[0]` 不设硬编码默认值，由首次运行向导让用户选择**（D12）；用户直接关掉向导时，回退到 `%APPDATA%/AIRunner/models` 并保持"未初始化"提示。

### 11.2 工程目录

```
e:/AI_GO/
├── cmd/ai-runner/main.go
├── internal/
│   ├── bridge/       # WailsTransport / HTTPTransport 双实现
│   ├── core/         # 编排、状态机、事件总线
│   ├── params/       # ★ 参数注册表（单一事实源）
│   ├── pool/         # 引擎实例池、端口分配
│   ├── router/       # 模型路由、别名、按需加载
│   ├── scheduler/    # 显存预算、KV预估、LRU 淘汰、Pin
│   ├── engine/       # llamacpp/ ollama/ lmstudio/ mock/
│   ├── process/      # 子进程守护、Job Object
│   ├── hwprobe/      # 硬件自检：WMI/注册表/NVML/CPUID（D15）
│   ├── advisor/      # 参数推荐规则引擎 + 实测校准（D15，纯算术）
│   ├── hub/          # 推荐模型清单、外链白名单打开、GGUF 校验、拖拽/路径注册、后端分发（D16）
│   ├── modelstore/   # 多根目录扫描、GGUF 解析、Ollama 合并视图
│   ├── netguard/     # 私网来源白名单 + 公网暴露自检（D22，默认生效）
│   ├── auth/         # 访问令牌（可选，默认不加载，D22）
│   ├── audit/        # 落库、聚合、导出、TTL、正文开关
│   ├── alert/        # 告警规则、去重、Toast/Webhook 通道
│   ├── netinfo/      # 网卡 IP 枚举、接入信息组装、二维码生成
│   ├── setup/        # 首次运行向导状态与初始化（D12）
│   ├── server/       # 双监听器 + 中间件 + /admin 静态托管
│   ├── protocol/     # openai/ ollama/ plain/ 协议转换
│   ├── config/  metrics/  store/  logging/  platform/
├── frontend/         # Vue3（构建产物同时供 Wails 与 /admin）
│   └── src/views/{SetupWizard,Dashboard,Library,Hub,Instances,Params,Service,Access,Audit,Alerts,Presets,Logs,Settings}
├── assets/           # recommended_models.json（D13）、backends.json（D16）、icons/、schema/
├── wails.json  go.mod  README.md
```

---

## 12. 关键流程

**⓪ 首次运行向导（D12 + D15）**
`首次启动（first_run_done=false）→ ① hwprobe 自检（NVML/WMI/注册表/CPUID，约 200ms）+ **探测本机已有引擎（LM Studio 1234 / Ollama 11434）** → 展示硬件画像与「已发现可用引擎」→ ② 目录选择器选模型根目录（校验可写、剩余空间、建议非系统盘）→ ③ 展示接入卡片（局域网 IP + 二维码 + base_url，**注明无需填写 API Key**）→ 写入 config，first_run_done=true`

> **D18 的效果**：若探测到 LM Studio 在线，第 ① 步直接提示「发现 LM Studio（3 个模型可用），已作为默认引擎」——**用户不需要下载任何后端就能立刻开始用**。

**⑩ 硬件自检与参数推荐（D15）**
`hwprobe.Probe() → HardwareProfile{GPU厂商/专用显存/已用/后端可用性/物理核/指令集/内存/磁盘} → advisor.Recommend(profile, GGUFMeta, 用途标签) → 按 §5.6 的 ①~⑧ 步纯算术推导 → 输出推荐值 + 每条的算式依据 → UI 展示，用户点 [一键应用] 写入模型级预填值（不覆盖用户已手改的项）`

**⑪ 实测校准回填（D15）**
`加载实例 → 记录加载前显存基线 → 3 秒后读 NVML 实测占用 A → ratio = A / 预测值P → 按 GGUF architecture 分组写入 bbolt → 下次推荐时 P' = P × median(历史 ratio)；单次异常不污染（中位数抗噪）`

**① 启动实例**
`点击加载 → scheduler.Preview() 算显存 → 不足则列出待淘汰 → 用户确认 → 卸载 victim → process 启动 llama-server（分配端口）→ 解析 stdout 探活（"server is listening"）→ 注册实例 → 事件广播 → 状态灯变绿`

**② 手机接入（个人场景最高频 · D22 后零摩擦）**
`手机连同一 WiFi → 桌面窗口「设备接入」卡片显示 http://192.168.1.100:8080/v1 → 扫码或复制 → 在手机 AI 客户端**只填 base_url**（API Key 留空即可）→ 直接可聊（模型未加载时自动按需加载）`

**③ 多模型路由**
`POST /v1/chat/completions {model:"qwen2.5-7b"} → 中间件链（SourceFilter 私网放行 → 鉴权默认跳过）→ router 解析（实例ID→别名→库名）→ 命中？ → 是则转发；否则 allow_auto_load 则触发① → Handle.BaseURL → protocol 转换 → 转发 SSE → 逐 token 回流统计 → 写审计（含来源 IP 便于识别是哪个设备）`

**⑫ 公网暴露自检（D22）**
`启动时（及每 30 分钟）→ netinfo 枚举本机 IP → 判断是否存在公网 IP 直连 → 尝试判断端口可达性 → 若疑似暴露：① UI 顶部显著横幅告警 ② 触发 alert 通道推送 ③ 服务页高亮「你的服务可能已暴露到公网」并给出关闭路由器端口映射的指引；同时记录所有非私网来源请求（默认已 403）`

**④ 参数作用域合并**
`请求体 → 校验是否属于可覆盖白名单（否则 403）→ 模型级预设补齐 → 全局默认兜底 → 最终 UnifiedRequest`

**⑤ 显存紧张时淘汰**
`新请求需加载 → 预算不足 → 候选 = 未 Pin 且无在途请求的实例，按 (last_used_at 升序) 排序 → 逐个卸载（等待进程退出）→ 每轮重新预算 → 满足则加载，仍不足按 queue_policy 排队或 507 → 触发 vram_insufficient 告警`

**⑥ 模型获取与注册（D10）**
`打开「模型获取」页 → 看按本机显存分好组的推荐清单 → 点 [用浏览器打开] 跳到 HF/ModelScope 仓库页 → 在浏览器下载 .gguf → 回到应用拖入文件（OnFileDrop）或填路径 → hub 校验 GGUF 魔数（不合格给出明确原因）→ 复制/移动到目标根目录（同盘 rename 秒完成）→ modelstore 扫描入库 → 出现在模型库可直接加载`

**⑦ 告警触发**
`事件总线订阅（崩溃/加载失败/显存不足/磁盘不足…）→ alert 规则匹配 → 静默期去重（同事件 5 分钟内只推一次）→ 逐通道发送（Toast 立即；Webhook 失败退避重试 3 次）→ 全部失败降级为 Windows Toast → 写入告警历史`

**⑧ 子进程崩溃自恢复**
`心跳 5s → 连续 3 次失败 → 标记 Unhealthy → 触发 instance_crashed 告警 → auto_restart 则指数退避重启（1s/2s/4s，最多 5 次）→ 恢复后重新注册实例；耗尽则触发 instance_restart_failed`

**⑨ 优雅退出**
`停止接收新请求 → 等待在途（最多 10s）→ 排空审计与告警队列 → 逐个 graceful stop 子进程 → 超时 kill → 释放端口`（Job Object 保证异常退出也不留僵尸）

---

## 13. 安全设计

| 项 | 设计 |
| --- | --- |
| **双监听器隔离** | 推理口 `0.0.0.0:8080`，管理口 `127.0.0.1:8081`。局域网设备 TCP 层连不上管理口（D6） |
| **来源过滤（主防线，D22）** | **默认生效**：只放行私网网段（`127/8`、`10/8`、`172.16/12`、`192.168/16`、`fc00::/7`），非私网来源一律 `403 SOURCE_DENIED`。用户零配置 |
| **公网暴露自检（D22）** | 启动与每 30 分钟检测本机是否直连公网 + 端口可达；疑似暴露则显著告警 + 告警通道推送 + 给出关闭端口映射的指引 |
| 访问令牌（可选） | 默认**关闭**。启用后：`ConstantTimeCompare` 比较、仅存 SHA256 哈希、支持禁用/轮换。面向"将来需要"（公司网络、临时给朋友用） |
| 参数防滥用 | Load 级参数不可被请求覆盖，杜绝"一个请求撑爆显存" |
| 传输（**已知取舍**） | **仅 HTTP 明文**（D11）。缓解：**私网来源白名单**（D22，比原来的"强制令牌"更直接地挡住了外部风险）+ 启动与每 30 分钟公网暴露自检 + 服务页显著标注。**将来若要在公司网络 / 合租 / 公共 WiFi 使用，必须补回 HTTPS**——这是有意识接受的技术债，不是遗漏 |
| 告警防护 | 别让贴错 Webhook 地址把日志推到公网——地址保存前做连通性测试 |
| 路径 | 模型路径限定在 `models_roots[]` 白名单内，防路径穿越 |
| 外链打开 | `/api/v1/hub/open` 只接受 `huggingface.co` / `hf-mirror.com` / `modelscope.cn` / `ollama.com` 白名单域名且必须 `https`，禁止 `file://` 与任意 URL（防被诱导打开本地程序）（D10） |
| 资源 | 请求体 32MB 上限、JSON 深度限制、端口池上限 |
| 进程 | 子进程内网端口仅绑 `127.0.0.1`，Job Object 回收，单实例锁防重复监听 |
| 审计 | 默认只存元数据；正文开关默认关闭，日志与审计中 `Authorization`/`api_key` 自动打码 |
| 删除防护 | 删除模型需输入名称；**使用中的模型禁止删除**；重置配置需确认 |
| 提示 | 首次绑定 `0.0.0.0` 时提示「确保路由器未将该端口映射到公网」，并在检测到来自非私网网段的请求时记录警告 |

---

## 14. 性能与资源目标

| 指标 | 目标 | 口径说明 |
| --- | --- | --- |
| **llamadock.exe 主进程内存**（空闲） | < 150 MB | **仅统计主进程，不含 WebView2 子进程**（v1.5 明确口径：Wails 应用下 WebView2 自身通常另占 100~200MB） |
| WebView2 额外占用（空闲） | 100~200 MB | 单独计量、单独展示，避免与主进程混算导致指标失真 |
| 二进制体积 | < 20 MB | 不含模型、WebView2、**下载的 CUDA/Vulkan 后端**（见 §9.6） |
| 冷启动到窗口可见 | < 1.5 s | — |
| **硬件自检耗时** | < 300 ms | 首次运行；结果缓存后重扫仅 < 50 ms（D15） |
| **参数推荐耗时** | < 10 ms | 纯算术，无 IO（D15） |
| 网关额外延迟 | < 3 ms（首字节） | — |
| 流式转发 | ≥ 2000 tokens/s 透传不丢帧 | — |
| 审计写入 | 异步批量，P99 不影响响应延迟 | — |
| 指标采集开销 | < 1% CPU | 高频采样（1s）仅在实例页可见时启用 |

---

## 15. 里程碑规划（v1.6）

> **执行原则（D21）**：直接按 M1–M8 推进完整版；**M2 结束时设一个"最小闭环"验收检查点**（见 §15.2）。

### 15.1 阶段分解

| 阶段 | 内容 | 预估 |
| --- | --- | --- |
| **M1 骨架** | Wails 窗口 + 托盘 + 单实例 + Bridge 双 Transport + headless 降级 + 配置中心 + 日志 + **首次运行向导（含硬件自检）**（D12/D15）+ **内置 CPU 后端校验**（D16） | 5 天 |
| **M2 引擎与实例池** | llama.cpp 子进程守护、Ollama 适配器、**LM Studio 适配器（D18）**、端口池、实例注册与探活、**硬件自检 hwprobe**（D15） | 7 天 |
| **M3 显存调度 + 推荐** | KV/显存预估、GGUF 头部解析、LRU 淘汰、Pin、按需加载、端口/显存视图、**advisor 推荐规则引擎 + 实测校准**（D15）、**可调度/不可调度引擎区分**（§5.8） | 6 天 |
| **M4 参数面板** | 参数注册表（含作用域）、参数 UI（常用 15 项 + 高级折叠，§6.6）、预设、热更新、来源标注、**推荐值静默应用 + 算式可展开**（D20） | 5 天 |
| **M5 API 网关** | 双监听器、OpenAI 兼容层、**Anthropic 协议**（D17 补回）、ModelRouter、多格式协商、SSE/NDJSON | 4 天 |
| **M6 来源过滤 + 审计** | **私网白名单过滤 + 公网暴露自检**（D22）、审计落库与导出、正文开关与清空、网卡枚举 + 接入二维码（D11：不含证书；D22：不含令牌管理） | 2 天 |
| **M7 模型库与获取** | 多根目录、**三来源分区**（本程序 / LM Studio / Ollama）、推荐清单与外链、拖拽/路径注册、GGUF 校验、量化标注、**后端分层复用**（D18/D19） | 3 天 |
| **M8 告警 + 发布** | 告警规则引擎、Toast、钉钉/企微/自定义 Webhook、测试与历史、浏览器管理台、打包 | 5 天 |
| 合计 | v1.0 可交付 | **约 37 人日（单人 7.5 周 / 双人 4 周）** |
| | *D22 净影响* | *删除令牌管理/轮换/二维码分享约 −1 天，新增来源过滤与公网自检约 +0.5 天，向导少一步 −0.5 天 → 实际约 **36 人日*** |

### 15.2 执行方式（D21 确认：直接做完整版）

**按 D21 决定，直接按 M1–M8 推进完整版**，不再走"先做最小版"的路线。

**但建议保留一个检查点**：**M2 结束时应当已经能跑通最小闭环**——

```
M2 结束时的验收标准（不额外花工期，只是检查）
├── 窗口能开、托盘能用
├── 探测到本机 LM Studio → 列出它的模型 → 选中一个
├── 对手机端 API 发一次 chat/completions → 拿到回复
└── 硬件自检结果正确显示
```

若 M2 结束时这条链路不通，说明地基有问题，**此时调整比做到 M8 再调整便宜得多**。这是 D21 下唯一保留的风险控制点。

> 工时演变：v1.1 为 36 人日 → v1.2 砍多租户省 2 天、加多根目录与告警 +2 天（持平 36）→ v1.3 移除下载器省 4 天（32）→ v1.4 移除 HTTPS 省 1 天、加首次运行向导 +1 天（持平 32）→ v1.5 加硬件自检/推荐/后端分发/Anthropic 协议 +5 天（37）→ **v1.6 加 LM Studio 适配器 +1 天、简化后端管理 −1 天（持平 37 人日）**。
>
> **注意**：这是**乐观估算**。按经验，这类"轮子全自造"的 Go 项目实际投入常在估算的 ×1.5 左右。好在 D18 让第一版就能跑通，所以即使超期，你也能更早看到可用的东西。

---

## 16. 风险与对策

| 风险 | 影响 | 对策 |
| --- | --- | --- |
| WebView2 缺失 | 窗口白屏 | 检测 + 引导安装；**headless + 本机浏览器管理台**兜底（同构前端，零成本） |
| **非 N 卡用户（AMD / Intel）读不到 NVML** | 显存调度整体失灵 | **D15 已解决**：hwprobe 按厂商分支，AMD/Intel 走注册表 `qwMemorySize`；无法精确监控时自动切换"档位保守模式"（小/中/大）+ 加载失败即回滚 |
| **WMI `AdapterRAM` 是 32 位、>4GB 会溢出** | 显存读成 4GB，推荐参数全错 | 明确绕开 `AdapterRAM`，改用注册表 `HardwareInformation.qwMemorySize`；并在自检结果页标注数据来源 |
| **推理后端不可用**（D16/D19） | 用户装完不能用 | **D18 已大幅缓解**：优先复用本机 LM Studio 运行时；其次内置 CPU 后端；GPU 变体为可选下载，失败回退 CPU 并明确告知 |
| **外部引擎（LM Studio/Ollama）参数不生效** | 用户当成 bug | §5.8 显式区分可调度/不可调度；Load 级参数整组灰置 + 「该参数由 LM Studio 管理」提示，**绝不静默无效** |
| **1234 / 11434 端口被占用或指向别处** | 探测到假引擎、请求发错 | 探测时校验响应特征（LM Studio 会返回特定 `models` 结构），而非只看端口通不通；结果在设置页可手动改端点并做连通性测试 |
| **LM Studio 升级后运行时路径变化**（D19 复用策略） | 复用失效 | 每次启动重新探测，不缓存绝对路径；失效则自动回退内置 CPU 后端并提示 |
| **CUDA 大版本不匹配**（驱动太旧） | 加载报错看不懂 | 自检时读驱动版本，与实际所需比对，给出"需升级驱动到 x.x"的可读提示，而非透传底层错误 |
| 推荐参数与实测偏差大 | 用户不信任推荐 | §5.7 实测回填校准；推荐值始终标注"参考值 + 基于 N 次样本"；**推荐永不锁定，用户可随时手改** |
| 显存预估不准导致 OOM | 加载失败/卡死 | 预估加 512MB 余量；提供「保守模式」（×1.15）；加载失败自动回滚并释放 |
| 多实例并发踩踏显存 | 随机崩溃 | 全局加载/卸载互斥锁，同一时刻仅 1 个实例变更 |
| llama.cpp 参数随版本变动 | 命令拼接失效 | 参数注册表标注 `min_engine_version`；探活时解析 `--help` 做能力发现 |
| Ollama 与 llama.cpp 能力不对等 | 参数静默失效 | `Capabilities()` 位图控制 UI 灰置；网关降级丢弃并记审计 warning |
| Ollama 模型无法读取元数据 | 显存预估不准 | Ollama 走 `/api/show` 取参数；拿不到时该模型禁用"合并视图下的预估"并标注 |
| 多根目录出现重复模型 | 列表混乱 | 按 `(文件名 + 文件大小 + SHA256前16)` 跨根去重，冲突时标注两个路径 |
| 子进程残留占显存 | 用户投诉 | Job Object + 启动前扫描同名进程 + 「释放全部显存」按钮 |
| 告警刷屏 / 通道失效 | 用户关掉告警 | 静默期去重 + 每通道「发送测试」按钮 + 全通道失败降级 Toast |
| **路由器把端口映射到公网**（D22 的主要风险） | 被陌生人白嫖 GPU | ① **私网来源白名单默认生效**（非私网直接 403，从源头挡住）② 启动与每 30 分钟公网暴露自检 + 显著告警 ③ 记录非私网来源请求便于排查 |
| 用户在内网使用 VPN / 代理导致来源 IP 非私网 | 被误判 403，客户端连不上 | 白名单可自定义追加网段；`SOURCE_DENIED` 时在日志与 UI 明确说明"来源 x.x.x.x 不在白名单"，并给出一键加白名单入口 |
| 用户从浏览器下错文件（下成 safetensors 分片 / 未量化） | 加载失败、体验差 | 推荐清单明确标注「必须选 `.gguf` 后缀」与推荐量化；入库前校验 GGUF 魔数，不合格给出可读原因而非静默失败 |
| 推荐清单里的链接失效 / 模型下架 | 用户点开是 404 | 清单随版本更新；外链打开失败时提示「该链接可能已失效，可在 HF 搜索同名模型」，不阻塞流程 |
| **局域网明文 HTTP 被同网设备抓包**（D11 的已知代价） | 对话内容泄露 | 定位为"可信家庭网络"；启动与服务页明确提示；**一旦使用场景变化（公司/合租/公共 WiFi），必须补回 HTTPS**。（D22 后局域网内已无令牌可泄露，风险面进一步收窄） |
| 内置推荐清单随模型迭代过时 | 推荐的模型已不是最优选 | 清单文件与代码分离（`assets/recommended_models.json`），可单独替换；版本号写在清单里，启动时提示"清单已 N 个月未更新" |
| 首次运行向导被用户跳过 | 出现"没有模型目录"的半残状态 | 跳过时回退默认目录并保持"未初始化"提示；设置页常驻「重新运行向导」入口 |

---

## 17. 范围冻结与 v1.1 候选

### 17.1 已确认的全部决策（D1–D21）

| 维度 | 最终结论 |
| --- | --- |
| 定位 | 个人使用的本地大模型控制台 + 局域网推理网关 |
| GUI | Wails v2（同构前端：桌面窗口 + 本机浏览器管理台） |
| **引擎（D18）** | **LM Studio（首选，本机已有）+ llama.cpp（自管子进程）+ Ollama**；三者统一成一套参数与 API |
| **可调度性（D18/§5.8）** | llama.cpp 完全可控 → 参与显存调度；LM Studio / Ollama 外部管理 → **只观测、不淘汰、不被淘汰** |
| **推理后端分发（D16/D19）** | 分层：① 复用 LM Studio 运行时 ② 内置 CPU 后端兜底 ③ 可选下载 GPU 变体；**不做多版本管理与自动更新** |
| 多模型 | 实例池 + 显存调度 + 按需自动加载 |
| **硬件自检（D15）** | 启动时自动探测 GPU/专用显存/CPU 指令集/内存/磁盘；NVML 优先，非 N 卡走注册表并降级为档位模式 |
| **参数推荐（D15/D20）** | 约 80 项参数的**确定性算术推导** + 逐条算式依据 + 实测回填校准；**不使用任何 AI / 机器学习**；**静默自动应用，不弹窗**，算式依据可展开查阅 |
| 参数作用域 | 三级（请求 > 模型 > 全局），Load 级不可被请求覆盖；常用 15 项默认展开（§6.6） |
| API | 双监听器：`0.0.0.0:8080` 推理（**私网来源白名单，默认免 Key**）／`127.0.0.1:8081` 管理（免鉴权） |
| **访问控制（D22）** | **默认不启用 API Key**。措施为「私网来源白名单 + 公网暴露自检」；Key 为可选的进阶开关，不在默认路径上出现 |
| 传输 | **仅 HTTP**（明文，已知取舍，D11） |
| **格式界定（D17）** | 「多种格式」= **接口/协议格式**（6 请求 + 4 响应 + 4 协议风格）；模型文件格式**只支持 GGUF**，扩展**以后再加** |
| 模型获取 | 内置静态推荐清单 + 浏览器外链 + 拖拽/路径注册（无内置下载器，D10） |
| 模型目录 | 首次运行向导让用户选择；**三来源分区**（本程序 / LM Studio / Ollama）（D12/D18） |
| 审计 | 仅元数据 + 正文开关（默认关） |
| 告警 | 事件规则 + 静默期去重 + Toast/钉钉/企微/自定义 Webhook |
| **推进方式（D21）** | **直接做完整版 M1–M8**；M2 结束时设"最小闭环"验收检查点 |
| 排除项 | 多租户配额、管理员角色、**默认 API Key（D22）**、多模态、HTTPS、模型下载器、Function Calling 一等公民、模型快速对比、开机静默自启、safetensors/ONNX/GPTQ/AWQ（均列为 v1.1+ 候选） |

### 17.2 v1.1 候选（按预期价值排序）

| 优先级 | 候选功能 | 触发条件 |
| --- | --- | --- |
| P0 | **HTTPS（自签 CA）** | 一旦需要离开家庭可信网络使用 |
| P0 | **Function Calling / 结构化输出完善** | 开始用它做 Agent / 业务集成时 |
| P1 | 开机静默到托盘 + 自动加载上次模型 | 想当"家庭 AI 服务器"常驻时 |
| P1 | 模型快速对比 | 需要在多个模型间做选型时 |
| P2 | 多模态（mmproj 视觉 / whisper 语音） | 有图片或语音输入需求时 |
| P2 | 模型下载器 | 觉得"浏览器下载 + 拖回来"仍嫌麻烦时 |
| P2 | **模型文件格式扩展**（safetensors / ONNX） | D17 确认"以后再加"——需要时走 vLLM / transformers 后端 |
| P3 | 中英双语界面 | 有非中文使用者时 |

### 17.3 开工结论：设计到此为止，进入编码

**设计已连续六轮迭代**（36 → 37 人日，完整走了"加功能 → 砍功能 → 补地基 → 加智能 → 接现实"的循环）。

**D21 已确认：直接做完整版 M1–M8，不再分阶段试探。** 至此所有阻塞项都已关闭：

| 曾经的阻塞项 | 关闭方式 |
| --- | --- |
| llama-server 从哪来？ | D18 用本机 LM Studio 绕过冷启动，D19 分层复用 |
| 非 N 卡怎么办？ | D15 厂商分支 + 档位降级（§5.5） |
| 显存公式不准怎么办？ | D15 实测回填校准（§5.7） |
| 80 个参数太杂怎么办？ | §6.6 定义常用 15 项 + D15 算术推荐 + D20 静默应用 |
| 首次打开该干什么？ | D12 四步向导 |
| 「多种格式」到底指什么？ | D17 界定完成 |

**唯一保留的风险控制点**（不额外花工期）：M2 结束时跑一次「最小闭环」验收——探测到 LM Studio → 列出模型 → 手机端发一次请求拿到回复。**若这条链路不通，此时调整远比做到 M8 再调整便宜。**

> 保留下方的原风险评估作为备忘——它指出的两个假设（是否需要多模型并存、推荐参数准不准）**仍未被验证**，只是按 D21 决定，改为在完整版推进过程中用 M2/M3 的真实运行数据来回答，而不是先做最小版。
>
> | 待验证假设 | 验证时机 | 若被推翻 |
> | --- | --- | --- |
> | 真的需要同时加载多个模型吗？ | M3 完成后 | 砍掉实例池与 LRU，M3 的一部分工作可简化 |
> | 推荐参数准不准？ | M2–M3 之间 | 加大 §5.7 校准权重，或简化为三档预设 |
> | 常用 15 项够用吗？ | M4 完成后 | 调整 §6.6 清单，或按使用频次自动排序 |

### 17.4 原始需求对照（v1.6 收口）

| 原始需求 | 满足情况 | 对应设计 |
| --- | --- | --- |
| 轻量级 | ✅ 体积达标；代码量属中型 | 主进程 < 150MB、二进制 < 20MB（§14） |
| Go 语言开发 | ✅ | §2 技术选型 |
| 要有窗口和按钮 | ✅ | Wails v2 + 13 个视图 + 系统托盘（§2.1 / §10） |
| 可以调节很多 AI 参数 | ✅✅✅ | 约 80 项 + 三级作用域 + **硬件自检 + 算术推荐 + 静默应用**（§6 / §5.5–5.7） |
| 可对外提供 API 接口 | ✅ | 双监听器 + OpenAI/Anthropic/Ollama 兼容（§8） |
| 支持多种格式 | ✅ **按 D17 界定为满足** | 接口/协议格式：6 请求 + 4 响应 + 4 协议风格；模型文件格式**仅 GGUF**（"以后再加"，§9.0） |
| *（额外）* 自己用、非商业 | ✅ | D19 简化后端管理、D18 复用本机 LM Studio、移除多版本管理与自动更新 |

> **对照结论**：六项原始需求全部按双方确认的口径达成一致，**无遗留歧义**。唯一需要留意的是模型文件格式的边界（D17 已明确"以后再加"），已记录在 §9.0 与 §17.2 候选清单中。
