package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/sviniabanditka/promin/server/internal/subtitles"
)

// External subtitles (OpenSubtitles) for the player: search by imdb id, then
// the file as WebVTT through our origin (media token, cached on disk).
type subtitlesHandlers struct {
	svc *subtitles.Client
}

// GET /api/v1/subtitles/search?imdb_id=&season=&episode=&langs=uk,ru,en
func (h *subtitlesHandlers) search(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil || !h.svc.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "results": []subtitles.Result{}})
		return
	}
	q := r.URL.Query()
	langs := []string{"uk", "ru", "en"}
	if l := strings.TrimSpace(q.Get("langs")); l != "" {
		langs = strings.Split(l, ",")
	}
	res, err := h.svc.Search(r.Context(), subtitles.Query{
		IMDbID: q.Get("imdb_id"), Season: atoiDefault(q.Get("season"), 0), Episode: atoiDefault(q.Get("episode"), 0), Langs: langs,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "subtitles_unavailable", "сервіс субтитрів недоступний")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "results": res})
}

// GET /api/v1/subtitles/{file_id}.vtt (requireAuthMedia: ?t=) → text/vtt
func (h *subtitlesHandlers) file(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil || !h.svc.Enabled() {
		writeNotFound(w, "subtitles_disabled", "субтитри вимкнено")
		return
	}
	id, err := strconv.ParseInt(strings.TrimSuffix(r.PathValue("file"), ".vtt"), 10, 64)
	if err != nil || id <= 0 {
		writeBadRequest(w, "невірний file_id")
		return
	}
	vtt, err := h.svc.VTT(r.Context(), id)
	if err != nil {
		if errors.Is(err, subtitles.ErrNotFound) {
			writeNotFound(w, "not_found", "файл не знайдено")
			return
		}
		writeError(w, http.StatusBadGateway, "subtitles_unavailable", "не вдалося завантажити субтитри")
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(vtt)
}
