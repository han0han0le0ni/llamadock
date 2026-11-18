// Package reqlog 保存最近 N 条推理请求记录（v2.0 的「请求日志」，默认 200 条）。
package reqlog

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry 一条请求记录。
type Entry struct {
	ID               int64     `json:"id"`
	Time             time.Time `json:"time"`
	SourceIP         string    `json:"source_ip"`
	Device           string    `json:"device,omitempty"`
	Model            string    `json:"model"`
	Endpoint         string    `json:"endpoint"`
	Stream           bool      `json:"stream"`
	Format           string    `json:"format"`
	Status           int       `json:"status"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TTFTMs           int       `json:"ttft_ms"`
	DurationMs       int       `json:"duration_ms"`
	Error            string    `json:"error,omitempty"`
}

// Filter 查询条件。
type Filter struct {
	Model   string
	Status  int // 0 = 全部
	Keyword string
	Since   time.Time
	FromID  int64
	Limit   int
}

// Ring 有界请求日志。
type Ring struct {
	mu   sync.Mutex
	max  int
	buf  []Entry
	seq  int64
	subs []func(Entry)
}

// New 创建请求日志，容量 n（默认 200）。
func New(n int) *Ring {
	if n <= 0 {
		n = 200
	}
	return &Ring{max: n, buf: make([]Entry, 0, n)}
}

// Add 追加记录并返回落库后的实体（含自增 ID）。
func (r *Ring) Add(e Entry) Entry {
	r.mu.Lock()
	r.seq++
	e.ID = r.seq
	if len(r.buf) >= r.max {
		copy(r.buf, r.buf[1:])
		r.buf[len(r.buf)-1] = e
	} else {
		r.buf = append(r.buf, e)
	}
	subs := append([]func(Entry){}, r.subs...)
	r.mu.Unlock()
	for _, fn := range subs {
		fn(e)
	}
	return e
}

// Query 按条件查询（时间倒序）。
func (r *Ring) Query(f Filter) []Entry {
	r.mu.Lock()
	out := make([]Entry, 0, len(r.buf))
	for _, e := range r.buf {
		if f.FromID > 0 && e.ID <= f.FromID {
			continue
		}
		if f.Model != "" && e.Model != f.Model {
			continue
		}
		if f.Status != 0 && e.Status != f.Status {
			continue
		}
		if !f.Since.IsZero() && e.Time.Before(f.Since) {
			continue
		}
		if f.Keyword != "" {
			kw := strings.ToLower(f.Keyword)
			if !strings.Contains(strings.ToLower(e.Model), kw) &&
				!strings.Contains(strings.ToLower(e.SourceIP), kw) &&
				!strings.Contains(strings.ToLower(e.Endpoint), kw) {
				continue
			}
		}
		out = append(out, e)
	}
	r.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out
}

// Stats 简单聚合。
type Stats struct {
	Total            int              `json:"total"`
	Failed           int              `json:"failed"`
	PromptTokens     int              `json:"prompt_tokens"`
	CompletionTokens int              `json:"completion_tokens"`
	ByModel          map[string]int   `json:"by_model"`
	AvgTTFTMs        float64          `json:"avg_ttft_ms"`
	AvgDurationMs    float64          `json:"avg_duration_ms"`
}

// Aggregate 聚合统计。
func (r *Ring) Aggregate() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := Stats{ByModel: map[string]int{}}
	var ttft, dur float64
	for _, e := range r.buf {
		st.Total++
		st.PromptTokens += e.PromptTokens
		st.CompletionTokens += e.CompletionTokens
		st.ByModel[e.Model]++
		ttft += float64(e.TTFTMs)
		dur += float64(e.DurationMs)
		if e.Status >= 400 {
			st.Failed++
		}
	}
	if st.Total > 0 {
		st.AvgTTFTMs = ttft / float64(st.Total)
		st.AvgDurationMs = dur / float64(st.Total)
	}
	return st
}

// Clear 清空。
func (r *Ring) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = r.buf[:0]
}

// Subscribe 订阅新记录（供推送）。
func (r *Ring) Subscribe(fn func(Entry)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs = append(r.subs, fn)
}

// Models 返回出现过的模型名。
func (r *Ring) Models() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	set := map[string]struct{}{}
	for _, e := range r.buf {
		if e.Model != "" {
			set[e.Model] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
