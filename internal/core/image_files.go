// 生图结果落盘与局域网 URL：把 sd-server 返回的 base64 图片保存到磁盘，
// 并在 OpenAI 兼容响应的 data[] 项里补充 url 字段，让手机端能直接打开/保存，
// 同时保留 b64_json 以兼容只认 base64 的客户端。
package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"airunner/internal/netinfo"
)

// maxKeptImages 最多保留的历史图片数量（按修改时间），防止无限增长占满磁盘。
const maxKeptImages = 200

// imageOutputDir 返回生图落盘目录：显式配置优先，否则用配置文件同级的 images 子目录。
func (c *Core) imageOutputDir() string {
	dir := strings.TrimSpace(c.Cfg.Snapshot().Image.OutputDir)
	if dir == "" {
		dir = filepath.Join(filepath.Dir(c.Cfg.Path()), "images")
	}
	return dir
}

// ImageOutputDir 暴露给网关静态路由使用。
func (c *Core) ImageOutputDir() string { return c.imageOutputDir() }

// EnrichImageResponse 解析 sd-server 的 OpenAI 生图响应，把 data[].b64_json 落盘，
// 并补充 data[].url（局域网可访问）与 data[].file_name。失败时原样返回 body，不影响生图主流程。
//
// 说明：sd-server 返回结构为 {"created":..., "data":[{"b64_json":"..."}]}。
// url 用 netinfo.BestLANIP() + 网关端口构造，手机与本机在同一 WiFi 即可打开。
func (c *Core) EnrichImageResponse(body []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, nil // 非 JSON：原样透传，交由上层
	}
	data, ok := root["data"].([]any)
	if !ok || len(data) == 0 {
		return body, nil // 错误响应（含 error 字段）不处理
	}

	dir := c.imageOutputDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return body, fmt.Errorf("创建出图目录失败：%w", err)
	}

	ip := netinfo.BestLANIP()
	port := c.Cfg.Snapshot().Server.Port
	if port <= 0 {
		port = 8080
	}

	changed := false
	for _, item := range data {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		b64, _ := m["b64_json"].(string)
		if b64 == "" {
			continue
		}
		raw := b64
		if i := strings.Index(raw, ","); strings.HasPrefix(raw, "data:") && i > 0 {
			raw = raw[i+1:]
		}
		imgBytes, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(imgBytes) == 0 {
			continue
		}
		name := fmt.Sprintf("gen-%s-%d.png", time.Now().Format("20060102-150405"), time.Now().UnixNano()%1e9)
		if err := os.WriteFile(filepath.Join(dir, name), imgBytes, 0o644); err != nil {
			continue // 单个文件写失败不影响其它
		}
		m["url"] = fmt.Sprintf("http://%s:%d/v1/images/files/%s", ip, port, name)
		m["file_name"] = name
		changed = true
	}
	if !changed {
		return body, nil
	}
	pruneImages(dir)
	out, err := json.Marshal(root)
	if err != nil {
		return body, nil
	}
	return out, nil
}

// pruneImages 只保留最近 maxKeptImages 张，按修改时间排序删除更早的。
func pruneImages(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fInfo struct {
		path string
		mod  time.Time
	}
	var files []fInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
			continue
		}
		if st, err := e.Info(); err == nil {
			files = append(files, fInfo{filepath.Join(dir, e.Name()), st.ModTime()})
		}
	}
	if len(files) <= maxKeptImages {
		return
	}
	// 简单冒泡：数量不大，够用
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			if files[j].mod.After(files[i].mod) {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
	for _, f := range files[maxKeptImages:] {
		_ = os.Remove(f.path)
	}
}
