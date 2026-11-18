package params

// 本文件是参数注册表的**数据部分**：全部参数在这里声明一次。
//
// 约定：
//   Runtime=true       立即生效（采样类），false 需重新加载实例（Load/Cache/Accel 级）
//   Overridable=true   客户端请求体可覆盖（仅采样白名单）
//   Common=true        「常用 15 项」，UI 默认展开
//   PassThrough=false  不写进 chat 请求体（服务类参数）
//
// 加载级参数（Scope=load/cache/accel）由 engine/llamacpp.BuildArgs 拼成 llama-server 的启动开关，
// 采样/长度/控制级参数由 params.EngineBody 拼进 chat 请求体——两条路互不重叠。

func f(v float64) *float64 { return &v }

// commonOrder 「常用 15 项」在界面上的展示顺序（对齐设计方案 §6.6）。
var commonOrder = []string{
	"temperature", "top_p", "top_k", "min_p", "repeat_penalty",
	"max_tokens", "seed", "stop", "stream",
	"system_prompt",
	"n_ctx", "n_gpu_layers",
	"cache_type_k", "cache_type_v", "flash_attn",
}

func init() {
	regSample()
	regLoad()
	regReasoning()
	regPrompt()
	regService()
}

func regSample() {
	reg(Param{Key: "temperature", Label: "温度 temperature", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 0.7, Min: f(0), Max: f(2), Step: f(0.01), Runtime: true, Overridable: true, Common: true, PassThrough: true,
		Desc: "越低越确定、越高越发散。代码/数学 0.2，通用对话 0.7，创意写作 1.1", Advice: "调风格的第一旋钮"})
	reg(Param{Key: "dynatemp_range", Label: "动态温度范围 dynatemp_range", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 0.0, Min: f(-1), Max: f(5), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: ">0 时温度在 [T-range/2, T+range/2] 内动态变化，缓解复读与死板"})
	reg(Param{Key: "dynatemp_exponent", Label: "动态温度指数", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 1.0, Min: f(0), Max: f(5), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "控制动态温度随熵变化的曲线形状"})
	reg(Param{Key: "top_k", Label: "top_k", Group: "采样", Scope: ScopeSample, Kind: KindInt,
		Default: 40, Min: f(-1), Max: f(1000), Step: f(1), Runtime: true, Overridable: true, Common: true, PassThrough: true,
		Desc: "只从概率最高的 k 个词中采样；-1 表示不限制", Advice: "抑制长尾乱词"})
	reg(Param{Key: "top_p", Label: "top_p（核采样）", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 0.95, Min: f(0), Max: f(1), Step: f(0.01), Runtime: true, Overridable: true, Common: true, PassThrough: true,
		Desc: "累积概率截断，越小越保守"})
	reg(Param{Key: "min_p", Label: "min_p", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 0.05, Min: f(0), Max: f(1), Step: f(0.005), Runtime: true, Overridable: true, Common: true, PassThrough: true,
		Desc: "按最大概率的比例截断，通常比 top_p 更稳", Advice: "比 top_p 更稳的截断"})
	reg(Param{Key: "typical_p", Label: "typical_p", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 1.0, Min: f(0), Max: f(1), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "局部典型性采样，1 表示关闭"})
	reg(Param{Key: "tfs_z", Label: "tfs_z", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 1.0, Min: f(0), Max: f(1), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "低概率长尾抑制，1 表示关闭"})
	reg(Param{Key: "top_n_sigma", Label: "top_n_sigma", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: -1.0, Min: f(-1), Max: f(10), Step: f(0.1), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "按标准差截断，-1 表示关闭"})
	reg(Param{Key: "repeat_penalty", Label: "重复惩罚 repeat_penalty", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 1.1, Min: f(0), Max: f(2), Step: f(0.01), Runtime: true, Overridable: true, Common: true, PassThrough: true,
		Desc: "1.0 不惩罚，越大越抑制重复。中文对话 1.05~1.15", Advice: "治复读"})
	reg(Param{Key: "repeat_last_n", Label: "重复惩罚窗口", Group: "采样", Scope: ScopeSample, Kind: KindInt,
		Default: 64, Min: f(-1), Max: f(4096), Step: f(1), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "回看多少 token 计算重复；-1 表示整段上下文"})
	reg(Param{Key: "frequency_penalty", Label: "频率惩罚 frequency_penalty", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 0.0, Min: f(-2), Max: f(2), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "OpenAI 风格：按出现次数惩罚"})
	reg(Param{Key: "presence_penalty", Label: "存在惩罚 presence_penalty", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 0.0, Min: f(-2), Max: f(2), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "OpenAI 风格：出现过就惩罚"})
	reg(Param{Key: "dry_multiplier", Label: "DRY 系数", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 0.0, Min: f(0), Max: f(5), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "DRY 采样惩罚强度，0 关闭。用于长文防复读"})
	reg(Param{Key: "dry_base", Label: "DRY base", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 1.75, Min: f(1), Max: f(4), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "DRY 增长底数"})
	reg(Param{Key: "dry_allowed_length", Label: "DRY 允许长度", Group: "采样", Scope: ScopeSample, Kind: KindInt,
		Default: 2, Min: f(0), Max: f(64), Step: f(1), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "允许重复的序列长度上限"})
	reg(Param{Key: "dry_penalty_last_n", Label: "DRY 回看窗口", Group: "采样", Scope: ScopeSample, Kind: KindInt,
		Default: -1, Min: f(-1), Max: f(8192), Step: f(1), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "DRY 计算窗口；-1 表示整段上下文"})
	reg(Param{Key: "xtc_probability", Label: "XTC 概率", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 0.0, Min: f(0), Max: f(1), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "XTC 采样触发概率，0 关闭"})
	reg(Param{Key: "xtc_threshold", Label: "XTC 阈值", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 0.1, Min: f(0), Max: f(1), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "XTC 被剔除 token 的概率阈值"})
	reg(Param{Key: "mirostat", Label: "Mirostat 模式", Group: "采样", Scope: ScopeSample, Kind: KindEnum,
		Default: "0", Options: []string{"0", "1", "2"}, Runtime: true, Overridable: true, PassThrough: true,
		Desc: "0 关闭；1 为 Mirostat v1；2 为 Mirostat v2（自动控困惑度）"})
	reg(Param{Key: "mirostat_tau", Label: "Mirostat tau", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 5.0, Min: f(0), Max: f(20), Step: f(0.1), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "目标困惑度，越小越保守"})
	reg(Param{Key: "mirostat_eta", Label: "Mirostat eta", Group: "采样", Scope: ScopeSample, Kind: KindFloat,
		Default: 0.1, Min: f(0), Max: f(1), Step: f(0.01), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "学习率"})
	reg(Param{Key: "max_tokens", Label: "最大输出 max_tokens", Group: "长度", Scope: ScopeLength, Kind: KindInt,
		Default: -1, Min: f(-1), Max: f(131072), Step: f(1), Runtime: true, Overridable: true, Common: true, PassThrough: true,
		Desc: "单次回复的 token 上限；-1 表示不限制（由上下文余量决定）", Advice: "控制输出长度"})
	reg(Param{Key: "n_predict", Label: "n_predict", Group: "长度", Scope: ScopeLength, Kind: KindInt,
		Default: -1, Min: f(-1), Max: f(131072), Step: f(1), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "llama.cpp 原生字段，与 max_tokens 等价"})
	reg(Param{Key: "min_keep", Label: "最少保留候选", Group: "长度", Scope: ScopeLength, Kind: KindInt,
		Default: 0, Min: f(0), Max: f(256), Step: f(1), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "采样时至少保留的候选数，防止截断过狠"})
	reg(Param{Key: "seed", Label: "随机种子 seed", Group: "控制", Scope: ScopeControl, Kind: KindInt,
		Default: -1, Min: f(-1), Max: f(2147483647), Step: f(1), Runtime: true, Overridable: true, Common: true, PassThrough: true,
		Desc: "-1 为随机；填固定值可复现同一输出", Advice: "复现结果"})
	reg(Param{Key: "stop", Label: "停止串 stop", Group: "控制", Scope: ScopeControl, Kind: KindStringList,
		Default: []string{}, Runtime: true, Overridable: true, Common: true, PassThrough: true,
		Desc: "命中即截断输出，可填多行", Advice: "截断标记"})
	reg(Param{Key: "ignore_eos", Label: "忽略 EOS", Group: "控制", Scope: ScopeControl, Kind: KindBool,
		Default: false, Runtime: true, Overridable: true, PassThrough: true,
		Desc: "忽略结束符，强制生成到 max_tokens（一般不用）"})
	reg(Param{Key: "stream", Label: "流式输出 stream", Group: "控制", Scope: ScopeControl, Kind: KindBool,
		Default: true, Runtime: true, Overridable: true, Common: true, PassThrough: true,
		Desc: "是否逐 token 返回；手机端聊天建议开启", Advice: "是否流式"})
	reg(Param{Key: "logprobs", Label: "返回对数概率", Group: "控制", Scope: ScopeControl, Kind: KindBool,
		Default: false, Runtime: true, Overridable: true, PassThrough: true,
		Desc: "返回每个 token 的 logprob，便于调试"})
	reg(Param{Key: "top_logprobs", Label: "top_logprobs", Group: "控制", Scope: ScopeControl, Kind: KindInt,
		Default: 0, Min: f(0), Max: f(20), Step: f(1), Runtime: true, Overridable: true, PassThrough: true,
		Desc: "每个位置返回的候选概率个数（需先开 logprobs）"})
	reg(Param{Key: "logit_bias", Label: "logit_bias", Group: "控制", Scope: ScopeControl, Kind: KindString,
		Default: "", Runtime: true, Overridable: true, PassThrough: true,
		Desc: "JSON：{\"token_id\": 偏置}，正数鼓励、负数抑制"})
	reg(Param{Key: "response_format", Label: "响应格式", Group: "结构化", Scope: ScopeControl, Kind: KindEnum,
		Default: "text", Options: []string{"text", "json_object", "json_schema"}, Runtime: true, Overridable: true, PassThrough: true,
		Desc: "要求模型输出 JSON 时的模式"})
	reg(Param{Key: "json_schema", Label: "JSON Schema", Group: "结构化", Scope: ScopeControl, Kind: KindString,
		Default: "", Runtime: true, Overridable: true, PassThrough: false,
		Desc: "response_format=json_schema 时使用的结构定义（本版仅记录，不做自动转换）"})
	reg(Param{Key: "grammar", Label: "GBNF 语法 grammar", Group: "结构化", Scope: ScopeControl, Kind: KindString,
		Default: "", Runtime: true, Overridable: true, PassThrough: true,
		Desc: "llama.cpp GBNF 语法约束输出，直接交给 llama-server"})
}

func regLoad() {
	reg(Param{Key: "model_path", Label: "模型路径", Group: "加载", Scope: ScopeLoad, Kind: KindString,
		Default: "", Runtime: false, Overridable: false, PassThrough: true,
		Desc: "GGUF 文件路径，由「模型」页选择"})
	reg(Param{Key: "mmproj", Label: "视觉投影 mmproj", Group: "加载", Scope: ScopeLoad, Kind: KindString,
		Default: "", Runtime: false, Overridable: false, PassThrough: true,
		Desc: "多模态模型的 mmproj-*.gguf 路径。留空时自动在同目录查找；VLM 必须配它才能识别图片"})
	reg(Param{Key: "lora", Label: "LoRA 列表", Group: "加载", Scope: ScopeLoad, Kind: KindStringList,
		Default: []string{}, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "挂载的 LoRA 适配器路径"})
	reg(Param{Key: "lora_scale", Label: "LoRA 强度", Group: "加载", Scope: ScopeLoad, Kind: KindFloat,
		Default: 1.0, Min: f(0), Max: f(2), Step: f(0.01), Runtime: false, Overridable: false, PassThrough: true,
		Desc: "LoRA 缩放系数"})
	reg(Param{Key: "n_gpu_layers", Label: "GPU 卸载层数 n_gpu_layers", Group: "加载", Scope: ScopeLoad, Kind: KindInt,
		Default: -1, Min: f(-1), Max: f(256), Step: f(1), Runtime: false, Overridable: false, Common: true, PassThrough: true,
		Desc: "-1 表示全部层放 GPU；显存不足时逐层减少。由推荐引擎自动给出", Advice: "显存与速度的核心权衡"})
	reg(Param{Key: "split_mode", Label: "多卡切分模式", Group: "加载", Scope: ScopeLoad, Kind: KindEnum,
		Default: "layer", Options: []string{"none", "layer", "row"}, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "多 GPU 时的切分方式"})
	reg(Param{Key: "tensor_split", Label: "张量切分比例", Group: "加载", Scope: ScopeLoad, Kind: KindStringList,
		Default: []string{}, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "各卡比例，如 0.6/0.4"})
	reg(Param{Key: "main_gpu", Label: "主 GPU 序号", Group: "加载", Scope: ScopeLoad, Kind: KindInt,
		Default: 0, Min: f(0), Max: f(8), Step: f(1), Runtime: false, Overridable: false, PassThrough: true,
		Desc: "主显卡编号"})
	reg(Param{Key: "n_ctx", Label: "上下文长度 n_ctx", Group: "加载", Scope: ScopeLoad, Kind: KindInt,
		Default: 8192, Min: f(128), Max: f(1048576), Step: f(128), Unit: " tokens", Runtime: false, Overridable: false, Common: true, PassThrough: true,
		Desc: "越大越能塞长文档，但 KV 显存线性增长。推荐值由硬件自检推导", Advice: "决定能否塞长文档"})
	reg(Param{Key: "n_batch", Label: "批大小 n_batch", Group: "加载", Scope: ScopeLoad, Kind: KindInt,
		Default: 512, Min: f(1), Max: f(8192), Step: f(1), Runtime: false, Overridable: false, PassThrough: true,
		Desc: "prompt 处理的逻辑批大小，影响显存与速度"})
	reg(Param{Key: "n_ubatch", Label: "物理批大小 n_ubatch", Group: "加载", Scope: ScopeLoad, Kind: KindInt,
		Default: 512, Min: f(1), Max: f(8192), Step: f(1), Runtime: false, Overridable: false, PassThrough: true,
		Desc: "物理批大小，显存不足时先降它"})
	reg(Param{Key: "rope_freq_base", Label: "RoPE base", Group: "加载", Scope: ScopeLoad, Kind: KindFloat,
		Default: 0.0, Min: f(0), Max: f(1e7), Step: f(1), Runtime: false, Overridable: false, PassThrough: true,
		Desc: "0 表示用模型默认值，外推长上下文时可调"})
	reg(Param{Key: "rope_freq_scale", Label: "RoPE scale", Group: "加载", Scope: ScopeLoad, Kind: KindFloat,
		Default: 0.0, Min: f(0), Max: f(10), Step: f(0.01), Runtime: false, Overridable: false, PassThrough: true,
		Desc: "0 表示用模型默认值"})
	reg(Param{Key: "rope_scaling", Label: "RoPE 外推方式", Group: "加载", Scope: ScopeLoad, Kind: KindEnum,
		Default: "none", Options: []string{"none", "linear", "yarn"}, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "长上下文外推策略"})
	reg(Param{Key: "cache_type_k", Label: "K 缓存类型 cache_type_k", Group: "缓存", Scope: ScopeCache, Kind: KindEnum,
		Default: "f16", Options: []string{"f16", "bf16", "q8_0", "q5_1", "q5_0", "q4_1", "q4_0", "iq4_nl"},
		Runtime: false, Overridable: false, Common: true, PassThrough: true,
		Desc: "KV 的 K 量化。q8_0 约省一半显存且几乎无损，q4_0 更省但可能掉质量", Advice: "省显存最有效的手段"})
	reg(Param{Key: "cache_type_v", Label: "V 缓存类型 cache_type_v", Group: "缓存", Scope: ScopeCache, Kind: KindEnum,
		Default: "f16", Options: []string{"f16", "bf16", "q8_0", "q5_1", "q5_0", "q4_1", "q4_0", "iq4_nl"},
		Runtime: false, Overridable: false, Common: true, PassThrough: true,
		Desc: "KV 的 V 量化，通常与 K 保持一致"})
	reg(Param{Key: "cache_reuse", Label: "KV 复用长度", Group: "缓存", Scope: ScopeCache, Kind: KindInt,
		Default: 0, Min: f(0), Max: f(1024), Step: f(1), Runtime: false, Overridable: false, PassThrough: true,
		Desc: "跨请求复用 KV 的最小长度，0 关闭"})
	reg(Param{Key: "n_threads", Label: "CPU 线程数", Group: "加载", Scope: ScopeLoad, Kind: KindInt,
		Default: 0, Min: f(0), Max: f(128), Step: f(1), Runtime: false, Overridable: false, PassThrough: true,
		Desc: "0 表示自动取物理核数；超过 16 收益递减"})
	reg(Param{Key: "n_threads_batch", Label: "批处理线程数", Group: "加载", Scope: ScopeLoad, Kind: KindInt,
		Default: 0, Min: f(0), Max: f(128), Step: f(1), Runtime: false, Overridable: false, PassThrough: true,
		Desc: "0 表示与 n_threads 相同"})
	reg(Param{Key: "numa", Label: "NUMA 策略", Group: "加载", Scope: ScopeLoad, Kind: KindEnum,
		Default: "disabled", Options: []string{"disabled", "distribute", "isolate", "numactl"}, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "多路服务器才有意义，桌面机保持 disabled"})
	reg(Param{Key: "mmap", Label: "内存映射 mmap", Group: "加载", Scope: ScopeLoad, Kind: KindBool,
		Default: true, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "Windows 上建议始终开启，加快加载并降低内存峰值"})
	reg(Param{Key: "mlock", Label: "锁定内存 mlock", Group: "加载", Scope: ScopeLoad, Kind: KindBool,
		Default: false, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "锁定物理内存防换页；桌面系统收益小、风险大"})
	reg(Param{Key: "no_kv_offload", Label: "禁止 KV 卸载到 GPU", Group: "加载", Scope: ScopeLoad, Kind: KindBool,
		Default: false, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "显存极紧张时把 KV 放回内存（速度明显下降）"})
	reg(Param{Key: "flash_attn", Label: "FlashAttention", Group: "加速", Scope: ScopeAccel, Kind: KindEnum,
		Default: "auto", Options: []string{"auto", "on", "off"}, Runtime: false, Overridable: false, Common: true, PassThrough: true,
		Desc: "开启后长上下文更快更省显存；量化 KV 必须配合它", Advice: "一个开关，速度差异明显"})
	reg(Param{Key: "no_perf", Label: "关闭性能计时", Group: "加速", Scope: ScopeAccel, Kind: KindBool,
		Default: false, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "关闭内部计时，理论上略微加速"})
	reg(Param{Key: "offload_kqv", Label: "卸载 KQV 到 GPU", Group: "加速", Scope: ScopeAccel, Kind: KindBool,
		Default: true, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "把注意力计算放 GPU，关掉可省显存但变慢"})
	reg(Param{Key: "swa_full", Label: "SWA 全量缓存", Group: "加速", Scope: ScopeAccel, Kind: KindBool,
		Default: false, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "滑动窗口注意力模型是否用全量 KV（更费显存、更准）"})
	reg(Param{Key: "embedding", Label: "嵌入模式", Group: "加载", Scope: ScopeLoad, Kind: KindBool,
		Default: false, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "以向量化模式加载，供 /v1/embeddings 使用"})
	reg(Param{Key: "pooling_type", Label: "池化方式", Group: "加载", Scope: ScopeLoad, Kind: KindEnum,
		Default: "unspecified", Options: []string{"unspecified", "none", "mean", "cls", "last", "rank"}, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "嵌入模式的池化策略"})
	reg(Param{Key: "cont_batching", Label: "连续批处理", Group: "加载", Scope: ScopeLoad, Kind: KindBool,
		Default: true, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "连续批处理可提升并发吞吐"})
	reg(Param{Key: "n_parallel", Label: "并发槽 n_parallel", Group: "加载", Scope: ScopeLoad, Kind: KindInt,
		Default: 1, Min: f(1), Max: f(32), Step: f(1), Runtime: false, Overridable: false, PassThrough: true,
		Desc: "同时处理的请求数，每个槽都会占用 KV 显存"})
	reg(Param{Key: "check_tensors", Label: "校验张量", Group: "加载", Scope: ScopeLoad, Kind: KindBool,
		Default: false, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "加载时校验张量完整性（更慢，但能提前发现坏文件）"})
}

// regReasoning 思考（reasoning）模式的调节中心：开关、预算、内容去向、强度。
//
// 这些是**加载级**参数（会变成 llama-server 的启动开关，见 llamacpp.BuildArgs），
// 改动后需要重新加载模型才生效。
func regReasoning() {
	reg(Param{Key: "reasoning", Label: "思考模式 reasoning", Group: "思考", Scope: ScopeLoad, Kind: KindEnum,
		Default: "auto", Options: []string{"auto", "on", "off"}, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "auto 由模型模板决定（思考模型默认开）；on 强制思考；off 关闭思考", Advice: "手机端收不到答案时先关它"})
	reg(Param{Key: "reasoning_budget", Label: "思考预算 reasoning_budget", Group: "思考", Scope: ScopeLoad, Kind: KindInt,
		Default: -1, Min: f(-1), Max: f(32768), Step: f(1), Unit: " tokens", Runtime: false, Overridable: false, PassThrough: true,
		Desc: "思考最多用多少 token；-1 不限，0 立即结束（等于关闭思考），N 限制思考长度", Advice: "防止思考吃光 max_tokens"})
	reg(Param{Key: "reasoning_format", Label: "思考内容去向 reasoning_format", Group: "思考", Scope: ScopeLoad, Kind: KindEnum,
		Default: "auto", Options: []string{"auto", "none", "deepseek", "deepseek-legacy"}, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "none 思考混在正文里；deepseek 单独放到 reasoning_content；auto 由引擎判断", Advice: "客户端看不到思考内容时改 deepseek"})
	reg(Param{Key: "reasoning_effort", Label: "思考强度 reasoning_effort", Group: "思考", Scope: ScopeLoad, Kind: KindEnum,
		Default: "default", Options: []string{"default", "minimal", "low", "medium", "high", "xhigh", "max"}, Runtime: false, Overridable: false, PassThrough: true,
		Desc: "传给模型模板的思考强度（仅支持的模型有效）；default 保持模板默认"})
}

func regPrompt() {
	reg(Param{Key: "system_prompt", Label: "系统提示词 system", Group: "Prompt", Scope: ScopePrompt, Kind: KindString,
		Default: "", Runtime: true, Overridable: true, Common: true, PassThrough: true,
		Desc: "全局系统提示词；请求体未带 system 时自动注入", Advice: "最常改的一项"})
	reg(Param{Key: "prompt_template", Label: "提示词模板", Group: "Prompt", Scope: ScopePrompt, Kind: KindString,
		Default: "", Runtime: true, Overridable: false, PassThrough: false,
		Desc: "自定义拼装模板，支持 {{.System}} / {{.Prompt}} 占位（本版仅保存，不做注入改写）"})
	reg(Param{Key: "chat_template_mode", Label: "Chat 模板模式", Group: "Prompt", Scope: ScopePrompt, Kind: KindEnum,
		Default: "auto", Options: []string{"auto", "manual", "jinja"}, Runtime: false, Overridable: false, PassThrough: false,
		Desc: "auto 用模型内置模板；manual/jinja 使用自定义模板"})
	reg(Param{Key: "chat_template", Label: "Chat 模板内容", Group: "Prompt", Scope: ScopePrompt, Kind: KindString,
		Default: "", Runtime: false, Overridable: false, PassThrough: false,
		Desc: "自定义 Chat 模板（Jinja 或替换式）"})
	reg(Param{Key: "stop_words", Label: "全局停止词", Group: "Prompt", Scope: ScopePrompt, Kind: KindStringList,
		Default: []string{}, Runtime: true, Overridable: true, PassThrough: true,
		Desc: "对所有请求生效的停止串"})
	reg(Param{Key: "prefix_suffix", Label: "FIM 前后缀模式", Group: "Prompt", Scope: ScopePrompt, Kind: KindBool,
		Default: false, Runtime: true, Overridable: false, PassThrough: false,
		Desc: "代码补全（Fill-In-Middle）模式"})
	reg(Param{Key: "context_shift", Label: "上下文溢出策略", Group: "Prompt", Scope: ScopePrompt, Kind: KindEnum,
		Default: "keep_first", Options: []string{"keep_first", "keep_last", "shift"}, Runtime: true, Overridable: false, PassThrough: false,
		Desc: "超出上下文时保留开头还是最近内容"})
	reg(Param{Key: "add_bos", Label: "添加 BOS", Group: "Prompt", Scope: ScopePrompt, Kind: KindBool,
		Default: false, Runtime: true, Overridable: false, PassThrough: true,
		Desc: "在提示词前加 BOS（一般由模板自动处理）"})
	reg(Param{Key: "add_eos", Label: "添加 EOS", Group: "Prompt", Scope: ScopePrompt, Kind: KindBool,
		Default: false, Runtime: true, Overridable: false, PassThrough: true,
		Desc: "在提示词后加 EOS"})
}

func regService() {
	reg(Param{Key: "public_host", Label: "监听地址", Group: "服务", Scope: ScopeService, Kind: KindString,
		Default: "0.0.0.0", Runtime: false, Overridable: false, PassThrough: false,
		Desc: "0.0.0.0 = 局域网可访问；127.0.0.1 = 仅本机"})
	reg(Param{Key: "public_port", Label: "网关端口", Group: "服务", Scope: ScopeService, Kind: KindInt,
		Default: 8080, Min: f(1024), Max: f(65535), Step: f(1), Runtime: false, Overridable: false, PassThrough: false,
		Desc: "手机/笔记本连接使用该端口"})
	reg(Param{Key: "cors_origins", Label: "CORS 允许来源", Group: "服务", Scope: ScopeService, Kind: KindStringList,
		Default: []string{"*"}, Runtime: true, Overridable: false, PassThrough: false,
		Desc: "允许跨域调用的来源，* 表示不限制"})
	reg(Param{Key: "max_concurrent_requests", Label: "最大并发请求", Group: "服务", Scope: ScopeService, Kind: KindInt,
		Default: 4, Min: f(1), Max: f(64), Step: f(1), Runtime: true, Overridable: false, PassThrough: false,
		Desc: "超过后按队列策略处理"})
	reg(Param{Key: "request_timeout", Label: "请求超时", Group: "服务", Scope: ScopeService, Kind: KindInt,
		Default: 300, Min: f(10), Max: f(3600), Step: f(10), Unit: " 秒", Runtime: true, Overridable: false, PassThrough: false,
		Desc: "单个请求的最长处理时间"})
	reg(Param{Key: "queue_policy", Label: "队列策略", Group: "服务", Scope: ScopeService, Kind: KindEnum,
		Default: "queue", Options: []string{"queue", "reject"}, Runtime: true, Overridable: false, PassThrough: false,
		Desc: "并发满时排队等待，还是直接拒绝"})
	reg(Param{Key: "allow_auto_load", Label: "按需自动加载", Group: "服务", Scope: ScopeService, Kind: KindBool,
		Default: true, Runtime: true, Overridable: false, PassThrough: false,
		Desc: "请求的模型未加载时自动加载（手机端无需关心模型状态）"})
	reg(Param{Key: "idle_unload_minutes", Label: "空闲自动卸载", Group: "服务", Scope: ScopeService, Kind: KindInt,
		Default: 30, Min: f(0), Max: f(1440), Step: f(1), Unit: " 分钟", Runtime: true, Overridable: false, PassThrough: false,
		Desc: "0 表示不自动卸载"})
	reg(Param{Key: "vram_reserve_mb", Label: "显存安全余量", Group: "服务", Scope: ScopeService, Kind: KindInt,
		Default: 512, Min: f(0), Max: f(8192), Step: f(64), Unit: " MB", Runtime: true, Overridable: false, PassThrough: false,
		Desc: "预留给桌面/系统的显存，推荐时从可用量中扣除"})
	reg(Param{Key: "max_loaded_instances", Label: "最大加载实例数", Group: "服务", Scope: ScopeService, Kind: KindInt,
		Default: 2, Min: f(1), Max: f(8), Step: f(1), Runtime: true, Overridable: false, PassThrough: false,
		Desc: "同时加载的模型数量上限（受显存约束）"})
	reg(Param{Key: "auto_apply_recommendation", Label: "自动应用推荐参数", Group: "服务", Scope: ScopeService, Kind: KindBool,
		Default: true, Runtime: true, Overridable: false, PassThrough: false,
		Desc: "切换模型时静默填入硬件推荐值（不弹窗，可随时手改）"})
	reg(Param{Key: "advisor_conservative", Label: "推荐保守系数", Group: "服务", Scope: ScopeService, Kind: KindFloat,
		Default: 1.0, Min: f(1), Max: f(1.5), Step: f(0.01), Runtime: true, Overridable: false, PassThrough: false,
		Desc: "预估显存乘以该系数，越大越保守（1.15 适合不确定的场景）"})
}
