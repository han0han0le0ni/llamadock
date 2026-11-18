// Package metrics 采集运行指标：吞吐、显存、并发、token 计数。
// 采样进入环形缓冲，供仪表盘曲线使用。
package metrics

import (
	"sync"
	"time"
)

// Sample 一个采样点。
type Sample struct {
	T          time.Time `json:"t"`
	TPS        float64   `json:"tps"`
	VRAMUsedMB int       `json:"vram_used_mb"`
	Active     int       `json:"active"`
}

// Snapshot 对外暴露的指标快照。
type Snapshot struct {
	UptimeSec        int64    `json:"uptime_sec"`
	TotalRequests    int64    `json:"total_requests"`
	FailedRequests   int64    `json:"failed_requests"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	Active           int      `json:"active"`
	TPS              float64  `json:"tps"`
	AvgTPS           float64  `json:"avg_tps"`
	PeakTPS          float64  `json:"peak_tps"`
	TTFTMs           float64  `json:"ttft_ms"`
	VRAMUsedMB       int      `json:"vram_used_mb"`
	Recent           []Sample `json:"recent"`
}

type tokenTick struct {
	at time.Time
	n  int
}

// Store 指标存储。
type Store struct {
	mu      sync.Mutex
	max     int
	buf     []Sample
	ticks   []tokenTick
	ttfts   []float64

	started    time.Time
	lastSample time.Time

	totalReq    int64
	failedReq   int64
	promptTok   int64
	complTok    int64
	active      int
	tps         float64
	peakTPS     float64
	vramUsedMB  int
	lastActive  int
	sumTPS      float64
	tpsSamples  int
}

// New 创建指标存储，max 为环形缓冲容量（默认 2s 一采样 → 3600 ≈ 2 小时）。
func New(max int) *Store {
	if max <= 0 {
		max = 3600
	}
	return &Store{max: max, buf: make([]Sample, 0, max), started: time.Now()}
}

// Sample 记录一次采样（供定时器调用）。
func (s *Store) Sample(vramUsedMB int, active int, tps float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vramUsedMB = vramUsedMB
	s.tps = tps
	if tps > s.peakTPS {
		s.peakTPS = tps
	}
	if tps > 0 {
		s.sumTPS += tps
		s.tpsSamples++
	}
	sm := Sample{T: time.Now(), TPS: tps, VRAMUsedMB: vramUsedMB, Active: active}
	if len(s.buf) >= s.max {
		copy(s.buf, s.buf[1:])
		s.buf[len(s.buf)-1] = sm
	} else {
		s.buf = append(s.buf, sm)
	}
	s.lastSample = sm.T
}

// BeginRequest / EndRequest 并发计数。
func (s *Store) BeginRequest() {
	s.mu.Lock()
	s.active++
	s.totalReq++
	s.mu.Unlock()
}

func (s *Store) EndRequest(failed bool) {
	s.mu.Lock()
	if s.active > 0 {
		s.active--
	}
	if failed {
		s.failedReq++
	}
	s.mu.Unlock()
}

// AddTTFT 记录首字节延迟。
func (s *Store) AddTTFT(d time.Duration) {
	s.mu.Lock()
	s.ttfts = append(s.ttfts, float64(d.Milliseconds()))
	if len(s.ttfts) > 200 {
		s.ttfts = s.ttfts[len(s.ttfts)-200:]
	}
	s.mu.Unlock()
}

// AddTokens 记录本次请求产出的 token 数，用于计算实时吞吐。
func (s *Store) AddTokens(prompt, completion int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promptTok += int64(prompt)
	s.complTok += int64(completion)
	s.ticks = append(s.ticks, tokenTick{at: time.Now(), n: completion})
	s.trimTicks()
}

// AddTotals 只累计总量（输入 / 输出 token），**不进吞吐窗口**。
//
// 流式请求的吞吐窗口已由 AddProgress 实时填充；若结算时改用 AddTokens
// （它同时写总量与窗口），调用方为了不重复计数只能传「差额」，结果总量
// 几乎不增长——仪表盘的「输出 tokens」会长时间停在 0。
func (s *Store) AddTotals(prompt, completion int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promptTok += int64(prompt)
	s.complTok += int64(completion)
}

// AddProgress 记录**进行中**的流式产出（只进吞吐窗口，不计入总量，避免与最终 usage 重复计数）。
//
// 没有它的话，仪表盘在流式生成期间会一直显示 0 t/s，只有等请求结束才跳一下。
func (s *Store) AddProgress(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ticks = append(s.ticks, tokenTick{at: time.Now(), n: n})
	s.trimTicks()
}

func (s *Store) trimTicks() {
	cut := time.Now().Add(-5 * time.Second)
	i := 0
	for i < len(s.ticks) && s.ticks[i].at.Before(cut) {
		i++
	}
	if i > 0 {
		s.ticks = append([]tokenTick{}, s.ticks[i:]...)
	}
}

// TPS 最近 5 秒窗口内的 tokens/s。
func (s *Store) TPS() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trimTicks()
	total := 0
	for _, t := range s.ticks {
		total += t.n
	}
	return float64(total) / 5.0
}

// Snapshot 生成快照，recentN 为曲线点数。
func (s *Store) Snapshot(recentN int) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trimTicks()
	total := 0
	for _, t := range s.ticks {
		total += t.n
	}
	cur := float64(total) / 5.0
	avg := 0.0
	if s.tpsSamples > 0 {
		avg = s.sumTPS / float64(s.tpsSamples)
	}
	ttft := 0.0
	if len(s.ttfts) > 0 {
		sum := 0.0
		for _, v := range s.ttfts {
			sum += v
		}
		ttft = sum / float64(len(s.ttfts))
	}
	if recentN <= 0 || recentN > len(s.buf) {
		recentN = len(s.buf)
	}
	recent := make([]Sample, recentN)
	copy(recent, s.buf[len(s.buf)-recentN:])
	return Snapshot{
		UptimeSec:        int64(time.Since(s.started).Seconds()),
		TotalRequests:    s.totalReq,
		FailedRequests:   s.failedReq,
		PromptTokens:     s.promptTok,
		CompletionTokens: s.complTok,
		Active:           s.active,
		TPS:              cur,
		AvgTPS:           avg,
		PeakTPS:          s.peakTPS,
		TTFTMs:           ttft,
		VRAMUsedMB:       s.vramUsedMB,
		Recent:           recent,
	}
}

// Reset 清空统计（不重启进程）。
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalReq, s.failedReq, s.promptTok, s.complTok = 0, 0, 0, 0
	s.buf = s.buf[:0]
	s.ticks = s.ticks[:0]
	s.ttfts = s.ttfts[:0]
	s.sumTPS, s.tpsSamples = 0, 0
	s.peakTPS = 0
}
