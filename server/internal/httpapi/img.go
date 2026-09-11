package httpapi

import (
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// sizeRe accepts any TMDB image size token: "w<number>" or "original".
// docs/api.md enumerates w200|w500|original as examples, but TMDB
// itself serves a wider fixed set (w92,w154,...,w1280,original) depending
// on poster vs backdrop — the proxy stays permissive and lets TMDB itself
// 404 on a bogus size rather than hardcoding the enum here.
var sizeRe = regexp.MustCompile(`^(w\d{2,4}|original)$`)

// negativeTTL: how long a TMDB 404 for a poster is remembered. Every render of
// a shelf with a dead poster used to re-ask TMDB.
const negativeTTL = time.Hour

// imgProxy proxies+caches TMDB images through our own domain: TV webviews
// often can't reach image.tmdb.org directly (blocks/CORS), and per
// docs/api.md the cache is permanent on disk — once
// downloaded, a poster is served straight from
// PROMIN_DATA_DIR/img/<size>/<file> without hitting TMDB again.
func imgProxy(dataDir string, logger *slog.Logger) http.Handler {
	// A cold home fires ~100 poster fetches at once; the default transport keeps
	// only 2 idle connections per host, so nearly every one paid a TLS handshake.
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:        32,
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
	var negMu sync.Mutex
	negative := map[string]time.Time{} // disk path → expiry of a remembered 404

	// The route is open (poster <img> tags carry no token) and every hit is
	// written to the shared data volume forever, so an anonymous client could
	// fill the disk that holds promin.db. Bound it three ways: a per-IP rate
	// cap, a size cap on the cache directory (oldest files go first), and a
	// cap on the remembered-404 map.
	limiter := newIPLimiter(600, time.Minute) // a Home screen is ~120 posters
	go sweepImgCache(filepath.Join(dataDir, "img"), imgCacheLimitBytes, logger)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.allow(clientIP(r)) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/img/")
		size, file, ok := strings.Cut(rest, "/")
		if !ok || size == "" || file == "" || strings.Contains(file, "..") || strings.Contains(file, "/") {
			http.NotFound(w, r)
			return
		}
		if !sizeRe.MatchString(size) {
			writeBadRequest(w, "невірний розмір зображення")
			return
		}

		diskPath := filepath.Join(dataDir, "img", size, file)

		if st, err := os.Stat(diskPath); err == nil && !st.IsDir() {
			logger.Debug("img cache hit", "size", size, "file", file)
			serveImageFile(w, r, file, diskPath)
			return
		}

		negMu.Lock()
		until, known := negative[diskPath]
		if known && time.Now().After(until) {
			delete(negative, diskPath)
			known = false
		}
		negMu.Unlock()
		if known {
			http.NotFound(w, r)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://image.tmdb.org/t/p/"+size+"/"+file, nil)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			negMu.Lock()
			if len(negative) > 10000 {
				negative = map[string]time.Time{} // ponytail: flush instead of LRU; entries are cheap to re-learn
			}
			negative[diskPath] = time.Now().Add(negativeTTL)
			negMu.Unlock()
			http.NotFound(w, r)
			return
		}
		if resp.StatusCode != http.StatusOK {
			http.Error(w, "upstream status", resp.StatusCode)
			return
		}

		// Stream the body to a unique temp file (no whole-image buffer in
		// memory — an `original` backdrop is up to 3 MB), rename it into place,
		// then serve from disk like a hit. A failed cache write still serves
		// the bytes: fall back to streaming the rest straight through.
		if err := os.MkdirAll(filepath.Dir(diskPath), 0o755); err != nil {
			logger.Warn("img cache mkdir failed", "path", diskPath, "error", err)
			serveImageStream(w, file, resp.Body)
			return
		}
		tmp, err := os.CreateTemp(filepath.Dir(diskPath), "img-*")
		if err != nil {
			logger.Warn("img cache temp failed", "path", diskPath, "error", err)
			serveImageStream(w, file, resp.Body)
			return
		}
		_, werr := io.Copy(tmp, resp.Body)
		tmp.Close()
		if werr != nil {
			logger.Warn("img cache write failed", "path", tmp.Name(), "error", werr)
			os.Remove(tmp.Name())
			http.Error(w, "upstream read error", http.StatusBadGateway)
			return
		}
		if rerr := os.Rename(tmp.Name(), diskPath); rerr != nil {
			logger.Warn("img cache rename failed", "path", diskPath, "error", rerr)
			// Serve the temp file anyway, then drop it.
			serveImageFile(w, r, file, tmp.Name())
			os.Remove(tmp.Name())
			return
		}
		logger.Debug("img cache miss (fetched from tmdb)", "size", size, "file", file)
		serveImageFile(w, r, file, diskPath)
	})
}

func imageHeaders(w http.ResponseWriter, file string) {
	if ct := mime.TypeByExtension(filepath.Ext(file)); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "image/jpeg")
	}
	// Permanent cache, per docs/api.md ("бессрочный кэш").
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
}

// serveImageFile streams a cached image from disk: Content-Length, Range and
// Last-Modified for free, and no 1 MB buffer per request.
func serveImageFile(w http.ResponseWriter, r *http.Request, file, diskPath string) {
	imageHeaders(w, file)
	http.ServeFile(w, r, diskPath)
}

func serveImageStream(w http.ResponseWriter, file string, body io.Reader) {
	imageHeaders(w, file)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

// imgCacheLimitBytes caps the on-disk poster cache. The catalog a household
// actually browses is a few GB; the rest is churn.
const imgCacheLimitBytes = 8 << 30

// sweepImgCache deletes the oldest files under dir until the total is below
// limit. Runs at start and hourly; a full walk of the cache is cheap next to
// the disk it protects.
func sweepImgCache(dir string, limit int64, logger *slog.Logger) {
	for {
		sweepImgCacheOnce(dir, limit, logger)
		time.Sleep(time.Hour)
	}
}

func sweepImgCacheOnce(dir string, limit int64, logger *slog.Logger) {
	type f struct {
		path string
		size int64
		mod  time.Time
	}
	var files []f
	var total int64
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, f{p, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})
	if total <= limit {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	target := limit * 8 / 10
	removed := 0
	for _, x := range files {
		if total <= target {
			break
		}
		if os.Remove(x.path) == nil {
			total -= x.size
			removed++
		}
	}
	logger.Info("img cache swept", "removed", removed, "bytes_left", total)
}
