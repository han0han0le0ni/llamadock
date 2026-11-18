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
		"general.name":             "Tiny Model",
		"general.architecture":     "llama",
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
