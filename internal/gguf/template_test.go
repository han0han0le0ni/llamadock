package gguf

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTinyGGUF 构造一个最小的 GGUF v3 文件，只包含若干字符串类型的 KV。
func makeTinyGGUF(t *testing.T, kvs map[string]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(ggufMagic)
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint32(buf[:4], 3) // version
	b.Write(buf[:4])
	binary.LittleEndian.PutUint64(buf, 0) // tensor count
	b.Write(buf)
	binary.LittleEndian.PutUint64(buf, uint64(len(kvs))) // kv count
	b.Write(buf)
	for k, v := range kvs {
		binary.LittleEndian.PutUint64(buf, uint64(len(k)))
		b.Write(buf)
		b.WriteString(k)
		binary.LittleEndian.PutUint32(buf[:4], 8) // type string
		b.Write(buf[:4])
		binary.LittleEndian.PutUint64(buf, uint64(len(v)))
		b.Write(buf)
		b.WriteString(v)
	}
	p := filepath.Join(t.TempDir(), "tiny.gguf")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseChatTemplate(t *testing.T) {
	tpl := "{% for m in messages %}{{ m.role }}: {{ m.content }}\n{% endfor %}"
	p := makeTinyGGUF(t, map[string]string{
		"general.name":            "Tiny Model",
		"general.architecture":    "llama",
		"tokenizer.chat_template": tpl,
	})
	m, err := Parse(p)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.ChatTemplate != tpl {
		t.Fatalf("ChatTemplate 提取失败：got %q want %q", m.ChatTemplate, tpl)
	}
}

func TestParseChatTemplateFallbackKey(t *testing.T) {
	tpl := "USER: {{ msg }}\nASSISTANT:"
	p := makeTinyGGUF(t, map[string]string{"chat_template": tpl})
	m, err := Parse(p)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.ChatTemplate != tpl {
		t.Fatalf("备用键 chat_template 未生效：got %q want %q", m.ChatTemplate, tpl)
	}
}

func TestParseNoChatTemplate(t *testing.T) {
	p := makeTinyGGUF(t, map[string]string{"general.name": "Base Model"})
	m, err := Parse(p)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.ChatTemplate != "" {
		t.Fatalf("无内嵌模板时应为空，got %q", m.ChatTemplate)
	}
}

// makeTinyGGUFWithTags 构造带 general.tags 字符串数组的最小 GGUF v3。
func makeTinyGGUFWithTags(t *testing.T, arch string, tags []string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(ggufMagic)
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint32(buf[:4], 3)
	b.Write(buf[:4])
	binary.LittleEndian.PutUint64(buf, 0) // tensor count
	b.Write(buf)
	binary.LittleEndian.PutUint64(buf, 2) // kv count: arch + tags
	b.Write(buf)
	writeKey := func(k string) {
		binary.LittleEndian.PutUint64(buf, uint64(len(k)))
		b.Write(buf)
		b.WriteString(k)
	}
	writeStr := func(s string) {
		binary.LittleEndian.PutUint32(buf[:4], 8) // type string
		b.Write(buf[:4])
		binary.LittleEndian.PutUint64(buf, uint64(len(s)))
		b.Write(buf)
		b.WriteString(s)
	}
	writeKey("general.architecture")
	writeStr(arch)
	writeKey("general.tags")
	binary.LittleEndian.PutUint32(buf[:4], 9) // type array
	b.Write(buf[:4])
	binary.LittleEndian.PutUint32(buf[:4], 8) // element type string
	b.Write(buf[:4])
	binary.LittleEndian.PutUint64(buf, uint64(len(tags)))
	b.Write(buf)
	for _, tag := range tags {
		binary.LittleEndian.PutUint64(buf, uint64(len(tag)))
		b.Write(buf)
		b.WriteString(tag)
	}
	p := filepath.Join(t.TempDir(), "tags.gguf")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseTagsAndCapabilities(t *testing.T) {
	p := makeTinyGGUFWithTags(t, "qwen35", []string{"reasoning", "tool-use", "function-calling", "text-generation"})
	m, err := Parse(p)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Tags) != 4 || m.Tags[0] != "reasoning" {
		t.Fatalf("Tags 解析失败：%v", m.Tags)
	}
	joined := strings.Join(m.Capabilities, ",")
	for _, want := range []string{"推理", "工具调用", "文本生成"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Capabilities 缺少 %s：%v", want, m.Capabilities)
		}
	}
	if m.IsVision() {
		t.Fatal("qwen35 + 普通标签不应判为视觉模型")
	}
}

func TestVisionDetectionByTag(t *testing.T) {
	// qwen35 架构 + image-text-to-text 标签（真实存在的 Qwen3.5-9B 情况）
	p := makeTinyGGUFWithTags(t, "qwen35", []string{"image-text-to-text"})
	m, err := Parse(p)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !m.IsVision() {
		t.Fatal("带 image-text-to-text 标签应判为视觉模型")
	}
	found := false
	for _, c := range m.Capabilities {
		if c == "视觉" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Capabilities 应含视觉：%v", m.Capabilities)
	}
}

func TestVisionDetectionByArch(t *testing.T) {
	p := makeTinyGGUFWithTags(t, "qwen3vl", []string{})
	m, err := Parse(p)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !m.IsVision() {
		t.Fatal("qwen3vl 架构应判为视觉模型")
	}
}
