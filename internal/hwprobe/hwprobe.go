// Package hwprobe 采集本机硬件画像（GPU / 专用显存 / CPU / 内存 / 磁盘 / 可用后端）。
//
// 设计约束（对齐设计方案 §4.1）：
//   - 全本地：只读 nvidia-smi / WMI / Windows API，不联网、不依赖任何 AI；
//   - 只做一条路径：本机是 NVIDIA 卡，因此优先 NVML 口径（nvidia-smi 查询），
//     读不到时降级为 WMI，并明确标注数据来源（Source 字段）；
//   - 结果缓存进配置，界面可「重新自检」。
package hwprobe

import (
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// GPU 单块显卡。
type GPU struct {
	Name            string `json:"name"`
	Vendor          string `json:"vendor"` // NVIDIA / AMD / Intel / Unknown
	Driver          string `json:"driver,omitempty"`
	VRAMDedicatedMB int    `json:"vram_dedicated_mb"` // 专用显存（预算基数）
	VRAMUsedMB      int    `json:"vram_used_mb"`      // 当前占用
	VRAMFreeMB      int    `json:"vram_free_mb"`
	Source          string `json:"source"`  // nvidia-smi / wmi / manual
	Backend         string `json:"backend"` // cuda / vulkan / cpu
	SharedMemoryMB  int    `json:"shared_memory_mb,omitempty"`
}

// CPU 处理器信息。
type CPU struct {
	Name            string   `json:"name"`
	PhysicalCores   int      `json:"physical_cores"`
	LogicalCores    int      `json:"logical_cores"`
	InstructionSets []string `json:"instruction_sets,omitempty"`
}

// Memory 内存信息。
type Memory struct {
	TotalMB int `json:"total_mb"`
	AvailMB int `json:"avail_mb"`
}

// Disk 磁盘剩余空间。
type Disk struct {
	Path    string  `json:"path"`
	TotalGB float64 `json:"total_gb"`
	FreeGB  float64 `json:"free_gb"`
}

// Profile 硬件画像全量。
type Profile struct {
	CapturedAt time.Time `json:"captured_at"`
	OS         string    `json:"os"`
	Hostname   string    `json:"hostname"`
	GPUs       []GPU     `json:"gpus"`
	CPU        CPU       `json:"cpu"`
	Memory     Memory    `json:"memory"`
	Disks      []Disk    `json:"disks"`
	Backends   []string  `json:"backends"`
	Notes      []string  `json:"notes,omitempty"`
}

// PrimaryGPU 返回第一块独显（优先 NVIDIA 且显存最大者）。
func (p *Profile) PrimaryGPU() *GPU {
	if p == nil || len(p.GPUs) == 0 {
		return nil
	}
	best := 0
	for i := range p.GPUs {
		if p.GPUs[i].VRAMDedicatedMB > p.GPUs[best].VRAMDedicatedMB {
			best = i
		}
	}
	return &p.GPUs[best]
}

// BudgetMB 计算可用于放模型的显存预算。
//
//	可用 = 专用显存 − 安全余量 − 当前占用
func (p *Profile) BudgetMB(reserveMB int) int {
	g := p.PrimaryGPU()
	if g == nil {
		return 0
	}
	b := g.VRAMDedicatedMB - reserveMB - g.VRAMUsedMB
	if b < 0 {
		return 0
	}
	return b
}

// HasNVIDIA 是否检测到可用的 CUDA 路径。
func (p *Profile) HasNVIDIA() bool {
	g := p.PrimaryGPU()
	return g != nil && strings.EqualFold(g.Vendor, "NVIDIA") && g.Source == "nvidia-smi"
}

// Summary 给界面用的一行摘要。
func (p *Profile) Summary() string {
	if p == nil {
		return "尚未自检"
	}
	g := p.PrimaryGPU()
	if g == nil {
		return fmt.Sprintf("%s · %d 核 · 内存 %.1fGB", p.CPU.Name, p.CPU.LogicalCores, float64(p.Memory.TotalMB)/1024)
	}
	return fmt.Sprintf("%s %.1fGB(用 %.1fGB) · %s %d 核 · 内存 %.1fGB",
		g.Name, float64(g.VRAMDedicatedMB)/1024, float64(g.VRAMUsedMB)/1024,
		shortCPU(p.CPU.Name), p.CPU.LogicalCores, float64(p.Memory.TotalMB)/1024)
}

func shortCPU(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "Intel(R) ")
	name = strings.TrimPrefix(name, "AMD ")
	name = regexp.MustCompile(`\s+`).ReplaceAllString(name, " ")
	if len(name) > 28 {
		name = name[:28]
	}
	return name
}

// Probe 执行一次硬件自检。
func Probe() (*Profile, error) {
	p := &Profile{CapturedAt: time.Now(), OS: runtime.GOOS + "/" + runtime.GOARCH}
	if hn, err := hostname(); err == nil {
		p.Hostname = hn
	}
	if err := probePlatform(p); err != nil {
		return p, err
	}
	finalize(p)
	return p, nil
}

// finalize 补全后端判定与统计信息。
func finalize(p *Profile) {
	backends := map[string]bool{"cpu": true}
	for i := range p.GPUs {
		g := &p.GPUs[i]
		if g.Backend == "" {
			switch {
			case strings.EqualFold(g.Vendor, "NVIDIA") && g.Source == "nvidia-smi":
				g.Backend = "cuda"
			case g.VRAMDedicatedMB > 0:
				g.Backend = "vulkan"
			default:
				g.Backend = "cpu"
			}
		}
		backends[g.Backend] = true
	}
	order := []string{"cuda", "vulkan", "cpu"}
	p.Backends = p.Backends[:0]
	for _, b := range order {
		if backends[b] {
			p.Backends = append(p.Backends, b)
		}
	}
	if p.CPU.LogicalCores == 0 {
		p.CPU.LogicalCores = 1
	}
	if p.CPU.PhysicalCores == 0 {
		p.CPU.PhysicalCores = p.CPU.LogicalCores
	}
	if !p.HasNVIDIA() {
		p.Notes = append(p.Notes, "未通过 nvidia-smi 读到 NVIDIA 显存，推荐参数将使用保守档位估算")
	}
}

// Mock 生成一份用于测试/演示的画像（AIRUNNER_FAKE_HW 环境变量）。
func Mock(spec string) *Profile {
	// 形如 "RTX 4060,8192,2048"
	parts := strings.Split(spec, ",")
	name := "Mock GPU"
	total := 8192
	used := 512
	if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
		name = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		if v, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
			total = v
		}
	}
	if len(parts) > 2 {
		if v, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil {
			used = v
		}
	}
	p := &Profile{
		CapturedAt: time.Now(),
		OS:         runtime.GOOS,
		Hostname:   "mock",
		GPUs: []GPU{{
			Name: name, Vendor: "NVIDIA", Driver: "mock",
			VRAMDedicatedMB: total, VRAMUsedMB: used, VRAMFreeMB: total - used,
			Source: "nvidia-smi",
		}},
		CPU:    CPU{Name: "Mock CPU", PhysicalCores: 8, LogicalCores: 16},
		Memory: Memory{TotalMB: 32768, AvailMB: 16384},
		Notes:  []string{"使用 AIRUNNER_FAKE_HW 模拟硬件"},
	}
	finalize(p)
	return p
}

// ProbeLight 轻量探测：只查显存总量/占用，供指标曲线高频采样（每 3 秒一次）。
// 不触发 WMI / PowerShell，避免采样本身成为负担。
func ProbeLight() (*Profile, error) {
	out, err := runTimeout(3*time.Second, "nvidia-smi",
		"--query-gpu=name,memory.total,memory.used,memory.free",
		"--format=csv,noheader,nounits")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil, fmt.Errorf("无法读取显存占用")
	}
	p := &Profile{CapturedAt: time.Now()}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}
		p.GPUs = append(p.GPUs, GPU{
			Name:            strings.TrimSpace(fields[0]),
			Vendor:          "NVIDIA",
			VRAMDedicatedMB: atoiDefault(fields[1], 0),
			VRAMUsedMB:      atoiDefault(fields[2], 0),
			VRAMFreeMB:      atoiDefault(fields[3], 0),
			Source:          "nvidia-smi",
			Backend:         "cuda",
		})
	}
	if len(p.GPUs) == 0 {
		return nil, fmt.Errorf("未检测到 NVIDIA 显卡")
	}
	return p, nil
}

// runTimeout 执行外部命令并带超时。
func runTimeout(timeout time.Duration, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
		return string(out), err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return "", fmt.Errorf("%s 执行超时", name)
	}
}

func atoiDefault(s string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return v
}

func toFloatDefault(s string, def float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return def
	}
	return v
}
