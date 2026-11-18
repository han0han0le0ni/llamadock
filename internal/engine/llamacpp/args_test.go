package llamacpp

import (
	"strings"
	"testing"
)

func baseValues() map[string]any {
	return map[string]any{
		"model_path":      `E:\Models\qwen2.5-7b-q4_k_m.gguf`,
		"n_ctx":           8192,
		"n_gpu_layers":    28,
		"n_batch":         512,
		"n_ubatch":        256,
		"n_threads":       12,
		"n_threads_batch": 12,
		"cache_type_k":    "q8_0",
		"cache_type_v":    "q8_0",
		"flash_attn":      "on",
		"mmap":            true,
		"mlock":           false,
		"n_parallel":      1,
		"main_gpu":        0,
		"split_mode":      "layer",
		"numa":            "disabled",
		"rope_scaling":    "none",
		"cont_batching":   true,
		"offload_kqv":     true,
		"context_shift":   "keep_first",
	}
}

func argIndex(args []string, flag string) int {
	for i, a := range args {
		if a == flag {
			return i
		}
	}
	return -1
}

func argValue(args []string, flag string) string {
	i := argIndex(args, flag)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

// 加载级参数必须真的变成命令行开关——这就是「自己打开模型」的核心。
func TestBuildArgsMapsLoadParams(t *testing.T) {
	args := BuildArgs(baseValues())
	want := map[string]string{
		"-m":   `E:\Models\qwen2.5-7b-q4_k_m.gguf`,
		"-c":   "8192",
		"-ngl": "28",
		"-b":   "512",
		"-ub":  "256",
		"-t":   "12",
		"-tb":  "12",
		"-ctk": "q8_0",
		"-ctv": "q8_0",
		"-fa":  "on",
	}
	for flag, val := range want {
		if got := argValue(args, flag); got != val {
			t.Fatalf("%s 期望 %s，实际 %q（完整参数：%v）", flag, val, got, args)
		}
	}
	// mmap=true + mlock=false 是引擎默认，不应下发任何加载模式参数
	if argIndex(args, "--mmap") >= 0 || argIndex(args, "--no-mmap") >= 0 || argIndex(args, "--mlock") >= 0 {
		t.Fatalf("已废弃的内存开关不应再出现（%v）", args)
	}
	if argIndex(args, "--load-mode") >= 0 {
		t.Fatalf("默认加载模式不应下发 --load-mode（%v）", args)
	}
	if argIndex(args, "--cont-batching") < 0 {
		t.Fatal("连续批处理开启时应生成 --cont-batching")
	}
}

// 关闭态与默认态不应生成多余开关（否则会在某些构建上直接报错退出）。
func TestBuildArgsOmitsDefaults(t *testing.T) {
	v := baseValues()
	v["mlock"] = true
	v["no_kv_offload"] = true
	v["offload_kqv"] = false
	v["cache_reuse"] = 0
	v["rope_freq_base"] = 0
	v["n_parallel"] = 1
	v["mmap"] = false
	v["cont_batching"] = false
	args := BuildArgs(v)

	// 旧版的 --mmap/--no-mmap/--mlock 已被 --load-mode 取代，发了会直接启动失败
	if got := argValue(args, "--load-mode"); got != "mlock" {
		t.Fatalf("mmap=false + mlock=true 应生成 --load-mode mlock，实际 %q", got)
	}
	if argIndex(args, "--no-kv-offload") < 0 {
		t.Fatal("no_kv_offload/offload_kqv 关闭时应生成 --no-kv-offload")
	}
	if argIndex(args, "-np") >= 0 {
		t.Fatal("n_parallel=1 不应生成 -np")
	}
	if argIndex(args, "--cache-reuse") >= 0 {
		t.Fatal("cache_reuse=0 不应生成 --cache-reuse")
	}
	if argIndex(args, "--rope-freq-base") >= 0 {
		t.Fatal("rope_freq_base=0 表示用模型默认值，不应下发")
	}
	if argIndex(args, "--no-mmap") >= 0 {
		t.Fatal("--no-mmap 已在新版 llama.cpp 中移除，应改用 --load-mode")
	}
	if argIndex(args, "--no-cont-batching") < 0 {
		t.Fatal("cont_batching=false 应生成 --no-cont-batching")
	}
	if argIndex(args, "--numa") >= 0 {
		t.Fatal("numa=disabled 不应下发")
	}
}

// LoRA 与自定义模板。
func TestBuildArgsLoraAndTemplate(t *testing.T) {
	v := baseValues()
	v["lora"] = []string{`E:\lora\a.bin`, `E:\lora\b.bin`}
	v["lora_scale"] = 0.8
	args := BuildArgs(v)
	// -lm 在新版 llama.cpp 里是 --load-mode，多个适配器必须逗号连接成一个取值
	if n := strings.Count(strings.Join(args, " "), "--lora-scaled"); n != 1 {
		t.Fatalf("多个 LoRA 应合并为一个 --lora-scaled，实际 %d（%v）", n, args)
	}
	scaled := argValue(args, "--lora-scaled")
	if !strings.Contains(scaled, "0.8") || !strings.Contains(scaled, ",") {
		t.Fatalf("--lora-scaled 应用逗号连接多个适配器并带上缩放，实际 %q", scaled)
	}

	v2 := baseValues()
	v2["chat_template_mode"] = "jinja"
	v2["chat_template"] = "{% for m in messages %}{{ m.content }}{% endfor %}"
	args2 := BuildArgs(v2)
	if got := argValue(args2, "--chat-template"); !strings.Contains(got, "messages") {
		t.Fatalf("自定义模板未下发：%q", got)
	}
	// 内联 Jinja 模板必须同时开启 --jinja，否则新版 llama.cpp 拒绝启动
	if argIndex(args2, "--jinja") < 0 {
		t.Fatalf("下发自定义模板时必须同时下发 --jinja，实际参数：%v", args2)
	}

	// manual 模式同样需要 --jinja（走同一条下发路径）
	v2["chat_template_mode"] = "manual"
	args2 = BuildArgs(v2)
	if argIndex(args2, "--jinja") < 0 {
		t.Fatalf("manual 模式下发模板时也应有 --jinja，实际参数：%v", args2)
	}

	// auto 模式不应下发模板，也不应下发 --jinja
	v2["chat_template_mode"] = "auto"
	args2 = BuildArgs(v2)
	if argIndex(args2, "--chat-template") >= 0 {
		t.Fatal("auto 模式不应下发 --chat-template")
	}
	if argIndex(args2, "--jinja") >= 0 {
		t.Fatalf("auto 模式不应下发 --jinja，实际参数：%v", args2)
	}
}

// 思考模式：默认态不下发，显式设置才变成 llama-server 开关。
func TestBuildArgsReasoning(t *testing.T) {
	// 默认（auto / -1 / default）不应产生任何 reasoning 开关
	base := baseValues()
	base["reasoning"] = "auto"
	base["reasoning_budget"] = -1
	base["reasoning_format"] = "auto"
	base["reasoning_effort"] = "default"
	for _, f := range []string{"--reasoning", "--reasoning-budget", "--reasoning-format", "--reasoning-effort"} {
		if argIndex(BuildArgs(base), f) >= 0 {
			t.Fatalf("默认取值不应下发 %s", f)
		}
	}

	// 关闭思考
	v := baseValues()
	v["reasoning"] = "off"
	if got := argValue(BuildArgs(v), "--reasoning"); got != "off" {
		t.Fatalf("--reasoning 期望 off，实际 %q", got)
	}

	// 思考预算 0 = 立即结束（等于关闭思考）
	v2 := baseValues()
	v2["reasoning_budget"] = 0
	if got := argValue(BuildArgs(v2), "--reasoning-budget"); got != "0" {
		t.Fatalf("--reasoning-budget 期望 0，实际 %q", got)
	}

	// 思考内容单独放 reasoning_content + 降低思考强度
	v3 := baseValues()
	v3["reasoning_format"] = "deepseek"
	v3["reasoning_effort"] = "low"
	args3 := BuildArgs(v3)
	if got := argValue(args3, "--reasoning-format"); got != "deepseek" {
		t.Fatalf("--reasoning-format 期望 deepseek，实际 %q", got)
	}
	if got := argValue(args3, "--reasoning-effort"); got != "low" {
		t.Fatalf("--reasoning-effort 期望 low，实际 %q", got)
	}
}

// 加载模式：新版 llama.cpp 用 --load-mode 表达 mmap/mlock，默认组合不下发。
// 旧版的 --mmap/--no-mmap/--mlock 会直接导致 "invalid argument" 启动失败。
func TestBuildArgsLoadMode(t *testing.T) {
	cases := []struct {
		mmap, mlock bool
		want        string
	}{
		{true, false, ""}, // auto（与引擎默认一致，不下发）
		{false, false, "none"},
		{true, true, "mmap+mlock"},
		{false, true, "mlock"},
	}
	for _, c := range cases {
		v := baseValues()
		v["mmap"] = c.mmap
		v["mlock"] = c.mlock
		args := BuildArgs(v)
		if got := argValue(args, "--load-mode"); got != c.want {
			t.Fatalf("mmap=%v mlock=%v 期望 --load-mode=%q，实际 %q（%v）", c.mmap, c.mlock, c.want, got, args)
		}
		for _, dead := range []string{"--mmap", "--no-mmap", "--mlock"} {
			if argIndex(args, dead) >= 0 {
				t.Fatalf("%s 已废弃，不应下发（%v）", dead, args)
			}
		}
	}
}

// 缩放为 1 时走 --lora，且多个适配器必须逗号连接（官方要求单个取值）。
func TestBuildArgsLoraUnscaled(t *testing.T) {
	v := baseValues()
	v["lora"] = []string{`E:\lora\a.bin`, `E:\lora\b.bin`}
	v["lora_scale"] = 1
	args := BuildArgs(v)
	got := argValue(args, "--lora")
	if !strings.Contains(got, "a.bin") || !strings.Contains(got, "b.bin") || !strings.Contains(got, ",") {
		t.Fatalf("--lora 应逗号连接多个适配器，实际 %q", got)
	}
	if argIndex(args, "-lm") >= 0 {
		t.Fatal("-lm 在新版 llama.cpp 里是 --load-mode，不能用于 LoRA")
	}
	if argIndex(args, "--lora-scaled") >= 0 {
		t.Fatal("缩放为 1 时不应走 --lora-scaled")
	}
}

// 缺省值也不能崩（注册表外的空集合）。
func TestBuildArgsEmpty(t *testing.T) {
	args := BuildArgs(map[string]any{})
	if len(args) != 0 {
		t.Fatalf("空取值不应产生参数，实际 %v", args)
	}
}

func TestCommandLineQuoting(t *testing.T) {
	line := CommandLine(`C:\Program Files\llama\llama-server.exe`, "127.0.0.1", 10001,
		[]string{"-m", `E:\My Models\a.gguf`, "-ngl", "99"})
	if !strings.Contains(line, `"C:\Program Files\llama\llama-server.exe"`) {
		t.Fatalf("含空格的路径应加引号：%s", line)
	}
	if !strings.Contains(line, "--port 10001") {
		t.Fatalf("缺少端口：%s", line)
	}
}

// 硬件自检晚于运行时探测：自检结果出来后必须按新策略重新择优，否则会一直用 CPU 跑。
func TestSetPreferGPURePicksRuntime(t *testing.T) {
	m := New(Config{})
	m.runtime = []Runtime{
		{Variant: "cpu", Exe: "cpu.exe"},
		{Variant: "cuda12", Exe: "cuda.exe", GPU: true},
	}
	m.SetPreferGPU(true)
	if got := m.SelectedRuntime().Variant; got != "cuda12" {
		t.Fatalf("检测到 N 卡后应改用 GPU 变体，实际 %s", got)
	}
	m.SetPreferGPU(false)
	if got := m.SelectedRuntime().Variant; got != "cpu" {
		t.Fatalf("不需要 GPU 时应回到 CPU 变体，实际 %s", got)
	}
}

func TestPickBestPrefersGPU(t *testing.T) {
	rts := []Runtime{
		{Variant: "cpu", Version: "2.56.0", Exe: "cpu.exe"},
		{Variant: "cuda12", Version: "2.56.0", Exe: "cuda.exe"},
		{Variant: "vulkan", Version: "2.56.0", Exe: "vk.exe"},
	}
	if rt, _ := PickBest(rts, true); rt.Variant != "cuda12" {
		t.Fatalf("有 N 卡时应选 cuda12，实际 %s", rt.Variant)
	}
	if rt, _ := PickBest(rts, false); rt.Variant != "cpu" {
		t.Fatalf("无 GPU 需求时应选 CPU 版，实际 %s", rt.Variant)
	}
}
