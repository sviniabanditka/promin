package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/sviniabanditka/promin/server/internal/sync"
)

// playerStateHandlers implements POST /api/v1/player/state (docs/miniapp.md):
// the TV reports what it plays; the hub keeps it per device and fans it out
// as player_state for the Mini App.
type playerStateHandlers struct {
	hub *sync.Hub
}

func (h *playerStateHandlers) set(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	var req struct {
		sync.PlayerState
		Closed bool `json:"closed"`
		Lists  bool `json:"lists"` // voices/subtitles present in this report
	}
	// A report is a few hundred bytes; the voice/subtitle lists a few dozen
	// rows. Anything larger is stored per device in memory and fanned out to
	// every phone, so cap it here rather than at the generic 1 MiB body limit.
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	if len(req.Title) > 300 || len(req.Voices) > 64 || len(req.Subtitles) > 64 {
		writeBadRequest(w, "title ≤ 300, voices/subtitles ≤ 64")
		return
	}
	dev := info.Session.ID
	if req.Closed {
		h.hub.ClearPlayerState(info.User.ID, dev)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if req.TMDBID <= 0 || (req.MediaType != "movie" && req.MediaType != "tv") {
		writeBadRequest(w, "tmdb_id і media_type (movie|tv) обов'язкові")
		return
	}
	if req.Volume < 0 || req.Volume > 100 {
		writeBadRequest(w, "volume: 0..100")
		return
	}
	if !req.Lists {
		// Position-only tick: keep the menus from the last full report.
		if prev := h.hub.PlayerState(info.User.ID, dev); prev != nil {
			req.Voices, req.Subtitles = prev.Voices, prev.Subtitles
		}
	}
	h.hub.SetPlayerState(info.User.ID, dev, req.PlayerState)
	w.WriteHeader(http.StatusNoContent)
}

// deviceSettings: POST /api/v1/device/settings {legacy_tv_mode, reduce_motion,
// debug_mode} ("true"/"false") — the TV reports its device-local settings on
// boot and on change so the Mini App can show and flip them.
func (h *playerStateHandlers) deviceSettings(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	var body map[string]string
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		writeBadRequest(w, "невірне тіло")
		return
	}
	h.hub.SetDeviceSettings(info.User.ID, info.Session.ID, body)
	w.WriteHeader(http.StatusNoContent)
}
