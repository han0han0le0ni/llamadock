//go:build windows

package llamacpp

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

// CREATE_NEW_PROCESS_GROUP：让子进程拥有独立进程组，便于整体清理。
const createNewProcessGroup = 0x00000200

// setProcAttr 隐藏子进程控制台窗口（GUI 程序拉起命令行进程时不留黑框）。
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup,
	}
}

// killTree 结束后端进程（含可能派生的子进程）。
func killTree(pid int) {
	if pid <= 0 {
		return
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	setProcAttr(kill)
	if err := kill.Run(); err == nil {
		return
	}
	// taskkill 不可用时退回到直接终止
	if p, err := os.FindProcess(pid); err == nil && p != nil {
		_ = p.Kill()
	}
}

// --- 进程内存占用 ---

var (
	psapi                = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemory = psapi.NewProc("GetProcessMemoryInfo")
	kernel32Proc         = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess      = kernel32Proc.NewProc("OpenProcess")
	procCloseHandle      = kernel32Proc.NewProc("CloseHandle")
)

type processMemoryCounters struct {
	Cb                      uint32
	PageFaultCount          uint32
	PeakWorkingSetSize      uintptr
	WorkingSetSize          uintptr
	QuotaPeakPagedPoolUsage uintptr
	QuotaPagedPoolUsage     uintptr
	QuotaPeakNonPagedUsage  uintptr
	QuotaNonPagedPoolUsage  uintptr
	PagefileUsage           uintptr
	PeakPagefileUsage       uintptr
}

// processRSSMB 读取进程常驻内存（MB）；失败返回 0。
func processRSSMB(pid int) int {
	if pid <= 0 {
		return 0
	}
	const processQueryLimitedInformation = 0x1000
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return 0
	}
	defer procCloseHandle.Call(h)
	var pmc processMemoryCounters
	pmc.Cb = uint32(unsafe.Sizeof(pmc))
	r, _, _ := procGetProcessMemory.Call(h, uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.Cb))
	if r == 0 {
		return 0
	}
	return int(pmc.WorkingSetSize / (1024 * 1024))
}
