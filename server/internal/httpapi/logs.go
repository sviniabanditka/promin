package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/sviniabanditka/promin/server/internal/logbuf"
)

// logsHandlers serve the admin panel's "Логи" tab: a history JSON endpoint and
// a realtime SSE stream under /admin/logs/*, gated by the admin session cookie
// (adminHandlers.gate) like every other /admin route.
type logsHandlers struct {
	buf  *logbuf.Buffer
	gate func(w http.ResponseWriter, r *http.Request) bool
}

func parseMinLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (h *logsHandlers) history(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	q := r.URL.Query()
	limit := atoiDefault(q.Get("limit"), 1000)
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	recs := h.buf.History(limit, parseMinLevel(q.Get("level")), q.Get("q"))
	writeJSON(w, http.StatusOK, map[string]any{"records": recs})
}

func (h *logsHandlers) stream(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	q := r.URL.Query()
	minLevel := parseMinLevel(q.Get("level"))
	query := q.Get("q")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub := h.buf.Subscribe()
	defer unsub()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case rec, ok := <-ch:
			if !ok {
				return
			}
			if !logbuf.Matches(rec, minLevel, query) {
				continue
			}
			b, err := json.Marshal(rec)
			if err != nil {
				continue
			}
			if _, err := w.Write([]byte("data: ")); err != nil {
				return
			}
			w.Write(b)
			w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}
