// Package server 提供对外网关：单端口、OpenAI 兼容、多格式协商。
//
// v2.0 有意不做的事情（见设计方案 §6）：
//   - 不鉴权（不公布、局域网自用）
//   - 不做来源过滤（只保证 allow_lan_only 之外的额外防护一律不做）
//   - 不做双监听器（管理 API 只绑 127.0.0.1，由 api 包负责）
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"airunner/internal/core"
	"airunner/internal/params"
	"airunner/internal/reqlog"
)

// Gateway 推理网关。
type Gateway struct {
	core *core.Core
	log  *slog.Logger

	mu      sync.RWMutex
	srv     *http.Server
	ln      net.Listener
	addr    string
	sem     chan struct{}
	running bool
}

// NewGateway 创建网关。
func NewGateway(c *core.Core, log *slog.Logger) *Gateway {
	return &Gateway{core: c, log: log}
}

// Start 开始监听。
func (g *Gateway) Start(host string, port int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败：%w", addr, err)
	}
	cfg := g.core.ConfigSnapshot()
	maxConc := cfg.Server.MaxConcurrent
	if maxConc <= 0 {
		maxConc = 4
	}
	g.sem = make(chan struct{}, maxConc)
	g.ln = ln
	g.addr = ln.Addr().String()
	g.srv = &http.Server{
		Handler:           g.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	g.running = true
	go func() {
		if err := g.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			g.log.Error("网关异常退出", "error", err)
		}
	}()
	g.log.Info("推理网关已启动", "addr", g.addr, "并发上限", maxConc)
	return nil
}

// Stop 优雅停止。
func (g *Gateway) Stop(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.running {
		return nil
	}
	g.running = false
	err := g.srv.Shutdown(ctx)
	g.log.Info("推理网关已停止")
	return err
}

// Running 是否在监听。
func (g *Gateway) Running() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.running
}

// Addr 监听地址。
func (g *Gateway) Addr() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.addr
}

// Handler 路由表。
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", g.handleCompletions("/v1/chat/completions"))
	mux.HandleFunc("POST /v1/completions", g.handleCompletions("/v1/completions"))
	mux.HandleFunc("POST /v1/embeddings", g.handleCompletions("/v1/embeddings"))
	mux.HandleFunc("GET /v1/models", g.handleModels)
	mux.HandleFunc("GET /healthz", g.handleHealthz)
	mux.HandleFunc("GET /readyz", g.handleReadyz)
	mux.HandleFunc("GET /api/status", g.handleStatus)
	mux.HandleFunc("GET /", g.handleRoot)
	return mux
}

func (g *Gateway) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeErr(w, http.StatusNotFound, "not_found", "未知路径", "")
		return
	}
	cfg := g.core.ConfigSnapshot()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<title>LlamaDock 网关</title><style>
body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;background:#0f1115;color:#e6e8eb;margin:0;padding:48px}
code{background:#1b1f27;padding:2px 6px;border-radius:4px}
.card{max-width:760px;margin:0 auto;background:#161a21;border:1px solid #242a35;border-radius:12px;padding:28px}
h1{font-size:20px;margin:0 0 16px} li{margin:6px 0;line-height:1.7;color:#b6bcc6}
a{color:#6ea8fe}
</style></head><body><div class="card">
<h1>LlamaDock 网关正在运行</h1>
<ul>
<li>OpenAI 兼容入口：<code>http://%s:%d/v1</code></li>
<li>模型列表：<a href="/v1/models">/v1/models</a> · 健康探针：<a href="/healthz">/healthz</a></li>
<li>运行状态：<a href="/api/status">/api/status</a></li>
<li>本程序是个人自用的本地大模型控制台，无鉴权、无来源过滤。</li>
</ul></div></body></html>`, hostForDisplay(r), cfg.Server.Port)
}

func hostForDisplay(r *http.Request) string {
	h := r.Host
	if i := strings.LastIndex(h, ":"); i > 0 {
		h = h[:i]
	}
	if h == "" {
		h = "127.0.0.1"
	}
	return h
}

func (g *Gateway) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": core.Version})
}

func (g *Gateway) handleReadyz(w http.ResponseWriter, r *http.Request) {
	eng := g.core.EngineInfo()
	if !eng.Health.Online {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "degraded", "engine": eng.Health.Error, "endpoint": eng.Endpoint,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "engine": eng.Endpoint, "models": eng.Health.ModelCount})
}

func (g *Gateway) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.core.Status())
}

func (g *Gateway) handleModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	if !g.core.EngineInfo().Health.Online {
		g.core.RescanEngine(ctx)
	}
	models, err := g.core.Models(ctx)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "engine_unavailable", err.Error(), "engine_unavailable")
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, m := range models {
		data = append(data, map[string]any{
			"id":       m.ID,
			"object":   "model",
			"owned_by": "llamadock",
			"created":  time.Now().Unix(),
			"state":    m.State,
			"type":     m.Type,
			"arch":     m.Arch,
			"quant":    m.Quant,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// handleCompletions 处理推理请求：参数合并 → 转发 → 多格式输出 → 记账。
func (g *Gateway) handleCompletions(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		cfg := g.core.ConfigSnapshot()
		release, ok := g.acquire(r.Context(), cfg.Server.RequestTimeout)
		if !ok {
			writeErr(w, http.StatusServiceUnavailable, "queue_full", "并发已满，请稍后重试", "QUEUE_FULL")
			return
		}
		defer release()

		g.core.Metrics.BeginRequest()
		entry := reqlog.Entry{
			Time:     start,
			SourceIP: clientIP(r),
			Endpoint: path,
		}
		var (
			status         = http.StatusOK
			failed         = false
			promptTok      int
			compTok        int
			progressTokens int // 已计入实时吞吐的 token 数（避免与最终 usage 重复计数）
			ttftMs         int
			formatName     = "json"
			errMsg         string
		)
		defer func() {
			entry.Status = status
			entry.PromptTokens = promptTok
			entry.CompletionTokens = compTok
			entry.TTFTMs = ttftMs
			entry.DurationMs = int(time.Since(start).Milliseconds())
			entry.Stream = formatName == "sse"
			entry.Format = formatName
			entry.Error = errMsg
			g.core.ReqLog.Add(entry)
			g.core.Metrics.EndRequest(failed)
			// 总量按 usage 全额累计（AddTotals 只写总量，不写吞吐窗口）。
			if promptTok > 0 || compTok > 0 {
				g.core.Metrics.AddTotals(promptTok, compTok)
			}
			// 吞吐窗口已由流式过程的 AddProgress 填充过 progressTokens，
			// 这里只补差额，避免重复计数。
			if remain := compTok - progressTokens; remain > 0 {
				g.core.Metrics.AddProgress(remain)
			}
		}()

		body, err := io.ReadAll(io.LimitReader(r.Body, int64(maxInt(cfg.Server.MaxBodyMB, 1))<<20))
		if err != nil {
			status, errMsg = http.StatusBadRequest, "读取请求体失败"
			writeErr(w, status, "invalid_request", errMsg, "")
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			status, errMsg = http.StatusBadRequest, "请求体不是合法 JSON"
			writeErr(w, status, "invalid_request", errMsg, "")
			return
		}

		// 模型解析
		model, _ := payload["model"].(string)
		models, _ := g.core.Models(r.Context())
		resolved, err := resolveModel(model, models, cfg.Server.AllowAutoLoad)
		if err != nil {
			status, errMsg = http.StatusNotFound, err.Error()
			writeErr(w, status, "model_not_found", errMsg, "MODEL_NOT_FOUND")
			return
		}
		payload["model"] = resolved
		entry.Model = resolved

		// 参数作用域合并（请求 > 全局）
		merge := params.Merge(payload, cfg.Params)
		if len(merge.Dropped) > 0 {
			for _, k := range merge.Dropped {
				delete(payload, k)
			}
			g.log.Warn("请求试图覆盖不可覆盖参数，已拒绝", "keys", merge.Dropped)
		}
		if len(merge.Invalid) > 0 {
			status, errMsg = http.StatusBadRequest, merge.Invalid[0].Message
			writeErr(w, status, "param_invalid", errMsg, "PARAM_INVALID")
			return
		}
		if len(merge.Unknown) > 0 {
			// 非参数注册表字段（messages / tools / tool_choice / n ...）原样透传给引擎，
			// 保证任何 OpenAI 兼容客户端零改代码可用；仅记日志便于排查。
			g.log.Debug("请求含注册表外字段，已透传", "keys", merge.Unknown)
		}
		engineParams := params.EngineBody(merge.Final)
		for k, v := range engineParams {
			payload[k] = v
		}
		if sys := strings.TrimSpace(fmt.Sprint(merge.Final["system_prompt"])); sys != "" {
			injectSystem(payload, sys)
		}
		// 规范化消息顺序：把 system 提到首位，兼容 Llama3/Qwen/Gemma 等
		// 带「System message must be at the beginning」严格校验的模板。
		if cfg.Server.ShouldNormalizeMessages() {
			normalizeMessages(payload)
		}

		// 输出格式协商
		streamReq := boolOf(payload["stream"])
		mode := negotiate(r, streamReq)
		formatName = mode
		payload["stream"] = mode == "sse" || mode == "ndjson" || mode == "text"

		out, _ := json.Marshal(payload)
		// 自管引擎（llamacpp）：请求前按需拉起模型；外部引擎交给它自己 JIT 加载。
		if err := g.core.EnsureLoaded(r.Context(), resolved); err != nil {
			status, errMsg = http.StatusServiceUnavailable, err.Error()
			failed = true
			writeErr(w, status, "engine_unavailable", errMsg, "ENGINE_OFFLINE")
			return
		}
		resp, err := g.core.Forward(r.Context(), path, out, r.Header)
		if err != nil {
			status, errMsg = http.StatusServiceUnavailable, "llama-server 未就绪，请稍后重试（"+err.Error()+"）"
			failed = true
			writeErr(w, status, "engine_unavailable", errMsg, "ENGINE_OFFLINE")
			return
		}
		defer resp.Body.Close()

		entry.Stream = streamReq
		entry.Format = mode

		if resp.StatusCode >= 400 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			status, errMsg = resp.StatusCode, truncate(string(raw), 400)
			failed = true
			g.log.Warn("引擎返回错误", "status", resp.StatusCode, "body", truncate(string(raw), 400))
			// 把模板类报错翻译成中文可读提示，便于定位（原始数据附在末尾）
			outRaw := localizeEngineError(raw)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(outRaw)
			return
		}

		if mode == "" { // 非流式：原样透传 JSON
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				status, errMsg = http.StatusBadGateway, "读取引擎响应失败"
				failed = true
				writeErr(w, status, "engine_error", errMsg, "")
				return
			}
			promptTok, compTok = usageOf(raw)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(raw)
			return
		}

		// 流式
		w.Header().Set("Content-Type", contentTypeFor(mode))
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		first := true
		var buf strings.Builder
		sawUsage := false
		chunks := 0
		progressAcc := 0
		lastProgress := time.Now()
		scanSSEWithRaw(resp.Body, func(raw string, d delta) {
			if first {
				ttftMs = int(time.Since(start).Milliseconds())
				g.core.Metrics.AddTTFT(time.Duration(ttftMs) * time.Millisecond)
				first = false
			}
			if d.ErrorMsg != "" {
				errMsg = d.ErrorMsg
				failed = true
			}
			if d.HasUsage {
				sawUsage = true
				promptTok, compTok = d.PromptTok, d.CompTok
			}
			// 正式内容与「思考内容」（reasoning_content）都属于模型已生成的输出：
			// 思考型模型（Qwen3.5 等）默认把全部输出放在 reasoning_content，
			// 若只统计 content，会出现「跑了 20 多秒、completion_tokens=0」的假象。
			if d.Content != "" || d.Reasoning != "" {
				chunks++
				if d.Content != "" {
					buf.WriteString(d.Content)
				}
				// 实时吞吐：按 250ms 节流上报，让仪表盘曲线在生成过程中就动起来
				progressAcc++
				if now := time.Now(); now.Sub(lastProgress) > 250*time.Millisecond {
					g.core.Metrics.AddProgress(progressAcc)
					progressTokens += progressAcc
					progressAcc = 0
					lastProgress = now
				}
			}
			switch mode {
			case "sse":
				// 原样透传（含空行与 [DONE]），保证客户端拿到的分片与引擎完全一致
				_, _ = io.WriteString(w, raw+"\n")
			case "ndjson":
				// NDJSON：每行一个裸 JSON 对象（去掉 SSE 的 data: 前缀）
				if trimmedDataLine(raw) {
					payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "data:"))
					if payload != "[DONE]" && (d.Content != "" || d.Reasoning != "" || d.Finished || d.HasUsage) {
						_, _ = io.WriteString(w, payload+"\n")
					}
				}
			case "text":
				if d.Content != "" {
					_, _ = io.WriteString(w, d.Content)
				} else if d.Reasoning != "" {
					_, _ = io.WriteString(w, d.Reasoning)
				}
			}
			if flusher != nil && mode != "ndjson" {
				flusher.Flush()
			}
		})
		if progressAcc > 0 {
			g.core.Metrics.AddProgress(progressAcc)
			progressTokens += progressAcc
			progressAcc = 0
		}
		if !sawUsage {
			// 引擎未上报 usage 时按分片数近似（宁可保守，不虚报）
			compTok = chunks
			promptTok = estimateTokens(payload)
		}
		if compTok > 0 && compTok < progressTokens {
			compTok = progressTokens
		}
		_ = buf
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// acquire 获取并发槽（queue 策略下等待，reject 策略下直接失败）。
func (g *Gateway) acquire(ctx context.Context, timeoutSec int) (func(), bool) {
	g.mu.RLock()
	sem := g.sem
	g.mu.RUnlock()
	if sem == nil {
		return func() {}, true
	}
	cfg := g.core.ConfigSnapshot()
	if cfg.Params["queue_policy"] == "reject" {
		select {
		case sem <- struct{}{}:
			return func() { <-sem }, true
		default:
			return nil, false
		}
	}
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)
	defer timer.Stop()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, true
	case <-ctx.Done():
		return nil, false
	case <-timer.C:
		return nil, false
	}
}

func resolveModel(requested string, models []core.ModelDTO, allowAutoLoad bool) (string, error) {
	requested = strings.TrimSpace(requested)
	known := map[string]bool{}
	for _, m := range models {
		known[m.ID] = true
		known[shortName(m.ID)] = true
	}
	if requested == "" {
		for _, m := range models {
			if m.Loaded {
				return m.ID, nil
			}
		}
		if len(models) > 0 {
			return models[0].ID, nil
		}
		return "", fmt.Errorf("模型库是空的，请到「设置 → 模型根目录」添加存放 .gguf 的目录")
	}
	if known[requested] {
		for _, m := range models {
			if m.ID == requested || shortName(m.ID) == requested {
				return m.ID, nil
			}
		}
	}
	if !allowAutoLoad {
		return "", fmt.Errorf("模型 %s 未加载，且 allow_auto_load 已关闭", requested)
	}
	// 未知模型也转发：由 EnsureLoaded 决定能否按需拉起，拉不起来会被明确拒绝
	return requested, nil
}

func shortName(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

func negotiate(r *http.Request, streamReq bool) string {
	if f := r.URL.Query().Get("format"); f != "" {
		switch strings.ToLower(f) {
		case "text":
			return "text"
		case "ndjson":
			return "ndjson"
		case "sse", "stream":
			return "sse"
		case "json":
			return ""
		}
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	switch {
	case strings.Contains(accept, "application/x-ndjson"):
		return "ndjson"
	case strings.Contains(accept, "text/plain"):
		return "text"
	case strings.Contains(accept, "text/event-stream"):
		return "sse"
	}
	if streamReq {
		return "sse"
	}
	return ""
}

func contentTypeFor(mode string) string {
	switch mode {
	case "ndjson":
		return "application/x-ndjson; charset=utf-8"
	case "text":
		return "text/plain; charset=utf-8"
	default:
		return "text/event-stream; charset=utf-8"
	}
}

// scanSSEWithRaw 在解析分片的同时把原始行交给调用方（用于原样透传）。
func scanSSEWithRaw(r io.Reader, onDelta func(raw string, d delta)) bool {
	return scanSSERaw(r, onDelta)
}
func usageOf(raw []byte) (prompt, comp int) {
	var out struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err == nil && out.Usage != nil {
		return out.Usage.PromptTokens, out.Usage.CompletionTokens
	}
	return estimateFromJSON(raw), 0
}

func estimateFromJSON(raw []byte) int {
	var out struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0
	}
	n := 0
	for _, m := range out.Messages {
		switch v := m.Content.(type) {
		case string:
			n += len([]rune(v))
		}
	}
	return n / 2
}

func estimateTokens(payload map[string]any) int {
	total := 0
	switch msgs := payload["messages"].(type) {
	case []any:
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok {
				if s, ok := mm["content"].(string); ok {
					total += len([]rune(s))
				}
			}
		}
	default:
		if s, ok := payload["prompt"].(string); ok {
			total += len([]rune(s))
		}
	}
	return total / 2
}

func injectSystem(payload map[string]any, system string) {
	msgs, ok := payload["messages"].([]any)
	if !ok {
		return
	}
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok {
			if r, _ := mm["role"].(string); r == "system" {
				return // 请求里已有 system，尊重客户端（位置交给 normalizeMessages 处理）
			}
		}
	}
	payload["messages"] = append([]any{map[string]any{"role": "system", "content": system}}, msgs...)
}

// localizeEngineError 把引擎返回的模板类报错翻译成中文可读提示。
// 保持 OpenAI 错误结构不变，只改写 error.message，并把原文附在末尾便于排查。
// 非模板类报错或非 JSON 响应原样返回，避免破坏客户端解析。
func localizeEngineError(raw []byte) []byte {
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return raw
	}
	errObj, ok := env["error"].(map[string]any)
	if !ok {
		return raw
	}
	msg, ok := errObj["message"].(string)
	if !ok {
		return raw
	}
	zh := templateErrorHint(msg)
	if zh == "" {
		return raw
	}
	errObj["message"] = zh + "\n（引擎原文：" + msg + "）"
	out, err := json.Marshal(env)
	if err != nil {
		return raw
	}
	return out
}

// templateErrorHint 识别常见模板报错特征，返回中文提示；不匹配返回空串。
func templateErrorHint(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "system message must be at the beginning"):
		return "对话模板要求 system 消息必须是第一条，但请求里不是。" +
			"请到「设置 → 局域网网关」确认已开启「规范化消息顺序」；若已开启仍报错，" +
			"说明该模板本身过于严格，可在「参数 → Prompt」换用内置模板或改用 auto 模式。"
	case strings.Contains(lower, "no messages provided") || strings.Contains(lower, "empty messages"):
		return "请求里没有任何消息（messages 为空），模板无法生成提示词。"
	case strings.Contains(lower, "jinja") && (strings.Contains(lower, "expect") || strings.Contains(lower, "parse") || strings.Contains(lower, "syntax")):
		return "自定义 Jinja 模板语法有误（引擎解析失败）。请到「参数 → Prompt」检查 chat_template，" +
			"或换用内置模板。"
	case strings.Contains(lower, "unsupported chat template") || strings.Contains(lower, "unknown chat template"):
		return "当前运行时不支持这个 chat 模板（可能是 llama.cpp 版本较旧或模板名无效）。" +
			"建议升级 llama-server，或在「参数 → Prompt」改用 auto 模式让模型用自带模板。"
	case strings.Contains(lower, "template") && strings.Contains(lower, "not found"):
		return "找不到指定的 chat 模板。请检查 chat_template_mode 与 chat_template 的填法。"
	}
	return ""
}

// isSystemMsg 判断一条消息是否为 system 角色。
func isSystemMsg(m any) bool {
	mm, ok := m.(map[string]any)
	if !ok {
		return false
	}
	r, _ := mm["role"].(string)
	return r == "system"
}

// normalizeMessages 把 messages 里的 system 消息提到首位，其余保持原有相对顺序。
//
// 背景：Llama 3.x / Qwen2.5 / Gemma 等主流 GGUF 自带的 Jinja 模板里有严格校验——
// 第一条消息必须是 system，否则模板引擎直接抛
// 「System message must be at the beginning」导致请求失败。很多手机端客户端会把
// system 放中间或不带，这里统一归一化，兼容「OpenAI 兼容」客户端的松散写法。
//
// 无 system 时不改动（不能凭空造一条，可能改变语义）；只有存在 system 但位置不对时才重排。
// 多个 system 全部提到前面并保持相对顺序，不丢数据。
func normalizeMessages(payload map[string]any) {
	msgs, ok := payload["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return
	}
	// 已经是首位则无需改动（最常见路径，零分配）
	if isSystemMsg(msgs[0]) {
		return
	}
	var systems, others []any
	for _, m := range msgs {
		if isSystemMsg(m) {
			systems = append(systems, m)
		} else {
			others = append(others, m)
		}
	}
	if len(systems) == 0 {
		return // 没有 system，保持原样
	}
	ordered := make([]any, 0, len(msgs))
	ordered = append(ordered, systems...)
	ordered = append(ordered, others...)
	payload["messages"] = ordered
}

// trimmedDataLine 判断该行是否为 `data:` 负载行。
func trimmedDataLine(raw string) bool {
	return strings.HasPrefix(strings.TrimSpace(raw), "data:")
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func boolOf(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, typ, msg, errCode string) {
	writeJSON(w, code, map[string]any{
		"error": map[string]any{"message": msg, "type": typ, "code": errCode, "param": nil},
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
