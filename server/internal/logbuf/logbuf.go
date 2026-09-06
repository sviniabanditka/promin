// Package logbuf is an in-memory slog capture: a slog.Handler that keeps the
// last N log records in a ring buffer and fans new ones out to live subscribers,
// so the /logs web UI can show recent history + a realtime stream without any
// external log stack (matches Promin's single-binary design). It wraps an inner
// handler (stdout), so normal logging is unaffected.
//
// History is in-memory only: it resets on restart (e.g. a deploy). Enough for
// debugging a flow you just reproduced; cross-restart persistence would need a
// disk tee, not done here.
package logbuf

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Attr is one flattened key/value of a log record (ordered, unlike a map).
type Attr struct {
	Key string `json:"k"`
	Val string `json:"v"`
}

// Record is one captured log line, JSON-serialisable for the UI.
type Record struct {
	Seq   uint64    `json:"seq"`
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`
	Attrs []Attr    `json:"attrs,omitempty"`
}

// Buffer is the shared ring + subscriber registry. One per process.
type Buffer struct {
	mu    sync.Mutex
	ring  []Record
	size  int
	next  int // next write index
	count int // filled slots (<= size)
	seq   uint64
	subs  map[chan Record]struct{}
}

// NewBuffer builds a ring holding the most recent `size` records.
func NewBuffer(size int) *Buffer {
	if size <= 0 {
		size = 5000
	}
	return &Buffer{
		ring: make([]Record, size),
		size: size,
		subs: make(map[chan Record]struct{}),
	}
}

func (b *Buffer) add(r Record) {
	b.mu.Lock()
	b.seq++
	r.Seq = b.seq
	b.ring[b.next] = r
	b.next = (b.next + 1) % b.size
	if b.count < b.size {
		b.count++
	}
	subs := make([]chan Record, 0, len(b.subs))
	for c := range b.subs {
		subs = append(subs, c)
	}
	b.mu.Unlock()

	// Non-blocking fan-out: a slow subscriber drops records rather than
	// stalling the logging path.
	for _, c := range subs {
		select {
		case c <- r:
		default:
		}
	}
}

// History returns up to `limit` most-recent records (oldest first) at or above
// minLevel, optionally containing `query` (case-insensitive, in msg or attrs).
func (b *Buffer) History(limit int, minLevel slog.Level, query string) []Record {
	b.mu.Lock()
	all := make([]Record, 0, b.count)
	start := 0
	if b.count == b.size {
		start = b.next
	}
	for i := 0; i < b.count; i++ {
		all = append(all, b.ring[(start+i)%b.size])
	}
	b.mu.Unlock()

	q := strings.ToLower(query)
	out := make([]Record, 0, len(all))
	for _, r := range all {
		if !matches(r, minLevel, q) {
			continue
		}
		out = append(out, r)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Subscribe registers a live feed of new records; call the returned func to
// unsubscribe. The channel is buffered; overflow drops (see add).
func (b *Buffer) Subscribe() (<-chan Record, func()) {
	c := make(chan Record, 256)
	b.mu.Lock()
	b.subs[c] = struct{}{}
	b.mu.Unlock()
	return c, func() {
		b.mu.Lock()
		if _, ok := b.subs[c]; ok {
			delete(b.subs, c)
			close(c)
		}
		b.mu.Unlock()
	}
}

// Matches is the exported filter used by the SSE endpoint per record.
func Matches(r Record, minLevel slog.Level, query string) bool {
	return matches(r, minLevel, strings.ToLower(query))
}

func matches(r Record, minLevel slog.Level, lowerQuery string) bool {
	if levelValue(r.Level) < minLevel {
		return false
	}
	if lowerQuery == "" {
		return true
	}
	if strings.Contains(strings.ToLower(r.Msg), lowerQuery) {
		return true
	}
	for _, a := range r.Attrs {
		if strings.Contains(strings.ToLower(a.Key), lowerQuery) || strings.Contains(strings.ToLower(a.Val), lowerQuery) {
			return true
		}
	}
	return false
}

func levelValue(s string) slog.Level {
	switch {
	case strings.HasPrefix(s, "DEBUG"):
		return slog.LevelDebug
	case strings.HasPrefix(s, "WARN"):
		return slog.LevelWarn
	case strings.HasPrefix(s, "ERROR"):
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Handler is a slog.Handler that captures into a Buffer, then delegates to an
// inner handler (so stdout logging still happens).
type Handler struct {
	inner  slog.Handler
	buf    *Buffer
	attrs  []Attr
	prefix string // group prefix for attr keys
}

// NewHandler wraps inner, capturing every emitted record into buf.
func NewHandler(inner slog.Handler, buf *Buffer) *Handler {
	return &Handler{inner: inner, buf: buf}
}

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	rec := Record{Time: r.Time, Level: r.Level.String(), Msg: r.Message}
	rec.Attrs = append(rec.Attrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		rec.Attrs = append(rec.Attrs, flatten(h.prefix, a)...)
		return true
	})
	h.buf.add(rec)
	return h.inner.Handle(ctx, r)
}

func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	na := make([]Attr, len(h.attrs), len(h.attrs)+len(as))
	copy(na, h.attrs)
	for _, a := range as {
		na = append(na, flatten(h.prefix, a)...)
	}
	return &Handler{inner: h.inner.WithAttrs(as), buf: h.buf, attrs: na, prefix: h.prefix}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	p := h.prefix
	if name != "" {
		p = p + name + "."
	}
	return &Handler{inner: h.inner.WithGroup(name), buf: h.buf, attrs: h.attrs, prefix: p}
}

// flatten renders one slog.Attr into key/value pairs, recursing into groups
// (prefixing keys) so nested attrs stay searchable in the UI.
func flatten(prefix string, a slog.Attr) []Attr {
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		var out []Attr
		gp := prefix
		if a.Key != "" {
			gp = prefix + a.Key + "."
		}
		for _, ga := range a.Value.Group() {
			out = append(out, flatten(gp, ga)...)
		}
		return out
	}
	return []Attr{{Key: prefix + a.Key, Val: a.Value.String()}}
}
