package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sviniabanditka/promin/server/internal/dvr"
	"github.com/sviniabanditka/promin/server/internal/store"
)

// A recording id is 16 hex chars (internal/dvr.newID); its files are the
// playlist and the numbered segments beside it — nothing else may be read out
// of the directory.
var recordingID = regexp.MustCompile(`^[0-9a-f]{16}$`)
var recordingFile = regexp.MustCompile(`^(playlist\.m3u8|seg-[0-9]{5}\.ts)$`)

// dvrHandlers serves recorded live TV (docs/tv.md): scheduling off the guide,
// the list on the TV screen, and the HLS files of a finished recording.
type dvrHandlers struct {
	svc  *dvr.Service
	repo *store.RecordingsRepo
}

func (h *dvrHandlers) enabled(w http.ResponseWriter) bool {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "dvr_disabled", "запис ТВ вимкнено")
		return false
	}
	return true
}

// list: GET /api/v1/tv/records → {records:[…]} for this profile.
func (h *dvrHandlers) list(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	info, _ := authFrom(r)
	recs, err := h.repo.List(info.User.ID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": recs})
}

// add: POST /api/v1/tv/records {channel_id, channel_title, title, start_at,
// end_at} — the programme's own times; internal/dvr adds the padding.
func (h *dvrHandlers) add(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	info, _ := authFrom(r)
	var req struct {
		ChannelID    string `json:"channel_id"`
		ChannelTitle string `json:"channel_title"`
		Title        string `json:"title"`
		StartAt      int64  `json:"start_at"`
		EndAt        int64  `json:"end_at"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		writeBadRequest(w, "невірне тіло запиту")
		return
	}
	if !tvChannelID.MatchString(req.ChannelID) {
		writeBadRequest(w, "невірний channel_id")
		return
	}
	if req.EndAt <= req.StartAt || req.StartAt <= 0 {
		writeBadRequest(w, "start_at/end_at обов'язкові")
		return
	}
	// A programme that ended before we were asked can never be recorded — the
	// stream only carries what is on air.
	if req.EndAt <= time.Now().Unix() {
		writeBadRequest(w, "передача вже завершилася")
		return
	}
	if len(req.Title) > 300 || len(req.ChannelTitle) > 200 {
		writeBadRequest(w, "title ≤ 300, channel_title ≤ 200")
		return
	}
	rec, err := h.svc.Schedule(info.User.ID, req.ChannelID, req.ChannelTitle, req.Title, req.StartAt, req.EndAt)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// remove: DELETE /api/v1/tv/records/{id} — stops it if running and deletes the
// files.
func (h *dvrHandlers) remove(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	info, _ := authFrom(r)
	id := r.PathValue("id")
	if !recordingID.MatchString(id) {
		writeBadRequest(w, "невірний id")
		return
	}
	if err := h.svc.Delete(id, info.User.ID); err != nil {
		if errors.Is(err, store.ErrRecordingNotFound) {
			writeNotFound(w, "record_not_found", "запис не знайдено")
			return
		}
		writeInternal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// timeshift: POST /api/v1/tv/channels/{id}/timeshift — start (or keep alive)
// the channel's rolling window and say where to play it from. The TV calls it
// on tune-in and every 30 s while watching; two TVs on one channel share the
// buffer, and it is dropped two minutes after the last heartbeat.
func (h *dvrHandlers) timeshift(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	id := r.PathValue("id")
	if !tvChannelID.MatchString(id) {
		writeBadRequest(w, "невірний id")
		return
	}
	window, err := h.svc.EnsureBuffer(id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "timeshift_unavailable", "таймшифт зараз недоступний")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        "/tv/timeshift/" + id + "/" + dvr.PlaylistFile,
		"window_sec": window,
	})
}

// serveTimeshift: GET /tv/timeshift/{id}/{file} — the rolling window's playlist
// and segments, same media token and token stamping as a recording.
func (h *dvrHandlers) serveTimeshift(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		writeNotFound(w, "timeshift_off", "таймшифт вимкнено")
		return
	}
	id := r.PathValue("id")
	file := r.PathValue("file")
	if !tvChannelID.MatchString(id) || !recordingFile.MatchString(file) {
		writeBadRequest(w, "невірний шлях")
		return
	}
	h.serveHLS(w, r, filepath.Join(h.svc.BufferDir(id), file), file)
}

// serveFile: GET /tv/records/{id}/{file} — the recording's playlist and
// segments, behind the media token like every other media route. The playlist's
// child URIs are relative, so the caller's token is stamped onto them (same
// helper the remux playlists use).
func (h *dvrHandlers) serveFile(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		writeNotFound(w, "record_not_found", "запис не знайдено")
		return
	}
	id := r.PathValue("id")
	file := r.PathValue("file")
	if !recordingID.MatchString(id) || !recordingFile.MatchString(file) {
		writeBadRequest(w, "невірний шлях")
		return
	}
	h.serveHLS(w, r, filepath.Join(h.svc.Dir(id), file), file)
}

// serveHLS is the shared body of the two media routes: a playlist with the
// caller's token stamped onto its relative children, or a segment off disk.
func (h *dvrHandlers) serveHLS(w http.ResponseWriter, r *http.Request, path, file string) {
	if strings.HasSuffix(file, ".m3u8") {
		data, err := os.ReadFile(path)
		if err != nil {
			writeNotFound(w, "record_not_ready", "запис ще не почався")
			return
		}
		if token := tokenFromRequest(r, true); token != "" {
			data = appendRemuxToken(data, token)
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	if _, err := os.Stat(path); err != nil {
		writeNotFound(w, "segment_not_found", "сегмент не знайдено")
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	// A timeshift segment is deleted as the window slides, so it must not be
	// cached as immutable forever by anything in between.
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeFile(w, r, path)
}
