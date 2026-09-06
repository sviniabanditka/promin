package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// omdbTTL is the cache lifetime for OMDb responses. IMDB ratings move
// slowly, so 7 days is effectively "forever" for our purposes while still
// letting a rating eventually refresh (task brief: "кэшируй НАВСЕГДА...
// TTL 7 дней ок").
const omdbTTL = 7 * 24 * time.Hour

const omdbRequestTimeout = 8 * time.Second

// OMDbClient is a caching client for the OMDb (omdbapi.com) API, used to
// enrich title cards with an imdb_rating TMDB itself doesn't provide.
//
// When apiKey is empty the client is inert: FetchRating always returns
// (0, false) without making a request — the feature is simply off, not an
// error (task brief item 1).
type OMDbClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	cache      *store.TMDBCacheRepo
	logger     *slog.Logger
}

// NewOMDbClient builds an OMDb client backed by the shared tmdb_cache
// table (reused rather than a dedicated table, per task brief item 2 —
// cache keys are prefixed "omdb:" to avoid colliding with TMDB entries).
func NewOMDbClient(apiKey string, cache *store.TMDBCacheRepo, logger *slog.Logger) *OMDbClient {
	return &OMDbClient{
		httpClient: &http.Client{Timeout: omdbRequestTimeout},
		baseURL:    "https://www.omdbapi.com/",
		apiKey:     apiKey,
		cache:      cache,
		logger:     logger,
	}
}

// omdbResponse is the subset of the OMDb "by id" payload we care about.
type omdbResponse struct {
	ImdbRating string `json:"imdbRating"`
	Response   string `json:"Response"`
	Error      string `json:"Error"`
}

// FetchRating returns the IMDB rating for imdbID (e.g. "tt0944947"),
// serving from tmdb_cache when fresh and refreshing on miss. ok is false
// whenever no numeric rating is available for any reason — disabled
// feature (no API key), empty id, upstream error, or OMDb returning "N/A"
// / an error payload. FetchRating never returns an error: a broken OMDb
// call must not break title-card retrieval (task brief item 5).
func (c *OMDbClient) FetchRating(ctx context.Context, imdbID string) (float64, bool) {
	if c == nil || c.apiKey == "" || imdbID == "" {
		return 0, false
	}

	key := "omdb:" + imdbID
	now := time.Now().Unix()

	cached, cacheErr := c.cache.Get(key)
	if cacheErr == nil && cached.ExpiresAt > now {
		return parseOMDbBody(cached.JSON)
	}

	body, err := c.doGet(ctx, imdbID)
	if err != nil {
		if cacheErr == nil {
			c.logger.Warn("omdb upstream error, serving stale cache", "imdb_id", imdbID, "error", err)
			return parseOMDbBody(cached.JSON)
		}
		c.logger.Warn("omdb upstream error, no cache available", "imdb_id", imdbID, "error", err)
		return 0, false
	}

	if err := c.cache.Set(key, string(body), now+int64(omdbTTL.Seconds())); err != nil {
		c.logger.Warn("omdb cache write failed", "imdb_id", imdbID, "error", err)
	}

	return parseOMDbBody(string(body))
}

func (c *OMDbClient) doGet(ctx context.Context, imdbID string) ([]byte, error) {
	q := url.Values{"i": {imdbID}, "apikey": {c.apiKey}}
	reqURL := c.baseURL + "?" + q.Encode()

	ctx, cancel := context.WithTimeout(ctx, omdbRequestTimeout)
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("omdb: status %d: %s", resp.StatusCode, truncate(body, 200))
	}
	return body, nil
}

func parseOMDbBody(body string) (float64, bool) {
	var r omdbResponse
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return 0, false
	}
	if r.Response == "False" {
		return 0, false
	}
	return parseImdbRatingString(r.ImdbRating)
}

// parseImdbRatingString parses OMDb's imdbRating field ("8.5", "N/A", or
// empty) into a float. "N/A"/empty/unparseable all report ok=false rather
// than an error, per task brief item 3.
func parseImdbRatingString(s string) (float64, bool) {
	if s == "" || s == "N/A" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
