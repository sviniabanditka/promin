package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// /img/<size>/<file> maps straight onto a disk path: every escape from the
// cache directory must 404 before touching the filesystem or TMDB.
func TestImgProxyRejectsTraversalAndBadSize(t *testing.T) {
	dir := t.TempDir()
	h := imgProxy(dir, slog.Default())
	cases := map[string]int{
		"/img/w342/../secret.jpg":  http.StatusNotFound,
		"/img/w342/a%2F..%2Fb.jpg": http.StatusNotFound, // decoded "/" inside the file segment
		"/img/w342/":               http.StatusNotFound,
		"/img/original":            http.StatusNotFound,
		"/img/huge/x.jpg":          http.StatusBadRequest,
		"/img/w342x/x.jpg":         http.StatusBadRequest,
		"/img/../../etc/passwd":    http.StatusNotFound, // ".." is caught before the size check
	}
	for target, want := range cases {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		if rr.Code != want {
			t.Errorf("%s → %d, want %d", target, rr.Code, want)
		}
	}
}

// A cached file is served from disk with the permanent-cache headers and no
// upstream call (the test has no network).
func TestImgProxyServesDiskHit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "img", "w342", "poster.jpg")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("JPEGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	imgProxy(dir, slog.Default()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/img/w342/poster.jpg", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "JPEGDATA" {
		t.Fatalf("status %d body %q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if rr.Header().Get("Content-Length") == "" {
		t.Error("ServeFile should give Content-Length")
	}
}
