// Package config 负责配置的加载 / 校验 / 迁移 / 落盘。
//
// 分层覆盖顺序：内置默认值 → config.json → API PUT。
// 配置文件位置：%APPDATA%/AIRunner/config.json
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"airunner/internal/hwprobe"
)

// CurrentVersion 配置结构版本，用于将来迁移。
const CurrentVersion = 1

// ServerConfig 网关与服务参数。
type ServerConfig struct {
	Host           string   `json:"host"`         // 监听地址，默认 0.0.0.0
	Port           int      `json:"port"`         // 网关端口，默认 8080
	AdminPort      int      `json:"admin_port"`   // 本机管理 API 端口，默认 8081（仅 127.0.0.1）
	CORSOrigins    []string `json:"cors_origins"` // 允许的跨域来源
	MaxBodyMB      int      `json:"max_body_mb"`  // 请求体上限
	RequestTimeout int      `json:"request_timeout_sec"`
	MaxConcurrent  int      `json:"max_concurrent_requests"`
	AutoStart      bool     `json:"auto_start_gateway"` // 启动时自动开网关
	AllowAutoLoad  bool     `json:"allow_auto_load"`
	// LockLoadedModel 为 true 时忽略客户端请求里的 model 字段，始终用当前已加载的模型。
	// 单引擎同一时间只能跑一个 GGUF，开启后可避免外部客户端把已加载模型顶掉、连带模板一起切换。
	LockLoadedModel bool `json:"lock_loaded_model,omitempty"`
	IdleUnloadMin   int  `json:"idle_unload_minutes"`
	VramReserveMB   int  `json:"vram_reserve_mb"`
	// NormalizeMessages 控制是否在转发前把 system 消息提到 messages 首位。
	// 指针类型：nil（老配置缺失）= 默认开启；显式 false 关闭。
	NormalizeMessages *bool `json:"normalize_messages,omitempty"`
}

// ShouldNormalizeMessages 返回是否应规范化消息顺序（默认开启）。
func (s ServerConfig) ShouldNormalizeMessages() bool {
	return s.NormalizeMessages == nil || *s.NormalizeMessages
}

// EngineConfig 引擎接入参数。
//
// 只有一种引擎：**本程序自己拉起 llama-server 子进程**。
// 不依赖任何外部软件：运行时从自带目录 / 用户指定目录 / PATH 里找（见 engine/llamacpp.DetectRuntimes）。
type EngineConfig struct {
	ExePath     string   `json:"exe_path"`     // llama-server.exe（留空则自动探测）
	RuntimeDirs []string `json:"runtime_dirs"` // 额外的运行时搜索目录（自己解压的 llama.cpp 放哪就填哪）
	Host        string   `json:"host"`         // 只绑回环，局域网不可见
	PortMin     int      `json:"port_min"`     // 端口池下界
	PortMax     int      `json:"port_max"`     // 端口池上界
	AutoLoad    bool     `json:"auto_load"`    // 请求未加载的模型时自动拉起
	AutoDetect  bool     `json:"auto_detect"`  // 启动时探测运行时与模型
	KeepAlive   bool     `json:"keep_alive"`   // 空闲时保持常驻（false=按 idle_unload_minutes 卸载）
}

// ImageConfig 生图引擎（stable-diffusion.cpp 的 sd-server）配置。
//
// 生图与文本是两套完全独立的进程：SD/Flux 走 sd-server，文本走 llama-server。
// 8GB 显存下两者不能同时进显存，因此提供 UnloadLLM 开关在生图前腾出显存。
// 该引擎完全可选：ExePath 为空时所有生图接口返回明确错误，不影响文本功能。
type ImageConfig struct {
	Enabled       bool    `json:"enabled"`           // 是否启用生图功能
	ExePath       string  `json:"exe_path"`          // sd-server.exe 路径（留空则自动探测）
	ModelDir      string  `json:"model_dir"`         // 生图模型目录（gguf / safetensors）
	ModelPath     string  `json:"model_path"`        // 当前选用的主模型（绝对路径）
	ExtraArgs     string  `json:"extra_args"`        // 额外启动参数（Flux/Z-Image 多组件用，原样拼到命令行）
	Port          int     `json:"port"`              // 引擎监听端口，默认 1234
	Backend       string  `json:"backend"`           // cuda | vulkan | cpu，默认 cuda
	OffloadToCPU  bool    `json:"offload_to_cpu"`    // 权重留内存按需进显存，省显存
	UnloadLLM     bool    `json:"unload_llm"`        // 生图前卸载文本模型释放显存
	DefaultWidth  int     `json:"default_width"`     // 默认出图宽
	DefaultHeight int     `json:"default_height"`    // 默认出图高
	DefaultSteps  int     `json:"default_steps"`     // 默认采样步数
	DefaultCfg    float64 `json:"default_cfg_scale"` // 默认 CFG
}

// UIConfig 界面与桌面行为。
type UIConfig struct {
	Theme          string `json:"theme"` // dark | light
	MinimizeToTray bool   `json:"minimize_to_tray"`
	StartWithOS    bool   `json:"start_with_os"`
	OpenBrowser    bool   `json:"open_browser"` // headless 模式下自动打开浏览器
	LogLevel       string `json:"log_level"`
	LastView       string `json:"last_view"`
}

// Preset 采样预设。
type Preset struct {
	Name    string         `json:"name"`
	Values  map[string]any `json:"values"`
	Builtin bool           `json:"builtin"`
	Desc    string         `json:"desc,omitempty"`
}

// TemplateRule 一条「模型名 → Chat 模板」自动套用规则。
//
// 规则自带完整模板正文（而非仅存模板 id），这样后端无需了解前端模板库，
// 也就不存在前后端模板内容漂移的问题。前端套用内置模板时把 mode/text 一并写入。
type TemplateRule struct {
	Pattern string `json:"pattern"`         // 匹配模型名的关键字（小写子串），如 "qwen2.5" / "llama-3" / "gemma"
	Mode    string `json:"mode"`            // auto | manual | jinja
	Text    string `json:"text"`            // 模板正文（Jinja）
	Label   string `json:"label,omitempty"` // 展示名，仅用于界面回显
}

// ModelOverride 单个模型的个性化设置。
//
// 存在的意义：同一个模型往往要调好几轮才能找到合适的 n_gpu_layers / n_ctx / 采样组合，
// 换个模型再换回来时不该从头再调一遍。
type ModelOverride struct {
	Key    string         `json:"key"`            // 稳定标识（由文件名派生，换目录不影响）
	Name   string         `json:"name,omitempty"` // 展示名
	Path   string         `json:"path,omitempty"` // 最近一次见到的绝对路径
	Params map[string]any `json:"params"`         // 只存与全局默认**不同**的项

	LastCtx    int   `json:"last_ctx,omitempty"` // 上次加载用的上下文长度
	LoadCount  int   `json:"load_count"`         // 累计加载次数
	LastUsedAt int64 `json:"last_used_at"`       // 最近一次使用/加载（Unix 秒）
}

// Config 全量配置。
type Config struct {
	Version       int                      `json:"version"`
	FirstRunDone  bool                     `json:"first_run_done"`
	Server        ServerConfig             `json:"server"`
	Engine        EngineConfig             `json:"engine"`
	Image         ImageConfig              `json:"image,omitempty"`
	UI            UIConfig                 `json:"ui"`
	Params        map[string]any           `json:"params"` // 全局默认参数（params 注册表的取值）
	Presets       []Preset                 `json:"presets"`
	ActivePreset  string                   `json:"active_preset"`
	ModelRoots    []string                 `json:"model_roots"` // 多个模型根目录（gguf 库）
	Models        map[string]ModelOverride `json:"models,omitempty"`
	LastModel     string                   `json:"last_model"`
	LastModelPath string                   `json:"last_model_path"`
	HW            *hwprobe.Profile         `json:"hw_profile,omitempty"`
	LogHistory    int                      `json:"request_log_size"`

	// 模型名 → Chat 模板自动套用。加载模型时若用户未显式指定模板，按规则匹配模型名套用。
	TemplateAutoEnabled bool           `json:"template_auto_enabled"`
	TemplateAutoMap     []TemplateRule `json:"template_auto_map,omitempty"`
}

// Default 返回内置默认配置。
func Default() Config {
	return Config{
		Version: CurrentVersion,
		Server: ServerConfig{
			Host:           "0.0.0.0",
			Port:           8080,
			AdminPort:      8081,
			CORSOrigins:    []string{"*"},
			MaxBodyMB:      32,
			RequestTimeout: 300,
			MaxConcurrent:  4,
			AutoStart:      true,
			AllowAutoLoad:  true,
			IdleUnloadMin:  30,
			VramReserveMB:  512,
		},
		Engine: EngineConfig{
			ExePath:    "",
			Host:       "127.0.0.1",
			PortMin:    10001,
			PortMax:    10099,
			AutoLoad:   true,
			AutoDetect: true,
			KeepAlive:  false,
		},
		Image: ImageConfig{
			Enabled:       false,
			Port:          1234,
			Backend:       "cuda",
			OffloadToCPU:  true,
			UnloadLLM:     true,
			DefaultWidth:  512,
			DefaultHeight: 512,
			DefaultSteps:  25,
			DefaultCfg:    7.0,
		},
		UI: UIConfig{
			Theme:          "dark",
			MinimizeToTray: true,
			OpenBrowser:    true,
			LogLevel:       "info",
			LastView:       "dashboard",
		},
		Params:              map[string]any{},
		Presets:             BuiltinPresets(),
		ActivePreset:        "平衡",
		LogHistory:          200,
		TemplateAutoEnabled: true,
		TemplateAutoMap:     DefaultTemplateAutoMap(),
	}
}

// DefaultTemplateAutoMap 开箱即用的模型名 → 模板映射。
//
// 这些模板正文与前端 chatTemplates.js 保持一致，都是「宽松化」版本：
// 不含会抛 "System message must be at the beginning" 的 raise 校验，
// 因此比模型 GGUF 自带的严格模板更不容易报错。用户可在设置页增删改。
func DefaultTemplateAutoMap() []TemplateRule {
	return []TemplateRule{
		{
			Pattern: "qwen", Label: "Qwen 2.5（ChatML 式）", Mode: "jinja",
			Text: `{% for message in messages %}{% if loop.first and message['role'] != 'system' %}{{ '<|im_start|>system\nYou are a helpful assistant.<|im_end|>\n' }}{% endif %}{{ '<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>\n' }}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% endif %}`,
		},
		{
			Pattern: "llama-3", Label: "Llama 3", Mode: "jinja",
			Text: `{% for message in messages %}{% if loop.first and message['role'] != 'system' %}{{ '<|start_header_id|>system<|end_header_id|>\n\n<|eot_id|>' }}{% endif %}{{ '<|start_header_id|>' + message['role'] + '<|end_header_id|>\n\n' + message['content'] + '<|eot_id|>' }}{% endfor %}{% if add_generation_prompt %}{{ '<|start_header_id|>assistant<|end_header_id|>\n\n' }}{% endif %}`,
		},
		{
			Pattern: "llama3", Label: "Llama 3", Mode: "jinja",
			Text: `{% for message in messages %}{% if loop.first and message['role'] != 'system' %}{{ '<|start_header_id|>system<|end_header_id|>\n\n<|eot_id|>' }}{% endif %}{{ '<|start_header_id|>' + message['role'] + '<|end_header_id|>\n\n' + message['content'] + '<|eot_id|>' }}{% endfor %}{% if add_generation_prompt %}{{ '<|start_header_id|>assistant<|end_header_id|>\n\n' }}{% endif %}`,
		},
		{
			Pattern: "gemma", Label: "Gemma", Mode: "jinja",
			Text: `{% for message in messages %}{% if message['role'] == 'user' %}{{ '<start_of_turn>user\n' + message['content'] + '<end_of_turn>\n' }}{% elif message['role'] == 'assistant' %}{{ '<start_of_turn>model\n' + message['content'] + '<end_of_turn>\n' }}{% endif %}{% endfor %}{% if add_generation_prompt %}{{ '<start_of_turn>model\n' }}{% endif %}`,
		},
		{
			Pattern: "chatml", Label: "ChatML", Mode: "jinja",
			Text: `{% for message in messages %}{{'<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>' + '\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% endif %}`,
		},
		{
			Pattern: "openchat", Label: "ChatML", Mode: "jinja",
			Text: `{% for message in messages %}{{'<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>' + '\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% endif %}`,
		},
	}
}

// BuiltinPresets 四套内置采样预设（对齐设计方案 §6.5）。
func BuiltinPresets() []Preset {
	return []Preset{
		{Name: "精确", Builtin: true, Desc: "代码、数学、抽取", Values: map[string]any{
			"temperature": 0.2, "top_p": 0.9, "repeat_penalty": 1.05}},
		{Name: "平衡", Builtin: true, Desc: "通用对话（默认）", Values: map[string]any{
			"temperature": 0.7, "top_p": 0.95, "repeat_penalty": 1.1}},
		{Name: "创意", Builtin: true, Desc: "文案、头脑风暴", Values: map[string]any{
			"temperature": 1.1, "top_p": 0.98, "repeat_penalty": 1.05}},
		{Name: "长文", Builtin: true, Desc: "长文档摘要 / RAG", Values: map[string]any{
			"temperature": 0.6, "top_p": 0.9, "repeat_penalty": 1.15}},
	}
}

// Dir 返回配置目录（%APPDATA%/AIRunner）。
func Dir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "AIRunner")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".airunner")
}

// Manager 线程安全的配置管理器。
type Manager struct {
	mu   sync.RWMutex
	path string
	cfg  Config
	subs []func(Config)
}

// NewManager 加载（或初始化）配置。dir 为空时使用 Dir()。
func NewManager(dir string) (*Manager, error) {
	if dir == "" {
		dir = Dir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	m := &Manager{path: filepath.Join(dir, "config.json")}
	if err := m.load(); err != nil {
		return nil, err
	}
	// 没有模型根目录时给一个默认值（<配置目录>\models）：自管引擎（llamacpp）靠它扫 GGUF 库，
	// 否则「模型」页会永远是空的（旧版单引擎配置升级上来时正好命中这里）。
	if len(m.cfg.ModelRoots) == 0 {
		root := filepath.Join(dir, "models")
		_ = os.MkdirAll(root, 0o755)
		m.mu.Lock()
		m.cfg.ModelRoots = []string{root}
		err := m.saveLocked()
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) load() error {
	raw, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			m.cfg = Default()
			return m.saveLocked()
		}
		return err
	}
	cfg := Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		// 配置损坏时不阻塞启动：备份后用默认值（不静默丢弃用户文件）
		_ = os.WriteFile(m.path+".bad", raw, 0o644)
		m.cfg = Default()
		return nil
	}
	m.migrate(&cfg)
	m.cfg = cfg
	return nil
}

// migrate 按版本补齐新增字段的默认值。
func (m *Manager) migrate(c *Config) {
	def := Default()
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Server.Port == 0 {
		c.Server.Port = def.Server.Port
	}
	if c.Server.AdminPort == 0 {
		c.Server.AdminPort = def.Server.AdminPort
	}
	if c.Server.Host == "" {
		c.Server.Host = def.Server.Host
	}
	if c.Server.MaxBodyMB == 0 {
		c.Server.MaxBodyMB = def.Server.MaxBodyMB
	}
	if c.Server.RequestTimeout == 0 {
		c.Server.RequestTimeout = def.Server.RequestTimeout
	}
	if c.Server.MaxConcurrent == 0 {
		c.Server.MaxConcurrent = def.Server.MaxConcurrent
	}
	if c.Engine.Host == "" {
		c.Engine.Host = def.Engine.Host
	}
	if c.Engine.PortMin == 0 {
		c.Engine.PortMin = def.Engine.PortMin
	}
	if c.Engine.PortMax == 0 {
		c.Engine.PortMax = def.Engine.PortMax
	}
	// 生图配置：老配置没有该段（Port==0）时整体补默认值，不动用户已有设置。
	if c.Image.Port == 0 {
		c.Image = def.Image
	}
	if c.Image.Backend == "" {
		c.Image.Backend = def.Image.Backend
	}
	if c.Image.DefaultWidth == 0 {
		c.Image.DefaultWidth = def.Image.DefaultWidth
	}
	if c.Image.DefaultHeight == 0 {
		c.Image.DefaultHeight = def.Image.DefaultHeight
	}
	if c.Image.DefaultSteps == 0 {
		c.Image.DefaultSteps = def.Image.DefaultSteps
	}
	if c.Image.DefaultCfg == 0 {
		c.Image.DefaultCfg = def.Image.DefaultCfg
	}
	if c.UI.Theme == "" {
		c.UI.Theme = def.UI.Theme
	}
	if c.UI.LogLevel == "" {
		c.UI.LogLevel = def.UI.LogLevel
	}
	if c.LogHistory == 0 {
		c.LogHistory = def.LogHistory
	}
	if c.Params == nil {
		c.Params = map[string]any{}
	}
	if len(c.Presets) == 0 {
		c.Presets = BuiltinPresets()
	}
	// 老配置没有模板映射字段（nil）→ 补默认规则并保持开启。
	// 注意用 nil 判断而非 len==0：用户主动清空规则后是空切片（非 nil），不应被重新填满。
	if c.TemplateAutoMap == nil {
		c.TemplateAutoMap = DefaultTemplateAutoMap()
		c.TemplateAutoEnabled = true
	}
	c.Version = CurrentVersion
}

// Path 配置文件绝对路径。
func (m *Manager) Path() string { return m.path }

// Snapshot 返回配置的深拷贝，调用方可安全读取。
func (m *Manager) Snapshot() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return clone(m.cfg)
}

// Update 以函数式方式修改配置、落盘并通知订阅者。
func (m *Manager) Update(fn func(*Config)) (Config, error) {
	m.mu.Lock()
	if fn != nil {
		fn(&m.cfg)
	}
	err := m.saveLocked()
	snap := clone(m.cfg)
	subs := append([]func(Config){}, m.subs...)
	m.mu.Unlock()
	if err != nil {
		return snap, err
	}
	for _, fn := range subs {
		fn(snap)
	}
	return snap, nil
}

// Subscribe 注册配置变更回调（用于热生效）。
func (m *Manager) Subscribe(fn func(Config)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs = append(m.subs, fn)
}

func (m *Manager) saveLocked() error {
	raw, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func clone(c Config) Config {
	raw, err := json.Marshal(c)
	if err != nil {
		return c
	}
	var out Config
	if err := json.Unmarshal(raw, &out); err != nil {
		return c
	}
	return out
}
