// Package proxymon watches the residential HTTP proxy that native providers
// use for catalog pages (config.NativeProxyURL): whether it still answers, how
// fast, and how much of the paid traffic package is left.
//
// It lives in the app rather than in a separate exporter because the app is
// the only place that already holds the proxy credentials — putting them in a
// blackbox-exporter ConfigMap would write them to cluster config in the clear.
// Metrics land on the existing /metrics listener, so the ServiceMonitor and
// the Grafana dashboard need no new plumbing.
package proxymon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sviniabanditka/promin/server/internal/metrics"
)

const (
	// One tick drives both checks. Every probe spends proxy traffic, so this is
	// deliberately slow: 5 min is ~9 MB a month out of a 1 GB package.
	interval = 5 * time.Minute
	// Small, stable and cheap to fetch. The point is "does the proxy forward
	// traffic", not what the target says, so it is a neutral endpoint rather
	// than a provider catalogue we would otherwise hammer 288 times a day.
	defaultProbeURL = "https://1.1.1.1/cdn-cgi/trace"
	defaultAPIBase  = "https://api.stableproxy.com/v2"
)

// Monitor probes the proxy and, when a StableProxy API token is configured,
// reads the traffic packages. An empty token disables the quota half.
type Monitor struct {
	token    string
	log      *slog.Logger
	viaProxy *http.Client
	direct   *http.Client

	// activePkg is the traffic package the configured proxy draws on, read off
	// the gateway host ("rsg-50946.sp2.ovh" → "50946"). Empty when the host does
	// not carry one; the ProminProxyPackageUnknown alert covers that case.
	activePkg string

	// Overridden by tests.
	probeURL string
	apiBase  string
}

// packageFromHost pulls the package id out of the proxy gateway host. The
// vendor names each package's gateway after it, so the first digit run in the
// leftmost label is the id. Returns "" when the host looks different.
func packageFromHost(host string) string {
	label, _, _ := strings.Cut(host, ".")
	start := strings.IndexFunc(label, func(r rune) bool { return r >= '0' && r <= '9' })
	if start < 0 {
		return ""
	}
	end := start
	for end < len(label) && label[end] >= '0' && label[end] <= '9' {
		end++
	}
	if end-start < 4 { // ids are five digits; a shorter run is something else
		return ""
	}
	return label[start:end]
}

// New returns nil when there is no proxy to watch.
func New(proxyURL, token string, logger *slog.Logger) *Monitor {
	if proxyURL == "" {
		return nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		logger.Warn("proxymon: bad proxy url", "error", err)
		return nil
	}
	return &Monitor{
		token:     token,
		log:       logger,
		activePkg: packageFromHost(u.Hostname()),
		probeURL:  defaultProbeURL,
		apiBase:   defaultAPIBase,
		viaProxy: &http.Client{
			Timeout:   20 * time.Second,
			Transport: &http.Transport{Proxy: http.ProxyURL(u)},
		},
		direct: &http.Client{Timeout: 20 * time.Second},
	}
}

// Run ticks until ctx is cancelled. Safe on a nil Monitor.
func (m *Monitor) Run(ctx context.Context) {
	if m == nil {
		return
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
	m.probe(ctx)
	if m.token != "" {
		m.quota(ctx)
	}
}

// probe fetches a tiny page through the proxy and publishes up/duration.
//
// Two attempts: the gateway hands out a residential peer per session and
// answers 407 ("Stable Proxy - Peer") when it cannot allocate one, which is a
// transient condition rather than a dead proxy. One retry keeps a single
// hiccup from showing DOWN on the dashboard for the whole 5-minute interval.
func (m *Monitor) probe(ctx context.Context) {
	var lastErr error
	var d time.Duration
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
		ok, dur, err := m.probeOnce(ctx)
		d, lastErr = dur, err
		if ok {
			metrics.SetProxyUp(true, d)
			return
		}
	}
	metrics.SetProxyUp(false, d)
	m.log.Warn("proxymon: proxy unreachable after 2 attempts", "error", lastErr, "took", d)
}

func (m *Monitor) probeOnce(ctx context.Context) (bool, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.probeURL, nil)
	if err != nil {
		return false, 0, err
	}
	start := time.Now()
	resp, err := m.viaProxy.Do(req)
	d := time.Since(start)
	if err != nil {
		return false, d, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		return false, d, fmt.Errorf("status %d", resp.StatusCode)
	}
	return true, d, nil
}

// pkgList mirrors GET /v2/package/list. The vendor wraps the payload twice:
// {"good":…,"data":{…Laravel paginator…,"data":[packages]}}. One page holds 25
// and an account has a handful, so the paginator is read but not followed.
type pkgList struct {
	Good bool `json:"good"`
	Data struct {
		Packages []struct {
			ID        int64  `json:"id"`
			StopDate  string `json:"stop_date"`
			Bandwidth struct {
				Used      float64 `json:"bytes_used"`
				Remaining float64 `json:"bytes_remaining"`
				Limit     float64 `json:"bytes_limit"`
			} `json:"bandwidth_summary"`
		} `json:"data"`
	} `json:"data"`
}

// quota reads the traffic packages from the provider API. The call goes out
// directly, not through the proxy: the account API is not what the proxy is
// for, and routing it there would spend the very quota it reports.
func (m *Monitor) quota(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.apiBase+"/package/list", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "API-Token "+m.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.direct.Do(req)
	if err != nil {
		m.log.Warn("proxymon: package list failed", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		m.log.Warn("proxymon: package list status", "status", resp.StatusCode)
		return
	}
	var pl pkgList
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&pl); err != nil {
		m.log.Warn("proxymon: package list decode", "error", err)
		return
	}
	for _, p := range pl.Data.Packages {
		id := strconv.FormatInt(p.ID, 10)
		metrics.SetProxyQuota(id, p.Bandwidth.Used, p.Bandwidth.Remaining, p.Bandwidth.Limit)
		metrics.SetProxyPackageActive(id, id == m.activePkg)
		if t, err := time.Parse(time.RFC3339, p.StopDate); err == nil {
			metrics.SetProxyExpiry(id, t)
		}
	}
}
