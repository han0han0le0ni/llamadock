//go:build desktop

// 桌面窗口版入口（wails build 默认构建的就是本文件）。
package main

import "airunner/internal/desktopui"

func main() { desktopui.Main() }
