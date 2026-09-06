package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sviniabanditka/promin/server/internal/sources"
)

// A playlist that 307-redirects to a CDN must have its relative children
// rewritten against the CDN (final) URL, not the API host we asked first.
func TestRelayResolvesManifestChildrenAgainstRedirectTarget(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vod/abc/master.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\n/vod/abc/720p/index.m3u8\nseg1.ts\n")
			return
		}
		http.NotFound(w, r)
	}))
	defer cdn.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, cdn.URL+"/vod/abc/master.m3u8", http.StatusTemporaryRedirect)
	}))
	defer api.Close()

	relayAllowLoopback = true
	defer func() { relayAllowLoopback = false }()
	h := relayHandler(slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/relay?u="+strings.TrimPrefix(sources.EncodeRelayURL(api.URL+"/playlists/master.m3u8"), "/relay?u=")+"&t=tok", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "/relay?u=") {
			continue
		}
		u := strings.TrimPrefix(strings.SplitN(line, "&t=", 2)[0], "/relay?u=")
		raw, err := sources.DecodeRelayParam(u)
		if err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		if !strings.HasPrefix(raw, cdn.URL) {
			t.Fatalf("child %q resolved against the API host, want CDN %s", raw, cdn.URL)
		}
	}
	if !strings.Contains(body, "/relay?u=") {
		t.Fatalf("no rewritten children in %q", body)
	}
}
