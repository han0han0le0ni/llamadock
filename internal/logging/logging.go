// Package logging 提供结构化日志：文件（自带滚动）+ 控制台 + UI 环形缓冲。
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Entry UI 侧可展示的一条日志。
type Entry struct {
	Seq   int64          `json:"seq"`
	Time  time.Time      `json:"time"`
	Level string         `json:"level"`
	Msg   string         `json:"msg"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// Ring 固定容量的日志环形缓冲，供界面「日志」页读取。
type Ring struct {
	mu   sync.Mutex
	max  int
	buf  []Entry
	seq  int64
	subs []func(Entry)
}

// NewRing 创建容量为 n 的环形缓冲。
func NewRing(n int) *Ring {
	if n <= 0 {
		n = 500
	}
	return &Ring{max: n, buf: make([]Entry, 0, n)}
}

// Add 写入一条日志，并向订阅者推送。
func (r *Ring) Add(e Entry) {
	r.mu.Lock()
	r.seq++
	e.Seq = r.seq
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
}

// Recent 返回最近 limit 条（时间正序）。
func (r *Ring) Recent(limit int) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > len(r.buf) {
		limit = len(r.buf)
	}
	out := make([]Entry, limit)
	copy(out, r.buf[len(r.buf)-limit:])
	return out
}

// Clear 清空缓冲。
func (r *Ring) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = r.buf[:0]
}

// Subscribe 订阅新日志（用于 WebSocket 推送，可选）。
func (r *Ring) Subscribe(fn func(Entry)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs = append(r.subs, fn)
}

// handler 把 slog 记录同时写入下游 handler 与环形缓冲。
type handler struct {
	next slog.Handler
	ring *Ring
}

func (h *handler) Enabled(ctx context.Context, lvl slog.Level) bool {
	if h.next != nil && h.next.Enabled(ctx, lvl) {
		return true
	}
	return h.ring != nil
}

func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	if h.ring != nil {
		attrs := map[string]any{}
		rec.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})
		h.ring.Add(Entry{Time: rec.Time, Level: levelName(rec.Level), Msg: rec.Message, Attrs: attrs})
	}
	if h.next != nil {
		return h.next.Handle(ctx, rec)
	}
	return nil
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := &handler{ring: h.ring}
	if h.next != nil {
		n.next = h.next.WithAttrs(attrs)
	}
	return n
}

func (h *handler) WithGroup(name string) slog.Handler {
	n := &handler{ring: h.ring}
	if h.next != nil {
		n.next = h.next.WithGroup(name)
	}
	return n
}

func levelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}

// ParseLevel 解析字符串日志级别。
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New 构建 logger 与 UI 环形缓冲；dir 下的 llamadock.log 会自动滚动。
func New(level string, dir string) (*slog.Logger, *Ring, io.Closer) {
	ring := NewRing(800)
	var sinks []slog.Handler
	sinks = append(sinks, slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: ParseLevel(level)}))
	var closer io.Closer
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			if w, err := newRotatingWriter(filepath.Join(dir, "llamadock.log"), 5<<20, 3); err == nil {
				sinks = append(sinks, slog.NewTextHandler(w, &slog.HandlerOptions{Level: ParseLevel(level)}))
				closer = w
			}
		}
	}
	var next slog.Handler
	switch len(sinks) {
	case 1:
		next = sinks[0]
	default:
		next = multiHandler(sinks)
	}
	return slog.New(&handler{next: next, ring: ring}), ring, closer
}

type multi struct{ hs []slog.Handler }

func multiHandler(hs []slog.Handler) slog.Handler { return &multi{hs: hs} }

func (m *multi) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range m.hs {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (m *multi) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range m.hs {
		if err := h.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *multi) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		out[i] = h.WithAttrs(attrs)
	}
	return &multi{hs: out}
}

func (m *multi) WithGroup(name string) slog.Handler {
	out := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		out[i] = h.WithGroup(name)
	}
	return &multi{hs: out}
}

// rotatingWriter 按大小滚动的文件写入器。
type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	size     int64
}

func newRotatingWriter(path string, maxBytes int64, keep int) (*rotatingWriter, error) {
	w := &rotatingWriter{path: path, maxBytes: maxBytes, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, st.Size()
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(p)) > w.maxBytes {
		w.rotateLocked()
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingWriter) rotateLocked() {
	_ = w.f.Close()
	for i := w.keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	_ = os.Rename(w.path, w.path+".1")
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	w.f, w.size = f, 0
}

// Close 关闭底层文件。
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	return w.f.Close()
}
