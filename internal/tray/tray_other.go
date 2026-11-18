//go:build !windows

// 非 Windows 平台的托盘占位实现（本项目目标平台为 Windows）。
package tray

import "errors"

// Item 一个菜单项。
type Item struct {
	Title   string
	Sep     bool
	OnClick func()
}

// Run 在非 Windows 平台不支持。
func Run(tooltip string, items []Item, onLeftClick func()) (func(), error) {
	return func() {}, errors.New("当前平台不支持系统托盘")
}

// SetTooltip 空实现。
func SetTooltip(text string) {}
