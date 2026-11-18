package server

import "testing"

// 思考型模型（Qwen3.5 等）把输出放在 reasoning_content 里，
// 必须能被解析出来，否则网关会把整段生成误记为 0 token。
func TestParseChunkReasoningContent(t *testing.T) {
	d := parseChunk(`{"choices":[{"delta":{"reasoning_content":"思考中"},"finish_reason":null}]}`)
	if d.Reasoning != "思考中" {
		t.Fatalf("reasoning_content 应被解析，实际 %q", d.Reasoning)
	}
	if d.Content != "" {
		t.Fatalf("该分片没有 content，实际 %q", d.Content)
	}
}

// 普通 content 分片与 usage 分片的解析不能受影响。
func TestParseChunkContentAndUsage(t *testing.T) {
	c := parseChunk(`{"choices":[{"delta":{"content":"你好"},"finish_reason":null}]}`)
	if c.Content != "你好" || c.Reasoning != "" {
		t.Fatalf("普通内容解析异常: content=%q reasoning=%q", c.Content, c.Reasoning)
	}

	u := parseChunk(`{"choices":[],"usage":{"prompt_tokens":576,"completion_tokens":0}}`)
	if !u.HasUsage || u.PromptTok != 576 || u.CompTok != 0 {
		t.Fatalf("usage 解析异常: %+v", u)
	}
}
