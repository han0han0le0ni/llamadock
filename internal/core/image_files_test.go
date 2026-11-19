package core

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"airunner/internal/config"
)

func TestEnrichImageResponse(t *testing.T) {
	tmp := t.TempDir()
	imgDir := filepath.Join(tmp, "images")
	mgr, err := config.NewManager(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Update(func(c *config.Config) {
		c.Image.OutputDir = imgDir
		c.Server.Port = 8080
	}); err != nil {
		t.Fatal(err)
	}
	c := &Core{Cfg: mgr}

	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	in, _ := json.Marshal(map[string]any{
		"created": 123,
		"data":    []any{map[string]any{"b64_json": b64}},
	})
	out, err := c.EnrichImageResponse(in)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	data := root["data"].([]any)
	item := data[0].(map[string]any)
	url, _ := item["url"].(string)
	if !strings.HasPrefix(url, "http://") || !strings.Contains(url, ":8080/v1/images/files/gen-") {
		t.Fatalf("url 构造错误：%s", url)
	}
	if item["b64_json"] != b64 {
		t.Fatal("应保留 b64_json 兼容旧客户端")
	}
	name, _ := item["file_name"].(string)
	saved, err := os.ReadFile(filepath.Join(imgDir, name))
	if err != nil || string(saved) != string(pngBytes) {
		t.Fatalf("图片未正确落盘：%v %v", saved, err)
	}
}

func TestEnrichImageResponseErrorBody(t *testing.T) {
	tmp := t.TempDir()
	mgr, _ := config.NewManager(tmp)
	c := &Core{Cfg: mgr}
	in := []byte(`{"error":{"message":"oops"}}`)
	out, err := c.EnrichImageResponse(in)
	if err != nil || string(out) != string(in) {
		t.Fatalf("错误响应应原样透传：%s %v", out, err)
	}
}
