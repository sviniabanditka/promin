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
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
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

	// Overridden by tests.
	probeURL string
	apiBase  string
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
		token:    token,
		log:      logger,
		probeURL: defaultProbeURL,
		apiBase:  defaultAPIBase,
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
func (m *Monitor) probe(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.probeURL, nil)
	if err != nil {
		return
	}
	start := time.Now()
	resp, err := m.viaProxy.Do(req)
	d := time.Since(start)
	if err != nil {
		metrics.SetProxyUp(false, d)
		m.log.Warn("proxymon: probe failed", "error", err, "took", d)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	ok := resp.StatusCode < 400
	metrics.SetProxyUp(ok, d)
	if !ok {
		m.log.Warn("proxymon: probe status", "status", resp.StatusCode, "took", d)
	}
}

type pkgList struct {
	Good bool `json:"good"`
	Data []struct {
		ID        int64  `json:"id"`
		StopDate  string `json:"stop_date"`
		Bandwidth struct {
			Used      float64 `json:"bytes_used"`
			Remaining float64 `json:"bytes_remaining"`
			Limit     float64 `json:"bytes_limit"`
		} `json:"bandwidth_summary"`
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
	for _, p := range pl.Data {
		id := strconv.FormatInt(p.ID, 10)
		metrics.SetProxyQuota(id, p.Bandwidth.Used, p.Bandwidth.Remaining, p.Bandwidth.Limit)
		if t, err := time.Parse(time.RFC3339, p.StopDate); err == nil {
			metrics.SetProxyExpiry(id, t)
		}
	}
}
