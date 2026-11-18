package params

import "testing"

// 注册表必须自洽：常用项数量、类型、范围都成立。
func TestRegistrySanity(t *testing.T) {
	if Count() < 80 {
		t.Fatalf("参数数量不足 80：%d", Count())
	}
	if n := len(Common()); n != 15 {
		t.Fatalf("「常用 15 项」应为 15，实际 %d", n)
	}
	for _, p := range All() {
		if p.Default == nil {
			t.Fatalf("%s 缺少默认值", p.Key)
		}
		if p.Kind == KindEnum && len(p.Options) == 0 {
			t.Fatalf("%s 是枚举但没给取值", p.Key)
		}
		if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
			t.Fatalf("%s 范围非法", p.Key)
		}
	}
	if len(Defaults()) != Count() {
		t.Fatal("Defaults() 数量与注册表不一致")
	}
}

// 请求级不可覆盖的参数必须被丢弃（防止撑爆显存）。
func TestMergeDropsLoadParams(t *testing.T) {
	res := Merge(map[string]any{
		"n_gpu_layers": 99, "n_ctx": 999999, "temperature": 0.3, "messages": []any{},
	}, Defaults())
	for _, k := range res.Dropped {
		if k == "temperature" {
			t.Fatal("采样参数不应被丢弃")
		}
	}
	if res.Final["n_ctx"] != Defaults()["n_ctx"] {
		t.Fatal("n_ctx 不应被请求覆盖")
	}
	if res.Final["temperature"] != 0.3 {
		t.Fatalf("temperature 应被请求覆盖，实际 %v", res.Final["temperature"])
	}
	if len(res.Unknown) != 1 || res.Unknown[0] != "messages" {
		t.Fatalf("messages 应被识别为注册表外字段，实际 %v", res.Unknown)
	}
}

// 非法取值必须报错而不是静默吞掉。
func TestMergeRejectsInvalid(t *testing.T) {
	res := Merge(map[string]any{"temperature": 99}, Defaults())
	if len(res.Invalid) != 1 {
		t.Fatalf("应当报告温度越界，实际 %v", res.Invalid)
	}
	if res.Final["temperature"] == 99 {
		t.Fatal("越界值不应进入最终参数")
	}
}

// 下发引擎的字段必须是「有意义且受支持」的。
func TestEngineBody(t *testing.T) {
	res := Merge(map[string]any{"temperature": 0.9}, Defaults())
	body := EngineBody(res.Final)

	if body["temperature"] != 0.9 {
		t.Fatalf("采样参数必须显式下发，实际 %v", body["temperature"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Fatal("max_tokens=-1 表示不限，不应下发")
	}
	if _, ok := body["seed"]; ok {
		t.Fatal("seed=-1 表示随机，不应下发")
	}
	if _, ok := body["top_logprobs"]; ok {
		t.Fatal("top_logprobs=0 不应下发")
	}
	if _, ok := body["logprobs"]; ok {
		t.Fatal("logprobs=false 不应下发")
	}
	if _, ok := body["stop"]; ok {
		t.Fatal("空停止串不应下发")
	}
	if _, ok := body["n_gpu_layers"]; ok {
		t.Fatal("加载级参数必须不下发（它们只作为 llama-server 的启动开关）")
	}
	if _, ok := body["mirostat"]; ok {
		t.Fatal("枚举保持默认值时不应下发")
	}
	if _, ok := body["response_format"]; ok {
		t.Fatal("response_format=text 是默认值，不应下发")
	}

	// 非默认的枚举：数字枚举按整数下发，response_format 转成对象
	res2 := Merge(map[string]any{"mirostat": "2", "response_format": "json_object"}, Defaults())
	body2 := EngineBody(res2.Final)
	if body2["mirostat"] != 2 {
		t.Fatalf("数字枚举应按整数下发，实际 %T %v", body2["mirostat"], body2["mirostat"])
	}
	if rf, ok := body2["response_format"].(map[string]any); !ok || rf["type"] != "json_object" {
		t.Fatalf("response_format 应转为对象，实际 %#v", body2["response_format"])
	}
}

// 加载级参数只在拉起子进程时下发；请求体里不该出现它们。
func TestEngineBodySkipsLoadParams(t *testing.T) {
	vals := Defaults()
	vals["n_gpu_layers"] = 28 // 非默认值，确保不是被「默认值过滤」挡掉的
	vals["n_ctx"] = 16384
	vals["cache_type_k"] = "q8_0"
	vals["flash_attn"] = "on"

	body := EngineBody(vals)
	for _, k := range []string{"n_gpu_layers", "n_ctx", "cache_type_k", "flash_attn"} {
		if _, ok := body[k]; ok {
			t.Fatalf("%s 是加载级参数，不应出现在请求体里（%v）", k, body[k])
		}
	}
	if _, ok := body["temperature"]; !ok {
		t.Fatal("采样参数必须显式下发")
	}
}

// 服务类参数不得下发给引擎。
func TestEngineBodySkipsService(t *testing.T) {
	body := EngineBody(Defaults())
	for _, k := range []string{"public_host", "public_port", "cors_origins", "vram_reserve_mb"} {
		if _, ok := body[k]; ok {
			t.Fatalf("服务参数 %s 不应下发", k)
		}
	}
}

func TestNormalizeRange(t *testing.T) {
	vals, issues := Normalize(map[string]any{"n_ctx": 4096, "temperature": 0.5, "不存在的参数": 1})
	if vals["n_ctx"] != 4096 {
		t.Fatalf("n_ctx 归一化失败: %v", vals["n_ctx"])
	}
	if len(issues) != 1 {
		t.Fatalf("应只有 1 个问题（未知参数），实际 %v", issues)
	}
}

func TestJSONSchema(t *testing.T) {
	sch := JSONSchema()
	props, ok := sch["properties"].(map[string]any)
	if !ok || len(props) != Count() {
		t.Fatalf("schema 属性数应与注册表一致: %d vs %d", len(props), Count())
	}
}

// 思考模式参数必须注册齐全，供参数面板调节（思考型模型没答案时的关键开关）。
func TestReasoningParamsRegistered(t *testing.T) {
	for _, k := range []string{"reasoning", "reasoning_budget", "reasoning_format", "reasoning_effort"} {
		p, ok := index[k]
		if !ok {
			t.Fatalf("缺少思考参数 %s", k)
		}
		if p.Group != "思考" || p.Scope != ScopeLoad {
			t.Fatalf("%s 应为「思考」分组下的加载级参数，实际 group=%s scope=%s", k, p.Group, p.Scope)
		}
		if p.Runtime {
			t.Fatalf("%s 是命令行开关，应标记为需重载（Runtime=false）", k)
		}
	}
}

// OpenAI 客户端常把 response_format 传成对象（{"type":"json_object"}），
// 必须被归一化为枚举字符串，而不是直接报「取值必须是 text/json_object/json_schema 之一」。
func TestResponseFormatAcceptsObject(t *testing.T) {
	res := Merge(map[string]any{
		"response_format": map[string]any{"type": "json_object"},
	}, Defaults())
	if len(res.Invalid) != 0 {
		t.Fatalf("对象形式的 response_format 不应被判为非法：%v", res.Invalid)
	}
	if res.Final["response_format"] != "json_object" {
		t.Fatalf("应归一化为 json_object，实际 %#v", res.Final["response_format"])
	}
	body := EngineBody(res.Final)
	if rf, ok := body["response_format"].(map[string]any); !ok || rf["type"] != "json_object" {
		t.Fatalf("下发给引擎的应是 {\"type\":\"json_object\"}，实际 %#v", body["response_format"])
	}

	// 真·非法取值仍应被拒绝
	bad := Merge(map[string]any{"response_format": map[string]any{"type": "yaml"}}, Defaults())
	if len(bad.Invalid) != 1 {
		t.Fatalf("非法 response_format 仍应报错，实际 %v", bad.Invalid)
	}
}

// dry_penalty_last_n=-1 是旧版语义，新版 llama.cpp 只接受 >= 0，不应下发。
func TestDryPenaltyLastNNotSent(t *testing.T) {
	if _, ok := EngineBody(Defaults())["dry_penalty_last_n"]; ok {
		t.Fatal("默认值 -1 不应下发给引擎")
	}
	res := Merge(map[string]any{"dry_penalty_last_n": 512}, Defaults())
	if got := EngineBody(res.Final)["dry_penalty_last_n"]; got != 512 {
		t.Fatalf("显式设置 512 应下发，实际 %#v", got)
	}
}
