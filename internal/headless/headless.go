// Package headless 提供无窗口启动方式。
//
// 两个用途：
//   - 开发调试：`go run .` / `go run ./cmd/ai-runner` 直接起服务，浏览器打开管理台；
//   - 兜底：WebView2 缺失时也能用（与桌面窗口共用同一份前端与同一套 API）。
package headless

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"airunner/internal/app"
)

// Options 启动参数。
type Options struct {
	DataDir   string // 配置目录，空 = 默认 %APPDATA%/AIRunner
	NoBrowser bool   // 不自动打开浏览器
	Block     bool   // 是否阻塞等待 Ctrl+C（false 时直接返回，供测试使用）
}

// Run 启动应用并阻塞等待退出信号。
func Run(opts Options) error {
	a, err := app.New(opts.DataDir)
	if err != nil {
		return fmt.Errorf("启动失败：%w", err)
	}
	if err := a.Start(); err != nil {
		return fmt.Errorf("启动失败：%w", err)
	}
	defer a.Stop()

	st := a.Core.Status()
	fmt.Println("──────────────────────────────────────────────")
	fmt.Printf(" LlamaDock %s（headless 模式）\n", st.Version)
	fmt.Printf("   管理台：    %s\n", a.AdminURL())
	fmt.Printf("   推理网关：  %s\n", a.GatewayURL())
	fmt.Printf("   引擎：      %s（%s）\n", st.Engine.Endpoint, onlineText(st.Engine.Health.Online))
	fmt.Printf("   硬件：      %s\n", st.HWSummary)
	fmt.Println("   桌面窗口版本：go build -tags desktop .")
	fmt.Println("──────────────────────────────────────────────")
	log.Printf("管理台已就绪：%s", a.AdminURL())

	if !opts.NoBrowser {
		openBrowser(a.AdminURL())
	}
	if !opts.Block {
		return nil
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	fmt.Println("正在退出…")
	return nil
}

func onlineText(ok bool) string {
	if ok {
		return "已连接"
	}
	return "未启动（尚未加载模型）"
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
