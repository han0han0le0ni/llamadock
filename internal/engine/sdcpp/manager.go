// Package sdcpp 管理 stable-diffusion.cpp 的 sd-server 子进程。
//
// 与文本引擎（llama.cpp）完全独立：两者是不同进程、不同端口、不同模型格式。
// 本包只负责三件事：
//  1. 探测 / 拉起 sd-server.exe（OpenAI 兼容的 /v1/images/generations）
//  2. 扫描生图模型目录（gguf / safetensors / ckpt）
//  3. 转发生图请求并暴露状态给上层
//
// 之所以直接对接 sd-server 自带的 OpenAI 兼容接口，而不自己实现扩散推理：
// sd.cpp 上游已经把模型加载、recipe、采样、异步队列、LoRA 等全部封装好了，
// 我们只需复用并把它纳入 LlamaDock 的统一生命周期（启停 / 日志 / 显存避让）。
package sdcpp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config 生图引擎配置（由 core 从 config.ImageConfig 翻译而来）。
type Config struct {
	ExePath      string
	ModelDir     string
	ModelPath    string
	ExtraArgs    string
	Port         int
	Backend      string
	OffloadToCPU bool
	LogDir       string
	OnLog        func(line string)
	OnExit       func(code int, err error)
}

// Manager 负责 sd-server 子进程的完整生命周期。
type Manager struct {
	mu  sync.RWMutex
	cfg Config

	cmd       *exec.Cmd
	proc      *os.Process
	port      int
	modelPath string
	args      []string
	exePath   string
	startedAt time.Time
	running   bool
	exited    chan struct{}
	lastErr   string
	logTail   []string
	logFile   *os.File

	hc       *http.Client
	progress GenProgress
}

// GenProgress 一次生图任务的实时进度（从 sd-server 的 verbose 进度条日志解析）。
// sd-server 的 OpenAI 接口不暴露进度，但它会在采样时向 stderr/stdout 打印
// 形如 "45/80 - 6.28it/s - ETA 00:35" 的进度条（用 \r 刷新），这里解析出来给前端轮询展示。
type GenProgress struct {
	Step      int     `json:"step"`       // 当前采样步
	Total     int     `json:"total"`      // 总步数
	Speed     float64 `json:"speed"`      // 速度数值
	SpeedUnit string  `json:"speed_unit"` // "it/s" 或 "s/it"
	ETA       string  `json:"eta"`        // 剩余时间 MM:SS，空表示未知
	Percent   int     `json:"percent"`    // 0-100
	Stage     string  `json:"stage,omitempty"` // "sampling" / "decoding" 等
}

// Status 进程状态快照（JSON 直接给前端）。
type Status struct {
	Running    bool        `json:"running"`
	PID        int         `json:"pid"`
	Port       int         `json:"port"`
	Endpoint   string      `json:"endpoint"`
	Model      string      `json:"model"`
	ModelPath  string      `json:"model_path"`
	BinPath    string      `json:"bin_path"`
	UptimeSec  int64       `json:"uptime_sec"`
	RSSMB      int         `json:"rss_mb"`
	CmdLine    string      `json:"cmd_line"`
	Args       []string    `json:"args"`
	LogPath    string      `json:"log_path"`
	LogTail    []string    `json:"log_tail,omitempty"`
	LastError  string      `json:"last_error,omitempty"`
	Detected   string      `json:"detected"`    // 探测到的 sd-server.exe 路径
	ModelsRoot string      `json:"models_root"` // 当前模型目录
	Progress   GenProgress `json:"progress"`    // 当前生图进度（无任务时为全 0）
}

// New 创建管理器。
func New(cfg Config) *Manager {
	if cfg.Port <= 0 {
		cfg.Port = 1234
	}
	return &Manager{cfg: cfg, exited: make(chan struct{}), hc: &http.Client{Timeout: 0}}
}

// SetConfig 热更新配置（设置页改动后调用）。
func (m *Manager) SetConfig(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cfg.Port <= 0 {
		cfg.Port = 1234
	}
	cfg.ExePath = strings.TrimSpace(cfg.ExePath)
	m.cfg = cfg
}

// Config 返回配置快照。
func (m *Manager) Config() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// ResolveExe 解析出要使用的 sd-server.exe：显式路径 → 常见目录 → PATH。
func (m *Manager) ResolveExe() (string, error) {
	m.mu.RLock()
	exe := m.cfg.ExePath
	m.mu.RUnlock()
	if exe != "" {
		if st, err := os.Stat(exe); err == nil && !st.IsDir() {
			return exe, nil
		}
		return "", fmt.Errorf("指定的 sd-server.exe 不存在：%s", exe)
	}
	if found := Detect(); found != "" {
		return found, nil
	}
	return "", fmt.Errorf("没找到 sd-server.exe。请到「设置 → 生图引擎」指定它的路径，" +
		"或把 stable-diffusion.cpp 的 Windows CUDA 压缩包解压到程序目录下的 runtime\\sd.cpp\\（本程序会自动找到）")
}

// Detect 在常见位置探测 sd-server.exe。
func Detect() string {
	var dirs []string
	if exe, _ := os.Executable(); exe != "" {
		base := filepath.Dir(exe)
		dirs = append(dirs,
			filepath.Join(base, "runtime", "sd.cpp"),
			filepath.Join(base, "runtime", "stable-diffusion.cpp"),
			filepath.Join(base, "runtime", "sd"),
			base,
		)
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs,
			filepath.Join(wd, "runtime", "sd.cpp"),
			filepath.Join(wd, "runtime", "stable-diffusion.cpp"),
			wd,
		)
	}
	for _, d := range dirs {
		for _, name := range []string{"sd-server.exe", "sd-server", "sd-server-bin.exe"} {
			p := filepath.Join(d, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	if p, err := exec.LookPath("sd-server"); err == nil {
		return p
	}
	return ""
}

// Status 返回当前状态快照。
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := Status{
		Port:       m.port,
		ModelPath:  m.modelPath,
		BinPath:    m.exePath,
		Args:       append([]string{}, m.args...),
		LogTail:    tail(m.logTail, 12),
		LastError:  m.lastErr,
		Detected:   Detect(),
		ModelsRoot: m.cfg.ModelDir,
		Progress:   m.progress,
	}
	if st.Port == 0 {
		st.Port = m.cfg.Port
	}
	if m.modelPath != "" {
		st.Model = filepath.Base(m.modelPath)
	}
	if st.BinPath == "" {
		st.BinPath = st.Detected
	}
	if m.cfg.LogDir != "" {
		st.LogPath = filepath.Join(m.cfg.LogDir, "sd-server.log")
	}
	if m.running {
		st.Running = true
		st.PID = m.pidLocked()
		st.UptimeSec = int64(time.Since(m.startedAt).Seconds())
		st.Endpoint = fmt.Sprintf("http://127.0.0.1:%d", m.port)
		st.CmdLine = commandLine(m.exePath, st.Args)
	}
	return st
}

func (m *Manager) pidLocked() int {
	if m.proc == nil {
		return 0
	}
	return m.proc.Pid
}

// Running 是否在运行。
func (m *Manager) Running() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// Endpoint 当前引擎基址。
func (m *Manager) Endpoint() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.port
	if p == 0 {
		p = m.cfg.Port
	}
	return fmt.Sprintf("http://127.0.0.1:%d", p)
}

// Start 拉起 sd-server 子进程并等待 /v1/models 就绪。
func (m *Manager) Start(ctx context.Context, modelPath string) error {
	m.mu.RLock()
	curModel := m.modelPath
	m.mu.RUnlock()
	if m.Running() && (modelPath == "" || samePath(curModel, modelPath)) {
		return nil
	}
	if m.Running() {
		if err := m.Stop(ctx); err != nil {
			return fmt.Errorf("切换生图模型前停止旧进程失败：%w", err)
		}
	}

	exe, err := m.ResolveExe()
	if err != nil {
		return err
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if modelPath == "" {
		modelPath = cfg.ModelPath
	}

	args := []string{}
	if modelPath != "" {
		// 单文件 checkpoint（SD1.5 / SDXL / SD3 单文件）用 -m；
		// Flux / Z-Image 等多组件模型由 ExtraArgs 提供 --diffusion-model / --llm / --vae。
		ext := strings.ToLower(filepath.Ext(modelPath))
		if ext == ".gguf" || ext == ".safetensors" || ext == ".ckpt" {
			args = append(args, "-m", modelPath)
		}
	}
	args = append(args,
		"--listen-ip", "127.0.0.1",
		"--listen-port", strconv.Itoa(cfg.Port),
	)
	if cfg.ModelDir != "" {
		args = append(args, "--model-dir", cfg.ModelDir)
	}
	if cfg.OffloadToCPU {
		args = append(args, "--offload-to-cpu")
	}
	// 用 verbose 日志级别：sd-server 会在采样时逐 step 打印进度条（N/M - Xit/s - ETA MM:SS），
	// 我们解析这些行把实时进度暴露给前端（OpenAI 接口本身不返回进度）。
	// 放在 ExtraArgs 之前，允许用户通过 extra_args 里的 --log-level 覆盖。
	if !hasLogLevelFlag(cfg.ExtraArgs) {
		args = append(args, "--log-level", "verbose")
	}
	if extra := strings.TrimSpace(cfg.ExtraArgs); extra != "" {
		args = append(args, splitArgs(extra)...)
	}

	if portInUse(cfg.Port) {
		return fmt.Errorf("生图端口 %d 已被占用，请到「设置 → 生图引擎」换一个端口", cfg.Port)
	}

	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	setProcAttr(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 sd-server 失败：%w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.proc = cmd.Process
	m.port = cfg.Port
	m.modelPath = modelPath
	m.args = args
	m.exePath = exe
	m.startedAt = time.Now()
	m.running = true
	m.lastErr = ""
	m.logTail = nil
	m.progress = GenProgress{}
	m.exited = make(chan struct{})
	logPath := ""
	if cfg.LogDir != "" {
		logPath = filepath.Join(cfg.LogDir, "sd-server.log")
	}
	m.mu.Unlock()

	if logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			m.mu.Lock()
			m.logFile = f
			m.mu.Unlock()
		}
	}

	go m.pump(stdout)
	go m.pump(stderr)
	go m.waitLoop(cmd)

	if err := m.waitReady(ctx, 300*time.Second); err != nil {
		_ = m.Stop(context.Background())
		return err
	}
	return nil
}

func (m *Manager) pump(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 32*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		m.mu.Lock()
		m.logTail = append(m.logTail, line)
		if len(m.logTail) > 80 {
			m.logTail = m.logTail[len(m.logTail)-80:]
		}
		// 解析 verbose 进度条行，更新当前生图进度（取一行里最后一次刷新）。
		if p, ok := parseProgressLine(line); ok {
			m.progress = p
		}
		onLog := m.cfg.OnLog
		f := m.logFile
		m.mu.Unlock()
		if f != nil {
			_, _ = f.WriteString(line + "\n")
		}
		if onLog != nil {
			onLog(line)
		}
	}
}

func (m *Manager) waitLoop(cmd *exec.Cmd) {
	err := cmd.Wait()
	code := cmd.ProcessState.ExitCode()
	m.mu.Lock()
	m.running = false
	m.proc = nil
	m.cmd = nil
	if err != nil {
		m.lastErr = err.Error()
	}
	if m.logFile != nil {
		_ = m.logFile.Close()
		m.logFile = nil
	}
	onExit := m.cfg.OnExit
	exited := m.exited
	m.mu.Unlock()
	close(exited)
	if onExit != nil {
		onExit(code, err)
	}
}

func (m *Manager) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.exited:
			return m.failure("sd-server 启动后立即退出")
		case <-time.After(800 * time.Millisecond):
		}
		body, code, err := m.get(ctx, "/v1/models")
		if err == nil && code == http.StatusOK {
			// 服务已就绪（/v1/models 空数组也代表进程已起来，只是尚未加载模型）
			_ = body
			return nil
		}
	}
	return m.failure("等待 sd-server 就绪超时")
}

func (m *Manager) failure(reason string) error {
	m.mu.RLock()
	t := tail(m.logTail, 6)
	m.mu.RUnlock()
	if len(t) == 0 {
		return fmt.Errorf("%s（无子进程日志输出）", reason)
	}
	return fmt.Errorf("%s：%s", reason, strings.Join(t, " ｜ "))
}

// Stop 停止子进程。
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	proc := m.proc
	exited := m.exited
	running := m.running
	m.mu.Unlock()
	if !running || proc == nil {
		return nil
	}
	killTree(proc.Pid)
	select {
	case <-exited:
	case <-time.After(8 * time.Second):
		_ = proc.Kill()
	}
	return nil
}

// ResetProgress 清空当前进度（发起新一次生图、或引擎重启时调用）。
func (m *Manager) ResetProgress() {
	m.mu.Lock()
	m.progress = GenProgress{}
	m.mu.Unlock()
}

// Generate 转发 OpenAI 兼容的生图请求到 sd-server。
// 请求体由调用方（网关）组装好，这里只做纯转发，响应原样回传（图片是 base64）。
func (m *Manager) Generate(ctx context.Context, path string, body []byte, hdr http.Header) (*http.Response, error) {
	if !m.Running() {
		return nil, fmt.Errorf("生图引擎未运行")
	}
	m.ResetProgress()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if hdr != nil {
		if v := hdr.Get("Accept"); v != "" {
			req.Header.Set("Accept", v)
		}
	}
	return m.hc.Do(req)
}

// Health 轻量探测（用于状态页）。
func (m *Manager) Health(ctx context.Context) (bool, string) {
	if !m.Running() {
		return false, "生图引擎未启动"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, m.Endpoint()+"/v1/models", nil)
	resp, err := c.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return true, ""
}

func (m *Manager) get(ctx context.Context, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Endpoint()+path, nil)
	if err != nil {
		return nil, 0, err
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return b, resp.StatusCode, err
}

// --- 模型扫描 ---

// ImageModel 一个可用的生图模型文件。
type ImageModel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	SizeMB int    `json:"size_mb,omitempty"`
	Kind   string `json:"kind,omitempty"` // checkpoint / lora / vae
}

// ScanModels 递归扫描生图模型目录（最多 3 层），返回 gguf/safetensors/ckpt 文件。
func ScanModels(root string) []ImageModel {
	out := []ImageModel{}
	if strings.TrimSpace(root) == "" {
		return out
	}
	seen := map[string]bool{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".gguf" && ext != ".safetensors" && ext != ".ckpt" && ext != ".sft" {
			return nil
		}
		abs, _ := filepath.Abs(path)
		if seen[abs] {
			return nil
		}
		seen[abs] = true
		lower := strings.ToLower(path)
		kind := "checkpoint"
		if strings.Contains(lower, "lora") {
			kind = "lora"
		} else if strings.Contains(lower, "vae") {
			kind = "vae"
		}
		out = append(out, ImageModel{
			ID:     abs,
			Name:   filepath.Base(path),
			Path:   abs,
			SizeMB: int(info.Size() / 1048576),
			Kind:   kind,
		})
		return nil
	})
	return out
}

// --- 工具 ---

// commandLine 生成可读的命令行（用于状态页展示，含引号便于复制）。
func commandLine(exe string, args []string) string {
	parts := append([]string{exe}, args...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \t") && !strings.HasPrefix(p, `"`) {
			parts[i] = `"` + p + `"`
		}
	}
	return strings.Join(parts, " ")
}

func portInUse(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// splitArgs 把一整串额外参数按空白拆开（支持双引号包裹带空格的路径）。
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case (r == ' ' || r == '\t' || r == '\r' || r == '\n') && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// hasLogLevelFlag 判断额外参数里是否已指定了日志级别（避免重复添加 --log-level）。
func hasLogLevelFlag(extra string) bool {
	toks := splitArgs(extra)
	for _, t := range toks {
		if t == "--log-level" || t == "-v" || t == "--verbose" || strings.HasPrefix(t, "--log-level=") {
			return true
		}
	}
	return false
}

// progressRe 匹配 sd-server verbose 进度条，例如：
//
//	"45/80 -   6.28it/s - ETA 00:35"
//	"  1/80 -  25.45s/it - ETA 33:30"
//	" 80/80 -   6.04it/s"
//	"| 12/20 - 3.2it/s - ETA 00:05 |====>         |"
var progressRe = regexp.MustCompile(`(\d+)\s*/\s*(\d+)\s*-\s*([0-9.]+)\s*(it/s|s/it)(?:\s*-\s*ETA\s*([0-9:]+))?`)

// parseProgressLine 从一行日志里提取最新的一条进度。
// sd-server 用 \r 刷新进度条，Scanner 按 \n 分割，所以一行里可能有多次刷新的片段；
// 用 FindAll 取最后一条即最新进度。找不到返回 false。
func parseProgressLine(line string) (GenProgress, bool) {
	ms := progressRe.FindAllStringSubmatch(line, -1)
	if len(ms) == 0 {
		return GenProgress{}, false
	}
	m := ms[len(ms)-1]
	step, _ := strconv.Atoi(strings.TrimSpace(m[1]))
	total, _ := strconv.Atoi(strings.TrimSpace(m[2]))
	speed, _ := strconv.ParseFloat(strings.TrimSpace(m[3]), 64)
	if total <= 0 || step < 0 {
		return GenProgress{}, false
	}
	pct := 0
	if total > 0 {
		pct = step * 100 / total
		if pct > 100 {
			pct = 100
		}
	}
	return GenProgress{
		Step: step, Total: total, Speed: speed,
		SpeedUnit: m[4], ETA: strings.TrimSpace(m[5]),
		Percent: pct, Stage: "sampling",
	}, true
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func tail(lines []string, n int) []string {
	if len(lines) <= n {
		return append([]string{}, lines...)
	}
	return append([]string{}, lines[len(lines)-n:]...)
}

// GenerateRequest 前端/网关提交的生图请求（OpenAI 兼容 + sd.cpp 常用扩展字段）。
// 网关会把它翻译成 sd-server OpenAI 接口能懂的格式（扩展参数塞进 sd_cpp_extra_args）。
type GenerateRequest struct {
	Prompt         string  `json:"prompt"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	Size           string  `json:"size,omitempty"` // WIDTHxHEIGHT
	N              int     `json:"n,omitempty"`
	Steps          int     `json:"steps,omitempty"`
	CfgScale       float64 `json:"cfg_scale,omitempty"`
	Seed           int64   `json:"seed,omitempty"`
	Sampler        string  `json:"sampler,omitempty"`
	OutputFormat   string  `json:"output_format,omitempty"`
	// 图生图（img2img）：底图与重绘强度。底图支持 raw base64 或 data URL。
	// 为空时走纯文生图；非空时经 sd_cpp_extra_args 下发 init_image + strength。
	InitImage string  `json:"init_image,omitempty"`
	Strength  float64 `json:"strength,omitempty"` // 0~1，越大越"推倒重来"；0 表示默认 0.75
}

// BuildOpenAIBody 把扩展请求转成 sd-server /v1/images/generations 的请求体。
func BuildOpenAIBody(req GenerateRequest, defW, defH, defSteps int, defCfg float64) ([]byte, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("提示词不能为空")
	}
	w, h := defW, defH
	if req.Size != "" {
		if nw, nh, err := parseSize(req.Size); err == nil {
			w, h = nw, nh
		}
	}
	if w <= 0 || h <= 0 {
		w, h = 512, 512
	}
	steps := req.Steps
	if steps <= 0 {
		steps = defSteps
	}
	cfg := req.CfgScale
	if cfg <= 0 {
		cfg = defCfg
	}

	extra := map[string]any{
		"negative_prompt": req.NegativePrompt,
		"width":           w,
		"height":          h,
	}
	sp := map[string]any{"sample_steps": steps}
	if req.Sampler != "" {
		sp["sample_method"] = req.Sampler
	}
	sp["guidance"] = map[string]any{"txt_cfg": cfg}
	extra["sample_params"] = sp
	if req.Seed != 0 {
		extra["seed"] = req.Seed
	}
	// 图生图：init_image（base64/data URL）+ strength 走 native 字段下发。
	if img := strings.TrimSpace(req.InitImage); img != "" {
		extra["init_image"] = img
		if req.Strength > 0 {
			extra["strength"] = req.Strength
		}
	}
	extraJSON, _ := json.Marshal(extra)
	prompt = prompt + " <sd_cpp_extra_args>" + string(extraJSON) + "</sd_cpp_extra_args>"

	n := req.N
	if n <= 0 {
		n = 1
	}
	fmtOut := req.OutputFormat
	if fmtOut == "" {
		fmtOut = "png"
	}
	body := map[string]any{
		"prompt":        prompt,
		"n":             n,
		"size":          fmt.Sprintf("%dx%d", w, h),
		"output_format": fmtOut,
	}
	return json.Marshal(body)
}

func parseSize(s string) (int, int, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("尺寸格式应为 宽x高，例如 512x512")
	}
	w, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if e1 != nil || e2 != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("尺寸格式应为 宽x高，例如 512x512")
	}
	return w, h, nil
}
