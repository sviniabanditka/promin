package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"syscall"
	"time"

	"github.com/sviniabanditka/promin/server/internal/metrics"
	"github.com/sviniabanditka/promin/server/internal/sources"
)

// relayClient has no fixed Timeout: segment/mp4 bodies can legitimately
// stream for the whole duration of playback. Each request instead gets a
// deadline derived from the incoming request's context (client disconnect
// cancels it) plus a bounded dial/header timeout via relayTransport.
// MaxIdleConnsPerHost keeps HLS segment fetches on warm connections (default 2
// re-dialed mid-playlist). CheckRedirect re-validates each redirect hop so a
// 30x can't bounce the proxy to a loopback/metadata target.
var relayClient = &http.Client{
	Transport: &http.Transport{
		// Control sees the resolved address, so a hostname that points at a
		// cluster service, the node or loopback is refused even though
		// validateUpstream could only inspect IP literals (SSRF via DNS).
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: guardDial}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if err := validateUpstream(req.URL); err != nil {
			return err
		}
		if len(via) >= 10 {
			return errors.New("relay: too many redirects")
		}
		return nil
	},
}

// validateUpstream rejects an upstream whose host is a LITERAL loopback /
// link-local / unspecified IP — the SSRF targets that matter (127.0.0.1,
// 169.254.169.254 cloud metadata, ::1, 0.0.0.0). Private ranges are allowed on
// purpose: legitimate relay/remux targets live on the private cluster network
// (lampac). Domain names aren't resolved here (no per-request DNS cost).
// relayAllowLoopback lets tests point the relay at httptest servers; never set
// in production code.
var relayAllowLoopback = false

const relayBrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// blockedIP is the address policy shared by validateUpstream (URL literals)
// and guardDial (what is actually dialed).
func blockedIP(ip net.IP) bool {
	if relayAllowLoopback && ip.IsLoopback() {
		return false
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsPrivate()
}

// guardDial is the net.Dialer Control hook of relayClient: refuse connections
// to private, loopback, link-local and unspecified addresses after DNS
// resolution. This is what actually stops SSRF — a name can resolve anywhere.
func guardDial(_ string, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || blockedIP(ip) {
		return errors.New("relay: blocked address")
	}
	return nil
}

// resolvesToBlocked reports whether any address the host resolves to is
// blocked. For the remux path: ffmpeg dials the URL itself, outside
// relayClient, so the check has to happen before the job starts.
func resolvesToBlocked(ctx context.Context, host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return blockedIP(ip)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return true // cannot verify → do not start ffmpeg on it
	}
	for _, a := range ips {
		if blockedIP(a.IP) {
			return true
		}
	}
	return false
}

func validateUpstream(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("relay: bad scheme")
	}
	host := u.Hostname()
	// Literal IPs are refused here; hostnames are checked when dialed
	// (guardDial), because only the resolved address tells the truth.
	if ip := net.ParseIP(host); ip != nil && blockedIP(ip) {
		return errors.New("relay: blocked host")
	}
	return nil
}

// sandboxMedia marks a proxied body as data, never a document: an upstream
// (or a torrent) can answer text/html, and without this the page would run on
// our origin with the session token in localStorage. <video>, hls.js and
// <track> are unaffected by a sandbox CSP.
func sandboxMedia(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "sandbox")
}

// manifestHeaderTimeout bounds fetching+parsing a (small) HLS manifest
// before we can start rewriting it; segment/mp4 proxying has no such cap
// beyond ResponseHeaderTimeout above.
const manifestReadLimit = 4 << 20 // 4 MiB, generous for any real playlist

// relayHandler implements GET /relay?u=<base64url-upstream-url>, per
// docs/api.md: a transparent proxy for balancer-provided
// stream URLs so the client only ever talks to our own origin (CORS/mixed
// content on TV webviews, per docs/streaming.md). Auth (?t=)
// is not enforced yet — Phase 3 wires the device-token check shared by all
// media endpoints.
func relayHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := sources.DecodeRelayParam(r.URL.Query().Get("u"))
		if err != nil {
			writeBadRequest(w, "невірний параметр u")
			return
		}
		upstream, err := url.Parse(raw)
		if err != nil {
			writeBadRequest(w, "невірний upstream URL")
			return
		}
		if err := validateUpstream(upstream); err != nil {
			writeBadRequest(w, "невірний upstream URL")
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream.String(), nil)
		if err != nil {
			writeBadRequest(w, "не вдалося побудувати запит")
			return
		}
		if rng := r.Header.Get("Range"); rng != "" {
			req.Header.Set("Range", rng)
		}
		// Pass the TV's own UA through (some CDNs key on it), but a non-browser
		// client (curl, a bare media stack) gets a Chrome UA: VK's CDN answers
		// 400 to anything that does not look like a browser.
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "Mozilla/") {
			ua = relayBrowserUA
		}
		req.Header.Set("User-Agent", ua)

		resp, err := relayClient.Do(req)
		if err != nil {
			metrics.RelayUpstreamErrors.Inc()
			metrics.RelayRequests.WithLabelValues("other").Inc()
			logger.Warn("relay: upstream request failed", "url", raw, "error", err)
			writeError(w, http.StatusBadGateway, "upstream_unavailable", "джерело потоку недоступне")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			metrics.RelayUpstreamErrors.Inc()
		}

		// Resolve children against the FINAL URL: an upstream that 307-redirects
		// its playlist to a CDN (VeoVeo does) writes root-relative segment paths
		// meant for the CDN host, not the API host we were given.
		final := upstream
		if resp.Request != nil && resp.Request.URL != nil {
			final = resp.Request.URL
		}
		if isSubtitle(final.Path, resp.Header.Get("Content-Type")) {
			metrics.RelayRequests.WithLabelValues("subtitle").Inc()
			relaySubtitle(w, resp, logger)
			return
		}
		if isManifest(final.Path, resp.Header.Get("Content-Type")) {
			metrics.RelayRequests.WithLabelValues("manifest").Inc()
			// Propagate the caller's media token into every rewritten child
			// URL: after the hard gate /relay needs ?t=, and hls.js fetches the
			// rewritten segment/variant URLs directly (mediaUrl only stamps the
			// master). Token is read from THIS /relay request (already authed by
			// requireAuthMedia) and only ever appended to our own /relay wrapper
			// — never forwarded to the foreign upstream (a fresh request without
			// it is built above).
			relayManifest(w, resp, final, tokenFromRequest(r, true), logger)
			return
		}
		metrics.RelayRequests.WithLabelValues(relayKind(final.Path)).Inc()
		relayPassthrough(w, resp)
	}
}

// relayKind: HLS media segments vs everything else (whole mp4, keys, …).
func relayKind(path string) string {
	switch strings.ToLower(pathpkg.Ext(path)) {
	case ".ts", ".m4s", ".aac", ".m4a", ".mp4a":
		return "segment"
	}
	return "other"
}

func isManifest(path, contentType string) bool {
	if strings.HasSuffix(strings.ToLower(path), ".m3u8") {
		return true
	}
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "mpegurl") || strings.Contains(ct, "x-mpegurl")
}

// relayPassthrough proxies non-manifest bodies (segments, mp4) byte for
// byte, preserving status code and the headers a <video> element needs for
// seek (Range/Content-Range/Accept-Ranges) per docs/api.md
// section 5.
func relayPassthrough(w http.ResponseWriter, resp *http.Response) {
	copyHeader(w.Header(), resp.Header, "Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control", "ETag", "Last-Modified")
	w.Header().Set("Accept-Ranges", "bytes")
	sandboxMedia(w)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func copyHeader(dst, src http.Header, keys ...string) {
	for _, k := range keys {
		if v := src.Get(k); v != "" {
			dst.Set(k, v)
		}
	}
}

// relayManifest rewrites every URI line/attribute in an HLS manifest
// (master or media playlist) to point back through /relay, so hls.js never
// makes a cross-origin request to the real balancer/CDN, per
// docs/streaming.md ("Как читаем исходный m3u8 и строим
// свой" — here it's 1:1 passthrough with URL rewriting, not the
// demux->mux remux pipeline, which is a separate Phase 2b feature).
func relayManifest(w http.ResponseWriter, resp *http.Response, base *url.URL, token string, logger *slog.Logger) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, manifestReadLimit))
	if err != nil {
		metrics.RelayUpstreamErrors.Inc()
		logger.Warn("relay: failed reading manifest", "url", base.String(), "error", err)
		writeError(w, http.StatusBadGateway, "upstream_unavailable", "не вдалося прочитати плейлист")
		return
	}

	rewritten := rewriteManifest(body, base, token)

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rewritten)
}

// rewriteManifest resolves every relative/absolute URI reference in an HLS
// manifest against base and replaces it with an /relay-wrapped URL:
//   - plain lines that aren't "#" comments (segment URIs, variant stream
//     URIs)
//   - the URI="..." attribute inside tags like #EXT-X-MEDIA, #EXT-X-KEY,
//     #EXT-X-MAP, #EXT-X-I-FRAME-STREAM-INF
func rewriteManifest(body []byte, base *url.URL, token string) []byte {
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "#"):
			lines[i] = rewriteTagAttributes(trimmed, base, token)
		default:
			lines[i] = relayResolve(trimmed, base, token)
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func rewriteTagAttributes(line string, base *url.URL, token string) string {
	start, end, ok := m3u8URIAttr(line)
	if !ok {
		return line
	}
	return line[:start] + relayResolve(line[start:end], base, token) + line[end:]
}

func relayResolve(ref string, base *url.URL, token string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ref
	}
	resolved, err := base.Parse(ref)
	if err != nil {
		return ref
	}
	// wrapped is always "/relay?u=..." (our own origin), so the token is safe
	// here and never reaches the foreign upstream.
	return withMediaToken(sources.EncodeRelayURL(resolved.String()), token)
}
