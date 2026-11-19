package gguf

import "testing"

func TestClassifyKind(t *testing.T) {
	cases := []struct {
		name string
		arch string
		typ  string
		file string
		want string
	}{
		{"mmproj by type", "clip", "mmproj", "mmproj-Qwen3VL-8B-Instruct-Q8_0.gguf", "mmproj"},
		{"mmproj by filename", "clip", "", "mmproj-Qwen3VL-8B-Instruct-F16.gguf", "mmproj"},
		{"qwen image 21", "qwen_image21", "", "qwen-image-2.1-step90000-Q4_K_M.gguf", "diffusion"},
		{"qwen image", "qwen_image", "", "qwen_image_2.1-Q4_K.gguf", "diffusion"},
		{"flux", "flux", "", "flux1-dev-Q4_K_M.gguf", "diffusion"},
		{"sdxl", "sdxl", "", "sd_xl_base_1.0.gguf", "diffusion"},
		{"wan video", "wan", "", "wan2.1-Q4_K_M.gguf", "diffusion"},
		{"bert embedding", "bert", "", "nomic-embed-text.gguf", "embedding"},
		{"vlm qwen3vl", "qwen3vl", "", "Qwen3VL-8B-Instruct-Q4_K_M.gguf", "vlm"},
		{"plain llama", "llama", "", "qwen2.5-7b-q4_k_m.gguf", "chat"},
		{"qwen35 text stays chat", "qwen3", "", "qwen-qwen3.5-9B.gguf", "chat"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyKind(c.arch, c.typ, c.file); got != c.want {
				t.Fatalf("ClassifyKind(%q,%q,%q) = %q, want %q", c.arch, c.typ, c.file, got, c.want)
			}
		})
	}
}

func TestIsDiffusionArch(t *testing.T) {
	yes := []string{"qwen_image21", "QWEN_IMAGE", "flux1", "sd", "sdxl", "sd3.5", "wan2.1", "z_image", "hidream"}
	no := []string{"llama", "qwen3", "qwen3vl", "qwen2vl", "gemma3", "deepseek2"}
	for _, a := range yes {
		if !isDiffusionArch(a) {
			t.Fatalf("isDiffusionArch(%q) = false, want true", a)
		}
	}
	for _, a := range no {
		if isDiffusionArch(a) {
			t.Fatalf("isDiffusionArch(%q) = true, want false", a)
		}
	}
}
