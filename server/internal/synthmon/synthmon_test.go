package synthmon

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sviniabanditka/promin/server/internal/catalog"
)

func TestFirstBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.m3u8":
			if r.Header.Get("Range") == "" {
				t.Errorf("expected a Range header")
			}
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n"))
		case "/empty":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()

	n, err := firstBytes(context.Background(), srv.Client(), srv.URL+"/ok.m3u8")
	if err != nil || n == 0 {
		t.Fatalf("playlist: n=%d err=%v", n, err)
	}
	if _, err := firstBytes(context.Background(), srv.Client(), srv.URL+"/empty"); err != errEmptyBody {
		t.Fatalf("empty body: err=%v", err)
	}
	if _, err := firstBytes(context.Background(), srv.Client(), srv.URL+"/nope"); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("403: err=%v", err)
	}
}

func TestPickTitle(t *testing.T) {
	items := []catalog.Title{{TMDBID: 1, Type: "person"}, {TMDBID: 2, Type: "tv"}, {TMDBID: 3, Type: "movie"}}
	if got := pickTitle(items); got == nil || got.TMDBID != 3 {
		t.Fatalf("want the movie, got %+v", got)
	}
	if got := pickTitle(items[:2]); got == nil || got.TMDBID != 2 {
		t.Fatalf("want the series when no movie, got %+v", got)
	}
	if pickTitle(nil) != nil {
		t.Fatal("nil for no items")
	}
}

func TestUpstreamOf(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString([]byte("https://cdn.example/v.m3u8"))
	if got := upstreamOf("/relay?u=" + enc); got != "https://cdn.example/v.m3u8" {
		t.Fatalf("relay: %q", got)
	}
	if got := upstreamOf("/remux?u=" + enc + "&kind=copy_hls&audio=0"); got != "https://cdn.example/v.m3u8" {
		t.Fatalf("remux: %q", got)
	}
	if upstreamOf("/stream/abc/0?t=x") != "" || upstreamOf("https://direct.example/a.mp4") != "https://direct.example/a.mp4" {
		t.Fatal("bare path must be empty, absolute URL unchanged")
	}
}
