package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sviniabanditka/promin/server/internal/logbuf"
)

// logsHandlers serve the admin panel's "Логи" tab: a history JSON endpoint and
// a realtime SSE stream under /admin/logs/*, gated by the admin session cookie
// (adminHandlers.gate) like every other /admin route.
type logsHandlers struct {
	buf    *logbuf.Buffer
	gate   func(w http.ResponseWriter, r *http.Request) bool
	level  *slog.LevelVar // nil → level endpoints answer 404
	logger *slog.Logger
}

// getLevel: GET /admin/logs/level → {"level": "info"|"debug"|"warn"|"error"}.
func (h *logsHandlers) getLevel(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.level == nil {
		writeNotFound(w, "not_found", "рівень логів не керується")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"level": strings.ToLower(h.level.Level().String())})
}

// setLevel: PUT /admin/logs/level {"level": "debug"} switches the process log
// level at runtime (stdout and the ring buffer alike). Not persisted: a restart
// returns to PROMIN_LOG_LEVEL, which is the point — debug is for a session.
func (h *logsHandlers) setLevel(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if h.level == nil {
		writeNotFound(w, "not_found", "рівень логів не керується")
		return
	}
	var req struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req); err != nil {
		writeBadRequest(w, "невірне тіло")
		return
	}
	switch req.Level {
	case "debug", "info", "warn", "error":
	default:
		writeBadRequest(w, "level: debug|info|warn|error")
		return
	}
	h.level.Set(parseMinLevel(req.Level))
	if h.logger != nil {
		h.logger.Info("log level changed", "level", req.Level)
	}
	writeJSON(w, http.StatusOK, map[string]any{"level": req.Level})
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
