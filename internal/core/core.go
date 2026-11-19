// Package core 是编排层：把配置、日志、指标、请求日志、硬件自检、推荐引擎、
// 引擎适配器聚合在一起，对上（REST API / Wails 窗口）提供**一份**业务能力。
//
// 界面与网关卡都只调用本包，避免出现两套事实源。
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"airunner/internal/advisor"
	"airunner/internal/config"
	"airunner/internal/engine"
	"airunner/internal/engine/llamacpp"
	"airunner/internal/engine/sdcpp"
	"airunner/internal/gguf"
	"airunner/internal/hwprobe"
	"airunner/internal/logging"
	"airunner/internal/metrics"
	"airunner/internal/netinfo"
	"airunner/internal/params"
	"airunner/internal/reqlog"
)

// Version 应用版本。
const Version = "2.0.0"

// Core 编排中心。
type Core struct {
	Cfg     *config.Manager
	Log     *slog.Logger
	LogRing *logging.Ring
	Metrics *metrics.Store
	ReqLog  *reqlog.Ring

	// 推理引擎：本程序自己拉起并管理 llama-server 子进程（可调度）。
	//
	// eng 与 llama 指向**同一个实例**，只是两种视图：
	//   - eng：中性接口，状态 DTO 与网关只依赖它，将来新增后端（Ollama / vLLM）时上层零改动；
	//   - llama：具体类型，提供运行时候选探测、端口池、子进程生命周期等专属能力。
	eng   engine.Adapter
	llama *llamacpp.Manager

	// sd 生图引擎（stable-diffusion.cpp 的 sd-server），与文本引擎完全独立。
	// 8GB 显存下两者不能同时进显存；生图前可按配置 UnloadLLM 腾出显存。
	sd *sdcpp.Manager

	mu        sync.RWMutex
	hw        *hwprobe.Profile
	health    engine.Health
	models    []ModelDTO
	activeID  string
	activePth string
	presets   []config.Preset
	activePre string
	lastUse   time.Time // 最近一次转发/加载时间，用于空闲卸载

	startedAt time.Time

	startGW   func() error
	stopGW    func() error
	gwRunning func() bool
	gwAddr    func() string
	adminAddr string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 一键安装 llama-server 的后台任务状态（同一时刻只允许一个）。
	installMu sync.Mutex
	install   EngineInstallState
}

// SetGatewayController 由装配层注入网关启停钩子（避免 core ↔ server 循环依赖）。
func (c *Core) SetGatewayController(start, stop func() error, running func() bool, addr func() string) {
	c.startGW, c.stopGW, c.gwRunning, c.gwAddr = start, stop, running, addr
}

// New 构造编排中心。
func New(cfg *config.Manager, log *slog.Logger, ring *logging.Ring, adminAddr string) *Core {
	snap := cfg.Snapshot()
	c := &Core{
		Cfg:       cfg,
		Log:       log,
		LogRing:   ring,
		Metrics:   metrics.New(3600),
		ReqLog:    reqlog.New(snap.LogHistory),
		presets:   snap.Presets,
		activePre: snap.ActivePreset,
		activeID:  snap.LastModel,
		activePth: snap.LastModelPath,
		adminAddr: adminAddr,
		startedAt: time.Now(),
		lastUse:   time.Now(),
		hw:        snap.HW,
	}
	c.llama = llamacpp.New(c.llamaConfig(snap))
	c.eng = c.llama
	c.sd = sdcpp.New(c.imageConfig(snap))
	c.ctx, c.cancel = context.WithCancel(context.Background())
	// 启动时先对齐「配置对象 ↔ 参数表」中的服务参数，避免两处取值不一致
	if _, err := cfg.Update(func(cc *config.Config) { mirrorServerToParams(cc) }); err != nil {
		log.Warn("同步服务参数失败", "error", err)
	}
	return c
}

// llamaConfig 把配置翻译成 llamacpp 管理器参数（含日志回调）。
func (c *Core) llamaConfig(snap config.Config) llamacpp.Config {
	return llamacpp.Config{
		ExePath:     snap.Engine.ExePath,
		RuntimeDirs: append([]string{}, snap.Engine.RuntimeDirs...),
		ModelRoots:  append([]string{}, snap.ModelRoots...),
		Host:        snap.Engine.Host,
		PortMin:     snap.Engine.PortMin,
		PortMax:     snap.Engine.PortMax,
		LogDir:      filepath.Dir(c.Cfg.Path()),
		PreferGPU:   snap.HW != nil && snap.HW.HasNVIDIA(),
		OnLog: func(line string) {
			c.Log.Debug("llama-server", "line", line)
		},
		OnExit: func(code int, err error) {
			if code == 0 {
				c.Log.Info("llama-server 已退出")
				return
			}
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			c.Log.Warn("llama-server 异常退出", "code", code, "error", msg)
		},
	}
}

// imageConfig 把配置翻译成 sdcpp 管理器参数（含日志回调）。
func (c *Core) imageConfig(snap config.Config) sdcpp.Config {
	img := snap.Image
	return sdcpp.Config{
		ExePath:      img.ExePath,
		ModelDir:     img.ModelDir,
		ModelPath:    img.ModelPath,
		ExtraArgs:    img.ExtraArgs,
		Port:         img.Port,
		Backend:      img.Backend,
		OffloadToCPU: img.OffloadToCPU,
		LogDir:       filepath.Dir(c.Cfg.Path()),
		OnLog: func(line string) {
			c.Log.Debug("sd-server", "line", line)
		},
		OnExit: func(code int, err error) {
			if code == 0 {
				c.Log.Info("sd-server 已退出")
				return
			}
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			c.Log.Warn("sd-server 异常退出", "code", code, "error", msg)
		},
	}
}

// Start 启动后台循环（指标采样、引擎健康巡检、空闲卸载）。
func (c *Core) Start() {
	snap := c.Cfg.Snapshot()
	// 启动时探测一次 llama.cpp 运行时：设置页要能立刻看到候选后端
	if snap.Engine.AutoDetect {
		if rts := c.llama.Detect(); len(rts) > 0 {
			sel := c.llama.SelectedRuntime()
			c.Log.Info("已探测到 llama.cpp 运行时", "count", len(rts), "selected", sel.Variant, "exe", sel.Exe)
		} else {
			c.Log.Warn("未找到 llama-server.exe：请在「设置 → 推理引擎」指定路径，" +
				"或把 llama.cpp 解压到程序目录的 runtime\\llama.cpp 下")
		}
	}
	c.wg.Add(1)
	go c.loop()
}

// Stop 停止后台循环。
func (c *Core) Stop() {
	c.cancel()
	c.wg.Wait()
	// 生图引擎是独立子进程，主程序退出时必须一并关掉，避免残留占用显存/端口。
	if c.sd != nil {
		if err := c.sd.Stop(context.Background()); err != nil {
			c.Log.Warn("停止生图引擎失败", "error", err)
		}
	}
}

func (c *Core) loop() {
	defer c.wg.Done()
	sampleTicker := time.NewTicker(3 * time.Second)
	healthTicker := time.NewTicker(10 * time.Second)
	defer sampleTicker.Stop()
	defer healthTicker.Stop()
	c.health = c.probeEngine(c.ctx) // 首次探测
	c.refreshModels(c.ctx)
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-sampleTicker.C:
			c.sample()
		case <-healthTicker.C:
			c.health = c.probeEngine(c.ctx)
			c.refreshModels(c.ctx)
			c.maybeIdleUnload()
		}
	}
}

// maybeIdleUnload 空闲卸载。
//
// KeepAlive=true 表示「空闲也常驻」；否则超过 idle_unload_minutes 没有请求就把子进程停掉，
// 把显存还给系统——这是自管引擎才有的能力。
func (c *Core) maybeIdleUnload() {
	if !c.llama.Running() {
		return
	}
	snap := c.Cfg.Snapshot()
	if snap.Engine.KeepAlive || snap.Server.IdleUnloadMin <= 0 {
		return
	}
	c.mu.RLock()
	last := c.lastUse
	c.mu.RUnlock()
	if time.Since(last) < time.Duration(snap.Server.IdleUnloadMin)*time.Minute {
		return
	}
	c.Log.Info("空闲超过阈值，卸载模型", "idle_minutes", snap.Server.IdleUnloadMin)
	if err := c.llama.Stop(context.Background(), true); err != nil {
		c.Log.Warn("空闲卸载失败", "error", err)
	}
}

// touch 记录一次「有人在用引擎」，用于空闲卸载判定。
func (c *Core) touch() {
	c.mu.Lock()
	c.lastUse = time.Now()
	c.mu.Unlock()
}

func (c *Core) probeEngine(ctx context.Context) engine.Health {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.eng.Health(ctx)
}

// SampleVRAM 只读显存占用（用于指标曲线），走 nvidia-smi 单次查询。
func (c *Core) sample() {
	vram := 0
	if p, err := hwprobe.ProbeLight(); err == nil {
		if g := p.PrimaryGPU(); g != nil {
			vram = g.VRAMUsedMB
			c.mu.Lock()
			if c.hw != nil {
				// 只更新占用，保留用户按「重新自检」得到的完整画像
				for i := range c.hw.GPUs {
					c.hw.GPUs[i].VRAMUsedMB = g.VRAMUsedMB
					c.hw.GPUs[i].VRAMFreeMB = g.VRAMFreeMB
				}
			}
			c.mu.Unlock()
		}
	}
	c.Metrics.Sample(vram, c.Metrics.Snapshot(0).Active, c.Metrics.TPS())
}

// ModelDTO 给界面的模型条目。
type ModelDTO struct {
	ID            string     `json:"id"`
	Type          string     `json:"type,omitempty"`
	Arch          string     `json:"arch,omitempty"`
	Quant         string     `json:"quant,omitempty"`
	State         string     `json:"state,omitempty"`
	Loaded        bool       `json:"loaded"`
	MaxCtx        int        `json:"max_context_length,omitempty"`
	SizeMB        int        `json:"size_mb,omitempty"`
	LocalPath     string     `json:"local_path,omitempty"`
	Meta          *gguf.Meta `json:"meta,omitempty"`
	Recommendable bool       `json:"recommendable"`
}

func (c *Core) refreshModels(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	list, err := c.eng.List(ctx)
	if err != nil {
		return
	}
	out := make([]ModelDTO, 0, len(list))
	loadedID := ""
	for _, m := range list {
		dto := ModelDTO{
			ID: m.ID, Type: m.Type, Arch: m.Arch, Quant: m.Quant,
			State: m.State, Loaded: m.Loaded,
			MaxCtx: m.MaxCtx, SizeMB: m.SizeMB, Meta: m.Meta,
		}
		// 模型 id 就是 gguf 文件的绝对路径（见 llamacpp.ScanModels）
		path := m.Path
		if path == "" {
			path = m.ID
		}
		if path != "" {
			dto.LocalPath = path
			if dto.Meta == nil {
				if meta, err := gguf.Parse(path); err == nil {
					dto.Meta = meta
				}
			}
			if meta := dto.Meta; meta != nil {
				if dto.SizeMB == 0 {
					dto.SizeMB = meta.SizeMB
				}
				if dto.Quant == "" {
					dto.Quant = meta.Quant
				}
				if dto.Arch == "" {
					dto.Arch = meta.Arch
				}
			}
		}
		if dto.Type == "" {
			dto.Type = "llm"
		}
		dto.Recommendable = dto.Meta != nil || dto.MaxCtx > 0
		if dto.Loaded {
			loadedID = m.ID
		}
		out = append(out, dto)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	c.mu.Lock()
	c.models = out
	if loadedID != "" {
		c.activeID = loadedID
	}
	c.mu.Unlock()
}

// syncServiceParams 把「服务」类参数回写到 config.Server。
//
// 参数注册表与配置对象里都存在服务参数，必须保持单向一致，
// 否则会出现「参数页显示 8080、实际监听 8081」这种最容易被误当成 bug 的失配。
func syncServiceParams(cfg *config.Config) {
	if cfg.Params == nil {
		return
	}
	if v, ok := cfg.Params["public_host"]; ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			cfg.Server.Host = s
		}
	}
	if n, err := toFloat(cfg.Params["public_port"]); err == nil && n > 0 {
		cfg.Server.Port = int(n)
	}
	if n, err := toFloat(cfg.Params["max_concurrent_requests"]); err == nil && n > 0 {
		cfg.Server.MaxConcurrent = int(n)
	}
	if n, err := toFloat(cfg.Params["request_timeout"]); err == nil && n > 0 {
		cfg.Server.RequestTimeout = int(n)
	}
	if n, err := toFloat(cfg.Params["idle_unload_minutes"]); err == nil {
		cfg.Server.IdleUnloadMin = int(n)
	}
	if n, err := toFloat(cfg.Params["vram_reserve_mb"]); err == nil {
		cfg.Server.VramReserveMB = int(n)
	}
	if v, ok := cfg.Params["allow_auto_load"]; ok {
		if b, err := toBool(v); err == nil {
			cfg.Server.AllowAutoLoad = b
		}
	}
	if v, ok := cfg.Params["cors_origins"]; ok {
		if ss, err := toStrings(v); err == nil && len(ss) > 0 {
			cfg.Server.CORSOrigins = ss
		}
	}
	if cfg.Server.MaxBodyMB == 0 {
		cfg.Server.MaxBodyMB = 32
	}
}

// mirrorServerToParams 把 config.Server 写入参数表（启动时与配置更新后调用）。
func mirrorServerToParams(cfg *config.Config) {
	if cfg.Params == nil {
		cfg.Params = map[string]any{}
	}
	cfg.Params["public_host"] = cfg.Server.Host
	cfg.Params["public_port"] = cfg.Server.Port
	cfg.Params["cors_origins"] = cfg.Server.CORSOrigins
	cfg.Params["max_concurrent_requests"] = cfg.Server.MaxConcurrent
	cfg.Params["request_timeout"] = cfg.Server.RequestTimeout
	cfg.Params["allow_auto_load"] = cfg.Server.AllowAutoLoad
	cfg.Params["idle_unload_minutes"] = cfg.Server.IdleUnloadMin
	cfg.Params["vram_reserve_mb"] = cfg.Server.VramReserveMB
}

func toBool(v any) (bool, error) {
	switch b := v.(type) {
	case bool:
		return b, nil
	case string:
		return strings.EqualFold(b, "true"), nil
	case float64:
		return b != 0, nil
	}
	return false, fmt.Errorf("not a bool")
}

func toStrings(v any) ([]string, error) {
	switch s := v.(type) {
	case []string:
		return s, nil
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			out = append(out, fmt.Sprint(e))
		}
		return out, nil
	}
	return nil, fmt.Errorf("not a string list")
}

// --- 对外业务方法 ---

// Status 运行态总览。
func (c *Core) Status() Status {
	snap := c.Cfg.Snapshot()
	c.mu.RLock()
	hw := c.hw
	health := c.health
	models := append([]ModelDTO{}, c.models...)
	active := c.activePth
	c.mu.RUnlock()

	running := false
	addr := ""
	if c.gwRunning != nil {
		running = c.gwRunning()
	}
	if c.gwAddr != nil {
		addr = c.gwAddr()
	}
	ms := c.Metrics.Snapshot(120)
	st := Status{
		Version:        Version,
		UptimeSec:      int64(time.Since(c.startedAt).Seconds()),
		GatewayRunning: running,
		GatewayAddr:    addr,
		AdminAddr:      c.adminAddr,
		Engine:         c.engineDTO(health),
		ModelCount:     len(models),
		LoadedModel:    loadedModel(models),
		ActiveModel:    snap.LastModel,
		Metrics:        ms,
		HW:             hw,
		ParamsCount:    params.Count(),
		GatewayURL:     fmt.Sprintf("http://%s:%d/v1", netinfo.BestLANIP(), snap.Server.Port),
		Devices:        c.deviceCount(),
		HasNVIDIA:      hw != nil && hw.HasNVIDIA(),
		LastModelPath:  active,
	}
	if hw != nil {
		st.HWSummary = hw.Summary()
	}
	if !health.Online {
		st.Notes = append(st.Notes, "引擎未启动：在「模型」页选一个 GGUF 点「加载」，或直接发一次请求由本程序自动拉起")
	}
	if running && !st.HasNVIDIA {
		st.Notes = append(st.Notes, "未检测到可用的 NVIDIA 显存信息，显存曲线将不准确")
	}
	return st
}

func loadedModel(models []ModelDTO) string {
	for _, m := range models {
		if m.Loaded {
			return m.ID
		}
	}
	return ""
}

func (c *Core) deviceCount() int {
	seen := map[string]struct{}{}
	for _, e := range c.ReqLog.Query(reqlog.Filter{Limit: 200}) {
		if e.SourceIP != "" {
			seen[e.SourceIP] = struct{}{}
		}
	}
	return len(seen)
}

// Status 状态快照。
type Status struct {
	Version        string           `json:"version"`
	UptimeSec      int64            `json:"uptime_sec"`
	GatewayRunning bool             `json:"gateway_running"`
	GatewayAddr    string           `json:"gateway_addr"`
	GatewayURL     string           `json:"gateway_url"`
	AdminAddr      string           `json:"admin_addr"`
	Engine         EngineDTO        `json:"engine"`
	ModelCount     int              `json:"model_count"`
	LoadedModel    string           `json:"loaded_model"`
	ActiveModel    string           `json:"active_model"`
	LastModelPath  string           `json:"last_model_path,omitempty"`
	Metrics        metrics.Snapshot `json:"metrics"`
	HW             *hwprobe.Profile `json:"hardware,omitempty"`
	HWSummary      string           `json:"hardware_summary"`
	HasNVIDIA      bool             `json:"has_nvidia"`
	ParamsCount    int              `json:"params_count"`
	Devices        int              `json:"devices"`
	Notes          []string         `json:"notes,omitempty"`
}

// EngineDTO 引擎状态。
type EngineDTO struct {
	Endpoint    string        `json:"endpoint"`
	Kind        string        `json:"kind,omitempty"`
	Schedulable bool          `json:"schedulable"`
	Health      engine.Health `json:"health"`
}

// engineDTO 组装引擎状态（端点/类型随活动引擎变化）。
func (c *Core) engineDTO(health engine.Health) EngineDTO {
	eng := c.eng
	return EngineDTO{
		Endpoint:    eng.Endpoint(),
		Kind:        string(eng.Kind()),
		Schedulable: eng.Schedulable(),
		Health:      health,
	}
}

// Hardware 返回硬件画像；rescan 为 true 时重新自检并写回配置。
func (c *Core) Hardware(rescan bool) (*hwprobe.Profile, error) {
	if !rescan {
		c.mu.RLock()
		if c.hw != nil {
			p := *c.hw
			c.mu.RUnlock()
			return &p, nil
		}
		c.mu.RUnlock()
	}
	p, err := hwprobe.Probe()
	if err != nil && p == nil {
		return nil, err
	}
	if p != nil {
		c.mu.Lock()
		c.hw = p
		c.mu.Unlock()
		_, _ = c.Cfg.Update(func(cfg *config.Config) { cfg.HW = p })
		// 硬件画像变了 → 自管引擎的运行时择优（CPU / CUDA / Vulkan）也跟着变
		c.llama.SetPreferGPU(p.HasNVIDIA())
		c.Log.Info("硬件自检完成",
			"gpu", gpuName(p), "vram_mb", vramOf(p), "cores", p.CPU.PhysicalCores)
	}
	return p, err
}

func gpuName(p *hwprobe.Profile) string {
	if g := p.PrimaryGPU(); g != nil {
		return g.Name
	}
	return "none"
}

func vramOf(p *hwprobe.Profile) int {
	if g := p.PrimaryGPU(); g != nil {
		return g.VRAMDedicatedMB
	}
	return 0
}

// Models 返回模型列表。
func (c *Core) Models(ctx context.Context) ([]ModelDTO, error) {
	c.refreshModels(ctx)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]ModelDTO{}, c.models...), nil
}

// Recommend 生成推荐参数。
func (c *Core) Recommend(model, localPath, purpose string) (*advisor.Recommendation, error) {
	cfg := c.Cfg.Snapshot()
	c.mu.RLock()
	hw := c.hw
	c.mu.RUnlock()
	if hw == nil {
		p, err := c.Hardware(true)
		if err != nil {
			return nil, err
		}
		hw = p
	}
	path := strings.Trim(strings.TrimSpace(localPath), `"`)
	if path == "" && model != "" {
		// 模型 id 就是 gguf 路径；也允许界面传「模型名」过来，从已扫描结果里回查
		c.mu.RLock()
		for _, m := range c.models {
			if ModelKey(m.ID) == ModelKey(model) {
				path = m.LocalPath
				break
			}
		}
		c.mu.RUnlock()
	}
	var meta *gguf.Meta
	if path != "" {
		m, err := gguf.Parse(path)
		if err != nil {
			return nil, fmt.Errorf("解析模型头部失败：%w", err)
		}
		meta = m
	} else if model != "" {
		// 读不到本地文件：用引擎报告的最大上下文兜底
		c.mu.RLock()
		for _, m := range c.models {
			if m.ID == model {
				meta = &gguf.Meta{Path: model, Arch: m.Arch, NativeCtx: m.MaxCtx, Quant: m.Quant,
					Layers: 32, EmbDim: 4096, Heads: 32, HeadsKV: 8}
				break
			}
		}
		c.mu.RUnlock()
	}
	// 目标模型若正在运行，把它自己的占用从预算里扣除（否则会得出自相矛盾的结论）
	targetLoaded := 0
	c.mu.RLock()
	loaded := loadedModel(c.models)
	c.mu.RUnlock()
	if loaded != "" && (loaded == model || shortID(loaded) == shortID(model)) && meta != nil && meta.SizeMB > 0 {
		targetLoaded = int(float64(meta.SizeMB) * 1.1)
	}
	return advisor.Recommend(hw, meta, advisor.Options{
		ReserveMB:      cfg.Server.VramReserveMB,
		Purpose:        purpose,
		Conservative:   c.conservative(),
		TargetLoadedMB: targetLoaded,
	}), nil
}

func shortID(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

func (c *Core) conservative() float64 {
	v, ok := c.Cfg.Snapshot().Params["advisor_conservative"]
	if !ok {
		return 1.0
	}
	if fv, err := toFloat(v); err == nil {
		return fv
	}
	return 1.0
}

func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case json.Number:
		return n.Float64()
	case string:
		var f float64
		_, err := fmt.Sscanf(n, "%g", &f)
		return f, err
	}
	return 0, fmt.Errorf("not a number")
}

// ParamsDTO 参数面板数据。
type ParamsDTO struct {
	Schema       []params.Param  `json:"schema"`
	Common       []string        `json:"common"`
	Groups       []string        `json:"groups"`
	Values       map[string]any  `json:"values"`
	Presets      []config.Preset `json:"presets"`
	ActivePreset string          `json:"active_preset"`
	Issues       []params.Issue  `json:"issues,omitempty"`
	Count        int             `json:"count"`
}

// Params 读取参数面板数据。
func (c *Core) Params() ParamsDTO {
	snap := c.Cfg.Snapshot()
	values, issues := params.Normalize(snap.Params)
	common := []string{}
	for _, p := range params.Common() {
		common = append(common, p.Key)
	}
	return ParamsDTO{
		Schema:       params.All(),
		Common:       common,
		Groups:       params.Groups(),
		Values:       values,
		Presets:      snap.Presets,
		ActivePreset: snap.ActivePreset,
		Issues:       issues,
		Count:        params.Count(),
	}
}

// SetParams 校验并保存参数（局部覆盖）。
func (c *Core) SetParams(values map[string]any) (ParamsDTO, error) {
	snap := c.Cfg.Snapshot()
	merged := map[string]any{}
	for k, v := range snap.Params {
		merged[k] = v
	}
	for k, v := range values {
		merged[k] = v
	}
	norm, issues := params.Normalize(merged)
	fatal := []params.Issue{}
	for _, is := range issues {
		if is.Message != "未知参数，已忽略" {
			fatal = append(fatal, is)
		}
	}
	if len(fatal) > 0 {
		dto := c.Params()
		dto.Issues = fatal
		return dto, fmt.Errorf("参数校验失败：%s", fatal[0].Message)
	}
	if _, err := c.Cfg.Update(func(cfg *config.Config) {
		cfg.Params = norm
		syncServiceParams(cfg)
	}); err != nil {
		return c.Params(), err
	}
	dto := c.Params()
	dto.Issues = issues
	return dto, nil
}

// ApplyPreset 应用预设到全局参数。
func (c *Core) ApplyPreset(name string) (ParamsDTO, error) {
	snap := c.Cfg.Snapshot()
	for _, p := range snap.Presets {
		if p.Name == name {
			dto, err := c.SetParams(p.Values)
			if err != nil {
				return dto, err
			}
			_, err = c.Cfg.Update(func(cfg *config.Config) { cfg.ActivePreset = name })
			dto.ActivePreset = name
			c.Log.Info("已应用预设", "preset", name)
			return dto, err
		}
	}
	return c.Params(), fmt.Errorf("预设不存在：%s", name)
}

// SavePreset 保存自定义预设（同名覆盖内置之外的项）。
func (c *Core) SavePreset(name string) ([]config.Preset, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("预设名不能为空")
	}
	snap := c.Cfg.Snapshot()
	values := map[string]any{}
	for k, v := range snap.Params {
		if p, ok := params.Get(k); ok && p.Overridable {
			values[k] = v
		}
	}
	cfg, err := c.Cfg.Update(func(cfg *config.Config) {
		found := false
		for i := range cfg.Presets {
			if cfg.Presets[i].Name == name {
				cfg.Presets[i].Values = values
				cfg.Presets[i].Builtin = false
				found = true
			}
		}
		if !found {
			cfg.Presets = append(cfg.Presets, config.Preset{Name: name, Values: values})
		}
	})
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.presets = cfg.Presets
	c.mu.Unlock()
	return cfg.Presets, nil
}

// DeletePreset 删除自定义预设（内置不可删）。
func (c *Core) DeletePreset(name string) ([]config.Preset, error) {
	cfg, err := c.Cfg.Update(func(cfg *config.Config) {
		out := cfg.Presets[:0]
		for _, p := range cfg.Presets {
			if p.Name == name && !p.Builtin {
				continue
			}
			out = append(out, p)
		}
		cfg.Presets = out
	})
	if err != nil {
		return nil, err
	}
	return cfg.Presets, nil
}

// Logs 读取日志。
func (c *Core) Logs(limit int, level string) []logging.Entry {
	entries := c.LogRing.Recent(limit)
	if level == "" || level == "all" {
		return entries
	}
	out := []logging.Entry{}
	for _, e := range entries {
		if e.Level == level {
			out = append(out, e)
		}
	}
	return out
}

// ClearLogs 清空界面日志缓冲（不动磁盘日志文件）。
func (c *Core) ClearLogs() { c.LogRing.Clear() }

// Requests 查询请求日志。
func (c *Core) Requests(f reqlog.Filter) []reqlog.Entry { return c.ReqLog.Query(f) }

// RequestStats 请求聚合。
func (c *Core) RequestStats() reqlog.Stats { return c.ReqLog.Aggregate() }

// ClearRequests 清空请求日志。
func (c *Core) ClearRequests() { c.ReqLog.Clear() }

// Network 接入信息（含二维码 payload 与调用示例）。
func (c *Core) Network() netinfo.Access {
	snap := c.Cfg.Snapshot()
	c.mu.RLock()
	model := ""
	for _, m := range c.models {
		if m.Loaded {
			model = m.ID
			break
		}
	}
	c.mu.RUnlock()
	if model == "" {
		model = snap.LastModel
	}
	acc := netinfo.Build(snap.Server.Port, model)
	if !snap.FirstRunDone {
		acc.Tip = "首次使用建议先在「模型」页加载一个 GGUF，然后即可用手机直接连。"
	}
	return acc
}

// EngineInfo 引擎信息。
func (c *Core) EngineInfo() EngineDTO {
	c.mu.RLock()
	h := c.health
	c.mu.RUnlock()
	return c.engineDTO(h)
}

// RescanEngine 重新探测引擎（顺带重新探测 llama-server 运行时）。
func (c *Core) RescanEngine(ctx context.Context) EngineDTO {
	c.llama.Detect()
	h := c.probeEngine(ctx)
	c.mu.Lock()
	c.health = h
	c.mu.Unlock()
	c.refreshModels(ctx)
	return c.EngineInfo()
}

// InspectGGUF 校验并解析一个本地 gguf 文件。
func (c *Core) InspectGGUF(path string) (*gguf.Meta, error) {
	path = strings.TrimSpace(strings.Trim(path, `"`))
	if path == "" {
		return nil, fmt.Errorf("请填写 gguf 文件路径")
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return nil, fmt.Errorf("文件不存在：%s", path)
	}
	if err := gguf.Validate(path); err != nil {
		return nil, err
	}
	meta, err := gguf.Parse(path)
	if err != nil {
		return nil, err
	}
	return meta, nil
}

// SetActiveModel 记录当前选中的模型（用于接入示例与推荐）。
func (c *Core) SetActiveModel(model, localPath string) {
	c.mu.Lock()
	c.activeID = model
	c.activePth = localPath
	c.mu.Unlock()
	_, _ = c.Cfg.Update(func(cfg *config.Config) {
		cfg.LastModel = model
		if localPath != "" {
			cfg.LastModelPath = localPath
		}
		cfg.FirstRunDone = true
	})
}

// findModel 在当前引擎的模型视图里按 id / 文件名 / 路径查找。
func (c *Core) findModel(ctx context.Context, id string) (engine.Model, bool) {
	list, err := c.eng.List(ctx)
	if err != nil {
		return engine.Model{}, false
	}
	want := ModelKey(id)
	for _, m := range list {
		if m.ID == id || shortID(m.ID) == id {
			return m, true
		}
		if sameFile(m.Path, id) {
			return m, true
		}
		if want == "" {
			continue
		}
		for _, key := range []string{m.ID, m.Name, m.Path} {
			if ModelKey(key) == want {
				return m, true
			}
		}
	}
	return engine.Model{}, false
}

// sameFile 判断两个模型标识是否指向同一个文件（自管引擎的 id 就是文件路径）。
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// LoadModel 加载模型：自管引擎拉起子进程，外部引擎调用它的 REST 加载接口。
func (c *Core) LoadModel(ctx context.Context, id string, ctxLen int) error {
	return c.LoadModelWith(ctx, id, ctxLen, nil)
}

// LoadModelWith 同上，但允许界面显式覆盖本次加载的加载级参数（n_gpu_layers / n_ctx / cache_type_* …）。
//
// 覆盖只对本次加载生效，不写回全局参数——避免「点一次加载」把用户的面板配置改掉。
func (c *Core) LoadModelWith(ctx context.Context, id string, ctxLen int, override map[string]any) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("请先选择模型")
	}
	m, ok := c.findModel(ctx, id)
	if !ok {
		// 允许直接给一个本地 gguf 路径（自管引擎手动加载的场景）
		p := strings.Trim(id, `"`)
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			return fmt.Errorf("模型 %s 不在模型库中，且不是可访问的本地文件", id)
		}
		m = engine.Model{
			ID:   p,
			Name: strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)),
			Path: p,
			Type: "llm",
		}
	}
	// 角色拦截：有些 .gguf 不是对话模型，加载只会失败或毫无意义。
	switch m.Type {
	case "mmproj":
		return fmt.Errorf("这是视觉投影附件（mmproj），不能单独加载。它是配套主模型的\"眼睛\"，在选择视觉模型（如 Qwen3VL）加载时会自动挂载")
	case "diffusion":
		return fmt.Errorf("这是扩散生图模型，不能用文本推理引擎加载。请到「生图」页使用它（需 stable-diffusion.cpp 引擎）")
	}
	// 显存避让：若生图引擎正在占用显存，先停掉再加载文本模型（8GB 卡两者不能同时进显存）。
	c.EnsureSDStopped(ctx)
	if err := c.loadEngineModel(ctx, m, ctxLen, override); err != nil {
		return fmt.Errorf("启动 llama-server 失败：%w", err)
	}
	c.Log.Info("已加载模型", "engine", string(c.eng.Kind()), "model", m.Name, "path", m.Path)
	c.refreshModels(ctx)
	return nil
}

// loadEngineModel 把模型与参数交给当前引擎加载。
//
// 参数优先级：全局默认 < 该模型的「记忆」（模型级覆盖） < 本次加载的显式 override。
// 注意这里刻意只叠加被设置过的项、不做全量默认填充——BuildArgs 依赖「是否设置过」
// 来决定某个开关要不要下发（如 --mmap / -ngl），填满默认值会把它们全部显式打开。
func (c *Core) loadEngineModel(ctx context.Context, m engine.Model, ctxLen int, override map[string]any) error {
	key := ModelKey(m.ID)
	snap := c.Cfg.Snapshot()
	values := map[string]any{}
	for k, v := range snap.Params {
		values[k] = v
	}
	for k, v := range c.ModelParams(key) { // 模型记忆：上次为这个模型调好的参数
		values[k] = v
	}
	for k, v := range override {
		values[k] = v
	}
	// 模型名 → 模板自动套用：仅当用户（全局/记忆/本次 override）都没显式指定模板时才注入。
	// 优先级：用户显式 > 自动映射 > auto（模型自带模板）。不写回配置，只作用于本次加载。
	if snap.TemplateAutoEnabled {
		if mode, _ := values["chat_template_mode"].(string); mode == "" || mode == "auto" {
			if text, _ := values["chat_template"].(string); strings.TrimSpace(text) == "" {
				generalName := ""
				if m.Meta != nil {
					generalName = m.Meta.Name
				}
				if rule := matchAutoTemplate(m.Name, generalName, ModelKey(m.ID), snap.TemplateAutoMap); rule != nil {
					values["chat_template_mode"] = rule.Mode
					values["chat_template"] = rule.Text
					c.Log.Info("按模型名自动套用 Chat 模板", "pattern", rule.Pattern, "label", rule.Label, "model", m.Name)
				}
			}
		}
	}
	// 多模态：用户没显式指定 mmproj 时，在同目录自动找配套的视觉投影文件。
	// llama.cpp 的主模型里不含 ViT 权重，VLM 必须带 --mmproj 才能处理图片。
	if mm, _ := values["mmproj"].(string); strings.TrimSpace(mm) == "" {
		if found := findMMProj(m); found != "" {
			values["mmproj"] = found
			c.Log.Info("自动识别视觉投影 mmproj", "model", m.Name, "mmproj", found)
		}
	}
	if m.Path != "" {
		values["model_path"] = m.Path
	}
	if ctxLen > 0 {
		values["n_ctx"] = ctxLen
	}
	alias := m.Name
	if alias == "" {
		alias = strings.TrimSuffix(filepath.Base(m.ID), filepath.Ext(m.ID))
	}
	c.touch()
	if err := c.eng.Load(ctx, m, engine.LoadOptions{Params: values, Alias: alias}); err != nil {
		return err
	}
	c.mu.Lock()
	c.activeID = m.ID
	if m.Path != "" {
		c.activePth = m.Path
	}
	c.mu.Unlock()
	c.touchModel(key, m.Name, m.Path, ctxLen) // 记录使用统计，供界面展示
	return nil
}

// matchAutoTemplate 按模型名匹配自动套用规则，返回首条命中的规则；无匹配返回 nil。
//
// 匹配源：general.name（语义最强）> 文件名 stem > 归一化的 ModelKey。
// 三者拼成一个小写串，规则的 Pattern 作为子串匹配（同样转小写）。 Pattern 为空的规则忽略。
// 保留规则顺序——越具体的规则应排在前面（如 "qwen2.5" 在 "qwen" 之前）。
func matchAutoTemplate(name, generalName, modelKey string, rules []config.TemplateRule) *config.TemplateRule {
	fields := strings.ToLower(strings.Join([]string{generalName, name, modelKey}, "\x00"))
	if strings.TrimSpace(fields) == "" {
		return nil
	}
	for i := range rules {
		p := strings.ToLower(strings.TrimSpace(rules[i].Pattern))
		if p == "" {
			continue
		}
		if strings.Contains(fields, p) {
			return &rules[i]
		}
	}
	return nil
}

// findMMProj 在主模型同目录寻找配套的 mmproj-*.gguf。
// llama.cpp 把视觉塔单独放在 mmproj 文件里；只加载主模型时图片输入会被忽略或报错。
// 多个候选时选与主模型文件名公共前缀最长的那个（如 mmproj-Qwen3VL-8B-Instruct-F16.gguf）。
// 除了标为 vlm 的模型，也兼容元数据误标为 llm 但实际配套了视觉塔的模型
// （如 DeepSeek-V4-Pro-Qwen3.5-9B）；这类靠文件名强匹配，避免给纯文本模型误挂。
func findMMProj(m engine.Model) string {
	if m.Path == "" {
		return ""
	}
	arch := strings.ToLower(m.Arch)
	typ := strings.ToLower(m.Type)
	isVLM := typ == "vlm" || strings.Contains(arch, "vl") || strings.Contains(arch, "vision")
	dir := filepath.Dir(m.Path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(m.Path), filepath.Ext(m.Path)))
	best, bestStem, bestScore := "", "", -1
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".gguf") || !strings.Contains(lower, "mmproj") {
			continue
		}
		// 去掉 mmproj- 前缀再比，否则所有候选的公共前缀都是 "mmproj-"，无法区分模型。
		stem := strings.Trim(strings.ReplaceAll(lower, "mmproj", ""), "-_.")
		score := commonPrefixLen(stem, base)
		if score > bestScore {
			best, bestStem, bestScore = filepath.Join(dir, name), stem, score
		}
	}
	if best == "" {
		return ""
	}
	// 纯文本模型（元数据标成 llm）也可能配了视觉塔，但必须文件名高度吻合才挂，
	// 否则会给同目录里任意一个无关 llm 误挂 mmproj，反而导致加载失败。
	if !isVLM {
		ref := len(bestStem)
		if len(base) < ref {
			ref = len(base)
		}
		if ref == 0 || bestScore < 4 || bestScore*100 < ref*60 {
			return ""
		}
	}
	return best
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// UnloadModel 卸载模型（即停止 llama-server 子进程，显存随之释放）。
func (c *Core) UnloadModel(ctx context.Context, id string) error {
	eng := c.eng
	m, ok := c.findModel(ctx, id)
	if !ok {
		m = engine.Model{ID: id, Path: id}
	}
	if err := eng.Unload(ctx, m); err != nil {
		return fmt.Errorf("停止 llama-server 失败：%w", err)
	}
	c.Log.Info("已卸载模型", "engine", string(eng.Kind()), "model", id)
	c.refreshModels(ctx)
	return nil
}

// EnsureLoaded 转发前保证目标模型可用：按配置自动拉起子进程；
// 关闭自动加载时明确报错（503 + 原因），而不是让客户端拿到一个莫名其妙的失败。
func (c *Core) EnsureLoaded(ctx context.Context, modelID string) error {
	if c.llama.Running() && sameFile(c.llama.ModelPath(), modelID) {
		return nil
	}
	snap := c.Cfg.Snapshot()
	if !snap.Engine.AutoLoad || !snap.Server.AllowAutoLoad {
		if c.llama.Running() {
			return fmt.Errorf("当前已加载 %s，但自动加载已关闭；请先在「模型」页加载 %s",
				filepath.Base(c.llama.ModelPath()), filepath.Base(modelID))
		}
		return fmt.Errorf("模型 %s 尚未加载，且自动加载已关闭；请在「模型」页手动加载", filepath.Base(modelID))
	}
	m, ok := c.findModel(ctx, modelID)
	if !ok {
		return fmt.Errorf("模型 %s 不在已扫描的模型库中，无法自动加载；请检查「设置 → 模型根目录」",
			filepath.Base(modelID))
	}
	c.Log.Info("按需加载模型", "engine", "llamacpp", "model", m.Name, "path", m.Path)
	return c.loadEngineModel(ctx, m, 0, nil)
}

// Forward 把 OpenAI 兼容请求转发给引擎。
func (c *Core) Forward(ctx context.Context, path string, body []byte, hdr http.Header) (*http.Response, error) {
	if !c.llama.Running() {
		return nil, fmt.Errorf("llama-server 未运行")
	}
	c.touch()
	return c.eng.Forward(ctx, path, body, hdr)
}

// TestChat 试运行：直接向引擎发一次短请求（非流式），返回回复文本。
func (c *Core) TestChat(ctx context.Context, model, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		prompt = "你好，用一句话介绍你自己。"
	}
	if model == "" {
		c.refreshModels(ctx)
		c.mu.RLock()
		model = loadedModel(c.models)
		if model == "" && len(c.models) > 0 {
			model = c.models[0].ID
		}
		c.mu.RUnlock()
	}
	if model == "" {
		return "", fmt.Errorf("没有可用模型：请先加载一个 GGUF 模型")
	}
	if err := c.EnsureLoaded(ctx, model); err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
		"stream": false,
	})
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	resp, err := c.Forward(ctx, "/v1/chat/completions", body, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("引擎返回 HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("解析引擎响应失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("引擎没有返回内容")
	}
	c.Metrics.AddTokens(out.Usage.PromptTokens, out.Usage.CompletionTokens)
	c.SetActiveModel(model, "")
	return out.Choices[0].Message.Content, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// GatewayStart / GatewayStop / GatewayRunning 网关控制。
func (c *Core) GatewayStart() error {
	if c.startGW == nil {
		return fmt.Errorf("网关未装配")
	}
	if c.gwRunning != nil && c.gwRunning() {
		return nil
	}
	return c.startGW()
}

func (c *Core) GatewayStop() error {
	if c.stopGW == nil {
		return fmt.Errorf("网关未装配")
	}
	return c.stopGW()
}

// GatewayRunning 网关状态。
func (c *Core) GatewayRunning() bool {
	return c.gwRunning != nil && c.gwRunning()
}

// GatewayAddr 网关监听地址。
func (c *Core) GatewayAddr() string {
	if c.gwAddr == nil {
		return ""
	}
	return c.gwAddr()
}

// ConfigSnapshot 配置快照。
func (c *Core) ConfigSnapshot() config.Config { return c.Cfg.Snapshot() }

// JSONSchema 参数 JSON Schema。
func (c *Core) JSONSchema() map[string]any { return params.JSONSchema() }

// UpdateConfig 局部更新配置（界面设置页）。
func (c *Core) UpdateConfig(patch map[string]any) (config.Config, error) {
	raw, err := json.Marshal(patch)
	if err != nil {
		return c.Cfg.Snapshot(), err
	}
	cur := c.Cfg.Snapshot()
	curRaw, err := json.Marshal(cur)
	if err != nil {
		return cur, err
	}
	merged := map[string]any{}
	_ = json.Unmarshal(curRaw, &merged)
	var patchMap map[string]any
	if err := json.Unmarshal(raw, &patchMap); err != nil {
		return cur, err
	}
	deepMerge(merged, patchMap)
	finalRaw, err := json.Marshal(merged)
	if err != nil {
		return cur, err
	}
	var next config.Config
	if err := json.Unmarshal(finalRaw, &next); err != nil {
		return cur, err
	}
	cfg, err := c.Cfg.Update(func(c *config.Config) {
		*c = next
		mirrorServerToParams(c)
	})
	if err != nil {
		return cur, err
	}
	c.applyEngineConfig(cfg)
	c.Log.Info("配置已更新")
	return cfg, nil
}

// RuntimeDTO 一个可用的 llama.cpp 运行时（给设置页选择后端用）。
type RuntimeDTO struct {
	Exe     string `json:"exe"`
	Dir     string `json:"dir"`
	Variant string `json:"variant"` // cuda12 / cuda / vulkan / cpu
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"` // bundled / custom / path
	GPU     bool   `json:"gpu"`
	Current bool   `json:"current"` // 当前会被使用的那个
}

// EngineDetail 引擎管理页需要的全部信息。
type EngineDetail struct {
	Kind         string        `json:"kind"`
	Schedulable  bool          `json:"schedulable"`
	ExePath      string        `json:"exe_path,omitempty"`
	RuntimeDirs  []string      `json:"runtime_dirs"`
	Host         string        `json:"host,omitempty"`
	PortMin      int           `json:"port_min,omitempty"`
	PortMax      int           `json:"port_max,omitempty"`
	DefaultDir   string        `json:"default_dir,omitempty"` // 推荐的运行时落位（程序目录\runtime\llama.cpp）
	AutoLoad     bool          `json:"auto_load"`
	AutoDetect   bool          `json:"auto_detect"`
	KeepAlive    bool          `json:"keep_alive"`
	PreferGPU    bool          `json:"prefer_gpu"`
	Runtimes     []RuntimeDTO  `json:"runtimes"`
	DetectedHint string        `json:"detected_hint,omitempty"`
	Health       engine.Health `json:"health"`
}

// EngineDetail 返回引擎管理页所需的参数与探测结果。
func (c *Core) EngineDetail() EngineDetail {
	snap := c.Cfg.Snapshot()
	eng := c.eng
	c.mu.RLock()
	h := c.health
	c.mu.RUnlock()

	selected := c.llama.SelectedRuntime()
	rts := c.llama.Runtimes()
	out := make([]RuntimeDTO, 0, len(rts))
	for _, rt := range rts {
		out = append(out, RuntimeDTO{
			Exe: rt.Exe, Dir: rt.Dir, Variant: rt.Variant, Version: rt.Version,
			Source: rt.Source, GPU: rt.GPU || rt.Variant != "cpu",
			Current: selected.Exe != "" && strings.EqualFold(selected.Exe, rt.Exe),
		})
	}
	d := EngineDetail{
		Kind:        string(eng.Kind()),
		Schedulable: eng.Schedulable(),
		ExePath:     snap.Engine.ExePath,
		RuntimeDirs: append([]string{}, snap.Engine.RuntimeDirs...),
		Host:        snap.Engine.Host,
		PortMin:     snap.Engine.PortMin,
		PortMax:     snap.Engine.PortMax,
		DefaultDir:  llamacpp.DefaultRuntimeDir(),
		AutoLoad:    snap.Engine.AutoLoad,
		AutoDetect:  snap.Engine.AutoDetect,
		KeepAlive:   snap.Engine.KeepAlive,
		PreferGPU:   snap.HW != nil && snap.HW.HasNVIDIA(),
		Runtimes:    out,
		Health:      h,
	}
	if len(out) == 0 {
		d.DetectedHint = "没有找到 llama-server.exe。把官方 llama.cpp 的 Windows 压缩包解压到 " +
			d.DefaultDir + "，或在下面填写 llama-server.exe 的位置"
	}
	return d
}

// DetectRuntimes 重新扫描 llama-server 运行时（设置页「重新探测」）。
func (c *Core) DetectRuntimes() []RuntimeDTO {
	c.llama.Detect()
	return c.EngineDetail().Runtimes
}

// ModelRoots 当前模型根目录列表。
func (c *Core) ModelRoots() []string {
	return append([]string{}, c.Cfg.Snapshot().ModelRoots...)
}

// AddModelRoot 追加一个模型根目录（目录不存在时创建），返回更新后的列表。
func (c *Core) AddModelRoot(path string) ([]string, error) {
	path = strings.Trim(strings.TrimSpace(path), `"`)
	if path == "" {
		return c.ModelRoots(), fmt.Errorf("目录不能为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return c.ModelRoots(), err
	}
	if st, err := os.Stat(abs); err != nil {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return c.ModelRoots(), fmt.Errorf("目录不存在且无法创建：%s", abs)
		}
	} else if !st.IsDir() {
		return c.ModelRoots(), fmt.Errorf("不是目录：%s", abs)
	}
	cfg, err := c.Cfg.Update(func(cc *config.Config) {
		for _, r := range cc.ModelRoots {
			if strings.EqualFold(filepath.Clean(r), filepath.Clean(abs)) {
				return
			}
		}
		cc.ModelRoots = append(cc.ModelRoots, abs)
	})
	if err != nil {
		return c.ModelRoots(), err
	}
	c.llama.SetModelRoots(cfg.ModelRoots)
	c.Log.Info("已添加模型根目录", "path", abs)
	c.refreshModels(c.ctx)
	return cfg.ModelRoots, nil
}

// RemoveModelRoot 移除一个模型根目录（磁盘上的文件不动）。
func (c *Core) RemoveModelRoot(path string) ([]string, error) {
	target := filepath.Clean(strings.Trim(strings.TrimSpace(path), `"`))
	cfg, err := c.Cfg.Update(func(cc *config.Config) {
		out := cc.ModelRoots[:0]
		for _, r := range cc.ModelRoots {
			if strings.EqualFold(filepath.Clean(r), target) {
				continue
			}
			out = append(out, r)
		}
		cc.ModelRoots = out
	})
	if err != nil {
		return c.ModelRoots(), err
	}
	c.llama.SetModelRoots(cfg.ModelRoots)
	c.Log.Info("已移除模型根目录", "path", target)
	c.refreshModels(c.ctx)
	return cfg.ModelRoots, nil
}

// applyEngineConfig 把界面改过的引擎参数同步到引擎实例。
//
// exe 路径、运行时目录或模型根目录变了，已探测的候选要么作废要么可能多出新的，
// 所以顺手重探一次；端口池/绑定地址只有下次加载才生效，不必动正在跑的进程。
func (c *Core) applyEngineConfig(snap config.Config) {
	before := c.llama.Config()
	c.llama.SetConfig(c.llamaConfig(snap))
	if c.sd != nil {
		c.sd.SetConfig(c.imageConfig(snap))
	}

	if before.ExePath != snap.Engine.ExePath ||
		!sameStrings(before.RuntimeDirs, snap.Engine.RuntimeDirs) {
		c.llama.Detect()
	}
	c.refreshModels(context.Background())
}

// sameStrings 顺序无关地比较两个字符串集合。
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[strings.ToLower(filepath.Clean(s))]++
	}
	for _, s := range b {
		k := strings.ToLower(filepath.Clean(s))
		if seen[k] == 0 {
			return false
		}
		seen[k]--
	}
	return true
}

func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if sv, ok := v.(map[string]any); ok {
			if dv, ok := dst[k].(map[string]any); ok {
				deepMerge(dv, sv)
				continue
			}
		}
		dst[k] = v
	}
}

// MetricsSnapshot 指标快照。
func (c *Core) MetricsSnapshot() metrics.Snapshot { return c.Metrics.Snapshot(120) }
