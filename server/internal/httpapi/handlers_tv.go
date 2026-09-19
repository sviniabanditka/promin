package httpapi

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sviniabanditka/promin/server/internal/store"
	"github.com/sviniabanditka/promin/server/internal/tv"
)

// Live TV (docs/tv.md): channel lists from the iptv-org catalogue and a
// playable URL per channel. All routes need a session except the logo proxy
// (<img> cannot send a bearer; same rule as /img for TMDB posters).
type tvHandlers struct {
	svc      *tv.Service
	dataDir  string
	logoHTTP *http.Client
}

var tvChannelID = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,80}$`)

func (h *tvHandlers) enabled(w http.ResponseWriter) bool {
	if h.svc == nil || !h.svc.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "tv_disabled", "розділ ТВ вимкнено")
		return false
	}
	return true
}

// allowedCountries: the profile's TV country restriction (nil = none).
func allowedCountries(r *http.Request) []string {
	info, _ := authFrom(r)
	f := info.User.Features()
	if len(f.TVCountries) == 0 {
		return nil
	}
	return f.TVCountries
}

// channelAllowed: the channel's country is within the profile's restriction.
func (h *tvHandlers) channelAllowed(w http.ResponseWriter, r *http.Request, id string) bool {
	info, _ := authFrom(r)
	f := info.User.Features()
	if len(f.TVCountries) == 0 {
		return true
	}
	ch, err := h.svc.Repo().Channel(id)
	if err != nil || !f.AllowsCountry(ch.Country) {
		writeError(w, http.StatusForbidden, "country_blocked", "цю країну вимкнено для профілю")
		return false
	}
	return true
}

// meta: GET /api/v1/tv/meta → {countries:[{code,name,flag,channels}], categories:[{id,name,channels}]}
func (h *tvHandlers) meta(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	countries, cats, err := h.svc.Meta(allowedCountries(r))
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"countries": countries, "categories": cats})
}

// channels: GET /api/v1/tv/channels?country=UA&category=news&q=&fav=1&recent=1&limit=
func (h *tvHandlers) channels(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	info, _ := authFrom(r)
	q := r.URL.Query()
	f := store.TVFilter{
		// ?id= is how the Mini App's "open on TV" resolves one channel without
		// pulling the whole catalogue down to the phone.
		ID:        q.Get("id"),
		Country:   q.Get("country"),
		Countries: allowedCountries(r),
		Category:  q.Get("category"),
		Query:     q.Get("q"),
		UserID:    info.User.ID,
		FavOnly:   q.Get("fav") == "1",
		Recent:    q.Get("recent") == "1",
		Limit:     atoiDefault(q.Get("limit"), 0),
	}
	if len(f.Country) > 2 || len(f.Category) > 40 || len(f.Query) > 80 {
		writeBadRequest(w, "невірні параметри")
		return
	}
	if f.ID != "" && !tvChannelID.MatchString(f.ID) {
		writeBadRequest(w, "невірний id")
		return
	}
	items, err := h.svc.Channels(f)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// play: GET /api/v1/tv/channels/{id}/play → {url, direct, quality, streams}
func (h *tvHandlers) play(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	info, _ := authFrom(r)
	id := r.PathValue("id")
	if !tvChannelID.MatchString(id) {
		writeBadRequest(w, "невірний id")
		return
	}
	if !h.channelAllowed(w, r, id) {
		return
	}
	p, err := h.svc.Play(info.User.ID, id)
	if errors.Is(err, tv.ErrNoStream) || errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, "no_stream", "у каналу немає потоку")
		return
	}
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// report: POST /api/v1/tv/channels/{id}/fail — the player could not start the
// stream; marks it for the next liveness pass.
func (h *tvHandlers) report(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	id := r.PathValue("id")
	if !tvChannelID.MatchString(id) {
		writeBadRequest(w, "невірний id")
		return
	}
	h.svc.Report(id)
	w.WriteHeader(http.StatusNoContent)
}

// favorite: PUT/DELETE /api/v1/tv/favorites/{id}
func (h *tvHandlers) favorite(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.enabled(w) {
			return
		}
		info, _ := authFrom(r)
		id := r.PathValue("id")
		if !tvChannelID.MatchString(id) {
			writeBadRequest(w, "невірний id")
			return
		}
		if err := h.svc.Repo().SetFavorite(info.User.ID, id, on); err != nil {
			writeInternal(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// epg: GET /api/v1/tv/channels/{id}/epg → {items:[{start,stop,title,desc}]} (−12 h … +36 h)
func (h *tvHandlers) epg(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	id := r.PathValue("id")
	if !tvChannelID.MatchString(id) {
		writeBadRequest(w, "невірний id")
		return
	}
	items, err := h.svc.Guide(id)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// now: GET /api/v1/tv/now → {items:{<channel id>:{now:{...},next:{...}}}} for
// every channel that has a guide (the overlay's channel list).
func (h *tvHandlers) now(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	items, err := h.svc.NowNext()
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "at": time.Now().Unix()})
}

var _ = strconv.Itoa

// logo: GET /img/tv/{id} — the channel's logo through our disk cache. The
// iptv-org logos live on imgur / wikimedia, which refuse or throttle direct
// requests from TVs; we fetch once with a browser UA and keep the file under
// <data>/img/tv (swept with the rest of the image cache).
func (h *tvHandlers) logo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if h.svc == nil || !tvChannelID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	ch, err := h.svc.Repo().Channel(id)
	if err != nil || ch.Logo == "" {
		http.NotFound(w, r)
		return
	}
	// Keep the upstream extension so imageHeaders picks the right type.
	ext := strings.ToLower(filepath.Ext(strings.SplitN(ch.Logo, "?", 2)[0]))
	if ext == "" || len(ext) > 5 {
		ext = ".png"
	}
	file := id + ext
	diskPath := filepath.Join(h.dataDir, "img", "tv", file)
	if st, err := os.Stat(diskPath); err == nil && !st.IsDir() {
		serveImageFile(w, r, file, diskPath)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, ch.Logo, nil)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	req.Header.Set("User-Agent", relayBrowserUA)
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	resp, err := h.logoHTTP.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		http.NotFound(w, r)
		return
	}
	defer resp.Body.Close()
	if err := os.MkdirAll(filepath.Dir(diskPath), 0o755); err != nil {
		serveImageStream(w, file, resp.Body)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(diskPath), "tv-*")
	if err != nil {
		serveImageStream(w, file, resp.Body)
		return
	}
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 4<<20)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		http.NotFound(w, r)
		return
	}
	tmp.Close()
	_ = os.Rename(tmp.Name(), diskPath)
	serveImageFile(w, r, file, diskPath)
}
