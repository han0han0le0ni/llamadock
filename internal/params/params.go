// Package params 是全部 AI 参数的**单一事实源**。
//
//	约 80 个参数在这里只声明一次：
//	  {key, 中英标签, 类型, 范围, 默认值, 单位, 生效方式, 作用域, 适用引擎, 说明, 是否常用}
//	UI 表单、JSON Schema 校验、配置默认值填充、请求体合并、启动命令拼接全部由它生成。
//	新增参数只改一处，UI / API / 校验 / 文档永不失步。
package params

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Kind 参数数据类型。
type Kind string

// 支持的类型。
const (
	KindInt        Kind = "int"
	KindFloat      Kind = "float"
	KindBool       Kind = "bool"
	KindString     Kind = "string"
	KindEnum       Kind = "enum"
	KindStringList Kind = "strlist"
)

// Scope 参数分组（用于 UI 折叠与作用域判定）。
type Scope string

// 参数分组。
const (
	ScopeSample  Scope = "sample"  // 采样（运行时生效）
	ScopeLength  Scope = "length"  // 长度
	ScopeControl Scope = "control" // 输出控制
	ScopePrompt  Scope = "prompt"  // Prompt / 模板
	ScopeLoad    Scope = "load"    // 加载（需重启实例）
	ScopeCache   Scope = "cache"   // KV 缓存
	ScopeAccel   Scope = "accel"   // 加速
	ScopeService Scope = "service" // 服务（不透传给引擎）
)

// Param 单个参数的元数据。
type Param struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Group       string   `json:"group"` // 中文分组标题
	Scope       Scope    `json:"scope"`
	Kind        Kind     `json:"kind"`
	Default     any      `json:"default"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Step        *float64 `json:"step,omitempty"`
	Options     []string `json:"options,omitempty"` // enum 取值
	Unit        string   `json:"unit,omitempty"`
	Runtime     bool     `json:"runtime"`     // true=立即生效；false=需重启实例
	Overridable bool     `json:"overridable"` // 请求级是否可覆盖
	Common      bool     `json:"common"`      // 是否属于「常用 15 项」
	PassThrough bool     `json:"pass_through"` // 是否转发给引擎（chat 请求体）
	Desc        string   `json:"desc"`
	Advice      string   `json:"advice,omitempty"` // 一句话调参建议
}

// registry 有序表 + 索引。
var (
	list  []Param
	index = map[string]Param{}
	groups []string
)

func reg(p Param) {
	if p.Scope == "" {
		p.Scope = ScopeSample
	}
	if !p.Runtime && p.Scope != ScopeService && p.Scope != ScopeLoad &&
		p.Scope != ScopeCache && p.Scope != ScopeAccel {
		p.Runtime = true
	}
	if p.Group == "" {
		p.Group = defaultGroup(p.Scope)
	}
	if _, dup := index[p.Key]; dup {
		panic("params: 重复的参数 key: " + p.Key)
	}
	list = append(list, p)
	index[p.Key] = p
	if !contains(groups, p.Group) {
		groups = append(groups, p.Group)
	}
}

func defaultGroup(s Scope) string {
	switch s {
	case ScopeSample:
		return "采样"
	case ScopeLength:
		return "长度"
	case ScopeControl:
		return "控制"
	case ScopePrompt:
		return "Prompt"
	case ScopeLoad:
		return "加载"
	case ScopeCache:
		return "缓存"
	case ScopeAccel:
		return "加速"
	case ScopeService:
		return "服务"
	}
	return "其它"
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// All 返回全部参数（有序）。
func All() []Param { return append([]Param{}, list...) }

// Groups 返回分组顺序。
func Groups() []string { return append([]string{}, groups...) }

// Count 参数总数。
func Count() int { return len(list) }

// Get 按 key 取参数。
func Get(key string) (Param, bool) {
	p, ok := index[key]
	return p, ok
}

// Exists 参数是否存在。
func Exists(key string) bool {
	_, ok := index[key]
	return ok
}

// Defaults 全部参数的默认值。
func Defaults() map[string]any {
	out := make(map[string]any, len(list))
	for _, p := range list {
		out[p.Key] = p.Default
	}
	return out
}

// Common 常用参数（默认展开区），按 commonOrder 的顺序返回。
func Common() []Param {
	byKey := map[string]Param{}
	for _, p := range list {
		if p.Common {
			byKey[p.Key] = p
		}
	}
	out := make([]Param, 0, len(byKey))
	for _, k := range commonOrder {
		if p, ok := byKey[k]; ok {
			out = append(out, p)
		}
	}
	// 兜底：commonOrder 里没列到但标记为常用的（防止新增漏改顺序表）
	for _, p := range list {
		if p.Common && !contains(commonOrder, p.Key) {
			out = append(out, p)
		}
	}
	return out
}

// OverridableKeys 请求级可覆盖的参数集合。
func OverridableKeys() []string {
	out := []string{}
	for _, p := range list {
		if p.Overridable {
			out = append(out, p.Key)
		}
	}
	return out
}

// GroupOf 取某分组下的参数。
func GroupOf(group string) []Param {
	out := []Param{}
	for _, p := range list {
		if p.Group == group {
			out = append(out, p)
		}
	}
	return out
}

// enumValue 把枚举取值归一化为字符串。
//
// OpenAI 协议里 response_format 是对象（如 {"type":"json_object"}），
// 而本程序的枚举参数是字符串，这里统一取出对象里的 type 字段。
func enumValue(v any) string {
	if m, ok := v.(map[string]any); ok {
		if t, ok := m["type"]; ok {
			return strings.TrimSpace(fmt.Sprint(t))
		}
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// Coerce 把任意输入（多来自 JSON）规整为参数声明的类型，并做范围校验。
func Coerce(p Param, v any) (any, error) {
	switch p.Kind {
	case KindInt:
		n, err := toFloat(v)
		if err != nil {
			return nil, fmt.Errorf("%s: 需要整数", p.Label)
		}
		iv := int(math.Round(n))
		if err := checkRange(p, float64(iv)); err != nil {
			return nil, err
		}
		return iv, nil
	case KindFloat:
		n, err := toFloat(v)
		if err != nil {
			return nil, fmt.Errorf("%s: 需要数字", p.Label)
		}
		if err := checkRange(p, n); err != nil {
			return nil, err
		}
		return n, nil
	case KindBool:
		b, err := toBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s: 需要布尔值", p.Label)
		}
		return b, nil
	case KindEnum:
		s := enumValue(v)
		if len(p.Options) > 0 && !contains(p.Options, s) {
			return nil, fmt.Errorf("%s: 取值必须是 %s 之一", p.Label, strings.Join(p.Options, "/"))
		}
		return s, nil
	case KindStringList:
		ss, err := toStringList(v)
		if err != nil {
			return nil, fmt.Errorf("%s: 需要字符串数组", p.Label)
		}
		return ss, nil
	default:
		if v == nil {
			return "", nil
		}
		return fmt.Sprint(v), nil
	}
}

func checkRange(p Param, v float64) error {
	if p.Min != nil && v < *p.Min {
		return fmt.Errorf("%s: 不能小于 %v%s", p.Label, trimNum(*p.Min), p.Unit)
	}
	if p.Max != nil && v > *p.Max {
		return fmt.Errorf("%s: 不能大于 %v%s", p.Label, trimNum(*p.Max), p.Unit)
	}
	return nil
}

func trimNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case json.Number:
		return n.Float64()
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0, fmt.Errorf("空字符串")
		}
		return strconv.ParseFloat(s, 64)
	case bool:
		if n {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("不支持的类型 %T", v)
}

func toBool(v any) (bool, error) {
	switch b := v.(type) {
	case bool:
		return b, nil
	case string:
		return strconv.ParseBool(strings.TrimSpace(b))
	case float64:
		return b != 0, nil
	case int:
		return b != 0, nil
	}
	return false, fmt.Errorf("不支持的类型 %T", v)
}

func toStringList(v any) ([]string, error) {
	switch s := v.(type) {
	case nil:
		return []string{}, nil
	case []string:
		return s, nil
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			out = append(out, fmt.Sprint(e))
		}
		return out, nil
	case string:
		s = strings.TrimSpace(s)
		if s == "" {
			return []string{}, nil
		}
		if strings.HasPrefix(s, "[") {
			var out []string
			if err := json.Unmarshal([]byte(s), &out); err == nil {
				return out, nil
			}
		}
		parts := strings.Split(s, "\n")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("不支持的类型 %T", v)
}

// Issue 校验问题。
type Issue struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Message string `json:"message"`
}

// Validate 对整个取值集合做类型与范围校验，返回全部问题（不短路）。
func Validate(values map[string]any) []Issue {
	issues := []Issue{}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p, ok := index[k]
		if !ok {
			issues = append(issues, Issue{Key: k, Label: k, Message: "未知参数"})
			continue
		}
		if _, err := Coerce(p, values[k]); err != nil {
			issues = append(issues, Issue{Key: k, Label: p.Label, Message: err.Error()})
		}
	}
	return issues
}

// Normalize 规整取值集合：补齐默认值、丢弃未知 key、按声明类型转换。
func Normalize(values map[string]any) (map[string]any, []Issue) {
	out := Defaults()
	var issues []Issue
	for k, v := range values {
		p, ok := index[k]
		if !ok {
			issues = append(issues, Issue{Key: k, Label: k, Message: "未知参数，已忽略"})
			continue
		}
		if v == nil {
			continue
		}
		cv, err := Coerce(p, v)
		if err != nil {
			issues = append(issues, Issue{Key: k, Label: p.Label, Message: err.Error()})
			continue
		}
		out[k] = cv
	}
	return out, issues
}

// MergeResult 参数作用域合并结果。
type MergeResult struct {
	Final    map[string]any `json:"final"`
	Dropped  []string       `json:"dropped"`  // 请求中不允许覆盖、已被丢弃的 key
	Unknown  []string       `json:"unknown"`  // 请求中不认识的 key
	Invalid  []Issue        `json:"invalid"`  // 类型/范围非法的 key
}

// Merge 两级作用域合并：请求参数 > 全局默认。
//
// 安全约束：只有声明 Overridable 的参数才能被请求覆盖（Load 级参数永远拒绝，
// 否则局域网里一个恶意请求就能撑爆显存）。
func Merge(request map[string]any, global map[string]any) MergeResult {
	res := MergeResult{Final: Defaults()}
	for k, v := range global {
		p, ok := index[k]
		if !ok || v == nil {
			continue
		}
		if cv, err := Coerce(p, v); err == nil {
			res.Final[k] = cv
		}
	}
	for k, v := range request {
		p, ok := index[k]
		if !ok {
			res.Unknown = append(res.Unknown, k)
			continue
		}
		if !p.Overridable {
			res.Dropped = append(res.Dropped, k)
			continue
		}
		if v == nil {
			continue
		}
		cv, err := Coerce(p, v)
		if err != nil {
			res.Invalid = append(res.Invalid, Issue{Key: k, Label: p.Label, Message: err.Error()})
			continue
		}
		res.Final[k] = cv
	}
	sort.Strings(res.Dropped)
	sort.Strings(res.Unknown)
	return res
}

// EngineBody 把合并后的取值转成发给引擎的请求体字段。
//
// 规则：
//   - 跳过服务参数（不下发）；
//   - **跳过加载级参数**（loading / cache / accel）：它们只在「拉起子进程」时才有意义，
//     混进 chat 请求体没有引擎会读，只会污染请求体与请求日志；
//   - **运行时参数（采样/长度/控制）始终显式下发**，这样界面上的值就是引擎实际用的值，
//     不会出现「面板显示 0.7、引擎实际跑 0.8」的失配；
//   - 剔除无意义取值（-1 的上限、空停止串、空字符串、空列表、false 的开关）。
func EngineBody(final map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range final {
		p, ok := index[k]
		if !ok || !p.PassThrough {
			continue
		}
		switch p.Scope {
		case ScopeLoad, ScopeCache, ScopeAccel:
			continue // 加载级参数由引擎适配器在加载时下发，见 llamacpp.BuildArgs
		}
		runtimeScoped := p.Scope == ScopeSample || p.Scope == ScopeLength || p.Scope == ScopeControl
		if p.Kind == KindEnum && isDefaultValue(p, v) {
			continue // 枚举保持默认时不发，避免与引擎的严格校验冲突
		}
		if !runtimeScoped && isDefaultValue(p, v) {
			continue
		}
		if skipMeaningless(p, v) {
			continue
		}
		// response_format 在 OpenAI 协议里是对象；这里做一次最小转换
		if k == "response_format" {
			switch strings.TrimSpace(fmt.Sprint(v)) {
			case "json_object":
				out[k] = map[string]any{"type": "json_object"}
			case "json_schema":
				// 设计边界（D14）：不把 json_schema 自动转换成引擎格式，宁可不发
			}
			continue
		}
		switch p.Kind {
		case KindStringList:
			if ss, err := toStringList(v); err == nil {
				if len(ss) == 0 {
					continue
				}
				out[k] = ss
			}
		case KindEnum:
			s := strings.TrimSpace(fmt.Sprint(v))
			// 纯数字枚举（如 mirostat 0/1/2）按整数下发，避免严格实现拒绝字符串
			if n, err := strconv.Atoi(s); err == nil && allNumeric(p.Options) {
				out[k] = n
			} else {
				out[k] = s
			}
		case KindString:
			if s := fmt.Sprint(v); strings.TrimSpace(s) != "" {
				out[k] = s
			}
		default:
			out[k] = v
		}
	}
	return out
}

// skipMeaningless 判断该取值是否属于「不发比发好」的情况。
func skipMeaningless(p Param, v any) bool {
	switch p.Kind {
	case KindInt:
		n, err := toFloat(v)
		if err != nil {
			return true
		}
		switch p.Key {
		case "max_tokens", "n_predict":
			return n < 0 // -1 表示不限，交给引擎默认
		case "seed":
			return n < 0 // -1 表示随机
		case "top_logprobs":
			return n <= 0 // 0 会让部分服务端报「requires logprobs」
		case "dry_penalty_last_n":
			return n < 0 // 新版 llama.cpp 只接受 >= 0，-1 是旧语义，改由引擎默认
		}
		return false
	case KindFloat:
		n, err := toFloat(v)
		if err != nil {
			return true
		}
		// 关闭态的采样扩展不需要下发
		switch p.Key {
		case "dry_multiplier", "xtc_probability":
			return n == 0
		}
		return false
	case KindBool:
		b, err := toBool(v)
		if err != nil {
			return true
		}
		if b {
			return false
		}
		// false 且默认就是 false → 不发（发 false 只会让严格实现报错）
		def, _ := toBool(p.Default)
		return !def
	case KindEnum:
		s := strings.TrimSpace(fmt.Sprint(v))
		return s == ""
	case KindStringList:
		ss, err := toStringList(v)
		return err != nil || len(ss) == 0
	case KindString:
		return strings.TrimSpace(fmt.Sprint(v)) == ""
	}
	return false
}

func allNumeric(opts []string) bool {
	if len(opts) == 0 {
		return false
	}
	for _, o := range opts {
		if _, err := strconv.Atoi(strings.TrimSpace(o)); err != nil {
			return false
		}
	}
	return true
}

func isDefaultValue(p Param, v any) bool {
	if v == nil {
		return true
	}
	switch p.Kind {
	case KindInt:
		n, err := toFloat(v)
		if err != nil {
			return false
		}
		d, _ := toFloat(p.Default)
		return n == d
	case KindFloat:
		n, err := toFloat(v)
		if err != nil {
			return false
		}
		d, _ := toFloat(p.Default)
		return math.Abs(n-d) < 1e-9
	case KindBool:
		b, err := toBool(v)
		if err != nil {
			return false
		}
		d, _ := toBool(p.Default)
		return b == d
	case KindStringList:
		ss, err := toStringList(v)
		return err == nil && len(ss) == 0
	default:
		return fmt.Sprint(p.Default) == fmt.Sprint(v)
	}
}

// JSONSchema 由注册表生成的参数 JSON Schema（供外部工具与前端复用）。
func JSONSchema() map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range list {
		sch := map[string]any{"title": p.Label, "description": p.Desc, "default": p.Default, "x-scope": string(p.Scope), "x-group": p.Group}
		switch p.Kind {
		case KindInt:
			sch["type"] = "integer"
		case KindFloat:
			sch["type"] = "number"
		case KindBool:
			sch["type"] = "boolean"
		case KindEnum:
			sch["type"] = "string"
			sch["enum"] = p.Options
		case KindStringList:
			sch["type"] = "array"
			sch["items"] = map[string]any{"type": "string"}
		default:
			sch["type"] = "string"
		}
		if p.Min != nil {
			sch["minimum"] = *p.Min
		}
		if p.Max != nil {
			sch["maximum"] = *p.Max
		}
		props[p.Key] = sch
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "LlamaDock 参数",
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}
