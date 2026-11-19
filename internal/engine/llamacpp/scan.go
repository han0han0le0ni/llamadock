package llamacpp

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"airunner/internal/engine"
	"airunner/internal/gguf"
)

// metaCache GGUF 头部解析结果缓存（按 路径+大小 失效），避免每次列表都读几 MB 头部。
type metaCache struct {
	mu sync.Mutex
	m  map[string]cachedMeta
}

type cachedMeta struct {
	size int64
	at   time.Time
	meta *gguf.Meta
}

func newMetaCache() *metaCache { return &metaCache{m: map[string]cachedMeta{}} }

func (c *metaCache) get(path string, size int64) (*gguf.Meta, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[path]
	if !ok || e.size != size || time.Since(e.at) > 10*time.Minute {
		return nil, false
	}
	return e.meta, true
}

func (c *metaCache) put(path string, size int64, meta *gguf.Meta) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[path] = cachedMeta{size: size, at: time.Now(), meta: meta}
}

var shardRe = regexp.MustCompile(`(?i)-(\d{5})-of-(\d{5})\.gguf$`)

// isLaterShard 判断是否为多分片 GGUF 的第 2..N 片（只展示第一片）。
func isLaterShard(name string) bool {
	m := shardRe.FindStringSubmatch(name)
	if len(m) != 3 {
		return false
	}
	idx, _ := strconv.Atoi(m[1])
	return idx > 1
}

// ScanModels 扫描模型根目录下的 GGUF 文件。
//
// budget 用于限制解析元数据的总耗时：文件很多时先给列表、元数据按缓存逐步补齐。
func ScanModels(roots []string, cache *metaCache, budget time.Duration) []engine.Model {
	if cache == nil {
		cache = newMetaCache()
	}
	type entry struct {
		path string
		size int64
	}
	var found []entry
	seen := map[string]bool{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			if d.IsDir() {
				if strings.Count(rel, string(os.PathSeparator)) > 4 {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.EqualFold(filepath.Ext(path), ".gguf") || isLaterShard(d.Name()) {
				return nil
			}
			key := strings.ToLower(path)
			if seen[key] {
				return nil
			}
			seen[key] = true
			info, err := d.Info()
			if err != nil {
				return nil
			}
			found = append(found, entry{path: path, size: info.Size()})
			return nil
		})
	}
	// 大模型优先展示
	sort.SliceStable(found, func(i, j int) bool { return found[i].size > found[j].size })

	deadline := time.Now().Add(budget)
	out := make([]engine.Model, 0, len(found))
	for _, f := range found {
		base := filepath.Base(f.path)
		m := engine.Model{
			ID:     f.path,
			Name:   strings.TrimSuffix(base, filepath.Ext(base)),
			Path:   f.path,
			Type:   "llm",
			SizeMB: int(f.size / (1024 * 1024)),
			State:  "not-loaded",
		}
		// 文件名兜底：即使 GGUF 头部没来得及解析，mmproj 附件也不能当对话模型。
		if strings.HasPrefix(strings.ToLower(base), "mmproj") {
			m.Type = "mmproj"
		}
		if meta, ok := cache.get(f.path, f.size); ok {
			m.Meta = meta
			applyMeta(&m, meta)
		} else if time.Now().Before(deadline) {
			if meta, err := gguf.Parse(f.path); err == nil {
				cache.put(f.path, f.size, meta)
				m.Meta = meta
				applyMeta(&m, meta)
			}
		}
		out = append(out, m)
	}
	return out
}

func applyMeta(m *engine.Model, meta *gguf.Meta) {
	m.Arch = meta.Arch
	m.Quant = meta.Quant
	m.MaxCtx = meta.NativeCtx
	if meta.SizeMB > 0 {
		m.SizeMB = meta.SizeMB
	}
	// 用 GGUF 解析出的角色分类覆盖类型；这比 arch 字符串匹配更可靠
	// （mmproj 的 arch 是 clip，扩散模型的 arch 是 qwen_image/flux…，都不是对话模型）。
	switch meta.Kind {
	case "mmproj":
		m.Type = "mmproj"
	case "diffusion":
		m.Type = "diffusion"
	case "embedding":
		m.Type = "embeddings"
	case "vlm":
		m.Type = "vlm"
	case "chat":
		m.Type = "llm"
	default:
		// 旧缓存（无 Kind）走原有兜底逻辑。
		if strings.Contains(strings.ToLower(meta.Arch), "bert") || strings.Contains(strings.ToLower(meta.Arch), "embed") {
			m.Type = "embeddings"
		}
		if strings.Contains(strings.ToLower(meta.Arch), "vl") || strings.Contains(strings.ToLower(meta.Arch), "vision") {
			m.Type = "vlm"
		}
	}
}
