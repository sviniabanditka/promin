// Package metrics holds Promin's Prometheus instruments and the middleware /
// listener that expose them. Everything is registered on the default registry
// and served on its OWN listener (PROMIN_METRICS_ADDR) — never on the public
// :8080 mux, which the ingress would expose.
//
// Naming: prefix promin_, low-cardinality labels only (route = mux pattern,
// provider = scraper id, outcome = a small fixed enum).
package metrics

import (
	"bufio"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Outcome enum values shared by the source/torrent counters.
const (
	OutcomeMatch       = "match"
	OutcomeNoMatch     = "nomatch"
	OutcomeError       = "error"
	OutcomeOK          = "ok"
	OutcomeEmpty       = "empty"
	OutcomeRateLimited = "rate_limited"
)

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "promin_http_requests_total", Help: "HTTP requests by mux route, method and status.",
	}, []string{"route", "method", "status"})
	// /stream and HLS segments are long-lived responses, hence the tail buckets.
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "promin_http_request_duration_seconds", Help: "HTTP request duration by mux route.",
		Buckets: append(prometheus.DefBuckets, 30, 120, 600),
	}, []string{"route"})

	sourceSearches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "promin_source_search_total", Help: "Native provider title searches (cache misses only): match|nomatch|error.",
	}, []string{"provider", "outcome"})
	sourceSearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "promin_source_search_duration_seconds", Help: "Native provider title search duration.",
		Buckets: []float64{.25, .5, 1, 2, 5, 10, 20, 40},
	}, []string{"provider"})
	sourceResolves = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "promin_source_resolve_total", Help: "Native provider stream resolves: ok|empty|error.",
	}, []string{"provider", "outcome"})
	sourceResolveDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "promin_source_resolve_duration_seconds", Help: "Native provider stream resolve duration.",
		Buckets: []float64{.25, .5, 1, 2, 5, 10, 20, 40},
	}, []string{"provider"})

	// TorrentSearches counts JacRed queries (cache misses): ok|error|rate_limited.
	TorrentSearches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "promin_torrent_search_total", Help: "Torrent indexer searches: ok|error|rate_limited.",
	}, []string{"outcome"})

	// RelayRequests counts /relay responses by kind: manifest|segment|subtitle|other.
	RelayRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "promin_relay_requests_total", Help: "/relay requests by payload kind.",
	}, []string{"kind"})
	// RelayUpstreamErrors counts upstream fetches that failed or answered 5xx.
	RelayUpstreamErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "promin_relay_upstream_errors_total", Help: "/relay upstream request failures and 5xx answers.",
	})

	RemuxJobsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "promin_remux_jobs_active", Help: "ffmpeg remux/transcode jobs currently running.",
	})
	TorrentsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "promin_torrents_active", Help: "Torrents currently held by the torrent client.",
	})
	WSClients = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "promin_ws_clients", Help: "Open sync WebSocket connections.",
	})
	PlayerDevicesPlaying = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "promin_player_devices_playing", Help: "Devices with a fresh, non-paused player state.",
	})

	buildInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "promin_build_info", Help: "Always 1; the version label carries the build version.",
	}, []string{"version"})
)

// SetBuildInfo publishes promin_build_info{version}=1.
func SetBuildInfo(version string) { buildInfo.WithLabelValues(version).Set(1) }

// SearchOutcome maps matchSource's result to the counter enum: a provider that
// never answered is an error (source down / markup changed), an answer without
// a hit is a nomatch (the source simply doesn't carry the title).
func SearchOutcome(matched, replied bool) string {
	switch {
	case matched:
		return OutcomeMatch
	case replied:
		return OutcomeNoMatch
	default:
		return OutcomeError
	}
}

// ObserveSourceSearch records one live (uncached) title search.
func ObserveSourceSearch(provider, outcome string, d time.Duration) {
	sourceSearches.WithLabelValues(provider, outcome).Inc()
	sourceSearchDuration.WithLabelValues(provider).Observe(d.Seconds())
}

// ObserveSourceResolve records one stream resolve.
func ObserveSourceResolve(provider, outcome string, d time.Duration) {
	sourceResolves.WithLabelValues(provider, outcome).Inc()
	sourceResolveDuration.WithLabelValues(provider).Observe(d.Seconds())
}

// HTTP wraps a Go 1.22+ ServeMux: after routing, r.Pattern names the matched
// route ("" or "/" = the static SPA fallback → "static"). /healthz is skipped —
// two probes every few seconds would only dilute the ratios.
func HTTP(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			mux.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		mux.ServeHTTP(sw, r)
		route := r.Pattern
		if route == "" || route == "/" {
			route = "static"
		}
		httpRequests.WithLabelValues(route, r.Method, strconv.Itoa(sw.status)).Inc()
		httpDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
	})
}

// ListenAndServe serves /metrics on addr (blocking). Empty addr = disabled.
func ListenAndServe(addr string) error {
	if addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

// statusWriter records the status and forwards Flush/Hijack/Unwrap so the WS
// upgrade and streaming responses keep working through the wrapper.
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

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
