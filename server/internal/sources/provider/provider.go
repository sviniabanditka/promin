// Package provider is the native-source layer: Promin's own scrapers, used
// alongside (and eventually instead of) the lampac sidecar — see the
// lampac-independence plan. A provider is the ONLY code that knows a source's
// HTML/quirks; everything above it works on the normalized types here.
//
// Design rules (ported from the smart-tv prototype's provider interface):
//   - A provider NEVER touches http.DefaultClient. All egress goes through
//     Ctx.Fetch, so the transport (direct / proxy sidecar / anything later) is
//     swappable without editing a single provider.
//   - Parsing is deterministic given fixed input → unit-testable from saved
//     fixtures, which is what catches "the site changed its HTML" on CI.
//   - Errors are typed: ErrSourceUnavailable (network/blocked) vs ErrParse
//     (layout changed) vs ErrResolveFailed, so the API layer can map them to
//     distinct client-visible states instead of one opaque failure.
package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrSourceUnavailable: the source could not be reached (network, block,
	// non-2xx). Retryable; the caller may fall back to another provider.
	ErrSourceUnavailable = errors.New("provider: source unavailable")
	// ErrParse: the source answered but its markup/payload no longer matches
	// what we parse. NOT retryable — it means this provider needs a fix.
	ErrParse = errors.New("provider: parse failed")
	// ErrResolveFailed: reached and parsed, but no playable stream for the
	// requested selection (missing episode/voice, empty playlist).
	ErrResolveFailed = errors.New("provider: resolve failed")
)

// SearchItem is one normalized catalog hit.
type SearchItem struct {
	ID    string // provider-scoped id, e.g. "uakinogo:1234--slug.html"
	Title string
	// OriginalTitle, when the source exposes it (Filmix does): the matcher
	// compares it too, so a localized Title that differs from TMDB's does not
	// lose a title whose original name is identical.
	OriginalTitle string
	Year          int
	Type          string // "movie" | "tv"
	Poster        string
}

// Season/Episode describe a series' structure as the provider sees it (the
// SOURCE's numbering, which may differ from TMDB's).
type Episode struct {
	Number int
	Title  string
}
type Season struct {
	Number   int
	Episodes []Episode
}

// AudioTrack is one dub/voice option ("переклад") a source offers.
type AudioTrack struct {
	ID    string
	Label string
}

// TitleDetails is a source title page, normalized.
type TitleDetails struct {
	ID       string
	Title    string
	Year     int
	Type     string
	Poster   string
	Overview string
	Seasons  []Season
	Audio    []AudioTrack
}

// ResolveRequest asks for one playable stream. Season/Episode are 0 for movies.
type ResolveRequest struct {
	ID      string
	Season  int
	Episode int
	Audio   string // AudioTrack.ID, empty = provider's default
}

// Subtitle is one sidecar subtitle track on a resolved stream.
type Subtitle struct {
	Lang  string
	Label string
	URL   string
}

// ResolvedStream is a playable result. URL is the RAW upstream (source/CDN)
// address — the HTTP layer wraps it in /relay so the TV only ever talks to us.
type ResolvedStream struct {
	URL         string
	Kind        string // "hls" | "mp4"
	Subtitles   []Subtitle
	DurationSec int
	// Audio: id (AudioTrack.ID) of the dub this stream carries, when the
	// provider knows it — the requested one or its default pick.
	Audio string
	// AudioNames: human labels for the HLS master's audio renditions, in
	// rendition order (Collaps names them rus0/rus1 in the manifest and ships
	// the real dub names separately). Empty when the manifest is self-describing.
	AudioNames []string
	// Variants: explicit per-quality URLs (progressive sources hand out one file
	// per quality). When present, URL is the best of them and the HTTP layer
	// exposes every variant so the client can pick. Sorted best-first.
	Variants []Variant
}

// Variant is one quality of a resolved stream.
type Variant struct {
	Quality int // vertical resolution, e.g. 1080
	Label   string
	URL     string
}

// Fetcher is a provider's only egress. Implementations add the proxy, rate
// limit, UA and timeout policy; providers just call Get/PostForm.
type Fetcher interface {
	Get(ctx context.Context, url string, hdr http.Header) ([]byte, error)
	// PostForm submits url-encoded values (DLE's search endpoint is POST-only).
	PostForm(ctx context.Context, url string, hdr http.Header, form url.Values) ([]byte, error)
	// PostJSON submits a raw JSON body (an embed player's lazy stream-resolution
	// endpoint takes one).
	PostJSON(ctx context.Context, url string, hdr http.Header, body []byte) ([]byte, error)
}

// Ctx is what a provider gets per call: egress + its base URL + a log hook.
// Deliberately tiny — a provider that needs more probably wants a helper here
// rather than a wider surface.
type Ctx struct {
	Fetch   Fetcher
	BaseURL string
	// Referer overrides BaseURL as the Referer header when a nested player
	// endpoint validates it (the embed URL, not the catalog).
	Referer string
	Log     func(msg string, args ...any)
}

// ExternalRef identifies a title by ids the catalog knows. Lookuper is an
// OPTIONAL provider capability: sources with an id map (VeoVeo) find a title
// exactly by imdb id, without the name-search-and-rank dance. The aggregator
// tries it first when the request carries an imdb id.
type ExternalRef struct {
	TMDBID        int
	IMDbID        string
	Title         string
	OriginalTitle string
	Year          int
	Type          string
}

type Lookuper interface {
	Lookup(ctx context.Context, ref ExternalRef, pc Ctx) (SearchItem, bool, error)
}

// Provider is one source. Registered in the registry; the aggregator queries
// every enabled provider plus (for now) the lampac sidecar.
type Provider interface {
	ID() string
	Search(ctx context.Context, query string, page int, pc Ctx) ([]SearchItem, error)
	Title(ctx context.Context, sourceID string, pc Ctx) (TitleDetails, error)
	Resolve(ctx context.Context, req ResolveRequest, pc Ctx) (ResolvedStream, error)
}

// --- default egress -------------------------------------------------------

// DirectFetcher goes straight out from this host. Measured working from the
// production VPS for the current sources; if one ever starts blocking the
// datacenter IP, slot a proxying Fetcher in here and no provider changes.
type DirectFetcher struct {
	Client    *http.Client
	UserAgent string
	// MaxBytes caps a response body so a hostile/huge page can't exhaust RAM.
	MaxBytes int64
}

const (
	defaultFetchTimeout = 15 * time.Second
	defaultMaxBytes     = 8 << 20 // 8 MiB — generous for any catalog page
	defaultUA           = "Mozilla/5.0 (SMART-TV; Linux) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36"
)

func NewDirectFetcher() *DirectFetcher {
	return &DirectFetcher{
		Client:    &http.Client{Timeout: defaultFetchTimeout},
		UserAgent: defaultUA,
		MaxBytes:  defaultMaxBytes,
	}
}

// NewProxyFetcher is a DirectFetcher whose requests go through an HTTP(S)
// proxy ("http://user:pass@host:port") — a residential exit for catalogs that
// block datacenter IPs. Only catalog pages go this way; stream URLs are
// handed to /relay, which fetches directly. A browser-like UA: these sites
// serve a stub to anything that looks like a TV/bot.
func NewProxyFetcher(proxyURL string) (*DirectFetcher, error) {
	u, err := url.Parse(proxyURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("provider: bad proxy url")
	}
	tr := &http.Transport{
		Proxy:               http.ProxyURL(u),
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
	}
	return &DirectFetcher{
		Client:    &http.Client{Timeout: 25 * time.Second, Transport: tr},
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36",
		MaxBytes:  defaultMaxBytes,
	}, nil
}

func (f *DirectFetcher) Get(ctx context.Context, target string, hdr http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	return f.do(req, hdr)
}

func (f *DirectFetcher) PostForm(ctx context.Context, target string, hdr http.Header, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	if hdr.Get("Content-Type") == "" {
		if hdr == nil {
			hdr = http.Header{}
		}
		hdr.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return f.do(req, hdr)
}

func (f *DirectFetcher) PostJSON(ctx context.Context, target string, hdr http.Header, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if hdr == nil {
		hdr = http.Header{}
	}
	if hdr.Get("Content-Type") == "" {
		hdr.Set("Content-Type", "application/json")
	}
	return f.do(req, hdr)
}

func (f *DirectFetcher) do(req *http.Request, hdr http.Header) ([]byte, error) {
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, errors.Join(ErrSourceUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, errors.Join(ErrSourceUnavailable, errors.New("status "+resp.Status))
	}
	max := f.MaxBytes
	if max <= 0 {
		max = defaultMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max))
	if err != nil {
		return nil, errors.Join(ErrSourceUnavailable, err)
	}
	return body, nil
}
