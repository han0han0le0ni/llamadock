package core

import (
	"testing"

	"airunner/internal/config"
)

func TestMatchAutoTemplate(t *testing.T) {
	rules := []config.TemplateRule{
		{Pattern: "qwen2.5", Label: "specific", Mode: "jinja", Text: "A"},
		{Pattern: "qwen", Label: "generic", Mode: "jinja", Text: "B"},
		{Pattern: "llama-3", Label: "llama", Mode: "jinja", Text: "C"},
		{Pattern: "", Label: "empty-should-skip", Mode: "jinja", Text: "X"},
	}

	tests := []struct {
		name       string
		modelName  string
		general    string
		modelKey   string
		wantLabel  string // 期望命中的规则 Label；空表示期望 nil
	}{
		{"命中 general.name", "Qwen2.5-7B.Q4_K_M", "Qwen2.5-7B-Instruct", "qwen2.5-7b.q4_k_m", "specific"},
		{"命中文件名 stem", "Qwen2.5-7B-Instruct", "", "qwen2.5-7b-instruct", "specific"},
		{"命中 ModelKey", "model", "", "qwen2.5-7b-instruct", "specific"},
		{"大小写不敏感", "QWEN2.5-7B", "", "QWEN2.5-7B", "specific"},
		{"回退到较宽的 qwen 规则", "Qwen1.5-7B-Chat", "", "qwen1.5-7b-chat", "generic"},
		{"多规则顺序：具体在前", "llama-3.1-8b", "", "llama-3.1-8b", "llama"},
		{"无匹配", "phi-3-mini", "", "phi-3-mini", ""},
		{"全空字段", "", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchAutoTemplate(tt.modelName, tt.general, tt.modelKey, rules)
			if tt.wantLabel == "" {
				if got != nil {
					t.Fatalf("期望无匹配，实际命中 %q", got.Label)
				}
				return
			}
			if got == nil {
				t.Fatalf("期望命中 %q，实际无匹配", tt.wantLabel)
			}
			if got.Label != tt.wantLabel {
				t.Fatalf("期望命中 %q，实际 %q", tt.wantLabel, got.Label)
			}
		})
	}
}

func TestMatchAutoTemplateEmptyRules(t *testing.T) {
	if got := matchAutoTemplate("qwen2.5", "", "qwen2.5", nil); got != nil {
		t.Fatalf("空规则表应无匹配，实际 %q", got.Label)
	}
	if got := matchAutoTemplate("qwen2.5", "", "qwen2.5", []config.TemplateRule{}); got != nil {
		t.Fatalf("空规则切片应无匹配，实际 %q", got.Label)
	}
}

func TestDefaultTemplateAutoMapUsable(t *testing.T) {
	m := config.DefaultTemplateAutoMap()
	if len(m) == 0 {
		t.Fatal("默认规则表不应为空")
	}
	// 每条规则都应有 pattern / mode / text，且模式是合法取值
	for _, r := range m {
		if r.Pattern == "" || r.Text == "" {
			t.Fatalf("默认规则字段不完整：%+v", r)
		}
		if r.Mode != "jinja" && r.Mode != "manual" && r.Mode != "auto" {
			t.Fatalf("默认规则 mode 非法：%q", r.Mode)
		}
	}
	// 默认规则应能命中典型模型名
	if got := matchAutoTemplate("Qwen2.5-7B-Instruct", "", "qwen2.5-7b-instruct", m); got == nil {
		t.Fatal("默认规则应能命中 Qwen2.5")
	}
	if got := matchAutoTemplate("Meta-Llama-3-8B", "", "meta-llama-3-8b", m); got == nil {
		t.Fatal("默认规则应能命中 Llama 3")
	}
}
