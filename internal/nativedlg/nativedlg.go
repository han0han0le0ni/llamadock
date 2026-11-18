// Package nativedlg 提供「本机原生」的目录 / 文件选择对话框。
//
// 只用在一个地方：管理口（仅绑 127.0.0.1）背后的操作者点「浏览…」。
// 对话框开在**运行本程序的这台机器**上——局域网设备在 TCP 层就连不到管理口，
// 所以不存在「远程弹窗」这种风险。
//
// 桌面窗口版与浏览器管理台共用同一套调用（都走管理 API），因此这里不做
// Wails 依赖：窗口版能从浏览器打开，浏览器版也不该失去「浏览」能力。
package nativedlg

import "errors"

// ErrUnsupported 当前平台没有可用的原生对话框实现。
var ErrUnsupported = errors.New("当前平台不支持原生文件选择，请手动填写路径")

// ErrCanceled 用户关掉了对话框（不是错误）。
var ErrCanceled = errors.New("已取消选择")

// Folder 弹出目录选择框，返回选中的绝对路径。
func Folder(title, initial string) (string, error) {
	return pickFolder(title, initial)
}

// File 弹出文件选择框，返回选中的绝对路径。
//
// filter 用 Windows 习惯的「显示名|模式」写法，例如 `可执行文件 (*.exe)|*.exe`；
// 非 Windows 平台会尽力转换为对应实现能理解的参数，转不了就退化成「不过滤」。
func File(title, initial, filter string) (string, error) {
	return pickFile(title, initial, filter)
}
