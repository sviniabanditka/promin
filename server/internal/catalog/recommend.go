package catalog

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// Recommendation / personalized-home shelves. Everything here is CACHE-FIRST:
// per-seed data is read straight from tmdb_cache (stale-ok, never fetched on
// the request path), and the only net-new upstream calls are the themed and
// provider /discover queries (fetchList, 6h TTL, shared across all users).
// The catalog package stays user-agnostic — the caller (internal/httpapi)
// reads the user's finished-list + watched-set from SQLite and passes them in,
// exactly like ContinueWatchingRow.

const (
	// minShelfItems drops a personalized shelf thinner than this — a 3-poster
	// lane reads as broken, not personal.
	minShelfItems = 8
	// themedVoteFloor: regional / post-Soviet titles rarely clear 100 TMDB
	// votes, so a high floor collapses themed lanes to translated Hollywood.
	// Calibratable against real family use (docs note).
	themedVoteFloor = "20"
	// watchRegion scopes provider availability + certifications. Household is
	// UA; calibratable.
	watchRegion = "UA"
)

// PersonalRows builds the personalized shelves (recommendation + themed) for
// an authenticated user from their finished-title seeds. seen is the shared
// exclude+dedup set (starts as the watched-set); each shelf adds its picks so
// no title repeats across shelves. epochDay drives daily seed rotation.
// Returns shelves in display order; nil/thin shelves are dropped.
func (s *Service) PersonalRows(ctx context.Context, lang string, finished []store.Timecode, seen map[int]bool, epochDay int) []Row {
	if len(finished) == 0 {
		return nil
	}
	if seen == nil {
		seen = map[int]bool{}
	}
	rows := make([]Row, 0, 3)

	// Shelf A: recommendations from the most-recently-finished title.
	seedA := finished[0]
	if row := s.RecsRow(lang, mediaTypeOr(seedA.MediaType), int(seedA.TMDBID), seen); row != nil {
		rows = append(rows, *row)
	}

	// Themed "{genre} · {actor}" from the first finished MOVIE (genre ids and
	// with_cast are movie-scoped; a TV seed's genres wouldn't map).
	for _, t := range finished {
		if mediaTypeOr(t.MediaType) == "movie" {
			if row := s.ThemedRow(ctx, lang, int(t.TMDBID), seen); row != nil {
				rows = append(rows, *row)
			}
			break
		}
	}

	// Shelf B: recommendations from a seed rotated daily within the finished
	// pool, for novelty at zero fetch cost. Skip if it collapses to seedA.
	if len(finished) > 1 {
		idx := epochDay % len(finished)
		if idx == 0 {
			idx = 1
		}
		seedB := finished[idx]
		if row := s.RecsRow(lang, mediaTypeOr(seedB.MediaType), int(seedB.TMDBID), seen); row != nil {
			rows = append(rows, *row)
		}
	}

	return rows
}

// RecsRow builds a "Тому що ви дивилися «X»" shelf from a seed title's
// already-cached detail (recommendations, backfilled with similar), minus
// anything in exclude. Cache-only → nil on cache miss. Adds its picks to
// exclude (cross-shelf dedup).
func (s *Service) RecsRow(lang, mediaType string, seedID int, exclude map[int]bool) *Row {
	d, ok := s.client.cachedDetail(mediaType, seedID, lang)
	if !ok {
		return nil
	}
	items := normalizeRelated(d.Recommendations, mediaType)
	if len(items) < minShelfItems {
		items = append(items, normalizeRelated(d.Similar, mediaType)...)
	}
	picked := pick(items, exclude)
	if len(picked) < minShelfItems {
		return nil
	}
	name := d.Title
	if name == "" {
		name = d.Name
	}
	return &Row{ID: "recs:" + mediaType + ":" + strconv.Itoa(seedID), Title: recsTitle(name, lang), Items: picked}
}

// ThemedRow builds a "{genre} · {actor}" shelf from a movie seed's top-billed
// cast member + primary genre via one /discover call. Falls back to a plain
// "Більше: {genre}" lane if the actor+genre pair is too thin.
func (s *Service) ThemedRow(ctx context.Context, lang string, seedID int, exclude map[int]bool) *Row {
	d, ok := s.client.cachedDetail("movie", seedID, lang)
	if !ok || len(d.Credits.Cast) == 0 || len(d.Genres) == 0 {
		return nil
	}
	actor := d.Credits.Cast[0]
	genre := d.Genres[0]

	q := url.Values{
		"with_cast":      {strconv.Itoa(actor.ID)},
		"with_genres":    {strconv.Itoa(genre.ID)},
		"sort_by":        {"vote_count.desc"},
		"vote_count.gte": {themedVoteFloor},
	}
	if row := s.discoverRow(ctx, "theme:cast:"+strconv.Itoa(actor.ID)+":"+strconv.Itoa(genre.ID),
		themedTitle(genre.Name, actor.Name), "/discover/movie", "movie", q, lang, exclude); row != nil {
		return row
	}
	// Fallback: drop with_cast.
	q2 := url.Values{"with_genres": {strconv.Itoa(genre.ID)}, "sort_by": {"popularity.desc"}}
	return s.discoverRow(ctx, "theme:genre:"+strconv.Itoa(genre.ID),
		moreGenreTitle(genre.Name, lang), "/discover/movie", "movie", q2, lang, exclude)
}

// ProviderRotatingRow builds one "Новинки на {provider}" shelf, the provider
// rotating daily. Editorial (user-agnostic) — the /discover result is cached
// once per (provider, 6h) and shared across all users. Rotation FALLS THROUGH
// the list from the day's offset: some providers (Disney+, Amazon) return an
// empty catalog for watch_region=UA, so a fixed pick would give no shelf on
// their day — we take the first provider that actually yields one.
func (s *Service) ProviderRotatingRow(ctx context.Context, lang string, epochDay int, exclude map[int]bool) *Row {
	today := time.Now().UTC().Format("2006-01-02")
	for i := 0; i < len(providers); i++ {
		p := providers[(epochDay+i)%len(providers)]
		q := url.Values{
			"with_watch_providers":     {strconv.Itoa(p.id)},
			"watch_region":             {watchRegion},
			"sort_by":                  {"primary_release_date.desc"},
			"vote_count.gte":           {themedVoteFloor},
			"primary_release_date.lte": {today}, // no unreleased future dates
		}
		if row := s.discoverRow(ctx, "provider:"+strconv.Itoa(p.id), providerTitle(p.name, lang),
			"/discover/movie", "movie", q, lang, exclude); row != nil {
			return row
		}
	}
	return nil
}

type provider struct {
	id   int
	name string
}

// Well-known TMDB watch-provider ids (global). Rotated one-per-day.
var providers = []provider{
	{8, "Netflix"},
	{350, "Apple TV+"},
	{337, "Disney+"},
	{119, "Amazon Prime Video"},
	{1899, "Max"},
}

// discoverRow runs one cached /discover list and turns it into a shelf, minus
// exclude. Uses normalizeRelated (no genre-name round-trip — home cards don't
// show genre chips). nil if the shelf ends up thinner than minShelfItems.
func (s *Service) discoverRow(ctx context.Context, id, title, path, mediaType string, q url.Values, lang string, exclude map[int]bool) *Row {
	resp, err := s.client.fetchList(ctx, "list:"+id+":"+normalizeLang(lang), path, lang, q, 1)
	if err != nil {
		return nil
	}
	picked := pick(normalizeRelated(resp, mediaType), exclude)
	if len(picked) < minShelfItems {
		return nil
	}
	return &Row{ID: id, Title: title, Items: picked}
}

// pick filters out excluded/duplicate ids and records each kept id in exclude
// (so it's also the cross-shelf seen-set).
func pick(items []Title, exclude map[int]bool) []Title {
	out := make([]Title, 0, len(items))
	for _, it := range items {
		if it.TMDBID == 0 || exclude[it.TMDBID] {
			continue
		}
		exclude[it.TMDBID] = true
		out = append(out, it)
	}
	return out
}

func mediaTypeOr(mt string) string {
	if mt == "tv" {
		return "tv"
	}
	return "movie"
}

// --- localized shelf labels -------------------------------------------------

func recsTitle(name, lang string) string {
	switch normalizeLang(lang) {
	case "ru":
		return "Потому что вы смотрели «" + name + "»"
	case "en":
		return "Because you watched “" + name + "”"
	default:
		return "Тому що ви дивилися «" + name + "»"
	}
}

// themedTitle uses a neutral "·" connector deliberately: any "з/с/with"
// connector leaves foreign names grammatically wrong in uk/ru (instrumental
// case), and per-language declension is not worth building.
func themedTitle(genre, actor string) string {
	return genre + " · " + actor
}

func moreGenreTitle(genre, lang string) string {
	switch normalizeLang(lang) {
	case "ru":
		return "Больше: " + genre
	case "en":
		return "More: " + genre
	default:
		return "Більше: " + genre
	}
}

func providerTitle(name, lang string) string {
	switch normalizeLang(lang) {
	case "en":
		return "New on " + name
	default: // uk + ru read the same here
		return "Новинки на " + name
	}
}
