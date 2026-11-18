//go:build !windows

package hwprobe

import (
	"os"
	"runtime"
)

// probePlatform 非 Windows 平台的最小实现：仅给出 CPU/内存的粗略画像。
// （本项目目标平台为 Windows，此文件只保证可编译与可测试。）
func probePlatform(p *Profile) error {
	p.CPU = CPU{Name: runtime.GOARCH, PhysicalCores: 4, LogicalCores: 4}
	p.Memory = Memory{TotalMB: 8192, AvailMB: 4096}
	return nil
}

func hostname() (string, error) { return os.Hostname() }
