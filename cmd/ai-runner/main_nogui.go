//go:build !desktop

// headless 可执行文件入口（带命令行参数）。
//
//	go run ./cmd/ai-runner -no-browser -data D:\airunner-data
//
// 管理台地址会打印在控制台，默认 http://127.0.0.1:8081/ 。
package main

import (
	"flag"
	"log"

	"airunner/internal/headless"
)

func main() {
	noBrowser := flag.Bool("no-browser", false, "启动后不自动打开浏览器")
	dataDir := flag.String("data", "", "配置目录（默认 %APPDATA%/AIRunner）")
	flag.Parse()

	if err := headless.Run(headless.Options{DataDir: *dataDir, NoBrowser: *noBrowser, Block: true}); err != nil {
		log.Fatalf("%v", err)
	}
}
