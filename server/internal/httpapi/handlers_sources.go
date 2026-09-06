package httpapi

import (
	"context"
	"errors"
	"github.com/sviniabanditka/promin/server/internal/catalog"
	"net/http"
	"time"

	"github.com/sviniabanditka/promin/server/internal/sources"
)

// sourcesHandlers holds the dependencies for /api/v1/sources/online[/resolve],
// wired in server.go. Only the "online" (Lampac balancer) path is
// implemented in this phase; /api/v1/sources/torrents (JacRed) is a later
// phase.
type sourcesHandlers struct {
	svc *sources.Service
	cat *catalog.Service
}

// ruTitle fetches the Russian TMDB title (cached in tmdb_cache like every
// detail) — native catalogs in Russian can't be searched by the Ukrainian UI
// title. Best-effort and bounded: a miss just leaves the field empty.
func (h *sourcesHandlers) ruTitle(ctx context.Context, mediaType string, tmdbID int) string {
	if h.cat == nil || tmdbID <= 0 {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	t, err := h.cat.Title(cctx, mediaType, tmdbID, 0, "ru")
	if err != nil {
		return ""
	}
	return t.Title
}

func (h *sourcesHandlers) online(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tmdbID := atoiDefault(q.Get("tmdb_id"), 0)
	if tmdbID <= 0 {
		writeBadRequest(w, "параметр tmdb_id обов'язковий")
		return
	}
	mediaType := q.Get("type")
	if mediaType != "movie" && mediaType != "tv" {
		writeBadRequest(w, "параметр type має бути movie або tv")
		return
	}

	resp := h.svc.Online(r.Context(), sources.OnlineRequest{
		TMDBID:        tmdbID,
		Type:          mediaType,
		Title:         q.Get("title"),
		TitleRU:       h.ruTitle(r.Context(), mediaType, tmdbID),
		OriginalTitle: q.Get("original_title"),
		Year:          atoiDefault(q.Get("year"), 0),
		IMDbID:        q.Get("imdb_id"),
	})
	writeJSON(w, http.StatusOK, resp)
}

func (h *sourcesHandlers) resolve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tmdbID := atoiDefault(q.Get("tmdb_id"), 0)
	if tmdbID <= 0 {
		writeBadRequest(w, "параметр tmdb_id обов'язковий")
		return
	}
	mediaType := q.Get("type")
	if mediaType != "movie" && mediaType != "tv" {
		writeBadRequest(w, "параметр type має бути movie або tv")
		return
	}
	// Lampac и /sources/online отдают поле "balanser"; принимаем его.
	balancer := q.Get("balanser")
	if balancer == "" {
		writeBadRequest(w, "параметр balanser обов'язковий")
		return
	}

	resp, err := h.svc.Resolve(r.Context(), sources.ResolveRequest{
		Balancer:      balancer,
		TMDBID:        tmdbID,
		Type:          mediaType,
		Season:        atoiDefault(q.Get("season"), 0),
		Episode:       atoiDefault(q.Get("episode"), 0),
		Voice:         q.Get("voice"),
		Title:         q.Get("title"),
		TitleRU:       h.ruTitle(r.Context(), mediaType, tmdbID),
		OriginalTitle: q.Get("original_title"),
		Year:          atoiDefault(q.Get("year"), 0),
		IMDbID:        q.Get("imdb_id"),
		// Client capability (docs/streaming.md): demuxed_hls=false → old Samsung, route
		// HLS through /remux so it gets a single muxed stream it can play.
		PreferMuxed: q.Get("demuxed_hls") == "false",
	})
	if err != nil {
		writeSourcesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeSourcesError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sources.ErrBalancerNotFound):
		writeNotFound(w, "balancer_not_found", "балансер не знайдено для цього тайтла")
	case errors.Is(err, sources.ErrUpstreamUnavailable):
		writeSourcesUnavailable(w)
	default:
		writeInternal(w, err)
	}
}
