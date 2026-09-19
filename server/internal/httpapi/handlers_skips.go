package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/sviniabanditka/promin/server/internal/store"
)

// skipHandlers serves the learned intro/recap segments (docs/player.md,
// "Skip intro"). The player reports the forward jumps a viewer makes near the
// start of an episode; once two episodes of a season agree, the segment comes
// back as an auto-skip for the rest of the show.
type skipHandlers struct {
	repo *store.SkipsRepo
}

// Bounds of a jump worth learning from. Everything outside is an ordinary seek
// (a viewer looking for a scene), not an intro.
const (
	// The jump must START inside the opening stretch of the episode.
	skipObserveWindowSec = 900.0
	skipMinLenSec        = 10.0
	skipMaxLenSec        = 240.0
	// How many distinct episodes must agree before the segment is offered.
	skipMinVotes = 2
)

type skipSegmentDTO struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Votes int     `json:"votes"`
}

// list implements GET /api/v1/skips?tmdb_id=&season=.
func (h *skipHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tmdbID := atoiDefault(q.Get("tmdb_id"), 0)
	if tmdbID <= 0 {
		writeBadRequest(w, "параметр tmdb_id обов'язковий")
		return
	}
	season := atoiDefault(q.Get("season"), 0)
	if season < 0 {
		writeBadRequest(w, "season має бути ≥ 0")
		return
	}
	segs, err := h.repo.List(int64(tmdbID), season, skipMinVotes)
	if err != nil {
		writeInternal(w, err)
		return
	}
	out := make([]skipSegmentDTO, 0, len(segs))
	for _, s := range segs {
		out = append(out, skipSegmentDTO{Start: s.StartSec, End: s.EndSec, Votes: s.Votes})
	}
	writeJSON(w, http.StatusOK, map[string]any{"segments": out})
}

// observe implements POST /api/v1/skips/observe with
// {tmdb_id, season, episode, from, to} — one manual forward jump.
func (h *skipHandlers) observe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TMDBID  int64   `json:"tmdb_id"`
		Season  int     `json:"season"`
		Episode int     `json:"episode"`
		From    float64 `json:"from"`
		To      float64 `json:"to"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&req); err != nil {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	if req.TMDBID <= 0 || req.Season < 0 || req.Episode < 0 {
		writeBadRequest(w, "tmdb_id, season, episode обов'язкові")
		return
	}
	if !observableSkip(req.From, req.To) {
		// Not an intro-shaped jump: accept and ignore, the client fires this
		// blind after every seek.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.repo.Observe(req.TMDBID, req.Season, req.Episode, req.From, req.To); err != nil {
		writeInternal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// observableSkip: a forward jump that starts in the opening stretch and is long
// enough to be an intro but short enough not to be "I want the last act".
func observableSkip(from, to float64) bool {
	if from < 0 || to <= from || from > skipObserveWindowSec {
		return false
	}
	length := to - from
	return length >= skipMinLenSec && length <= skipMaxLenSec
}
