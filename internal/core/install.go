package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"airunner/internal/config"
	"airunner/internal/engine/llamacpp"
)

// EngineInstallState 一键安装 llama-server 的进度 / 结果快照。
//
// 安装是后台任务（几百 MB 的下载），界面按这个结构轮询显示进度。
type EngineInstallState struct {
	Running    bool   `json:"running"`
	Stage      string `json:"stage"` // resolving / downloading / extracting / done / error
	Message    string `json:"message"`
	AssetName  string `json:"asset_name,omitempty"`
	AssetIndex int    `json:"asset_index"`
	AssetCount int    `json:"asset_count"`
	Downloaded int64  `json:"downloaded"` // 已下载字节（跨多个资产累计）
	Total      int64  `json:"total"`      // 全部资产总字节
	Percent    int    `json:"percent"`
	Tag        string `json:"tag,omitempty"`
	Variant    string `json:"variant,omitempty"`
	TargetDir  string `json:"target_dir,omitempty"`
	ExePath    string `json:"exe_path,omitempty"`
	Error      string `json:"error,omitempty"`
	UpdatedAt  int64  `json:"updated_at"`
}

// EngineInstallVariants 界面下拉用的变体列表。
func (c *Core) EngineInstallVariants() []string {
	all := llamacpp.AllVariants()
	out := make([]string, 0, len(all))
	for _, v := range all {
		out = append(out, string(v))
	}
	return out
}

// EngineInstallState 返回当前安装进度（界面轮询）。
func (c *Core) EngineInstallState() EngineInstallState {
	c.installMu.Lock()
	defer c.installMu.Unlock()
	return c.install
}

// InstallEngine 启动一次「下载并安装 llama-server」后台任务。
//
// variant 为空或 "auto" 时按硬件画像自动选择（有 CUDA 优先 CUDA，其次 Vulkan，再退 CPU）。
func (c *Core) InstallEngine(variant string) error {
	c.installMu.Lock()
	if c.install.Running {
		c.installMu.Unlock()
		return fmt.Errorf("已经在安装了，请等当前任务跑完")
	}
	want := llamacpp.InstallVariant(strings.TrimSpace(variant))
	if want == "" {
		want = llamacpp.VariantAuto
	}
	c.install = EngineInstallState{
		Running:   true,
		Stage:     "resolving",
		Message:   "正在解析官方最新版本…",
		UpdatedAt: time.Now().Unix(),
	}
	c.installMu.Unlock()

	go c.runInstall(want)
	return nil
}

func (c *Core) runInstall(want llamacpp.InstallVariant) {
	defer func() {
		c.installMu.Lock()
		c.install.Running = false
		c.install.UpdatedAt = time.Now().Unix()
		c.installMu.Unlock()
	}()

	// 下载几百 MB 可能很慢，给足时间；用户也可以直接关掉程序中断。
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()

	plan, err := llamacpp.ResolveInstallPlan(ctx, want, c.engineBackends())
	if err != nil {
		c.failInstall(err)
		return
	}

	c.installMu.Lock()
	c.install.Tag = plan.Tag
	c.install.Variant = string(plan.Variant)
	c.install.TargetDir = plan.TargetDir
	c.install.Total = plan.TotalSize
	c.install.AssetCount = len(plan.Assets)
	c.install.Stage = "downloading"
	c.install.Message = "开始下载"
	c.install.UpdatedAt = time.Now().Unix()
	c.installMu.Unlock()

	c.Log.Info("开始安装 llama-server", "tag", plan.Tag, "variant", plan.Variant, "dir", plan.TargetDir)

	err = llamacpp.Install(ctx, plan, func(p llamacpp.InstallProgress) {
		// 已下载字节 = 前面几个资产的完整大小 + 当前资产已下载部分
		var base int64
		for i := 0; i < p.AssetIndex-1 && i < len(plan.Assets); i++ {
			base += plan.Assets[i].Size
		}
		c.installMu.Lock()
		c.install.Stage = p.Stage
		c.install.Message = p.Message
		c.install.AssetName = p.AssetName
		c.install.AssetIndex = p.AssetIndex
		c.install.AssetCount = p.AssetCount
		c.install.Downloaded = base + p.Downloaded
		c.install.Total = plan.TotalSize
		c.install.Percent = percentOf(c.install.Downloaded, c.install.Total)
		c.install.UpdatedAt = time.Now().Unix()
		c.installMu.Unlock()
	})
	if err != nil {
		c.failInstall(err)
		return
	}

	exe := llamacpp.FindServerExe(plan.TargetDir)
	c.installMu.Lock()
	c.install.Stage = "done"
	c.install.Percent = 100
	c.install.Downloaded = plan.TotalSize
	c.install.ExePath = exe
	c.install.Message = "安装完成"
	if exe == "" {
		c.install.Message = "解压完成，但没在目录里找到 llama-server.exe"
	}
	c.install.UpdatedAt = time.Now().Unix()
	c.installMu.Unlock()

	c.Log.Info("llama-server 安装完成", "exe", exe, "dir", plan.TargetDir)

	// 写回引擎配置并重新探测，界面无需再手动填路径。
	if exe != "" {
		cfg, uerr := c.Cfg.Update(func(cc *config.Config) { cc.Engine.ExePath = exe })
		if uerr != nil {
			c.Log.Warn("写入引擎路径失败", "error", uerr)
		} else {
			c.applyEngineConfig(cfg)
		}
	}
	c.llama.Detect()
}

// engineBackends 本机可用的推理后端（cuda / vulkan / cpu），供自动选变体。
func (c *Core) engineBackends() []string {
	c.mu.RLock()
	hw := c.hw
	c.mu.RUnlock()
	if hw == nil {
		hw = c.Cfg.Snapshot().HW
	}
	if hw == nil {
		return nil
	}
	return hw.Backends
}

func (c *Core) failInstall(err error) {
	c.installMu.Lock()
	c.install.Stage = "error"
	c.install.Error = err.Error()
	c.install.Message = err.Error()
	c.install.UpdatedAt = time.Now().Unix()
	c.installMu.Unlock()
	c.Log.Warn("安装 llama-server 失败", "error", err)
}

func percentOf(done, total int64) int {
	if total <= 0 {
		return 0
	}
	p := int(done * 100 / total)
	if p > 100 {
		p = 100
	}
	if p < 0 {
		p = 0
	}
	return p
}
