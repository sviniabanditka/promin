package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sviniabanditka/promin/server/internal/sync"
)

// syncHandlers wires bookmarks/playlists/history/timecodes/settings plus
// sync/bootstrap and sync/events, per docs/api.md sections 4 and
// 7. Every route here is mounted behind requireAuth in server.go.
type syncHandlers struct {
	svc *sync.Service
}

// --- Bookmarks ------------------------------------------------------------

func (h *syncHandlers) listBookmarks(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	list, err := h.svc.ListBookmarks(info.User.ID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bookmarks": list})
}

type bookmarkRequest struct {
	TMDBID    int64  `json:"tmdb_id"`
	MediaType string `json:"media_type"`
}

func (h *syncHandlers) addBookmark(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	var req bookmarkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TMDBID <= 0 {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	dto, created, err := h.svc.AddBookmark(info.User.ID, req.TMDBID, req.MediaType)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, dto)
}

func (h *syncHandlers) removeBookmark(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	tmdbID, err := strconv.ParseInt(r.PathValue("tmdb_id"), 10, 64)
	if err != nil || tmdbID <= 0 {
		writeBadRequest(w, "невірний tmdb_id")
		return
	}
	mediaType := r.URL.Query().Get("media_type")
	if err := h.svc.RemoveBookmark(info.User.ID, tmdbID, mediaType); err != nil {
		writeSyncError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Playlists --------------------------------------------------------------

func (h *syncHandlers) listPlaylists(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	list, err := h.svc.ListPlaylists(info.User.ID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlists": list})
}

type playlistRequest struct {
	Name string `json:"name"`
}

func (h *syncHandlers) createPlaylist(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	var req playlistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeBadRequest(w, "поле name обов'язкове")
		return
	}
	dto, err := h.svc.CreatePlaylist(info.User.ID, req.Name)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (h *syncHandlers) renamePlaylist(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeBadRequest(w, "невірний id")
		return
	}
	var req playlistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeBadRequest(w, "поле name обов'язкове")
		return
	}
	dto, err := h.svc.RenamePlaylist(info.User.ID, id, req.Name)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (h *syncHandlers) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeBadRequest(w, "невірний id")
		return
	}
	if err := h.svc.DeletePlaylist(info.User.ID, id); err != nil {
		writeSyncError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *syncHandlers) listPlaylistItems(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeBadRequest(w, "невірний id")
		return
	}
	items, err := h.svc.ListPlaylistItems(info.User.ID, id)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *syncHandlers) addPlaylistItem(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeBadRequest(w, "невірний id")
		return
	}
	var req bookmarkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TMDBID <= 0 {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	item, err := h.svc.AddPlaylistItem(info.User.ID, id, req.TMDBID, req.MediaType)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *syncHandlers) removePlaylistItem(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeBadRequest(w, "невірний id")
		return
	}
	itemID, err := strconv.ParseInt(r.PathValue("item_id"), 10, 64)
	if err != nil {
		writeBadRequest(w, "невірний item_id")
		return
	}
	if err := h.svc.RemovePlaylistItem(info.User.ID, id, itemID); err != nil {
		writeSyncError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- History --------------------------------------------------------------

type historyRequest struct {
	TMDBID    int64  `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	Season    *int   `json:"season"`
	Episode   *int   `json:"episode"`
}

// --- Timecodes --------------------------------------------------------------

func (h *syncHandlers) getTimecode(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	tmdbID, err := strconv.ParseInt(r.PathValue("tmdb_id"), 10, 64)
	if err != nil || tmdbID <= 0 {
		writeBadRequest(w, "невірний tmdb_id")
		return
	}
	q := r.URL.Query()
	mediaType := q.Get("media_type")
	season := atoiDefault(q.Get("season"), 0)
	episode := atoiDefault(q.Get("episode"), 0)

	dto, err := h.svc.GetTimecode(info.User.ID, tmdbID, mediaType, season, episode)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

type timecodeUpsertRequest struct {
	TMDBID      int64   `json:"tmdb_id"`
	MediaType   string  `json:"media_type"`
	Season      int     `json:"season"`
	Episode     int     `json:"episode"`
	PositionSec float64 `json:"position_sec"`
	DurationSec float64 `json:"duration_sec"`
	UpdatedAt   int64   `json:"updated_at"`
}

func (h *syncHandlers) upsertTimecode(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	var req timecodeUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TMDBID <= 0 || req.UpdatedAt <= 0 {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	result, err := h.svc.UpsertTimecode(info.User.ID, req.TMDBID, req.MediaType, req.Season, req.Episode, req.PositionSec, req.DurationSec, req.UpdatedAt)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *syncHandlers) continueWatching(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	limit := atoiDefault(r.URL.Query().Get("limit"), 20)
	rows, err := h.svc.ListContinueWatching(info.User.ID, limit)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	items := make([]sync.TimecodeDTO, 0, len(rows))
	for _, t := range rows {
		items = append(items, sync.TimecodeDTO{
			TMDBID: t.TMDBID, MediaType: t.MediaType, Season: t.Season, Episode: t.Episode,
			PositionSec: t.PositionSec, DurationSec: t.DurationSec, UpdatedAt: t.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// --- Settings ---------------------------------------------------------------

func (h *syncHandlers) getSettings(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	settings, err := h.svc.GetSettings(info.User.ID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

type settingRequest struct {
	Value string `json:"value"`
}

func (h *syncHandlers) putSetting(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	key := r.PathValue("key")
	if key == "" {
		writeBadRequest(w, "невірний key")
		return
	}
	var req settingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	// A setting is a short scalar or the 15-entry search history; anything
	// bigger is abuse — it would be persisted AND broadcast to every TV and
	// kept in the in-memory event journal for an hour.
	if len(key) > 64 || len(req.Value) > 4096 {
		writeBadRequest(w, "key ≤ 64 байт, value ≤ 4 КіБ")
		return
	}
	if err := h.svc.SetSetting(info.User.ID, key, req.Value); err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": req.Value})
}

// --- Bootstrap / poll fallback ----------------------------------------------

func (h *syncHandlers) bootstrap(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	snap, err := h.svc.Bootstrap(info.User.ID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (h *syncHandlers) events(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)

	events, cursor, gone := h.svc.PollEvents(info.User.ID, since)
	if gone {
		writeError(w, http.StatusGone, "gone", "курсор застарів, виконайте повний bootstrap")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "cursor": cursor})
}

func writeSyncError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sync.ErrNotFound):
		writeNotFound(w, "not_found", "запис не знайдено")
	case errors.Is(err, sync.ErrInvalidMediaType):
		writeBadRequest(w, "media_type має бути movie або tv")
	case errors.Is(err, sync.ErrQueueFull):
		writeError(w, http.StatusConflict, "queue_full", "черга заповнена (200 елементів)")
	default:
		writeInternal(w, err)
	}
}
