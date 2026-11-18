//go:build windows

// Package tray 提供一个极简的 Windows 系统托盘（纯 syscall 调 Win32，无 cgo、无第三方依赖）。
//
// 之所以自己写：Wails v2 没有托盘能力，而第三方托盘库基本都需要 C 编译器。
// 这里的实现只覆盖本项目需要的四件事：一个图标、右键菜单、左键回调、提示文本。
package tray

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// Item 一个菜单项。
type Item struct {
	Title   string
	Sep     bool
	OnClick func()
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procCreatePopupMenu      = user32.NewProc("CreatePopupMenu")
	procAppendMenuW          = user32.NewProc("AppendMenuW")
	procDestroyMenu          = user32.NewProc("DestroyMenu")
	procTrackPopupMenu       = user32.NewProc("TrackPopupMenu")
	procGetCursorPos         = user32.NewProc("GetCursorPos")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procLoadIconW            = user32.NewProc("LoadIconW")
	procRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy   = 0x0002
	wmClose     = 0x0010
	wmRButtonUp = 0x0205
	wmLButtonUp = 0x0202
	wmContext   = 0x007B
	wmApp       = 0x8000

	nimAdd    = 0x00000000
	nimDelete = 0x00000002
	nimSetVer = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	tpmReturn   = 0x0100
	tpmRightBtn = 0x0002

	idiApplication = 32512
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             syscall.Handle
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            syscall.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         guid
	hBalloonIcon     syscall.Handle
}

type point struct{ X, Y int32 }

type msg struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

var (
	callbackMessage = uint32(wmApp + 1)
	taskbarCreated  uint32

	hwndMain syscall.Handle
	items    []Item
	onLeft   func()
	tipText  string
	running  bool
)

// Run 启动托盘。onLeftClick 可为 nil。返回的 stop 用于移除图标并退出消息循环。
func Run(tooltip string, menuItems []Item, onLeftClick func()) (stop func(), err error) {
	if running {
		return func() {}, nil
	}
	items = menuItems
	onLeft = onLeftClick
	tipText = tooltip

	ready := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		// Win32 消息循环必须固定在同一个 OS 线程上
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if e := runLoop(); e != nil {
			ready <- e
			close(done)
			return
		}
		ready <- nil
		close(done)
	}()

	if e := <-ready; e != nil {
		return func() {}, e
	}
	running = true
	stop = func() {
		if hwndMain != 0 {
			procPostMessageW.Call(uintptr(hwndMain), wmClose, 0, 0)
		}
		<-done
		running = false
	}
	return stop, nil
}

// SetTooltip 更新托盘悬停提示（可用于显示「N 实例 · 78 t/s · 9.4GB」）。
func SetTooltip(text string) {
	if hwndMain == 0 {
		return
	}
	tipText = text
	nid := newNID()
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
}

func newNID() notifyIconDataW {
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwndMain
	nid.uID = 1
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = callbackMessage
	icon, _, _ := procLoadIconW.Call(0, idiApplication)
	nid.hIcon = syscall.Handle(icon)
	if tip, err := syscall.UTF16FromString(tipText); err == nil {
		copy(nid.szTip[:], tip)
	}
	return nid
}

func runLoop() error {
	hInstance, _, _ := procGetModuleHandleW.Call(0)
	className, err := syscall.UTF16PtrFromString("AIRunnerTrayWindow")
	if err != nil {
		return err
	}

	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     syscall.Handle(hInstance),
		lpszClassName: className,
	}
	if r, _, e := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		// 类可能已注册（重复初始化），继续尝试创建窗口
		_ = e
	}

	hwnd, _, winErr := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0, 0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("创建托盘窗口失败: %v", winErr)
	}
	hwndMain = syscall.Handle(hwnd)

	nid := newNID()
	if r, _, e := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return fmt.Errorf("注册托盘图标失败: %v", e)
	}

	if tbMsg, e := syscall.UTF16PtrFromString("TaskbarCreated"); e == nil {
		v, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(tbMsg)))
		taskbarCreated = uint32(v)
	}

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	nidDelete := newNID()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nidDelete)))
	hwndMain = 0
	return nil
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch uint32(message) {
	case callbackMessage:
		event := uint32(lParam & 0xFFFF)
		switch event {
		case wmLButtonUp:
			if onLeft != nil {
				go onLeft()
			}
		case wmRButtonUp, wmContext:
			showMenu()
		}
		return 0
	case wmClose, wmDestroy:
		procDestroyWindow.Call(hwnd)
		if uint32(message) == wmDestroy {
			procPostQuitMessage.Call(0)
		}
		return 0
	default:
		if taskbarCreated != 0 && uint32(message) == taskbarCreated {
			// 资源管理器重启后重新挂上图标
			nid := newNID()
			procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
			return 0
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return r
}

func showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	idMap := map[uintptr]func(){}
	id := uintptr(1000)
	for _, it := range items {
		if it.Sep {
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		text, e := syscall.UTF16PtrFromString(it.Title)
		if e != nil {
			continue
		}
		procAppendMenuW.Call(menu, mfString, id, uintptr(unsafe.Pointer(text)))
		if it.OnClick != nil {
			idMap[id] = it.OnClick
		}
		id++
	}

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(uintptr(hwndMain))
	cmd, _, _ := procTrackPopupMenu.Call(menu,
		tpmReturn|tpmRightBtn, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwndMain), 0)
	if fn, ok := idMap[cmd]; ok {
		go fn()
	}
}
