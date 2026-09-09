package proxymon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Captured from a real /v2/package/list answer: the packages sit under a
// Laravel paginator, so the payload nests data.data. An earlier fixture that
// flattened it passed the test while production failed to decode.
const listBody = `{"good":true,"timestamp":1788876445,"data":{
 "current_page":1,"per_page":25,"total":2,"last_page":1,"from":1,"to":2,
 "path":"https://api.stableproxy.com/v2/package/list","next_page_url":null,"prev_page_url":null,
 "data":[
  {"id":50946,"stop_date":"2026-10-06T01:12:28.000000Z","proxy_count":1,
   "bandwidth_summary":{"bytes_used":4891618,"bytes_remaining":995108382,"bytes_limit":1000000000}},
  {"id":50945,"stop_date":"2026-09-13T01:11:10.000000Z","proxy_count":3,
   "bandwidth_summary":{"bytes_used":0,"bytes_remaining":1000000000,"bytes_limit":1000000000}}]}}`

func TestQuotaPublishesEveryPackage(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listBody))
	}))
	defer srv.Close()

	m := New("http://user:pass@rsg-50946.sp2.invalid:11001", "tok", quietLogger())
	if m == nil {
		t.Fatal("New returned nil for a valid proxy url")
	}
	m.apiBase = srv.URL
	m.quota(context.Background())

	if gotAuth != "API-Token tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "API-Token tok")
	}
	if gotPath != "/package/list" {
		t.Errorf("path = %q, want /package/list", gotPath)
	}

	const wantQuota = `# HELP promin_proxy_quota_bytes Traffic package of the residential proxy: used|remaining|limit.
# TYPE promin_proxy_quota_bytes gauge
promin_proxy_quota_bytes{kind="limit",package="50945"} 1e+09
promin_proxy_quota_bytes{kind="limit",package="50946"} 1e+09
promin_proxy_quota_bytes{kind="remaining",package="50945"} 1e+09
promin_proxy_quota_bytes{kind="remaining",package="50946"} 9.95108382e+08
promin_proxy_quota_bytes{kind="used",package="50945"} 0
promin_proxy_quota_bytes{kind="used",package="50946"} 4.891618e+06
`
	if err := testutil.GatherAndCompare(prometheus.DefaultGatherer,
		strings.NewReader(wantQuota), "promin_proxy_quota_bytes"); err != nil {
		t.Error(err)
	}

	// 2026-10-06T01:12:28Z and 2026-09-13T01:11:10Z as unix seconds.
	const wantExpiry = `# HELP promin_proxy_expires_timestamp_seconds Unix time the proxy traffic package expires.
# TYPE promin_proxy_expires_timestamp_seconds gauge
promin_proxy_expires_timestamp_seconds{package="50945"} 1.78926187e+09
promin_proxy_expires_timestamp_seconds{package="50946"} 1.791249148e+09
`
	if err := testutil.GatherAndCompare(prometheus.DefaultGatherer,
		strings.NewReader(wantExpiry), "promin_proxy_expires_timestamp_seconds"); err != nil {
		t.Error(err)
	}

	// Only the package named by the proxy host counts as in use — alerts join
	// on this so the idle package cannot page.
	const wantActive = `# HELP promin_proxy_package_active 1 for the traffic package the configured proxy actually uses.
# TYPE promin_proxy_package_active gauge
promin_proxy_package_active{package="50945"} 0
promin_proxy_package_active{package="50946"} 1
`
	if err := testutil.GatherAndCompare(prometheus.DefaultGatherer,
		strings.NewReader(wantActive), "promin_proxy_package_active"); err != nil {
		t.Error(err)
	}
}

func TestPackageFromHost(t *testing.T) {
	for _, c := range []struct{ host, want string }{
		{"rsg-50946.sp2.ovh", "50946"},
		{"rsg-50945.sp2.ovh", "50945"},
		{"proxy.example.com", ""},  // no digits at all
		{"gw-12.sp2.ovh", ""},      // too short to be a package id
		{"50946.sp2.ovh", "50946"}, // bare id
	} {
		if got := packageFromHost(c.host); got != c.want {
			t.Errorf("packageFromHost(%q) = %q, want %q", c.host, got, c.want)
		}
	}
}

func TestNewWithoutProxyIsNil(t *testing.T) {
	if m := New("", "tok", quietLogger()); m != nil {
		t.Error("New with no proxy url should return nil")
	}
	// A nil monitor must not panic when started.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var m *Monitor
	m.Run(ctx)
}

func TestProbeMarksProxyDown(t *testing.T) {
	m := New("http://127.0.0.1:1/", "", quietLogger()) // nothing listens on port 1
	m.probeURL = "http://example.invalid/"
	m.probe(context.Background())

	const want = `# HELP promin_proxy_up 1 when the last fetch through the residential proxy succeeded.
# TYPE promin_proxy_up gauge
promin_proxy_up 0
`
	if err := testutil.GatherAndCompare(prometheus.DefaultGatherer,
		strings.NewReader(want), "promin_proxy_up"); err != nil {
		t.Error(err)
	}
}
