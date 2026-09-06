// Package catalog is the TMDB-backed catalog: a caching client plus
// normalization into the canonical Title DTO (docs/api.md section
// 6). httpapi only ever sees Service's exported methods and DTOs, never
// raw TMDB shapes.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// Service is the high-level catalog API consumed by internal/httpapi.
type Service struct {
	client *Client
	omdb   *OMDbClient

	cardMemo  sync.Map // CardCached memo, see cardMemoEntry
	cardMemoN atomic.Int64
}

// NewService builds a Service around a caching TMDB Client. omdb may be
// nil (or built with an empty API key) — imdb_rating is then simply never
// populated, which is not an error (task brief item 1).
func NewService(client *Client, omdb *OMDbClient) *Service {
	return &Service{client: client, omdb: omdb}
}

// ListFilters are the accepted filters for Home/List/Search, per
// docs/api.md (`GET /api/v1/catalog/list`).
type ListFilters struct {
	Type       string // "movie" | "tv"
	Genre      int    // TMDB genre id, 0 = unset
	YearFrom   int
	YearTo     int
	RatingFrom float64
	Sort       string // "popularity" | "rating" | "year"
	Page       int
	Lang       string
}

// --- Home -------------------------------------------------------------

// Home builds the home-page shelves listed in the task brief. Row set is
// a Promin extension beyond docs/api.md's illustrative example (which shows
// "continue_watching"/"trending"/"new_releases"/"genre:28" as a sample,
// not an exhaustive list). continue_watching itself isn't built here — it
// needs the caller's timecodes, which this package doesn't own — see
// ContinueWatchingRow, which internal/httpapi calls and prepends to
// these rows when the request is authenticated (Phase 3 добивание).
func (s *Service) Home(ctx context.Context, lang string) (HomeResponse, error) {
	// Shelves are independent, so they are built concurrently: on a cold cache
	// the old serial version paid ~11 TMDB round-trips one after another (3+ s).
	// One failed shelf is dropped, not fatal — a hiccup on one list must not
	// blank the whole home; only "nothing at all" is an error.
	builders := []func() (Row, error){
		func() (Row, error) { return s.trendingRow(ctx, lang) },
		func() (Row, error) { return s.newReleasesRow(ctx, lang) },
		func() (Row, error) {
			return s.simpleListRow(ctx, "popular_movies", homeTitle("popular_movies", lang), "/movie/popular", "movie", nil, lang)
		},
		func() (Row, error) {
			return s.simpleListRow(ctx, "popular_tv", homeTitle("popular_tv", lang), "/tv/popular", "tv", nil, lang)
		},
		func() (Row, error) {
			return s.simpleListRow(ctx, "cartoons", homeTitle("cartoons", lang), "/discover/movie", "movie", url.Values{"with_genres": {"16"}}, lang)
		},
		func() (Row, error) { return s.animeRow(ctx, lang) },
	}
	results := make([]Row, len(builders))
	errs := make([]error, len(builders))
	var wg sync.WaitGroup
	for i, b := range builders {
		wg.Add(1)
		go func(i int, b func() (Row, error)) {
			defer wg.Done()
			results[i], errs[i] = b()
		}(i, b)
	}
	wg.Wait()

	rows := make([]Row, 0, len(builders))
	var firstErr error
	for i := range builders {
		if errs[i] != nil {
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		rows = append(rows, results[i])
	}
	if len(rows) == 0 && firstErr != nil {
		return HomeResponse{}, firstErr
	}
	return HomeResponse{Rows: rows}, nil
}

// Card fetches a single normalized title card by (tmdbID, mediaType), for
// callers that need to enrich a foreign record (bookmarks, timecodes)
// with a display card rather than list a whole shelf. It's Title with
// season=0 — same tmdb_cache-backed lookup, no season/episode fetch — so
// it's cheap even for shelves with many rows (Phase 3 добивание, see
// ContinueWatchingRow).
func (s *Service) Card(ctx context.Context, mediaType string, tmdbID int, lang string) (Title, error) {
	return s.title(ctx, mediaType, tmdbID, 0, lang, false)
}

// CardCached is Card's cache-only twin: it builds a display card from the
// title's already-cached TMDB detail (stale-ok) and returns ok=false on a
// cache miss instead of fetching. Used on the authenticated home path
// (continue-watching + recommendation shelves) where a per-item network
// round-trip would serialize behind live playback timecode Upserts on the
// single SQLite connection. No season/episode fetch, no rating/trailers.
func (s *Service) CardCached(mediaType string, tmdbID int, lang string) (Title, bool) {
	if mediaType != "movie" && mediaType != "tv" {
		return Title{}, false
	}
	// A card is ~1 KB; the detail blob it is cut from is 55–121 KB. The
	// continue-watching shelf asked for up to 20 of these on EVERY home
	// request — ~2.4 MB of JSON decode to read titles and posters. Memoize the
	// cut card in memory for a while.
	key := mediaType + ":" + strconv.Itoa(tmdbID) + ":" + normalizeLang(lang)
	now := time.Now().Unix()
	if v, ok := s.cardMemo.Load(key); ok {
		m := v.(cardMemoEntry)
		if now-m.at < cardMemoTTLSec {
			return m.title, true
		}
	}
	detail, ok := s.client.cachedDetail(mediaType, tmdbID, lang)
	if !ok {
		return Title{}, false
	}
	t := normalizeDetail(detail, mediaType, 0, nil, false)
	// Same shape as every other shelf card: w342 poster (the detail keeps w500
	// for the title screen) and no overview/genres — the continue shelf was the
	// one row still shipping 20 full descriptions nobody renders.
	t.Poster = strings.Replace(t.Poster, "/img/w500/", "/img/w342/", 1)
	t.Overview = ""
	t.Genres = nil
	// ponytail: crude bound — wipe everything past N entries instead of LRU.
	if s.cardMemoN.Add(1) > cardMemoMax {
		s.cardMemo.Range(func(k, _ any) bool { s.cardMemo.Delete(k); return true })
		s.cardMemoN.Store(0)
	}
	s.cardMemo.Store(key, cardMemoEntry{title: t, at: now})
	return t, true
}

type cardMemoEntry struct {
	title Title
	at    int64
}

const (
	cardMemoTTLSec = 10 * 60
	cardMemoMax    = 2000
)

// ContinueWatchingRow builds the "continue_watching" home shelf
// (docs/api.md: always first, cards carrying a filled
// `timecode`) from the caller's unfinished timecodes, as returned by
// internal/sync.Service.ListContinueWatching. internal/httpapi calls this
// and prepends the result to Home's rows when the request is
// authenticated; catalog itself has no notion of users/auth.
//
// Timecodes whose title can no longer be resolved against TMDB (deleted,
// upstream hiccup) are skipped rather than failing the whole row. An
// empty result (no timecodes, or none resolvable) returns nil — no
// "continue_watching" row at all, per the task brief.
func (s *Service) ContinueWatchingRow(ctx context.Context, lang string, timecodes []store.Timecode) *Row {
	if len(timecodes) == 0 {
		return nil
	}

	items := make([]Title, 0, len(timecodes))
	seen := map[string]bool{}
	for _, t := range timecodes {
		mediaType := t.MediaType
		if mediaType == "" {
			mediaType = "movie"
		}
		// The store already collapses to the latest row per title; a same-second
		// tie can still yield two, keep the first (newest).
		key := mediaType + ":" + strconv.FormatInt(t.TMDBID, 10)
		if seen[key] {
			continue
		}
		seen[key] = true
		// Cache-only: a resume item was opened-to-play, so its detail is
		// already in tmdb_cache. Skip the rare miss rather than firing up to
		// ~20 serial fetchDetail round-trips on the home path (the old N+1 —
		// more upstream cost than every recommendation shelf combined).
		card, ok := s.CardCached(mediaType, int(t.TMDBID), lang)
		if !ok {
			continue
		}
		card.Timecode = &Timecode{PositionSec: t.PositionSec, DurationSec: t.DurationSec, Season: t.Season, Episode: t.Episode}
		items = append(items, card)
	}
	if len(items) == 0 {
		return nil
	}
	return &Row{ID: "continue_watching", Title: homeTitle("continue_watching", lang), Items: items}
}

func homeTitle(id, lang string) string {
	uk := map[string]string{
		"continue_watching": "Продовжити перегляд",
		"trending":          "У тренді",
		"new_releases":      "Новинки",
		"popular_movies":    "Популярні фільми",
		"popular_tv":        "Популярні серіали",
		"cartoons":          "Мультфільми",
		"anime":             "Аніме",
	}
	ru := map[string]string{
		"continue_watching": "Продолжить просмотр",
		"trending":          "В тренде",
		"new_releases":      "Новинки",
		"popular_movies":    "Популярные фильмы",
		"popular_tv":        "Популярные сериалы",
		"cartoons":          "Мультфильмы",
		"anime":             "Аниме",
	}
	en := map[string]string{
		"continue_watching": "Continue Watching",
		"trending":          "Trending",
		"new_releases":      "New Releases",
		"popular_movies":    "Popular Movies",
		"popular_tv":        "Popular TV Shows",
		"cartoons":          "Cartoons",
		"anime":             "Anime",
	}
	switch normalizeLang(lang) {
	case "ru":
		return ru[id]
	case "en":
		return en[id]
	default:
		return uk[id]
	}
}

func (s *Service) trendingRow(ctx context.Context, lang string) (Row, error) {
	resp, err := s.client.fetchList(ctx, "list:trending:all:week:"+normalizeLang(lang), "/trending/all/week", lang, nil, 0)
	if err != nil {
		return Row{}, err
	}
	movieGenres, tvGenres, err := s.movieAndTVGenres(ctx, lang)
	if err != nil {
		return Row{}, err
	}

	items := make([]Title, 0, len(resp.Results))
	for _, it := range resp.Results {
		switch it.MediaType {
		case "movie":
			items = append(items, normalizeListItem(it, "movie", movieGenres))
		case "tv":
			items = append(items, normalizeListItem(it, "tv", tvGenres))
		default:
			// "person" and anything else isn't a title card, skip.
		}
	}
	return Row{ID: "trending", Title: homeTitle("trending", lang), Items: items}, nil
}

func (s *Service) newReleasesRow(ctx context.Context, lang string) (Row, error) {
	movies, err := s.client.fetchList(ctx, "list:now_playing:movie:"+normalizeLang(lang)+":1", "/movie/now_playing", lang, nil, 1)
	if err != nil {
		return Row{}, err
	}
	tv, err := s.client.fetchList(ctx, "list:on_the_air:tv:"+normalizeLang(lang)+":1", "/tv/on_the_air", lang, nil, 1)
	if err != nil {
		return Row{}, err
	}
	movieGenres, tvGenres, err := s.movieAndTVGenres(ctx, lang)
	if err != nil {
		return Row{}, err
	}

	items := make([]Title, 0, len(movies.Results)+len(tv.Results))
	max := len(movies.Results)
	if len(tv.Results) > max {
		max = len(tv.Results)
	}
	for i := 0; i < max; i++ {
		if i < len(movies.Results) {
			items = append(items, normalizeListItem(movies.Results[i], "movie", movieGenres))
		}
		if i < len(tv.Results) {
			items = append(items, normalizeListItem(tv.Results[i], "tv", tvGenres))
		}
	}
	return Row{ID: "new_releases", Title: homeTitle("new_releases", lang), Items: items}, nil
}

func (s *Service) simpleListRow(ctx context.Context, id, title, path, mediaType string, extra url.Values, lang string) (Row, error) {
	key := fmt.Sprintf("list:%s:%s:%s:1", cacheSegment(path), mediaType, normalizeLang(lang))
	resp, err := s.client.fetchList(ctx, key, path, lang, extra, 1)
	if err != nil {
		return Row{}, err
	}
	genres, err := s.client.fetchGenreMap(ctx, mediaType, lang)
	if err != nil {
		return Row{}, err
	}
	items := make([]Title, 0, len(resp.Results))
	for _, it := range resp.Results {
		items = append(items, normalizeListItem(it, mediaType, genres))
	}
	return Row{ID: id, Title: title, Items: items}, nil
}

func (s *Service) animeRow(ctx context.Context, lang string) (Row, error) {
	filters := url.Values{"with_genres": {"16"}, "with_origin_country": {"JP"}}

	movies, err := s.client.fetchList(ctx, "list:discover:anime:movie:"+normalizeLang(lang)+":1", "/discover/movie", lang, filters, 1)
	if err != nil {
		return Row{}, err
	}
	tv, err := s.client.fetchList(ctx, "list:discover:anime:tv:"+normalizeLang(lang)+":1", "/discover/tv", lang, filters, 1)
	if err != nil {
		return Row{}, err
	}
	movieGenres, tvGenres, err := s.movieAndTVGenres(ctx, lang)
	if err != nil {
		return Row{}, err
	}

	items := make([]Title, 0, len(movies.Results)+len(tv.Results))
	for _, it := range tv.Results {
		items = append(items, normalizeListItem(it, "tv", tvGenres))
	}
	for _, it := range movies.Results {
		items = append(items, normalizeListItem(it, "movie", movieGenres))
	}
	return Row{ID: "anime", Title: homeTitle("anime", lang), Items: items}, nil
}

func (s *Service) movieAndTVGenres(ctx context.Context, lang string) (movie, tv map[int]string, err error) {
	movie, err = s.client.fetchGenreMap(ctx, "movie", lang)
	if err != nil {
		return nil, nil, err
	}
	tv, err = s.client.fetchGenreMap(ctx, "tv", lang)
	if err != nil {
		return nil, nil, err
	}
	return movie, tv, nil
}

func cacheSegment(path string) string {
	// "/movie/popular" -> "popular", "/discover/movie" -> "discover"
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// --- List / Search ------------------------------------------------------

var ErrInvalidType = fmt.Errorf("catalog: type must be movie or tv")

// List implements GET /api/v1/catalog/list: TMDB /discover with filters.
func (s *Service) List(ctx context.Context, f ListFilters) (ListResponse, error) {
	if f.Type != "movie" && f.Type != "tv" {
		return ListResponse{}, ErrInvalidType
	}
	if f.Page < 1 {
		f.Page = 1
	}

	q := url.Values{}
	if f.Genre > 0 {
		q.Set("with_genres", fmt.Sprint(f.Genre))
	}
	if f.RatingFrom > 0 {
		q.Set("vote_average.gte", fmt.Sprint(f.RatingFrom))
	}

	dateField := "primary_release_date"
	if f.Type == "tv" {
		dateField = "first_air_date"
	}
	if f.YearFrom > 0 {
		q.Set(dateField+".gte", fmt.Sprintf("%04d-01-01", f.YearFrom))
	}
	if f.YearTo > 0 {
		q.Set(dateField+".lte", fmt.Sprintf("%04d-12-31", f.YearTo))
	}

	switch f.Sort {
	case "rating":
		q.Set("sort_by", "vote_average.desc")
		q.Set("vote_count.gte", "50") // avoid single-vote 10/10 noise dominating
	case "year":
		q.Set("sort_by", dateField+".desc")
	default:
		q.Set("sort_by", "popularity.desc")
	}

	key := fmt.Sprintf("list:discover:%s:%s:%s:%d", f.Type, q.Encode(), normalizeLang(f.Lang), f.Page)
	resp, err := s.client.fetchList(ctx, key, "/discover/"+f.Type, f.Lang, q, f.Page)
	if err != nil {
		return ListResponse{}, err
	}
	genres, err := s.client.fetchGenreMap(ctx, f.Type, f.Lang)
	if err != nil {
		return ListResponse{}, err
	}

	items := make([]Title, 0, len(resp.Results))
	for _, it := range resp.Results {
		items = append(items, normalizeListItem(it, f.Type, genres))
	}
	return ListResponse{Page: resp.Page, TotalPages: resp.TotalPages, Items: items}, nil
}

// Search implements GET /api/v1/catalog/search: TMDB /search/multi,
// filtered down to movie/tv (person results dropped, per docs/api.md).
func (s *Service) Search(ctx context.Context, q, lang string, page int) (ListResponse, error) {
	if page < 1 {
		page = 1
	}
	query := url.Values{"query": {q}}
	key := fmt.Sprintf("list:search:multi:%s:%s:%d", q, normalizeLang(lang), page)
	resp, err := s.client.fetchList(ctx, key, "/search/multi", lang, query, page)
	if err != nil {
		return ListResponse{}, err
	}
	movieGenres, tvGenres, err := s.movieAndTVGenres(ctx, lang)
	if err != nil {
		return ListResponse{}, err
	}

	items := make([]Title, 0, len(resp.Results))
	for _, it := range resp.Results {
		switch it.MediaType {
		case "movie":
			items = append(items, normalizeListItem(it, "movie", movieGenres))
		case "tv":
			items = append(items, normalizeListItem(it, "tv", tvGenres))
		default:
			// drop "person" results
		}
	}
	return ListResponse{Page: resp.Page, TotalPages: resp.TotalPages, Items: items}, nil
}

// --- Title ----------------------------------------------------------------

var ErrTitleNotFound = fmt.Errorf("catalog: title not found")

// Title implements GET /api/v1/catalog/title/{tmdb_id}. season, if > 0,
// requests that single season's episode list be filled in (see
// docs/backend.md task brief: "сезоны отдельными запросами — только по
// запросу ?season=N, чтобы не бомбить TMDB").
func (s *Service) Title(ctx context.Context, mediaType string, id, season int, lang string) (Title, error) {
	return s.title(ctx, mediaType, id, season, lang, true)
}

// title is the shared implementation behind Title and Card. fetchRating
// gates the OMDb lookup: only the single-title endpoint enriches
// imdb_rating — Card is also used to build shelves (e.g.
// continue_watching, one OMDb-eligible call per row item), which the task
// brief explicitly excludes to avoid hammering OMDb for lists (item 3).
func (s *Service) title(ctx context.Context, mediaType string, id, season int, lang string, fetchRating bool) (Title, error) {
	if mediaType != "movie" && mediaType != "tv" {
		return Title{}, ErrInvalidType
	}

	detail, err := s.client.fetchDetail(ctx, mediaType, id, lang)
	if err != nil {
		if errors.Is(err, errTMDBNotFound) {
			return Title{}, ErrTitleNotFound
		}
		return Title{}, err
	}
	if detail.ID == 0 {
		return Title{}, ErrTitleNotFound
	}

	var seasonDetail *tmdbSeasonDetail
	if mediaType == "tv" && season > 0 {
		sd, err := s.client.fetchSeason(ctx, id, season, lang)
		if err != nil {
			// Non-fatal: still return the title, just without episodes
			// filled in for the requested season.
			seasonDetail = nil
		} else {
			seasonDetail = &sd
		}
	}

	// withExtras=false: cast/similar/recommendations have no consumer in the UI
	// yet (verified: zero readers in web/src), and they were up to half the
	// title payload. The recommender reads them from the cached detail directly.
	out := normalizeDetail(detail, mediaType, season, seasonDetail, false)

	// Trailers: pull uk/ru/en (+untagged) in one call so the trailers modal can
	// offer a language choice. Falls back to whatever the detail request already
	// carried if the multi-lang call fails. Only on full cards (fetchRating).
	if fetchRating {
		if vids, verr := s.client.fetchVideosAllLangs(ctx, mediaType, id); verr == nil && len(vids) > 0 {
			out.Trailers = normalizeTrailers(vids)
		}
	}

	if fetchRating && s.omdb != nil && out.ExternalIDs != nil && out.ExternalIDs.ImdbID != "" {
		if rating, ok := s.omdb.FetchRating(ctx, out.ExternalIDs.ImdbID); ok {
			out.ImdbRating = rating
		}
	}

	return out, nil
}

// --- Genres -----------------------------------------------------------

// Genres implements GET /api/v1/catalog/genres (Promin extension, see
// README deviations — not in the docs/api.md canonical endpoint list, but the
// task brief explicitly requires it and /catalog/list needs genre ids to
// filter by).
func (s *Service) Genres(ctx context.Context, mediaType, lang string) (GenresResponse, error) {
	if mediaType != "movie" && mediaType != "tv" {
		return GenresResponse{}, ErrInvalidType
	}
	list, err := s.client.fetchGenreList(ctx, mediaType, lang)
	if err != nil {
		return GenresResponse{}, err
	}
	out := make([]GenreDTO, 0, len(list))
	for _, g := range list {
		out = append(out, GenreDTO{ID: g.ID, Name: g.Name})
	}
	return GenresResponse{Genres: out}, nil
}
