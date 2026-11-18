package sdcpp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSize(t *testing.T) {
	w, h, err := parseSize("1024x768")
	if err != nil || w != 1024 || h != 768 {
		t.Fatalf("parseSize(1024x768) = %d,%d,%v", w, h, err)
	}
	w, h, err = parseSize(" 512X512 ")
	if err != nil || w != 512 || h != 512 {
		t.Fatalf("parseSize( 512X512 ) = %d,%d,%v", w, h, err)
	}
	for _, bad := range []string{"", "abc", "512", "0x512", "512x"} {
		if _, _, err := parseSize(bad); err == nil {
			t.Fatalf("parseSize(%q) 应当报错", bad)
		}
	}
}

func TestBuildOpenAIBody(t *testing.T) {
	body, err := BuildOpenAIBody(GenerateRequest{
		Prompt:         "a red fox",
		NegativePrompt: "blurry",
		Size:           "768x1024",
		Steps:          30,
		CfgScale:       7.5,
		Seed:           42,
		Sampler:        "euler_a",
	}, 512, 512, 25, 7.0)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["size"] != "768x1024" || got["n"] != float64(1) {
		t.Fatalf("size/n 错误：%v", got)
	}
	prompt := got["prompt"].(string)
	if !strings.HasPrefix(prompt, "a red fox <sd_cpp_extra_args>") {
		t.Fatalf("prompt 未内嵌扩展：%s", prompt)
	}
	start := strings.Index(prompt, "<sd_cpp_extra_args>") + len("<sd_cpp_extra_args>")
	end := strings.Index(prompt, "</sd_cpp_extra_args>")
	var extra map[string]any
	if err := json.Unmarshal([]byte(prompt[start:end]), &extra); err != nil {
		t.Fatalf("扩展 JSON 解析失败：%v", err)
	}
	if extra["negative_prompt"] != "blurry" || extra["width"] != float64(768) || extra["height"] != float64(1024) {
		t.Fatalf("扩展字段错误：%v", extra)
	}
	if extra["seed"] != float64(42) {
		t.Fatalf("seed 错误：%v", extra)
	}
	sp := extra["sample_params"].(map[string]any)
	if sp["sample_steps"] != float64(30) || sp["sample_method"] != "euler_a" {
		t.Fatalf("sample_params 错误：%v", sp)
	}

	// 空提示词应报错
	if _, err := BuildOpenAIBody(GenerateRequest{}, 512, 512, 25, 7); err == nil {
		t.Fatal("空 prompt 应当报错")
	}
}

func TestBuildOpenAIBodyDefaults(t *testing.T) {
	body, _ := BuildOpenAIBody(GenerateRequest{Prompt: "cat"}, 640, 480, 20, 6.5)
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	if got["size"] != "640x480" {
		t.Fatalf("默认值未生效：%v", got["size"])
	}
}

func TestSplitArgs(t *testing.T) {
	got := splitArgs(`--diffusion-model "C:\my models\flux.gguf" --steps 20`)
	want := []string{"--diffusion-model", `C:\my models\flux.gguf`, "--steps", "20"}
	if len(got) != len(want) {
		t.Fatalf("splitArgs 长度不符：%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
