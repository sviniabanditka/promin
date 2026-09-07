package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHTTPRecordsRouteAndStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/catalog/title/{tmdb_id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {})
	mux.Handle("/", http.NotFoundHandler())
	h := HTTP(mux)

	for _, p := range []string{"/api/v1/catalog/title/42", "/healthz", "/some/static/file.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
	}

	if got := testutil.ToFloat64(httpRequests.WithLabelValues("GET /api/v1/catalog/title/{tmdb_id}", "GET", "418")); got != 1 {
		t.Fatalf("route counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(httpRequests.WithLabelValues("static", "GET", "404")); got != 1 {
		t.Fatalf("static counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(httpRequests.WithLabelValues("GET /healthz", "GET", "200")); got != 0 {
		t.Fatalf("healthz must be skipped, got %v", got)
	}
}

func TestSearchOutcome(t *testing.T) {
	cases := []struct {
		matched, replied bool
		want             string
	}{
		{true, true, OutcomeMatch},
		{true, false, OutcomeMatch},
		{false, true, OutcomeNoMatch},
		{false, false, OutcomeError},
	}
	for _, c := range cases {
		if got := SearchOutcome(c.matched, c.replied); got != c.want {
			t.Errorf("SearchOutcome(%v,%v)=%q want %q", c.matched, c.replied, got, c.want)
		}
	}
}
