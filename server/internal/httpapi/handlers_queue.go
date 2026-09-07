package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sviniabanditka/promin/server/internal/sync"
)

// queueHandlers: the watch queue (docs/miniapp.md). Mounted behind requireAuth.
type queueHandlers struct {
	svc *sync.Service
}

func (h *queueHandlers) list(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	items, err := h.svc.ListQueue(info.User.ID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *queueHandlers) add(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	var req historyRequest // same shape: {tmdb_id, media_type, season?, episode?}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TMDBID <= 0 ||
		(req.Season != nil && *req.Season < 0) || (req.Episode != nil && *req.Episode < 0) {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	item, created, err := h.svc.AddQueue(info.User.ID, req.TMDBID, req.MediaType, req.Season, req.Episode)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, item)
}

func (h *queueHandlers) remove(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeBadRequest(w, "невірний id")
		return
	}
	if err := h.svc.RemoveQueue(info.User.ID, id); err != nil {
		writeSyncError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *queueHandlers) move(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeBadRequest(w, "невірний id")
		return
	}
	var req struct {
		Position *int `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Position == nil {
		writeBadRequest(w, "поле position обов'язкове")
		return
	}
	if err := h.svc.MoveQueue(info.User.ID, id, *req.Position); err != nil {
		writeSyncError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *queueHandlers) clear(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	if err := h.svc.ClearQueue(info.User.ID); err != nil {
		writeSyncError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pop hands the TV the head and drops it; 204 on an empty queue.
func (h *queueHandlers) pop(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	item, ok, err := h.svc.PopQueue(info.User.ID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
