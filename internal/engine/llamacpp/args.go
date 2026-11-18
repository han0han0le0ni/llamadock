package llamacpp

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// BuildArgs 把参数注册表的取值翻译成 llama-server 命令行参数。
//
// 这是「自己拉起模型」的核心：参数面板里的加载级参数
// （n_gpu_layers / n_ctx / cache_type_k/v / flash_attn / n_batch / n_threads / mmap …）
// 在这里真正变成进程的启动开关，而不是只写进配置里当摆设。
//
// 纯函数、无副作用，参数顺序稳定 → 可单测（见 args_test.go）。
func BuildArgs(values map[string]any) []string {
	a := &argBuilder{}
	p := newReader(values)

	// 模型
	if m := p.str("model_path"); m != "" {
		a.add("-m", m)
	}
	// 上下文与卸载
	if v := p.int("n_ctx"); v > 0 {
		a.add("-c", strconv.Itoa(v))
	}
	if p.has("n_gpu_layers") {
		a.add("-ngl", strconv.Itoa(p.int("n_gpu_layers")))
	}
	// 批处理
	if v := p.int("n_batch"); v > 0 {
		a.add("-b", strconv.Itoa(v))
	}
	if v := p.int("n_ubatch"); v > 0 {
		a.add("-ub", strconv.Itoa(v))
	}
	// 线程
	if v := p.int("n_threads"); v > 0 {
		a.add("-t", strconv.Itoa(v))
	}
	if v := p.int("n_threads_batch"); v > 0 {
		a.add("-tb", strconv.Itoa(v))
	}
	// KV 缓存量化（省显存最有效的手段）
	if v := p.str("cache_type_k"); v != "" {
		a.add("-ctk", v)
	}
	if v := p.str("cache_type_v"); v != "" {
		a.add("-ctv", v)
	}
	// FlashAttention
	if v := p.str("flash_attn"); v != "" {
		a.add("-fa", v)
	}
	// 并发槽
	if v := p.int("n_parallel"); v > 1 {
		a.add("-np", strconv.Itoa(v))
	}
	// 多卡
	if p.has("main_gpu") {
		a.add("-mg", strconv.Itoa(p.int("main_gpu")))
	}
	if ts := p.list("tensor_split"); len(ts) > 0 {
		a.add("-ts", strings.Join(ts, ","))
	}
	if v := p.str("split_mode"); v != "" && v != "layer" {
		a.add("--split-mode", v)
	}
	// RoPE
	if v := p.float("rope_freq_base"); v > 0 {
		a.add("--rope-freq-base", trimFloat(v))
	}
	if v := p.float("rope_freq_scale"); v > 0 {
		a.add("--rope-freq-scale", trimFloat(v))
	}
	if v := p.str("rope_scaling"); v != "" && v != "none" {
		a.add("--rope-scaling", v)
	}
	// 内存
	if v := p.str("numa"); v != "" && v != "disabled" {
		a.add("--numa", v)
	}
	// 加载模式：新版 llama.cpp（b11617+）用 --load-mode 取代了旧的
	// --mmap / --no-mmap / --mlock —— 旧开关会直接报 "invalid argument" 启动失败。
	if mode := loadMode(p); mode != "" {
		a.add("--load-mode", mode)
	}
	// KV / 计算卸载
	if p.bool("no_kv_offload") || !p.boolDefault("offload_kqv", true) {
		a.add("--no-kv-offload")
	}
	if p.bool("no_perf") {
		a.add("--no-perf")
	}
	// 批处理调度
	if p.has("cont_batching") {
		if p.bool("cont_batching") {
			a.add("--cont-batching")
		} else {
			a.add("--no-cont-batching")
		}
	}
	// 嵌入模式
	if p.bool("embedding") {
		a.add("--embedding")
	}
	if v := p.str("pooling_type"); v != "" && v != "unspecified" {
		a.add("--pooling", v)
	}
	// 缓存复用 / SWA / 校验
	if v := p.int("cache_reuse"); v > 0 {
		a.add("--cache-reuse", strconv.Itoa(v))
	}
	if p.bool("swa_full") {
		a.add("--swa-full")
	}
	if p.bool("check_tensors") {
		a.add("--check-tensors")
	}
	// 上下文溢出策略
	if p.str("context_shift") == "shift" {
		a.add("--context-shift")
	}
	// 自定义 Chat 模板
	// 新版 llama.cpp 要求：内联 Jinja 模板文本必须配合 --jinja 才会被解析，
	// 否则报 "started without --jinja, we only support commonly used templates" 并拒绝启动。
	// 因此下发 --chat-template 时必须同时下发 --jinja，否则整套自定义模板形同虚设。
	// auto 模式（用模型内嵌模板）不受影响，保持原样。
	if p.str("chat_template_mode") != "" && p.str("chat_template_mode") != "auto" {
		if tpl, ok := p.templateText(); ok {
			a.add("--jinja")
			a.add("--chat-template", tpl)
		}
	}
	// 思考（reasoning）模式：与引擎默认一致的取值不下发，避免改变既有行为
	if v := p.str("reasoning"); v != "" && v != "auto" {
		a.add("--reasoning", v)
	}
	if v := p.str("reasoning_format"); v != "" && v != "auto" {
		a.add("--reasoning-format", v)
	}
	if v := p.str("reasoning_effort"); v != "" && v != "default" {
		a.add("--reasoning-effort", v)
	}
	if p.has("reasoning_budget") {
		if v := p.int("reasoning_budget"); v != -1 {
			a.add("--reasoning-budget", strconv.Itoa(v))
		}
	}
	// LoRA
	// 注意：-lm 在新版 llama.cpp 里已改指 --load-mode，不能再用它挂载适配器；
	// 官方要求多个适配器用逗号连接成一个取值。
	scale := p.floatDefault("lora_scale", 1)
	if paths := p.list("lora"); len(paths) > 0 {
		if math.Abs(scale-1) < 1e-9 {
			a.add("--lora", strings.Join(paths, ","))
		} else {
			scaled := make([]string, 0, len(paths))
			for _, path := range paths {
				scaled = append(scaled, fmt.Sprintf("%s:%s", path, trimFloat(scale)))
			}
			a.add("--lora-scaled", strings.Join(scaled, ","))
		}
	}
	return a.args
}

// loadMode 把 mmap / mlock 两个开关折算成新版 llama.cpp 的 --load-mode 取值：
// auto（默认，等价 mmap）/ none / mmap / mlock / mmap+mlock / dio。
//
// 默认组合（mmap 开、mlock 关）不下发任何参数，保持与引擎默认一致。
func loadMode(p *reader) string {
	mmap := true
	if p.has("mmap") {
		mmap = p.bool("mmap")
	}
	mlock := p.bool("mlock")
	switch {
	case mmap && mlock:
		return "mmap+mlock"
	case !mmap && mlock:
		return "mlock"
	case !mmap:
		return "none"
	default:
		return ""
	}
}

type argBuilder struct{ args []string }

func (a *argBuilder) add(parts ...string) { a.args = append(a.args, parts...) }

// String 便于日志与界面展示（命令回显）。
func (a *argBuilder) String() string { return strings.Join(a.args, " ") }

// --- 取值读取（容错：JSON 里数字可能是 float64 / int / string） ---

type reader struct{ m map[string]any }

func newReader(m map[string]any) *reader { return &reader{m: m} }

func (r *reader) has(key string) bool {
	_, ok := r.m[key]
	return ok
}

func (r *reader) raw(key string) any {
	v, _ := r.m[key]
	if v == nil {
		return nil
	}
	return v
}

func (r *reader) int(key string) int {
	switch v := r.raw(key).(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(math.Round(v))
	case json.Number:
		f, _ := v.Float64()
		return int(math.Round(f))
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

func (r *reader) float(key string) float64 { return r.floatDefault(key, 0) }

func (r *reader) floatDefault(key string, def float64) float64 {
	switch v := r.raw(key).(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, err := v.Float64()
		if err == nil {
			return f
		}
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return f
		}
	}
	return def
}

func (r *reader) str(key string) string {
	switch v := r.raw(key).(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case int:
		return strconv.Itoa(v)
	case float64:
		return trimFloat(v)
	}
	return ""
}

func (r *reader) bool(key string) bool { return r.boolDefault(key, false) }

func (r *reader) boolDefault(key string, def bool) bool {
	switch v := r.raw(key).(type) {
	case bool:
		return v
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			return b
		}
	case float64:
		return v != 0
	case int:
		return v != 0
	}
	return def
}

func (r *reader) list(key string) []string {
	switch v := r.raw(key).(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	return nil
}

// templateText 取自定义模板文本（兼容多行）。
func (r *reader) templateText() (string, bool) {
	s := r.str("chat_template")
	if s == "" {
		return "", false
	}
	return s, true
}

func trimFloat(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// CommandLine 生成可读的完整命令行（含可执行文件与 host/port），用于界面回显。
func CommandLine(exe, host string, port int, args []string) string {
	full := append([]string{}, args...)
	if host != "" && port > 0 {
		full = append(full, "--host", host, "--port", strconv.Itoa(port))
	}
	quoted := make([]string, 0, len(full)+1)
	quoted = append(quoted, quoteIfNeeded(exe))
	for _, a := range full {
		quoted = append(quoted, quoteIfNeeded(a))
	}
	return strings.Join(quoted, " ")
}

func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\n\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// SortedKeys 调试用：返回参数 key 的稳定顺序。
func SortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
