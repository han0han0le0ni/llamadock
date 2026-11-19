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

func TestBuildOpenAIBodyImg2Img(t *testing.T) {
	dataURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg=="
	body, err := BuildOpenAIBody(GenerateRequest{
		Prompt:    "make it a sunny day",
		Size:      "512x512",
		InitImage: dataURL,
		Strength:  0.45,
	}, 512, 512, 25, 7.0)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	prompt := got["prompt"].(string)
	start := strings.Index(prompt, "<sd_cpp_extra_args>") + len("<sd_cpp_extra_args>")
	end := strings.Index(prompt, "</sd_cpp_extra_args>")
	var extra map[string]any
	if err := json.Unmarshal([]byte(prompt[start:end]), &extra); err != nil {
		t.Fatalf("扩展 JSON 解析失败：%v", err)
	}
	if extra["init_image"] != dataURL {
		t.Fatalf("init_image 未下发：%v", extra["init_image"])
	}
	if extra["strength"] != 0.45 {
		t.Fatalf("strength 未下发：%v", extra["strength"])
	}
}

func TestBuildOpenAIBodyNoImg2ImgByDefault(t *testing.T) {
	body, _ := BuildOpenAIBody(GenerateRequest{Prompt: "cat"}, 512, 512, 25, 7)
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	prompt := got["prompt"].(string)
	if strings.Contains(prompt, "init_image") || strings.Contains(prompt, "strength") {
		t.Fatalf("无底图时不应出现 img2img 字段：%s", prompt)
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

func TestParseProgressLine(t *testing.T) {
	cases := []struct {
		line       string
		ok         bool
		step, total int
		speed      float64
		unit, eta  string
	}{
		{"  45/80 -   6.28it/s - ETA 00:35", true, 45, 80, 6.28, "it/s", "00:35"},
		{"  1/80 -  25.45s/it - ETA 33:30", true, 1, 80, 25.45, "s/it", "33:30"},
		{" 80/80 -   6.04it/s", true, 80, 80, 6.04, "it/s", ""},
		// 一整行里多次 \r 刷新：应取最后一条
		{"|  1/20 - 25s/it - ETA 08:00 |=>  | 12/20 - 3.2it/s - ETA 00:05 |====>  |", true, 12, 20, 3.2, "it/s", "00:05"},
		// 非进度行
		{"[I] sampling using Euler method --- request.cpp:478", false, 0, 0, 0, "", ""},
		{"loading diffusion model from 'x.gguf'", false, 0, 0, 0, "", ""},
		{"", false, 0, 0, 0, "", ""},
	}
	for _, c := range cases {
		p, ok := parseProgressLine(c.line)
		if ok != c.ok {
			t.Fatalf("parseProgressLine(%q) ok=%v want %v", c.line, ok, c.ok)
		}
		if !c.ok {
			continue
		}
		if p.Step != c.step || p.Total != c.total || p.Speed != c.speed || p.SpeedUnit != c.unit || p.ETA != c.eta {
			t.Fatalf("parseProgressLine(%q) = %+v, want step=%d total=%d speed=%v unit=%s eta=%s",
				c.line, p, c.step, c.total, c.speed, c.unit, c.eta)
		}
		if p.Percent != c.step*100/c.total {
			t.Fatalf("percent = %d, want %d", p.Percent, c.step*100/c.total)
		}
	}
}

func TestHasLogLevelFlag(t *testing.T) {
	for _, s := range []string{"--log-level debug", "--log-level=verbose", "-v", "--verbose", `x -v y`} {
		if !hasLogLevelFlag(s) {
			t.Fatalf("hasLogLevelFlag(%q) should be true", s)
		}
	}
	for _, s := range []string{"", "--offload-to-cpu", `--diffusion-model "a b.gguf"`} {
		if hasLogLevelFlag(s) {
			t.Fatalf("hasLogLevelFlag(%q) should be false", s)
		}
	}
}
