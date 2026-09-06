package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/sviniabanditka/promin/server/internal/auth"
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	dev := auth.TokenID(info.Session.Token)
	if req.Closed {
		h.hub.ClearPlayerState(info.User.ID, dev)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if req.TMDBID <= 0 || (req.MediaType != "movie" && req.MediaType != "tv") {
		writeBadRequest(w, "tmdb_id і media_type (movie|tv) обов'язкові")
		return
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
	h.hub.SetDeviceSettings(info.User.ID, auth.TokenID(info.Session.Token), body)
	w.WriteHeader(http.StatusNoContent)
}
