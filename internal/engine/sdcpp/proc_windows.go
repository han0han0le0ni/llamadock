//go:build windows

package sdcpp

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup,
	}
}

func killTree(pid int) {
	if pid <= 0 {
		return
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNewProcessGroup}
	if err := kill.Run(); err == nil {
		return
	}
	if p, err := os.FindProcess(pid); err == nil && p != nil {
		_ = p.Kill()
	}
}
