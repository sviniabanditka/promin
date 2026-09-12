package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/sviniabanditka/promin/server/internal/remux"
	"github.com/sviniabanditka/promin/server/internal/youtube"
)

// ytHandlers is the YouTube section: everything under /api/v1/yt/* proxies the
// signed-in profile's account on the ytx sidecar; /play turns a video into a
// remux job the existing player can consume. All routes require a session;
// the sidecar itself is reachable only inside the cluster.
type ytHandlers struct {
	yt    *youtube.Client // nil → section disabled
	queue *remux.Queue
}

var (
	ytVideoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	ytPageID  = regexp.MustCompile(`^[A-Za-z0-9_-]{2,64}$`)
	ytQuality = map[string]bool{"2160p": true, "1440p": true, "1080p": true, "720p": true, "480p": true, "360p": true}
	// Categories skipped by default; the player toggles per setting later.
	ytDefaultSkip = []string{"sponsor", "selfpromo", "interaction", "intro", "outro"}
)

func (h *ytHandlers) writeYTError(w http.ResponseWriter, err error) {
	var ye *youtube.Error
	switch {
	case errors.Is(err, youtube.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "youtube_disabled", "розділ YouTube не налаштовано")
	case errors.As(err, &ye):
		status := ye.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		writeError(w, status, ye.Code, ye.Message)
	default:
		writeInternal(w, err)
	}
}

func writeRawJSON(w http.ResponseWriter, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// account: GET /api/v1/yt/account → sign-in state (+ pending user code).
func (h *ytHandlers) account(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	a, err := h.yt.Account(r.Context(), info.User.ID)
	if err != nil {
		h.writeYTError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// login: POST /api/v1/yt/account/login → starts the TV device-code flow.
func (h *ytHandlers) login(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	a, err := h.yt.StartLogin(r.Context(), info.User.ID)
	if err != nil {
		h.writeYTError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// unlink: DELETE /api/v1/yt/account.
func (h *ytHandlers) unlink(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	if err := h.yt.Unlink(r.Context(), info.User.ID); err != nil {
		h.writeYTError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// browse: GET /api/v1/yt/browse/{page}?cont= — home|subscriptions|history|playlists|library|liked|watch_later|UC…|VL…
func (h *ytHandlers) browse(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	page := r.PathValue("page")
	if !ytPageID.MatchString(page) {
		writeBadRequest(w, "невірний page")
		return
	}
	f, err := h.yt.Browse(r.Context(), info.User.ID, page, r.URL.Query().Get("cont"))
	if err != nil {
		h.writeYTError(w, err)
		return
	}
	writeRawJSON(w, f)
}

// search: GET /api/v1/yt/search?q=&cont=
func (h *ytHandlers) search(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	cont := r.URL.Query().Get("cont")
	if q == "" && cont == "" {
		writeBadRequest(w, "параметр q обов'язковий")
		return
	}
	if len(q) > 200 {
		writeBadRequest(w, "q занадто довгий")
		return
	}
	f, err := h.yt.Search(r.Context(), info.User.ID, q, cont)
	if err != nil {
		h.writeYTError(w, err)
		return
	}
	writeRawJSON(w, f)
}

// video: GET /api/v1/yt/video/{id} → details + related.
func (h *ytHandlers) video(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	id := r.PathValue("id")
	if !ytVideoID.MatchString(id) {
		writeBadRequest(w, "невірний id")
		return
	}
	f, err := h.yt.Video(r.Context(), info.User.ID, id)
	if err != nil {
		h.writeYTError(w, err)
		return
	}
	writeRawJSON(w, f)
}

// play: GET /api/v1/yt/play/{id}?quality=1080p → {playlist_url, segments}.
// Submits a mux2 remux job over the sidecar's two track URLs; the player then
// streams /remux/<job>/playlist.m3u8 exactly like a torrent or online source.
func (h *ytHandlers) play(w http.ResponseWriter, r *http.Request) {
	info, _ := authFrom(r)
	id := r.PathValue("id")
	if !ytVideoID.MatchString(id) {
		writeBadRequest(w, "невірний id")
		return
	}
	if !h.yt.Enabled() {
		h.writeYTError(w, youtube.ErrDisabled)
		return
	}
	quality := r.URL.Query().Get("quality")
	if !ytQuality[quality] {
		quality = "1080p"
	}
	// Fail fast on an unlinked profile or an unplayable video instead of letting
	// ffmpeg discover it: the video call also warms the sidecar's player cache.
	if _, err := h.yt.Video(r.Context(), info.User.ID, id); err != nil {
		h.writeYTError(w, err)
		return
	}
	job, err := h.queue.SubmitMux2(
		h.yt.TrackURL(info.User.ID, id, "video", quality),
		h.yt.TrackURL(info.User.ID, id, "audio", quality),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "не вдалося запустити remux")
		return
	}
	cats := ytDefaultSkip
	if sb := r.URL.Query().Get("sb"); sb != "" {
		cats = strings.Split(sb, ",")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id":       job.ID,
		"playlist_url": "/remux/" + job.ID + "/playlist.m3u8",
		"quality":      quality,
		"segments":     h.yt.Segments(r.Context(), id, cats),
	})
}

// segments: GET /api/v1/yt/segments/{id}?cats=a,b → SponsorBlock spans.
func (h *ytHandlers) segments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ytVideoID.MatchString(id) {
		writeBadRequest(w, "невірний id")
		return
	}
	cats := ytDefaultSkip
	if c := r.URL.Query().Get("cats"); c != "" {
		cats = strings.Split(c, ",")
	}
	writeJSON(w, http.StatusOK, map[string]any{"segments": h.yt.Segments(r.Context(), id, cats)})
}
