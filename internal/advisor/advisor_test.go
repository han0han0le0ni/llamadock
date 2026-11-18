package advisor

import (
	"testing"

	"airunner/internal/gguf"
	"airunner/internal/hwprobe"
)

func testProfile(vram, used int) *hwprobe.Profile {
	p := &hwprobe.Profile{
		GPUs: []hwprobe.GPU{{
			Name: "RTX 4060", Vendor: "NVIDIA", Source: "nvidia-smi",
			VRAMDedicatedMB: vram, VRAMUsedMB: used, VRAMFreeMB: vram - used,
		}},
		CPU:    hwprobe.CPU{Name: "Test CPU", PhysicalCores: 16, LogicalCores: 32},
		Memory: hwprobe.Memory{TotalMB: 32768, AvailMB: 20000},
	}
	return p
}

func testMeta(sizeMB int) *gguf.Meta {
	return &gguf.Meta{
		Path: "qwen2.5-7b-q4_k_m.gguf", SizeMB: sizeMB, Arch: "qwen2",
		Layers: 28, EmbDim: 3584, Heads: 28, HeadsKV: 4, NativeCtx: 32768,
		Quant: "Q4_K_M",
	}
}

// 推荐必须确定性：同一输入两次调用结果完全一致。
func TestRecommendDeterministic(t *testing.T) {
	a := Recommend(testProfile(8192, 2048), testMeta(4700), Options{ReserveMB: 512, Purpose: "chat"})
	b := Recommend(testProfile(8192, 2048), testMeta(4700), Options{ReserveMB: 512, Purpose: "chat"})
	if len(a.Values) != len(b.Values) {
		t.Fatalf("两次推荐字段数不同: %d vs %d", len(a.Values), len(b.Values))
	}
	for k, v := range a.Values {
		if b.Values[k] != v {
			t.Fatalf("参数 %s 不确定: %v vs %v", k, v, b.Values[k])
		}
	}
	if len(a.Reasons) == 0 {
		t.Fatal("必须给出算式依据")
	}
}

// 显存 8GB 放 4.7GB 模型：应能全量卸载或接近全量，且 n_ctx 不应超过原生上限。
func TestRecommend8GB(t *testing.T) {
	rec := Recommend(testProfile(8192, 512), testMeta(4700), Options{Purpose: "chat"})
	ngl := rec.Values["n_gpu_layers"].(int)
	if ngl < 20 {
		t.Fatalf("8GB 空闲下卸载层数过低: %d", ngl)
	}
	if got := rec.Values["n_ctx"].(int); got > 32768 || got < 512 {
		t.Fatalf("n_ctx 越界: %d", got)
	}
	if rec.Values["cache_type_k"].(string) != "q8_0" && rec.Values["cache_type_k"].(string) != "f16" {
		t.Fatalf("量化选择过保守: %v", rec.Values["cache_type_k"])
	}
}

// 显存紧张时必须降低 n_gpu_layers，并且绝不出现负数。
func TestRecommendTightVRAM(t *testing.T) {
	rec := Recommend(testProfile(8192, 7000), testMeta(14000), Options{Purpose: "chat"})
	ngl := rec.Values["n_gpu_layers"].(int)
	if ngl < 0 {
		t.Fatalf("层数不能为负: %d", ngl)
	}
	if ngl >= 32 {
		t.Fatalf("14GB 模型在仅剩 1.2GB 显存时不应全量卸载: %d", ngl)
	}
	if rec.Values["cache_type_k"].(string) != "q4_0" {
		t.Fatalf("紧张时应选 q4_0，实际 %v", rec.Values["cache_type_k"])
	}
}

// 保守系数必须真的更保守。
func TestConservative(t *testing.T) {
	normal := Recommend(testProfile(8192, 1024), testMeta(6000), Options{Purpose: "chat"})
	cons := Recommend(testProfile(8192, 1024), testMeta(6000), Options{Purpose: "chat", Conservative: 1.15})
	if cons.Values["n_gpu_layers"].(int) > normal.Values["n_gpu_layers"].(int) {
		t.Fatal("保守系数应让推荐值更小或相等")
	}
}

// 目标模型自己已加载时，不应把「它自己的占用」算成别人的占用。
func TestTargetLoadedExcluded(t *testing.T) {
	// 8GB 卡已被 6GB 的模型占满（其中模型本体 5GB）
	naive := Recommend(testProfile(8192, 6144), testMeta(5000), Options{Purpose: "chat"})
	aware := Recommend(testProfile(8192, 6144), testMeta(5000), Options{Purpose: "chat", TargetLoadedMB: 5500})
	if aware.Values["n_gpu_layers"].(int) <= naive.Values["n_gpu_layers"].(int) {
		t.Fatalf("扣除自身占用后可用层数应变多: naive=%v aware=%v",
			naive.Values["n_gpu_layers"], aware.Values["n_gpu_layers"])
	}
	if aware.BudgetMB <= naive.BudgetMB {
		t.Fatal("预算应更大")
	}
}

func TestClampDownPow2(t *testing.T) {
	cases := []struct{ in, want int }{{13000, 8192}, {8192, 8192}, {513, 512}, {1024, 1024}}
	for _, c := range cases {
		if got := clampDownPow2(c.in, 512); got != c.want {
			t.Fatalf("clampDownPow2(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
