// Package httpapi wires up the Promin HTTP server: health/ping endpoints,
// the /api/v1 REST routes, the WS sync push channel and the embedded web
// UI as a SPA fallback.
package httpapi

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	stdsync "sync"
	"time"

	"github.com/sviniabanditka/promin/server/internal/auth"
	"github.com/sviniabanditka/promin/server/internal/catalog"
	"github.com/sviniabanditka/promin/server/internal/logbuf"
	"github.com/sviniabanditka/promin/server/internal/remux"
	"github.com/sviniabanditka/promin/server/internal/sources"
	"github.com/sviniabanditka/promin/server/internal/sync"
	torrentpkg "github.com/sviniabanditka/promin/server/internal/torrent"
	"github.com/sviniabanditka/promin/server/internal/weather"
)

// NewServer builds the http.Handler for the promin binary: healthz, ping,
// the catalog API, the sources/relay API, the remux API, auth, sync
// (bookmarks/playlists/history/timecodes/settings/WS), and the static UI.
//
// The hard gate makes media auth mandatory (a valid ?t= token is always
// required); there is no PROMIN_REQUIRE_AUTH toggle anymore.
func NewServer(
	version string,
	logger *slog.Logger,
	dataDir string,
	catalogSvc *catalog.Service,
	sourcesSvc *sources.Service,
	remuxQueue *remux.Queue,
	torrentMgr *torrentpkg.Manager,
	authSvc *auth.Service,
	syncSvc *sync.Service,
	httpAddr string,
	logBuf *logbuf.Buffer,
	logsPassword string,
	weatherSvc *weather.Service,
	weatherPlace string,
	h1Host string,
	mainHost string,
) http.Handler {
	mux := http.NewServeMux()

	// Loopback base URL for internal media reads: ffmpeg/ffprobe fetch a
	// torrent file through our own /stream endpoint (anacrolix Reader — blocks
	// until pieces arrive, serves valid bytes + Range/seek) instead of the raw
	// on-disk file (scattered/incomplete .part → garbage). Port only from the
	// listen addr; host is always loopback.
	// Port only from the listen addr; a no-colon addr would panic on the slice.
	port := ":8080"
	if i := strings.LastIndex(httpAddr, ":"); i >= 0 {
		port = httpAddr[i:]
	}
	selfBaseURL := "http://127.0.0.1" + port

	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /api/v1/ping", handlePing(version, h1Host, mainHost))
	// Client diagnostics (Налаштування → «Режим діагностики»): key codes, viewport,
	// JS errors from a TV that has no devtools — straight into the server log.
	// No auth gate: the report that matters most comes from a device that cannot
	// get past the PIN screen (see ?debug=1 in web settings.ts). Body is capped
	// at 16 KiB, the client throttles itself, and the log carries the remote IP.
	mux.HandleFunc("POST /api/v1/diag", handleDiag(logger))

	// Hard gate: every data + media route requires a valid session (PIN or
	// admin). The only open surfaces are the static SPA shell, the auth entry
	// points and the anonymous relay mirrors.
	cat := &catalogHandlers{svc: catalogSvc, syncSvc: syncSvc}
	mux.HandleFunc("GET /api/v1/catalog/home", requireAuth(authSvc, cat.home))
	mux.HandleFunc("GET /api/v1/catalog/list", requireAuth(authSvc, cat.list))
	mux.HandleFunc("GET /api/v1/catalog/search", requireAuth(authSvc, cat.search))
	mux.HandleFunc("GET /api/v1/catalog/title/{tmdb_id}", requireAuth(authSvc, cat.title))
	mux.HandleFunc("GET /api/v1/catalog/genres", requireAuth(authSvc, cat.genres))
	mux.HandleFunc("GET /api/v1/catalog/backdrops", requireAuth(authSvc, cat.backdrops))

	// Screensaver forecast strip. Its own endpoint (not part of catalog) and
	// never fatal: {"available":false} simply hides the strip.
	if weatherSvc != nil {
		wh := &weatherHandlers{svc: weatherSvc, place: weatherPlace}
		mux.HandleFunc("GET /api/v1/weather", requireAuth(authSvc, wh.forecast))
	}

	src := &sourcesHandlers{svc: sourcesSvc, cat: catalogSvc}
	mux.HandleFunc("GET /api/v1/sources/online", requireAuth(authSvc, src.online))
	mux.HandleFunc("GET /api/v1/sources/online/resolve", requireAuth(authSvc, src.resolve))

	trh := &torrentHandlers{sourcesSvc: sourcesSvc, mgr: torrentMgr, remuxQueue: remuxQueue, selfBaseURL: selfBaseURL, logger: logger}
	mux.HandleFunc("GET /api/v1/sources/torrents", requireAuth(authSvc, trh.list))
	mux.HandleFunc("GET /api/v1/torrents/add", requireAuth(authSvc, trh.add))
	mux.HandleFunc("POST /api/v1/torrents/add", requireAuth(authSvc, trh.add))
	mux.HandleFunc("GET /api/v1/torrents/active", requireAuth(authSvc, trh.active))
	mux.HandleFunc("GET /api/v1/torrents/audio", requireAuth(authSvc, trh.audioTracks))
	mux.HandleFunc("DELETE /api/v1/torrents/{infohash}", requireAuth(authSvc, trh.remove))

	mux.Handle("GET /msx/start.json", msxStartHandler())

	// Public onboarding/help page (phone + desktop): how to install Media
	// Station X, point it at promin.club and log in with a PIN. Open pre-gate.
	mux.HandleFunc("GET /onboarding", onboardingPage)
	// /img is a TMDB poster/backdrop proxy. It stays OPEN (no token): <img>
	// tags can't send an Authorization header and card posters carry no ?t=,
	// so gating it just broke every poster. Safe to open — a poster PATH is
	// only discoverable through the gated catalog API, and TMDB images are
	// public anyway.
	mux.Handle("GET /img/", imgProxy(dataDir, logger))
	mux.HandleFunc("GET /relay", requireAuthMedia(authSvc, relayHandler(logger)))
	mux.HandleFunc("GET /stream/{infohash}/{fileIdx}", requireAuthMedia(authSvc, trh.stream))

	rmx := &remuxHandlers{queue: remuxQueue, logger: logger}
	mux.HandleFunc("GET /remux", requireAuthMedia(authSvc, rmx.create))
	mux.HandleFunc("GET /remux/{job}/{file}", requireAuthMedia(authSvc, rmx.serveFile))

	// /logs web UI (Basic Auth gated by PROMIN_LOGS_PASSWORD). Disabled when the
	// password is unset — the routes simply aren't registered.
	if logsPassword != "" && logBuf != nil {
		lh := &logsHandlers{buf: logBuf, password: logsPassword}
		mux.HandleFunc("GET /logs", lh.page)
		mux.HandleFunc("GET /logs/api/history", lh.history)
		mux.HandleFunc("GET /logs/api/stream", lh.stream)
	}

	// Admin panel (phone, FORM login → admin session cookie). Open pre-gate:
	// it's a separate credential system and how the operator manages profiles.
	adminH := &adminHandlers{svc: authSvc, logger: logger}
	mux.HandleFunc("GET /admin", adminH.page)
	mux.HandleFunc("POST /admin/login", adminH.login)
	mux.HandleFunc("POST /admin/logout", adminH.logout)
	mux.HandleFunc("GET /admin/profiles", adminH.listProfiles)
	mux.HandleFunc("POST /admin/profiles", adminH.createProfile)
	mux.HandleFunc("PATCH /admin/profiles/{id}", adminH.patchProfile)
	mux.HandleFunc("DELETE /admin/profiles/{id}", adminH.deleteProfile)

	authH := &authHandlers{svc: authSvc}
	mux.HandleFunc("POST /api/v1/auth/pin", authH.pinLogin) // open pre-gate: TV PIN login
	// Password login/register + device pairing are removed — the only way in is a
	// PIN (or the admin panel). logout + devices stay (gated, useful for
	// "switch profile" / device management).
	mux.HandleFunc("POST /api/v1/auth/logout", requireAuth(authSvc, authH.logout))
	mux.HandleFunc("GET /api/v1/auth/devices", requireAuth(authSvc, authH.listDevices))
	mux.HandleFunc("DELETE /api/v1/auth/devices/{token_id}", requireAuth(authSvc, authH.revokeDevice))

	syncH := &syncHandlers{svc: syncSvc}
	mux.HandleFunc("GET /api/v1/bookmarks", requireAuth(authSvc, syncH.listBookmarks))
	mux.HandleFunc("POST /api/v1/bookmarks", requireAuth(authSvc, syncH.addBookmark))
	mux.HandleFunc("DELETE /api/v1/bookmarks/{tmdb_id}", requireAuth(authSvc, syncH.removeBookmark))

	mux.HandleFunc("GET /api/v1/playlists", requireAuth(authSvc, syncH.listPlaylists))
	mux.HandleFunc("POST /api/v1/playlists", requireAuth(authSvc, syncH.createPlaylist))
	mux.HandleFunc("PATCH /api/v1/playlists/{id}", requireAuth(authSvc, syncH.renamePlaylist))
	mux.HandleFunc("DELETE /api/v1/playlists/{id}", requireAuth(authSvc, syncH.deletePlaylist))
	mux.HandleFunc("GET /api/v1/playlists/{id}/items", requireAuth(authSvc, syncH.listPlaylistItems))
	mux.HandleFunc("POST /api/v1/playlists/{id}/items", requireAuth(authSvc, syncH.addPlaylistItem))
	mux.HandleFunc("DELETE /api/v1/playlists/{id}/items/{item_id}", requireAuth(authSvc, syncH.removePlaylistItem))

	mux.HandleFunc("GET /api/v1/history", requireAuth(authSvc, syncH.listHistory))
	mux.HandleFunc("POST /api/v1/history", requireAuth(authSvc, syncH.addHistory))

	mux.HandleFunc("GET /api/v1/timecodes/continue", requireAuth(authSvc, syncH.continueWatching))
	mux.HandleFunc("GET /api/v1/timecodes/{tmdb_id}", requireAuth(authSvc, syncH.getTimecode))
	mux.HandleFunc("POST /api/v1/timecodes", requireAuth(authSvc, syncH.upsertTimecode))

	mux.HandleFunc("GET /api/v1/settings", requireAuth(authSvc, syncH.getSettings))
	mux.HandleFunc("PUT /api/v1/settings/{key}", requireAuth(authSvc, syncH.putSetting))

	mux.HandleFunc("GET /api/v1/sync/bootstrap", requireAuth(authSvc, syncH.bootstrap))

	// Settings → Danger zone (docs/auth.md).
	meH := &meHandlers{svc: syncSvc, auth: authSvc, logger: logger}
	mux.HandleFunc("DELETE /api/v1/me/history", requireAuth(authSvc, meH.clearHistory))
	mux.HandleFunc("DELETE /api/v1/me/data", requireAuth(authSvc, meH.deleteData))
	mux.HandleFunc("GET /api/v1/sync/events", requireAuth(authSvc, syncH.events))

	ws := &wsHandlers{syncSvc: syncSvc, logger: logger}
	mux.HandleFunc("GET /api/v1/ws", requireAuthMedia(authSvc, ws.serve))

	mux.Handle("/", staticHandler())

	return withSecurityHeaders(withLogging(logger, mux))
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePing also names the two hosts the app may live on: the HTTP/1.1-only
// one for devices with "Режим старого ТВ" on, and the main (Cloudflare) one.
// core/legacy.ts moves the device to whichever its local switch says.
// handleDiag logs one client probe. Body is small JSON {kind, seq, host, data};
// it is logged verbatim (bounded) under msg=diag so `kubectl logs | grep diag`
// is the whole workflow.
// withSecurityHeaders: conservative defaults that cannot break TV webviews.
// No frame-ancestors rule on purpose (Media Station X may host the UI in a
// frame on some platforms); HSTS is set by the edge (Cloudflare / Traefik).
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// diagLimiter: /api/v1/diag is unauthenticated (a device stuck before the PIN
// screen must still be able to report), so cap it per client IP.
var diagLimiter = newIPLimiter(60, time.Minute)

type ipLimiter struct {
	mu     stdsync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
}

func newIPLimiter(limit int, window time.Duration) *ipLimiter {
	return &ipLimiter{hits: map[string][]time.Time{}, limit: limit, window: window}
}

func (l *ipLimiter) allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 4096 { // ponytail: flush everything instead of per-key GC; fine for one node
		l.hits = map[string][]time.Time{}
	}
	kept := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[ip] = kept
		return false
	}
	l.hits[ip] = append(kept, now)
	return true
}

func handleDiag(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !diagLimiter.allow(clientIP(r)) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
		if err != nil {
			writeBadRequest(w, "невірне тіло")
			return
		}
		var probe struct {
			Kind string          `json:"kind"`
			Seq  int             `json:"seq"`
			Host string          `json:"host"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			writeBadRequest(w, "невірний JSON")
			return
		}
		user := int64(0)
		if info, ok := authFrom(r); ok {
			user = info.User.ID
		}
		logger.Info("diag", "kind", probe.Kind, "seq", probe.Seq, "user", user, "ip", clientIP(r), "host", probe.Host, "ua", r.UserAgent(), "data", string(probe.Data))
		w.WriteHeader(http.StatusNoContent)
	}
}

func handlePing(version, h1Host, mainHost string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"pong":      true,
			"version":   version,
			"h1_host":   h1Host,
			"main_host": mainHost,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// maxJSONBody caps request bodies. Every API body is a small JSON document;
// behind Cloudflare a client could otherwise push up to 100 MB into a
// json.Decoder. Media routes are GETs, so this never touches them.
const maxJSONBody = 1 << 20

func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// One panic in a handler must not take the whole server down.
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("http handler panic", "path", r.URL.Path, "panic", rec)
				writeError(w, http.StatusInternalServerError, "internal_error", "внутрішня помилка")
			}
		}()
		if r.Body != nil && r.ContentLength != 0 {
			r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)

		// Logged AFTER the handler so status and duration are known — the old
		// pre-handler line said nothing useful for performance work. High-
		// frequency paths (images, segments, stream, healthz, the 15s sync poll
		// from every TV) stay at Debug so they don't flood the /logs ring.
		p := r.URL.Path
		attrs := []any{"method", r.Method, "path", p, "status", sw.status, "ms", time.Since(start).Milliseconds()}
		if strings.HasPrefix(p, "/img") || strings.HasPrefix(p, "/stream") || p == "/healthz" ||
			p == "/api/v1/sync/events" || p == "/api/v1/timecodes" ||
			strings.HasPrefix(p, "/remux") && strings.Contains(p, "/seg-") {
			logger.Debug("http request", attrs...)
		} else {
			logger.Info("http request", attrs...)
		}
	})
}

// statusWriter records the status code and forwards the optional interfaces
// the WebSocket upgrades (/api/v1/ws, /nws) and streaming responses rely on.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
