package llamacpp

import (
	"os"

	"airunner/internal/gguf"
)

// parseGGUF 读取 GGUF 头部（薄封装，便于统一替换实现）。
func parseGGUF(path string) (*gguf.Meta, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return gguf.Parse(path)
}
