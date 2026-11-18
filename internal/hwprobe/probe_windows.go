//go:build windows

package hwprobe

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/cpu"
)

// probePlatform 在 Windows 上采集硬件信息。
func probePlatform(p *Profile) error {
	probeNvidiaSMI(p)
	if len(p.GPUs) == 0 {
		probeWMIGPU(p)
	}
	probeWMICPU(p)
	probeMemory(p)
	probeDisks(p)
	if len(p.GPUs) == 0 {
		p.Notes = append(p.Notes, "未检测到显卡信息，将按 CPU 推理处理")
	}
	return nil
}

// probeNvidiaSMI 通过 nvidia-smi 读取精确的专用显存（区分专用/共享，>4GB 不溢出）。
func probeNvidiaSMI(p *Profile) {
	out, err := runTimeout(6*time.Second, "nvidia-smi",
		"--query-gpu=name,memory.total,memory.used,memory.free,driver_version",
		"--format=csv,noheader,nounits")
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}
		g := GPU{
			Name:            strings.TrimSpace(fields[0]),
			Vendor:          "NVIDIA",
			VRAMDedicatedMB: atoiDefault(fields[1], 0),
			VRAMUsedMB:      atoiDefault(fields[2], 0),
			VRAMFreeMB:      atoiDefault(fields[3], 0),
			Source:          "nvidia-smi",
			Backend:         "cuda",
		}
		if len(fields) >= 5 {
			g.Driver = strings.TrimSpace(fields[4])
		}
		p.GPUs = append(p.GPUs, g)
	}
}

type wmiGPU struct {
	Name          string `json:"Name"`
	DriverVersion string `json:"DriverVersion"`
	AdapterRAM    int64  `json:"AdapterRAM"`
}

// probeWMIGPU 无 nvidia-smi 时的降级路径。
//
// 注意：WMI 的 AdapterRAM 是 32 位，>4GB 会溢出，因此这里只用来判断型号与厂商，
// 显存数值标注来源为 wmi，推荐引擎会按「档位保守模式」处理。
func probeWMIGPU(p *Profile) {
	out, err := runTimeout(10*time.Second, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-CimInstance Win32_VideoController | Select-Object Name,DriverVersion,AdapterRAM | ConvertTo-Json -Compress")
	if err != nil {
		return
	}
	for _, item := range decodeJSONArray[wmiGPU](out) {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		upper := strings.ToUpper(name)
		vendor := "Unknown"
		switch {
		case strings.Contains(upper, "NVIDIA"):
			vendor = "NVIDIA"
		case strings.Contains(upper, "AMD"), strings.Contains(upper, "RADEON"):
			vendor = "AMD"
		case strings.Contains(upper, "INTEL"):
			vendor = "Intel"
		}
		mb := int(item.AdapterRAM) / (1024 * 1024)
		if mb < 0 {
			mb = 0
		}
		p.GPUs = append(p.GPUs, GPU{
			Name:            name,
			Vendor:          vendor,
			Driver:          strings.TrimSpace(item.DriverVersion),
			VRAMDedicatedMB: mb,
			Source:          "wmi",
		})
	}
}

type wmiCPU struct {
	Name                      string `json:"Name"`
	NumberOfCores             uint32 `json:"NumberOfCores"`
	NumberOfLogicalProcessors uint32 `json:"NumberOfLogicalProcessors"`
}

func probeWMICPU(p *Profile) {
	out, err := runTimeout(10*time.Second, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,NumberOfLogicalProcessors | ConvertTo-Json -Compress")
	if err != nil {
		return
	}
	items := decodeJSONArray[wmiCPU](out)
	if len(items) == 0 {
		return
	}
	p.CPU.Name = strings.TrimSpace(items[0].Name)
	for _, it := range items {
		p.CPU.PhysicalCores += int(it.NumberOfCores)
		p.CPU.LogicalCores += int(it.NumberOfLogicalProcessors)
	}
	if p.CPU.PhysicalCores == 0 {
		p.CPU.PhysicalCores = p.CPU.LogicalCores / 2
	}
	p.CPU.InstructionSets = detectInstructionSets()
}

// --- Windows 内存 / 磁盘 ---

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemStat = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetDiskFreeSp = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetLogicalDrv = kernel32.NewProc("GetLogicalDrives")
)

func probeMemory(p *Profile) {
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	r, _, _ := procGlobalMemStat.Call(uintptr(unsafe.Pointer(&ms)))
	if r == 0 {
		return
	}
	p.Memory.TotalMB = int(ms.TotalPhys / (1024 * 1024))
	p.Memory.AvailMB = int(ms.AvailPhys / (1024 * 1024))
}

// probeDisks 遍历所有逻辑盘，读取剩余空间。
func probeDisks(p *Profile) {
	mask, _, _ := procGetLogicalDrv.Call()
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + ":\\"
		free, total, ok := diskFree(root)
		if !ok || total == 0 {
			continue
		}
		p.Disks = append(p.Disks, Disk{
			Path:    root,
			TotalGB: float64(total) / (1024 * 1024 * 1024),
			FreeGB:  float64(free) / (1024 * 1024 * 1024),
		})
	}
}

func diskFree(root string) (free, total uint64, ok bool) {
	ptr, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return 0, 0, false
	}
	var freeBytes, totalBytes, totalFree uint64
	r, _, _ := procGetDiskFreeSp.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&freeBytes)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, 0, false
	}
	return totalFree, totalBytes, true
}

// detectInstructionSets 读取 CPU 指令集（x/sys/cpu 在包初始化时已探测完毕）。
func detectInstructionSets() []string {
	out := []string{}
	if cpu.X86.HasFMA {
		out = append(out, "FMA")
	}
	if cpu.X86.HasAVX {
		out = append(out, "AVX")
	}
	if cpu.X86.HasAVX2 {
		out = append(out, "AVX2")
	}
	if cpu.X86.HasAVX512F {
		out = append(out, "AVX512F")
	}
	if cpu.X86.HasAMXTile {
		out = append(out, "AMX")
	}
	return out
}

func hostname() (string, error) { return os.Hostname() }

// decodeJSONArray 兼容 ConvertTo-Json 输出「单个对象」或「数组」两种情况。
func decodeJSONArray[T any](raw string) []T {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var arr []T
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		return arr
	}
	var one T
	if err := json.Unmarshal([]byte(raw), &one); err == nil {
		return []T{one}
	}
	return nil
}
