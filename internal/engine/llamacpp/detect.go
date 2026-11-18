package llamacpp

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Runtime 一个可用的 llama.cpp 运行时。
type Runtime struct {
	Exe        string   `json:"exe"`
	Dir        string   `json:"dir"`
	Variant    string   `json:"variant"` // cuda12 / cuda / vulkan / cpu
	Version    string   `json:"version"`
	VendorDirs []string `json:"vendor_dirs,omitempty"`
	GPU        bool     `json:"gpu"`
	Source     string   `json:"source"` // bundled / custom / path
}

// variantRank 变体优先级（数值越大越好，实际选择还会看硬件）。
func variantRank(v string) int {
	switch v {
	case "cuda12":
		return 40
	case "cuda":
		return 30
	case "vulkan":
		return 20
	case "cpu":
		return 10
	}
	return 0
}

// DetectRuntimes 扫描本机可用的 llama.cpp 运行时。
//
// 探测顺序：
//  1. 用户显式指定的 exe 所在目录
//  2. 用户显式指定的运行时搜索目录（可多个，递归找 llama-server.exe）
//  3. 自带运行时的几个候选根目录（见 RuntimeRoots）
//  4. 系统 PATH 里的 llama-server
func DetectRuntimes(customDirs []string) []Runtime {
	var out []Runtime
	seen := map[string]bool{}
	add := func(rt Runtime) {
		key := strings.ToLower(rt.Exe)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, rt)
	}

	for _, dir := range customDirs {
		for _, exe := range scanForExe(dir, 3) {
			add(newRuntime(exe, "custom", nil, ""))
		}
	}

	for _, exe := range selfRuntimes() {
		add(newRuntime(exe, "bundled", nil, ""))
	}

	if p, err := exec.LookPath("llama-server.exe"); err == nil {
		add(newRuntime(p, "path", nil, ""))
	}

	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := variantRank(out[i].Variant), variantRank(out[j].Variant); ri != rj {
			return ri > rj
		}
		return compareVersion(out[i].Version, out[j].Version) > 0
	})
	return out
}

// newRuntime 组装运行时信息；variant 为空时从路径推断。
func newRuntime(exe, source string, vendorDirs []string, variant string) Runtime {
	dir := filepath.Dir(exe)
	lower := strings.ToLower(dir)
	if variant == "" {
		switch {
		case strings.Contains(lower, "cuda12"):
			variant = "cuda12"
		case strings.Contains(lower, "cuda"):
			variant = "cuda"
		case strings.Contains(lower, "vulkan"):
			variant = "vulkan"
		default:
			variant = "cpu"
		}
	}
	if vendorDirs == nil {
		// 自定义目录：把自己和同级的 vendor 目录都加进 PATH
		vendorDirs = []string{}
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() && strings.Contains(strings.ToLower(e.Name()), "vendor") {
					vendorDirs = append(vendorDirs, filepath.Join(dir, e.Name()))
				}
			}
		}
	}
	return Runtime{
		Exe:        exe,
		Dir:        dir,
		Variant:    variant,
		Version:    guessVersion(dir),
		VendorDirs: vendorDirs,
		GPU:        variant != "cpu",
		Source:     source,
	}
}

var versionRe = regexp.MustCompile(`(\d+\.\d+\.\d+|\d+\.\d+)`)

func guessVersion(dir string) string {
	if m := versionRe.FindString(filepath.Base(dir)); m != "" {
		return m
	}
	return ""
}

func compareVersion(a, b string) int {
	pa, pb := splitVersion(a), splitVersion(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := 0, 0
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func splitVersion(v string) []int {
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, _ := strconv.Atoi(strings.TrimSpace(p))
		out = append(out, n)
	}
	return out
}

// RuntimeRoots 自带运行时的候选根目录，按优先级从高到低。
//
// 为什么要好几个根：本程序有两条运行方式，程序目录并不总是同一个地方——
//   - 发行/构建产物：build\llamadock.exe，运行时就在它旁边（第一优先级）；
//   - 源码调试：go run . 的 exe 落在临时目录，此时只能靠工作目录找；
//   - 布局差异：构建脚本常把产物放进子目录（本项目的 build\），
//     于是工作目录下再看一层常见产物目录名。
//
// 只做「固定几个候选」，不做无限向上回溯、也不遍历任意子目录：那容易误捡到
// 别处的运行时，让「到底用了哪个」变得不可解释。
func RuntimeRoots() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		clean := filepath.Clean(p)
		key := strings.ToLower(clean)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, clean)
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		add(filepath.Join(dir, "runtime", "llama.cpp"))               // 标准落位
		add(filepath.Join(dir, "runtime"))                            // 整包解压到 runtime\ 的情况
		add(filepath.Join(filepath.Dir(dir), "runtime", "llama.cpp")) // exe 在 build\、runtime\ 在项目根
	}

	if cwd, err := os.Getwd(); err == nil {
		add(filepath.Join(cwd, "runtime", "llama.cpp")) // 直接在部署根目录跑
		add(filepath.Join(cwd, "runtime"))
		// 源码调试：exe 在临时目录，go run . 的工作目录是源码根，
		// 而构建产物（连运行时）在根目录的子目录里。
		for _, sub := range []string{"build", "dist", "bin", "out"} {
			add(filepath.Join(cwd, sub, "runtime", "llama.cpp"))
		}
	}
	return out
}

// selfRuntimes 在自带运行时的各个候选根目录里递归找 llama-server.exe。
func selfRuntimes() []string {
	var out []string
	seen := map[string]bool{}
	for _, root := range RuntimeRoots() {
		for _, exe := range scanForExe(root, 3) {
			key := strings.ToLower(exe)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, exe)
		}
	}
	return out
}

// DefaultRuntimeDir 推荐的运行时落位：程序目录\runtime\llama.cpp。
//
// 用户把官方 llama.cpp 的 Windows 压缩包解压到这个目录即可，本程序会自动找到，
// 不需要任何配置，也不需要装别的东西。它同时是内置安装的目标目录。
func DefaultRuntimeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return filepath.Join("runtime", "llama.cpp")
	}
	return filepath.Join(filepath.Dir(exe), "runtime", "llama.cpp")
}

func scanForExe(root string, maxDepth int) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if depth := len(strings.Split(rel, string(os.PathSeparator))); depth > maxDepth+1 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), "llama-server.exe") {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// PickBest 按硬件挑一个最合适的运行时；wantGPU 表示是否希望用 GPU 变体。
//
// 判定以 Variant 为准（cuda12 > cuda > vulkan > cpu）：GPU 布尔位只是派生信息，
// 两处都判断会出现「变体是 cuda 但标记为 false」这种自相矛盾的数据，故只认变体名。
func PickBest(rts []Runtime, wantGPU bool) (Runtime, bool) {
	if len(rts) == 0 {
		return Runtime{}, false
	}
	isGPU := func(rt Runtime) bool {
		return rt.GPU || variantRank(rt.Variant) >= variantRank("vulkan")
	}
	if wantGPU {
		best, idx := -1, -1
		for i, rt := range rts {
			if !isGPU(rt) {
				continue
			}
			if r := variantRank(rt.Variant); r > best {
				best, idx = r, i
			}
		}
		if idx >= 0 {
			return rts[idx], true
		}
	}
	// 退回 CPU 变体（比自己瞎猜一个 vulkan 更稳）
	for _, rt := range rts {
		if rt.Variant == "cpu" {
			return rt, true
		}
	}
	// 兜底：取变体优先级最高者
	best, idx := -1, 0
	for i, rt := range rts {
		if r := variantRank(rt.Variant); r > best {
			best, idx = r, i
		}
	}
	return rts[idx], true
}
