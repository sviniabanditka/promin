// Package synthmon runs the hourly synthetic user: the two flows that break
// silently — "find a film and get the first bytes of a stream" and "YouTube
// plays" — exercised end to end against the real upstreams, published as
// promin_synthetic_* gauges (metrics.go) and alerted on in k8s/monitoring.yaml.
// Blackbox probes only prove the HTTP server answers; these prove TMDB, the
// source providers and the YouTube attestation recipe still work.
package synthmon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sviniabanditka/promin/server/internal/catalog"
	"github.com/sviniabanditka/promin/server/internal/metrics"
	"github.com/sviniabanditka/promin/server/internal/sources"
	"github.com/sviniabanditka/promin/server/internal/youtube"
)

const (
	interval   = time.Hour
	firstDelay = 2 * time.Minute
	// A query with many stable results in every language.
	defaultQuery = "matrix"
	// Enough to prove the upstream serves media, small enough not to matter.
	firstBytesWant = 64 << 10
)

// Stages reached by the sources check (promin_synthetic_stage{check="sources"}).
const (
	StageSearch  = 1 // TMDB search answered with titles
	StageOnline  = 2 // at least one online source listed
	StageResolve = 3 // a source resolved to streams
	StageBytes   = 4 // the stream served its first bytes
)

// Stages of the YouTube check.
const (
	StageYTProbe  = 1 // /player as the signed-in TV client was OK
	StageYTStream = 2 // the SABR stream delivered past the unattested cut
)

type Monitor struct {
	catalog *catalog.Service
	sources *sources.Service
	yt      *youtube.Client
	http    *http.Client
	log     *slog.Logger
	query   string
}

func New(cat *catalog.Service, src *sources.Service, yt *youtube.Client, logger *slog.Logger) *Monitor {
	return &Monitor{
		catalog: cat,
		sources: src,
		yt:      yt,
		http:    &http.Client{Timeout: 30 * time.Second},
		log:     logger,
		query:   defaultQuery,
	}
}

// Run ticks until ctx is cancelled. The first run waits a couple of minutes
// so a fresh deploy's restart storm does not page anyone.
func (m *Monitor) Run(ctx context.Context) {
	if m == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(firstDelay):
	}
	m.tick(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.tick(ctx)
		}
	}
}

func (m *Monitor) tick(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	m.checkSources(cctx)
	if m.yt != nil && m.yt.Enabled() {
		m.checkYouTube(cctx)
	}
}

// checkSources: search → online list → resolve → first bytes, as the TV does.
func (m *Monitor) checkSources(ctx context.Context) {
	t0 := time.Now()
	stage := 0
	var got int64
	err := func() error {
		res, err := m.catalog.Search(ctx, m.query, "en", 1, "")
		if err != nil {
			return err
		}
		title := pickTitle(res.Items)
		if title == nil {
			return errNoTitles
		}
		stage = StageSearch
		online := m.sources.Online(ctx, sources.OnlineRequest{
			TMDBID: title.TMDBID, Type: title.Type, Title: title.Title, OriginalTitle: title.OriginalTitle, Year: title.Year,
		})
		if len(online.Sources) == 0 {
			return errNoSources
		}
		stage = StageOnline
		var lastErr error = errNoStreams
		for i, s := range online.Sources {
			if i >= 3 {
				break
			}
			req := sources.ResolveRequest{
				Balancer: s.Balancer, TMDBID: title.TMDBID, Type: title.Type, Title: title.Title, OriginalTitle: title.OriginalTitle, Year: title.Year,
			}
			if title.Type == "tv" {
				req.Season, req.Episode = 1, 1
			}
			resolved, err := m.sources.Resolve(ctx, req)
			if err != nil || len(resolved.Streams) == 0 {
				if err != nil {
					lastErr = err
				}
				continue
			}
			stage = StageResolve
			n, err := firstBytes(ctx, m.http, upstreamOf(resolved.Streams[0].URL))
			if err != nil {
				lastErr = err
				continue
			}
			got = n
			stage = StageBytes
			return nil
		}
		return lastErr
	}()
	d := time.Since(t0)
	metrics.SetSynthetic("sources", err == nil, stage, d, got)
	if err != nil {
		m.log.Warn("synthetic: sources check failed", "stage", stage, "ms", d.Milliseconds(), "error", err)
	} else {
		m.log.Info("synthetic: sources ok", "ms", d.Milliseconds(), "bytes", got)
	}
}

// checkYouTube asks the sidecar to play as the linked account (ytx /v1/check).
func (m *Monitor) checkYouTube(ctx context.Context) {
	t0 := time.Now()
	r, err := m.yt.Check(ctx)
	d := time.Since(t0)
	stage := 0
	switch {
	case err == nil && r.OK:
		stage = StageYTStream
	case err == nil && r.Probe:
		stage = StageYTProbe
	}
	ok := err == nil && r.OK
	metrics.SetSynthetic("youtube", ok, stage, d, r.Bytes)
	if !ok {
		reason := r.Reason
		if err != nil {
			reason = err.Error()
		}
		m.log.Warn("synthetic: youtube check failed", "stage", stage, "sps", r.SPS, "bytes", r.Bytes, "reason", reason)
	} else {
		m.log.Info("synthetic: youtube ok", "ms", r.MS, "bytes", r.Bytes, "account", r.Account)
	}
}

// pickTitle prefers a movie (one resolve, no season) over a series.
func pickTitle(items []catalog.Title) *catalog.Title {
	for i := range items {
		if items[i].Type == "movie" && items[i].TMDBID > 0 {
			return &items[i]
		}
	}
	for i := range items {
		if items[i].TMDBID > 0 && (items[i].Type == "tv" || items[i].Type == "movie") {
			return &items[i]
		}
	}
	return nil
}

// firstBytes fetches the beginning of a stream URL the way the relay would
// (browser UA, a Range) and returns how many bytes arrived. A playlist counts:
// its body is the proof the upstream serves this title.
func firstBytes(ctx context.Context, c *http.Client, rawURL string) (int64, error) {
	if rawURL == "" {
		return 0, errNoUpstream
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (SMART-TV; Linux; Tizen 5.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/69.0.3497.106 Safari/537.36")
	req.Header.Set("Range", "bytes=0-65535")
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, &statusError{resp.StatusCode}
	}
	n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, firstBytesWant))
	if n == 0 {
		return 0, errEmptyBody
	}
	return n, nil
}

// upstreamOf turns what the TV would play — a /relay?u= or /remux?u= wrapper
// around the provider's URL — back into that URL. Anything else that is a
// bare path (a torrent /stream) has no upstream to fetch: empty.
func upstreamOf(u string) string {
	switch {
	case strings.HasPrefix(u, "/relay?"):
		if raw, err := sources.DecodeRelayURL(u); err == nil {
			return raw
		}
		return ""
	case strings.HasPrefix(u, "/remux?"):
		if q, err := url.ParseQuery(strings.TrimPrefix(u, "/remux?")); err == nil {
			if raw, err := sources.DecodeRelayParam(q.Get("u")); err == nil {
				return raw
			}
		}
		return ""
	case strings.HasPrefix(u, "/"):
		return ""
	}
	return u
}

type statusError struct{ code int }

func (e *statusError) Error() string { return "upstream status " + http.StatusText(e.code) }

type sentinel string

func (s sentinel) Error() string { return string(s) }

const (
	errNoTitles  = sentinel("search returned no titles")
	errNoSources = sentinel("no online sources listed")
	errNoStreams = sentinel("no source resolved to a stream")
	errEmptyBody = sentinel("stream body empty")
	errNoUpstream = sentinel("stream url has no fetchable upstream")
)
