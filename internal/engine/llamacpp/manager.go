package llamacpp

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
	"strconv"
	"strings"
	"sync"
	"time"

	"airunner/internal/engine"
)

// Config 自管引擎配置。
type Config struct {
	ExePath     string   // 用户显式指定的 llama-server.exe
	RuntimeDirs []string // 额外的运行时搜索目录
	ModelRoots  []string // 模型扫描根目录
	Host        string   // 只绑回环，局域网不可见（设计方案 §5.1）
	PortMin     int
	PortMax     int
	LogDir      string
	PreferGPU   bool // 依据硬件自检结果选择 CUDA / Vulkan / CPU 变体
	OnLog       func(line string)
	OnExit      func(code int, err error)
}

// Manager 负责 llama-server 子进程的完整生命周期。
type Manager struct {
	mu  sync.RWMutex
	cfg Config

	rt      Runtime
	runtime []Runtime

	cmd       *exec.Cmd
	proc      *os.Process
	port      int
	modelPath string
	alias     string
	args      []string
	variant   string
	exePath   string
	startedAt time.Time
	running   bool
	exited    chan struct{}
	lastExit  int
	lastErr   string
	logTail   []string
	logFile   *os.File

	meta *metaCache
	hc   *http.Client
}

// New 创建管理器。
func New(cfg Config) *Manager {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.PortMin <= 0 {
		cfg.PortMin = 10001
	}
	if cfg.PortMax <= 0 {
		cfg.PortMax = 10099
	}
	m := &Manager{cfg: cfg, meta: newMetaCache(), exited: make(chan struct{})}
	m.hc = &http.Client{Timeout: 0}
	return m
}

// Config 返回当前配置的快照（切片已拷贝，调用方可安全持有）。
func (m *Manager) Config() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg := m.cfg
	cfg.RuntimeDirs = append([]string{}, cfg.RuntimeDirs...)
	cfg.ModelRoots = append([]string{}, cfg.ModelRoots...)
	return cfg
}

// SetConfig 热更新配置（设置页改动后调用）。
func (m *Manager) SetConfig(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.PortMin <= 0 {
		cfg.PortMin = 10001
	}
	if cfg.PortMax <= 0 {
		cfg.PortMax = 10099
	}
	// 保留运行期字段
	cfg.ExePath = strings.TrimSpace(cfg.ExePath)
	m.cfg = cfg
}

// Kind 实现 engine.Adapter。
func (m *Manager) Kind() engine.Kind { return engine.KindLlamaCpp }

// Schedulable 自管引擎：显存与上下文由本程序决定。
func (m *Manager) Schedulable() bool { return true }

// Endpoint 当前基址；未运行时返回预期端口上的地址。
func (m *Manager) Endpoint() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	port := m.port
	if port == 0 {
		port = m.cfg.PortMin
	}
	return fmt.Sprintf("http://%s:%d", m.cfg.Host, port)
}

// Detect 重新探测可用运行时。
func (m *Manager) Detect() []Runtime {
	dirs := append([]string{}, m.cfg.RuntimeDirs...)
	if m.cfg.ExePath != "" {
		dirs = append([]string{filepath.Dir(m.cfg.ExePath)}, dirs...)
	}
	rts := DetectRuntimes(dirs)
	m.mu.Lock()
	m.runtime = rts
	if len(rts) > 0 {
		if rt, ok := PickBest(rts, m.cfg.PreferGPU); ok {
			m.rt = rt
		}
	}
	m.mu.Unlock()
	return rts
}

// Runtimes 返回上次探测结果。
func (m *Manager) Runtimes() []Runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Runtime{}, m.runtime...)
}

// SelectedRuntime 当前选中的运行时。
func (m *Manager) SelectedRuntime() Runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rt
}

// resolveRuntime 解析出要使用的 llama-server。
func (m *Manager) resolveRuntime() (Runtime, error) {
	m.mu.RLock()
	exe := m.cfg.ExePath
	rt := m.rt
	preferGPU := m.cfg.PreferGPU
	dirs := append([]string{}, m.cfg.RuntimeDirs...)
	m.mu.RUnlock()

	if exe != "" {
		if st, err := os.Stat(exe); err == nil && !st.IsDir() {
			return newRuntime(exe, "custom", nil, ""), nil
		}
		return Runtime{}, fmt.Errorf("指定的 llama-server.exe 不存在：%s", exe)
	}
	if rt.Exe != "" {
		return rt, nil
	}
	rts := DetectRuntimes(dirs)
	if len(rts) == 0 {
		return Runtime{}, fmt.Errorf("没找到 llama-server.exe。请到「设置 → 推理引擎」指定它的路径，" +
			"或把官方 llama.cpp 的 Windows 压缩包解压到程序目录下的 runtime\\llama.cpp\\（本程序会自动找到），" +
			"或把它所在的目录加进系统 PATH")
	}
	best, _ := PickBest(rts, preferGPU)
	m.mu.Lock()
	m.runtime = rts
	m.rt = best
	m.mu.Unlock()
	return best, nil
}

// Status 进程状态。
type Status struct {
	Running   bool     `json:"running"`
	PID       int      `json:"pid"`
	Port      int      `json:"port"`
	Endpoint  string   `json:"endpoint"`
	Model     string   `json:"model"`
	ModelPath string   `json:"model_path"`
	Alias     string   `json:"alias"`
	UptimeSec int64    `json:"uptime_sec"`
	RSSMB     int      `json:"rss_mb"`
	BinPath   string   `json:"bin_path"`
	Variant   string   `json:"variant"`
	CmdLine   string   `json:"cmd_line"`
	Args      []string `json:"args"`
	LogPath   string   `json:"log_path"`
	LogTail   []string `json:"log_tail,omitempty"`
}

// Status 返回当前状态快照。
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := Status{
		Running:   m.running,
		Port:      m.port,
		Model:     m.alias,
		ModelPath: m.modelPath,
		Alias:     m.alias,
		BinPath:   m.exePath,
		Variant:   m.variant,
		Args:      append([]string{}, m.args...),
		LogPath:   m.logPathLocked(),
		LogTail:   tail(m.logTail, 12),
	}
	if m.running {
		st.PID = m.pidLocked()
		st.UptimeSec = int64(time.Since(m.startedAt).Seconds())
		st.RSSMB = processRSSMB(st.PID)
		st.Endpoint = fmt.Sprintf("http://%s:%d", m.cfg.Host, m.port)
		st.CmdLine = CommandLine(m.exePath, m.cfg.Host, m.port, m.args)
	} else {
		st.Endpoint = m.endpointLocked()
	}
	return st
}

func (m *Manager) pidLocked() int {
	if m.proc == nil {
		return 0
	}
	return m.proc.Pid
}

func (m *Manager) endpointLocked() string {
	port := m.port
	if port == 0 {
		port = m.cfg.PortMin
	}
	return fmt.Sprintf("http://%s:%d", m.cfg.Host, port)
}

func (m *Manager) logPathLocked() string {
	if m.cfg.LogDir == "" {
		return ""
	}
	return filepath.Join(m.cfg.LogDir, "llama-server.log")
}

// Health 实现 engine.Adapter。
func (m *Manager) Health(ctx context.Context) engine.Health {
	st := m.Status()
	h := engine.Health{
		Kind:        string(engine.KindLlamaCpp),
		Endpoint:    st.Endpoint,
		Schedulable: true,
		Running:     st.Running,
		PID:         st.PID,
		Model:       st.Alias,
		ModelPath:   st.ModelPath,
		UptimeSec:   st.UptimeSec,
		RSSMB:       st.RSSMB,
		BinPath:     st.BinPath,
		Variant:     st.Variant,
		CmdLine:     st.CmdLine,
		Args:        st.Args,
		LogPath:     st.LogPath,
		LogTail:     st.LogTail,
	}
	if !st.Running {
		h.Error = "引擎未启动：在「模型」页选择一个 GGUF 文件点「加载」，或发起一次请求由本程序自动拉起"
		if st.ModelPath != "" {
			h.Error = "引擎已停止（上次加载：" + filepath.Base(st.ModelPath) + "）"
		}
		return h
	}
	start := time.Now()
	body, code, err := m.get(ctx, "/health")
	if err != nil {
		h.Error = err.Error()
		return h
	}
	h.LatencyMs = int(time.Since(start).Milliseconds())
	if code != http.StatusOK {
		h.Error = fmt.Sprintf("模型加载中（HTTP %d）", code)
		return h
	}
	var payload struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(body, &payload)
	// llama-server 的 /health：{"status":"ok"} 或 {"status":"loading model"} 或 error
	h.Online = payload.Status == "ok"
	if !h.Online {
		h.Error = "模型加载中：" + strings.TrimSpace(payload.Status)
	}
	if models, _, err := m.get(ctx, "/v1/models"); err == nil {
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(models, &list) == nil {
			h.ModelCount = len(list.Data)
			if len(list.Data) > 0 && h.Model == "" {
				h.Model = list.Data[0].ID
			}
		}
	}
	return h
}

// List 实现 engine.Adapter：扫描本地 GGUF 库。
func (m *Manager) List(ctx context.Context) ([]engine.Model, error) {
	m.mu.RLock()
	roots := append([]string{}, m.cfg.ModelRoots...)
	loadedPath := m.modelPath
	running := m.running
	alias := m.alias
	m.mu.RUnlock()

	models := ScanModels(roots, m.meta, 2*time.Second)
	for i := range models {
		if running && samePath(models[i].Path, loadedPath) {
			models[i].Loaded = true
			models[i].State = "loaded"
			if alias != "" {
				models[i].Name = alias
			}
		}
	}
	// 已加载但不在扫描目录里的模型（手动填路径加载）也要出现
	if running && loadedPath != "" {
		found := false
		for _, mm := range models {
			if samePath(mm.Path, loadedPath) {
				found = true
				break
			}
		}
		if !found {
			meta, _ := m.meta.get(loadedPath, 0)
			if meta == nil {
				if parsed, err := parseGGUF(loadedPath); err == nil {
					meta = parsed
				}
			}
			mm := engine.Model{
				ID: loadedPath, Name: alias, Path: loadedPath, Type: "llm",
				Loaded: true, State: "loaded", Meta: meta,
			}
			if meta != nil {
				applyMeta(&mm, meta)
			}
			models = append([]engine.Model{mm}, models...)
		}
	}
	return models, nil
}

// Forward 实现 engine.Adapter。
func (m *Manager) Forward(ctx context.Context, path string, body []byte, hdr http.Header) (*http.Response, error) {
	if !m.Running() {
		return nil, fmt.Errorf("自管引擎未运行")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	if hdr != nil {
		if v := hdr.Get("Accept"); v != "" {
			req.Header.Set("Accept", v)
		}
	}
	return m.hc.Do(req)
}

// Load 实现 engine.Adapter：启动子进程。
func (m *Manager) Load(ctx context.Context, model engine.Model, opts engine.LoadOptions) error {
	path := model.Path
	if path == "" {
		path = model.ID
	}
	values := map[string]any{}
	for k, v := range opts.Params {
		values[k] = v
	}
	values["model_path"] = path
	alias := opts.Alias
	if alias == "" {
		alias = model.Name
	}
	return m.Start(ctx, path, values, alias)
}

// Unload 实现 engine.Adapter：停止子进程。
func (m *Manager) Unload(ctx context.Context, model engine.Model) error {
	m.mu.RLock()
	cur := m.modelPath
	m.mu.RUnlock()
	if cur != "" && model.Path != "" && !samePath(cur, model.Path) {
		return nil // 请求卸载的不是当前模型
	}
	return m.Stop(ctx, true)
}

// Running 是否在运行。
func (m *Manager) Running() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// ModelPath 当前加载的模型文件。
func (m *Manager) ModelPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.modelPath
}

// IdleSince 已空闲多久（用于空闲自动卸载）。
func (m *Manager) Uptime() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.running {
		return 0
	}
	return time.Since(m.startedAt)
}

// Start 拉起 llama-server 子进程并等待就绪。
func (m *Manager) Start(ctx context.Context, modelPath string, values map[string]any, alias string) error {
	// 同模型 + 同参数已在跑 → 直接复用（幂等）
	if m.Running() {
		m.mu.RLock()
		cur := m.modelPath
		m.mu.RUnlock()
		if samePath(cur, modelPath) {
			return nil
		}
		if err := m.Stop(ctx, true); err != nil {
			return fmt.Errorf("切换模型前停止旧进程失败：%w", err)
		}
	}

	rt, err := m.resolveRuntime()
	if err != nil {
		return err
	}
	args := BuildArgs(values)
	if !containsFlag(args, "-m") {
		args = append([]string{"-m", modelPath}, args...)
	}
	if alias != "" {
		args = append(args, "-a", alias)
	}

	var lastErr error
	// 端口被占时换一个（端口池 10001~10099）
	for attempt := 0; attempt < 3; attempt++ {
		port, err := pickPort(m.cfg.PortMin, m.cfg.PortMax)
		if err != nil {
			return err
		}
		if err := m.spawn(ctx, rt, args, port, modelPath, alias); err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "绑定") {
				continue
			}
			return err
		}
		if err := m.waitReady(ctx, 240*time.Second); err != nil {
			lastErr = err
			_ = m.Stop(context.Background(), false)
			if strings.Contains(err.Error(), "bind") || strings.Contains(err.Error(), "绑定") {
				continue
			}
			return err
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("启动失败")
	}
	return lastErr
}

func (m *Manager) spawn(ctx context.Context, rt Runtime, args []string, port int, modelPath, alias string) error {
	full := append([]string{}, args...)
	full = append(full, "--host", m.cfg.Host, "--port", strconv.Itoa(port))

	cmd := exec.Command(rt.Exe, full...)
	cmd.Dir = rt.Dir
	cmd.Env = envWithPath(rt)
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
		return fmt.Errorf("启动 llama-server 失败：%w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.proc = cmd.Process
	m.port = port
	m.modelPath = modelPath
	m.alias = alias
	m.args = args
	m.variant = rt.Variant
	m.exePath = rt.Exe
	m.startedAt = time.Now()
	m.running = true
	m.logTail = nil
	m.exited = make(chan struct{})
	logPath := m.logPathLocked()
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
	return nil
}

// pump 把子进程输出按行送进日志（界面「运行日志」页直接可见）。
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
	m.lastExit = code
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	m.lastErr = errText
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

// waitReady 轮询 /health 直到模型加载完成。
func (m *Manager) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.exited:
			return m.failure("llama-server 启动后立即退出")
		case <-time.After(600 * time.Millisecond):
		}
		body, code, err := m.get(ctx, "/health")
		if err == nil && code == http.StatusOK {
			var payload struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(body, &payload) == nil && payload.Status == "ok" {
				return nil
			}
		}
	}
	return m.failure("等待模型加载超时")
}

// failure 组装带日志尾巴的错误信息（显存不足 / 模板不兼容等都在日志里）。
func (m *Manager) failure(reason string) error {
	m.mu.RLock()
	tailLines := tail(m.logTail, 6)
	m.mu.RUnlock()
	if len(tailLines) == 0 {
		return fmt.Errorf("%s（无子进程日志输出）", reason)
	}
	return fmt.Errorf("%s：%s", reason, strings.Join(tailLines, " ｜ "))
}

// Stop 停止子进程。
func (m *Manager) Stop(ctx context.Context, graceful bool) error {
	m.mu.Lock()
	proc := m.proc
	exited := m.exited
	running := m.running
	m.mu.Unlock()
	if !running || proc == nil {
		return nil
	}
	_ = graceful
	killTree(proc.Pid)
	select {
	case <-exited:
	case <-time.After(8 * time.Second):
		_ = proc.Kill()
	}
	return nil
}

// Restart 重启（沿用上次的参数）。
func (m *Manager) Restart(ctx context.Context) error {
	m.mu.RLock()
	path, alias := m.modelPath, m.alias
	args := append([]string{}, m.args...)
	rt := m.rt
	m.mu.RUnlock()
	if path == "" {
		return fmt.Errorf("没有可重启的模型")
	}
	if err := m.Stop(ctx, true); err != nil {
		return err
	}
	// 从上次参数里回填取值（--host/--port 由 spawn 负责，不在这里）
	values := map[string]any{"model_path": path}
	_ = args
	m.mu.Lock()
	m.rt = rt
	m.mu.Unlock()
	return m.Start(ctx, path, values, alias)
}

// SetModelRoots 更新模型扫描根目录。
func (m *Manager) SetModelRoots(roots []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.ModelRoots = append([]string{}, roots...)
}

// SetPreferGPU 更新是否优先 GPU 变体；若已经探测过运行时，顺带按新策略重新择优。
//
// 这一步不能省：Detect() 常发生在硬件自检之前，此时还不知道有没有 N 卡，
// 会先挑到 CPU 变体；自检完成后必须重新择优，否则会一直用 CPU 跑。
func (m *Manager) SetPreferGPU(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.PreferGPU = v
	if len(m.runtime) == 0 {
		return
	}
	if rt, ok := PickBest(m.runtime, v); ok {
		m.rt = rt
	}
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// --- 工具 ---

func envWithPath(rt Runtime) []string {
	env := os.Environ()
	dirs := append([]string{rt.Dir}, rt.VendorDirs...)
	return appendPathEnv(env, dirs)
}

func appendPathEnv(env []string, dirs []string) []string {
	joined := strings.Join(dirs, string(os.PathListSeparator))
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			kv = kv + string(os.PathListSeparator) + joined
			out = append(out, kv)
			joined = ""
			continue
		}
		out = append(out, kv)
	}
	if joined != "" {
		out = append(out, "PATH="+joined)
	}
	return out
}

func pickPort(min, max int) (int, error) {
	if max < min {
		max = min
	}
	for p := min; p <= max; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			_ = ln.Close()
			return p, nil
		}
	}
	return 0, fmt.Errorf("端口池 %d~%d 已用尽", min, max)
}

func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
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
