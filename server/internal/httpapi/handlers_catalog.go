package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/sviniabanditka/promin/server/internal/catalog"
	"github.com/sviniabanditka/promin/server/internal/sync"
)

// catalogHandlers holds the dependencies for /api/v1/catalog/*, wired in
// server.go. Paths/params/pagination follow docs/api.md;
// GET /catalog/genres is a Promin extension not present in that canonical
// list (see README deviations) added because /catalog/list needs genre
// ids to filter by. syncSvc is only used by home, to enrich the
// "continue_watching" shelf for authenticated callers (Phase 3
// добивание).
type catalogHandlers struct {
	svc     *catalog.Service
	syncSvc *sync.Service
}

// home is mounted behind optionalAuth (server.go): guests get the plain
// shelves, callers with a valid token additionally get "continue_watching"
// prepended first, per docs/api.md's example response.
func (h *catalogHandlers) home(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	resp, err := h.svc.Home(r.Context(), lang)
	if err != nil {
		writeCatalogError(w, err)
		return
	}

	if info, ok := authFrom(r); ok {
		resp.Rows = h.personalizeHome(r, lang, info.User.ID, resp.Rows)
	}

	writeJSON(w, http.StatusOK, resp)
}

// backdrops serves random catalog backdrops for the idle screensaver. Kept a
// separate endpoint (not derived from home) so the screensaver draws from the
// whole catalog instead of whatever is on the home shelves.
func (h *catalogHandlers) backdrops(w http.ResponseWriter, r *http.Request) {
	limit := atoiDefault(r.URL.Query().Get("limit"), 40)
	if limit < 1 || limit > 120 {
		limit = 40
	}
	urls := h.svc.RandomBackdrops(r.Context(), r.URL.Query().Get("lang"), limit)
	writeJSON(w, http.StatusOK, map[string]any{"backdrops": urls})
}

// personalizeHome assembles the authenticated home: continue-watching, then
// personalized recommendation/themed shelves (only once the user has >=2
// finished titles — below that personalization is invisible-by-design, plain
// editorial shows), then one rotating provider ("Новинки на …") lane, then the
// editorial base. Personalization DISPLACES the weakest editorial lanes
// (popular_movies/popular_tv) so the D-pad lane count stays sane. Every step
// degrades to the plain editorial rows on any read error — home never 500s
// because a recommendation query hiccuped.
func (h *catalogHandlers) personalizeHome(r *http.Request, lang string, userID int64, editorial []catalog.Row) []catalog.Row {
	ctx := r.Context()
	epochDay := int(time.Now().Unix() / 86400)

	var continueRow *catalog.Row
	if tcs, err := h.syncSvc.ListContinueWatching(userID, 0); err == nil {
		continueRow = h.svc.ContinueWatchingRow(ctx, lang, tcs)
	}

	seen, err := h.syncSvc.WatchedSet(userID)
	if err != nil || seen == nil {
		seen = map[int]bool{}
	}

	var personal []catalog.Row
	if finished, err := h.syncSvc.ListFinished(userID, 10); err == nil && len(finished) >= 2 {
		personal = h.svc.PersonalRows(ctx, lang, finished, seen, epochDay)
		if len(personal) > 0 {
			editorial = dropRows(editorial, "popular_movies", "popular_tv")
		}
	}

	providerRow := h.svc.ProviderRotatingRow(ctx, lang, epochDay, seen)

	out := make([]catalog.Row, 0, len(editorial)+len(personal)+2)
	if continueRow != nil {
		out = append(out, *continueRow)
	}
	out = append(out, personal...)
	if providerRow != nil {
		out = append(out, *providerRow)
	}
	out = append(out, editorial...)
	return out
}

// dropRows returns rows minus any whose ID is in ids.
func dropRows(rows []catalog.Row, ids ...string) []catalog.Row {
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	out := rows[:0:0]
	for _, row := range rows {
		if !drop[row.ID] {
			out = append(out, row)
		}
	}
	return out
}

func (h *catalogHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := catalog.ListFilters{
		Type:       q.Get("type"),
		Genre:      atoiDefault(q.Get("genre"), 0),
		YearFrom:   atoiDefault(q.Get("year_from"), 0),
		YearTo:     atoiDefault(q.Get("year_to"), 0),
		RatingFrom: atofDefault(q.Get("rating_from"), 0),
		Sort:       q.Get("sort"),
		Page:       atoiDefault(q.Get("page"), 1),
		Lang:       q.Get("lang"),
	}

	resp, err := h.svc.List(r.Context(), f)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *catalogHandlers) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := q.Get("q")
	if query == "" {
		writeBadRequest(w, "параметр q обов'язковий")
		return
	}
	page := atoiDefault(q.Get("page"), 1)

	resp, err := h.svc.Search(r.Context(), query, q.Get("lang"), page)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *catalogHandlers) title(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("tmdb_id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		writeBadRequest(w, "невірний tmdb_id")
		return
	}

	q := r.URL.Query()
	mediaType := q.Get("type")
	if mediaType == "" {
		writeBadRequest(w, "параметр type обов'язковий (movie|tv)")
		return
	}
	season := atoiDefault(q.Get("season"), 0)

	title, err := h.svc.Title(r.Context(), mediaType, id, season, q.Get("lang"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	// The viewer's latest spot on this title (any episode) rides along so the
	// screen can offer "Continue" from server truth, not only from the local
	// timecode cache (capped at 300 records).
	if info, ok := authFrom(r); ok && h.syncSvc != nil {
		if tc, found := h.syncSvc.LatestTimecode(info.User.ID, int64(id), mediaType); found {
			title.Timecode = &catalog.Timecode{PositionSec: tc.PositionSec, DurationSec: tc.DurationSec, Season: tc.Season, Episode: tc.Episode}
		}
	}
	writeJSON(w, http.StatusOK, title)
}

func (h *catalogHandlers) genres(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mediaType := q.Get("type")
	if mediaType == "" {
		writeBadRequest(w, "параметр type обов'язковий (movie|tv)")
		return
	}

	resp, err := h.svc.Genres(r.Context(), mediaType, q.Get("lang"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeCatalogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalog.ErrTitleNotFound):
		writeNotFound(w, "title_not_found", "тайтл не знайдено")
	case errors.Is(err, catalog.ErrInvalidType):
		writeBadRequest(w, "type має бути movie або tv")
	case errors.Is(err, catalog.ErrUpstreamUnavailable):
		writeUpstreamUnavailable(w)
	default:
		writeInternal(w, err)
	}
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func atofDefault(s string, def float64) float64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}
