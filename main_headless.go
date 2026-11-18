//go:build !desktop

// 默认（无 desktop 标签）入口：headless 模式，方便 `go run .` 直接调试。
//
// 桌面窗口版本：wails build 或 go build -tags desktop -o llamadock-desktop.exe .
package main

import (
	"log"

	"airunner/internal/headless"
)

func main() {
	if err := headless.Run(headless.Options{Block: true}); err != nil {
		log.Fatalf("%v", err)
	}
}
