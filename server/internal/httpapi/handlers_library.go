package httpapi

import (
	"net/http"

	"github.com/sviniabanditka/promin/server/internal/store"
	"github.com/sviniabanditka/promin/server/internal/sync"
)

// "Continue" vs "Finished" (docs/frontend.md Library). A movie is finished at
// ≥90 % (the store's SQL). A series is finished only when its LAST aired
// episode (TMDB last_episode_to_air, cache-only) is at ≥90 % — any other
// finished episode just means "next episode", so the title stays in Continue.
const finishedRatio = 0.9

func finishedAt(t store.Timecode) bool {
	return t.DurationSec > 0 && t.PositionSec >= t.DurationSec*finishedRatio
}

// seriesDone reports whether the user watched the series' last aired episode.
// Unknown last episode (cache miss) → false: the show stays in Continue.
func (h *catalogHandlers) seriesDone(userID, tmdbID int64, lang string) bool {
	season, episode, ok := h.svc.LastAiredEpisode(int(tmdbID), lang)
	if !ok {
		return false
	}
	t, err := h.syncSvc.GetTimecode(userID, tmdbID, "tv", season, episode)
	if err != nil {
		return false
	}
	return finishedAt(store.Timecode{PositionSec: t.PositionSec, DurationSec: t.DurationSec})
}

// continueRows is ListContinueWatching minus the series that are done.
func (h *catalogHandlers) continueRows(userID int64, lang string, limit int) ([]store.Timecode, error) {
	rows, err := h.syncSvc.ListContinueWatching(userID, limit)
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, t := range rows {
		if t.MediaType == "tv" && h.seriesDone(userID, t.TMDBID, lang) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// finishedRows is ListFinished (any finished row per title) narrowed to
// movies plus series whose last episode is done.
func (h *catalogHandlers) finishedRows(userID int64, lang string, limit int) ([]store.Timecode, error) {
	rows, err := h.syncSvc.ListFinished(userID, limit*2)
	if err != nil {
		return nil, err
	}
	out := make([]store.Timecode, 0, len(rows))
	for _, t := range rows {
		if t.MediaType == "tv" && !h.seriesDone(userID, t.TMDBID, lang) {
			continue
		}
		out = append(out, t)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func timecodeItems(rows []store.Timecode) []sync.TimecodeDTO {
	items := make([]sync.TimecodeDTO, 0, len(rows))
	for _, t := range rows {
		items = append(items, sync.TimecodeDTO{
			TMDBID: t.TMDBID, MediaType: t.MediaType, Season: t.Season, Episode: t.Episode,
			PositionSec: t.PositionSec, DurationSec: t.DurationSec, UpdatedAt: t.UpdatedAt,
		})
	}
	return items
}

// GET /api/v1/timecodes/continue
func (h *catalogHandlers) continueWatching(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	rows, err := h.continueRows(info.User.ID, r.URL.Query().Get("lang"), atoiDefault(r.URL.Query().Get("limit"), 20))
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": timecodeItems(rows)})
}

// GET /api/v1/timecodes/finished
func (h *catalogHandlers) finished(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	rows, err := h.finishedRows(info.User.ID, r.URL.Query().Get("lang"), atoiDefault(r.URL.Query().Get("limit"), 20))
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": timecodeItems(rows)})
}
