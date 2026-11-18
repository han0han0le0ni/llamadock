// Package app 负责装配：配置 → 日志 → 编排核心 → 管理 API → 推理网关。
//
// 桌面窗口与 headless 启动都复用这里的装配逻辑，保证两种入口行为一致。
package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"airunner/internal/api"
	"airunner/internal/config"
	"airunner/internal/core"
	"airunner/internal/logging"
	"airunner/internal/server"
)

// App 装配后的应用实例。
type App struct {
	Cfg     *config.Manager
	Core    *core.Core
	API     *api.Server
	Gateway *server.Gateway
	Log     *slog.Logger

	mu      sync.RWMutex
	rootCtx context.Context
	closer  io.Closer
}

// New 装配应用（不启动监听）。
func New(dataDir string) (*App, error) {
	cfg, err := config.NewManager(dataDir)
	if err != nil {
		return nil, fmt.Errorf("加载配置失败：%w", err)
	}
	snap := cfg.Snapshot()
	log, ring, closer := logging.New(snap.UI.LogLevel, filepath.Dir(cfg.Path()))

	c := core.New(cfg, log, ring, fmt.Sprintf("127.0.0.1:%d", snap.Server.AdminPort))
	gw := server.NewGateway(c, log)
	apiSrv := api.New(c, log)

	c.SetGatewayController(
		func() error {
			cur := cfg.Snapshot()
			return gw.Start(cur.Server.Host, cur.Server.Port)
		},
		func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return gw.Stop(ctx)
		},
		gw.Running,
		gw.Addr,
	)

	app := &App{Cfg: cfg, Core: c, API: apiSrv, Gateway: gw, Log: log, closer: closer}
	log.Info("LlamaDock 启动中", "version", core.Version, "config", cfg.Path())
	return app, nil
}

// Start 启动管理 API、网关与后台循环。
func (a *App) Start() error {
	snap := a.Cfg.Snapshot()
	if err := a.API.Start(snap.Server.AdminPort); err != nil {
		return err
	}
	a.Core.Start()

	// 硬件自检：首次或配置里没有缓存时执行一次
	if snap.HW == nil {
		if p, err := a.Core.Hardware(true); err == nil && p != nil {
			a.Log.Info("首次硬件自检完成", "summary", p.Summary())
		} else if err != nil {
			a.Log.Warn("硬件自检失败（不影响网关运行）", "error", err)
		}
	}
	if snap.Server.AutoStart {
		if err := a.Gateway.Start(snap.Server.Host, snap.Server.Port); err != nil {
			a.Log.Error("推理网关启动失败", "error", err)
		}
	}
	if !snap.FirstRunDone {
		if _, err := a.Cfg.Update(func(c *config.Config) { c.FirstRunDone = true }); err != nil {
			a.Log.Warn("写入首次运行标记失败", "error", err)
		}
	}
	return nil
}

// Stop 优雅退出：停网关 → 停核心 → 关日志文件。
func (a *App) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if a.Gateway.Running() {
		if err := a.Gateway.Stop(ctx); err != nil {
			a.Log.Warn("网关停止超时", "error", err)
		}
	}
	if err := a.API.Stop(ctx); err != nil {
		a.Log.Warn("管理 API 停止超时", "error", err)
	}
	a.Core.Stop()
	if a.closer != nil {
		_ = a.closer.Close()
	}
}

// SetRootContext 桌面模式下由 Wails 注入根 context（托盘等回调需要）。
func (a *App) SetRootContext(ctx context.Context) {
	a.mu.Lock()
	a.rootCtx = ctx
	a.mu.Unlock()
}

// RootContext 返回根 context（未注入时返回 background）。
func (a *App) RootContext() context.Context {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.rootCtx == nil {
		return context.Background()
	}
	return a.rootCtx
}

// AdminURL 管理台地址（浏览器打开用）。
func (a *App) AdminURL() string {
	return "http://" + a.API.Addr() + "/"
}

// GatewayURL 局域网接入地址。
func (a *App) GatewayURL() string {
	return a.Core.Status().GatewayURL
}
