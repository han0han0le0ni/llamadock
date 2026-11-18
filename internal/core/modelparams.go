package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"airunner/internal/config"
	"airunner/internal/params"
)

// shardSuffix 多分片 gguf 的序号后缀，例如 -00001-of-00002。
var shardSuffix = regexp.MustCompile(`-\d{5}-of-\d{5}$`)

// ModelKey 模型级设置的稳定标识：文件名去掉扩展名与分片序号后小写。
//
// 刻意**不用绝对路径**：换个盘、挪个目录、重命名父目录，之前调好的参数都还在。
// 代价是不同目录下的同名 gguf 会共用一份设置——个人自用可以接受，而且比"记住路径"
// 更符合直觉：模型是"那个文件"，不是"那个位置"。
func ModelKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`)))
	if s == "" {
		return ""
	}
	s = filepath.Base(strings.ReplaceAll(s, `\`, "/"))
	s = strings.TrimSuffix(s, ".gguf")
	return shardSuffix.ReplaceAllString(s, "")
}

// MergeParams 把覆盖项叠加到全局参数之上，返回新 map（不改动入参）。
func MergeParams(global, override map[string]any) map[string]any {
	out := make(map[string]any, len(global)+len(override))
	for k, v := range global {
		out[k] = v
	}
	for k, v := range override {
		if v == nil {
			continue
		}
		out[k] = v
	}
	return out
}

// ModelParams 返回某模型自己的参数覆盖（只含与全局默认不同的项）。
// key 可以是模型 id / 名称 / 路径，内部统一归一化为 ModelKey。
func (c *Core) ModelParams(key string) map[string]any {
	key = ModelKey(key)
	if key == "" {
		return nil
	}
	mo, ok := c.Cfg.Snapshot().Models[key]
	if !ok || len(mo.Params) == 0 {
		return nil
	}
	return mo.Params
}

// EffectiveParams 某模型实际生效的参数 = 全局默认 + 该模型的覆盖。
//
// 模型级覆盖**不经过** params.Merge 的 Overridable 检查：那是给局域网请求用的安全阀，
// 而模型级设置是你自己在界面上亲手调的，理应完整生效（包括 n_gpu_layers 这类加载级参数）。
func (c *Core) EffectiveParams(key string) map[string]any {
	global, _ := params.Normalize(c.Cfg.Snapshot().Params)
	return MergeParams(global, c.ModelParams(key))
}

// ModelOverrideDTO 给界面的「模型记忆」摘要。
type ModelOverrideDTO struct {
	Key        string         `json:"key"`
	Name       string         `json:"name,omitempty"`
	Path       string         `json:"path,omitempty"`
	Count      int            `json:"count"` // 与全局不同的参数项数
	Params     map[string]any `json:"params"`
	LastCtx    int            `json:"last_ctx,omitempty"`
	LoadCount  int            `json:"load_count"`
	LastUsedAt int64          `json:"last_used_at"`
}

// ModelOverrides 列出所有有记录的模型（包含只记录了使用统计、没改过参数的）。
func (c *Core) ModelOverrides() []ModelOverrideDTO {
	snap := c.Cfg.Snapshot()
	out := make([]ModelOverrideDTO, 0, len(snap.Models))
	for key, mo := range snap.Models {
		out = append(out, ModelOverrideDTO{
			Key: key, Name: mo.Name, Path: mo.Path,
			Count: len(mo.Params), Params: mo.Params,
			LastCtx: mo.LastCtx, LoadCount: mo.LoadCount, LastUsedAt: mo.LastUsedAt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastUsedAt != out[j].LastUsedAt {
			return out[i].LastUsedAt > out[j].LastUsedAt
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// SetModelParams 保存某模型的参数覆盖。
//
// 只记录**与全局默认不同**的项：这样配置里能一眼看出"这个模型我到底改了什么"，
// 而不是把 80 多个值整份抄一遍（全局值将来变了，这里也不会变成化石）。
// 若某模型最终与全局完全一致，则删掉它的覆盖记录（保留使用统计）。
func (c *Core) SetModelParams(key, name, path string, values map[string]any) (ParamsDTO, error) {
	key = ModelKey(key)
	if key == "" {
		return c.Params(), fmt.Errorf("缺少模型标识")
	}
	global, _ := params.Normalize(c.Cfg.Snapshot().Params)

	diff := map[string]any{}
	for k, v := range values {
		p, ok := params.Get(k)
		if !ok || v == nil {
			continue
		}
		if k == "model_path" { // 加载时才注入，不属于可保存的参数
			continue
		}
		cv, err := params.Coerce(p, v)
		if err != nil {
			return c.Params(), fmt.Errorf("参数「%s」非法：%s", p.Label, err)
		}
		if sameValue(global[k], cv) {
			continue
		}
		diff[k] = cv
	}

	if _, err := c.Cfg.Update(func(cfg *config.Config) {
		ensureModels(cfg)
		mo := cfg.Models[key]
		mo.Key = key
		if name != "" {
			mo.Name = name
		}
		if path != "" {
			mo.Path = path
		}
		if len(diff) == 0 {
			mo.Params = nil
		} else {
			mo.Params = diff
		}
		cfg.Models[key] = mo
	}); err != nil {
		return c.Params(), err
	}

	if len(diff) == 0 {
		c.Log.Info("该模型已与全局参数一致，覆盖记录已清空", "model", key)
	} else {
		c.Log.Info("已保存模型级参数", "model", key, "keys", len(diff))
	}
	return c.Params(), nil
}

// ClearModelParams 忘掉某模型的全部记忆（参数覆盖 + 使用统计）。
func (c *Core) ClearModelParams(key string) error {
	key = ModelKey(key)
	if key == "" {
		return fmt.Errorf("缺少模型标识")
	}
	_, err := c.Cfg.Update(func(cfg *config.Config) {
		delete(cfg.Models, key)
	})
	if err != nil {
		return err
	}
	c.Log.Info("已清除模型记忆", "model", key)
	return nil
}

// touchModel 记录一次「这个模型被用到了」：加载次数、上次用得多久、上次的上下文长度。
//
// 每次加载都落盘——加载本来就不是高频操作，换来的是换模型再换回来时心里有数。
func (c *Core) touchModel(key, name, path string, ctxLen int) {
	key = ModelKey(key)
	if key == "" {
		return
	}
	_, err := c.Cfg.Update(func(cfg *config.Config) {
		ensureModels(cfg)
		mo := cfg.Models[key]
		mo.Key = key
		if name != "" {
			mo.Name = name
		}
		if path != "" {
			mo.Path = path
		}
		if ctxLen > 0 {
			mo.LastCtx = ctxLen
		}
		mo.LoadCount++
		mo.LastUsedAt = time.Now().Unix()
		cfg.Models[key] = mo
	})
	if err != nil {
		c.Log.Warn("记录模型使用情况失败", "model", key, "error", err)
	}
}

func ensureModels(cfg *config.Config) {
	if cfg.Models == nil {
		cfg.Models = map[string]config.ModelOverride{}
	}
}

// sameValue 比较两个参数取值是否等价（切片、数字类型都要经得起 json 往返）。
func sameValue(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ra, err1 := json.Marshal(a)
	rb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return fmt.Sprint(a) == fmt.Sprint(b)
	}
	return string(ra) == string(rb)
}
