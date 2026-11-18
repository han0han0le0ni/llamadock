// Package engine 定义推理引擎的**统一适配层**。
//
// 目前只有一个实现：本程序自己拉起的 llama-server 子进程（可调度——
// 显存与上下文由我们决定，见 internal/engine/llamacpp）。
//
// 之所以仍然保留这层接口：上层（core / server / api / 前端）只依赖这里的中性类型，
// 将来新增后端（Ollama / vLLM / transformers）时不必改动上层任何一行代码。
package engine

import (
	"context"
	"net/http"

	"airunner/internal/gguf"
)

// Kind 引擎类型。
type Kind string

// 支持的引擎。
const (
	KindLlamaCpp Kind = "llamacpp" // 本程序自己拉起的 llama-server 子进程（可调度）
)

// Model 统一模型视图。
type Model struct {
	ID      string     `json:"id"`   // 自管引擎：gguf 文件绝对路径
	Name    string     `json:"name"` // 展示名
	Path    string     `json:"path,omitempty"`
	Type    string     `json:"type,omitempty"` // llm / embeddings / vlm
	Arch    string     `json:"arch,omitempty"`
	Quant   string     `json:"quant,omitempty"`
	State   string     `json:"state,omitempty"` // loaded / not-loaded
	Loaded  bool       `json:"loaded"`
	SizeMB  int        `json:"size_mb,omitempty"`
	MaxCtx  int        `json:"max_context_length,omitempty"`
	Meta    *gguf.Meta `json:"meta,omitempty"`
	Sharded bool       `json:"sharded,omitempty"` // 多分片 GGUF 的后续分片（不单独展示）
}

// Health 统一健康信息。
type Health struct {
	Online     bool   `json:"online"`
	Kind       string `json:"kind"`
	Endpoint   string `json:"endpoint"`
	LatencyMs  int    `json:"latency_ms"`
	ModelCount int    `json:"model_count"`
	Error      string `json:"error,omitempty"`

	// 自管引擎的进程信息
	Schedulable bool     `json:"schedulable"` // true = 本程序能决定加载几层到 GPU
	Running     bool     `json:"running,omitempty"`
	PID         int      `json:"pid,omitempty"`
	Model       string   `json:"model,omitempty"`
	ModelPath   string   `json:"model_path,omitempty"`
	UptimeSec   int64    `json:"uptime_sec,omitempty"`
	RSSMB       int      `json:"rss_mb,omitempty"`
	BinPath     string   `json:"bin_path,omitempty"`
	Variant     string   `json:"variant,omitempty"`
	CmdLine     string   `json:"cmd_line,omitempty"`
	Args        []string `json:"args,omitempty"`
	LogPath     string   `json:"log_path,omitempty"`
	LogTail     []string `json:"log_tail,omitempty"` // 子进程最近若干行输出
}

// LoadOptions 加载模型时的选项（来自参数注册表合并结果）。
type LoadOptions struct {
	Params map[string]any // 合并后的全量参数
	Alias  string         // /v1/models 里展示的名字
}

// Adapter 引擎适配器统一接口。
type Adapter interface {
	Kind() Kind
	// Schedulable 是否由本程序控制显存/进程
	Schedulable() bool
	// Endpoint 引擎基址
	Endpoint() string
	// Health 探测一次
	Health(ctx context.Context) Health
	// List 列出可用模型
	List(ctx context.Context) ([]Model, error)
	// Forward 转发 OpenAI 兼容请求（流式由调用方处理响应体）
	Forward(ctx context.Context, path string, body []byte, hdr http.Header) (*http.Response, error)
	// Load 加载模型（即拉起 llama-server 子进程）
	Load(ctx context.Context, model Model, opts LoadOptions) error
	// Unload 卸载模型（即停止子进程）
	Unload(ctx context.Context, model Model) error
}
