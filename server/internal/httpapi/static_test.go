package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func staticTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":     {Data: []byte("<html>index</html>")},
		"app.js":         {Data: []byte("plain js")},
		"app.js.gz":      {Data: []byte("GZIPPED")}, // content irrelevant; we check negotiation
		"vendor/x.js":    {Data: []byte("vendor")},
		"msx/start.json": {Data: []byte("{}")},
	}
}

func get(t *testing.T, root fstest.MapFS, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := r.URL.Path[1:]
		if reqPath == "" {
			reqPath = "index.html"
		}
		if !fsExists(root, reqPath) {
			reqPath = "index.html"
		}
		serveFile(w, r, root, reqPath)
	})
	h.ServeHTTP(rr, req)
	return rr
}

// A versioned asset is immutable for a year and revalidates by ETag → 304.
func TestStaticVersionedAssetIsImmutableWithETag(t *testing.T) {
	rr := get(t, staticTestFS(), "/app.js?v=abc123", nil)
	if cc := rr.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if et := rr.Header().Get("ETag"); et != `"abc123"` {
		t.Fatalf("ETag = %q", et)
	}
	rr2 := get(t, staticTestFS(), "/app.js?v=abc123", map[string]string{"If-None-Match": `"abc123"`})
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("revalidation status = %d, want 304", rr2.Code)
	}
}

// index.html must always be revalidated: it carries the ?v= tokens.
func TestStaticIndexNoCache(t *testing.T) {
	rr := get(t, staticTestFS(), "/", nil)
	if cc := rr.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q", cc)
	}
}

// gzip is served only when accepted, with Vary, keeping the original type.
func TestStaticServesPrecompressedWhenAccepted(t *testing.T) {
	rr := get(t, staticTestFS(), "/app.js", map[string]string{"Accept-Encoding": "gzip, deflate"})
	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("no gzip: %v", rr.Header())
	}
	if rr.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("Vary = %q", rr.Header().Get("Vary"))
	}
	if ct := rr.Header().Get("Content-Type"); ct == "" || ct == "application/gzip" || ct == "application/x-gzip" {
		t.Fatalf("Content-Type must stay the script type, got %q", ct)
	}
	if rr.Body.String() != "GZIPPED" {
		t.Fatalf("body = %q, want the .gz sibling", rr.Body.String())
	}

	plain := get(t, staticTestFS(), "/app.js", nil)
	if plain.Header().Get("Content-Encoding") != "" || plain.Body.String() != "plain js" {
		t.Fatalf("client without gzip got %q / %q", plain.Header().Get("Content-Encoding"), plain.Body.String())
	}
	// No sibling → plain even when gzip is accepted.
	v := get(t, staticTestFS(), "/vendor/x.js", map[string]string{"Accept-Encoding": "gzip"})
	if v.Header().Get("Content-Encoding") != "" {
		t.Fatalf("vendor/x.js has no .gz but was sent encoded")
	}
}
