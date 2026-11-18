// Package api 提供**仅本机**（127.0.0.1）的管理 REST API，并托管前端静态资源。
//
// 只绑回环地址 = 局域网设备在 TCP 层就连不上，因此无需鉴权（v2.0 的取舍）。
// 桌面窗口与浏览器管理台共用同一份接口，前端不需要维护两条数据通路。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"airunner/internal/core"
	"airunner/internal/engine/sdcpp"
	"airunner/internal/nativedlg"
	"airunner/internal/reqlog"
	"airunner/internal/webui"
)

// Server 管理 API 服务。
type Server struct {
	core *core.Core
	log  *slog.Logger
	srv  *http.Server
	ln   net.Listener
}

// New 创建管理 API。
func New(c *core.Core, log *slog.Logger) *Server { return &Server{core: c, log: log} }

// Start 在 127.0.0.1:port 上启动。
func (s *Server) Start(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("管理口监听失败 %s：%w", addr, err)
	}
	s.ln = ln
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Error("管理 API 异常退出", "error", err)
		}
	}()
	s.log.Info("管理 API 已启动", "addr", ln.Addr().String())
	return nil
}

// Stop 关闭服务。
func (s *Server) Stop(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// Addr 监听地址。
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Handler 路由表。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": core.Version})
	})
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.Status())
	})
	mux.HandleFunc("GET /api/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.MetricsSnapshot())
	})

	// 硬件
	mux.HandleFunc("GET /api/v1/hardware", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.core.Hardware(r.URL.Query().Get("rescan") == "1")
		s.respond(w, p, err)
	})
	mux.HandleFunc("POST /api/v1/hardware/rescan", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.core.Hardware(true)
		s.respond(w, p, err)
	})

	// 模型
	mux.HandleFunc("GET /api/v1/models", func(w http.ResponseWriter, r *http.Request) {
		models, err := s.core.Models(r.Context())
		s.respond(w, models, err)
	})
	mux.HandleFunc("POST /api/v1/models/recommend", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model   string `json:"model"`
			Path    string `json:"path"`
			Purpose string `json:"purpose"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		rec, err := s.core.Recommend(req.Model, req.Path, req.Purpose)
		s.respond(w, rec, err)
	})
	mux.HandleFunc("POST /api/v1/models/load", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID            string         `json:"id"`
			ContextLength int            `json:"context_length"`
			Params        map[string]any `json:"params"` // 本次加载的参数覆盖（不写回全局）
		}
		if !s.decode(w, r, &req) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
		defer cancel()
		if err := s.core.LoadModelWith(ctx, req.ID, req.ContextLength, req.Params); err != nil {
			writeErr(w, 400, "load_failed", err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/models/unload", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		if err := s.core.UnloadModel(r.Context(), req.ID); err != nil {
			writeErr(w, 400, "unload_failed", err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/models/active", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
			Path  string `json:"path"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		s.core.SetActiveModel(req.Model, req.Path)
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/gguf/inspect", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		meta, err := s.core.InspectGGUF(req.Path)
		s.respond(w, meta, err)
	})

	// 模型记忆：每个模型自己调好的参数与使用统计
	mux.HandleFunc("GET /api/v1/models/overrides", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"overrides": s.core.ModelOverrides()})
	})
	mux.HandleFunc("GET /api/v1/models/params", func(w http.ResponseWriter, r *http.Request) {
		key := core.ModelKey(r.URL.Query().Get("key"))
		writeJSON(w, 200, map[string]any{
			"key":       key,
			"override":  s.core.ModelParams(key),
			"effective": s.core.EffectiveParams(key),
		})
	})
	mux.HandleFunc("PUT /api/v1/models/params", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key    string         `json:"key"`
			Name   string         `json:"name"`
			Path   string         `json:"path"`
			Values map[string]any `json:"values"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		dto, err := s.core.SetModelParams(req.Key, req.Name, req.Path, req.Values)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error(), "params": dto})
			return
		}
		writeJSON(w, 200, dto)
	})
	mux.HandleFunc("DELETE /api/v1/models/params", func(w http.ResponseWriter, r *http.Request) {
		if err := s.core.ClearModelParams(r.URL.Query().Get("key")); err != nil {
			writeErr(w, 400, "error", err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	// 参数
	mux.HandleFunc("GET /api/v1/params", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.Params())
	})
	mux.HandleFunc("PUT /api/v1/params", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Values map[string]any `json:"values"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		dto, err := s.core.SetParams(req.Values)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error(), "params": dto})
			return
		}
		writeJSON(w, 200, dto)
	})
	mux.HandleFunc("GET /api/v1/schema", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.JSONSchema())
	})
	mux.HandleFunc("GET /api/v1/presets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.Params().Presets)
	})
	mux.HandleFunc("POST /api/v1/presets/apply", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		dto, err := s.core.ApplyPreset(req.Name)
		if err != nil {
			writeErr(w, 400, "preset_failed", err.Error())
			return
		}
		writeJSON(w, 200, dto)
	})
	mux.HandleFunc("POST /api/v1/presets", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		list, err := s.core.SavePreset(req.Name)
		s.respond(w, list, err)
	})
	mux.HandleFunc("DELETE /api/v1/presets", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.core.DeletePreset(r.URL.Query().Get("name"))
		s.respond(w, list, err)
	})

	// 日志
	mux.HandleFunc("GET /api/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		limit := intQuery(r, "limit", 300)
		writeJSON(w, 200, s.core.Logs(limit, r.URL.Query().Get("level")))
	})
	mux.HandleFunc("DELETE /api/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		s.core.ClearLogs()
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	// 请求日志
	mux.HandleFunc("GET /api/v1/requests", func(w http.ResponseWriter, r *http.Request) {
		f := reqlog.Filter{
			Model:   r.URL.Query().Get("model"),
			Keyword: r.URL.Query().Get("keyword"),
			Status:  intQuery(r, "status", 0),
			Limit:   intQuery(r, "limit", 200),
			FromID:  int64(intQuery(r, "from_id", 0)),
		}
		writeJSON(w, 200, s.core.Requests(f))
	})
	mux.HandleFunc("GET /api/v1/requests/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.RequestStats())
	})
	mux.HandleFunc("DELETE /api/v1/requests", func(w http.ResponseWriter, r *http.Request) {
		s.core.ClearRequests()
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	// 网络接入
	mux.HandleFunc("GET /api/v1/network", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.Network())
	})

	// 引擎
	mux.HandleFunc("GET /api/v1/engine", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.EngineInfo())
	})
	mux.HandleFunc("POST /api/v1/engine/rescan", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		writeJSON(w, 200, s.core.RescanEngine(ctx))
	})
	mux.HandleFunc("GET /api/v1/engine/detail", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.EngineDetail())
	})
	mux.HandleFunc("POST /api/v1/engine/runtimes/detect", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"runtimes": s.core.DetectRuntimes()})
	})

	// 一键下载安装 llama-server（官方 release → 运行时目录）
	mux.HandleFunc("GET /api/v1/engine/install", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"state":    s.core.EngineInstallState(),
			"variants": s.core.EngineInstallVariants(),
		})
	})
	mux.HandleFunc("POST /api/v1/engine/install", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variant string `json:"variant"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		if err := s.core.InstallEngine(req.Variant); err != nil {
			writeErr(w, 409, "install_busy", err.Error())
			return
		}
		writeJSON(w, 200, s.core.EngineInstallState())
	})

	// 本机原生选择框：对话框开在「跑本程序的那台机器」上。
	// 管理口只绑 127.0.0.1，局域网设备根本连不上，所以不存在远程弹窗的问题。
	mux.HandleFunc("POST /api/v1/dialog/pick-folder", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Title   string `json:"title"`
			Initial string `json:"initial"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		p, err := nativedlg.Folder(req.Title, req.Initial)
		s.respondDialog(w, p, err)
	})
	mux.HandleFunc("POST /api/v1/dialog/pick-file", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Title   string `json:"title"`
			Initial string `json:"initial"`
			Filter  string `json:"filter"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		p, err := nativedlg.File(req.Title, req.Initial, req.Filter)
		s.respondDialog(w, p, err)
	})

	// 模型根目录（gguf 库）
	mux.HandleFunc("GET /api/v1/model-roots", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"roots": s.core.ModelRoots()})
	})
	mux.HandleFunc("POST /api/v1/model-roots", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		roots, err := s.core.AddModelRoot(req.Path)
		s.respond(w, map[string]any{"roots": roots}, err)
	})
	mux.HandleFunc("DELETE /api/v1/model-roots", func(w http.ResponseWriter, r *http.Request) {
		roots, err := s.core.RemoveModelRoot(r.URL.Query().Get("path"))
		s.respond(w, map[string]any{"roots": roots}, err)
	})
	mux.HandleFunc("POST /api/v1/test-chat", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()
		start := time.Now()
		reply, err := s.core.TestChat(ctx, req.Model, req.Prompt)
		if err != nil {
			writeErr(w, 400, "chat_failed", err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"reply": reply, "duration_ms": time.Since(start).Milliseconds(),
		})
	})

	// 网关
	mux.HandleFunc("POST /api/v1/gateway/start", func(w http.ResponseWriter, r *http.Request) {
		if err := s.core.GatewayStart(); err != nil {
			writeErr(w, 400, "gateway_error", err.Error())
			return
		}
		writeJSON(w, 200, s.core.Status())
	})
	mux.HandleFunc("POST /api/v1/gateway/stop", func(w http.ResponseWriter, r *http.Request) {
		if err := s.core.GatewayStop(); err != nil {
			writeErr(w, 400, "gateway_error", err.Error())
			return
		}
		writeJSON(w, 200, s.core.Status())
	})

	// 配置
	mux.HandleFunc("GET /api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.ConfigSnapshot())
	})
	mux.HandleFunc("PUT /api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Patch map[string]any `json:"patch"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		cfg, err := s.core.UpdateConfig(req.Patch)
		if err != nil {
			writeErr(w, 400, "config_error", err.Error())
			return
		}
		writeJSON(w, 200, cfg)
	})

	// 生图（stable-diffusion.cpp）
	mux.HandleFunc("GET /api/v1/image/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.core.ImageStatus(r.Context()))
	})
	mux.HandleFunc("GET /api/v1/image/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"models": s.core.ImageModels()})
	})
	mux.HandleFunc("POST /api/v1/image/start", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		if err := s.core.ImageStart(r.Context(), req.Model); err != nil {
			writeErr(w, 400, "image_error", err.Error())
			return
		}
		writeJSON(w, 200, s.core.ImageStatus(r.Context()))
	})
	mux.HandleFunc("POST /api/v1/image/stop", func(w http.ResponseWriter, r *http.Request) {
		if err := s.core.ImageStop(r.Context()); err != nil {
			writeErr(w, 400, "image_error", err.Error())
			return
		}
		writeJSON(w, 200, s.core.ImageStatus(r.Context()))
	})
	// 前端生图页直接调用：把 sd-server 的响应转成 JSON（图片 base64 在 data[].b64_json）
	mux.HandleFunc("POST /api/v1/image/generate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Prompt         string  `json:"prompt"`
			NegativePrompt string  `json:"negative_prompt"`
			Size           string  `json:"size"`
			Steps          int     `json:"steps"`
			CfgScale       float64 `json:"cfg_scale"`
			Seed           int64   `json:"seed"`
			Sampler        string  `json:"sampler"`
		}
		if !s.decode(w, r, &req) {
			return
		}
		resp, err := s.core.ImageGenerate(r.Context(), sdcpp.GenerateRequest{
			Prompt: req.Prompt, NegativePrompt: req.NegativePrompt, Size: req.Size,
			Steps: req.Steps, CfgScale: req.CfgScale, Seed: req.Seed, Sampler: req.Sampler,
		})
		if err != nil {
			writeErr(w, 400, "image_error", err.Error())
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
	})

	// 静态资源（同一份前端）
	mux.Handle("GET /", s.staticHandler())
	return cors(mux)
}

func (s *Server) staticHandler() http.Handler {
	root := webui.FS()
	fileServer := http.FileServerFS(root)
	index := webui.IndexHTML()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || p == "admin" || p == "admin/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(index)
			return
		}
		if _, err := fs.Stat(root, p); err != nil {
			// SPA 回退：未命中的路径一律交给前端路由
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(index)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeErr(w, 400, "invalid_request", "读取请求体失败")
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeErr(w, 400, "invalid_request", "请求体不是合法 JSON："+err.Error())
		return false
	}
	return true
}

func (s *Server) respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeErr(w, 400, "error", err.Error())
		return
	}
	writeJSON(w, 200, v)
}

// respondDialog 原生选择框的返回值：取消不是错误，用 canceled 标记即可，
// 界面据此保持原输入不变；真正的失败（平台不支持 / 打开失败）才报错。
func (s *Server) respondDialog(w http.ResponseWriter, path string, err error) {
	switch {
	case err == nil:
		writeJSON(w, 200, map[string]any{"path": path, "canceled": false})
	case errors.Is(err, nativedlg.ErrCanceled):
		writeJSON(w, 200, map[string]any{"path": "", "canceled": true})
	case errors.Is(err, nativedlg.ErrUnsupported):
		writeJSON(w, 501, map[string]any{"error": err.Error(), "unsupported": true})
	default:
		writeErr(w, 500, "dialog_error", err.Error())
	}
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 管理口只绑 127.0.0.1，因此放开跨域是安全的（Wails 窗口与浏览器都需要）
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func intQuery(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, typ, msg string) {
	writeJSON(w, code, map[string]any{"error": msg, "type": typ})
}
