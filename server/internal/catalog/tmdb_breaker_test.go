package catalog

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// newTestCache spins up a throwaway on-disk sqlite so getCached has a real
// TMDBCacheRepo to read/write.
func newTestCache(t *testing.T) *store.TMDBCacheRepo {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.TMDBCache
}

// TestMultiHostFallback: primary 500s, fallback answers → client returns the
// fallback body.
func TestMultiHostFallback(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ok":true}`)
	}))
	defer fallback.Close()

	c := NewClient([]string{primary.URL, fallback.URL}, "k", newTestCache(t), noopLogger())
	body, err := c.getCached(context.Background(), "key1", time.Hour, "/x", nil)
	if err != nil {
		t.Fatalf("getCached: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("got %q, want fallback body", body)
	}
}

// TestCircuitBreakerServesStaleFast: once the (only) host fails, a second call
// with a stale cache entry must skip the network entirely and return stale —
// verified by counting upstream hits (breaker should keep it at 1).
func TestCircuitBreakerServesStaleFast(t *testing.T) {
	var hits atomic.Int32
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer dead.Close()

	cache := newTestCache(t)
	// seed a stale entry (expired) so serve-stale has something to return
	if err := cache.Set("key2", `{"stale":true}`, time.Now().Unix()-1); err != nil {
		t.Fatalf("seed: %v", err)
	}

	c := NewClient([]string{dead.URL}, "k", cache, noopLogger())

	// Under stale-while-revalidate a stale read NEVER blocks: it returns the
	// local copy and revalidates behind the response. So the first read is
	// instant, and the (failing) background refresh is what trips the breaker.
	if body, err := c.getCached(context.Background(), "key2", time.Hour, "/x", nil); err != nil || string(body) != `{"stale":true}` {
		t.Fatalf("first call: body=%q err=%v", body, err)
	}

	// Wait for that background refresh to fail and trip the breaker.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && hits.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if hits.Load() != 1 {
		t.Fatalf("background refresh did not run exactly once: %d", hits.Load())
	}

	// Breaker is now open: further stale reads must serve locally and schedule
	// NO further network work — the original point of this test.
	for i := 0; i < 5; i++ {
		if body, err := c.getCached(context.Background(), "key2", time.Hour, "/x", nil); err != nil || string(body) != `{"stale":true}` {
			t.Fatalf("read %d: body=%q err=%v", i, body, err)
		}
	}
	time.Sleep(150 * time.Millisecond) // give any stray refresh a chance to show up
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hit %d times, want 1 (open breaker must skip the network)", got)
	}
}

func noopLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
