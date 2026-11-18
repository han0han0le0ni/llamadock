package server

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// delta 从 OpenAI 兼容的流式分片中抽出的最小信息。
type delta struct {
	Content   string
	Reasoning string // 思考型模型的 reasoning_content（Qwen3.5 等默认把输出放这里）
	Role      string
	Finished  bool
	Model     string
	HasUsage  bool
	PromptTok int
	CompTok   int
	ErrorMsg  string
}

// scanSSERaw 逐行扫描 SSE 流：
//   - 对每一行（含空行）回调，raw 为原始行内容，便于调用方**原样透传**；
//   - 数据行会额外解析出 delta（内容增量、usage、结束标记）。
//
// 返回是否读到 [DONE]。
func scanSSERaw(r io.Reader, onLine func(raw string, d delta)) bool {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	sawDone := false
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !strings.HasPrefix(trimmed, "data:") {
			onLine(line, delta{})
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "[DONE]" {
			sawDone = true
			onLine(line, delta{Finished: true})
			continue
		}
		onLine(line, parseChunk(payload))
	}
	return sawDone
}

func parseChunk(payload string) delta {
	var raw struct {
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Role             string `json:"role"`
			} `json:"delta"`
			Text         string `json:"text"`
			FinishReason any    `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	d := delta{}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return d
	}
	d.Model = raw.Model
	if raw.Error != nil {
		d.ErrorMsg = raw.Error.Message
	}
	for _, ch := range raw.Choices {
		if ch.Delta.Content != "" {
			d.Content += ch.Delta.Content
		} else if ch.Text != "" {
			d.Content += ch.Text
		}
		// 思考内容单独累计：llama.cpp 默认 --reasoning-format=auto，
		// 思考型模型会把全部输出放在这里，漏掉就会误记为 0 token。
		if ch.Delta.ReasoningContent != "" {
			d.Reasoning += ch.Delta.ReasoningContent
		}
		if ch.Delta.Role != "" {
			d.Role = ch.Delta.Role
		}
		if ch.FinishReason != nil {
			d.Finished = true
		}
	}
	if raw.Usage != nil {
		d.HasUsage = true
		d.PromptTok = raw.Usage.PromptTokens
		d.CompTok = raw.Usage.CompletionTokens
	}
	return d
}
