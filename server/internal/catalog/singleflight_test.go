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

// Three viewers opening the same shelf on an EMPTY cache must cost one upstream
// call, not three.
func TestColdMissIsSingleFlighted(t *testing.T) {
	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release // hold every in-flight request so the callers really overlap
		_, _ = w.Write([]byte(`{"results":[{"id":1,"title":"A"}]}`))
	}))
	defer srv.Close()
	c := NewClient([]string{srv.URL}, "k", newTestCache(t), slog.Default())

	const n = 3
	var wg sync.WaitGroup
	started := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			if _, err := c.getCached(context.Background(), "list:sf", listTTL, "/x", nil); err != nil {
				t.Errorf("getCached: %v", err)
			}
		}()
	}
	for i := 0; i < n; i++ {
		<-started
	}
	// Let the leader reach the server, then release everything.
	for hits.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
}
