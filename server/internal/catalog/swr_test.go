package catalog

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls until cond holds or the deadline passes (background refresh is
// asynchronous by design, so tests observe it rather than assume timing).
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// A stale entry must be served IMMEDIATELY (no upstream wait) and refreshed
// behind the response — that is what turns the cache into a local library.
func TestStaleServedInstantlyThenRefreshed(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(120 * time.Millisecond) // upstream is slow
		_, _ = w.Write([]byte(`{"v":"fresh"}`))
	}))
	defer srv.Close()

	cache := newTestCache(t)
	// Seed an EXPIRED entry.
	if err := cache.Set("k", `{"v":"stale"}`, time.Now().Unix()-1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := NewClient([]string{srv.URL}, "key", cache, slog.Default())

	start := time.Now()
	body, err := c.getCached(context.Background(), "k", time.Hour, "/x", nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("getCached: %v", err)
	}
	if string(body) != `{"v":"stale"}` {
		t.Errorf("want the stale copy served, got %s", body)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("stale read waited on the network (%v) — should be instant", elapsed)
	}

	// ...and the refresh lands in the cache shortly after.
	if !waitFor(t, 2*time.Second, func() bool {
		row, err := cache.Get("k")
		return err == nil && row.JSON == `{"v":"fresh"}`
	}) {
		t.Error("background refresh never updated the cache")
	}
}

// Many concurrent readers of the same stale key must trigger ONE refresh.
func TestStaleRefreshIsSingleFlighted(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(80 * time.Millisecond)
		_, _ = w.Write([]byte(`{"v":"fresh"}`))
	}))
	defer srv.Close()

	cache := newTestCache(t)
	cache.Set("k", `{"v":"stale"}`, time.Now().Unix()-1)
	c := NewClient([]string{srv.URL}, "key", cache, slog.Default())

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.getCached(context.Background(), "k", time.Hour, "/x", nil); err != nil {
				t.Errorf("getCached: %v", err)
			}
		}()
	}
	wg.Wait()
	waitFor(t, 2*time.Second, func() bool { return hits.Load() > 0 })
	time.Sleep(200 * time.Millisecond) // let any stragglers fire

	if h := hits.Load(); h != 1 {
		t.Errorf("want exactly 1 upstream refresh for 20 readers, got %d", h)
	}
}

// A COLD miss (nothing cached) still blocks and fetches — there is nothing to
// serve otherwise.
func TestColdMissStillFetches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"v":"first"}`))
	}))
	defer srv.Close()
	c := NewClient([]string{srv.URL}, "key", newTestCache(t), slog.Default())

	body, err := c.getCached(context.Background(), "cold", time.Hour, "/x", nil)
	if err != nil {
		t.Fatalf("cold fetch: %v", err)
	}
	if string(body) != `{"v":"first"}` {
		t.Errorf("got %s", body)
	}
}

// A dead upstream must not stop stale reads: the local copy keeps serving and
// the failed refresh is silent (this is the "TMDB down, app still works" case).
func TestStaleSurvivesDeadUpstream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cache := newTestCache(t)
	cache.Set("k", `{"v":"stale"}`, time.Now().Unix()-1)
	c := NewClient([]string{srv.URL}, "key", cache, slog.Default())

	for i := 0; i < 3; i++ {
		body, err := c.getCached(context.Background(), "k", time.Hour, "/x", nil)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if string(body) != `{"v":"stale"}` {
			t.Errorf("read %d: got %s", i, body)
		}
	}
	// The stale copy must NOT be clobbered by a failed refresh.
	if row, err := cache.Get("k"); err != nil || row.JSON != `{"v":"stale"}` {
		t.Errorf("failed refresh damaged the cached copy: %+v err=%v", row, err)
	}
}

// The request context dying (response written) must not abort the refresh.
func TestRefreshOutlivesRequestContext(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte(`{"v":"fresh"}`))
	}))
	defer srv.Close()

	cache := newTestCache(t)
	cache.Set("k", `{"v":"stale"}`, time.Now().Unix()-1)
	c := NewClient([]string{srv.URL}, "key", cache, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := c.getCached(ctx, "k", time.Hour, "/x", nil); err != nil {
		t.Fatalf("getCached: %v", err)
	}
	cancel() // request is done; the refresh must continue

	if !waitFor(t, 2*time.Second, func() bool {
		row, err := cache.Get("k")
		return err == nil && row.JSON == `{"v":"fresh"}`
	}) {
		t.Error("refresh died with the request context")
	}
}
