// Package webui 内嵌前端构建产物（Vue3 + Vite 打包到本包 dist/ 目录）。
//
// 同一份前端同时供：
//   - Wails 桌面窗口（从这里读资源）
//   - 本机浏览器（管理 API 直接托管同一份资源，便于调试与 headless 兜底）
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS 返回前端静态资源的根文件系统（已剥离 dist 前缀）。
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return embedded
	}
	return sub
}

// IndexHTML 返回首页内容。
func IndexHTML() []byte {
	raw, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		return []byte("<!doctype html><meta charset=utf-8><h3>前端尚未构建，请先执行 npm run build</h3>")
	}
	return raw
}
