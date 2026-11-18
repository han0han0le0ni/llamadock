package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func msgs(payload map[string]any) []any {
	v, _ := payload["messages"].([]any)
	return v
}

func roleOf(m any) string {
	mm, _ := m.(map[string]any)
	r, _ := mm["role"].(string)
	return r
}

func payloadFrom(t *testing.T, jsonStr string) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// system 不在首位时移到开头，其余保持相对顺序。
func TestNormalizeMessagesMovesSystemToFront(t *testing.T) {
	p := payloadFrom(t, `{"messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"hello"},
		{"role":"system","content":"rules"},
		{"role":"user","content":"q"}
	]}`)
	normalizeMessages(p)
	got := msgs(p)
	if len(got) != 4 {
		t.Fatalf("消息数应保持 4，实际 %d", len(got))
	}
	want := []string{"system", "user", "assistant", "user"}
	for i, r := range want {
		if roleOf(got[i]) != r {
			t.Fatalf("第 %d 条应为 %s，实际 %s（序列 %v）", i, r, roleOf(got[i]), got)
		}
	}
	// 非 system 的相对顺序不变
	if c, _ := got[1].(map[string]any)["content"].(string); c != "hi" {
		t.Fatalf("user 消息相对顺序被破坏，实际 %q", c)
	}
}

// system 已在首位时零改动（同一 slice，不重新分配）。
func TestNormalizeMessagesNoopWhenAlreadyFirst(t *testing.T) {
	p := payloadFrom(t, `{"messages":[{"role":"system","content":"r"},{"role":"user","content":"q"}]}`)
	before := msgs(p)
	normalizeMessages(p)
	after := msgs(p)
	if &before[0] != &after[0] {
		t.Fatal("system 已在首位时不应重新分配消息数组")
	}
}

// 没有 system 时不凭空造一条。
func TestNormalizeMessagesNoSystem(t *testing.T) {
	p := payloadFrom(t, `{"messages":[{"role":"user","content":"q"},{"role":"assistant","content":"a"}]}`)
	normalizeMessages(p)
	got := msgs(p)
	if len(got) != 2 || roleOf(got[0]) != "user" {
		t.Fatalf("无 system 时应保持原样，实际 %v", got)
	}
}

// 多个 system 全部提到前面，保持相对顺序，不丢数据。
func TestNormalizeMessagesMultipleSystem(t *testing.T) {
	p := payloadFrom(t, `{"messages":[
		{"role":"user","content":"q1"},
		{"role":"system","content":"sys-A"},
		{"role":"user","content":"q2"},
		{"role":"system","content":"sys-B"}
	]}`)
	normalizeMessages(p)
	got := msgs(p)
	if len(got) != 4 {
		t.Fatalf("消息不应丢失，实际 %d 条", len(got))
	}
	if roleOf(got[0]) != "system" || roleOf(got[1]) != "system" {
		t.Fatalf("两个 system 都应提到前面，实际 %v", got)
	}
	c, _ := got[0].(map[string]any)["content"].(string)
	if c != "sys-A" {
		t.Fatalf("system 之间相对顺序应保持，第一条实际 %q", c)
	}
}

// 引擎的模板报错翻译成中文。
func TestLocalizeEngineError(t *testing.T) {
	raw := []byte(`{"error":{"message":"Jinja Exception: System message must be at the beginning.","type":"server_error"}}`)
	out := localizeEngineError(raw)
	if string(out) == string(raw) {
		t.Fatal("模板报错应被翻译")
	}
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("输出应仍是合法 JSON：%v", err)
	}
	msg := env["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "system 消息") || !strings.Contains(msg, "规范化消息顺序") {
		t.Fatalf("中文提示内容不对：%s", msg)
	}
	if !strings.Contains(msg, "System message must be at the beginning") {
		t.Fatalf("应保留引擎原文：%s", msg)
	}
}

// 非模板类报错原样返回，避免破坏客户端解析。
func TestLocalizeEngineErrorPassthrough(t *testing.T) {
	raw := []byte(`{"error":{"message":"model not loaded","type":"invalid_request"}}`)
	out := localizeEngineError(raw)
	if string(out) != string(raw) {
		t.Fatalf("非模板报错应原样返回：%s", out)
	}
	// 非 JSON 也原样返回
	bad := []byte(`garbage`)
	if string(localizeEngineError(bad)) != string(bad) {
		t.Fatal("非 JSON 应原样返回")
	}
}
