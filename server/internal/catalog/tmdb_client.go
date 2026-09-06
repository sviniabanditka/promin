package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

const (
	// listTTL is the cache lifetime for list-shaped TMDB responses
	// (trending/now_playing/popular/discover/search/genres главная), per
	// docs/backend.md
	listTTL = 6 * time.Hour
	// detailTTL is the cache lifetime for a single title card
	// (append_to_response=...). Long on purpose: a title's cast/overview/
	// runtime barely change, and stale-while-revalidate (getCached) serves the
	// local copy INSTANTLY and refreshes behind the response — so a long TTL
	// costs nothing in freshness while cutting upstream calls hard. The goal is
	// a local library that keeps working when TMDB does not.
	detailTTL = 7 * 24 * time.Hour
	// genresTTL: genre lists change essentially never.
	genresTTL = 30 * 24 * time.Hour

	// requestTimeout bounds ONE HTTP attempt to ONE TMDB host. Kept short:
	// during a TMDB outage a long timeout × many home shelves stacks into a
	// minute-long hang before stale cache is served. The circuit breaker
	// (below) makes sure we don't even pay this once TMDB is known-down.
	requestTimeout = 6 * time.Second

	// breakerCooldown: after an upstream failure, skip TMDB entirely for this
	// long and serve stale cache immediately. One probe request per cooldown
	// re-tests the primary; a success clears the breaker. Turns a multi-shelf
	// home from "N × requestTimeout hang" into an instant stale render.
	breakerCooldown = 15 * time.Second
)

// ErrUpstreamUnavailable is returned when TMDB failed and no cached
// (even stale) copy was available to fall back to.
var ErrUpstreamUnavailable = errors.New("catalog: tmdb upstream unavailable")

// errTMDBNotFound is a definitive "no such id" from TMDB (HTTP 404) — not
// a transient failure, so it must not fall back to a stale cache entry
// nor get wrapped as ErrUpstreamUnavailable.
var errTMDBNotFound = errors.New("catalog: tmdb 404")

// Client is a caching TMDB API client. Raw TMDB JSON never leaves this
// package — internal/catalog normalizes everything into the Title DTO
// before it reaches httpapi (docs/backend.md).
type Client struct {
	httpClient *http.Client
	baseURLs   []string // primary first, then fallback mirrors
	apiKey     string
	cache      *store.TMDBCacheRepo
	logger     *slog.Logger

	// refreshing holds the cache keys whose background revalidation is in
	// flight, so N concurrent viewers of the same shelf trigger ONE refresh
	// instead of a stampede.
	refreshing sync.Map
	// inflight single-flights COLD misses (no cached copy at all): three TVs
	// opening home on an empty cache used to fire 3× every shelf at TMDB.
	// key → chan struct{} closed when the fetch settles.
	inflight sync.Map

	// failedUntil is the unix-seconds deadline of an open circuit: while
	// now < failedUntil, getCached skips the network and serves stale cache
	// (or fails fast) instead of hammering a known-dead TMDB. atomic so the
	// many concurrent home/list goroutines share one breaker without a lock.
	failedUntil atomic.Int64
}

// NewClient builds a TMDB client backed by the given cache repository.
// baseURLs is the primary TMDB host followed by any fallback mirrors, tried
// in order per request until one answers.
func NewClient(baseURLs []string, apiKey string, cache *store.TMDBCacheRepo, logger *slog.Logger) *Client {
	if len(baseURLs) == 0 {
		baseURLs = []string{"https://api.themoviedb.org/3"}
	}
	return &Client{
		httpClient: &http.Client{Timeout: requestTimeout},
		baseURLs:   baseURLs,
		apiKey:     apiKey,
		cache:      cache,
		logger:     logger,
	}
}

// getCached fetches path+query from TMDB, transparently caching the raw
// response body under key for ttl. On upstream failure, a stale cache
// entry (if any) is served instead (stale-while-error, per
// docs/backend.md); if there is no cached copy at all,
// ErrUpstreamUnavailable is returned.
func (c *Client) getCached(ctx context.Context, key string, ttl time.Duration, path string, query url.Values) ([]byte, error) {
	now := time.Now().Unix()

	cached, cacheErr := c.cache.Get(key)
	if cacheErr == nil && cached.ExpiresAt > now {
		c.logger.Debug("tmdb cache hit", "key", key)
		return []byte(cached.JSON), nil
	}

	// STALE-WHILE-REVALIDATE: we have a copy, it is merely past its TTL. Serve
	// it immediately and refresh behind the response. This is what makes the
	// local cache behave like our own database — every title/list already seen
	// renders instantly and keeps rendering while TMDB is slow or down. Only a
	// COLD miss (no copy at all) blocks on the network below.
	if cacheErr == nil {
		c.refreshLater(key, ttl, path, query)
		c.logger.Debug("tmdb stale hit, refreshing in background", "key", key)
		return []byte(cached.JSON), nil
	}

	// Circuit open: TMDB failed recently, don't pay another timeout. Serve
	// stale immediately if we have any copy, else fail fast. One request per
	// cooldown still falls through to doGet below to re-probe.
	if c.failedUntil.Load() > now {
		if cacheErr == nil {
			c.logger.Debug("tmdb circuit open, serving stale cache", "key", key)
			return []byte(cached.JSON), nil
		}
		return nil, fmt.Errorf("%w: circuit open", ErrUpstreamUnavailable)
	}

	// Cold miss: one goroutine fetches, the others wait and re-read the cache.
	done := make(chan struct{})
	if existing, busy := c.inflight.LoadOrStore(key, done); busy {
		select {
		case <-existing.(chan struct{}):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if again, err := c.cache.Get(key); err == nil {
			return []byte(again.JSON), nil
		}
		// The leader failed (or got a 404) — fall through to our own attempt.
	} else {
		defer func() {
			c.inflight.Delete(key)
			close(done)
		}()
	}

	body, err := c.doGet(ctx, path, query)
	if err != nil {
		if errors.Is(err, errTMDBNotFound) {
			return nil, errTMDBNotFound
		}
		// Trip the breaker so sibling shelves don't each wait a full timeout.
		c.failedUntil.Store(time.Now().Unix() + int64(breakerCooldown.Seconds()))
		if cacheErr == nil {
			c.logger.Warn("tmdb upstream error, serving stale cache", "key", key, "error", err)
			return []byte(cached.JSON), nil
		}
		c.logger.Error("tmdb upstream error, no cache available", "key", key, "error", err)
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	// Success — clear any open circuit.
	c.failedUntil.Store(0)

	if cacheErr == nil {
		c.logger.Debug("tmdb cache miss (stale, refreshed)", "key", key)
	} else {
		c.logger.Debug("tmdb cache miss", "key", key)
	}

	if err := c.cache.Set(key, string(body), now+int64(ttl.Seconds())); err != nil {
		c.logger.Warn("tmdb cache write failed", "key", key, "error", err)
	}

	return body, nil
}

// refreshLater revalidates a stale entry off the request path. Single-flighted
// per key; skipped while the breaker is open (a known-down TMDB would just burn
// goroutines). Uses its own context: the request's is cancelled the moment the
// response is written.
func (c *Client) refreshLater(key string, ttl time.Duration, path string, query url.Values) {
	if c.failedUntil.Load() > time.Now().Unix() {
		return
	}
	if _, busy := c.refreshing.LoadOrStore(key, struct{}{}); busy {
		return
	}
	// Copy the query: the caller may reuse/mutate its url.Values after we return.
	q := url.Values{}
	for k, v := range query {
		q[k] = append([]string(nil), v...)
	}
	go func() {
		defer c.refreshing.Delete(key)
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout+2*time.Second)
		defer cancel()

		body, err := c.doGet(ctx, path, q)
		if err != nil {
			if !errors.Is(err, errTMDBNotFound) {
				c.failedUntil.Store(time.Now().Unix() + int64(breakerCooldown.Seconds()))
			}
			c.logger.Debug("tmdb background refresh failed", "key", key, "error", err)
			return
		}
		c.failedUntil.Store(0)
		if err := c.cache.Set(key, string(body), time.Now().Unix()+int64(ttl.Seconds())); err != nil {
			c.logger.Warn("tmdb cache write failed", "key", key, "error", err)
			return
		}
		c.logger.Debug("tmdb refreshed in background", "key", key)
	}()
}

// doGet tries each base host in order until one answers, returning the first
// success. A definitive 404 stops early (the id doesn't exist — no mirror will
// have it). Otherwise the last transient error is returned so getCached can
// trip the breaker / serve stale.
func (c *Client) doGet(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("api_key", c.apiKey)
	qs := "?" + query.Encode()

	var lastErr error
	for _, base := range c.baseURLs {
		body, err := c.doGetOne(ctx, base+path+qs)
		if err == nil {
			return body, nil
		}
		if errors.Is(err, errTMDBNotFound) {
			return nil, err
		}
		lastErr = err
		c.logger.Debug("tmdb host failed, trying next", "base", base, "error", err)
	}
	return nil, lastErr
}

func (c *Client) doGetOne(ctx context.Context, reqURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, errTMDBNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb status %d: %s", resp.StatusCode, truncate(body, 200))
	}
	return body, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// --- typed fetchers -------------------------------------------------------

func (c *Client) fetchList(ctx context.Context, key string, path, lang string, extra url.Values, page int) (tmdbListResponse, error) {
	q := url.Values{}
	for k, v := range extra {
		q[k] = v
	}
	q.Set("language", tmdbLanguage(lang))
	if page > 0 {
		q.Set("page", fmt.Sprint(page))
	}

	body, err := c.getCached(ctx, key, listTTL, path, q)
	if err != nil {
		return tmdbListResponse{}, err
	}

	var out tmdbListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return tmdbListResponse{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return out, nil
}

func (c *Client) fetchGenreMap(ctx context.Context, mediaType, lang string) (map[int]string, error) {
	list, err := c.fetchGenreList(ctx, mediaType, lang)
	if err != nil {
		return nil, err
	}
	m := make(map[int]string, len(list))
	for _, g := range list {
		m[g.ID] = g.Name
	}
	return m, nil
}

func (c *Client) fetchGenreList(ctx context.Context, mediaType, lang string) ([]tmdbGenre, error) {
	key := fmt.Sprintf("genres:%s:%s", mediaType, normalizeLang(lang))
	q := url.Values{"language": {tmdbLanguage(lang)}}

	body, err := c.getCached(ctx, key, genresTTL, "/genre/"+mediaType+"/list", q)
	if err != nil {
		return nil, err
	}

	var out tmdbGenreListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode genres: %w", err)
	}
	return out.Genres, nil
}

func (c *Client) fetchDetail(ctx context.Context, mediaType string, id int, lang string) (tmdbDetail, error) {
	key := fmt.Sprintf("title:%s:%d:%s", mediaType, id, normalizeLang(lang))

	appends := "external_ids,keywords,credits,similar,recommendations,videos"
	if mediaType == "movie" {
		appends += ",release_dates"
	} else {
		appends += ",content_ratings"
	}

	q := url.Values{
		"language":           {tmdbLanguage(lang)},
		"append_to_response": {appends},
	}

	body, err := c.getCached(ctx, key, detailTTL, fmt.Sprintf("/%s/%d", mediaType, id), q)
	if err != nil {
		return tmdbDetail{}, err
	}

	var out tmdbDetail
	if err := json.Unmarshal(body, &out); err != nil {
		return tmdbDetail{}, fmt.Errorf("decode detail: %w", err)
	}
	return out, nil
}

// cachedDetail reads a title's already-cached detail JSON straight from
// tmdb_cache and returns it REGARDLESS of ExpiresAt — a deliberate stale-ok
// read for the recommender's request path. tmdb_cache never evicts (unlike
// torrent_cache), so every title ever opened-to-play still has its
// recommendations/similar/credits/genres intact, and recommendations don't
// rot day-to-day. Returns ok=false on cache miss or unparsable body: the
// caller SKIPS that seed instead of paying a network round-trip on the home
// path (which would also contend a tmdb_cache write with live playback
// Upserts on the single SQLite connection).
func (c *Client) cachedDetail(mediaType string, id int, lang string) (tmdbDetail, bool) {
	key := fmt.Sprintf("title:%s:%d:%s", mediaType, id, normalizeLang(lang))
	cached, err := c.cache.Get(key)
	if err != nil {
		return tmdbDetail{}, false
	}
	var out tmdbDetail
	if err := json.Unmarshal([]byte(cached.JSON), &out); err != nil || out.ID == 0 {
		return tmdbDetail{}, false
	}
	return out, true
}

// fetchVideos gets a title's videos in a specific language. Used as an
// en fallback: TMDB has no localized videos for most titles in smaller
// languages (e.g. uk-UA), so a trailer that exists only in English would
// otherwise be invisible.

// fetchVideosAllLangs pulls a title's videos across uk/ru/en (plus untagged) in
// one call via include_video_language, so the trailers list can offer a
// language choice. Each result carries its own iso_639_1.
func (c *Client) fetchVideosAllLangs(ctx context.Context, mediaType string, id int) ([]tmdbVideo, error) {
	key := fmt.Sprintf("videos:%s:%d:multi", mediaType, id)
	q := url.Values{"include_video_language": {"uk,ru,en,null"}}

	body, err := c.getCached(ctx, key, detailTTL, fmt.Sprintf("/%s/%d/videos", mediaType, id), q)
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []tmdbVideo `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode videos: %w", err)
	}
	return out.Results, nil
}

// genericEpisodeName matches TMDB's auto-generated placeholder episode titles
// ("Серія 5" / "Серия 5" / "Эпизод 5" / "Епізод 5" / "Episode 5") that it
// returns when a language has no real per-episode translation. Empty also
// counts. Used to decide whether to backfill from the English season.
var genericEpisodeName = regexp.MustCompile(`(?i)^(епізод|серія|серия|эпизод|episode)\s*\d+$`)

func isGenericEpisodeName(name string, num int) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	return genericEpisodeName.MatchString(name)
}

// fetchSeason fetches a TV season in the requested language and, mirroring
// Lampa's [preferred, en, null] language fallback, backfills any episode whose
// localized title is a generic placeholder ("Серія 5") with the English title
// — but only when English actually has a real one. The English season is
// fetched (and cached) only if at least one placeholder is present, so the
// common fully-translated case costs no extra request.
func (c *Client) fetchSeason(ctx context.Context, tvID, season int, lang string) (tmdbSeasonDetail, error) {
	out, err := c.fetchSeasonLang(ctx, tvID, season, lang)
	if err != nil {
		return tmdbSeasonDetail{}, err
	}
	if normalizeLang(lang) == "en" {
		return out, nil
	}
	needEn := false
	for _, e := range out.Episodes {
		if isGenericEpisodeName(e.Name, e.EpisodeNumber) {
			needEn = true
			break
		}
	}
	if !needEn {
		return out, nil
	}
	en, err := c.fetchSeasonLang(ctx, tvID, season, "en")
	if err != nil {
		return out, nil // fallback fetch failed → keep the localized placeholders
	}
	enByNum := make(map[int]string, len(en.Episodes))
	for _, e := range en.Episodes {
		enByNum[e.EpisodeNumber] = e.Name
	}
	for i, e := range out.Episodes {
		if !isGenericEpisodeName(e.Name, e.EpisodeNumber) {
			continue
		}
		if enName, ok := enByNum[e.EpisodeNumber]; ok && !isGenericEpisodeName(enName, e.EpisodeNumber) {
			out.Episodes[i].Name = enName
		}
	}
	return out, nil
}

func (c *Client) fetchSeasonLang(ctx context.Context, tvID, season int, lang string) (tmdbSeasonDetail, error) {
	key := fmt.Sprintf("title:tv:%d:season:%d:%s", tvID, season, normalizeLang(lang))
	q := url.Values{"language": {tmdbLanguage(lang)}}

	body, err := c.getCached(ctx, key, detailTTL, fmt.Sprintf("/tv/%d/season/%d", tvID, season), q)
	if err != nil {
		return tmdbSeasonDetail{}, err
	}

	var out tmdbSeasonDetail
	if err := json.Unmarshal(body, &out); err != nil {
		return tmdbSeasonDetail{}, fmt.Errorf("decode season: %w", err)
	}
	return out, nil
}
