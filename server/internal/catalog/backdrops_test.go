package catalog

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// discoverSrv answers /discover/* with two items, one of which has no backdrop.
func discoverSrv(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// Echo the page so each page yields distinct art (like the real API).
		page := r.URL.Query().Get("page")
		_, _ = w.Write([]byte(`{"page":1,"total_pages":500,"results":[
			{"id":1,"title":"A","backdrop_path":"/a` + page + `.jpg","vote_average":7},
			{"id":2,"title":"B","backdrop_path":"","vote_average":6}
		]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRandomBackdropsCollectsAndFilters(t *testing.T) {
	var hits atomic.Int32
	srv := discoverSrv(t, &hits)
	svc := NewService(NewClient([]string{srv.URL}, "k", newTestCache(t), slog.Default()), nil)

	got := svc.RandomBackdrops(context.Background(), "uk", 40)
	if len(got) == 0 {
		t.Fatal("no backdrops collected")
	}
	seen := map[string]bool{}
	for _, b := range got {
		// Served through our own image proxy at w1280 — the size the title backdrop
		// layer already uses — so the /img disk cache is shared, not duplicated.
		if !strings.HasPrefix(b.URL, "/img/w1280/") {
			t.Errorf("not proxied at the shared size: %q", b.URL)
		}
		// The item without a backdrop_path must be skipped, never emitted bare.
		if b.URL == "/img/w1280/" {
			t.Error("empty backdrop path emitted")
		}
		// The screensaver names what is on screen, so a title is required.
		if b.Title == "" {
			t.Errorf("backdrop without a title: %+v", b)
		}
		if seen[b.URL] {
			t.Errorf("duplicate backdrop %q", b.URL)
		}
		seen[b.URL] = true
	}
	if hits.Load() == 0 {
		t.Error("never queried discover")
	}
}

// The vote floor and adult exclusion must actually reach TMDB — they are what
// keep the screensaver from showing the untended tail of the catalog.
func TestRandomBackdropsSendsQualityFilters(t *testing.T) {
	var gotQuery atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.RawQuery)
		_, _ = w.Write([]byte(`{"results":[{"id":1,"title":"A","backdrop_path":"/a.jpg"}]}`))
	}))
	defer srv.Close()
	svc := NewService(NewClient([]string{srv.URL}, "k", newTestCache(t), slog.Default()), nil)
	svc.RandomBackdrops(context.Background(), "uk", 1)

	q, _ := gotQuery.Load().(string)
	for _, want := range []string{"vote_count.gte=" + backdropVoteFloor, "include_adult=false", "page="} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q: %s", want, q)
		}
	}
}

// A dead upstream must yield an empty slice, not an error/panic: the
// screensaver then simply shows the clock.
func TestRandomBackdropsDegradesQuietly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	svc := NewService(NewClient([]string{srv.URL}, "k", newTestCache(t), slog.Default()), nil)
	if got := svc.RandomBackdrops(context.Background(), "uk", 20); len(got) != 0 {
		t.Errorf("expected nothing from a dead upstream, got %d", len(got))
	}
}
