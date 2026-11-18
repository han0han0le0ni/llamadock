//go:build desktop

// Package desktopui 是桌面窗口版本的装配：Wails 窗口 + 系统托盘。
//
// 构建：wails build  或  go build -tags desktop -o llamadock-desktop.exe .
//
// 窗口只负责「显示界面 + 托盘」，全部业务能力由内嵌前端通过本机管理 API 调用，
// 因此桌面窗口与浏览器管理台共享同一份前端代码与同一套接口。
package desktopui

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"airunner/internal/app"
	"airunner/internal/tray"
	"airunner/internal/webui"
)

// Main 启动桌面版。
func Main() {
	dataDir := flag.String("data", "", "配置目录（默认 %APPDATA%/AIRunner）")
	flag.Parse()

	a, err := app.New(*dataDir)
	if err != nil {
		log.Fatalf("启动失败：%v", err)
	}
	if err := a.Start(); err != nil {
		log.Fatalf("启动失败：%v", err)
	}

	// 系统托盘（Wails v2 无内置托盘，这里用 internal/tray 的 Win32 实现）
	stopTray, trayErr := tray.Run(trayTooltip(a), []tray.Item{
		{Title: "显示主窗口", OnClick: func() { runtime.WindowShow(a.RootContext()) }},
		{Title: "打开浏览器管理台", OnClick: func() { runtime.BrowserOpenURL(a.RootContext(), a.AdminURL()) }},
		{Sep: true},
		{Title: "启动推理网关", OnClick: func() {
			if err := a.Core.GatewayStart(); err != nil {
				runtime.LogErrorf(a.RootContext(), "启动失败: %v", err)
			}
		}},
		{Title: "停止推理网关", OnClick: func() {
			if err := a.Core.GatewayStop(); err != nil {
				runtime.LogErrorf(a.RootContext(), "停止失败: %v", err)
			}
		}},
		{Sep: true},
		{Title: "退出", OnClick: func() { runtime.Quit(a.RootContext()) }},
	}, func() { runtime.WindowShow(a.RootContext()) })
	if trayErr != nil {
		log.Printf("系统托盘不可用（不影响主窗口）: %v", trayErr)
	} else {
		defer stopTray()
		go func() {
			for range time.Tick(10 * time.Second) {
				tray.SetTooltip(trayTooltip(a))
			}
		}()
	}

	appMenu := menu.NewMenu()
	fileMenu := appMenu.AddSubmenu("文件")
	fileMenu.AddText("打开浏览器管理台", keys.CmdOrCtrl("b"), func(_ *menu.CallbackData) {
		runtime.BrowserOpenURL(a.RootContext(), a.AdminURL())
	})
	fileMenu.AddSeparator()
	fileMenu.AddText("退出", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		runtime.Quit(a.RootContext())
	})

	err = wails.Run(&options.App{
		Title:            "LlamaDock · 本地大模型控制台",
		Width:            1280,
		Height:           800,
		MinWidth:         960,
		MinHeight:        640,
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 21, A: 1},
		AssetServer:      &assetserver.Options{Assets: webui.FS()},
		OnStartup: func(ctx context.Context) {
			a.SetRootContext(ctx)
		},
		OnBeforeClose: func(ctx context.Context) bool {
			// 关窗即最小化到托盘（可在设置页关闭该行为）
			if a.Cfg.Snapshot().UI.MinimizeToTray {
				runtime.Hide(ctx)
				return true
			}
			return false
		},
		OnShutdown: func(ctx context.Context) {
			a.Stop()
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "ai-runner-8f2c1d4e",
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				runtime.WindowShow(a.RootContext())
			},
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			OnSuspend:            func() {},
			OnResume:             func() {},
		},
	})
	if err != nil {
		log.Printf("Wails 运行失败：%v", err)
		a.Stop()
		os.Exit(1)
	}
}

// trayTooltip 托盘悬停提示：版本 · 引擎状态 · 吞吐 · 显存。
func trayTooltip(a *app.App) string {
	st := a.Core.Status()
	engine := "引擎离线"
	if st.Engine.Health.Online {
		engine = "引擎在线"
	}
	gw := "网关已停"
	if st.GatewayRunning {
		gw = "网关 :" + lastSegment(st.GatewayAddr)
	}
	return fmt.Sprintf("LlamaDock %s · %s · %s · %.1f t/s · 显存 %.1fGB",
		st.Version, engine, gw, st.Metrics.TPS, float64(st.Metrics.VRAMUsedMB)/1024)
}

func lastSegment(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i+1:]
		}
	}
	return addr
}
