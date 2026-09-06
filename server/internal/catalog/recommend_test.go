package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// seedDetailJSON builds a movie-detail cache body with n recommendations.
func seedDetailJSON(id int, name string, n int) string {
	recs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, fmt.Sprintf(`{"id":%d,"title":"Rec %d","poster_path":"/p%d.jpg"}`, 1000+i, i, i))
	}
	return fmt.Sprintf(
		`{"id":%d,"title":%q,"genres":[{"id":28,"name":"Action"}],
		  "credits":{"cast":[{"id":500,"name":"Jackie Chan","order":0}]},
		  "recommendations":{"results":[%s]},"similar":{"results":[]}}`,
		id, name, strings.Join(recs, ","))
}

// TestRecsRowCacheOnly proves RecsRow reads the seed straight from tmdb_cache
// (stale-ok: expiresAt=0/past) and NEVER hits the network — the base URL points
// at a server that fails the test if called.
func TestRecsRowCacheOnly(t *testing.T) {
	cache := newTestCache(t)
	// Stale entry (expiresAt in the past) must still be read.
	if err := cache.Set("title:movie:100:uk", seedDetailJSON(100, "Rush Hour", 10), 1); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	netHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		netHit = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	svc := NewService(NewClient([]string{srv.URL}, "k", cache, slog.Default()), nil)

	row := svc.RecsRow("uk", "movie", 100, map[int]bool{})
	if row == nil {
		t.Fatal("expected a recs row from a cache-warm seed")
	}
	if netHit {
		t.Fatal("RecsRow hit the network — must be cache-only")
	}
	if !strings.Contains(row.Title, "Rush Hour") {
		t.Fatalf("seed name not in title: %q", row.Title)
	}
	if len(row.Items) < minShelfItems {
		t.Fatalf("want >=%d items, got %d", minShelfItems, len(row.Items))
	}

	// Cache miss → nil, still no network.
	if got := svc.RecsRow("uk", "movie", 999, map[int]bool{}); got != nil {
		t.Fatal("expected nil for a cache-miss seed")
	}
	if netHit {
		t.Fatal("cache-miss RecsRow hit the network")
	}
}

// TestRecsRowExcludeAndThinDrop: excluded ids are filtered, and a shelf that
// falls under the minimum after exclusion is dropped (nil).
func TestRecsRowExcludeAndThinDrop(t *testing.T) {
	cache := newTestCache(t)
	cache.Set("title:movie:200:uk", seedDetailJSON(200, "Seed", 10), 1)
	svc := NewService(NewClient([]string{"http://127.0.0.1:0"}, "k", cache, slog.Default()), nil)

	// Exclude the first 4 recs (ids 1000..1003) → 6 remain, below min 8 → drop.
	exclude := map[int]bool{1000: true, 1001: true, 1002: true, 1003: true}
	if row := svc.RecsRow("uk", "movie", 200, exclude); row != nil {
		t.Fatalf("expected thin shelf dropped, got %d items", len(row.Items))
	}
	// The kept picks must have been recorded in exclude (cross-shelf dedup),
	// even though the shelf was dropped — pick() marks as it goes.
	if !exclude[1004] {
		t.Fatal("pick did not record kept ids in the exclude set")
	}
}

// TestPersonalRowsEmptyGuard: no finished seeds → no personal shelves, no panic.
func TestPersonalRowsEmptyGuard(t *testing.T) {
	cache := newTestCache(t)
	svc := NewService(NewClient([]string{"http://127.0.0.1:0"}, "k", cache, slog.Default()), nil)
	if rows := svc.PersonalRows(context.Background(), "uk", nil, nil, 0); rows != nil {
		t.Fatalf("expected nil rows for empty finished, got %d", len(rows))
	}
}
