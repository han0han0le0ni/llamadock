//go:build !windows

package llamacpp

import "os/exec"

// setProcAttr 非 Windows 平台无额外进程属性。
func setProcAttr(cmd *exec.Cmd) {}

// killTree 非 Windows 平台由调用方用 Process.Kill 兜底。
func killTree(pid int) {}

// processRSSMB 非 Windows 平台暂不支持。
func processRSSMB(pid int) int { return 0 }
