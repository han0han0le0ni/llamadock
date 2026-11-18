// Package advisor 是参数推荐引擎：由硬件画像 + GGUF 元数据**纯算术**推导推荐参数。
//
// 四条设计原则（设计方案 §4.2）：
//  1. 确定性：同一台机器 + 同一个模型 → 永远得到同一组推荐值（可复现、可单测）
//  2. 可解释：每条推荐都附算式与代入数值，用户可以自己核对
//  3. 可覆盖：推荐值只是预填默认，用户随时手改，不隐藏不锁定
//  4. 保守优先：宁可少卸载几层到 GPU，也不要 OOM
//
// 本包不做任何 AI / 机器学习推断，全部是四则运算。
package advisor

import (
	"fmt"
	"math"
	"strings"

	"airunner/internal/gguf"
	"airunner/internal/hwprobe"
)

// Reason 单条推荐的算式依据。
type Reason struct {
	Key    string   `json:"key"`
	Label  string   `json:"label"`
	Value  any      `json:"value"`
	Lines  []string `json:"lines"`
	Source string   `json:"source,omitempty"`
}

// Recommendation 一次完整推荐。
type Recommendation struct {
	Model            string         `json:"model"`
	Arch             string         `json:"arch"`
	Purpose          string         `json:"purpose"`
	Values           map[string]any `json:"values"`
	Reasons          []Reason       `json:"reasons"`
	Notes            []string       `json:"notes,omitempty"`
	BudgetMB         int            `json:"budget_mb"`
	ModelMB          int            `json:"model_mb"`
	PerLayerMB       float64        `json:"per_layer_mb"`
	KVPerTokenKB     float64        `json:"kv_per_token_kb"`
	KVTotalMB        float64        `json:"kv_total_mb"`
	EstimatedTotalMB float64        `json:"estimated_total_mb"`
}

// Options 推荐选项。
type Options struct {
	ReserveMB       int     // 显存安全余量（默认 512）
	Purpose         string  // code / chat / creative / long
	Conservative    float64 // 保守系数，>1 更保守（默认 1.0）
	ThreadCap       int     // 线程上限（默认 16）
	TargetLoadedMB  int     // 目标模型当前已占用的显存（用于换算「先卸载再加载」的可用预算）
}

func (o *Options) norm() {
	if o.ReserveMB <= 0 {
		o.ReserveMB = 512
	}
	if o.Purpose == "" {
		o.Purpose = "chat"
	}
	if o.Conservative < 1 {
		o.Conservative = 1
	}
	if o.ThreadCap <= 0 {
		o.ThreadCap = 16
	}
	if o.TargetLoadedMB < 0 {
		o.TargetLoadedMB = 0
	}
}

// PurposePreset 用途 → 采样预设名。
func PurposePreset(purpose string) string {
	switch strings.ToLower(purpose) {
	case "code", "math", "extract", "精确":
		return "精确"
	case "creative", "创意":
		return "创意"
	case "long", "rag", "长文":
		return "长文"
	default:
		return "平衡"
	}
}

// Recommend 主入口。
func Recommend(p *hwprobe.Profile, m *gguf.Meta, o Options) *Recommendation {
	o.norm()
	rec := &Recommendation{Values: map[string]any{}, Purpose: o.Purpose}
	if m != nil {
		rec.Model = m.Name
		if rec.Model == "" {
			rec.Model = m.Path
		}
		rec.Arch = m.Arch
		rec.ModelMB = m.SizeMB
	}
	if p == nil {
		rec.Notes = append(rec.Notes, "尚无硬件自检结果，无法推导推荐值")
		return rec
	}
	g := p.PrimaryGPU()
	if g == nil {
		rec.Notes = append(rec.Notes, "未检测到可用显卡，按 CPU 推理给出建议")
	}

	// ① 后端与可用显存
	var dedMB, usedMB int
	if g != nil {
		dedMB, usedMB = g.VRAMDedicatedMB, g.VRAMUsedMB
	}
	// 目标模型已加载时，把它自己的占用从「当前占用」中扣除：
	// 否则会得出「这个模型自己占满了显存，所以只能放 4 层」这种自相矛盾的结论。
	otherUsedMB := usedMB
	if o.TargetLoadedMB > 0 {
		otherUsedMB = usedMB - o.TargetLoadedMB
		if otherUsedMB < 0 {
			otherUsedMB = 0
		}
	}
	budget := float64(dedMB-o.ReserveMB-otherUsedMB) / o.Conservative
	if budget < 0 {
		budget = 0
	}
	// CPU-only 或读不到显存时，退化为按内存约束的保守档位
	cpuOnly := g == nil || dedMB == 0 || (g.Source == "wmi" && dedMB < 2048)
	if cpuOnly {
		budget = math.Max(0, float64(p.Memory.AvailMB-2048)/o.Conservative)
	}
	rec.BudgetMB = int(budget)

	budgetLines := []string{
		fmt.Sprintf("专用显存 %.1f GB − 安全余量 %d MB − 当前占用 %.1f GB%s",
			float64(dedMB)/1024, o.ReserveMB, float64(otherUsedMB)/1024, conservativeSuffix(o.Conservative)),
		fmt.Sprintf("= %.1f GB", budget/1024),
	}
	if o.TargetLoadedMB > 0 {
		budgetLines = append(budgetLines, fmt.Sprintf(
			"（该模型当前已加载，实测占用约 %.1f GB 已从预算中扣除，等价于「先卸载再加载」）",
			float64(o.TargetLoadedMB)/1024))
	}
	rec.Reasons = append(rec.Reasons, Reason{
		Key: "vram_budget", Label: "可用显存预算", Value: fmt.Sprintf("%.1f GB", budget/1024),
		Lines:  budgetLines,
		Source: gpuSource(p),
	})

	if m == nil || m.SizeMB == 0 {
		rec.Notes = append(rec.Notes, "模型元数据缺失，只给出与模型无关的建议")
		applyThreads(rec, p, o, g)
		applySampling(rec, o.Purpose)
		return rec
	}

	// ② cache_type_k / cache_type_v（先定它，因为 KV 大小依赖它）
	ratio := 0.0
	if rec.ModelMB > 0 {
		ratio = budget / float64(rec.ModelMB)
	}
	cache := "q4_0"
	switch {
	case ratio >= 2.0:
		cache = "f16"
	case ratio >= 1.5:
		cache = "q8_0"
	}
	rec.Values["cache_type_k"] = cache
	rec.Values["cache_type_v"] = cache
	rec.Reasons = append(rec.Reasons, Reason{
		Key: "cache_type", Label: "KV 缓存量化", Value: cache,
		Lines: []string{
			fmt.Sprintf("可用显存 / 模型大小 = %.1f GB / %.1f GB = %.2f 倍", budget/1024, float64(rec.ModelMB)/1024, ratio),
			"≥2.0 → f16（质量优先）｜ ≥1.5 → q8_0（甜点）｜ 否则 → q4_0（省显存优先）",
		},
	})

	// ③ n_ctx（KV 预算反推，而不是直接取模型上限）
	kvBytes := gguf.BytesOfQuant(cache)
	kvPerTokenBytes := kvPerToken(m, kvBytes)
	kvBudgetMB := math.Max(0, budget-float64(rec.ModelMB)*1.02)
	ctxMax := 0
	if kvPerTokenBytes > 0 {
		ctxMax = int(kvBudgetMB * 1024 * 1024 / kvPerTokenBytes)
	}
	native := m.NativeCtx
	if native <= 0 {
		native = 4096
	}
	nctx := clampDownPow2(minInt(native, ctxMax), 512)
	if nctx < 512 {
		nctx = 512
	}
	rec.Values["n_ctx"] = nctx
	rec.KVPerTokenKB = kvPerTokenBytes / 1024
	rec.Reasons = append(rec.Reasons, Reason{
		Key: "n_ctx", Label: "上下文长度", Value: nctx,
		Lines: []string{
			fmt.Sprintf("kv/token = 2 × %d 层 × (%d/%d × %d) × %.3f 字节 ≈ %.1f KB",
				m.Layers, m.EmbDim, maxInt(m.Heads, 1), maxInt(m.HeadsKV, 1), kvBytes, kvPerTokenBytes/1024),
			fmt.Sprintf("KV 预算 = %.1f GB − 模型 %.1f GB × 1.02 = %.2f GB",
				budget/1024, float64(rec.ModelMB)/1024, kvBudgetMB/1024),
			fmt.Sprintf("%.2f GB ÷ %.1f KB ≈ %d → 取 2 的幂 = %d（模型原生上限 %d）",
				kvBudgetMB/1024, kvPerTokenBytes/1024, ctxMax, nctx, native),
		},
	})

	// ④ n_gpu_layers（核心算术）
	layers := m.Layers
	if layers <= 0 {
		layers = 32
	}
	perLayer := float64(rec.ModelMB) / float64(layers)
	rec.PerLayerMB = perLayer
	kvTotalMB := float64(nctx) * kvPerTokenBytes / (1024 * 1024)
	rec.KVTotalMB = kvTotalMB
	layersMax := 0
	if perLayer > 0 {
		layersMax = int(math.Floor((budget - kvTotalMB) / perLayer))
	}
	nGL := layers
	if cpuOnly {
		nGL = 0
	} else {
		nGL = minInt(layers, int(float64(layersMax)*0.95))
	}
	if nGL < 0 {
		nGL = 0
	}
	rec.Values["n_gpu_layers"] = nGL
	rec.Reasons = append(rec.Reasons, Reason{
		Key: "n_gpu_layers", Label: "GPU 卸载层数", Value: fmt.Sprintf("%d / %d", nGL, layers),
		Lines: []string{
			fmt.Sprintf("模型 %.1f GB ÷ %d 层 = %.0f MB/层", float64(rec.ModelMB)/1024, layers, perLayer),
			fmt.Sprintf("可用显存 %.1f GB − KV %.2f GB = %.2f GB", budget/1024, kvTotalMB/1024, (budget-kvTotalMB)/1024),
			fmt.Sprintf("⌊%.2f GB ÷ %.0f MB⌋ = %d 层 → 应用 5%% 安全收敛 → %d 层",
				(budget-kvTotalMB)/1024, perLayer, layersMax, nGL),
		},
		Source: gpuSource(p),
	})

	// ⑤ 批处理与线程
	rec.Values["n_batch"] = batchFor(cpuOnly, p.Memory.TotalMB)
	rec.Values["n_ubatch"] = rec.Values["n_batch"]
	applyThreads(rec, p, o, g)

	// ⑥ 加速开关
	flash := "off"
	if !cpuOnly {
		flash = "on"
		if cache == "q4_0" && o.Conservative > 1.1 {
			flash = "auto"
		}
	}
	rec.Values["flash_attn"] = flash
	rec.Values["mmap"] = true
	rec.Values["mlock"] = false

	// ⑦ 采样参数（与硬件无关，按用途给预设）
	applySampling(rec, o.Purpose)

	// ⑧ 预估总量
	rec.EstimatedTotalMB = float64(rec.ModelMB)*1.02 + kvTotalMB + float64(rec.Values["n_batch"].(int))*float64(maxInt(m.EmbDim, 1024))*4*2/(1024*1024)
	rec.Notes = append(rec.Notes, archNotes(m)...)
	if nGL < layers {
		rec.Notes = append(rec.Notes, fmt.Sprintf("显存只够放下 %d/%d 层，其余 %d 层在 CPU 上，速度会明显下降；想全放 GPU 可降低 n_ctx 或改用更小量化。", nGL, layers, layers-nGL))
	}
	return rec
}

func conservativeSuffix(c float64) string {
	if c > 1.0 {
		return fmt.Sprintf("，再除以保守系数 %.2f", c)
	}
	return ""
}

func gpuSource(p *hwprobe.Profile) string {
	g := p.PrimaryGPU()
	if g == nil {
		return "无显卡"
	}
	if g.Source == "nvidia-smi" {
		return fmt.Sprintf("%s，专用显存 %.1f GB，当前已占用 %.1f GB（来源：nvidia-smi）",
			g.Name, float64(g.VRAMDedicatedMB)/1024, float64(g.VRAMUsedMB)/1024)
	}
	return fmt.Sprintf("%s（来源：WMI，显存数值可能不准，已按保守档位估算）", g.Name)
}

func kvPerToken(m *gguf.Meta, bytesPerElem float64) float64 {
	heads := maxInt(m.Heads, 1)
	headsKV := maxInt(m.HeadsKV, heads)
	dim := m.EmbDim
	if dim == 0 {
		dim = 4096
	}
	layers := m.Layers
	if layers == 0 {
		layers = 32
	}
	headDim := float64(dim) / float64(heads)
	return 2 * float64(layers) * headDim * float64(headsKV) * bytesPerElem
}

func batchFor(cpuOnly bool, totalMemMB int) int {
	if !cpuOnly {
		return 512
	}
	if totalMemMB >= 16*1024 {
		return 256
	}
	return 128
}

func applyThreads(rec *Recommendation, p *hwprobe.Profile, o Options, g *hwprobe.GPU) {
	cores := p.CPU.PhysicalCores
	if cores == 0 {
		cores = p.CPU.LogicalCores
	}
	n := minInt(cores, o.ThreadCap)
	if n < 1 {
		n = 4
	}
	rec.Values["n_threads"] = n
	rec.Values["n_threads_batch"] = n
	rec.Reasons = append(rec.Reasons, Reason{
		Key: "n_threads", Label: "CPU 线程数", Value: n,
		Lines: []string{
			fmt.Sprintf("物理核 %d，上限 %d（超过后收益递减）", cores, o.ThreadCap),
		},
		Source: p.CPU.Name,
	})
	_ = g
}

func applySampling(rec *Recommendation, purpose string) {
	switch PurposePreset(purpose) {
	case "精确":
		rec.Values["temperature"] = 0.2
		rec.Values["top_p"] = 0.9
		rec.Values["repeat_penalty"] = 1.05
	case "创意":
		rec.Values["temperature"] = 1.1
		rec.Values["top_p"] = 0.98
		rec.Values["repeat_penalty"] = 1.05
	case "长文":
		rec.Values["temperature"] = 0.6
		rec.Values["top_p"] = 0.9
		rec.Values["repeat_penalty"] = 1.15
	default:
		rec.Values["temperature"] = 0.7
		rec.Values["top_p"] = 0.95
		rec.Values["repeat_penalty"] = 1.1
	}
	rec.Reasons = append(rec.Reasons, Reason{
		Key: "temperature", Label: "采样预设", Value: rec.Values["temperature"],
		Lines: []string{
			fmt.Sprintf("按用途「%s」套用预设：temperature %v / top_p %v / repeat_penalty %v",
				PurposePreset(purpose), rec.Values["temperature"], rec.Values["top_p"], rec.Values["repeat_penalty"]),
			"采样参数与硬件无关，可随时手改",
		},
	})
}

func archNotes(m *gguf.Meta) []string {
	var out []string
	arch := strings.ToLower(m.Arch)
	switch {
	case m.MoE:
		out = append(out, "该模型是 MoE 架构，专家层的常驻显存计算方式与稠密模型不同，预估值仅供参考（偏保守）")
	case strings.Contains(arch, "deepseek"):
		out = append(out, "该模型使用 MLA 注意力，KV 被大幅压缩，本公式会**高估** KV 占用，实际可开更长上下文")
	case strings.Contains(arch, "qwen3"), strings.Contains(arch, "mistral"), strings.Contains(arch, "phi"):
		out = append(out, "该架构可能使用滑动窗口注意力，长上下文的 KV 增长不是线性的，预估偏保守")
	}
	if m.HeadsKV > 0 && m.HeadsKV < m.Heads {
		out = append(out, fmt.Sprintf("使用 GQA（%d 个 KV head / %d 个 query head），KV 估算已按此计算", m.HeadsKV, m.Heads))
	}
	return out
}

func clampDownPow2(v, minV int) int {
	if v <= minV {
		return minV
	}
	p := minV
	for p*2 <= v {
		p *= 2
	}
	return p
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
