package core

import (
	"os"
	"path/filepath"
	"testing"

	"airunner/internal/engine"
)

func TestFindMMProj(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "Qwen3VL-8B-Instruct-Q4_K_M.gguf")
	proj := filepath.Join(dir, "mmproj-Qwen3VL-8B-Instruct-F16.gguf")
	other := filepath.Join(dir, "mmproj-OtherModel-F16.gguf")
	for _, p := range []string{model, proj, other} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := findMMProj(engine.Model{Path: model, Type: "vlm", Arch: "qwen3vl"})
	if got != proj {
		t.Fatalf("应选与模型名最匹配的 mmproj，got=%s want=%s", got, proj)
	}
}

func TestFindMMProjSkipsTextModel(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "qwen2.5-7b-q4_k_m.gguf")
	proj := filepath.Join(dir, "mmproj-Qwen3VL-8B-Instruct-F16.gguf")
	for _, p := range []string{model, proj} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := findMMProj(engine.Model{Path: model, Type: "llm", Arch: "qwen2"}); got != "" {
		t.Fatalf("纯文本模型不应自动挂 mmproj，got=%s", got)
	}
}
