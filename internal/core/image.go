// 生图业务能力：把 sdcpp.Manager 包装成上层（API / 网关）可直接调用的方法。
//
// 显存避让：8GB 显卡无法同时容纳文本模型与扩散模型。生图引擎启动前，
// 若配置开启 UnloadLLM，会先停掉 llama-server 释放显存；反之亦然
// （加载文本模型时由 LoadModel 负责停掉生图引擎，见 core.go 的对应钩子）。
package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"airunner/internal/config"
	"airunner/internal/engine/sdcpp"
)

// ImageStatusDTO 生图引擎状态（给前端）。
type ImageStatusDTO struct {
	Enabled       bool                   `json:"enabled"`
	Available     bool                   `json:"available"` // 是否探测到 sd-server.exe
	Running       bool                   `json:"running"`
	Online        bool                   `json:"online"`
	Status        sdcpp.Status           `json:"process"`
	Defaults      ImageDefaults          `json:"defaults"`
	Error         string                 `json:"error,omitempty"`
	Profiles      []config.ImageProfile  `json:"profiles,omitempty"`      // 生图模型预设列表
	ActiveProfile string                 `json:"active_profile,omitempty"` // 当前激活的预设 id
}

// ImageDefaults 前端表单默认值（来自配置）。
type ImageDefaults struct {
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	Steps   int     `json:"steps"`
	Cfg     float64 `json:"cfg_scale"`
	Sampler string  `json:"sampler,omitempty"`
}

// ImageStatus 返回生图引擎状态。
func (c *Core) ImageStatus(ctx context.Context) ImageStatusDTO {
	snap := c.Cfg.Snapshot()
	img := snap.Image
	st := c.sd.Status()
	dto := ImageStatusDTO{
		Enabled:   img.Enabled,
		Running:   st.Running,
		Status:    st,
		Available: st.Detected != "" || strings.TrimSpace(img.ExePath) != "",
		Defaults: ImageDefaults{
			Width:   img.DefaultWidth,
			Height:  img.DefaultHeight,
			Steps:   img.DefaultSteps,
			Cfg:     img.DefaultCfg,
			Sampler: img.DefaultSampler,
		},
		Profiles:      img.Profiles,
		ActiveProfile: img.ActiveProfile,
	}
	if st.LastError != "" {
		dto.Error = st.LastError
	}
	if st.Running {
		online, err := c.sd.Health(ctx)
		dto.Online = online
		if err != "" {
			dto.Error = err
		}
	}
	return dto
}

// ImageModels 列出可扫描到的生图模型。
func (c *Core) ImageModels() []sdcpp.ImageModel {
	snap := c.Cfg.Snapshot()
	return sdcpp.ScanModels(snap.Image.ModelDir)
}

// ImageStart 启动生图引擎（必要时先卸载文本模型腾显存）。
func (c *Core) ImageStart(ctx context.Context, modelPath string) error {
	snap := c.Cfg.Snapshot()
	if !snap.Image.Enabled {
		return fmt.Errorf("生图功能未启用，请先到「设置 → 生图引擎」开启并填写 sd-server.exe 路径")
	}
	if strings.TrimSpace(modelPath) == "" {
		modelPath = snap.Image.ModelPath
	}
	// 多组件模型（Qwen-Image / Flux 等）ModelPath 为空，靠 ExtraArgs 传 --diffusion-model/--llm/--vae，
	// 因此只要配了 ExtraArgs 就允许启动。
	if strings.TrimSpace(modelPath) == "" && strings.TrimSpace(snap.Image.ExtraArgs) == "" {
		return fmt.Errorf("请先选择生图模型（设置页的模型路径或额外启动参数，或启动时传入 model）")
	}
	// 显存避让：先停文本模型
	if snap.Image.UnloadLLM && c.llama.Running() {
		c.Log.Info("生图启动前卸载文本模型以释放显存")
		if err := c.llama.Stop(ctx, true); err != nil {
			return fmt.Errorf("卸载文本模型失败：%w", err)
		}
	}
	if err := c.sd.Start(ctx, modelPath); err != nil {
		return err
	}
	c.Log.Info("生图引擎已启动", "model", modelPath, "port", snap.Image.Port)
	return nil
}

// ImageUseProfile 激活一个生图模型预设：把该预设的模型路径 / 额外参数 / 默认出图参数
// 写回 ImageConfig 主字段并持久化（配置订阅会自动把新配置热更新到 sdcpp.Manager）。
// 若引擎正在运行则先停掉——切换预设后需要重新启动以加载新模型。
func (c *Core) ImageUseProfile(ctx context.Context, id string) (ImageStatusDTO, error) {
	snap := c.Cfg.Snapshot()
	var target *config.ImageProfile
	for i := range snap.Image.Profiles {
		if snap.Image.Profiles[i].ID == id {
			target = &snap.Image.Profiles[i]
			break
		}
	}
	if target == nil {
		return c.ImageStatus(ctx), fmt.Errorf("找不到生图预设：%s", id)
	}
	cfg, err := c.Cfg.Update(func(cfg *config.Config) {
		cfg.Image.ModelPath = target.ModelPath
		cfg.Image.ExtraArgs = target.ExtraArgs
		cfg.Image.ActiveProfile = target.ID
		if target.DefaultWidth > 0 {
			cfg.Image.DefaultWidth = target.DefaultWidth
		}
		if target.DefaultHeight > 0 {
			cfg.Image.DefaultHeight = target.DefaultHeight
		}
		if target.DefaultSteps > 0 {
			cfg.Image.DefaultSteps = target.DefaultSteps
		}
		if target.DefaultCfg > 0 {
			cfg.Image.DefaultCfg = target.DefaultCfg
		}
		if target.DefaultSampler != "" {
			cfg.Image.DefaultSampler = target.DefaultSampler
		}
	})
	if err != nil {
		return c.ImageStatus(ctx), fmt.Errorf("保存预设失败：%w", err)
	}
	// 显式把新配置热更新到 sdcpp 管理器（Cfg.Update 不触发 applyEngineConfig；
	// 且切换生图预设不应触碰文本引擎，故只更新 sdcpp，不走整套 applyEngineConfig）。
	if c.sd != nil {
		c.sd.SetConfig(c.imageConfig(cfg))
	}
	// 若引擎在运行，停掉以释放旧模型。
	if c.sd != nil && c.sd.Running() {
		if err := c.sd.Stop(ctx); err != nil {
			c.Log.Warn("切换生图预设时停止旧引擎失败", "error", err)
		}
	}
	c.Log.Info("已切换生图模型预设", "id", target.ID, "name", target.Name, "mode", target.Mode)
	return c.ImageStatus(ctx), nil
}

// ImageStop 停止生图引擎。
func (c *Core) ImageStop(ctx context.Context) error {
	return c.sd.Stop(ctx)
}

// ImageGenerate 执行一次文生图，直接返回 sd-server 的原始 HTTP 响应（调用方负责转发 body）。
func (c *Core) ImageGenerate(ctx context.Context, req sdcpp.GenerateRequest) (*http.Response, error) {
	if !c.Cfg.Snapshot().Image.Enabled {
		return nil, fmt.Errorf("生图功能未启用")
	}
	if !c.sd.Running() {
		return nil, fmt.Errorf("生图引擎未运行，请先在「生图」页启动引擎并加载模型")
	}
	snap := c.Cfg.Snapshot()
	img := snap.Image
	body, err := sdcpp.BuildOpenAIBody(req, img.DefaultWidth, img.DefaultHeight, img.DefaultSteps, img.DefaultCfg)
	if err != nil {
		return nil, err
	}
	// 生图通常耗时较长（几十秒），这里不再额外加超时，由客户端 context 控制。
	return c.sd.Generate(ctx, "/v1/images/generations", body, nil)
}

// EnsureSDStopped 在加载文本模型前调用：若生图引擎在跑，停掉它释放显存。
// 采用「谁先启动谁占用」的简单策略，避免两个引擎抢同一块 8GB 显存直接 OOM。
func (c *Core) EnsureSDStopped(ctx context.Context) {
	if c.sd != nil && c.sd.Running() {
		c.Log.Info("加载文本模型前停止生图引擎以释放显存")
		if err := c.sd.Stop(ctx); err != nil {
			c.Log.Warn("停止生图引擎失败", "error", err)
		}
	}
}

// ImageElapsedHint 返回一个面向用户的耗时提示（避免用户以为卡死）。
func ImageElapsedHint(start time.Time) string {
	return fmt.Sprintf("已用时 %.0f 秒，首次出图通常需要 30~90 秒", time.Since(start).Seconds())
}
