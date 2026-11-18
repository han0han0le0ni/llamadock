//go:build !windows

package sdcpp

import (
	"os"
	"os/exec"
	"syscall"
)

func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(pid int) {
	if pid <= 0 {
		return
	}
	// 负 PID =  whole process group
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if p, err := os.FindProcess(pid); err == nil && p != nil {
		_ = p.Kill()
	}
}
