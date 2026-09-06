package httpapi

import (
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/sviniabanditka/promin/server/webdist"
)

// webRoot returns the web bundle. Normally the embedded copy (compiled in),
// but when PROMIN_WEBDIR points at a directory it serves that live from disk —
// so a plain `npm run build` is enough to iterate without rebuilding the Go
// binary. Production leaves PROMIN_WEBDIR unset and uses the embed.
func webRoot() fs.FS {
	if dir := os.Getenv("PROMIN_WEBDIR"); dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return os.DirFS(dir)
		}
	}
	return webdist.FS
}

// staticHandler serves the embedded web bundle, falling back to
// index.html for any path that isn't a real file (SPA client-side
// routing, e.g. "/title/123"). The Telegram Mini App lives under /tg/ as a
// second SPA (webdist/tg/, docs/miniapp.md) with its own index fallback.
func staticHandler() http.Handler {
	return spaHandler(webRoot())
}

func spaHandler(root fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tg" { // relative asset URLs in the Mini App need the slash
			http.Redirect(w, r, "/tg/", http.StatusMovedPermanently)
			return
		}
		reqPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		index := "index.html"
		if (reqPath == "tg" || strings.HasPrefix(reqPath, "tg/")) && fsExists(root, "tg/index.html") {
			index = "tg/index.html"
		}
		if reqPath == "" || reqPath == "." {
			reqPath = index
		}
		// The precompressed siblings are an implementation detail, never a URL.
		if strings.HasSuffix(reqPath, ".gz") || !fsExists(root, reqPath) {
			// No such file: SPA fallback to the section's index.html.
			serveFile(w, r, root, index)
			return
		}
		serveFile(w, r, root, reqPath)
	})
}

func fsExists(root fs.FS, name string) bool {
	st, err := fs.Stat(root, name)
	return err == nil && !st.IsDir()
}

// serveFile sends one bundle file with the right caching and, when the client
// accepts it, the precompressed .gz sibling the build writes next to it.
//
// Caching: index.html is always revalidated (it carries the ?v= tokens).
// Anything requested WITH ?v=<content hash> is immutable for a year and gets an
// ETag from that hash — so a TV that already has it gets a 304 instead of
// re-downloading 333 KB of app.js daily (the embed FS has no mtime, so
// Last-Modified could never do this). Unversioned files keep a day.
//
// Compression: the origin sent everything raw. Cloudflare compresses on
// promin.club, but the h1 path old Samsung uses (:8444) is not behind it.
func serveFile(w http.ResponseWriter, r *http.Request, root fs.FS, name string) {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// Media Station X fetches /msx/start.json cross-origin from its own host,
	// so the static bundle must allow any origin — otherwise MSX reports
	// "Data error: code 0". Content is public. Full CORS incl. methods/headers
	// so a preflight (if MSX sends one) also passes.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")

	v := r.URL.Query().Get("v")
	switch {
	case path.Base(name) == "index.html":
		w.Header().Set("Cache-Control", "no-cache")
	case v != "":
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("ETag", `"`+v+`"`) // ServeContent honours If-None-Match → 304
	default:
		w.Header().Set("Cache-Control", "max-age=86400")
	}

	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") && fsExists(root, name+".gz") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		http.ServeFileFS(w, r, root, name+".gz")
		return
	}
	http.ServeFileFS(w, r, root, name)
}
